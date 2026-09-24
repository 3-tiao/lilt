package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/player"
)

// albumSpyEngine records the finite-queue orchestration an album play turns
// into: which song starts playback and which songs are appended after it.
type albumSpyEngine struct {
	*fakeengine.FakeEngine
	mu                sync.Mutex
	album             core.Item
	tracks            []core.Item
	albumErr          error
	plays             []core.PlaybackRequest
	enqueues          []core.PlaybackRequest
	playSongsRequests []core.PlaySongsRequest
}

func (e *albumSpyEngine) AlbumTracks(context.Context, string) (core.Item, []core.Item, error) {
	if e.albumErr != nil {
		return core.Item{}, nil, e.albumErr
	}
	return e.album, e.tracks, nil
}

// PlayState (not Play) is what the server calls, and the embedded FakeEngine
// dispatches PlayState to its own Play, so the spy records at this level.
func (e *albumSpyEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	e.mu.Lock()
	e.plays = append(e.plays, request)
	e.mu.Unlock()
	return e.FakeEngine.PlayState(ctx, request)
}

func (e *albumSpyEngine) Enqueue(ctx context.Context, request core.PlaybackRequest, position string) (core.PlaybackState, error) {
	e.mu.Lock()
	e.enqueues = append(e.enqueues, request)
	e.mu.Unlock()
	return e.FakeEngine.Enqueue(ctx, request, position)
}

func (e *albumSpyEngine) calls() ([]core.PlaybackRequest, []core.PlaybackRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]core.PlaybackRequest(nil), e.plays...), append([]core.PlaybackRequest(nil), e.enqueues...)
}

func newAlbumEngine(tracks int) *albumSpyEngine {
	engine := &albumSpyEngine{FakeEngine: fakeengine.NewFakeEngine(), album: core.Item{Kind: api.KindAlbum, ID: "al1", Title: "Test Album"}}
	for i := 0; i < tracks; i++ {
		engine.tracks = append(engine.tracks, core.Item{Kind: api.KindSong, ID: fmt.Sprintf("t%d", i+1), Title: fmt.Sprintf("Track %d", i+1)})
	}
	return engine
}

// album.tracks is the detail page behind Apple's library albums. It is an
// optional source extension: sources that do not implement AlbumProvider must
// answer unsupported_command instead of degrading to a playlist lookup.

func TestAlbumTracksRouting(t *testing.T) {
	_, socket := startTestServer(t)

	response := call(t, socket, "album.tracks", map[string]any{"ref": "apple-music:album:fake:album"})
	if !response.OK {
		t.Fatalf("apple album.tracks failed: %+v", response.Error)
	}
	var result api.AlbumTracksResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Album.Kind != api.KindAlbum || result.Album.Ref != "apple-music:album:fake:album" {
		t.Fatalf("album = %#v", result.Album)
	}
	if len(result.Items) != 2 || result.Items[0].Ref != "apple-music:song:fake:track:1" {
		t.Fatalf("items = %#v", result.Items)
	}

	if response := call(t, socket, "album.tracks", map[string]any{"ref": "audius:album:x"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius album.tracks = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "album.tracks", map[string]any{"ref": "radio:album:x"}); response.OK || response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("radio album.tracks = %+v, want invalid_reference", response)
	}
	if response := call(t, socket, "album.tracks", map[string]any{"ref": "apple-music:playlist:p1"}); response.OK || response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("playlist ref album.tracks = %+v, want invalid_reference", response)
	}
	if response := call(t, socket, "album.tracks", map[string]any{"ref": "nope:album:x"}); response.OK || response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("unknown source album.tracks = %+v, want invalid_reference", response)
	}
}

func TestSearchAlbumsGroupRouting(t *testing.T) {
	_, socket := startTestServer(t)

	response := call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "x", "type": "album"})
	if !response.OK {
		t.Fatalf("apple album search failed: %+v", response.Error)
	}
	var result api.SearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	albums := result.Groups[api.GroupAlbums]
	if len(albums) != 1 || albums[0].Kind != api.KindAlbum || albums[0].Ref != "apple-music:album:fake:album" {
		t.Fatalf("albums group = %#v", result.Groups)
	}
	if _, ok := result.Groups[api.GroupSongs]; ok {
		t.Fatalf("type=album leaked other groups: %#v", result.Groups)
	}

	// type=all only includes groups the source declares; Apple declares albums.
	response = call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "x", "type": "all"})
	if !response.OK {
		t.Fatalf("apple all search failed: %+v", response.Error)
	}
	result = api.SearchResult{}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Groups[api.GroupAlbums]) != 1 {
		t.Fatalf("type=all omitted the albums group: %#v", result.Groups)
	}

	// Audius does not declare search.albums, so the same request must fail
	// rather than silently return an empty group.
	if response := call(t, socket, "discovery.search", map[string]any{"source": "audius", "term": "x", "type": "album"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius album search = %+v, want unsupported_command", response)
	}
}

