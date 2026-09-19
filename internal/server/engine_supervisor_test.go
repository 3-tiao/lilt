package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

// supervisedEngine is a fake engine whose state update stream the test controls.
type supervisedEngine struct {
	*fakeengine.FakeEngine
	updates chan core.PlaybackStateUpdate
}

func newSupervisedEngine() *supervisedEngine {
	return &supervisedEngine{FakeEngine: fakeengine.NewFakeEngine(), updates: make(chan core.PlaybackStateUpdate)}
}

func (e *supervisedEngine) SubscribeState(context.Context) (core.StateSubscription, error) {
	return core.StateSubscription{Updates: e.updates}, nil
}

func TestRequireEngineRestarting(t *testing.T) {
	restarting := &Server{engineRestarting: true, canRestart: true}
	if err := restarting.requireEngine(); err == nil || err.Code != api.CodeEngineRestarting {
		t.Fatalf("restarting error = %v", err)
	}
	unavailable := &Server{}
	if err := unavailable.requireEngine(); err == nil || err.Code != api.CodeSourceUnavailable {
		t.Fatalf("unavailable error = %v", err)
	}
	ready := &Server{engine: newSupervisedEngine()}
	if err := ready.requireEngine(); err != nil {
		t.Fatalf("ready error = %v", err)
	}
}

func TestRequireEngineStartsAvailableFactory(t *testing.T) {
	engine := newSupervisedEngine()
	server := &Server{engineFactory: func() (Engine, error) { return engine, nil }}

	if err := server.requireEngine(); err != nil {
		t.Fatalf("requireEngine: %v", err)
	}
	if server.currentEngine() != engine {
		t.Fatal("requireEngine did not attach the factory engine")
	}
}

func TestEngineRebuildPublishesLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	var mu sync.Mutex
	built := 0
	engines := make(chan *supervisedEngine, 4)
	factory := func() (Engine, error) {
		engine := newSupervisedEngine()
		mu.Lock()
		built++
		mu.Unlock()
		engines <- engine
		return engine, nil
	}
	server, err := Start(Options{
		SocketPath:    socket,
		EngineFactory: factory,
		Store:         state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	first := <-engines

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, nil, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer watcher.Close()
	if !response.OK {
		t.Fatalf("watch initial = %+v", response.Error)
	}
	server.mu.Lock()
	server.activeSource = api.SourceRadio
	server.playbackGeneration = 9
	server.transportSessionID = "stale-session"
	server.mu.Unlock()

	// Simulate helper death: the update stream closes.
	close(first.updates)

	seen := map[string]bool{}
	var reset api.PlaybackState
	want := []string{"server.warning", "engine.restarted", "sources.changed", "playback.changed"}
	deadline := time.After(4 * time.Second)
	for !allSeen(seen, want) {
		select {
		case event := <-watcher.Events:
			seen[event.Event] = true
			if event.Event == "playback.changed" {
				var payload struct {
					State api.PlaybackState `json:"state"`
				}
				_ = json.Unmarshal(event.Data, &payload)
				reset = payload.State
			}
		case <-deadline:
			t.Fatalf("missing lifecycle events; saw %v", seen)
		}
	}
	mu.Lock()
	count := built
	mu.Unlock()
	if count < 2 {
		t.Fatalf("factory built %d engines, want at least 2", count)
	}
	server.mu.Lock()
	generation, sessionID, activeSource := server.playbackGeneration, server.transportSessionID, server.activeSource
	server.mu.Unlock()
	if generation != 10 || sessionID != "" || activeSource != api.SourceRadio {
		t.Fatalf("restart session generation=%d id=%q source=%q", generation, sessionID, activeSource)
	}
	if reset.Status != "stopped" || reset.Source != api.SourceRadio {
		t.Fatalf("restart reset=%+v", reset)
	}

	// The rebuilt engine accepts commands again.
	played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if !played.OK {
		t.Fatalf("play after rebuild failed: %+v", played.Error)
	}
}

// authEngine models a helper whose authorization settles after launch: the
// descriptor was built while the handshake was incomplete, and the state
// stream carries the transition.
type authEngine struct {
	*fakeengine.FakeEngine
	updates chan core.PlaybackStateUpdate
	auth    string
}

func (e *authEngine) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: e.auth, CanPlayCatalogContent: e.auth == "authorized", HasCloudLibraryEnabled: e.auth == "authorized"}, nil
}

