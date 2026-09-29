package mpvplayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/3-tiao/lilt/core"
)

const (
	// startTimeout bounds mpv's launch plus its IPC socket becoming visible.
	startTimeout = 10 * time.Second
	// commandTimeout bounds one IPC command when the caller gave no deadline.
	commandTimeout = 10 * time.Second
	// defaultLoadWait bounds how long a start waits for mpv to report a terminal
	// outcome before returning a buffering state. It is short on purpose: a
	// station that is merely slow must not consume the command's time budget.
	defaultLoadWait = 2 * time.Second
	// sampleInterval matches the macOS helper's one-second progress sampler.
	sampleInterval = time.Second
	// shutdownTimeout bounds a graceful mpv quit before it is killed.
	shutdownTimeout = 3 * time.Second
	// maxQueuedUpdates coalesces state publications when the consumer is slow.
	// Every update is a full snapshot, so dropping earlier ones is safe.
	maxQueuedUpdates = 8
)

// ErrMPVMissing reports that no usable mpv binary was found. It surfaces on the
// first playback attempt, not at construction: browsing radio must keep working
// on a machine without mpv.
var ErrMPVMissing = errors.New("mpv is not installed")

// playbackTarget is the media a client was last asked to play, together with
// the URL-queue session identity the server uses to discard stale updates.
type playbackTarget struct {
	item       core.Item
	url        string
	live       bool
	generation uint64
	session    string
}

// Client owns one mpv process and its JSON IPC socket. It implements
// server.AudioEngine; there is no helper process on Linux, so the server's
// engine boundaries are satisfied in-process.
type Client struct {
	startMu sync.Mutex
	mu      sync.Mutex

	cmd    *exec.Cmd
	ipc    *ipcConn
	dir    string
	exited chan error
	ticker chan struct{}

	// loadWait is non-nil while a start is waiting for mpv to accept the media.
	loadWait chan error

	current playbackTarget

	loaded   bool
	paused   bool
	idle     bool
	duration float64
	position float64
	eof      bool
	errText  string

	updates  chan core.PlaybackStateUpdate
	queue    []core.PlaybackStateUpdate
	wake     chan struct{}
	halt     chan struct{}
	done     chan struct{}
	closed   bool
	sequence uint64

	closeOnce sync.Once
	closeErr  error

	// LoadWait overrides defaultLoadWait; tests shorten it.
	LoadWait time.Duration
	// HTTP probes streams; nil uses http.DefaultClient. It exists so the probe
	// tests can use an httptest server instead of the network.
	HTTP *http.Client
}

// New returns an idle client. mpv is started lazily on the first playback so a
// server that only browses radio never spawns a player.
func New() *Client {
	c := &Client{
		updates: make(chan core.PlaybackStateUpdate),
		wake:    make(chan struct{}, 1),
		halt:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.dispatch()
	return c
}

// --- AudioEngine -------------------------------------------------------------

func (c *Client) State(ctx context.Context) (core.PlaybackState, error) {
	c.mu.Lock()
	ipc := c.ipc
	c.mu.Unlock()
	if ipc == nil {
		// Answering a status question must not spawn a player.
		return c.snapshot(), nil
	}
	if err := c.refresh(ctx); err != nil {
		return core.PlaybackState{}, err
	}
	return c.snapshot(), nil
}

func (c *Client) PauseState(ctx context.Context) (core.PlaybackState, error) {
	return c.setPaused(ctx, true)
}

func (c *Client) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	return c.setPaused(ctx, false)
}

func (c *Client) Stop(ctx context.Context) (core.PlaybackState, error) {
	return c.stop(ctx)
}

func (c *Client) RadioPlay(ctx context.Context, url, name string) (core.PlaybackState, error) {
	return c.play(ctx, playbackTarget{
		item: core.Item{Kind: "stream", URL: url, Title: name},
		url:  url,
		live: true,
	})
}

func (c *Client) RadioStop(ctx context.Context) (core.PlaybackState, error) {
	return c.stop(ctx)
}

