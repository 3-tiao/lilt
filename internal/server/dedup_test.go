package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
)

func TestDedupSameRequestReusesResult(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	first, isNew, err := cache.begin("id-1", "fp")
	if err != nil || !isNew {
		t.Fatalf("begin = %v, isNew=%v", err, isNew)
	}
	second, isNew, err := cache.begin("id-1", "fp")
	if err != nil {
		t.Fatalf("second begin: %v", err)
	}
	if isNew {
		t.Fatal("second identical request reported as new")
	}
	if first != second {
		t.Fatal("second request did not reuse the in-flight entry")
	}
	cache.finish("id-1", api.Success("id-1", map[string]any{"v": 1}))
	select {
	case <-second.done:
	default:
		t.Fatal("entry was not released after finish")
	}
	result := cache.result(second)
	if !result.OK {
		t.Fatalf("cached result = %+v", result)
	}
}

func TestDedupDifferentFingerprintRejected(t *testing.T) {
	cache := newDedupCache(4, 0, time.Minute)
	if _, _, err := cache.begin("id-1", "fp-a"); err != nil {
		t.Fatal(err)
	}
	_, _, err := cache.begin("id-1", "fp-b")
	if err == nil || err.Code != api.CodeInvalidRequest {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}

func TestDedupEvictedBodyYieldsDuplicateResultUnavailable(t *testing.T) {
	cache := newDedupCache(1, 0, time.Minute)
	first, _, err := cache.begin("id-1", "fp")
	if err != nil {
		t.Fatal(err)
	}
	cache.finish("id-1", api.Success("id-1", map[string]any{"v": 1}))
	second, _, err := cache.begin("id-2", "fp")
	if err != nil {
		t.Fatal(err)
	}
	cache.finish("id-2", api.Success("id-2", map[string]any{"v": 2}))

	if result := cache.result(first); result.OK || result.Error.Code != api.CodeDuplicateResultUnavailable {
		t.Fatalf("evicted result = %+v, want duplicate_result_unavailable", result)
	}
	if result := cache.result(second); !result.OK {
		t.Fatalf("retained result = %+v", result)
	}
}

func TestFingerprintIgnoresKeyOrder(t *testing.T) {
	a := json.RawMessage(`{"b":1,"a":2}`)
	b := json.RawMessage(`{"a":2,"b":1}`)
	if fingerprint("cmd", normalizeParams(a)) != fingerprint("cmd", normalizeParams(b)) {
		t.Fatal("fingerprints differ for equivalent params")
	}
	if fingerprint("cmd", normalizeParams(a)) == fingerprint("other", normalizeParams(a)) {
		t.Fatal("fingerprints collide across commands")
	}
}

func TestDedupPruneClearsCompletedOrder(t *testing.T) {
	cache := newDedupCache(10, 0, time.Nanosecond)
	for _, id := range []string{"a", "b"} {
		if _, _, err := cache.begin(id, "fp"); err != nil {
			t.Fatal(err)
		}
		cache.finish(id, api.Success(id, map[string]any{}))
	}
	time.Sleep(2 * time.Millisecond)
	// begin() prunes expired tombstones; the completion-order slice must drop
	// them too or a later reuse can evict a fresh entry prematurely.
	if _, _, err := cache.begin("c", "fp"); err != nil {
		t.Fatal(err)
	}
	if len(cache.completed) != 0 {
		t.Fatalf("completed order retained %d expired ids", len(cache.completed))
	}
}

// The ledger's own bound must actually recycle entries at a realistic
// configuration, and a pending entry must survive the sweep: the in-flight
// request still needs its result.
func TestLedgerRecyclesCompletedEntriesAndKeepsPendingOnes(t *testing.T) {
	cache := newDedupCache(4, 3, 20*time.Millisecond)

	// Two completed requests plus one in-flight request fill the cap.
	for index := range 2 {
		id := fmt.Sprintf("done-%d", index)
		if _, isNew, err := cache.begin(id, "fp"); err != nil || !isNew {
			t.Fatalf("begin %s: isNew=%v err=%v", id, isNew, err)
		}
		cache.finish(id, api.Success(id, map[string]any{}))
	}
	if _, isNew, err := cache.begin("pending", "fp"); err != nil || !isNew {
		t.Fatalf("begin pending: isNew=%v err=%v", isNew, err)
	}
	if _, _, err := cache.begin("overflow", "fp"); err == nil || err.Code != api.CodeServerBusy {
		t.Fatalf("begin on a full ledger = %+v, want server_busy", err)
	}

	// Age the completed entries past the tombstone window: the next admission
	// sweeps them, keeps the pending one, and therefore has room again.
	time.Sleep(40 * time.Millisecond)
	if _, isNew, err := cache.begin("after-sweep", "fp"); err != nil || !isNew {
		t.Fatalf("begin after the sweep: isNew=%v err=%v", isNew, err)
	}
	cache.mu.Lock()
	size := len(cache.entries)
	_, pendingAlive := cache.entries["pending"]
	_, completedAlive := cache.entries["done-0"]
	cache.mu.Unlock()
	if !pendingAlive {
		t.Fatal("the sweep recycled an in-flight entry")
	}
	if completedAlive {
		t.Fatal("a completed entry older than the tombstone window was not recycled")
	}
	if size > 3 {
		t.Fatalf("ledger size after recycling = %d, want at most the cap", size)
	}
	// Recycling is a window, not a guarantee: outside it the same requestId may
	// execute again, which is exactly what the design claims.
	if _, isNew, err := cache.begin("done-0", "fp"); err != nil || !isNew {
		t.Fatalf("recycled id: isNew=%v err=%v", isNew, err)
	}
}

// A full ledger keeps verification available: a query runs without registering
// anything, and a mutation is rejected before it can be executed.
func TestFullLedgerQueryPathDoesNotGrowTheLedger(t *testing.T) {
	cache := newDedupCache(4, 1, time.Minute)
	if _, isNew, err := cache.begin("only", "fp"); err != nil || !isNew {
		t.Fatalf("begin: isNew=%v err=%v", isNew, err)
	}
	cache.finish("only", api.Success("only", map[string]any{}))

	entry, isNew, deduped, err := cache.beginQuery("query", "fp")
	if err != nil {
		t.Fatalf("beginQuery on a full ledger: %v", err)
	}
	if deduped || isNew || entry != nil {
		t.Fatalf("query on a full ledger = entry:%v isNew:%v deduped:%v, want an uncached run", entry, isNew, deduped)
	}
	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size != 1 {
		t.Fatalf("ledger size after the uncached query = %d, want 1", size)
	}
}
