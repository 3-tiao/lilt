package server

import (
	"context"
	"encoding/json"
	"errors"
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
