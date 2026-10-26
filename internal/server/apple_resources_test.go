package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/state"
)

// authedResource stands in for a reachable Apple resource helper whose
// subscription read has settled: the descriptor derived from it declares
// shuffle/queue available.
type authedResource struct {
	fakeengine.FakeEngine
}

func (r *authedResource) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready", CanPlayCatalogContent: true}, nil
}

// settlingResource reproduces the real helper's asynchronous subscription
// read: authorized immediately, AccountStatus empty until the settle tick.
type settlingResource struct {
	fakeengine.FakeEngine
	mu sync.Mutex
	// settleAt zero means only settleNow() settles the read.
	settleAt time.Time
	settle   bool
}

func (r *settlingResource) Authorization(context.Context) (core.AuthorizationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settle || (!r.settleAt.IsZero() && !time.Now().Before(r.settleAt)) {
		return core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready", CanPlayCatalogContent: true}, nil
	}
	return core.AuthorizationStatus{Status: "authorized"}, nil
}

func (r *settlingResource) settleNow() {
	r.mu.Lock()
	r.settle = true
	r.mu.Unlock()
}

// A cold start used to publish "still being read" as the verdict: the first
// sources.list in the process raced the helper's subscription read and reported
// apple-music degraded with full playback unavailable, so automation that trusts
// the descriptor silently picked a lower-priority source. Resource creation now
// waits for the read within a bounded window instead.
func TestSourcesListWaitsForAppleAccountSettle(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-res-cold-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resource := &settlingResource{settleAt: time.Now().Add(300 * time.Millisecond)}
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		AppleResourceFactory: func() (AppleResourceClient, error) {
			return resource, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// One call, no retry: the descriptor must already carry the settled facts.
	response := call(t, srv.path, "sources.list", nil)
	if !response.OK {
		t.Fatalf("sources.list: %+v", response.Error)
	}
	var sources []api.SourceDescriptor
	if err := json.Unmarshal(response.Data, &sources); err != nil {
		t.Fatalf("decode sources.list: %v", err)
	}
	for _, descriptor := range sources {
		if descriptor.ID != api.SourceAppleMusic {
			continue
		}
		if descriptor.Availability != api.AvailabilityReady {
			t.Fatalf("cold-start availability = %q (%s), want ready", descriptor.Availability, descriptor.Reason)
		}
		for _, name := range []string{api.CapPlaybackFull, api.CapQueue, api.CapShuffle, api.CapRepeat} {
			if !descriptor.Capabilities[name].Available {
				t.Fatalf("cold-start %s unavailable: %+v", name, descriptor.Capabilities[name])
			}
		}
		return
	}
	t.Fatal("sources.list reported no apple-music descriptor")
}

// A capability transition must advance the watch sequence; otherwise TUI
// discards it as older than the initial snapshot.
func TestAppleResourceChangeAdvancesWatchSequence(t *testing.T) {
	var available atomic.Bool
	dir, err := os.MkdirTemp("/tmp", "lilt-res-seq-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		AppleResourceFactory: func() (AppleResourceClient, error) {
			if !available.Load() {
				return nil, errors.New("resource unavailable")
			}
			return &authedResource{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	initial, watcher, err := api.Watch(ctx, srv.path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(initial.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	available.Store(true)
	if response := call(t, srv.path, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list: %+v", response.Error)
	}
	// The capability transition must be newer than the snapshot it follows.
	select {
	case event := <-watcher.Events:
		if event.Event != "sources.changed" || event.Sequence <= snapshot.Sequence {
			t.Fatalf("event = %+v, snapshot sequence = %d", event, snapshot.Sequence)
		}
	case <-ctx.Done():
		t.Fatal("no resource-ready event")
	}
}

// The Apple resource runtime starts lazily, so a sources.list answered before
// it is ready carries degraded capabilities. The server must republish
// sources.changed when the runtime becomes reachable — that is the only signal
// a watch client gets (usability batch 2026-09-21-r13).
func TestAppleResourceReadyPublishesSourcesChanged(t *testing.T) {
	var available atomic.Bool
	dir, err := os.MkdirTemp("/tmp", "lilt-res-ready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		AppleResourceFactory: func() (AppleResourceClient, error) {
			if !available.Load() {
				return nil, errors.New("resource unavailable")
			}
			return &authedResource{}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, watcher, err := api.Watch(ctx, srv.path, nil, false)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer watcher.Close()

	available.Store(true)
	if response := call(t, srv.path, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list: %+v", response.Error)
	}

	deadline := time.After(2 * time.Second)
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
				t.Fatalf("decode sources.changed: %v", err)
			}
			for _, descriptor := range payload.Sources {
				if descriptor.ID == api.SourceAppleMusic {
					if !descriptor.Capabilities[api.CapShuffle].Available {
						t.Fatalf("ready event still hides shuffle: %+v", descriptor.Capabilities)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("no sources.changed event after the resource runtime became ready")
		}
	}
}

// The helper's subscription read settles asynchronously: the first descriptor
// answers "still being read" with full-playback capabilities unavailable. The
// server must publish sources.changed when the read settles — that is what
// un-sticks the TUI's capability-gated UI without a restart. This covers the
// helper that needs longer than the bounded creation wait; the fast case is
// TestSourcesListWaitsForAppleAccountSettle.
func TestAppleAccountSettlePublishesSourcesChanged(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-res-settle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resource := &settlingResource{}
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		AppleResourceFactory: func() (AppleResourceClient, error) {
			return resource, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	initial, watcher, err := api.Watch(ctx, srv.path, nil, false)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer watcher.Close()
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(initial.Data, &snapshot); err != nil {
		t.Fatal(err)
	}

	resource.settleNow()

	deadline := time.After(20 * time.Second)
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
				t.Fatalf("decode sources.changed: %v", err)
			}
			for _, descriptor := range payload.Sources {
				if descriptor.ID == api.SourceAppleMusic && descriptor.Capabilities[api.CapShuffle].Available {
					if event.Sequence <= snapshot.Sequence {
						t.Fatalf("settled event sequence %d must exceed snapshot %d", event.Sequence, snapshot.Sequence)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("no sources.changed event after the account capabilities settled")
		}
	}
}

// Losing the resource runtime is the same kind of capability transition: a
// watch client that once saw available capabilities must learn they are gone.
// The lazy runtime recreates on demand, so the gone-state only shows in the
// descriptor when recreation fails too — exactly how a dead helper behaves.
func TestAppleResourceInvalidationPublishesSourcesChanged(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-res-down-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resource := &authedResource{}
	starts := 0
	srv, err := Start(Options{
		SocketPath: filepath.Join(dir, "s.sock"),
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		AppleResourceFactory: func() (AppleResourceClient, error) {
			starts++
			if starts == 1 {
				return resource, nil
			}
			return nil, api.Errorf(api.CodeSourceUnavailable, "helper is gone")
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if response := call(t, srv.path, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list: %+v", response.Error)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	initial, watcher, err := api.Watch(ctx, srv.path, nil, false)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer watcher.Close()
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(initial.Data, &snapshot); err != nil {
		t.Fatal(err)
	}

	srv.invalidateAppleResource(resource)

	deadline := time.After(2 * time.Second)
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
				t.Fatalf("decode sources.changed: %v", err)
			}
			for _, descriptor := range payload.Sources {
				if descriptor.ID == api.SourceAppleMusic {
					if descriptor.Capabilities[api.CapShuffle].Available {
						// Recreation is legitimate; keep waiting for the event
						// that reports the runtime as gone.
						continue
					}
					if event.Sequence <= snapshot.Sequence {
						t.Fatalf("invalidation event sequence %d must exceed snapshot %d", event.Sequence, snapshot.Sequence)
					}
					return
				}
			}
		case <-deadline:
			t.Fatal("no sources.changed event after the resource runtime was invalidated")
		}
	}
}
