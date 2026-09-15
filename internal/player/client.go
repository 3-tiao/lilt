// Package player implements the private NDJSON JSON-RPC connection to the
// LaunchServices-launched lilt-player app.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caiguo/lilt/core"
)

const (
	startupTimeout  = 20 * time.Second
	shutdownTimeout = 3 * time.Second
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

type stateChangedNotification struct {
	Sequence uint64             `json:"sequence"`
	State    core.PlaybackState `json:"state"`
}

// RPCError preserves helper error codes for callers such as the host session
// socket.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

type pending struct{ response chan rpcResponse }

// streamClient is transport-agnostic and retained for deterministic protocol
// tests. Production gives it a connected Unix socket, never helper stdio.
type streamClient struct {
	conn         io.ReadWriteCloser
	writeMu      sync.Mutex
	pendingMu    sync.Mutex
	pending      map[uint64]pending
	terminalErr  error
	next         atomic.Uint64
	done         chan struct{}
	notifyMu     sync.Mutex
	notifyQ      []core.PlaybackStateUpdate
	notifyWake   chan struct{}
	notifyStop   chan struct{}
	notifyDone   chan struct{}
	notifyClosed bool
	updates      chan core.PlaybackStateUpdate
	closeOnce    sync.Once
	abortOnce    sync.Once
	notifyOnce   sync.Once
}

const disconnectedMessage = "lilt-player RPC transport is unusable; quit and restart lilt to reconnect"

func newStreamClient(conn io.ReadWriteCloser) *streamClient {
	c := &streamClient{
		conn: conn, pending: make(map[uint64]pending), done: make(chan struct{}),
		notifyWake: make(chan struct{}, 1), notifyStop: make(chan struct{}),
		notifyDone: make(chan struct{}), updates: make(chan core.PlaybackStateUpdate),
	}
	go c.dispatchNotifications()
	go c.read()
	return c
}

// dispatchNotifications drains the mutex-protected queue in arrival order.
// Only this goroutine can block on the consumer; the socket reader only
// appends to notifyQ and signals notifyWake without waiting.
func (c *streamClient) dispatchNotifications() {
	defer close(c.notifyDone)
	defer close(c.updates)
	for {
		c.notifyMu.Lock()
		if len(c.notifyQ) > 0 {
			next := c.notifyQ[0]
			c.notifyQ[0] = core.PlaybackStateUpdate{}
			c.notifyQ = c.notifyQ[1:]
			c.notifyMu.Unlock()
			select {
			case c.updates <- next:
				continue
			case <-c.notifyStop:
				return
			}
		}
		closed := c.notifyClosed
		c.notifyMu.Unlock()
		if closed {
			return
		}
		select {
		case <-c.notifyWake:
		case <-c.notifyStop:
			return
		}
	}
}

func (c *streamClient) read() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var message rpcMessage
		if json.Unmarshal(line, &message) != nil || message.JSONRPC != "2.0" {
			continue
		}
		if message.ID == nil {
			c.dispatchNotification(message)
			continue
		}
		var response rpcResponse
		if json.Unmarshal(line, &response) != nil {
			continue
		}
		c.pendingMu.Lock()
		p, ok := c.pending[response.ID]
		if ok {
			delete(c.pending, response.ID)
			p.response <- response
		}
		c.pendingMu.Unlock()
	}
	c.abort(errors.New("lilt-player closed its Unix RPC socket"))
	close(c.done)
}

func (c *streamClient) dispatchNotification(message rpcMessage) {
	if message.Method != "stateChanged" {
		return
	}
	var notification stateChangedNotification
	if json.Unmarshal(message.Params, &notification) != nil {
		return
	}
	c.notifyMu.Lock()
	if c.notifyClosed {
		c.notifyMu.Unlock()
		return
	}
	c.notifyQ = append(c.notifyQ, core.PlaybackStateUpdate{Sequence: notification.Sequence, State: notification.State})
	c.notifyMu.Unlock()
	select {
	case c.notifyWake <- struct{}{}:
	default:
	}
}

func (c *streamClient) abort(reason error) {
	c.abortOnce.Do(func() {
		c.pendingMu.Lock()
		c.terminalErr = reason
		for id, p := range c.pending {
			delete(c.pending, id)
			close(p.response)
		}
		c.pendingMu.Unlock()
		c.notifyMu.Lock()
		c.notifyClosed = true
		c.notifyQ = nil
		c.notifyMu.Unlock()
		c.notifyOnce.Do(func() { close(c.notifyStop) })
		_ = c.conn.Close()
	})
}

