package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
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
// the public queue and the engine's own queue untouched.
func requireQueueUnavailable(t *testing.T, socket string, engine Engine) {
	t.Helper()
	before := call(t, socket, "queue.list", nil)
	var beforeQueue api.QueueState
	if err := json.Unmarshal(before.Data, &beforeQueue); err != nil {
		t.Fatal(err)
	}

	response := call(t, socket, "queue.add", map[string]any{"ref": "apple-music:song:1", "position": "append"})
	if response.OK {
		t.Fatalf("queue.add apple ref reached a silent backend: %s", response.Data)
	}
	if response.Error.Code != api.CodeQueueUnavailable {
		t.Fatalf("queue.add apple ref error = %+v, want %s", response.Error, api.CodeQueueUnavailable)
	}

	// The public queue stays empty and its revision does not move.
	after := call(t, socket, "queue.list", nil)
	var afterQueue api.QueueState
	if err := json.Unmarshal(after.Data, &afterQueue); err != nil {
		t.Fatal(err)
	}
	if afterQueue.Source != nil || len(afterQueue.Items) != 0 {
		t.Fatalf("queue gained contents from a rejected add: %+v", afterQueue)
	}
	if afterQueue.QueueRevision != beforeQueue.QueueRevision {
		t.Fatalf("queue revision moved from %d to %d on a rejected add",
			beforeQueue.QueueRevision, afterQueue.QueueRevision)
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
