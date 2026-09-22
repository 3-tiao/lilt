package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/state"
)

func TestJamendoDiscoveryAndPlaylistOverSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("client_id"); got != "client-1" {
			t.Errorf("client_id=%q", got)
		}
		switch r.URL.Path {
		case "/tracks":
			if r.URL.Query().Get("featured") == "1" {
				if got := r.URL.Query().Get("order"); got != "popularity_month" {
					t.Errorf("trending order=%q, want popularity_month", got)
				}
				_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[` +
					`{"id":"t1","name":"Featured","duration":120,"artist_name":"Artist","audio":"https://media.invalid/t1.mp3","shareurl":"https://www.jamendo.com/track/t1"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[` +
				`{"id":"t1","name":"Song","duration":120,"artist_name":"Artist","audio":"https://media.invalid/t1.mp3","shareurl":"https://www.jamendo.com/track/t1"},` +
				`{"id":"blocked","name":"Blocked","artist_name":"Artist","audio":""}]}`))
		case "/playlists":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"p1","name":"List","user_name":"Curator","shareurl":"https://www.jamendo.com/list/p1"}]}`))
		case "/playlists/tracks":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"t1","name":"Song","duration":120,"artist_name":"Artist","audio":"https://media.invalid/t1.mp3","shareurl":"https://www.jamendo.com/track/t1"}]}`))
		default:
			t.Fatalf("unexpected Jamendo path %q", r.URL.Path)
		}
	}))
	defer upstream.Close()

	store := securestore.NewMemory()
	if err := jamendo.SaveClientID(store, "client-1"); err != nil {
		t.Fatal(err)
	}
	server, socket := startJamendoTestServer(t, store, jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()})
	defer server.Close()

	descriptor := jamendoDescriptor(t, socket)
	if !descriptor.Available || descriptor.Availability != api.AvailabilityReady {
		t.Fatalf("descriptor=%#v", descriptor)
	}
	if _, ok := descriptor.Capabilities[api.CapSearchSongs]; !ok {
		t.Fatal("Jamendo does not declare search.songs")
	}
	if _, ok := descriptor.Capabilities[api.CapSearchPlaylists]; !ok {
		t.Fatal("Jamendo does not declare search.playlists")
	}
	for _, present := range []string{api.CapPlaybackFull, api.CapQueue} {
		if _, ok := descriptor.Capabilities[present]; !ok {
			t.Fatalf("Jamendo does not declare %s", present)
		}
	}
	if _, ok := descriptor.Capabilities[api.CapSearchTrendingSongs]; !ok {
		t.Fatal("Jamendo does not declare search.trending.songs")
	}
	if _, ok := descriptor.Capabilities[api.CapSearchTrending]; ok {
		t.Fatal("Jamendo must not declare kind-ambiguous search.trending")
	}

	response := call(t, socket, "discovery.search", map[string]any{"source": "jamendo", "term": "song", "type": "song", "limit": 2})
	if !response.OK {
		t.Fatalf("search=%+v", response.Error)
	}
	var result api.SearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	songs := result.Groups[api.GroupSongs]
	if len(songs) != 1 || songs[0].Ref != "jamendo:song:t1" || songs[0].URL != "https://www.jamendo.com/track/t1" {
		t.Fatalf("songs=%#v", songs)
	}
	if strings.Contains(string(response.Data), "media.invalid") {
		t.Fatalf("search response leaked a media URL: %s", response.Data)
	}

	response = call(t, socket, "discovery.search", map[string]any{"source": "jamendo", "term": "list", "type": "all", "limit": 2})
	if !response.OK {
		t.Fatalf("search all=%+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Groups[api.GroupSongs]) != 1 || len(result.Groups[api.GroupPlaylists]) != 1 {
		t.Fatalf("groups=%#v", result.Groups)
	}

	response = call(t, socket, "playlist.tracks", map[string]any{"ref": "jamendo:playlist:p1"})
	if !response.OK {
		t.Fatalf("playlist=%+v", response.Error)
	}
	var playlist api.PlaylistTracksResult
	if err := json.Unmarshal(response.Data, &playlist); err != nil {
		t.Fatal(err)
	}
	if playlist.Playlist.Ref != "jamendo:playlist:p1" || len(playlist.Items) != 1 || playlist.Items[0].Ref != "jamendo:song:t1" {
		t.Fatalf("playlist=%#v", playlist)
	}

	// The default type is "all": it returns exactly the declared kinds, so a
	// song-only source yields the songs group alone.
	trendingAll := call(t, socket, "discovery.trending", map[string]any{"source": "jamendo"})
	if !trendingAll.OK {
		t.Fatalf("trending default all=%+v", trendingAll.Error)
	}
	result = api.SearchResult{}
	if err := json.Unmarshal(trendingAll.Data, &result); err != nil {
		t.Fatal(err)
	}
	if featured := result.Groups[api.GroupSongs]; len(featured) != 1 || featured[0].Ref != "jamendo:song:t1" || featured[0].Title != "Featured" || strings.Contains(string(trendingAll.Data), "media.invalid") {
		t.Fatalf("featured songs=%#v", featured)
	}
	if _, hasPlaylists := result.Groups[api.GroupPlaylists]; hasPlaylists {
		t.Fatalf("default all must not invent a playlists group: %s", trendingAll.Data)
	}
	if response := call(t, socket, "discovery.trending", map[string]any{"source": "jamendo", "type": "song"}); !response.OK {
		t.Fatalf("trending songs=%+v", response.Error)
	}
	// Song-only trending must not silently degrade to playlists.
	if response := call(t, socket, "discovery.trending", map[string]any{"source": "jamendo", "type": "playlist"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("trending playlists=%+v, want unsupported_command", response)
	}

	response = call(t, socket, "authorization.disconnect", map[string]any{"source": "jamendo"})
	if !response.OK {
		t.Fatalf("disconnect=%+v", response.Error)
	}
	if _, err := jamendo.LoadClientID(store); !errors.Is(err, jamendo.ErrNotConfigured) {
		t.Fatalf("client_id remains after disconnect: %v", err)
	}
	if descriptor := jamendoDescriptor(t, socket); descriptor.Available || descriptor.Availability != api.AvailabilityUnavailable {
		t.Fatalf("descriptor after disconnect=%#v", descriptor)
	}
}

func TestJamendoPreparePlaybackBuildsLazyMP32Plan(t *testing.T) {
	resolveCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlists/tracks":
			if got := r.URL.Query().Get("audioformat"); got != "" {
				t.Errorf("preparation audioformat=%q", got)
			}
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[` +
				`{"id":"t1","name":"One","duration":120,"artist_name":"A","audio":"https://default.invalid/t1","shareurl":"https://www.jamendo.com/track/t1"},` +
				`{"id":"t2","name":"Two","duration":90,"artist_name":"A","audio":"https://default.invalid/t2","shareurl":"https://www.jamendo.com/track/t2"}]}`))
		case "/tracks":
			resolveCalls++
			if r.URL.Query().Get("id") != "t2" || r.URL.Query().Get("audioformat") != "mp32" {
				t.Errorf("resolver query=%v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"t2","name":"Two","duration":90,"artist_name":"A","audio":"https://media.invalid/t2.mp3","image":"https://images.invalid/t2.jpg"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	provider := jamendoProvider{
		client:      jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client(), Credentials: func() (string, error) { return "client-1", nil }},
		credentials: func() (string, error) { return "client-1", nil },
	}

	plan, apiErr := provider.PreparePlayback(context.Background(), PlaybackRequest{
		References: []api.Reference{{Source: api.SourceJamendo, Kind: api.KindPlaylist, ID: "p1"}},
		StartIndex: 1,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if resolveCalls != 0 {
		t.Fatalf("preparation resolved %d media URLs", resolveCalls)
	}
	if plan.Source() != api.SourceJamendo || plan.Transport() != transportURLQueue || plan.StartIndex() != 1 || len(plan.PublicQueue()) != 2 {
		t.Fatalf("plan source=%q transport=%q index=%d queue=%+v", plan.Source(), plan.Transport(), plan.StartIndex(), plan.PublicQueue())
	}
	for _, item := range plan.PublicQueue() {
		if strings.Contains(item.URL, "default.invalid") || strings.Contains(item.URL, "media.invalid") {
			t.Fatalf("media URL leaked into public queue: %+v", item)
		}
	}
	resolved, err := plan.(urlQueuePrepared).resolveURL(context.Background(), plan.PublicQueue()[1])
	if err != nil || resolved.URL != "https://media.invalid/t2.mp3" || resolved.Duration != 90 || resolveCalls != 1 {
		t.Fatalf("resolved=%+v calls=%d err=%v", resolved, resolveCalls, err)
	}
	if plan.PublicQueue()[1].URL != "https://www.jamendo.com/track/t2" {
		t.Fatalf("public queue mutated: %+v", plan.PublicQueue()[1])
	}
}

func TestJamendoUnconfiguredIsVisibleButUnavailable(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
	}))
	defer upstream.Close()

	store := securestore.NewMemory()
	server, socket := startJamendoTestServer(t, store, jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()})
	defer server.Close()

	descriptor := jamendoDescriptor(t, socket)
	if descriptor.Available || descriptor.Availability != api.AvailabilityUnavailable || !strings.Contains(descriptor.Reason, "lilt jamendo setup") {
		t.Fatalf("descriptor=%#v", descriptor)
	}
	for name, capability := range descriptor.Capabilities {
		if capability.Available || capability.Reason == "" {
			t.Fatalf("capability %s=%#v", name, capability)
		}
	}

	var authorizations []api.SourceAuthorization
	response := call(t, socket, "authorization.list", nil)
	if !response.OK {
		t.Fatalf("authorization.list=%+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &authorizations); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, authorization := range authorizations {
		if authorization.Source == api.SourceJamendo {
			found = true
			if authorization.Status != api.AuthNotRequired {
				t.Fatalf("Jamendo auth=%#v", authorization)
			}
		}
	}
	if !found {
		t.Fatal("Jamendo missing from authorization.list")
	}

	response = call(t, socket, "discovery.search", map[string]any{"source": "jamendo", "term": "song", "type": "song"})
	if response.OK || response.Error.Code != api.CodeAuthorizationFailed || !strings.Contains(response.Error.Message, "lilt jamendo setup") {
		t.Fatalf("search=%+v", response)
	}
	if calls.Load() != 0 {
		t.Fatalf("unconfigured provider made %d upstream calls", calls.Load())
	}

	response = call(t, socket, "authorization.begin", map[string]any{"source": "jamendo", "interactive": true})
	if response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("authorization.begin=%+v", response)
	}
}

