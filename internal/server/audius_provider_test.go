package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

func TestAudiusDiscoveryAndPlaylistOverSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("app_name") != "lilt" {
			t.Errorf("missing app_name")
		}
		switch r.URL.Path {
		case "/tracks/search":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"Song","permalink":"/artist/song","is_streamable":true,"user":{"name":"Artist"}}]}`))
		case "/tracks/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"top1","title":"Trending","permalink":"/artist/top","is_streamable":true,"user":{"name":"Artist"}}]}`))
		case "/playlists/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"tp1","playlist_name":"Top List"}]}`))
		case "/playlists/search":
			_, _ = w.Write([]byte(`{"data":[{"id":"p1","playlist_name":"List"}]}`))
		case "/playlists/p1":
			// Real Audius single-playlist lookups return a one-element array.
			_, _ = w.Write([]byte(`{"data":[{"id":"p1","playlist_name":"List"}]}`))
		case "/playlists/p1/tracks":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"Song","is_streamable":true,"user":{"name":"Artist"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer upstream.Close()
	dir, err := os.MkdirTemp("/tmp", "lilt-aud-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	s, err := Start(Options{SocketPath: socket, Engine: fakeengine.NewFakeEngine(), Store: state.New(filepath.Join(dir, "state.json")), AudiusClient: &client})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	response := call(t, socket, "discovery.search", map[string]any{"source": "audius", "term": "song", "type": "song", "limit": 1})
	if !response.OK {
		t.Fatalf("search=%+v", response.Error)
	}
	var result api.SearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Groups[api.GroupSongs][0]; got.ID != "audius:song:t1" || got.Ref != "audius:song:t1" {
		t.Fatalf("item=%#v", got)
	}
	if got := result.Groups[api.GroupSongs][0].URL; got != "https://audius.co/artist/song" {
		t.Fatalf("url=%q, want canonical public URL", got)
	}
	response = call(t, socket, "playlist.tracks", map[string]any{"ref": "audius:playlist:p1"})
	if !response.OK {
		t.Fatalf("playlist=%+v", response.Error)
	}
	var playlist api.PlaylistTracksResult
	if err := json.Unmarshal(response.Data, &playlist); err != nil {
		t.Fatal(err)
	}
	if playlist.Playlist.ID != "audius:playlist:p1" || len(playlist.Items) != 1 {
		t.Fatalf("playlist=%#v", playlist)
	}

	// Trending is an optional provider extension.
	trending := call(t, socket, "discovery.trending", map[string]any{"source": "audius", "type": "song", "limit": 1})
	if !trending.OK {
		t.Fatalf("trending songs: %+v", trending.Error)
	}
	if err := json.Unmarshal(trending.Data, &result); err != nil {
		t.Fatal(err)
	}
	if songs := result.Groups[api.GroupSongs]; len(songs) != 1 || songs[0].Ref != "audius:song:top1" {
		t.Fatalf("trending songs=%#v", result.Groups)
	}
	trending = call(t, socket, "discovery.trending", map[string]any{"source": "audius", "type": "playlist", "limit": 1})
	if !trending.OK {
		t.Fatalf("trending playlists: %+v", trending.Error)
	}
	if err := json.Unmarshal(trending.Data, &result); err != nil {
		t.Fatal(err)
	}
	if lists := result.Groups[api.GroupPlaylists]; len(lists) != 1 || lists[0].Ref != "audius:playlist:tp1" {
		t.Fatalf("trending playlists=%#v", result.Groups)
	}
	// Sources without trending return a stable unsupported_command.
	if response := call(t, socket, "discovery.trending", map[string]any{"source": "apple-music", "type": "song"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("apple trending=%+v", response)
	}
	if response := call(t, socket, "discovery.trending", map[string]any{"source": "radio", "type": "song"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("radio trending=%+v", response)
	}

	// `all` must skip discovery kinds the source does not support (Audius has no
	// stations) instead of failing the whole search.
	response = call(t, socket, "discovery.search", map[string]any{"source": "audius", "term": "song", "type": "all"})
	if !response.OK {
		t.Fatalf("search all=%+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Groups[api.GroupSongs]) != 1 || len(result.Groups[api.GroupPlaylists]) != 1 {
		t.Fatalf("all groups=%#v", result.Groups)
	}
	if _, ok := result.Groups[api.GroupStations]; ok {
		t.Fatalf("unexpected station group for Audius: %#v", result.Groups)
	}

	// An explicitly unsupported kind is a stable unsupported_command, not a
	// generic search failure.
	response = call(t, socket, "discovery.search", map[string]any{"source": "audius", "term": "song", "type": "station"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("station search=%+v, want unsupported_command", response)
	}

	// This server deliberately has no URL driver, so declared Audius playback is
	// unavailable rather than silently routed through Apple Music.
	response = call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("audius play=%+v, want source_unavailable", response)
	}
	response = call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"audius:song:t1"}})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("audius playSongs=%+v, want source_unavailable", response)
	}

	// Source is explicit: a missing source is an invalid request, and radio is
	// not a discovery.search provider.
	response = call(t, socket, "discovery.search", map[string]any{"term": "song", "type": "song"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("missing source=%+v, want invalid_request", response)
	}
	response = call(t, socket, "discovery.search", map[string]any{"source": "radio", "term": "song", "type": "song"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("radio discovery=%+v, want unsupported_command", response)
	}
}

func TestAudiusPreparePlaybackBuildsLazyURLQueuePlan(t *testing.T) {
	streamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tracks/t1":
			_, _ = w.Write([]byte(`{"data":{"id":"t1","title":"One","permalink":"/artist/one","is_streamable":true,"user":{"name":"Artist"}}}`))
		case "/playlists/p1/tracks":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"One","permalink":"/artist/one","is_streamable":true},{"id":"t2","title":"Two","permalink":"/artist/two","is_streamable":true}]}`))
		case "/tracks/t1/stream", "/tracks/t2/stream":
			streamCalls++
			if r.URL.Query().Get("no_redirect") != "true" {
				t.Errorf("no_redirect=%q", r.URL.Query().Get("no_redirect"))
			}
			_, _ = w.Write([]byte(`{"data":"https://media.invalid/short-lived"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	provider := audiusProvider{client: audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}}
	plan, apiErr := provider.PreparePlayback(context.Background(), PlaybackRequest{References: []api.Reference{{Source: api.SourceAudius, Kind: api.KindPlaylist, ID: "p1"}}, StartIndex: 1})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if streamCalls != 0 {
		t.Fatalf("PreparePlayback resolved %d stream URLs", streamCalls)
	}
	if plan.Source() != api.SourceAudius || plan.Transport() != transportURLQueue || plan.StartIndex() != 1 || len(plan.PublicQueue()) != 2 {
		t.Fatalf("plan source=%q transport=%q index=%d queue=%+v", plan.Source(), plan.Transport(), plan.StartIndex(), plan.PublicQueue())
	}
	for _, item := range plan.PublicQueue() {
		if item.URL == "https://media.invalid/short-lived" {
			t.Fatalf("media URL leaked into public queue: %+v", item)
		}
	}
	prepared := plan.(urlQueuePrepared)
	resolved, err := prepared.resolveURL(context.Background(), plan.PublicQueue()[1])
	if err != nil || resolved.URL != "https://media.invalid/short-lived" || streamCalls != 1 {
		t.Fatalf("resolved=%q calls=%d err=%v", resolved.URL, streamCalls, err)
	}
	if plan.PublicQueue()[1].URL != "https://audius.co/artist/two" {
		t.Fatalf("public URL mutated: %+v", plan.PublicQueue()[1])
	}

	descriptor := provider.Descriptor(context.Background())
	if !declaresCapability(descriptor, api.CapPlaybackFull) || !declaresCapability(descriptor, api.CapQueue) {
		t.Fatalf("Audius must declare playback and queue: %+v", descriptor.Capabilities)
	}
}
