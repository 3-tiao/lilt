package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherCloseAfterCancellationIsIdempotent(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	_, cancel := context.WithCancel(context.Background())
	watcher := &Watcher{conn: client, cancel: cancel}
	cancel() // The watch read goroutine may already have closed this connection.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := watcher.Close(); err != nil {
			t.Fatalf("Close on an already closed watcher: %v", err)
		}
	}
}

func TestCallRoundTrip(t *testing.T) {
	socket, listener := listenUnix(t)
	seen := make(chan Request, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			return
		}
		seen <- request
		_ = json.NewEncoder(conn).Encode(Success(request.RequestID, map[string]any{"echo": request.Command}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := Command(ctx, socket, "session.status", nil)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !response.OK {
		t.Fatalf("response = %+v", response)
	}
	request := <-seen
	if request.RequestID == "" {
		t.Fatal("requestId was not generated")
	}
}

func TestCallNoActiveSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Command(ctx, filepath.Join(t.TempDir(), "absent.sock"), "session.status", nil)
	if !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("err = %v, want ErrNoActiveSession", err)
	}
}

func TestWatchStream(t *testing.T) {
	socket, listener := listenUnix(t)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			return
		}
		encoder := json.NewEncoder(conn)
		_ = encoder.Encode(Success(request.RequestID, WatchSnapshot{Sequence: 1}))
		_ = encoder.Encode(Event{Event: "playback.changed", Sequence: 2, Data: json.RawMessage(`{"state":{}}`)})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, watcher, err := Watch(ctx, socket, nil, true)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !response.OK {
		t.Fatalf("watch initial response = %+v", response)
	}
	defer watcher.Close()
	select {
	case event := <-watcher.Events:
		if event.Event != "playback.changed" || event.Sequence != 2 {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestWatchEarlyCancel(t *testing.T) {
	socket, listener := listenUnix(t)
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request Request
		if json.NewDecoder(conn).Decode(&request) != nil {
			return
		}
		close(accepted)
		// No initial snapshot: wait until the caller closes the connection.
		var next Request
		_ = json.NewDecoder(conn).Decode(&next)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, _, err := Watch(ctx, socket, nil, true)
		finished <- err
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("watch request never arrived")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Watch cancel = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch did not return after cancellation")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("watch connection stayed open after cancellation")
	}
}

func listenUnix(t *testing.T) (string, net.Listener) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "session.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return socket, listener
}
