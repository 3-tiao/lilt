package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/player"
)

type uncertainUndoEngine struct {
	*fakeengine.FakeEngine
	failRemove  bool
	failRestore bool
}

func (e *uncertainUndoEngine) QueueRemove(ctx context.Context, index int) (core.QueueRemoveOutcome, error) {
	if e.failRemove {
		return core.QueueRemoveOutcome{}, &player.TransportError{Err: errors.New("lost queueRemove response")}
	}
	return e.FakeEngine.QueueRemove(ctx, index)
}

func (e *uncertainUndoEngine) QueueRestore(ctx context.Context, handle string) (core.PlaybackState, error) {
	if e.failRestore {
		return core.PlaybackState{}, &player.TransportError{Err: errors.New("lost queueRestore response")}
	}
	return e.FakeEngine.QueueRestore(ctx, handle)
}

func removeResult(t *testing.T, socket string, index int, revision uint64) api.QueueRemoveResult {
	t.Helper()
	response := call(t, socket, "queue.remove", map[string]any{"index": index, "ifQueueRevision": revision})
	if !response.OK {
		t.Fatalf("queue.remove: %+v", response.Error)
	}
	var result api.QueueRemoveResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestQueueUndoRestoresExactFutureItemAtomically(t *testing.T) {
	_, socket := startTestServer(t)
	started := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c"}})
	if !started.OK {
		t.Fatalf("playSongs: %+v", started.Error)
	}
	before := sessionState(t, socket)
	removed := removeResult(t, socket, 1, before.QueueRevision)
	if removed.Undo == nil || len(removed.State.Queue) != 2 || removed.State.Queue[1].ProviderID != "c" {
		t.Fatalf("remove result = %+v", removed)
	}
	response := call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": removed.State.QueueRevision})
	if !response.OK {
		t.Fatalf("undo: %+v", response.Error)
	}
	var restored api.PlaybackState
	if err := json.Unmarshal(response.Data, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Queue) != 3 || restored.Queue[0].ProviderID != "a" || restored.Queue[1].ProviderID != "b" || restored.Queue[2].ProviderID != "c" {
		t.Fatalf("restored queue = %+v", restored.Queue)
	}
	if restored.QueueRevision != removed.State.QueueRevision+1 {
		t.Fatalf("revision=%d want %d", restored.QueueRevision, removed.State.QueueRevision+1)
	}
}

func TestQueueUndoIsLatestOnlyAndCurrentRemovalHasNoOffer(t *testing.T) {
	_, socket := startTestServer(t)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c", "apple-music:song:d"}})
	first := removeResult(t, socket, 3, sessionState(t, socket).QueueRevision)
	second := removeResult(t, socket, 2, first.State.QueueRevision)
	if first.Undo == nil || second.Undo == nil {
		t.Fatalf("missing offers: first=%+v second=%+v", first.Undo, second.Undo)
	}
	stale := call(t, socket, "queue.undoRemove", map[string]any{"token": first.Undo.Token, "ifQueueRevision": first.State.QueueRevision})
	if stale.Error == nil || stale.Error.Code != api.CodeUndoUnavailable || stale.Error.Details["reason"] != "superseded" {
		t.Fatalf("stale undo = %+v", stale.Error)
	}
	current := removeResult(t, socket, 0, second.State.QueueRevision)
	if current.Undo != nil {
		t.Fatalf("current removal offered undo: %+v", current.Undo)
	}
}

func TestQueueUndoExpiresAndRevisionConflictBlocksRestore(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	server, socket := startTestServer(t)
	server.mu.Lock()
	server.now = func() time.Time { return now }
	server.queueUndoWindow = 5 * time.Second
	server.mu.Unlock()
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c"}})
	removed := removeResult(t, socket, 2, sessionState(t, socket).QueueRevision)
	now = now.Add(5 * time.Second)
	expired := call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": removed.State.QueueRevision})
	if expired.Error == nil || expired.Error.Code != api.CodeUndoUnavailable || expired.Error.Details["reason"] != "expired" {
		t.Fatalf("expired undo = %+v", expired.Error)
	}

	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c"}})
	removed = removeResult(t, socket, 2, sessionState(t, socket).QueueRevision)
	call(t, socket, "queue.move", map[string]any{"from": 0, "to": 1, "ifQueueRevision": removed.State.QueueRevision})
	conflict := call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": removed.State.QueueRevision})
	if conflict.Error == nil || conflict.Error.Code != api.CodeConflict {
		t.Fatalf("changed queue undo = %+v", conflict.Error)
	}
}

func TestQueueUndoRejectsNaturalPlaybackAdvanceWithoutRevisionChange(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	_, socket := startTestServerWithEngine(t, engine)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c"}})
	removed := removeResult(t, socket, 2, sessionState(t, socket).QueueRevision)
	if _, err := engine.QueueJump(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	response := call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": removed.State.QueueRevision})
	if response.Error == nil || response.Error.Code != api.CodeUndoUnavailable || response.Error.Details["reason"] != "playback_changed" {
		t.Fatalf("advanced undo = %+v", response.Error)
	}
}

func TestQueueRemoveAndUndoUnknownOutcomesInvalidateReceiptAndRevision(t *testing.T) {
	for _, operation := range []string{"remove", "undo"} {
		t.Run(operation, func(t *testing.T) {
			engine := &uncertainUndoEngine{FakeEngine: fakeengine.NewFakeEngine()}
			_, socket := startTestServerWithEngine(t, engine)
			call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:a", "apple-music:song:b", "apple-music:song:c", "apple-music:song:d"}})
			removed := removeResult(t, socket, 3, sessionState(t, socket).QueueRevision)
			if removed.Undo == nil {
				t.Fatal("first removal did not offer undo")
			}
			before := removed.State.QueueRevision
			var response api.Response
			if operation == "remove" {
				engine.failRemove = true
				response = call(t, socket, "queue.remove", map[string]any{"index": 2, "ifQueueRevision": before})
			} else {
				engine.failRestore = true
				response = call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": before})
			}
			if response.Error == nil || response.Error.Code != api.CodeOperationOutcomeUnknown {
				t.Fatalf("response=%+v", response)
			}
			if got := sessionState(t, socket).QueueRevision; got != before+1 {
				t.Fatalf("queueRevision=%d want %d", got, before+1)
			}
			stale := call(t, socket, "queue.undoRemove", map[string]any{"token": removed.Undo.Token, "ifQueueRevision": before})
			if stale.Error == nil || stale.Error.Code != api.CodeUndoUnavailable {
				t.Fatalf("stale receipt survived unknown outcome: %+v", stale)
			}
		})
	}
}

func TestURLQueueUndoUsesSingleLockAndDoesNotResolveMedia(t *testing.T) {
	// Audius, Jamendo, and the signed-in Apple browser all use this transport;
	// exercise the same exact-object contract for each public source.
	for _, source := range []api.SourceID{api.SourceAudius, api.SourceJamendo, api.SourceAppleMusic} {
		t.Run(string(source), func(t *testing.T) {
			resolves := 0
			item := func(id, title string) api.Item {
				return api.Item{Source: source, Kind: api.KindSong, ProviderID: id, ID: string(source) + ":song:" + id, Title: title}
			}
			transport := NewURLQueueTransport(&flakyURLDriver{})
			plan := NewURLQueuePlan(source, []api.Item{item("a", "A"), item("b", "B"), item("c", "C")}, 0, func(context.Context, api.Item) (urlResolution, error) {
				resolves++
				return urlResolution{URL: "https://signed.invalid/media"}, nil
			})
			if _, err := transport.Start(context.Background(), plan, 7, "session"); err != nil {
				t.Fatal(err)
			}
			outcome, undo, err := transport.Remove(context.Background(), 1)
			if err != nil || undo == nil || len(outcome.State.Queue) != 2 {
				t.Fatalf("remove outcome=%+v undo=%+v err=%v", outcome, undo, err)
			}
			restored, err := transport.RestoreRemoved(context.Background(), *undo)
			if err != nil {
				t.Fatal(err)
			}
			if resolves != 1 || len(restored.Queue) != 3 || restored.Queue[1].ID != "b" {
				t.Fatalf("resolves=%d queue=%+v", resolves, restored.Queue)
			}
			if _, err := transport.RestoreRemoved(context.Background(), *undo); !errors.Is(err, core.ErrQueueUndoUnavailable) {
				t.Fatalf("second restore err=%v", err)
			}
		})
	}
}

func TestURLQueueRemovalDoesNotOfferUndoForNonSong(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "a", ID: "am:a", Title: "A"},
		{Source: api.SourceAppleMusic, Kind: api.KindStation, ProviderID: "station", ID: "am:station", Title: "Station"},
	}
	// URLQueuePlan correctly rejects non-song finite queues. Seed the transport
	// directly to assert the defense-in-depth rule at the removal boundary too.
	transport := NewURLQueueTransport(&flakyURLDriver{})
	transport.source = api.SourceAppleMusic
	transport.items = items
	transport.index = 0
	transport.generation = 2
	transport.sessionID = "session"
	transport.last = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0}
	if err := transport.requireSessionLocked(); err != nil {
		t.Fatal(err)
	}
	_, undo, err := transport.Remove(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if undo != nil {
		t.Fatalf("non-song removal offered undo: %+v", undo)
	}
}
