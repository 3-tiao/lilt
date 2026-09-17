package server

import (
	"context"
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

	// Simulate helper death: the update stream closes.
	close(first.updates)

	seen := map[string]bool{}
	want := []string{"server.warning", "engine.restarted", "sources.changed", "playback.changed"}
	deadline := time.After(4 * time.Second)
	for !allSeen(seen, want) {
		select {
		case event := <-watcher.Events:
			seen[event.Event] = true
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

	// The rebuilt engine accepts commands again.
	played := call(t, socket, "playback.play", map[string]any{"ref": "song:1"})
	if !played.OK {
		t.Fatalf("play after rebuild failed: %+v", played.Error)
	}
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

	response := call(t, socket, "playback.play", map[string]any{"ref": "song:1"})
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

	response := call(t, socket, "playback.play", map[string]any{"ref": "song:1"})
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
