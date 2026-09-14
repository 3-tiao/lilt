package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
)

func TestHelperProtocolModelsEncodePlaybackAndPreview(t *testing.T) {
	request, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 7, Method: "play", Params: core.PlaybackRequest{Kind: "song", ID: "song:42", URL: "https://music.apple.com/us/song/example/42"}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["method"] != "play" || decoded["id"].(float64) != 7 {
		t.Fatalf("unexpected protocol request: %s", request)
	}
	state := core.PlaybackState{Mode: "preview", Authorization: "denied", Status: "playing"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !strings.Contains(string(data), `"mode":"preview"`) {
		t.Fatalf("mode absent: %s", data)
	}
}

func TestPlaybackRequestReverseWireFormat(t *testing.T) {
	withReverse, err := json.Marshal(core.PlaybackRequest{Kind: "playlist", ID: "p1", Reverse: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withReverse), `"reverse":true`) {
		t.Fatalf("reverse absent: %s", withReverse)
	}
	withoutReverse, err := json.Marshal(core.PlaybackRequest{Kind: "playlist", ID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutReverse), `"reverse"`) {
		t.Fatalf("reverse should be omitted: %s", withoutReverse)
	}
}

func TestStreamClientMultiplexesResponsesAndNotifications(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := newStreamClient(clientConn)
	t.Cleanup(func() { _ = client.close(); _ = serverConn.Close() })

	type callResult struct {
		value string
		err   error
	}
	results := make(chan callResult, 2)
	var started sync.WaitGroup
	started.Add(2)
	for _, method := range []string{"first", "second"} {
		method := method
		go func() {
			started.Done()
			var value string
			err := client.call(context.Background(), method, nil, &value)
			results <- callResult{value: value, err: err}
		}()
	}
	started.Wait()

	decoder := json.NewDecoder(serverConn)
	requests := make(map[string]uint64)
	for range 2 {
		var request rpcRequest
		if err := decoder.Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests[request.Method] = request.ID
	}
	encoder := json.NewEncoder(serverConn)
	messages := []any{
		map[string]any{"jsonrpc": "2.0", "method": "unknownEvent", "params": map[string]any{"ignored": true}},
		map[string]any{"jsonrpc": "2.0", "method": "stateChanged", "params": map[string]any{"sequence": 4, "state": core.PlaybackState{Status: "playing", Position: 1}}},
		map[string]any{"jsonrpc": "2.0", "id": requests["second"], "result": "second-result"},
		map[string]any{"jsonrpc": "2.0", "method": "stateChanged", "params": map[string]any{"sequence": 5, "state": core.PlaybackState{Status: "paused", Position: 2}}},
		map[string]any{"jsonrpc": "2.0", "id": requests["first"], "result": "first-result"},
	}
	for _, message := range messages {
		if err := encoder.Encode(message); err != nil {
			t.Fatal(err)
		}
	}

	gotResults := map[string]bool{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		gotResults[result.value] = true
	}
	if !gotResults["first-result"] || !gotResults["second-result"] {
		t.Fatalf("mismatched concurrent responses: %#v", gotResults)
	}
	for _, want := range []uint64{4, 5} {
		select {
		case update := <-client.updates:
			if update.Sequence != want {
				t.Fatalf("notification sequence = %d, want %d", update.Sequence, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for notification %d", want)
		}
	}
}

func TestSlowNotificationConsumerDoesNotBlockRPCResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := newStreamClient(clientConn)
	t.Cleanup(func() { _ = client.close(); _ = serverConn.Close() })

	const notificationCount = 64
	response := make(chan error, 1)
	go func() {
		var result string
		err := client.call(context.Background(), "whileNotificationsPending", nil, &result)
		if err == nil && result != "complete" {
			err = fmt.Errorf("result = %q, want complete", result)
		}
		response <- err
	}()

	decoder := json.NewDecoder(serverConn)
	var request rpcRequest
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		encoder := json.NewEncoder(serverConn)
		for sequence := 1; sequence <= notificationCount; sequence++ {
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  "stateChanged",
				"params": map[string]any{
					"sequence": sequence,
					"state":    core.PlaybackState{Status: "playing", Position: float64(sequence)},
				},
			}); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "complete"})
	}()

	// Intentionally do not receive from client.updates until the response has
	// crossed the same socket reader as every preceding notification.
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC response was blocked by the unread notification stream")
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	for want := uint64(1); want <= notificationCount; want++ {
		select {
		case update := <-client.updates:
			if update.Sequence != want {
				t.Fatalf("notification sequence = %d, want %d", update.Sequence, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for notification %d", want)
		}
	}
}

func TestBlockingCallTimeoutInvalidatesTransportAndRejectsLateResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := newStreamClient(clientConn)
	t.Cleanup(func() { _ = client.close(); _ = serverConn.Close() })

	requestRead := make(chan rpcRequest, 1)
	serverWrite := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(serverConn)
		var request rpcRequest
		if err := decoder.Decode(&request); err != nil {
			serverWrite <- err
			return
		}
		requestRead <- request
		// Deterministically remain blocked past the caller deadline, then try
		// to deliver the response on the transport that must have been closed.
		time.Sleep(100 * time.Millisecond)
		serverWrite <- json.NewEncoder(serverConn).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "late"})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var result string
	err := client.call(ctx, "blocking", nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "quit and restart") {
		t.Fatalf("timeout error = %v", err)
	}
	<-requestRead
	started := time.Now()
	err = client.call(context.Background(), "after-timeout", nil, &result)
	if err == nil || !strings.Contains(err.Error(), "quit and restart") {
		t.Fatalf("subsequent call error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("subsequent call took %v on unusable transport", elapsed)
	}
	if err := <-serverWrite; err == nil {
		t.Fatal("late response was accepted on timed-out transport")
	}
	if result != "" {
		t.Fatalf("late result was accepted: %q", result)
	}
}