func (c *streamClient) transportError() error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.terminalErr != nil {
		return c.terminalErr
	}
	return errors.New(disconnectedMessage)
}

func (c *streamClient) call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	p := pending{response: make(chan rpcResponse, 1)}
	c.pendingMu.Lock()
	if c.terminalErr != nil {
		err := c.terminalErr
		c.pendingMu.Unlock()
		return err
	}
	c.pending[id] = p
	c.pendingMu.Unlock()

	request, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		c.removePending(id)
		return err
	}
	writeDone := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		_, writeErr := c.conn.Write(append(request, '\n'))
		c.writeMu.Unlock()
		writeDone <- writeErr
	}()
	select {
	case err = <-writeDone:
		if err != nil {
			transportErr := fmt.Errorf("%s after request %q write failed: %w", disconnectedMessage, method, err)
			c.abort(transportErr)
			return transportErr
		}
	case <-ctx.Done():
		timeoutErr := fmt.Errorf("%s while writing request %q: %w", disconnectedMessage, method, ctx.Err())
		c.abort(timeoutErr)
		return timeoutErr
	case <-c.done:
		return c.transportError()
	}

	select {
	case response, ok := <-p.response:
		if !ok {
			return c.transportError()
		}
		if err := ctx.Err(); err != nil {
			timeoutErr := fmt.Errorf("%s after request %q timed out: %w", disconnectedMessage, method, err)
			c.abort(timeoutErr)
			return timeoutErr
		}
		if response.ID != id {
			return errors.New("lilt-player returned a mismatched request ID")
		}
		if response.Error != nil {
			return response.Error
		}
		if result != nil && len(response.Result) > 0 {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-ctx.Done():
		timeoutErr := fmt.Errorf("%s after request %q timed out: %w", disconnectedMessage, method, ctx.Err())
		// The helper executes requests serially and cannot safely cancel an
		// arbitrary MusicKit operation. Closing the transport rejects every late
		// response and every following call instead of allowing ambiguous effects.
		c.abort(timeoutErr)
		return timeoutErr
	case <-c.done:
		return c.transportError()
	}
}

func (c *streamClient) removePending(id uint64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *streamClient) close() error {
	var err error
	c.closeOnce.Do(func() {
		c.abort(errors.New("lilt-player RPC transport closed"))
		<-c.done
		<-c.notifyDone
	})
	return err
}

// Client owns one private app instance, its Unix RPC connection, and its
// per-session runtime directory.
type Client struct {
	rpc        *streamClient
	cmd        *exec.Cmd
	wait       <-chan error
	stderr     io.Reader
	runtimeDir string
	socketPath string
	appPID     int
	closeOnce  sync.Once
	killOnce   sync.Once
	closeErr   error
	Trace      func(method string, duration time.Duration, err error)
}

type helloResult struct {
	PID int `json:"pid"`
}