func (e *authEngine) SubscribeState(context.Context) (core.StateSubscription, error) {
	return core.StateSubscription{Updates: e.updates}, nil
}

// The helper settling its authorization handshake after launch silently flips
// Apple Music capabilities; the server must republish sources.changed on that
// transition or watch clients keep the pre-settle descriptor snapshot.
func TestEngineAuthorizationTransitionPublishesSourcesChanged(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	engine := &authEngine{FakeEngine: fakeengine.NewFakeEngine(), updates: make(chan core.PlaybackStateUpdate, 1), auth: "denied"}
	server, err := Start(Options{
		SocketPath:    socket,
		EngineFactory: func() (Engine, error) { return engine, nil },
		Store:         state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, nil, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer watcher.Close()
	if !response.OK {
		t.Fatalf("watch initial = %+v", response.Error)
	}
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(response.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if capabilityAvailability(snapshot.Sources, api.SourceAppleMusic, api.CapShuffle) {
		t.Fatalf("initial snapshot reports shuffle available while the helper is denied")
	}

	engine.auth = "authorized"
	engine.updates <- core.PlaybackStateUpdate{State: core.PlaybackState{Status: "playing", Authorization: "authorized"}}
	deadline := time.After(4 * time.Second)
	for {
		select {
		case event := <-watcher.Events:
			if event.Event != "sources.changed" {
				continue
			}
			var payload struct {
				Sources []api.SourceDescriptor `json:"sources"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if !capabilityAvailability(payload.Sources, api.SourceAppleMusic, api.CapShuffle) {
				t.Fatalf("transition sources.changed still reports shuffle unavailable")
			}
			return
		case <-deadline:
			t.Fatal("authorization transition did not publish sources.changed")
		}
	}
}

func capabilityAvailability(sources []api.SourceDescriptor, source api.SourceID, capability string) bool {
	for _, descriptor := range sources {
		if descriptor.ID != source {
			continue
		}
		return descriptor.Capabilities[capability].Available
	}
	return false
}

func TestEngineRestartingRejectsCommands(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server, err := Start(Options{SocketPath: socket, Engine: newSupervisedEngine(), Store: state.New(filepath.Join(dir, "state.json"))})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	server.mu.Lock()
	server.engineRestarting = true
	server.mu.Unlock()

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if response.Error == nil || response.Error.Code != api.CodeEngineRestarting {
		t.Fatalf("response = %+v, want engine_restarting", response.Error)
	}
}

func allSeen(seen map[string]bool, want []string) bool {
	for _, name := range want {
		if !seen[name] {
			return false
		}
	}
	return true
}

type closeableEngine struct {
	*supervisedEngine
	mu     sync.Mutex
	closed bool
}

func (e *closeableEngine) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

// Shutdown stops accepting mutating commands and closes the engine (helper).
func TestShutdownClosesEngineAndRejectsMutations(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	engine := &closeableEngine{supervisedEngine: newSupervisedEngine()}
	server, err := Start(Options{SocketPath: socket, Engine: engine, Store: state.New(filepath.Join(dir, "state.json"))})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	server.triggerShutdown()

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if response.Error == nil || response.Error.Code != api.CodeSessionUnavailable {
		t.Fatalf("draining mutation = %+v, want session_unavailable", response.Error)
	}
	// Reads still work while draining.
	if status := call(t, socket, "session.status", nil); !status.OK {
		t.Fatalf("draining read failed: %+v", status.Error)
	}
	_ = server.Close()
	engine.mu.Lock()
	closed := engine.closed
	engine.mu.Unlock()
	if !closed {
		t.Fatal("engine was not closed on shutdown")
	}
}