func (c *Client) SubscribeState(context.Context) (core.StateSubscription, error) {
	c.mu.Lock()
	initial := core.PlaybackStateUpdate{Sequence: c.sequence, State: c.stateLocked()}
	c.mu.Unlock()
	return core.StateSubscription{Initial: initial, Updates: c.updates}, nil
}

func (c *Client) UnsubscribeState(context.Context) error { return nil }

// --- URL queue driver --------------------------------------------------------

// PlayURL implements server.URLPlaybackDriver. target.URL is runtime-only and
// is never logged or retained beyond this client's memory.
func (c *Client) PlayURL(ctx context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	return c.play(ctx, playbackTarget{
		item:       target.Item,
		url:        target.URL,
		generation: target.PlaybackGeneration,
		session:    target.TransportSessionID,
	})
}

func (c *Client) PauseURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if err := c.requireSession(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	return c.setPaused(ctx, true)
}

func (c *Client) ResumeURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if err := c.requireSession(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	return c.setPaused(ctx, false)
}

func (c *Client) StopURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if err := c.requireSession(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	return c.stop(ctx)
}

func (c *Client) StateURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if err := c.requireSession(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	return c.State(ctx)
}

// requireSession rejects a command aimed at a superseded URL queue session: a
// late call from a replaced queue must not touch the new one.
func (c *Client) requireSession(generation uint64, session string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current.session == "" {
		return errors.New("no url playback session is running")
	}
	if c.current.generation != generation || c.current.session != session {
		return errors.New("url playback session was replaced")
	}
	return nil
}

// --- playback ----------------------------------------------------------------

func (c *Client) play(ctx context.Context, target playbackTarget) (core.PlaybackState, error) {
	if target.url == "" {
		return core.PlaybackState{}, errors.New("mpv playback needs a stream URL")
	}
	if err := c.ensureStarted(ctx); err != nil {
		return core.PlaybackState{}, err
	}

	wait := make(chan error, 1)
	c.mu.Lock()
	c.current = target
	c.loaded, c.paused, c.idle, c.eof = false, false, false, false
	c.duration, c.position, c.errText = 0, 0, ""
	c.loadWait = wait
	ipc := c.ipc
	c.mu.Unlock()

	if _, err := ipc.call(ctx, "loadfile", target.url, "replace"); err != nil {
		c.clearLoadWait(wait)
		return core.PlaybackState{}, err
	}

	// mpv reports acceptance (file-loaded) and refusal (end-file with an error
	// reason) on the event stream. Waiting for one of them catches a dead URL in
	// the same command, but a station that is merely slow must not hold the
	// command past its own deadline, so the wait is capped and its expiry is
	// reported as buffering rather than as a failure.
	select {
	case err := <-wait:
		if err != nil {
			return c.snapshot(), err
		}
		return c.State(ctx)
	case <-time.After(c.loadBudget(ctx)):
		c.clearLoadWait(wait)
		return c.snapshot(), nil
	case <-ctx.Done():
		c.clearLoadWait(wait)
		return c.snapshot(), nil
	}
}

func (c *Client) setPaused(ctx context.Context, paused bool) (core.PlaybackState, error) {
	ipc, err := c.requireIPC()
	if err != nil {
		return core.PlaybackState{}, err
	}
	c.mu.Lock()
	changed := c.paused != paused
	c.mu.Unlock()
	if changed {
		if _, err := ipc.call(ctx, "set_property", "pause", paused); err != nil {
			return core.PlaybackState{}, err
		}
	}
	// mpv applies the property asynchronously: the property-change event may
	// still be in flight, so the reply alone would report the previous state.
	c.mu.Lock()
	c.paused = paused
	c.mu.Unlock()
	if err := c.refresh(ctx); err != nil {
		return core.PlaybackState{}, err
	}
	state := c.snapshot()
	c.publish(state)
	return state, nil
}

func (c *Client) stop(ctx context.Context) (core.PlaybackState, error) {
	ipc, err := c.requireIPC()
	if err != nil {
		return core.PlaybackState{}, err
	}
	if _, err := ipc.call(ctx, "stop"); err != nil {
		return core.PlaybackState{}, err
	}
	c.mu.Lock()
	c.current = playbackTarget{}
	c.loaded, c.paused, c.eof = false, false, false
	c.idle, c.errText = true, ""
	c.duration, c.position = 0, 0
	state := c.stateLocked()
	c.mu.Unlock()
	c.publish(state)
	return state, nil
}