// Start launches a new signed app instance through LaunchServices and waits
// for its private Unix socket. appPath must name the .app bundle, not its
// Contents/MacOS executable.
func Start(appPath string) (*Client, error) {
	if filepath.Ext(appPath) != ".app" {
		return nil, fmt.Errorf("lilt-player path must be a signed .app bundle: %s", appPath)
	}
	info, err := os.Stat(appPath)
	if err != nil {
		return nil, fmt.Errorf("stat lilt-player app: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("lilt-player path is not an app bundle: %s", appPath)
	}

	runtimeDir, err := os.MkdirTemp("/tmp", fmt.Sprintf("lilt-%d-", os.Getuid()))
	if err != nil {
		return nil, fmt.Errorf("create private RPC directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0700); err != nil {
		_ = os.RemoveAll(runtimeDir)
		return nil, fmt.Errorf("secure private RPC directory: %w", err)
	}
	socketPath := filepath.Join(runtimeDir, "rpc.sock")

	cmd := exec.Command("/usr/bin/open", "-n", "-W", appPath, "--args", "--rpc-socket", socketPath)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(runtimeDir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(runtimeDir)
		return nil, fmt.Errorf("launch lilt-player through LaunchServices: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	rpc, err := dialUnix(ctx, socketPath, wait)
	if err != nil {
		_ = cmd.Process.Kill() // Stops only the open waiter; the app has its own no-host watchdog.
		_ = os.RemoveAll(runtimeDir)
		return nil, err
	}
	client := &Client{rpc: rpc, cmd: cmd, wait: wait, stderr: stderr, runtimeDir: runtimeDir, socketPath: socketPath}
	var hello helloResult
	if err := client.Call(ctx, "ping", nil, &hello); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("verify lilt-player RPC socket: %w", err)
	}
	client.appPID = hello.PID
	return client, nil
}

func dialUnix(ctx context.Context, path string, processDone <-chan error) (*streamClient, error) {
	dialer := net.Dialer{}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		conn, err := dialer.DialContext(ctx, "unix", path)
		if err == nil {
			return newStreamClient(conn), nil
		}
		lastErr = err
		select {
		case err := <-processDone:
			if err == nil {
				err = errors.New("app exited before opening its RPC socket")
			}
			return nil, fmt.Errorf("lilt-player startup failed: %w", err)
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("wait for lilt-player Unix socket %s: %w (%v)", path, ctx.Err(), lastErr)
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Stderr is LaunchServices launch diagnostics only; protocol data always uses
// the private Unix socket.
func (c *Client) Stderr() io.Reader { return c.stderr }

// PID returns the helper app's process id after Start.
func (c *Client) PID() int { return c.appPID }

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	start := time.Now()
	err := c.rpc.call(ctx, method, params, result)
	if ctx.Err() != nil {
		// Closing the socket rejects responses; killing this private helper
		// instance prevents a serial MusicKit operation from completing with a
		// late side effect after its caller has timed out.
		c.killOnce.Do(func() {
			if c.appPID > 0 {
				if process, findErr := os.FindProcess(c.appPID); findErr == nil {
					_ = process.Kill()
				}
			}
			if c.cmd != nil && c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
		})
	}
	if c.Trace != nil {
		c.Trace(method, time.Since(start), err)
	}
	return err
}

// Close asks this exact app instance to stop playback and terminate. If it is
// unresponsive, only the PID returned by this private socket is signalled.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		shutdownErr := c.Call(ctx, "shutdown", nil, nil)
		cancel()
		_ = c.rpc.close()

		select {
		case waitErr := <-c.wait:
			if shutdownErr == nil && waitErr != nil {
				c.closeErr = waitErr
			}
		case <-time.After(shutdownTimeout):
			if c.appPID > 0 {
				if process, err := os.FindProcess(c.appPID); err == nil {
					_ = process.Signal(os.Interrupt)
				}
			}
			select {
			case <-c.wait:
			case <-time.After(time.Second):
				if c.appPID > 0 {
					if process, err := os.FindProcess(c.appPID); err == nil {
						_ = process.Kill()
					}
				}
				_ = c.cmd.Process.Kill()
			}
		}
		if shutdownErr != nil && !strings.Contains(shutdownErr.Error(), "closed its Unix RPC socket") {
			c.closeErr = shutdownErr
		}
		if err := os.RemoveAll(c.runtimeDir); c.closeErr == nil && err != nil {
			c.closeErr = err
		}
	})
	return c.closeErr
}

func (c *Client) Play(ctx context.Context, request core.PlaybackRequest) error {
	_, err := c.PlayState(ctx, request)
	return err
}
func (c *Client) Pause(ctx context.Context) error    { _, err := c.PauseState(ctx); return err }
func (c *Client) Resume(ctx context.Context) error   { _, err := c.ResumeState(ctx); return err }
func (c *Client) Next(ctx context.Context) error     { _, err := c.NextState(ctx); return err }
func (c *Client) Previous(ctx context.Context) error { _, err := c.PreviousState(ctx); return err }

func (c *Client) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "play", request, &state)
	return state, err
}
func (c *Client) PauseState(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "pause", nil, &state)
	return state, err
}
func (c *Client) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "resume", nil, &state)
	return state, err
}
func (c *Client) NextState(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "next", nil, &state)
	return state, err
}
func (c *Client) PreviousState(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "previous", nil, &state)
	return state, err
}
func (c *Client) State(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "state", nil, &state)
	return state, err
}

// SubscribeState enables helper publication and returns its initial snapshot
// together with the ordered notification stream for this connection.
func (c *Client) SubscribeState(ctx context.Context) (core.StateSubscription, error) {
	var initial core.PlaybackStateUpdate
	if err := c.Call(ctx, "subscribeState", nil, &initial); err != nil {
		return core.StateSubscription{}, err
	}
	return core.StateSubscription{Initial: initial, Updates: c.rpc.updates}, nil
}

