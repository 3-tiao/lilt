package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/state"
)

type blockingDiscoveryProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingDiscoveryProvider) Source() api.SourceID { return api.SourceAudius }
func (p *blockingDiscoveryProvider) Descriptor(context.Context) api.SourceDescriptor {
	return api.SourceDescriptor{
		ID: api.SourceAudius, Available: true,
		Capabilities: map[string]api.Capability{api.CapSearchSongs: {Available: true}},
	}
}
func (p *blockingDiscoveryProvider) Search(context.Context, string, string, int) ([]api.Item, *api.Error) {
	close(p.started)
	<-p.release
	return []api.Item{{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:1", Ref: "audius:song:1", Title: "Result"}}, nil
}

type changingDescriptorProvider struct {
	mu        sync.Mutex
	once      sync.Once
	available bool
	started   chan struct{}
	release   chan struct{}
}

func (p *changingDescriptorProvider) Source() api.SourceID { return api.SourceAudius }
func (p *changingDescriptorProvider) Descriptor(context.Context) api.SourceDescriptor {
	p.mu.Lock()
	available := p.available
	p.mu.Unlock()
	p.once.Do(func() {
		close(p.started)
		<-p.release
	})
	return api.SourceDescriptor{ID: api.SourceAudius, Available: available,
		Capabilities: map[string]api.Capability{api.CapSearchSongs: {Available: available}}}
}
func (*changingDescriptorProvider) Search(context.Context, string, string, int) ([]api.Item, *api.Error) {
	return nil, nil
}

type collidingDescriptorProvider struct {
	requests chan chan struct{}
}

func (p *collidingDescriptorProvider) Source() api.SourceID { return api.SourceAudius }
func (p *collidingDescriptorProvider) Descriptor(context.Context) api.SourceDescriptor {
	release := make(chan struct{})
	p.requests <- release
	<-release
	return api.SourceDescriptor{ID: api.SourceAudius, Available: true}
}
func (*collidingDescriptorProvider) Search(context.Context, string, string, int) ([]api.Item, *api.Error) {
	return nil, nil
}

// startFakeAudius gives every default test server a hermetic Audius upstream.
// Without it, structural tests that only touch discovery (the provider gate)
// would still reach the real network just because the provider is registered.
func startFakeAudius(t *testing.T) *audius.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tracks/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"top1","title":"Trending","permalink":"/artist/top","is_streamable":true,"user":{"name":"Artist"}}]}`))
		case "/playlists/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"tp1","playlist_name":"Top List"}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	t.Cleanup(upstream.Close)
	return &audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
}

// startFakeJamendo gives every default test server a hermetic Jamendo
// upstream (featured tracks for trending; empty results elsewhere).
func startFakeJamendo(t *testing.T) *jamendo.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"headers":{"status":"success","code":0},"results":[]}`
		if r.URL.Path == "/tracks" && r.URL.Query().Get("featured") == "1" {
			body = `{"headers":{"status":"success","code":0},"results":[{"id":"t1","name":"Featured","duration":120,"artist_name":"Artist","audio":"https://media.invalid/t1","shareurl":"https://www.jamendo.com/track/t1"}]}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	return &jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
}

func startTestServer(t *testing.T) (*Server, string) {
	return startTestServerWithEngine(t, fakeengine.NewFakeEngine())
}

func startTestServerWithEngine(t *testing.T, engine Engine) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-srv-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	// The default server is fully hermetic: Jamendo gets a fake upstream and a
	// stored client_id so the provider gate can exercise its real descriptors.
	secureStore := securestore.NewMemory()
	if err := jamendo.SaveClientID(secureStore, "client-1"); err != nil {
		t.Fatalf("seed Jamendo client_id: %v", err)
	}
	server, startErr := Start(Options{
		SocketPath:    socket,
		Engine:        engine,
		Store:         state.New(filepath.Join(dir, "state.json")),
		AudiusClient:  startFakeAudius(t),
		JamendoClient: startFakeJamendo(t),
		SecureStore:   secureStore,
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, socket
}

func call(t *testing.T, socket, command string, params any) api.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := api.Command(ctx, socket, command, params)
	if err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	return response
}

// session.watch bypasses dispatch, but its params schema is still closed: an
// unknown key must be rejected instead of silently ignored (a typo in `topics`
// would otherwise subscribe the client to every topic).
func TestWatchRejectsUnknownParams(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "session.watch", map[string]any{"bogus": true})
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("session.watch unknown param = %+v, want invalid_request", response.Error)
	}
}

// Watch bypasses dispatch, so it must enforce the requestId rule itself.
func TestWatchRejectsEmptyRequestID(t *testing.T) {
	_, socket := startTestServer(t)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(api.Request{Command: "session.watch"}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var response api.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("watch without requestId = %+v, want invalid_request", response.Error)
	}
}

// The debug channel records one server.request per command carrying
// requestId/command/ok/errorCode, so a session can be reconstructed.
func TestServerRequestLoggedOnDebugChannel(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-log-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	type entry struct {
		kind   string
		fields map[string]any
	}
	var (
		mu      sync.Mutex
		entries []entry
	)
	server, err := Start(Options{
		SocketPath:    filepath.Join(dir, "s.sock"),
		Engine:        fakeengine.NewFakeEngine(),
		Store:         state.New(filepath.Join(dir, "state.json")),
		AudiusClient:  startFakeAudius(t),
		JamendoClient: startFakeJamendo(t),
		SecureStore:   securestore.NewMemory(),
		DebugLog: func(kind string, fields map[string]any) {
			mu.Lock()
			entries = append(entries, entry{kind: kind, fields: fields})
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	call(t, server.path, "sources.list", nil)
	bad := call(t, server.path, "playback.play", map[string]any{"ref": "not-a-ref"})
	if bad.Error == nil {
		t.Fatal("expected an invalid_reference error")
	}

	mu.Lock()
	defer mu.Unlock()
	var request *entry
	for i := range entries {
		if entries[i].kind == "server.request" && entries[i].fields["command"] == "playback.play" {
			request = &entries[i]
		}
	}
	if request == nil {
		t.Fatalf("no server.request entry for playback.play: %+v", entries)
	}
	if request.fields["ok"] != false || request.fields["errorCode"] == nil {
		t.Fatalf("failed request logged as %+v", request.fields)
	}
	if requestID, _ := request.fields["requestId"].(string); requestID == "" {
		t.Fatalf("request entry has no requestId: %+v", request.fields)
	}
}

// Watch events are recorded with their name and sequence on the debug channel.
func TestWatchPublishLoggedOnDebugChannel(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-watchlog-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	events := make(chan map[string]any, 16)
	server, err := Start(Options{
		SocketPath:    filepath.Join(dir, "s.sock"),
		Engine:        fakeengine.NewFakeEngine(),
		Store:         state.New(filepath.Join(dir, "state.json")),
		AudiusClient:  startFakeAudius(t),
		JamendoClient: startFakeJamendo(t),
		SecureStore:   securestore.NewMemory(),
		DebugLog: func(kind string, fields map[string]any) {
			if kind == "watch.publish" {
				events <- fields
			}
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	call(t, server.path, "ui.set", map[string]any{"theme": "gruvbox"})
	select {
	case fields := <-events:
		if fields["event"] == nil || fields["sequence"] == nil {
			t.Fatalf("watch.publish fields = %+v", fields)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no watch.publish entry after a state change")
	}
}

func TestWatchSnapshotRetriesAChangedProviderProjection(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-watch-snapshot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	provider := &changingDescriptorProvider{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		Providers:  []ContentProvider{provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type watchResult struct {
		snapshot api.WatchSnapshot
		err      error
	}
	result := make(chan watchResult, 1)
	go func() {
		response, watcher, watchErr := api.Watch(ctx, srv.path, []string{"sources"}, true)
		if watchErr != nil {
			result <- watchResult{err: watchErr}
			return
		}
		defer watcher.Close()
		var snapshot api.WatchSnapshot
		watchErr = json.Unmarshal(response.Data, &snapshot)
		result <- watchResult{snapshot: snapshot, err: watchErr}
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("watch did not enter provider projection")
	}
	// A state commit during the provider read must cause a fresh projection,
	// not a snapshot that mixes the old descriptor with the new sequence.
	provider.mu.Lock()
	provider.available = true
	provider.mu.Unlock()
	if response := call(t, srv.path, "ui.set", map[string]any{"theme": "gruvbox"}); !response.OK {
		t.Fatalf("ui.set: %+v", response.Error)
	}
	releaseOnce.Do(func() { close(provider.release) })
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.snapshot.Sequence == 0 || got.snapshot.State == nil {
			t.Fatalf("snapshot missing committed state: %+v", got.snapshot)
		}
		if !capabilityAvailability(got.snapshot.Sources, api.SourceAudius, api.CapSearchSongs) {
			t.Fatalf("snapshot retained the stale source descriptor: %+v", got.snapshot.Sources)
		}
	case <-ctx.Done():
		t.Fatal("watch snapshot never completed")
	}
}

func TestWatchCollisionsDoNotRunProviderUnderPlaybackLock(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-watch-retry-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	provider := &collidingDescriptorProvider{requests: make(chan chan struct{}, 4)}
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"), Engine: fakeengine.NewFakeEngine(),
		Store: state.New(filepath.Join(dir, "state.json")), Providers: []ContentProvider{provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Release any blocked provider read before closing the server on failure.
	t.Cleanup(func() {
		for {
			select {
			case release := <-provider.requests:
				close(release)
			default:
				_ = srv.Close()
				return
			}
		}
	})
	result := make(chan api.Response, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, watcher, _ := api.Watch(ctx, srv.path, []string{"sources"}, false)
		if watcher != nil {
			_ = watcher.Close()
		}
		result <- response
	}()
	for attempt := 0; attempt < 3; attempt++ {
		var release chan struct{}
		select {
		case release = <-provider.requests:
		case <-time.After(2 * time.Second):
			t.Fatal("watch did not retry the changed projection")
		}
		if changed := call(t, srv.path, "ui.set", map[string]any{"theme": "theme"}); !changed.OK {
			t.Fatalf("ui.set: %+v", changed.Error)
		}
		close(release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	control, err := api.Command(ctx, srv.path, "playback.pause", nil)
	if err != nil || !control.OK {
		t.Fatalf("watch projection blocked playback: %+v, %v", control.Error, err)
	}
	select {
	case response := <-result:
		if response.OK || response.Error == nil || response.Error.Code != api.CodeSessionUnavailable {
			t.Fatalf("watch response = %+v, want retryable session_unavailable", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not return after three collisions")
	}
}

func TestDiscoveryDoesNotBlockPlaybackControl(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-concurrent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	provider := &blockingDiscoveryProvider{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "session.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		Providers:  []ContentProvider{provider},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	searchDone := make(chan api.Response, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, _ := api.Command(ctx, srv.path, "discovery.search", map[string]any{
			"source": "audius", "term": "blocked", "type": "song",
		})
		searchDone <- response
	}()
	<-provider.started

	controlCtx, cancelControl := context.WithTimeout(context.Background(), 500*time.Millisecond)
	control, controlErr := api.Command(controlCtx, srv.path, "playback.pause", nil)
	cancelControl()
	if controlErr != nil || !control.OK {
		t.Fatalf("playback.pause was blocked by discovery: response=%+v err=%v", control.Error, controlErr)
	}
	releaseOnce.Do(func() { close(provider.release) })
	if search := <-searchDone; !search.OK {
		t.Fatalf("discovery.search failed: %+v", search.Error)
	}
}

func TestRadioDiscoveryDoesNotBlockPlaybackControl(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`[{"stationuuid":"one","name":"Station","url_resolved":"https://radio.example/live"}]`))
	}))
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })

	dir, err := os.MkdirTemp("/tmp", "lilt-radio-concurrent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	radioClient := radio.New()
	radioClient.Base, radioClient.Fallbacks, radioClient.HTTP = upstream.URL, nil, upstream.Client()
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "session.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		Radio:      radioClient,
		RadioCache: radio.NewCache(filepath.Join(dir, "radio-cache.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	searchDone := make(chan api.Response, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, _ := api.Command(ctx, srv.path, "radio.search", map[string]any{"origin": api.OriginDirectory})
		searchDone <- response
	}()
	<-started
	controlCtx, cancelControl := context.WithTimeout(context.Background(), 500*time.Millisecond)
	control, controlErr := api.Command(controlCtx, srv.path, "playback.pause", nil)
	cancelControl()
	if controlErr != nil || !control.OK {
		t.Fatalf("playback.pause was blocked by radio search: response=%+v err=%v", control.Error, controlErr)
	}
	releaseOnce.Do(func() { close(release) })
	if search := <-searchDone; !search.OK {
		t.Fatalf("radio.search failed: %+v", search.Error)
	}
}

func TestCommandTimeoutStartsAfterSerializedQueueWait(t *testing.T) {
	s := &Server{
		registry: api.NewRegistry(),
		dedup:    newDedupCache(0, 0),
		closed:   make(chan struct{}),
	}
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	s.registry.Bind("playback.pause", func(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return map[string]any{"call": 1}, nil
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 4800*time.Millisecond {
			return nil, api.Errorf(api.CodeSessionUnavailable, "second command spent its timeout while queued")
		}
		return map[string]any{"call": 2}, nil
	})

	firstDone := make(chan api.Response, 1)
	go func() {
		firstDone <- s.dispatch(api.Request{RequestID: "first", Command: "playback.pause"})
	}()
	<-started
	secondDone := make(chan api.Response, 1)
	go func() {
		secondDone <- s.dispatch(api.Request{RequestID: "second", Command: "playback.pause"})
	}()
	// The second request waits longer than the assertion threshold without
	// spending its own five-second execution budget.
	time.Sleep(300 * time.Millisecond)
	close(release)
	if response := <-firstDone; !response.OK {
		t.Fatalf("first response = %+v", response.Error)
	}
	if response := <-secondDone; !response.OK {
		t.Fatalf("second response = %+v", response.Error)
	}
}

func TestDescribeOverWire(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "api.describe", nil)
	if !response.OK {
		t.Fatalf("describe failed: %+v", response.Error)
	}
	var description api.ApiDescription
	if err := json.Unmarshal(response.Data, &description); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	if len(description.Commands) == 0 {
		t.Fatalf("describe = %+v", description)
	}
}

func TestSourcesList(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "sources.list", nil)
	if !response.OK {
		t.Fatalf("sources.list failed: %+v", response.Error)
	}
	var sources []api.SourceDescriptor
	if err := json.Unmarshal(response.Data, &sources); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, source := range sources {
		if source.ID == api.SourceAppleMusic {
			found = true
		}
	}
	if !found {
		t.Fatalf("sources = %+v, want apple-music", sources)
	}
}

func TestSessionStatusQueueProjection(t *testing.T) {
	_, socket := startTestServer(t)
	without := call(t, socket, "session.status", nil)
	if !without.OK {
		t.Fatalf("status failed: %+v", without.Error)
	}
	var status api.PlaybackStatus
	if err := json.Unmarshal(without.Data, &status); err != nil {
		t.Fatal(err)
	}
	if status.Source != api.SourceAppleMusic {
		t.Fatalf("source = %q", status.Source)
	}

	with := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	if !with.OK {
		t.Fatalf("status(queue) failed: %+v", with.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(with.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Source != api.SourceAppleMusic {
		t.Fatalf("queue state source = %q", state.Source)
	}
}

func TestUnknownCommandRejected(t *testing.T) {
	_, socket := startTestServer(t)
	unknown := call(t, socket, "does.not.exist", nil)
	if unknown.Error == nil || unknown.Error.Code != api.CodeUnknownCommand {
		t.Fatalf("unknown = %+v", unknown.Error)
	}
}

func TestShutdownSignals(t *testing.T) {
	server, socket := startTestServer(t)
	response := call(t, socket, "session.shutdown", nil)
	if !response.OK {
		t.Fatalf("shutdown failed: %+v", response.Error)
	}
	select {
	case <-server.ShutdownRequested():
	case <-time.After(time.Second):
		t.Fatal("shutdown was not signalled")
	}
}

// protocol.md §6: by the time the shutdown caller gets its response the server
// is already draining, so a later side-effecting command is refused.
func TestShutdownLinearizesBeforeAnsweringCaller(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "session.shutdown", nil)
	if !response.OK {
		t.Fatalf("shutdown failed: %+v", response.Error)
	}
	refused := call(t, socket, "ui.set", map[string]any{"theme": "gruvbox"})
	if refused.Error == nil || refused.Error.Code != api.CodeSessionUnavailable {
		t.Fatalf("post-shutdown mutation = %+v, want session_unavailable", refused.Error)
	}
	// Read-only requests may still be served while draining.
	if read := call(t, socket, "sources.list", nil); !read.OK {
		t.Fatalf("post-shutdown read = %+v, want ok", read.Error)
	}
}

func TestSecondServerConflicts(t *testing.T) {
	_, socket := startTestServer(t)
	if _, err := Start(Options{SocketPath: socket, Engine: fakeengine.NewFakeEngine()}); err == nil {
		t.Fatal("second Start succeeded on the same socket")
	}
}

func TestLifecycleLockConflictsAcrossDifferentSockets(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	lockPath := filepath.Join(dir, "durable", "server.lock")
	first, err := Start(Options{
		SocketPath: filepath.Join(dir, "cache-a", "session.sock"),
		LockPath:   lockPath,
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "durable", "state.json")),
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if _, err := Start(Options{
		SocketPath: filepath.Join(dir, "cache-b", "session.sock"),
		LockPath:   lockPath,
		Engine:     fakeengine.NewFakeEngine(),
	}); !errors.Is(err, ErrActive) {
		t.Fatalf("second Start = %v, want ErrActive", err)
	}
}
