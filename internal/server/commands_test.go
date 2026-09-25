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

// queueRevision increments on every queue composition change, which now includes
// each append of a paced fill (docs/client-api/models.md). The property under
// test is the increment, not a count that depends on how a fill publishes.
func TestQueueRemoveIncrementsRevision(t *testing.T) {
	_, socket := startTestServer(t)
	call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1", "apple-music:song:2"}})
	before := sessionState(t, socket).QueueRevision

	response := call(t, socket, "queue.remove", map[string]any{"index": 0})
	if !response.OK {
		t.Fatalf("remove failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.QueueRevision != before+1 {
		t.Fatalf("queueRevision = %d, want %d after remove", state.QueueRevision, before+1)
	}
}

// sessionState reads the committed session state.
func sessionState(t *testing.T, socket string) api.PlaybackState {
	t.Helper()
	response := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	if !response.OK {
		t.Fatalf("session.status failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	return state
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

func (e *previewOnlyEngine) QueueRemove(context.Context, int) (core.PlaybackState, error) {
	return core.PlaybackState{}, &player.RPCError{Code: "preview_unsupported", Message: "queue edits are unavailable in preview mode"}
}

func (e *previewOnlyEngine) QueueMove(context.Context, int, int) (core.PlaybackState, error) {
	return core.PlaybackState{}, &player.RPCError{Code: "preview_unsupported", Message: "queue edits are unavailable in preview mode"}
}

// An empty queue answers the documented queue_unavailable family for a valid
// index, instead of leaking the helper's preview_unsupported. The index is
// validated first, so it must be in range for the empty-queue path.
func TestQueueEditsWithoutQueueReportQueueUnavailable(t *testing.T) {
	engine := &previewOnlyEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"1"}}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	for _, test := range []struct {
		command string
		params  map[string]any
	}{
		{"queue.jump", map[string]any{"index": 0}},
		{"queue.remove", map[string]any{"index": 0}},
		{"queue.move", map[string]any{"from": 0, "to": 0}},
	} {
		response := call(t, socket, test.command, test.params)
		if response.Error == nil || response.Error.Code != api.CodeQueueUnavailable {
			t.Fatalf("%s error = %+v, want queue_unavailable", test.command, response.Error)
		}
	}
}

// A genuinely empty queue (no playback at all) is queue_unavailable for a valid
// index, not invalid_request: there is no queue to index into.
func TestQueueEditsOnEmptyQueueReportQueueUnavailable(t *testing.T) {
	_, socket := startTestServer(t)
	for _, test := range []struct {
		command string
		params  map[string]any
	}{
		{"queue.jump", map[string]any{"index": 0}},
		{"queue.remove", map[string]any{"index": 0}},
		{"queue.move", map[string]any{"from": 0, "to": 0}},
	} {
		response := call(t, socket, test.command, test.params)
		if response.Error == nil || response.Error.Code != api.CodeQueueUnavailable {
			t.Fatalf("%s on empty queue = %+v, want queue_unavailable", test.command, response.Error)
		}
	}
}

// A definitely out-of-range index is invalid_request on the MusicKit path too,
// matching the URL-queue transport instead of a silent helper no-op.
func TestEngineQueueEditRejectsOutOfRangeIndex(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"1", "2"}}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	_, socket := startTestServerWithEngine(t, engine)
	for _, test := range []struct {
		command string
		params  map[string]any
	}{
		{"queue.jump", map[string]any{"index": 9}},
		{"queue.remove", map[string]any{"index": 9}},
		{"queue.move", map[string]any{"from": 0, "to": 9}},
		{"queue.move", map[string]any{"from": -1, "to": 0}},
	} {
		response := call(t, socket, test.command, test.params)
		if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
			t.Fatalf("%s out-of-range = %+v, want invalid_request", test.command, response.Error)
		}
	}
	if response := call(t, socket, "queue.jump", map[string]any{"index": 1}); !response.OK {
		t.Fatalf("in-range jump = %+v, want ok", response.Error)
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
//
// The report is a partial failure that keeps the queue (OQ17): the fill really
// happened, so discarding it forced the user to start over.
func TestWedgedQueueFillIsReportedAndKeepsTheQueue(t *testing.T) {
	engine := &wedgedFillEngine{FakeEngine: fakeengine.NewFakeEngine()}
	// The wedged fill lives on the fallback path; a one-shot batch that
	// succeeds never fills.
	engine.FailPlaySongs(errors.New("batch rejected"))
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:s1", "apple-music:song:s2"}})
	if response.OK {
		t.Fatalf("wedged fill reported success: %s", response.Data)
	}
	if response.Error.Code != api.CodePartialFailure {
		t.Fatalf("error = %+v, want partial_failure with the queue kept", response.Error)
	}
	if !strings.Contains(response.Error.Message, "did not start") {
		t.Fatalf("message = %q, want it to name the failed start", response.Error.Message)
	}
	raw, err := json.Marshal(response.Error.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		QueueReady bool              `json:"queueReady"`
		State      api.PlaybackState `json:"state"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details = %s: %v", raw, err)
	}
	if !details.QueueReady || len(details.State.Queue) == 0 {
		t.Fatalf("the wedged fill did not keep its queue: %s", raw)
	}
}

// A finished finite queue is resumable: toggle must replay instead of reporting
// invalid_state.
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

// A finite queue whose fill succeeded but whose start failed keeps the queue:
// the response is a partial failure carrying the built queue, not a plain
// playback error that discards it (docs/product/open-questions.md OQ17).
func TestQueueReadyButNotPlayingKeepsTheQueue(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	// The queue-ready-not-playing failure is a paced-fill outcome: force the
	// fallback by rejecting the one-shot batch.
	engine.FailPlaySongs(errors.New("batch rejected"))
	engine.ParkAfterEnqueue()
	engine.FailResume(errors.New("MPMusicPlayerControllerErrorDomain Code=1"))
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:fake:album"})
	if response.OK {
		t.Fatalf("a queue that did not start must not report success: %+v", response)
	}
	if response.Error.Code != api.CodePartialFailure {
		t.Fatalf("error = %+v, want partial_failure", response.Error)
	}
	var details struct {
		QueueReady bool              `json:"queueReady"`
		State      api.PlaybackState `json:"state"`
	}
	raw, err := json.Marshal(response.Error.Details)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details = %s: %v", raw, err)
	}
	if !details.QueueReady {
		t.Errorf("details do not mark the queue as ready: %s", raw)
	}
	if len(details.State.Queue) == 0 {
		t.Errorf("the built queue was discarded: %s", raw)
	}
	if details.State.Status != "paused" {
		t.Errorf("status = %q, want paused", details.State.Status)
	}
	if details.State.PlaybackError == nil || *details.State.PlaybackError == "" {
		t.Errorf("the state does not explain why nothing is playing: %s", raw)
	}
	// The committed session keeps the queue too, so a client that only watches
	// state sees the same thing.
	status := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	if !status.OK {
		t.Fatalf("session.status failed: %+v", status.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(status.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) == 0 {
		t.Fatalf("committed state lost the queue: %+v", state)
	}
}

// A fill the engine only partly accepts is reported with its counts, not
// silently shortened, and the queue it did build is kept.
func TestPartialFillReportsCountsAndKeepsTheQueue(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	// A partial fill only exists on the append fallback; reject the batch.
	engine.FailPlaySongs(errors.New("batch rejected"))
	// Enqueue receives the provider id, not the ref.
	engine.RefuseEnqueue("2")
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:1", "apple-music:song:2", "apple-music:song:3"},
	})
	if response.OK {
		t.Fatalf("a partial fill must not report plain success: %s", response.Data)
	}
	if response.Error.Code != api.CodePartialFailure {
		t.Fatalf("error = %+v, want partial_failure", response.Error)
	}
	raw, err := json.Marshal(response.Error.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		Added   int               `json:"added"`
		Skipped int               `json:"skipped"`
		Total   int               `json:"total"`
		State   api.PlaybackState `json:"state"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details = %s: %v", raw, err)
	}
	if details.Total != 3 || details.Skipped != 1 || details.Added != 2 {
		t.Fatalf("fill counts = %+v, want 2 of 3 with 1 refused", details)
	}
	if details.State.QueueFill != nil {
		t.Fatalf("a finished fill still reports progress: %s", raw)
	}
}

// The one-shot start is the primary playSongs path: the whole queue is
// assigned at once — no fill progress — and the response carries the complete
// queue, which keeps it rebuildable for an Up Next jump (2026-09-22 probes).
func TestPlaySongsStartsOneShotQueue(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs":       []string{"apple-music:song:1", "apple-music:song:2", "apple-music:song:3"},
		"startIndex": 1,
	})
	if !response.OK {
		t.Fatalf("playSongs failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 3 || state.QueueIndex != 1 || state.Status != "playing" {
		t.Fatalf("one-shot state = %d queue items at index %d, %s", len(state.Queue), state.QueueIndex, state.Status)
	}
	if state.QueueFill != nil {
		t.Fatalf("one-shot start reported fill progress: %+v", state.QueueFill)
	}
}