func (c *Client) requireIPC() (*ipcConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ipc == nil {
		return nil, errors.New("no mpv playback session is running")
	}
	return c.ipc, nil
}

// refresh reads the properties that change continuously. Only time-pos is
// queried on demand: observing it would publish a state change per mpv tick.
func (c *Client) refresh(ctx context.Context) error {
	ipc, err := c.requireIPC()
	if err != nil {
		return err
	}
	data, err := ipc.call(ctx, "get_property", "time-pos")
	if err != nil {
		// mpv reports "property unavailable" whenever nothing is loaded, which
		// is the stopped state, not a transport failure.
		if !strings.Contains(err.Error(), "property unavailable") {
			return err
		}
		data = nil
	}
	var position float64
	if len(data) > 0 {
		_ = json.Unmarshal(data, &position)
	}
	c.mu.Lock()
	c.position = position
	c.mu.Unlock()
	return nil
}

// --- state assembly ----------------------------------------------------------

func (c *Client) snapshot() core.PlaybackState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

// stateLocked assembles the public state from the observed properties. Callers
// hold c.mu.
func (c *Client) stateLocked() core.PlaybackState {
	if c.current.url == "" {
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	}
	item := c.current.item
	item.URL = c.current.url
	state := core.PlaybackState{
		Track:              &item,
		Position:           c.position,
		Duration:           c.duration,
		Mode:               "stream",
		IsLive:             c.current.live,
		QueueIndex:         -1,
		PlaybackGeneration: c.current.generation,
		TransportSessionID: c.current.session,
	}
	if !c.current.live {
		state.Mode = "full"
	}
	switch {
	case c.errText != "":
		state.Status = "error"
		state.Error = c.errText
	case c.eof:
		state.Status = "ended"
		state.Ended = true
	case c.idle:
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	case c.paused:
		state.Status = "paused"
	case c.loaded:
		state.Status = "playing"
	default:
		state.Status = "buffering"
	}
	return state
}

// publish enqueues a full snapshot for the subscribed server. Publication is
// coalesced so a slow consumer cannot block mpv's event reader.
func (c *Client) publish(state core.PlaybackState) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.sequence++
	if len(c.queue) >= maxQueuedUpdates {
		c.queue = c.queue[len(c.queue)-(maxQueuedUpdates-1):]
	}
	c.queue = append(c.queue, core.PlaybackStateUpdate{Sequence: c.sequence, State: state})
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) publishCurrent() { c.publish(c.snapshot()) }

func (c *Client) dispatch() {
	defer close(c.done)
	defer close(c.updates)
	for {
		c.mu.Lock()
		if len(c.queue) > 0 {
			next := c.queue[0]
			c.queue[0] = core.PlaybackStateUpdate{}
			c.queue = c.queue[1:]
			c.mu.Unlock()
			select {
			case c.updates <- next:
				continue
			case <-c.halt:
				return
			}
		}
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-c.wake:
		case <-c.halt:
			return
		}
	}
}

// --- mpv process -------------------------------------------------------------

func (c *Client) ensureStarted(ctx context.Context) error {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	c.mu.Lock()
	started := c.ipc != nil
	c.mu.Unlock()
	if started {
		return nil
	}
	return c.start(ctx)
}