func (c *Client) UnsubscribeState(ctx context.Context) error {
	return c.Call(ctx, "unsubscribeState", nil, nil)
}
func (c *Client) Search(ctx context.Context, term string, limit int) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "search", map[string]any{"term": term, "limit": limit}, &items)
	return items, err
}
func (c *Client) LibraryPlaylists(ctx context.Context) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "libraryPlaylists", nil, &items)
	return items, err
}
func (c *Client) PlaylistTracks(ctx context.Context, id string) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "playlistTracks", map[string]any{"id": id}, &items)
	return items, err
}
func (c *Client) Recommendations(ctx context.Context) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "recommendations", nil, &items)
	return items, err
}
func (c *Client) RecentPlayed(ctx context.Context, limit int) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "recentPlayed", map[string]any{"limit": limit}, &items)
	return items, err
}
func (c *Client) Stations(ctx context.Context, term string, limit int) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "stations", map[string]any{"term": term, "limit": limit}, &items)
	return items, err
}
func (c *Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]core.Item, error) {
	var items []core.Item
	err := c.Call(ctx, "searchPlaylists", map[string]any{"term": term, "limit": limit}, &items)
	return items, err
}
func (c *Client) ResolveURL(ctx context.Context, url string) (core.Item, error) {
	var items []core.Item
	if err := c.Call(ctx, "resolveUrl", map[string]any{"url": url}, &items); err != nil {
		return core.Item{}, err
	}
	if len(items) == 0 {
		return core.Item{}, errors.New("resolveUrl returned no item")
	}
	return items[0], nil
}
func (c *Client) Authorization(ctx context.Context) (core.AuthorizationStatus, error) {
	return c.RequestAuthorization(ctx, false)
}
func (c *Client) RequestAuthorization(ctx context.Context, request bool) (core.AuthorizationStatus, error) {
	var status core.AuthorizationStatus
	err := c.Call(ctx, "authorize", map[string]any{"request": request}, &status)
	return status, err
}

func (c *Client) Diagnose(ctx context.Context) (core.TokenDiagnostics, error) {
	var diagnostics core.TokenDiagnostics
	err := c.Call(ctx, "diagnose", nil, &diagnostics)
	return diagnostics, err
}
func (c *Client) SetShuffle(ctx context.Context, on bool) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "setShuffle", map[string]any{"on": on}, &state)
	return state, err
}
func (c *Client) SetRepeat(ctx context.Context, mode string) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "setRepeat", map[string]any{"mode": mode}, &state)
	return state, err
}
func (c *Client) Stop(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "stop", nil, &state)
	return state, err
}
func (c *Client) Enqueue(ctx context.Context, request core.PlaybackRequest, position string) (core.PlaybackState, error) {
	var state core.PlaybackState
	params := map[string]any{"kind": request.Kind, "id": request.ID, "url": request.URL, "position": position}
	err := c.Call(ctx, "enqueue", params, &state)
	return state, err
}
func (c *Client) PlaySongs(ctx context.Context, ids []string, startIndex int) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "playSongs", map[string]any{"ids": ids, "startIndex": startIndex}, &state)
	return state, err
}
func (c *Client) QueueJump(ctx context.Context, index int) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "queueJump", map[string]any{"index": index}, &state)
	return state, err
}
func (c *Client) QueueRemove(ctx context.Context, index int) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "queueRemove", map[string]any{"index": index}, &state)
	return state, err
}
func (c *Client) QueueMove(ctx context.Context, from, to int) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "queueMove", map[string]any{"from": from, "to": to}, &state)
	return state, err
}
func (c *Client) QueueClear(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "queueClear", nil, &state)
	return state, err
}
func (c *Client) RadioPlay(ctx context.Context, url, name string) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "radioPlay", map[string]any{"url": url, "name": name}, &state)
	return state, err
}
func (c *Client) RadioStop(ctx context.Context) (core.PlaybackState, error) {
	var state core.PlaybackState
	err := c.Call(ctx, "radioStop", nil, &state)
	return state, err
}

// Probe asks the helper whether AVFoundation can load a stream without
// playing it. The helper answers within timeoutMs even on failure, so the
// caller's context only guards a wedged helper.
func (c *Client) Probe(ctx context.Context, url string, timeoutMs int) (core.RadioProbeResult, error) {
	var result core.RadioProbeResult
	err := c.Call(ctx, "radioProbe", map[string]any{"url": url, "timeoutMs": timeoutMs}, &result)
	return result, err
}