func TestSearchTypeSchemaAcceptsEveryDocumentedKind(t *testing.T) {
	_, socket := startTestServer(t)

	for _, kind := range []string{"song", "album", "playlist", "station", "all"} {
		response := call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "x", "type": kind})
		if !response.OK {
			t.Fatalf("type %q rejected: %+v", kind, response.Error)
		}
	}

	response := call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "x", "type": "movie"})
	if response.OK || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("unknown type = %+v, want invalid_request", response)
	}
}

// An album ref is expanded server-side into its songs and started through the
// one-shot queue assignment (playSongs): one assignment keeps the queue
// rebuildable for an Up Next jump and starts without a paced fill. MusicKit's
// batch prepare still rejects some content (Code=6), which is why the append
// orchestration remains as the fallback (OQ1 probes, 2026-09-22).

func (e *albumSpyEngine) PlaySongs(ctx context.Context, request core.PlaySongsRequest) (core.PlaybackState, error) {
	e.mu.Lock()
	e.playSongsRequests = append(e.playSongsRequests, request)
	e.mu.Unlock()
	return e.FakeEngine.PlaySongs(ctx, request)
}

func (e *albumSpyEngine) playSongs() []core.PlaySongsRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]core.PlaySongsRequest(nil), e.playSongsRequests...)
}

func TestAlbumPlayStartsOneShotQueue(t *testing.T) {
	engine := newAlbumEngine(3)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1"})
	if !response.OK {
		t.Fatalf("album play failed: %+v", response.Error)
	}
	batches := engine.playSongs()
	if len(batches) != 1 || fmt.Sprint(batches[0].IDs) != "[t1 t2 t3]" || batches[0].StartAt != 0 {
		t.Fatalf("one-shot request = %+v", batches)
	}
	if plays, enqueues := engine.calls(); len(plays) != 0 || len(enqueues) != 0 {
		t.Fatalf("one-shot start still used the append path: plays=%#v enqueues=%#v", plays, enqueues)
	}
}

func TestAlbumPlayFromHereDropsEarlierTracks(t *testing.T) {
	engine := newAlbumEngine(4)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1", "startTrackID": "t3", "fromHere": true})
	if !response.OK {
		t.Fatalf("album play from here failed: %+v", response.Error)
	}
	batches := engine.playSongs()
	if len(batches) != 1 || fmt.Sprint(batches[0].IDs) != "[t3 t4]" || batches[0].StartAt != 0 {
		t.Fatalf("forward-only one-shot request = %+v", batches)
	}
}

func TestAlbumPlayStartAtKeepsEarlierTracksInTheQueue(t *testing.T) {
	engine := newAlbumEngine(4)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1", "startAt": 2})
	if !response.OK {
		t.Fatalf("album play with startAt failed: %+v", response.Error)
	}
	batches := engine.playSongs()
	if len(batches) != 1 || fmt.Sprint(batches[0].IDs) != "[t1 t2 t3 t4]" || batches[0].StartAt != 2 {
		t.Fatalf("startAt one-shot request = %+v", batches)
	}
}

// A batch MusicKit refuses to prepare (Code=6 on some content) must still
// play: the server falls back to the start-then-paced-append orchestration.
func TestAlbumPlayFallsBackToPacedAppendWhenBatchRejected(t *testing.T) {
	engine := newAlbumEngine(3)
	engine.FailPlaySongs(errors.New("MPMusicPlayerControllerErrorDomain Code=6"))
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1"})
	if !response.OK {
		t.Fatalf("album play failed: %+v", response.Error)
	}
	plays, enqueues := engine.calls()
	if len(plays) != 1 || plays[0].Kind != api.KindSong || plays[0].ID != "t1" {
		t.Fatalf("fallback start request = %#v", plays)
	}
	if len(enqueues) != 2 || enqueues[0].ID != "t2" || enqueues[1].ID != "t3" {
		t.Fatalf("fallback enqueue requests = %#v", enqueues)
	}
}

func TestAlbumPlayReportsResolutionFailure(t *testing.T) {
	engine := newAlbumEngine(2)
	engine.albumErr = errors.New("helper unavailable")
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1"})
	if response.OK || response.Error.Code != api.CodePlaybackError {
		t.Fatalf("album resolution failure = %+v, want playback_error", response)
	}
	if plays, _ := engine.calls(); len(plays) != 0 {
		t.Fatalf("failed expansion still started playback: %#v", plays)
	}
}