func (c *Client) start(ctx context.Context) error {
	binary, err := mpvBinary()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("lilt-mpv-%d-", os.Getuid()))
	if err != nil {
		return fmt.Errorf("create private mpv runtime directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("secure private mpv runtime directory: %w", err)
	}
	socket := filepath.Join(dir, "mpv.sock")

	// A deterministic audio-only slave: the user's mpv.conf, scripts, and window
	// behaviour must not change what lilt asks for.
	cmd := exec.Command(binary,
		"--no-config",
		"--idle=yes",
		"--no-terminal",
		"--force-window=no",
		"--audio-display=no",
		"--no-video",
		"--input-ipc-server="+socket,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("start mpv: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// mpv blocks on a full stderr pipe. Its diagnostics are not needed: an
	// end-file event carries the reason, and draining keeps playback alive.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	conn, err := dial(startCtx, socket, exited)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = os.RemoveAll(dir)
		return err
	}
	ipc := newIPCConn(conn, c.handleEvent)
	if err := observe(startCtx, ipc); err != nil {
		ipc.close()
		_ = cmd.Process.Kill()
		_ = os.RemoveAll(dir)
		return fmt.Errorf("prepare mpv property observation: %w", err)
	}

	ticker := make(chan struct{})
	c.mu.Lock()
	c.cmd, c.ipc, c.dir, c.exited, c.ticker = cmd, ipc, dir, exited, ticker
	c.mu.Unlock()
	go c.watchTransport(ipc)
	go c.sample(ticker)
	return nil
}

// dial waits for mpv to create its IPC socket. mpv binds it shortly after
// launch, so the first attempts routinely race it.
func dial(ctx context.Context, path string, exited <-chan error) (net.Conn, error) {
	dialer := net.Dialer{}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		conn, err := dialer.DialContext(ctx, "unix", path)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case err := <-exited:
			if err == nil {
				err = errors.New("mpv exited before opening its IPC socket")
			}
			return nil, fmt.Errorf("mpv startup failed: %w", err)
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for mpv IPC socket: %w (%v)", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func observe(ctx context.Context, ipc *ipcConn) error {
	for id, property := range []string{"pause", "idle-active", "eof-reached", "duration"} {
		if _, err := ipc.call(ctx, "observe_property", id+1, property); err != nil {
			return err
		}
	}
	return nil
}

// watchTransport marks the client unusable when mpv dies. Closing the update
// stream is what tells the server to rebuild its audio engine.
func (c *Client) watchTransport(ipc *ipcConn) {
	<-ipc.done
	c.mu.Lock()
	if c.ipc == ipc {
		c.ipc = nil
		c.cmd = nil
		c.closed = true
		c.queue = nil
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// sample publishes the advancing position once a second while audio is
// actually progressing, mirroring the macOS helper's sampler.
func (c *Client) sample(stop chan struct{}) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if !c.progressing() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		err := c.refresh(ctx)
		cancel()
		if err != nil {
			continue
		}
		c.publishCurrent()
	}
}

func (c *Client) progressing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ipc != nil && c.current.url != "" && c.loaded && !c.paused && !c.idle && c.errText == ""
}

func (c *Client) loadTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.LoadWait > 0 {
		return c.LoadWait
	}
	return defaultLoadWait
}

// loadBudget keeps the load wait inside the caller's deadline, leaving room for
// the state read that follows. A caller with no deadline gets the full budget.
func (c *Client) loadBudget(ctx context.Context) time.Duration {
	budget := c.loadTimeout()
	deadline, ok := ctx.Deadline()
	if !ok {
		return budget
	}
	if remaining := time.Until(deadline) - 500*time.Millisecond; remaining < budget {
		budget = remaining
	}
	return budget
}

func (c *Client) clearLoadWait(waiter chan error) {
	c.mu.Lock()
	if c.loadWait == waiter {
		c.loadWait = nil
	}
	c.mu.Unlock()
}

// --- events ------------------------------------------------------------------

func (c *Client) handleEvent(message ipcMessage) {
	switch message.Event {
	case "property-change":
		c.applyProperty(message)
	case "start-file":
		c.mu.Lock()
		c.loaded, c.eof, c.errText = false, false, ""
		// A new file is starting, so mpv is no longer idle. The idle-active
		// property also says so, but it arrives after the end-file of the file
		// this load replaces, and that gap used to report a stopped stream.
		c.idle = false
		c.mu.Unlock()
		c.publishCurrent()
	case "file-loaded":
		c.mu.Lock()
		c.loaded, c.idle, c.eof, c.errText = true, false, false, ""
		waiter := c.loadWait
		c.loadWait = nil
		c.mu.Unlock()
		if waiter != nil {
			waiter <- nil
		}
		c.publishCurrent()
	case "end-file":
		c.applyEndFile(message)
	}
}

