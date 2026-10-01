package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
)

// waitFor polls a condition with a hard deadline. It never guesses an order by
// sleeping: the condition itself is the observation, the deadline only fails a
// stuck test.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The gate hands the slot to waiters in admission order, and a request that
// gives up while queued never receives it.
func TestAdmissionGateIsFIFOAndBounded(t *testing.T) {
	gate := newAdmissionGate()
	if !gate.acquire(time.Second, nil) {
		t.Fatal("first acquire failed on a free slot")
	}

	// Queue three waiters deterministically, then observe the order the gate
	// hands the slot to them. The queue itself is what defines FIFO, so the test
	// controls the enqueue order instead of racing three goroutines.
	queued := []chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)}
	gate.mu.Lock()
	gate.waiters = append(gate.waiters, queued...)
	gate.mu.Unlock()
	admitted := make(chan int, 3)
	for index, ready := range queued {
		go func(index int, ready chan struct{}) {
			<-ready
			admitted <- index
		}(index, ready)
	}

	gate.release()
	if got := <-admitted; got != 0 {
		t.Fatalf("first admitted = %d, want 0", got)
	}
	gate.release()
	if got := <-admitted; got != 1 {
		t.Fatalf("second admitted = %d, want 1", got)
	}

	// A waiter that exceeds its budget removes itself and never receives the
	// slot; the waiters queued ahead of it are unaffected.
	if gate.acquire(20*time.Millisecond, nil) {
		t.Fatal("acquire succeeded while the slot was held")
	}
	if gate.pendingWaiters() != 1 {
		t.Fatalf("waiters after the timeout = %d, want the remaining 1", gate.pendingWaiters())
	}
	gate.release()
	if got := <-admitted; got != 2 {
		t.Fatalf("last queued waiter admitted = %d, want 2", got)
	}
	gate.release()
	if !gate.acquire(time.Second, nil) {
		t.Fatal("slot was lost after every waiter finished")
	}
	gate.release()
}

