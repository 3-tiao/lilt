package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/player"
)

func TestPlaybackPlayAndStatus(t *testing.T) {
	_, socket := startTestServer(t)
	played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1440845629"})
	if !played.OK {
		t.Fatalf("play failed: %+v", played.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(played.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "playing" || state.Source != api.SourceAppleMusic {
		t.Fatalf("state = %+v", state)
	}
	if state.Track == nil || state.Track.Ref != "apple-music:song:1440845629" {
		t.Fatalf("track = %+v", state.Track)
	}
	queue := call(t, socket, "queue.list", nil)
	if !queue.OK {
		t.Fatalf("queue.list failed: %+v", queue.Error)
	}
	var queueState api.QueueState
	if err := json.Unmarshal(queue.Data, &queueState); err != nil {
		t.Fatal(err)
	}
	if queueState.Source == nil || *queueState.Source != api.SourceAppleMusic || len(queueState.Items) == 0 {
		t.Fatalf("queue = %+v", queueState)
	}
}

func TestPlaySongsSourceMismatch(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:1", "audius:song:abc"},
	})
	if response.Error == nil || response.Error.Code != api.CodeSourceMismatch {
		t.Fatalf("response = %+v, want source_mismatch", response.Error)
	}
}

func TestQueueRevisionConflict(t *testing.T) {
	_, socket := startTestServer(t)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1", "apple-music:song:2"}})
	stale := uint64(0)
	response := call(t, socket, "queue.remove", map[string]any{"index": 0, "ifQueueRevision": stale})
	if response.Error == nil || response.Error.Code != api.CodeConflict {
		t.Fatalf("response = %+v, want conflict", response.Error)
	}
}

func TestFavoritesSetAndList(t *testing.T) {
	_, socket := startTestServer(t)
	item := api.Item{
		Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:42",
		ProviderID: "42", Ref: "apple-music:song:42", Title: "Favorite",
	}
	set := call(t, socket, "favorites.set", map[string]any{"item": item, "favorited": true})
	if !set.OK {
		t.Fatalf("favorites.set failed: %+v", set.Error)
	}
	list := call(t, socket, "favorites.list", nil)
	if !list.OK {
		t.Fatalf("favorites.list failed: %+v", list.Error)
	}
	var favorites []api.Item
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 || favorites[0].Ref != "apple-music:song:42" {
		t.Fatalf("favorites = %+v", favorites)
	}
	// Idempotent removal.
	remove := call(t, socket, "favorites.set", map[string]any{"item": item, "favorited": false})
	if !remove.OK {
		t.Fatalf("favorites.set(false) failed: %+v", remove.Error)
	}
	list = call(t, socket, "favorites.list", nil)
	_ = json.Unmarshal(list.Data, &favorites)
	if len(favorites) != 0 {
		t.Fatalf("favorites after removal = %+v", favorites)
	}
}

func TestRadioSearchBuiltinOnly(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "radio.search", map[string]any{"origin": api.OriginBuiltin, "limit": 5})
	if !response.OK {
		t.Fatalf("radio.search failed: %+v", response.Error)
	}
	var result api.RadioSearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) == 0 {
		t.Fatal("builtin search returned no stations")
	}
	for _, item := range result.Items {
		if item.Radio == nil || item.Radio.Origin != api.OriginBuiltin {
			t.Fatalf("item missing builtin origin: %+v", item)
		}
		if item.Source != api.SourceRadio {
			t.Fatalf("item source = %q", item.Source)
		}
	}
}

func TestRadioSearchDirectoryUnavailableDegrades(t *testing.T) {
	_, socket := startTestServer(t) // no radio client configured
	response := call(t, socket, "radio.search", map[string]any{"origin": "all", "limit": 5})
	if !response.OK {
		t.Fatalf("radio.search(all) failed: %+v", response.Error)
	}
	var result api.RadioSearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.DegradedOrigins) != 1 || result.DegradedOrigins[0].Origin != api.OriginDirectory {
		t.Fatalf("degraded = %+v", result.DegradedOrigins)
	}
	if len(result.Items) == 0 {
		t.Fatal("builtin fallback returned no items")
	}
}

