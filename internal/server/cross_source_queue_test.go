package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/state"
)

// TestQueueAddRejectsAnotherSourceWhileURLQueueIsActive pins the invariant that
// a ref only ever joins the queue that is actually playing. The mismatch guard
// used to sit behind a non-empty check on the engine's own queue, and switching
// to the URL transport stops the engine without closing it, which empties its
// queue — so a foreign ref fell through to the stopped engine, answered OK, and
// broadcast that engine's state under the still-active Audius source.
func TestQueueAddRejectsAnotherSourceWhileURLQueueIsActive(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	_, socket, _ := startAudiusPlaybackServer(t, upstream, &recordingURLDriver{})

	played := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"})
	if !played.OK {
		t.Fatalf("play: %+v", played.Error)
	}
	var before api.PlaybackState
	if err := json.Unmarshal(played.Data, &before); err != nil {
		t.Fatal(err)
	}
	if before.QueueSource == nil || *before.QueueSource != api.SourceAudius {
		t.Fatalf("queue owner = %+v, want audius", before.QueueSource)
	}
	beforeItems := len(before.Queue)

	for _, ref := range []string{"apple-music:song:1", "jamendo:song:1"} {
		response := call(t, socket, "queue.add", map[string]any{"ref": ref, "position": "append"})
		if response.OK {
			t.Fatalf("queue.add accepted %q from another source: %s", ref, response.Data)
		}
		if response.Error.Code != api.CodeSourceMismatch {
			t.Fatalf("queue.add %q error = %+v, want %s", ref, response.Error, api.CodeSourceMismatch)
		}
	}

	// The audible queue is untouched: same owner, same contents, same revision.
	listed := call(t, socket, "queue.list", nil)
	var queue api.QueueState
	if err := json.Unmarshal(listed.Data, &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Source == nil || *queue.Source != api.SourceAudius {
		t.Fatalf("queue owner = %+v, want audius", queue.Source)
	}
	if len(queue.Items) != beforeItems {
		t.Fatalf("queue grew from %d to %d after rejected adds", beforeItems, len(queue.Items))
	}
	if queue.QueueRevision != before.QueueRevision {
		t.Fatalf("queue revision moved from %d to %d on a rejected add",
			before.QueueRevision, queue.QueueRevision)
	}
}

// requireQueueUnavailable asserts that an engine-source queue.add is refused
// while a non-engine transport owns playback, and that the refusal leaves both
// the public queue and the engine's own queue untouched. The queue is observed
// through session.status includeQueue: queue.list rides the same ownership
// denial as the edit family here, so it has nothing to answer either.
func requireQueueUnavailable(t *testing.T, socket string, engine Engine) {
	t.Helper()
	before := sessionState(t, socket)

	response := call(t, socket, "queue.add", map[string]any{"ref": "apple-music:song:1", "position": "append"})
	if response.OK {
		t.Fatalf("queue.add apple ref reached a silent backend: %s", response.Data)
	}
	if response.Error.Code != api.CodeQueueUnavailable {
		t.Fatalf("queue.add apple ref error = %+v, want %s", response.Error, api.CodeQueueUnavailable)
	}

	// The public queue stays empty and its revision does not move.
	after := sessionState(t, socket)
	if after.QueueSource != nil || len(after.Queue) != 0 {
		t.Fatalf("queue gained contents from a rejected add: %+v", after)
	}
	if after.QueueRevision != before.QueueRevision {
		t.Fatalf("queue revision moved from %d to %d on a rejected add",
			before.QueueRevision, after.QueueRevision)
	}
	// The stopped engine was never asked to enqueue either.
	if state, err := engine.State(context.Background()); err == nil && len(state.Queue) != 0 {
		t.Fatalf("the silent backend queue was filled: %+v", state.Queue)
	}
}

// TestQueueAddRefusesEngineRefWhenURLQueueEmpties pins the same hole one step
// further: queue.clear and a natural end both empty the URL queue without
// resetting activeTransport, so queueOwnerLocked reports no owner there. The
// engine path then lazily started a MusicKit helper and answered OK over its
// stopped, invisible queue (2026-10-03 review round 1).
func TestQueueAddRefusesEngineRefWhenURLQueueEmpties(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()

	t.Run("cleared", func(t *testing.T) {
		driver := &recordingURLDriver{}
		_, socket, engine := startAudiusPlaybackServer(t, upstream, driver)
		if played := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !played.OK {
			t.Fatalf("play: %+v", played.Error)
		}
		if cleared := call(t, socket, "queue.clear", nil); !cleared.OK {
			t.Fatalf("queue.clear: %+v", cleared.Error)
		}
		requireQueueUnavailable(t, socket, engine)
	})

	t.Run("drained", func(t *testing.T) {
		driver := &recordingURLDriver{}
		_, socket, engine := startAudiusPlaybackServer(t, upstream, driver)
		if played := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !played.OK {
			t.Fatalf("play: %+v", played.Error)
		}
		for range 2 {
			target := driver.last()
			engine.publish(core.PlaybackStateUpdate{State: core.PlaybackState{
				Status:             "stopped",
				Ended:              true,
				PlaybackGeneration: target.PlaybackGeneration,
				TransportSessionID: target.TransportSessionID,
			}})
		}
		waitForStatus(t, socket, func(s api.PlaybackState) bool {
			return s.Status == "stopped" && len(s.Queue) == 0 && s.QueueSource == nil
		})
		requireQueueUnavailable(t, socket, engine)
	})
}

// TestQueueAddRefusesEngineRefDuringStreamPlayback covers the stream
// transport: radio playback owns lilt-audio and there is no editable queue at
// all, so an engine ref must answer queue_unavailable instead of starting the
// MusicKit helper behind the live stream.
func TestQueueAddRefusesEngineRefDuringStreamPlayback(t *testing.T) {
	music := &routingMusic{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startRoutingServer(t, music, &routingAudio{}, "")

	if played := call(t, socket, "playback.play", map[string]any{"ref": "https://radio.example/live"}); !played.OK {
		t.Fatalf("radio play: %+v", played.Error)
	}
	requireQueueUnavailable(t, socket, music)
}

// queueFamilyEdits is the queue command family: list, add, jump, remove, move
// and clear must route through one shared ownership decision instead of each
// handler growing its own transport guard (wire-0 only patched add; queue.list
// was the last member still lazily starting the parked engine).
var queueFamilyEdits = []struct {
	command string
	params  map[string]any
}{
	{"queue.list", map[string]any{}},
	{"queue.add", map[string]any{"ref": "apple-music:song:1", "position": "append"}},
	{"queue.jump", map[string]any{"index": 0}},
	{"queue.remove", map[string]any{"index": 0}},
	{"queue.move", map[string]any{"from": 0, "to": 1}},
	{"queue.clear", map[string]any{}},
}

// requireQueueFamilyUnavailable pins the whole family, not just queue.add:
// while no transport owns an editable queue, every member answers
// queue_unavailable, the public state keeps an empty queue with an unmoved
// revision, and the silent backend's own queue is never filled.
func requireQueueFamilyUnavailable(t *testing.T, socket string, engine Engine) {
	t.Helper()
	before := sessionState(t, socket)
	for _, edit := range queueFamilyEdits {
		response := call(t, socket, edit.command, edit.params)
		if response.OK {
			t.Fatalf("%s reached a silent backend: %s", edit.command, response.Data)
		}
		if response.Error.Code != api.CodeQueueUnavailable {
			t.Fatalf("%s error = %+v, want %s", edit.command, response.Error, api.CodeQueueUnavailable)
		}
	}
	after := sessionState(t, socket)
	if len(after.Queue) != 0 || after.QueueSource != nil {
		t.Fatalf("queue gained contents from refused edits: %+v", after)
	}
	if after.QueueRevision != before.QueueRevision {
		t.Fatalf("queue revision moved from %d to %d on refused edits",
			before.QueueRevision, after.QueueRevision)
	}
	if state, err := engine.State(context.Background()); err == nil && len(state.Queue) != 0 {
		t.Fatalf("the silent backend queue was filled: %+v", state.Queue)
	}
}

// TestQueueFamilyRefusesEditsDuringStreamPlayback is the family-wide pin
// behind wire-0: during stream playback the MusicKit engine is parked (closed
// and detached), and remove/jump/move used to lazily start a fresh helper
// beside the live stream just to answer an error, while clear answered a
// meaningless success over that freshly started, empty engine. No member may
// touch the engine factory.
func TestQueueFamilyRefusesEditsDuringStreamPlayback(t *testing.T) {
	music := &routingMusic{FakeEngine: fakeengine.NewFakeEngine()}
	var mu sync.Mutex
	built := 0
	factory := func() (Engine, error) {
		mu.Lock()
		built++
		mu.Unlock()
		return music, nil
	}
	audio := &routingAudio{}
	dir, err := os.MkdirTemp("/tmp", "lilt-queue-family-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := Start(Options{
		SocketPath:         socket,
		EngineFactory:      factory,
		Store:              state.New(filepath.Join(dir, "state.json")),
		AudioEngineFactory: func() (AudioEngine, error) { return audio, nil },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	mu.Lock()
	startBuilds := built
	mu.Unlock()
	if startBuilds != 1 {
		t.Fatalf("factory built %d engines at start, want 1", startBuilds)
	}

	if played := call(t, socket, "playback.play", map[string]any{"ref": "https://radio.example/live"}); !played.OK {
		t.Fatalf("radio play: %+v", played.Error)
	}
	music.mu.Lock()
	parked := music.closed
	music.mu.Unlock()
	if !parked || srv.currentEngine() != nil {
		t.Fatal("radio playback did not park the MusicKit engine")
	}

	requireQueueFamilyUnavailable(t, socket, music)

	mu.Lock()
	total := built
	mu.Unlock()
	if total != startBuilds {
		t.Fatalf("the queue family built the parked engine %d more time(s)", total-startBuilds)
	}
	if srv.currentEngine() != nil {
		t.Fatal("a queue edit re-attached the parked engine beside the live stream")
	}
}

// TestQueueFamilyRefusesEditsWhenURLQueueEmpties extends the wire-0 pin from
// queue.add to the whole family: clearing or draining the URL queue leaves
// activeTransport set, so no backend owns the editable queue anymore. clear
// used to answer a meaningless success over the emptied session.
func TestQueueFamilyRefusesEditsWhenURLQueueEmpties(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()

	start := func(t *testing.T) (string, *recordingURLDriver, *publishableEngine, func() int) {
		t.Helper()
		engine := newPublishableEngine()
		var mu sync.Mutex
		built := 0
		driver := &recordingURLDriver{}
		dir, err := os.MkdirTemp("/tmp", "lilt-queue-family-url-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		socket := filepath.Join(dir, "s.sock")
		client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
		if _, err := Start(Options{
			SocketPath: socket,
			EngineFactory: func() (Engine, error) {
				mu.Lock()
				built++
				mu.Unlock()
				return engine, nil
			},
			Store:             state.New(filepath.Join(dir, "state.json")),
			AudiusClient:      &client,
			URLPlaybackDriver: driver,
		}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		return socket, driver, engine, func() int { mu.Lock(); defer mu.Unlock(); return built }
	}

	t.Run("cleared", func(t *testing.T) {
		socket, _, engine, built := start(t)
		if played := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !played.OK {
			t.Fatalf("play: %+v", played.Error)
		}
		// Clearing a live queue is the one clear that succeeds.
		if cleared := call(t, socket, "queue.clear", nil); !cleared.OK {
			t.Fatalf("queue.clear over a live queue: %+v", cleared.Error)
		}
		requireQueueFamilyUnavailable(t, socket, engine)
		if built() != 1 {
			t.Fatalf("the queue family built the engine %d times, want the single start build", built())
		}
	})

	t.Run("drained", func(t *testing.T) {
		socket, driver, engine, built := start(t)
		if played := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !played.OK {
			t.Fatalf("play: %+v", played.Error)
		}
		for range 2 {
			target := driver.last()
			engine.publish(core.PlaybackStateUpdate{State: core.PlaybackState{
				Status:             "stopped",
				Ended:              true,
				PlaybackGeneration: target.PlaybackGeneration,
				TransportSessionID: target.TransportSessionID,
			}})
		}
		waitForStatus(t, socket, func(s api.PlaybackState) bool {
			return s.Status == "stopped" && len(s.Queue) == 0 && s.QueueSource == nil
		})
		requireQueueFamilyUnavailable(t, socket, engine)
		if built() != 1 {
			t.Fatalf("the queue family built the engine %d times, want the single start build", built())
		}
	})
}