// A paced fill publishes its progress, so a client can show 9/16 instead of an
// indefinite "working", and the finished state stops reporting it.
func TestFillPublishesProgress(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	// Fill progress only exists on the append fallback; reject the batch.
	engine.FailPlaySongs(errors.New("batch rejected"))
	_, socket := startTestServerWithEngine(t, engine)
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
	call(t, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:1", "apple-music:song:2", "apple-music:song:3"},
	})

	progressed := false
	finished := false
	deadline := time.After(3 * time.Second)
	for !finished {
		select {
		case event := <-watcher.Events:
			if event.Event != "playback.changed" {
				continue
			}
			var payload struct {
				State *api.PlaybackState `json:"state"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil || payload.State == nil {
				continue
			}
			if fill := payload.State.QueueFill; fill != nil {
				if fill.Total != 3 || fill.Queued <= 0 || fill.Queued > fill.Total {
					t.Fatalf("fill progress = %+v", fill)
				}
				progressed = true
			} else if progressed {
				finished = true
			}
		case <-deadline:
			t.Fatalf("no fill progress observed (progressed=%v)", progressed)
		}
	}
}

// A successful fill must answer with the queue it built. The album and playSongs
// paths assign through a report, and a `:=` there once shadowed the outer state,
// so playback worked while the response (and the committed session) said
// nothing was playing.
func TestSuccessfulFillReturnsTheQueue(t *testing.T) {
	for _, test := range []struct {
		name   string
		call   string
		params map[string]any
	}{
		{"playSongs", "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1", "apple-music:song:2"}}},
		{"album", "playback.play", map[string]any{"ref": "apple-music:album:fake:album"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, socket := startTestServer(t)
			response := call(t, socket, test.call, test.params)
			if !response.OK {
				t.Fatalf("%s failed: %+v", test.call, response.Error)
			}
			var state api.PlaybackState
			if err := json.Unmarshal(response.Data, &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Queue) == 0 {
				t.Fatalf("%s answered without the queue it built: %s", test.call, response.Data)
			}
			if state.Status == "" || state.Mode == "" {
				t.Fatalf("%s answered with an empty state: %s", test.call, response.Data)
			}
			// The committed session must agree with the response.
			committed := sessionState(t, socket)
			if len(committed.Queue) != len(state.Queue) {
				t.Fatalf("committed queue = %d, response = %d", len(committed.Queue), len(state.Queue))
			}
		})
	}
}

// A handler panic must not kill the server: the client gets a stable
// internal_error response, the panic and its stack land in the journal, and
// the next command dispatches normally.
func TestServerDispatchRecoversFromHandlerPanics(t *testing.T) {
	engine := panickingOnPlaySongs{fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:1111111111"},
	})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeInternalError {
		t.Fatalf("panic response = %+v, want internal_error", response)
	}
	if next := call(t, socket, "sources.list", map[string]any{}); !next.OK {
		t.Fatalf("server did not survive the panic: %+v", next.Error)
	}
}

func TestConcurrentQueryPanicCompletesDedupRetry(t *testing.T) {
	s := &Server{
		registry: api.NewRegistry(),
		dedup:    newDedupCache(0, 0),
		closed:   make(chan struct{}),
		logf:     func(string, map[string]any) {},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	s.registry.Bind("discovery.search", func(context.Context, json.RawMessage) (any, *api.Error) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			panic("provider failed")
		}
		return map[string]any{"ok": true}, nil
	})
	request := api.Request{RequestID: "same-search", Command: "discovery.search",
		Params: json.RawMessage(`{"source":"audius","term":"test","type":"song"}`)}
	first := make(chan api.Response, 1)
	go func() { first <- s.dispatch(request) }()
	<-started
	retry := make(chan api.Response, 1)
	go func() { retry <- s.dispatch(request) }()
	close(release)
	for _, pending := range []<-chan api.Response{first, retry} {
		select {
		case result := <-pending:
			if result.OK || result.Error == nil || result.Error.Code != api.CodeInternalError {
				t.Fatalf("panic result = %+v, want internal_error", result)
			}
		case <-time.After(time.Second):
			t.Fatal("same requestId stayed pending after provider panic")
		}
	}
	if calls != 1 {
		t.Fatalf("provider ran %d times for the same requestId", calls)
	}
	request.RequestID = "new-search"
	if result := s.dispatch(request); !result.OK {
		t.Fatalf("new request cannot run: %+v", result.Error)
	}
}

// panickingOnPlaySongs delegates everything to the wrapped engine except the
// one call this test needs to panic.
type panickingOnPlaySongs struct {
	Engine
}

func (e panickingOnPlaySongs) PlaySongs(ctx context.Context, r core.PlaySongsRequest) (core.PlaybackState, error) {
	panic("boom in engine")
}