func TestWatchPublishesPlaybackChange(t *testing.T) {
	_, socket := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, nil, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer watcher.Close()
	if !response.OK {
		t.Fatalf("watch initial = %+v", response.Error)
	}
	call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:7"})
	select {
	case event := <-watcher.Events:
		if event.Event != "playback.changed" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no playback.changed event")
	}
}

func TestUnknownWatchTopicRejected(t *testing.T) {
	_, socket := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, []string{"nope"}, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if watcher != nil {
		_ = watcher.Close()
	}
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("response = %+v, want invalid_request", response.Error)
	}
}

func TestAuthorizationStatusApple(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "authorization.status", map[string]any{"source": string(api.SourceAppleMusic)})
	if !response.OK {
		t.Fatalf("authorization.status failed: %+v", response.Error)
	}
	var authorization api.SourceAuthorization
	if err := json.Unmarshal(response.Data, &authorization); err != nil {
		t.Fatal(err)
	}
	if authorization.Source != api.SourceAppleMusic {
		t.Fatalf("authorization = %+v", authorization)
	}
}

func TestQueueRemoveIncrementsRevision(t *testing.T) {
	_, socket := startTestServer(t)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1", "apple-music:song:2"}})
	response := call(t, socket, "queue.remove", map[string]any{"index": 0})
	if !response.OK {
		t.Fatalf("remove failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.QueueRevision != 2 {
		t.Fatalf("queueRevision = %d, want 2 after remove", state.QueueRevision)
	}
}

func TestConflictIncludesLatestQueueDetails(t *testing.T) {
	_, socket := startTestServer(t)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1", "apple-music:song:2"}})
	stale := uint64(0)
	response := call(t, socket, "queue.remove", map[string]any{"index": 0, "ifQueueRevision": stale})
	if response.Error == nil || response.Error.Code != api.CodeConflict {
		t.Fatalf("response = %+v, want conflict", response.Error)
	}
	if response.Error.Details == nil || response.Error.Details["queueRevision"] == nil {
		t.Fatalf("conflict details missing queueRevision: %+v", response.Error.Details)
	}
	if response.Error.Details["queue"] == nil {
		t.Fatalf("conflict details missing queue state: %+v", response.Error.Details)
	}
}

func TestMissingRequiredParamsRejected(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "queue.remove", map[string]any{})
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("response = %+v, want invalid_request", response.Error)
	}
}

type recordingEngine struct {
	*fakeengine.FakeEngine
	mu   sync.Mutex
	last core.PlaybackRequest
}

func (e *recordingEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	e.mu.Lock()
	e.last = request
	e.mu.Unlock()
	return e.FakeEngine.PlayState(ctx, request)
}

func TestPlayForwardsStartTrackAndReverse(t *testing.T) {
	engine := &recordingEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	response := call(t, socket, "playback.play", map[string]any{
		"ref":          "apple-music:playlist:pl.x",
		"startAt":      3,
		"startTrackID": "535824738",
		"reverse":      true,
		"fromHere":     true,
	})
	if !response.OK {
		t.Fatalf("play failed: %+v", response.Error)
	}
	engine.mu.Lock()
	last := engine.last
	engine.mu.Unlock()
	if last.StartAt != 3 || last.StartTrackID != "535824738" || !last.Reverse || !last.FromHere {
		t.Fatalf("engine request = %+v", last)
	}
}

func TestRadioSearchBuiltinOmitsUnmatchedStructuredFilters(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "radio.search", map[string]any{"origin": api.OriginBuiltin, "tag": "jazz", "limit": 5})
	if !response.OK {
		t.Fatalf("radio.search failed: %+v", response.Error)
	}
	var result api.RadioSearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("builtin returned tag-unmatched stations: %+v", result.Items)
	}
}

type previewOnlyEngine struct{ *fakeengine.FakeEngine }

func (e *previewOnlyEngine) QueueJump(context.Context, int) (core.PlaybackState, error) {
	return core.PlaybackState{}, &player.RPCError{Code: "preview_unsupported", Message: "next and previous are unavailable in preview mode"}
}

func TestQueueJumpWithoutQueueReportsQueueUnavailable(t *testing.T) {
	engine := &previewOnlyEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	response := call(t, socket, "queue.jump", map[string]any{"index": 0})
	if response.Error == nil || response.Error.Code != api.CodeQueueUnavailable {
		t.Fatalf("queue.jump error = %+v, want queue_unavailable", response.Error)
	}
}
