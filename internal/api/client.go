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
	"sync"
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

// Server epoch cache. A client attaches the epoch it last observed so a stored
// revision or playback token can never be applied to a different server
// process on the same socket (see docs/internals/concurrency.md §5.2).
var (
	epochMu     sync.Mutex
	epochByPath = map[string]string{}
)

// Epoch returns the last epoch observed on socketPath, or "" when unknown.
// Long-lived clients use it to tell a reconnect to the same process from a
// reconnect to a restarted one.
func Epoch(socketPath string) string {
	epochMu.Lock()
	defer epochMu.Unlock()
	return epochByPath[socketPath]
}

// ForgetEpoch drops the cached epoch so the next command bootstraps it again.
// A server_epoch conflict calls this: the failing request is not retried here,
// because only the caller can decide whether to repeat the intent.
func ForgetEpoch(socketPath string) {
	epochMu.Lock()
	delete(epochByPath, socketPath)
	epochMu.Unlock()
}

func rememberEpoch(socketPath, epoch string) {
	if socketPath == "" || epoch == "" {
		return
	}
	epochMu.Lock()
	epochByPath[socketPath] = epoch
	epochMu.Unlock()
}

// ensureEpoch returns the epoch of the server behind socketPath, bootstrapping
// it with one pure session.status query the first time this process talks to
// that socket. A server (or test double) that reports no epoch yields "", and
// the command is sent without one.
func ensureEpoch(ctx context.Context, socketPath string) string {
	if epoch := Epoch(socketPath); epoch != "" {
		return epoch
	}
	response, err := Call(ctx, socketPath, Request{Command: "session.status"})
	if err != nil {
		return ""
	}
	rememberEpoch(socketPath, response.ServerInstanceID)
	return response.ServerInstanceID
}

// requestEpochFailure reports whether a response is the server rejecting a
// stale epoch, so the cache can be dropped.
func requestEpochFailure(response Response) bool {
	if response.Error == nil || response.Error.Code != CodeConflict {
		return false
	}
	reason, _ := response.Error.Details["reason"].(string)
	return reason == "server_epoch"
}

// Call dials the socket, sends request, and decodes one response. RequestID is
// filled in when empty.
func Call(ctx context.Context, socketPath string, request Request) (Response, error) {
	conn, err := dial(ctx, socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	response, err := roundTrip(conn, ctx, request)
	if err != nil {
		return Response{}, err
	}
	rememberEpoch(socketPath, response.ServerInstanceID)
	return response, nil
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
		rememberEpoch(socketPath, initial.ServerInstanceID)
		return initial, nil, nil
	}
	rememberEpoch(socketPath, initial.ServerInstanceID)
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

// Command is a convenience wrapper that builds and sends a typed command. For
// every command except the session.status bootstrap it attaches the server
// epoch observed on this socket, so a mutation cannot silently target a
// restarted server. A server_epoch conflict drops the cached epoch and is
// returned as-is: the caller re-reads state instead of this layer retrying.
func Command(ctx context.Context, socketPath, command string, params any) (Response, error) {
	request := Request{RequestID: NewRequestID(), Command: command}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return Response{}, fmt.Errorf("%w: %v", ErrTransport, err)
		}
		request.Params = raw
	}
	if command != "session.status" {
		request.IfServerInstanceID = ensureEpoch(ctx, socketPath)
	}
	response, err := Call(ctx, socketPath, request)
	if err != nil {
		return response, err
	}
	if requestEpochFailure(response) {
		ForgetEpoch(socketPath)
	}
	return response, nil
}