func TestSubscribeStateAPIs(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := &Client{rpc: newStreamClient(clientConn)}
	t.Cleanup(func() { _ = client.rpc.close(); _ = serverConn.Close() })
	go func() {
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		var request rpcRequest
		_ = decoder.Decode(&request)
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"sequence": 8, "state": core.PlaybackState{Status: "paused"}}})
		_ = decoder.Decode(&request)
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
	}()
	subscription, err := client.SubscribeState(context.Background())
	if err != nil || subscription.Initial.Sequence != 8 || subscription.Initial.State.Status != "paused" || subscription.Updates == nil {
		t.Fatalf("subscription = %#v, %v", subscription.Initial, err)
	}
	if err := client.UnsubscribeState(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestContentMethods(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	client := &Client{rpc: newStreamClient(clientConn)}
	t.Cleanup(func() { _ = client.rpc.close() })
	go func() {
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for _, expected := range []struct {
			method string
			params map[string]any
			result any
		}{
			{"recentPlayed", map[string]any{"limit": float64(12)}, []core.Item{{Kind: "song", ID: "1", Title: "Recent"}}},
			{"stations", map[string]any{"term": "radio", "limit": float64(8)}, []core.Item{{Kind: "station", ID: "2", Title: "Station"}}},
			{"resolveUrl", map[string]any{"url": "https://music.apple.com/us/song/example/3"}, []core.Item{{Kind: "song", ID: "3", Title: "Resolved"}}},
		} {
			var request rpcRequest
			if err := decoder.Decode(&request); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			if request.Method != expected.method || !mapsEqual(request.Params.(map[string]any), expected.params) {
				t.Errorf("request = %#v, want %s %#v", request, expected.method, expected.params)
				return
			}
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": expected.result}); err != nil {
				t.Errorf("encode response: %v", err)
				return
			}
		}
	}()
	ctx := context.Background()
	recent, err := client.RecentPlayed(ctx, 12)
	if err != nil || len(recent) != 1 || recent[0].Title != "Recent" {
		t.Fatalf("recent = %#v, %v", recent, err)
	}
	stations, err := client.Stations(ctx, "radio", 8)
	if err != nil || len(stations) != 1 || stations[0].Title != "Station" {
		t.Fatalf("stations = %#v, %v", stations, err)
	}
	resolved, err := client.ResolveURL(ctx, "https://music.apple.com/us/song/example/3")
	if err != nil || resolved.Title != "Resolved" {
		t.Fatalf("resolved = %#v, %v", resolved, err)
	}
}

func mapsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func TestUnixSocketStartupCallAndShutdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-rpc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rpc.sock")
	processDone := make(chan error, 1)
	serverDone := make(chan struct{})
	go fakeUnixRPCServer(t, path, processDone, serverDone)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rpc, err := dialUnix(ctx, path, processDone)
	if err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	client := &Client{rpc: rpc, wait: wait, stderr: strings.NewReader(""), runtimeDir: dir}
	go func() {
		<-serverDone
		wait <- nil
	}()

	var hello helloResult
	if err := client.Call(ctx, "ping", nil, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.PID != 4242 {
		t.Fatalf("PID = %d, want 4242", hello.PID)
	}
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "preview" || state.Authorization != "denied" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if err := client.Next(ctx); err == nil {
		t.Fatal("next unexpectedly succeeded")
	} else {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != "preview_unsupported" {
			t.Fatalf("RPC error = %#v, want preview_unsupported", err)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("private runtime directory remains after close: %v", err)
	}
}

func fakeUnixRPCServer(t *testing.T, path string, processDone chan<- error, done chan<- struct{}) {
	t.Helper()
	// Delay creation to exercise readiness polling rather than only dialing an
	// already-listening socket.
	time.Sleep(40 * time.Millisecond)
	listener, err := net.Listen("unix", path)
	if err != nil {
		processDone <- err
		return
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		processDone <- err
		return
	}
	defer listener.Close()
	conn, err := listener.Accept()
	if err != nil {
		processDone <- err
		return
	}
	defer conn.Close()
	decoder := json.NewDecoder(bufio.NewReader(conn))
	encoder := json.NewEncoder(conn)
	var expectedID uint64 = 1
	for {
		var request rpcRequest
		if err := decoder.Decode(&request); err != nil {
			processDone <- err
			return
		}
		if request.ID != expectedID {
			t.Errorf("request ID = %d, want %d", request.ID, expectedID)
		}
		expectedID++
		var result any = map[string]any{}
		switch request.Method {
		case "ping":
			result = map[string]int{"pid": 4242}
		case "state":
			result = core.PlaybackState{Status: "paused", Mode: "preview", Authorization: "denied"}
		case "next":
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": RPCError{Code: "preview_unsupported", Message: "preview has no queue"}}); err != nil {
				processDone <- err
				return
			}
			continue
		case "shutdown":
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				processDone <- err
				return
			}
			close(done)
			return
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			processDone <- err
			return
		}
	}
}