func (c *Client) applyProperty(message ipcMessage) {
	c.mu.Lock()
	changed := false
	switch message.Name {
	case "pause":
		if value := decodeBool(message.Data); value != c.paused {
			c.paused, changed = value, true
		}
	case "idle-active":
		if value := decodeBool(message.Data); value != c.idle {
			c.idle, changed = value, true
		}
	case "eof-reached":
		// mpv repeats this property; a track must not be advanced twice.
		if value := decodeBool(message.Data); value && !c.eof {
			c.eof, changed = true, true
		} else if !value && c.eof {
			c.eof, changed = false, true
		}
	case "duration":
		if value := decodeFloat(message.Data); value != c.duration {
			c.duration, changed = value, true
		}
	}
	state := c.stateLocked()
	c.mu.Unlock()
	if changed {
		c.publish(state)
	}
}

func (c *Client) applyEndFile(message ipcMessage) {
	c.mu.Lock()
	c.loaded = false
	// Whether playback is over is decided by the observed idle-active property,
	// not by end-file: loadfile replace ends the previous file too.
	var waiter chan error
	switch message.Reason {
	case "error":
		c.errText = message.Error
		if c.errText == "" {
			c.errText = "mpv could not play the stream"
		}
		waiter, c.loadWait = c.loadWait, nil
	case "eof":
		c.eof = true
		waiter, c.loadWait = c.loadWait, nil
	}
	// Only a terminal outcome of the file being started settles the wait. A
	// displaced file's "stop" arrives first and must not resolve it, or the
	// caller reads the state before the replacement has loaded.
	state := c.stateLocked()
	c.mu.Unlock()
	if waiter != nil {
		if state.Error != "" {
			waiter <- errors.New(state.Error)
		} else {
			waiter <- nil
		}
	}
	c.publish(state)
}

func decodeBool(data json.RawMessage) bool {
	return string(data) == "true"
}

func decodeFloat(data json.RawMessage) float64 {
	if len(data) == 0 {
		return 0
	}
	var value float64
	_ = json.Unmarshal(data, &value)
	return value
}

// --- binary resolution -------------------------------------------------------

// mpvBinary resolves the mpv executable. LILT_MPV_PATH wins so a deployment can
// pin an exact build; otherwise the first mpv on PATH is used.
func mpvBinary() (string, error) {
	if path := os.Getenv("LILT_MPV_PATH"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%w: LILT_MPV_PATH=%s: %v", ErrMPVMissing, path, err)
		}
		return path, nil
	}
	path, err := exec.LookPath("mpv")
	if err != nil {
		return "", fmt.Errorf("%w; install it (nixpkgs `mpv`, Debian/Ubuntu `apt install mpv`) and put it on PATH", ErrMPVMissing)
	}
	return path, nil
}

// --- shutdown ----------------------------------------------------------------

// Close stops mpv, waits for it, kills it if it refuses, and removes the private
// runtime directory. Exiting lilt must never leave an mpv process behind.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		ipc, cmd, exited, dir, ticker := c.ipc, c.cmd, c.exited, c.dir, c.ticker
		c.ipc, c.cmd, c.exited, c.dir, c.ticker = nil, nil, nil, "", nil
		c.mu.Unlock()

		if ticker != nil {
			close(ticker)
		}
		if ipc != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			_, _ = ipc.call(ctx, "quit")
			cancel()
			ipc.close()
		}
		if exited != nil {
			select {
			case <-exited:
			case <-time.After(shutdownTimeout):
			}
		}
		if cmd != nil && cmd.Process != nil {
			if process, err := os.FindProcess(cmd.Process.Pid); err == nil {
				_ = process.Kill()
			}
		}
		c.mu.Lock()
		c.closed = true
		c.queue = nil
		c.mu.Unlock()
		close(c.halt)
		if dir != "" {
			if err := os.RemoveAll(dir); err != nil {
				c.closeErr = err
			}
		}
	})
	return c.closeErr
}
