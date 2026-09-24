package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Transport-level sentinel errors. Callers map these to no_active_session or
// session_unavailable; they are not stable Client API error codes.
var (
	ErrNoActiveSession = errors.New("no active lilt session")
	ErrTransport       = errors.New("session transport failed")
)

// SocketPath returns the default server socket path. LILT_SOCKET overrides it
// for tests and explicit deployments.
func SocketPath() string {
	if value := os.Getenv("LILT_SOCKET"); value != "" {
		return value
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), fmt.Sprintf("lilt-%d.sock", os.Getuid()))
	}
	return filepath.Join(dir, "lilt", "session.sock")
}

// NewRequestID returns an opaque, unique, roughly time-sortable id.
func NewRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return strings.ToUpper(hex.EncodeToString(raw[:]))
}

// Call dials the socket, sends request, and decodes one response. RequestID is
// filled in when empty.
func Call(ctx context.Context, socketPath string, request Request) (Response, error) {
	conn, err := dial(ctx, socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	return roundTrip(conn, ctx, request)
}

func roundTrip(conn net.Conn, ctx context.Context, request Request) (Response, error) {
	if request.RequestID == "" {
		request.RequestID = NewRequestID()
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	return response, nil
}

func dial(ctx context.Context, socketPath string) (net.Conn, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "connection refused") {
			return nil, ErrNoActiveSession
		}
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	return conn, nil
}

// Watcher is a live session.watch connection. The initial snapshot is returned
// by Watch; subsequent events arrive on Events until the context is cancelled
// or the server closes the connection.
type Watcher struct {
	conn   net.Conn
	Events <-chan Event
	cancel context.CancelFunc
}

// Watch opens a session.watch stream. The returned Response carries the
// WatchSnapshot; Events delivers subsequent events. Call Close to release it.
func Watch(ctx context.Context, socketPath string, topics []string, includeState bool) (Response, *Watcher, error) {
	conn, err := dial(ctx, socketPath)
	if err != nil {
		return Response{}, nil, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	// The initial response can wait on a slow provider. Cover that decode as
	// well as the event stream with the caller's cancellation and deadline.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	go func() {
		<-watchCtx.Done()
		_ = conn.Close()
	}()
	params := map[string]any{}
	if includeState {
		params["includeState"] = true
	}
	if len(topics) > 0 {
		params["topics"] = topics
	}
	rawParams, _ := json.Marshal(params)
	request := Request{RequestID: NewRequestID(), Command: "session.watch", Params: rawParams}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		cancel()
		_ = conn.Close()
		if ctx.Err() != nil {
			return Response{}, nil, ctx.Err()
		}
		return Response{}, nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	decoder := json.NewDecoder(bufio.NewReader(conn))
	var initial Response
	if err := decoder.Decode(&initial); err != nil {
		cancel()
		_ = conn.Close()
		if ctx.Err() != nil {
			return Response{}, nil, ctx.Err()
		}
		return Response{}, nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	if !initial.OK {
		cancel()
		_ = conn.Close()
		return initial, nil, nil
	}
	events := make(chan Event, 64)
	watcher := &Watcher{conn: conn, Events: events, cancel: cancel}
	go func() {
		defer close(events)
		defer cancel()
		for {
			var event Event
			if err := decoder.Decode(&event); err != nil {
				return
			}
			select {
			case events <- event:
			case <-watchCtx.Done():
				return
			}
		}
	}()
	return initial, watcher, nil
}

// Close releases the watch connection.
func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	w.cancel()
	if err := w.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// Command is a convenience wrapper that builds and sends a typed command.
func Command(ctx context.Context, socketPath, command string, params any) (Response, error) {
	request := Request{RequestID: NewRequestID(), Command: command}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return Response{}, fmt.Errorf("%w: %v", ErrTransport, err)
		}
		request.Params = raw
	}
	return Call(ctx, socketPath, request)
}
