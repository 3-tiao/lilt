package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
)

// albumSpyEngine records the finite-queue orchestration an album play turns
// into: which song starts playback and which songs are appended after it.
type albumSpyEngine struct {
	*fakeengine.FakeEngine
	mu       sync.Mutex
	album    core.Item
	tracks   []core.Item
	albumErr error
	plays    []core.PlaybackRequest
	enqueues []core.PlaybackRequest
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

// An album ref is expanded server-side into the same start-then-paced-enqueue
// sequence as playback.playSongs. Two helper-side shapes were tried and rejected
// on a real account (batch 2026-09-20-search-and-queue): appending works but
// cannot be rebuilt for an Up Next jump, and assigning a whole album queue fails
// with Code=6 "Failed to prepare to play".

func TestAlbumPlayExpandsToOrchestratedSongQueue(t *testing.T) {
	engine := newAlbumEngine(3)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1"})
	if !response.OK {
		t.Fatalf("album play failed: %+v", response.Error)
	}
	plays, enqueues := engine.calls()
	if len(plays) != 1 || plays[0].Kind != api.KindSong || plays[0].ID != "t1" {
		t.Fatalf("start request = %#v", plays)
	}
	if len(enqueues) != 2 || enqueues[0].ID != "t2" || enqueues[1].ID != "t3" {
		t.Fatalf("enqueue requests = %#v", enqueues)
	}
}

func TestAlbumPlayFromHereDropsEarlierTracks(t *testing.T) {
	engine := newAlbumEngine(4)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1", "startTrackID": "t3", "fromHere": true})
	if !response.OK {
		t.Fatalf("album play from here failed: %+v", response.Error)
	}
	plays, enqueues := engine.calls()
	if len(plays) != 1 || plays[0].ID != "t3" {
		t.Fatalf("start request = %#v", plays)
	}
	if len(enqueues) != 1 || enqueues[0].ID != "t4" {
		t.Fatalf("forward-only queue = %#v", enqueues)
	}
}

func TestAlbumPlayStartAtKeepsEarlierTracksInTheQueue(t *testing.T) {
	engine := newAlbumEngine(4)
	_, socket := startTestServerWithEngine(t, engine)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:al1", "startAt": 2})
	if !response.OK {
		t.Fatalf("album play with startAt failed: %+v", response.Error)
	}
	plays, enqueues := engine.calls()
	if len(plays) != 1 || plays[0].ID != "t3" {
		t.Fatalf("start request = %#v", plays)
	}
	if len(enqueues) != 3 || enqueues[0].ID != "t1" || enqueues[1].ID != "t2" || enqueues[2].ID != "t4" {
		t.Fatalf("enqueue requests = %#v", enqueues)
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
