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
