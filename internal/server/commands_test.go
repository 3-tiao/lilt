package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestLibraryAlbumsRouting(t *testing.T) {
	_, socket := startTestServer(t)

	response := call(t, socket, "library.albums", map[string]any{"source": "apple-music"})
	if !response.OK {
		t.Fatalf("apple albums failed: %+v", response.Error)
	}
	var items []api.Item
	if err := json.Unmarshal(response.Data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != api.KindAlbum || items[0].Ref != "apple-music:album:fake:album" {
		t.Fatalf("albums = %#v", items)
	}

	if response := call(t, socket, "library.albums", map[string]any{"source": "audius"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius albums = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "library.albums", map[string]any{"source": "radio"}); response.OK || response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("radio albums = %+v, want source_unavailable", response)
	}
}

// playlist.tracks must carry the playlist's own name: the helper resolves the
// Playlist object, so the server must not invent a title from the id
// (batch manual-20260920 OQ10).
func TestPlaylistTracksKeepsThePlaylistName(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "playlist.tracks", map[string]any{"ref": "apple-music:playlist:pl.abc"})
	if !response.OK {
		t.Fatalf("playlist.tracks failed: %+v", response.Error)
	}
	var result api.PlaylistTracksResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Playlist.Title != "Fake Library Playlist" || result.Playlist.Kind != api.KindPlaylist {
		t.Fatalf("playlist row = %#v", result.Playlist)
	}
	if result.Playlist.ID != "am:pl.abc" || result.Playlist.Ref != "apple-music:playlist:pl.abc" {
		t.Fatalf("playlist identity = %#v", result.Playlist)
	}
	if len(result.Items) != 2 {
		t.Fatalf("tracks = %#v", result.Items)
	}
}

// wedgedFillEngine models MusicKit dropping a freshly filled queue: it reports
// playing while the fill runs, then stops with the track still set, and refuses
// to start again.
type wedgedFillEngine struct {
	*fakeengine.FakeEngine
	mu      sync.Mutex
	stopped bool
}

func (e *wedgedFillEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	// The start itself succeeds; the queue is dropped while it is being filled.
	return e.FakeEngine.PlayState(ctx, request)
}

func (e *wedgedFillEngine) Enqueue(ctx context.Context, request core.PlaybackRequest, position string) (core.PlaybackState, error) {
	state, err := e.FakeEngine.Enqueue(ctx, request, position)
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	return state, err
}

func (e *wedgedFillEngine) State(ctx context.Context) (core.PlaybackState, error) {
	state, err := e.FakeEngine.State(ctx)
	e.mu.Lock()
	stopped := e.stopped
	e.mu.Unlock()
	if stopped {
		state.Status = "stopped"
	}
	return state, err
}

func (e *wedgedFillEngine) ResumeState(context.Context) (core.PlaybackState, error) {
	return core.PlaybackState{}, errors.New("MPMusicPlayerControllerErrorDomain Code=1")
}

// A fill that leaves the player stopped must be reported, not committed as a
// successful play with a full queue and Stopped playback. Users previously saw
// UP NEXT (1/12) next to Stopped 0:00 with no error at all
// (batch 2026-09-20-form-fix-recheck OQ13).
func TestWedgedQueueFillReportsPlaybackError(t *testing.T) {
	engine := &wedgedFillEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:s1", "apple-music:song:s2"}})
	if response.OK {
		t.Fatalf("wedged fill reported success: %s", response.Data)
	}
	if response.Error.Code != api.CodePlaybackError {
		t.Fatalf("error = %+v, want playback_error", response.Error)
	}
	if !strings.Contains(response.Error.Message, "did not start") {
		t.Fatalf("message = %q, want it to name the failed start", response.Error.Message)
	}
}

// A finished finite queue is resumable: toggle must replay instead of reporting
// invalid_state (see docs/product/open-questions.md OQ11).
func TestToggleOnFinishedQueueResumes(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	_, socket := startTestServerWithEngine(t, engine)
	engine.SetStatus("ended")

	response := call(t, socket, "playback.toggle", nil)
	if !response.OK {
		t.Fatalf("toggle on a finished queue failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "playing" {
		t.Fatalf("status after toggling a finished queue = %q, want playing", state.Status)
	}
}