// Mutations execute in admission order, and a request that cannot be admitted
// inside the budget is reported as server_busy without ever running.
func TestAdmissionOrderIsFIFOAndBusyIsNotCached(t *testing.T) {
	server, socket := startTestServer(t)
	// Warm the epoch before the slot is held: the bootstrap query itself needs
	// the server lock, which the blocked mutation owns for its whole handler.
	epoch := epochOf(t, socket)
	// The catalog default admission budget applies here: the queued request must
	// wait for the slot rather than be rejected.
	server.admissionWait = 5 * time.Second

	var mu sync.Mutex
	var executed []string
	entered := make(chan string, 4)
	release := make(chan struct{})
	server.registry.Bind("ui.set", func(_ context.Context, raw json.RawMessage) (any, *api.Error) {
		var params struct {
			Theme string `json:"theme"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, api.Errorf(api.CodeInvalidRequest, "bad params")
		}
		mu.Lock()
		executed = append(executed, params.Theme)
		mu.Unlock()
		entered <- params.Theme
		if params.Theme == "first" {
			<-release
		}
		return map[string]any{}, nil
	})

	send := func(theme string, out chan<- api.Response) {
		params, _ := json.Marshal(map[string]string{"theme": theme})
		response, err := api.Call(context.Background(), socket, api.Request{
			RequestID: "order-" + theme, Command: "ui.set", Params: params,
			IfServerInstanceID: epoch,
		})
		if err != nil {
			t.Errorf("ui.set %s: %v", theme, err)
		}
		out <- response
	}

	response := make(chan api.Response, 1)
	go send("first", response)
	if got := <-entered; got != "first" {
		t.Fatalf("first execution = %q", got)
	}

	secondResponse := make(chan api.Response, 1)
	go send("second", secondResponse)
	waitFor(t, "second request queued behind the slot", func() bool { return server.admission.pendingWaiters() == 1 })
	mu.Lock()
	queued := append([]string(nil), executed...)
	mu.Unlock()
	if len(queued) != 1 || queued[0] != "first" {
		t.Fatalf("executed before release = %v, want only first", queued)
	}

	close(release)
	if resp := <-response; !resp.OK {
		t.Fatalf("first: %+v", resp.Error)
	}
	if resp := <-secondResponse; !resp.OK {
		t.Fatalf("second: %+v", resp.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 2 || executed[0] != "first" || executed[1] != "second" {
		t.Fatalf("execution order = %v, want [first second]", executed)
	}
}

// A request that waits past its admission budget runs neither now nor later:
// the rejection is transient and must not be cached, so the same requestId is
// admitted again once the slot is free.
func TestAdmissionTimeoutReportsServerBusyAndIsNotCached(t *testing.T) {
	server, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	server.admissionWait = 30 * time.Millisecond

	release := make(chan struct{})
	closeRelease := sync.OnceFunc(func() { close(release) })
	// The fixture must never block a cleanup forever, or a failing assertion
	// would hang Close behind the held mutation slot.
	t.Cleanup(closeRelease)
	entered := make(chan struct{}, 1)
	server.registry.Bind("ui.set", func(_ context.Context, raw json.RawMessage) (any, *api.Error) {
		entered <- struct{}{}
		<-release
		return map[string]any{}, nil
	})

	send := func(theme, requestID string, out chan<- api.Response) {
		params, _ := json.Marshal(map[string]string{"theme": theme})
		response, err := api.Call(context.Background(), socket, api.Request{
			RequestID: requestID, Command: "ui.set", Params: params,
			IfServerInstanceID: epoch,
		})
		if err != nil {
			t.Errorf("ui.set %s: %v", theme, err)
		}
		out <- response
	}

	firstResponse := make(chan api.Response, 1)
	go send("held", "busy-held", firstResponse)
	<-entered

	busy := make(chan api.Response, 1)
	go send("rejected", "busy-rejected", busy)
	var rejected api.Response
	select {
	case rejected = <-busy:
	case <-time.After(3 * time.Second):
		t.Fatal("queued mutation never returned from its bounded wait")
	}
	if rejected.Error == nil || rejected.Error.Code != api.CodeServerBusy {
		t.Fatalf("queued mutation = %+v, want server_busy", rejected.Error)
	}

	closeRelease()
	if resp := <-firstResponse; !resp.OK {
		t.Fatalf("held mutation: %+v", resp.Error)
	}

	// Same requestId and params: because the server_busy rejection was not
	// cached as an outcome, this is admitted and executes.
	server.registry.Bind("ui.set", func(_ context.Context, raw json.RawMessage) (any, *api.Error) {
		return map[string]any{}, nil
	})
	retry := make(chan api.Response, 1)
	go send("rejected", "busy-rejected", retry)
	select {
	case resp := <-retry:
		if !resp.OK {
			t.Fatalf("retry after server_busy: %+v", resp.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retry after server_busy never returned")
	}
}

// The ledger itself is bounded: when it is full a new mutation is rejected with
// server_busy instead of evicting a tombstone that still protects an executed
// request, and read-only verification stays available.
func TestFullLedgerRejectsMutationsButNotQueries(t *testing.T) {
	server, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	server.dedup = newDedupCache(4, 1, time.Minute)

	mutate := func(theme, requestID string) api.Response {
		params, _ := json.Marshal(map[string]string{"theme": theme})
		return rawCall(t, socket, api.Request{
			RequestID: requestID, Command: "ui.set", Params: params,
			IfServerInstanceID: epoch,
		})
	}
	if first := mutate("one", "ledger-first"); !first.OK {
		t.Fatalf("first mutation: %+v", first.Error)
	}
	second := mutate("two", "ledger-second")
	if second.Error == nil || second.Error.Code != api.CodeServerBusy {
		t.Fatalf("second mutation with a full ledger = %+v, want server_busy", second.Error)
	}
	if query := rawCall(t, socket, api.Request{Command: "session.status"}); !query.OK {
		t.Fatalf("query with a full ledger: %+v", query.Error)
	}
}

// A pure query must never release the mutation slot: it never took it. Before
// the fix, every non-concurrent query ran `release` anyway, which freed a slot
// another command was still using.
func TestQueryDoesNotReleaseTheMutationSlot(t *testing.T) {
	server, _ := startTestServer(t)
	if !server.admission.acquire(time.Second, nil) {
		t.Fatal("could not take the slot")
	}
	// The query completes while the slot is held (the gate is not about s.mu).
	if response := server.dispatch(api.Request{RequestID: "query", Command: "state.get"}); !response.OK {
		t.Fatalf("state.get: %+v", response.Error)
	}
	if server.admission.acquire(20*time.Millisecond, nil) {
		t.Fatal("a query released a mutation slot it never acquired")
	}
	server.admission.release()
	if !server.admission.acquire(time.Second, nil) {
		t.Fatal("the slot was lost after the real holder released it")
	}
	server.admission.release()
}

// A waiter that gives up must not orphan a slot that was handed to it: the
// token is either drained here or passed on to the next waiter.
func TestAbandonedHandoffDoesNotOrphanTheSlot(t *testing.T) {
	gate := newAdmissionGate()
	if !gate.acquire(time.Second, nil) {
		t.Fatal("could not take the slot")
	}
	ready := make(chan struct{}, 1)
	gate.mu.Lock()
	gate.waiters = append(gate.waiters, ready)
	gate.mu.Unlock()
	gate.release() // pops and hands the slot to `ready`
	if gate.cancelWait(ready) {
		t.Fatal("cancelWait reported success for a waiter that was handed the slot")
	}
	if gate.pendingWaiters() != 0 {
		t.Fatalf("waiters = %d, want none", gate.pendingWaiters())
	}
	// The abandoned handoff freed the slot: the token left in the discarded
	// channel is unread, and a new request can take the slot instead of waiting
	// forever for a handoff nobody will ever release.
	if !gate.acquire(20*time.Millisecond, nil) {
		t.Fatal("the slot was orphaned by an abandoned handoff")
	}
	gate.release()
	if !gate.acquire(time.Second, nil) {
		t.Fatal("the slot was lost after the freed handoff")
	}
	gate.release()
}

// Race probe: releasing while a waiter times out must never lose the slot or
// leave two holders, whatever the interleaving.
func TestAdmissionReleaseAndTimeoutRaceKeepsSlotAccounted(t *testing.T) {
	for round := range 500 {
		gate := newAdmissionGate()
		if !gate.acquire(time.Second, nil) {
			t.Fatal("could not take the slot")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Either it is admitted (then it must release) or it times out.
			if gate.acquire(50*time.Microsecond, nil) {
				gate.release()
			}
		}()
		go func() {
			defer wg.Done()
			gate.release()
		}()
		wg.Wait()
		waitFor(t, "the gate to settle", func() bool { return gate.pendingWaiters() == 0 })
		if !gate.acquire(time.Second, nil) {
			t.Fatalf("round %d orphaned the slot", round)
		}
		gate.release()
	}
}

// The ledger is process-local in-memory state: a duplicate waiting on a request
// that is rejected before it runs must be released with that rejection, and the
// requestId must be admissible again afterwards.
func TestAbortReleasesCoalescedWaitersAndFreesTheRequestID(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	entry, isNew, err := cache.begin("busy-id", "fingerprint")
	if err != nil || !isNew {
		t.Fatalf("begin: isNew=%v err=%v", isNew, err)
	}
	waiter := make(chan api.Response, 1)
	go func() {
		<-entry.done
		waiter <- cache.result(entry)
	}()
	busy := api.Failure("busy-id", api.Errorf(api.CodeServerBusy, "did not run"))
	cache.abort("busy-id", busy)
	select {
	case got := <-waiter:
		if got.Error == nil || got.Error.Code != api.CodeServerBusy {
			t.Fatalf("coalesced waiter got %+v, want server_busy", got.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("abort left a coalesced duplicate waiting forever")
	}
	if _, isNew, err = cache.begin("busy-id", "fingerprint"); err != nil || !isNew {
		t.Fatalf("after abort: isNew=%v err=%v, want the id admissible again", isNew, err)
	}
}

// A mutation that queued before the shutdown barrier must not execute after it.
// The barrier is taken when shutdown is accepted, and admission is where a
// request that had not yet linearized finds out.
func TestDrainingBarrierRejectsAQueuedMutation(t *testing.T) {
	server, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	server.admissionWait = 5 * time.Second

	var mu sync.Mutex
	executed := []string{}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server.registry.Bind("ui.set", func(_ context.Context, raw json.RawMessage) (any, *api.Error) {
		var params struct {
			Theme string `json:"theme"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, api.Errorf(api.CodeInvalidRequest, "bad params")
		}
		mu.Lock()
		executed = append(executed, params.Theme)
		mu.Unlock()
		if params.Theme == "holder" {
			entered <- struct{}{}
			<-release
		}
		return map[string]any{}, nil
	})

	send := func(theme, requestID string, out chan<- api.Response) {
		params, _ := json.Marshal(map[string]string{"theme": theme})
		response, err := api.Call(context.Background(), socket, api.Request{
			RequestID: requestID, Command: "ui.set", Params: params,
			IfServerInstanceID: epoch,
		})
		if err != nil {
			t.Errorf("ui.set %s: %v", theme, err)
		}
		out <- response
	}

	holder := make(chan api.Response, 1)
	go send("holder", "barrier-holder", holder)
	<-entered

	queued := make(chan api.Response, 1)
	go send("queued", "barrier-queued", queued)
	waitFor(t, "the second mutation to queue", func() bool { return server.admission.pendingWaiters() == 1 })

	// Shutdown is accepted while the queued request is still in the gate.
	server.draining.Store(true)
	close(release)

	if response := <-holder; !response.OK {
		t.Fatalf("holder: %+v", response.Error)
	}
	select {
	case response := <-queued:
		if response.Error == nil || response.Error.Code != api.CodeSessionUnavailable {
			t.Fatalf("queued mutation = %+v, want session_unavailable", response.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued mutation never returned")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 1 || executed[0] != "holder" {
		t.Fatalf("executed = %v, want only the holder", executed)
	}
}

// The admission budget comes from the catalog, and shutdown's is derived to
// outlast the longest command it may have to drain, so `lilt quit` behind a long
// play waits instead of reporting server_busy after the ordinary 5s. The
// AdmissionWait seam only shortens ordinary budgets.
func TestAdmissionWaitComesFromTheCatalog(t *testing.T) {
	server, _ := startTestServer(t)
	if got, want := server.admissionWaitFor("ui.set"), api.DefaultAdmission(); got != want {
		t.Fatalf("ordinary mutation admission = %s, want %s", got, want)
	}
	if got := server.admissionWaitFor("state.get"); got != 0 {
		t.Fatalf("query admission = %s, want 0", got)
	}
	if got := server.admissionWaitFor("session.shutdown"); got <= server.admissionWaitFor("ui.set") {
		t.Fatalf("shutdown admission = %s, want more than the ordinary budget", got)
	}
	server.admissionWait = 25 * time.Millisecond
	if got := server.admissionWaitFor("ui.set"); got != 25*time.Millisecond {
		t.Fatalf("seam did not shorten the ordinary budget: %s", got)
	}
	if got := server.admissionWaitFor("session.shutdown"); got == 25*time.Millisecond {
		t.Fatal("the test seam also shortened the derived shutdown budget")
	}
}

// abort and finish must not be able to double-close an entry's done channel, and
// a completed entry must never be aborted: it owns the dedup guarantee.
func TestAbortAndFinishDoNotDoubleClose(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	entry, isNew, err := cache.begin("finish-then-abort", "fingerprint")
	if err != nil || !isNew {
		t.Fatalf("begin: isNew=%v err=%v", isNew, err)
	}
	completed := api.Success("finish-then-abort", map[string]any{"ok": true})
	cache.finish("finish-then-abort", completed)
	cache.abort("finish-then-abort", api.Failure("finish-then-abort", api.Errorf(api.CodeServerBusy, "nope")))
	if got := cache.result(entry); !got.OK {
		t.Fatalf("a completed entry was aborted: %+v", got.Error)
	}
	if _, isNew, err = cache.begin("finish-then-abort", "fingerprint"); err != nil || isNew {
		t.Fatalf("completed entry lost its dedup guarantee: isNew=%v err=%v", isNew, err)
	}

	// abort then finish: the aborted entry is gone, so finish only has to be
	// harmless (no panic, no resurrected entry).
	_, isNew, err = cache.begin("abort-then-finish", "fingerprint")
	if err != nil || !isNew {
		t.Fatalf("begin 2: isNew=%v err=%v", isNew, err)
	}
	cache.abort("abort-then-finish", api.Failure("abort-then-finish", api.Errorf(api.CodeServerBusy, "busy")))
	cache.finish("abort-then-finish", api.Success("abort-then-finish", map[string]any{}))
	if _, isNew, err = cache.begin("abort-then-finish", "fingerprint"); err != nil || !isNew {
		t.Fatalf("aborted entry was resurrected by finish: isNew=%v err=%v", isNew, err)
	}
}

// The barrier is taken by the accepted shutdown itself, while it owns the
// mutation slot: work queued behind it is rejected, work queued ahead of it runs.
func TestAcceptedShutdownBarrierRejectsQueuedWork(t *testing.T) {
	server, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	server.admissionWait = 5 * time.Second

	var mu sync.Mutex
	executed := []string{}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	closeRelease := sync.OnceFunc(func() { close(release) })
	t.Cleanup(closeRelease)
	server.registry.Bind("ui.set", func(_ context.Context, raw json.RawMessage) (any, *api.Error) {
		var params struct {
			Theme string `json:"theme"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, api.Errorf(api.CodeInvalidRequest, "bad params")
		}
		mu.Lock()
		executed = append(executed, params.Theme)
		mu.Unlock()
		if params.Theme == "holder" {
			entered <- struct{}{}
			<-release
		}
		return map[string]any{}, nil
	})

	send := func(command, requestID string, out chan<- api.Response) {
		var params json.RawMessage
		if command == "ui.set" {
			params = json.RawMessage(`{"theme":"queued"}`)
		}
		response, err := api.Call(context.Background(), socket, api.Request{
			RequestID: requestID, Command: command, Params: params,
			IfServerInstanceID: epoch,
		})
		if err != nil {
			t.Errorf("%s: %v", command, err)
		}
		out <- response
	}

	holder := make(chan api.Response, 1)
	go func() {
		response, err := api.Call(context.Background(), socket, api.Request{
			RequestID: "barrier-holder", Command: "ui.set", Params: json.RawMessage(`{"theme":"holder"}`),
			IfServerInstanceID: epoch,
		})
		if err != nil {
			t.Errorf("holder: %v", err)
		}
		holder <- response
	}()
	<-entered

	shutdown := make(chan api.Response, 1)
	go send("session.shutdown", "barrier-shutdown", shutdown)
	waitFor(t, "shutdown to queue", func() bool { return server.admission.pendingWaiters() == 1 })
	queued := make(chan api.Response, 1)
	go send("ui.set", "barrier-queued", queued)
	waitFor(t, "the mutation to queue behind shutdown", func() bool { return server.admission.pendingWaiters() == 2 })

	closeRelease()
	if response := <-holder; !response.OK {
		t.Fatalf("holder: %+v", response.Error)
	}
	if response := <-shutdown; !response.OK {
		t.Fatalf("shutdown: %+v", response.Error)
	}
	select {
	case response := <-queued:
		if response.Error == nil || response.Error.Code != api.CodeSessionUnavailable {
			t.Fatalf("work queued behind the barrier = %+v, want session_unavailable", response.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued work never returned after the barrier")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 1 || executed[0] != "holder" {
		t.Fatalf("executed = %v, want only the pre-barrier holder", executed)
	}
}

// The barrier is taken inside dispatch, while the accepted shutdown still owns
// the mutation slot. Taking it only later (in prepareShutdown, after the slot was
// released) leaves a window where a request woken by that release runs.
func TestAcceptedShutdownTakesTheBarrierInsideDispatch(t *testing.T) {
	server, _ := startTestServer(t)
	if server.draining.Load() {
		t.Fatal("fixture already draining")
	}
	response := server.dispatch(api.Request{RequestID: "inside-dispatch", Command: "session.shutdown"})
	if !response.OK {
		t.Fatalf("shutdown: %+v", response.Error)
	}
	if !server.draining.Load() {
		t.Fatal("an accepted shutdown returned from dispatch without taking the barrier")
	}
}
