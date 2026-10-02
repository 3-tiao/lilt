package server

import (
	"encoding/json"
	"testing"

	"github.com/3-tiao/lilt/internal/api"
)

// TestQueueAddRejectsAnotherSourceWhileURLQueueIsActive pins the invariant that
// a ref only ever joins the queue that is actually playing. The mismatch guard
// used to sit behind a non-empty check, and switching to the URL transport stops
// (but does not close) the engine, which empties its queue — so a foreign ref
// fell through to the stopped engine, answered OK, and broadcast that engine's
// state under the still-active Audius source.
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