// OQ31: the helper settles its handshake in two steps — MusicAuthorization
// flips to "authorized" first, and the async subscription read fills
// accountStatus / canPlayCatalogContent a beat later. Each step must
// republish sources.changed, or the TUI keeps a degraded capability snapshot
// and rejects capability-gated keys (S) the server can actually serve.
func TestAppleAuthSettleStepsRepublishSourcesChanged(t *testing.T) {
	server, socket := startTestServerWithEngine(t, newAlbumEngine(1))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, watcher, err := api.Watch(ctx, socket, nil, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer watcher.Close()

	stateUpdate := func(authorization, accountStatus string) core.PlaybackStateUpdate {
		return core.PlaybackStateUpdate{State: core.PlaybackState{
			Mode: "full", Status: "playing", Authorization: authorization,
			AccountStatus: accountStatus,
			Track:         &core.Item{Kind: "song", ID: "t1"},
		}}
	}
	// Step 1: authorization flips to authorized with the account fields still
	// empty (the subscription read has not settled).
	server.applyEngineUpdate(stateUpdate("authorized", ""), server.engine, nil)
	if !waitSourceChanged(t, watcher) {
		t.Fatal("step 1 (authorized) republished no sources.changed")
	}
	// Step 2: the subscription read fills accountStatus. The status string is
	// unchanged, so this only republishes when the signature includes the
	// account fields (the OQ31 regression would swallow it).
	server.applyEngineUpdate(stateUpdate("authorized", "ready"), server.engine, nil)
	if !waitSourceChanged(t, watcher) {
		t.Fatal("step 2 (account fields filled) republished no sources.changed — OQ31 regression")
	}
	// drain the playback.changed copies of both steps
	// Repeating the same snapshot must NOT republish (signature dedup).
	server.applyEngineUpdate(stateUpdate("authorized", "ready"), server.engine, nil)
	deadline := time.After(600 * time.Millisecond)
	for {
		select {
		case event := <-watcher.Events:
			if event.Event == "sources.changed" {
				t.Fatalf("duplicate snapshot republished %s", event.Event)
			}
		case <-deadline:
			return
		}
	}
}

// waitSourceChanged consumes the watcher stream until a sources.changed event
// arrives or the deadline passes.
func waitSourceChanged(t *testing.T, watcher *api.Watcher) bool {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-watcher.Events:
			if event.Event == "sources.changed" {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// jumpSpyEngine refuses engine jumps with the helper's queue_not_jumpable code,
// which is what the real helper does for queues that appends built.
type jumpSpyEngine struct {
	*fakeengine.FakeEngine
	playSongsErr  error
	singlePlayErr error
}

func (e *jumpSpyEngine) QueueJump(_ context.Context, _ int) (core.PlaybackState, error) {
	return core.PlaybackState{}, &player.RPCError{Code: "queue_not_jumpable", Message: "refused"}
}

func (e *jumpSpyEngine) PlaySongs(ctx context.Context, request core.PlaySongsRequest) (core.PlaybackState, error) {
	if e.playSongsErr != nil {
		return core.PlaybackState{}, e.playSongsErr
	}
	return e.FakeEngine.PlaySongs(ctx, request)
}

func (e *jumpSpyEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	if e.singlePlayErr != nil {
		return core.PlaybackState{}, e.singlePlayErr
	}
	return e.FakeEngine.PlayState(ctx, request)
}

// A refused jump on an append-built queue is rebuilt as a one-shot assignment
// at the clicked row: the user's intent is delivered and the rebuilt queue is
// jumpable again (batch 2026-09-23-polish OQ37).
func TestQueueJumpRebuildsRefusedQueue(t *testing.T) {
	engine := &jumpSpyEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"1", "2", "3"}}); err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	// The raw engine queue carries provider ids only — the fallback must derive
	// refs itself.
	engine.SetQueue([]core.Item{
		{Kind: api.KindSong, ID: "1", Title: "One"},
		{Kind: api.KindSong, ID: "2", Title: "Two"},
		{Kind: api.KindSong, ID: "3", Title: "Three"},
	})

	response := call(t, socket, "queue.jump", map[string]any{"index": 2})
	if !response.OK {
		t.Fatalf("queue.jump = %+v, want the rebuild to deliver the jump", response)
	}
	var state core.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.QueueIndex != 2 || len(state.Queue) != 3 {
		t.Fatalf("jumped state = index %d of %d, want 2/3", state.QueueIndex, len(state.Queue))
	}
}

// When even the rebuild fails, the original refusal surfaces: the client sees
// the honest queue_not_jumpable instead of a generic start failure.
func TestQueueJumpRebuildFailureKeepsRefusal(t *testing.T) {
	engine := &jumpSpyEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"1", "2"}}); err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	engine.SetQueue([]core.Item{
		{Kind: api.KindSong, ID: "1", Title: "One"},
		{Kind: api.KindSong, ID: "2", Title: "Two"},
	})
	// Fail the rebuild paths only now that the queue exists.
	engine.playSongsErr = errors.New("batch rejected")
	engine.singlePlayErr = errors.New("cannot start")

	response := call(t, socket, "queue.jump", map[string]any{"index": 1})
	if response.OK || response.Error.Code != api.CodeQueueNotJumpable {
		t.Fatalf("queue.jump = %+v, want the queue_not_jumpable refusal", response)
	}
}