func TestJamendoBodyErrorsMapThroughRegisteredProvider(t *testing.T) {
	mode := 5
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"headers":{"status":"failed","code":%d,"error_message":"private upstream body"},"results":[]}`, mode)
	}))
	defer upstream.Close()
	store := securestore.NewMemory()
	if err := jamendo.SaveClientID(store, "client-1"); err != nil {
		t.Fatal(err)
	}
	server, socket := startJamendoTestServer(t, store, jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()})
	defer server.Close()

	for _, test := range []struct {
		providerCode int
		want         string
	}{
		{providerCode: 5, want: api.CodeAuthorizationFailed},
		{providerCode: 11, want: api.CodeAuthorizationFailed},
		{providerCode: 6, want: api.CodeSearchFailed},
		{providerCode: 3, want: api.CodeSearchFailed},
	} {
		mode = test.providerCode
		response := call(t, socket, "discovery.search", map[string]any{"source": "jamendo", "term": "song", "type": "song"})
		if response.OK || response.Error == nil || response.Error.Code != test.want {
			t.Fatalf("provider code %d: response=%+v", test.providerCode, response)
		}
		if strings.Contains(response.Error.Message, "private upstream body") {
			t.Fatalf("provider code %d leaked upstream body: %+v", test.providerCode, response.Error)
		}
		if got := fmt.Sprint(response.Error.Details["providerCode"]); got != fmt.Sprint(test.providerCode) {
			t.Fatalf("provider code detail=%q, want %d", got, test.providerCode)
		}
	}
}

func startJamendoTestServer(t *testing.T, store securestore.Store, client jamendo.Client) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-jamendo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server, err := Start(Options{
		SocketPath:    socket,
		Engine:        fakeengine.NewFakeEngine(),
		Store:         state.New(filepath.Join(dir, "state.json")),
		SecureStore:   store,
		JamendoClient: &client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, socket
}

func jamendoDescriptor(t *testing.T, socket string) api.SourceDescriptor {
	t.Helper()
	response := call(t, socket, "sources.list", nil)
	if !response.OK {
		t.Fatalf("sources.list=%+v", response.Error)
	}
	var descriptors []api.SourceDescriptor
	if err := json.Unmarshal(response.Data, &descriptors); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		if descriptor.ID == api.SourceJamendo {
			return descriptor
		}
	}
	t.Fatal("Jamendo missing from sources.list")
	return api.SourceDescriptor{}
}

// Jamendo embeds HTML entities in its user-facing text; the provider mapping
// must decode them before they reach the public item (batch
// 2026-09-22-recheck: a track titled "… &amp; …" rendered verbatim).
func TestJamendoMappingDecodesHTMLEntities(t *testing.T) {
	track, ok := jamendoTrack(jamendo.Track{ID: "1", Name: "Rock &amp; Roll", ArtistName: "Dada &amp; Sons", Audio: "https://example.invalid/a.mp3"})
	if !ok || track.Title != "Rock & Roll" || track.Artist != "Dada & Sons" {
		t.Fatalf("track = %+v ok=%v", track, ok)
	}
	playlist := jamendoPlaylist(jamendo.Playlist{ID: "9", Name: "Best of &quot;90s&quot;", UserName: "A &amp; B"})
	if playlist.Title != `Best of "90s"` || playlist.Artist != "A & B" {
		t.Fatalf("playlist = %+v", playlist)
	}
	// Names that need no decoding stay untouched, padding included.
	plain, ok := jamendoTrack(jamendo.Track{ID: "2", Name: "  Plain  ", ArtistName: "Someone", Audio: "https://example.invalid/b.mp3"})
	if !ok || plain.Title != "Plain" {
		t.Fatalf("plain track = %+v ok=%v", plain, ok)
	}
}
