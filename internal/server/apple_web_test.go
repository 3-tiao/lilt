package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/appleweb"
	"github.com/3-tiao/lilt/internal/jamendo"
	"github.com/3-tiao/lilt/internal/securestore"
	"github.com/3-tiao/lilt/internal/state"
)

// fakePageCatalog stands in for the browser session: no Chromium, no network.
type fakePageCatalog struct {
	mu                 sync.Mutex
	authorized         bool
	songs              []appleweb.CatalogSong
	albums             []appleweb.CatalogAlbum
	playlists          []appleweb.CatalogPlaylist
	recommended        []appleweb.Recommendation
	tracks             []appleweb.CatalogSong
	err                error
	recommendationsErr error

	// boot warm-up
	warmUpAuthorized bool
	warmDone         chan struct{}
	warmedUp         bool

	// sign-in flow behaviour
	signInErr        error
	signInBlock      chan struct{}
	signInAuthorized bool
	disconnect       error
}

func (f *fakePageCatalog) Authorized(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorized, f.err
}

// WarmUp models the boot warm-up: the session comes up and only then is the real
// authorization state known. Signed in here, so the status must flip to
// authorized without anyone asking for it.
func (f *fakePageCatalog) WarmUp(context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.warmedUp = true
	if f.warmDone != nil {
		close(f.warmDone)
		f.warmDone = nil
		f.authorized = f.warmUpAuthorized
	}
}

func (f *fakePageCatalog) SearchSongs(context.Context, string, int) ([]appleweb.CatalogSong, error) {
	return f.songs, f.err
}

func (f *fakePageCatalog) SearchAlbums(context.Context, string, int) ([]appleweb.CatalogAlbum, error) {
	return f.albums, f.err
}

func (f *fakePageCatalog) SearchPlaylists(context.Context, string, int) ([]appleweb.CatalogPlaylist, error) {
	return f.playlists, f.err
}

func (f *fakePageCatalog) TrendingSongs(context.Context, int) ([]appleweb.CatalogSong, error) {
	return f.songs, f.err
}

func (f *fakePageCatalog) Recommendations(context.Context, int) ([]appleweb.Recommendation, error) {
	if f.recommendationsErr != nil {
		return nil, f.recommendationsErr
	}
	return f.recommended, f.err
}

func (f *fakePageCatalog) AlbumTracks(context.Context, string) (appleweb.CatalogAlbum, []appleweb.CatalogSong, error) {
	if f.err != nil {
		return appleweb.CatalogAlbum{}, nil, f.err
	}
	if len(f.albums) == 0 {
		return appleweb.CatalogAlbum{}, nil, errors.New("album 222 was not found in the account's storefront")
	}
	return f.albums[0], f.tracks, nil
}

func (f *fakePageCatalog) PlaylistTracks(context.Context, string) (appleweb.CatalogPlaylist, []appleweb.CatalogSong, error) {
	if f.err != nil {
		return appleweb.CatalogPlaylist{}, nil, f.err
	}
	if len(f.playlists) == 0 {
		return appleweb.CatalogPlaylist{}, nil, errors.New("playlist pl.1 was not found in the account's storefront")
	}
	return f.playlists[0], f.tracks, nil
}

func (f *fakePageCatalog) SignIn(ctx context.Context) error {
	if f.signInBlock != nil {
		select {
		case <-f.signInBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Signing in is what makes the profile authorized: the fake flips the
	// answer the way a real completed sign-in would.
	f.mu.Lock()
	f.authorized = f.signInAuthorized
	f.mu.Unlock()
	return f.signInErr
}

// setAuthorized simulates the session changing underneath a live queue without
// a sign-in flow (for example an Apple-side expiry).
func (f *fakePageCatalog) setAuthorized(authorized bool) {
	f.mu.Lock()
	f.authorized = authorized
	f.mu.Unlock()
}

func (f *fakePageCatalog) Disconnect() error { return f.disconnect }

// started reports whether a session exists, which is what Describe keys on.
func (f *fakePageCatalog) started() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.warmedUp
}

func (f *fakePageCatalog) Song(_ context.Context, id string) (appleweb.CatalogSong, error) {
	if f.err != nil {
		return appleweb.CatalogSong{}, f.err
	}
	for _, song := range f.songs {
		if song.ID == id {
			return song, nil
		}
	}
	return appleweb.CatalogSong{}, errors.New("song " + id + " was not found in the account's storefront")
}

func fixtureSongs() []appleweb.CatalogSong {
	return []appleweb.CatalogSong{
		{ID: "1111111111", Title: "First Song", Artist: "Fixture Artist", Album: "Fixture Album", URL: "https://music.apple.com/cn/song/first/1111111111", DurationMs: 231000},
		{ID: "1111111112", Title: "Second Song", Artist: "Fixture Artist", Album: "Fixture Album", URL: "https://music.apple.com/cn/song/second/1111111112", DurationMs: 204000},
	}
}

func startAppleWebServer(t *testing.T, catalog *fakePageCatalog, browserAvailable error) (*Server, string, *fakeURLDriver) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-appleweb-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	// The provider gate walks every registered source, so this composition needs
	// the same hermetic Jamendo credentials the default harness seeds.
	secureStore := securestore.NewMemory()
	if err := jamendo.SaveClientID(secureStore, "client-1"); err != nil {
		t.Fatalf("seed Jamendo client_id: %v", err)
	}
	driver := &fakeURLDriver{}
	server, startErr := Start(Options{
		SocketPath:        socket,
		Store:             state.New(filepath.Join(dir, "state.json")),
		SecureStore:       secureStore,
		URLPlaybackDriver: driver,
		AudiusClient:      startFakeAudius(t),
		JamendoClient:     startFakeJamendo(t),
		Providers:         []ContentProvider{NewAppleWebProvider(catalog, func() error { return browserAvailable })},
		AuthProviders:     []AuthProvider{NewAppleWebAuthProvider(catalog, catalog.started)},
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, socket, driver
}

// appleDescriptorFor reads the Apple descriptor back over the real socket.
func appleDescriptorFor(t *testing.T, socket string) api.SourceDescriptor {
	t.Helper()
	var descriptors []api.SourceDescriptor
	if response := call(t, socket, "sources.list", nil); !response.OK {
		t.Fatalf("sources.list failed: %+v", response.Error)
	} else if err := json.Unmarshal(response.Data, &descriptors); err != nil {
		t.Fatalf("decode sources.list: %v", err)
	}
	for _, descriptor := range descriptors {
		if descriptor.ID == api.SourceAppleMusic {
			return descriptor
		}
	}
	t.Fatal("apple-music is missing from sources.list")
	return api.SourceDescriptor{}
}

func TestAppleWebDescriptorRequiresAWidevineBrowser(t *testing.T) {
	// No browser: the source must say so instead of advertising playback that
	// cannot happen.
	noBrowser := fmt.Errorf("%w; install Widevine chromium (nixpkgs: `chromium.override { enableWideVine = true; }`) or set LILT_CHROMIUM_PATH", appleweb.ErrNoBrowser)
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{}, noBrowser)
	descriptor := appleDescriptorFor(t, socket)
	if descriptor.Availability != api.AvailabilityUnavailable || descriptor.Available {
		t.Fatalf("descriptor = %+v, want unavailable", descriptor)
	}
	if !strings.Contains(descriptor.Reason, "Widevine") {
		t.Fatalf("reason = %q, want the install hint", descriptor.Reason)
	}
	if descriptor.Capabilities[api.CapPlaybackFull].Available {
		t.Fatal("full playback must not be advertised without a browser")
	}
}

func TestAppleWebDescriptorAdvertisesFullPlaybackWithAPrecondition(t *testing.T) {
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{}, nil)
	descriptor := appleDescriptorFor(t, socket)
	if descriptor.Availability != api.AvailabilityReady || !descriptor.Available {
		t.Fatalf("descriptor = %+v, want ready", descriptor)
	}
	for _, name := range []string{api.CapSearchSongs, api.CapSearchAlbums, api.CapSearchPlaylists, api.CapSearchTrendingSongs, api.CapRecommendations, api.CapPlaybackPreview, api.CapPlaybackFull, api.CapQueue} {
		if !descriptor.Capabilities[name].Available {
			t.Fatalf("capability %q should be available: %+v", name, descriptor.Capabilities[name])
		}
	}
	// The sign-in precondition is stated rather than hidden.
	if !strings.Contains(descriptor.Capabilities[api.CapPlaybackFull].Description, "signed in") {
		t.Fatalf("playback.full description = %q, want the sign-in precondition", descriptor.Capabilities[api.CapPlaybackFull].Description)
	}
	for _, name := range []string{api.CapSearchStations, api.CapLibrary, api.CapShuffle, api.CapRepeat} {
		if descriptor.Capabilities[name].Available {
			t.Fatalf("capability %q must not be available: %+v", name, descriptor.Capabilities[name])
		}
	}
}

func TestAppleWebDiscoveryBuildsCanonicalRefs(t *testing.T) {
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{songs: fixtureSongs(), albums: []appleweb.CatalogAlbum{
		{ID: "2222222222", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/2222222222", TrackCount: 2},
	}}, nil)

	response := call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "fixture", "type": "song"})
	if !response.OK {
		t.Fatalf("discovery.search: %+v", response.Error)
	}
	var result struct {
		Groups struct {
			Songs []api.Item `json:"songs"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Groups.Songs) != 2 {
		t.Fatalf("songs = %d, want 2", len(result.Groups.Songs))
	}
	first := result.Groups.Songs[0]
	if first.Ref != "apple-music:song:1111111111" || first.ID != "am:1111111111" || first.ProviderID != "1111111111" {
		t.Fatalf("identity = %+v", first)
	}
	// Album travels the whole projection: it is the only field that tells
	// same-title/same-artist rows apart (usability probe finding 1).
	if first.Album != "Fixture Album" {
		t.Fatalf("album = %q", first.Album)
	}
	if first.DurationMs != 231000 {
		t.Fatalf("catalog durationMs = %d", first.DurationMs)
	}
	// The public URL is the music.apple.com page, never a media asset.
	if first.URL != "https://music.apple.com/cn/song/first/1111111111" {
		t.Fatalf("url = %q, want the stable public page", first.URL)
	}

	response = call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "fixture", "type": "album"})
	if !response.OK {
		t.Fatalf("album search: %+v", response.Error)
	}
	var albums struct {
		Groups struct {
			Albums []api.Item `json:"albums"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(response.Data, &albums); err != nil {
		t.Fatalf("decode album search: %v", err)
	}
	if len(albums.Groups.Albums) != 1 || albums.Groups.Albums[0].Ref != "apple-music:album:2222222222" {
		t.Fatalf("albums = %+v", albums.Groups.Albums)
	}

	// Playlist search is declared too; this fixture has no playlist hits, so it
	// returns an empty successful group rather than unsupported_command.
	response = call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "fixture", "type": "playlist"})
	if !response.OK {
		t.Fatalf("type=playlist = %+v, want an empty successful result", response.Error)
	}
}

func TestAppleWebAlbumTracks(t *testing.T) {
	catalog := &fakePageCatalog{
		albums: []appleweb.CatalogAlbum{{ID: "2222222222", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/2222222222"}},
		tracks: fixtureSongs(),
	}
	_, socket, _ := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "album.tracks", map[string]any{"ref": "apple-music:album:2222222222"})
	if !response.OK {
		t.Fatalf("album.tracks: %+v", response.Error)
	}
	var payload struct {
		Album api.Item   `json:"album"`
		Items []api.Item `json:"items"`
	}
	if err := json.Unmarshal(response.Data, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Album.Ref != "apple-music:album:2222222222" || len(payload.Items) != 2 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAppleWebPlaylistDiscoveryAndTracksBuildCanonicalRefs(t *testing.T) {
	catalog := &fakePageCatalog{
		playlists: []appleweb.CatalogPlaylist{{
			ID: "pl.1", Title: "Fixture Mix", Artist: "Fixture Curator", URL: "https://music.apple.com/cn/playlist/pl.1",
		}},
		tracks: fixtureSongs(),
	}
	_, socket, _ := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "discovery.search", map[string]any{
		"source": "apple-music", "term": "fixture", "type": "playlist", "limit": 5,
	})
	if !response.OK {
		t.Fatalf("playlist search: %+v", response.Error)
	}
	var search api.SearchResult
	if err := json.Unmarshal(response.Data, &search); err != nil {
		t.Fatal(err)
	}
	playlists := search.Groups[api.GroupPlaylists]
	if len(playlists) != 1 || playlists[0].Kind != api.KindPlaylist || playlists[0].Ref != "apple-music:playlist:pl.1" {
		t.Fatalf("playlists = %+v", playlists)
	}

	response = call(t, socket, "playlist.tracks", map[string]any{"ref": "apple-music:playlist:pl.1"})
	if !response.OK {
		t.Fatalf("playlist.tracks: %+v", response.Error)
	}
	var detail api.PlaylistTracksResult
	if err := json.Unmarshal(response.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Playlist.Ref != "apple-music:playlist:pl.1" || len(detail.Items) != 2 || detail.Items[0].Ref != "apple-music:song:1111111111" {
		t.Fatalf("detail = %+v", detail)
	}
}

// A playlist ref must expand into its songs before the URL-queue preparer
// sees it: the preparer only accepts song refs, so an unexpanded playlist
// read as "Apple Music playback needs song references" and every browser-mode
// playlist play failed. startTrackID/fromHere/reverse follow the documented
// play semantics (commands.md).
func TestAppleWebPlaylistPlayExpandsIntoTheQueue(t *testing.T) {
	catalog := &fakePageCatalog{
		playlists: []appleweb.CatalogPlaylist{{
			ID: "pl.1", Title: "Fixture Mix", Artist: "Fixture Curator", URL: "https://music.apple.com/cn/playlist/pl.1",
		}},
		songs:  fixtureSongs(),
		tracks: fixtureSongs(),
	}
	_, socket, _ := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:playlist:pl.1"})
	if !response.OK {
		t.Fatalf("plain playlist play: %+v", response.Error)
	}
	var state core.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 2 || state.Queue[0].Ref != "apple-music:song:1111111111" || state.QueueIndex != 0 {
		t.Fatalf("plain play queue = %+v index %d", state.Queue, state.QueueIndex)
	}

	response = call(t, socket, "playback.play", map[string]any{"ref": "apple-music:playlist:pl.1", "startTrackID": "1111111112"})
	if !response.OK {
		t.Fatalf("startTrackID play: %+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.QueueIndex != 1 || state.Queue[1].Ref != "apple-music:song:1111111112" {
		t.Fatalf("startTrackID play queue index %d", state.QueueIndex)
	}

	response = call(t, socket, "playback.play", map[string]any{"ref": "apple-music:playlist:pl.1", "startTrackID": "1111111112", "fromHere": true})
	if !response.OK {
		t.Fatalf("fromHere play: %+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 1 || state.Queue[0].Ref != "apple-music:song:1111111112" {
		t.Fatalf("fromHere queue = %+v", state.Queue)
	}

	// reverse flips the order and mirrors the start point: the selection stays
	// the same track, now first, with the rest of the queue following the
	// reversed order (the 喜爱歌曲 mix convention).
	response = call(t, socket, "playback.play", map[string]any{"ref": "apple-music:playlist:pl.1", "startTrackID": "1111111112", "reverse": true})
	if !response.OK {
		t.Fatalf("reverse play: %+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 2 || state.Queue[0].Ref != "apple-music:song:1111111112" || state.QueueIndex != 0 {
		t.Fatalf("reverse queue = %+v index %d", state.Queue, state.QueueIndex)
	}
}

func TestAppleWebTrendingUsesTheSongOnlyCapability(t *testing.T) {
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{songs: fixtureSongs()}, nil)
	response := call(t, socket, "discovery.trending", map[string]any{
		"source": "apple-music", "type": "song", "limit": 1,
	})
	if !response.OK {
		t.Fatalf("discovery.trending: %+v", response.Error)
	}
	var result api.SearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if songs := result.Groups[api.GroupSongs]; len(songs) != 2 || songs[0].Kind != api.KindSong || songs[0].Ref != "apple-music:song:1111111111" {
		t.Fatalf("songs = %+v", songs)
	}
	response = call(t, socket, "discovery.trending", map[string]any{
		"source": "apple-music", "type": "playlist", "limit": 1,
	})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("playlist trending = %+v, want unsupported_command", response)
	}
}

// The browser engine implements the library extensions so the command routes
// by interface assertion, and reports the same unavailable resource runtime the
// MusicKit path yields without a helper: the web player's catalog API has no
// account library, and the capability is declared unavailable.
func TestAppleWebLibraryCommandsReportUnavailableResources(t *testing.T) {
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{}, nil)
	for _, command := range []string{"library.playlists", "library.albums"} {
		response := call(t, socket, command, map[string]any{"source": "apple-music"})
		if response.OK || response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
			t.Fatalf("%s = %+v, want source_unavailable", command, response.Error)
		}
	}
}

func TestAppleWebRecommendationsFlattenPlayableCatalogKinds(t *testing.T) {
	catalog := &fakePageCatalog{recommended: []appleweb.Recommendation{
		{Kind: api.KindPlaylist, ID: "pl.1", Title: "Fixture Mix", Artist: "Made for You", URL: "https://music.apple.com/cn/playlist/pl.1"},
		{Kind: api.KindAlbum, ID: "3333333333", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/3333333333"},
		// The page decoder normally drops this; the provider remains defensive
		// so an impossible browser-mode station can never leak onto Home.
		{Kind: api.KindStation, ID: "st.1", Title: "Fixture Radio"},
	}}
	_, socket, _ := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "recommendations.list", map[string]any{"source": "apple-music", "limit": 5})
	if !response.OK {
		t.Fatalf("recommendations.list: %+v", response.Error)
	}
	var items []api.Item
	if err := json.Unmarshal(response.Data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Ref != "apple-music:playlist:pl.1" || items[1].Ref != "apple-music:album:3333333333" {
		t.Fatalf("items = %+v, want playlist+album and no station", items)
	}
}

func TestAppleWebRecommendationsMapSignedOutToAuthorizationRequired(t *testing.T) {
	catalog := &fakePageCatalog{recommendationsErr: appleweb.ErrUnauthorized}
	_, socket, _ := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "recommendations.list", map[string]any{"source": "apple-music"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeAuthorizationRequired {
		t.Fatalf("recommendations.list = %+v, want authorization_required", response)
	}
}

// A signed-in browser remains unverified until its media length is observed;
// reporting "full" just from authorization would lie across storefronts.
// A signed-out browser reports a known preview without waiting.
// Signed-out items remain previews; neither path guesses full playback.
func TestAppleWebPlayModeFollowsTheSession(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		authorized bool
		wantMode   string
	}{
		{"signed in waits for media length", true, "unverified"},
		{"signed out plays previews", false, "preview"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := &fakePageCatalog{authorized: testCase.authorized, songs: fixtureSongs()}
			_, socket, driver := startAppleWebServer(t, catalog, nil)

			response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
			if !response.OK {
				t.Fatalf("playback.play: %+v", response.Error)
			}
			var state api.PlaybackState
			if err := json.Unmarshal(response.Data, &state); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if state.Mode != testCase.wantMode {
				t.Fatalf("mode = %q, want %q", state.Mode, testCase.wantMode)
			}
			if state.Source != "apple-music" {
				t.Fatalf("source = %q", state.Source)
			}
			// The queue is handed a target whose identity is the catalog id and
			// whose URL is the public page: no media asset travels.
			if len(driver.targets) != 1 {
				t.Fatalf("driver targets = %d, want 1", len(driver.targets))
			}
			target := driver.targets[0]
			if target.Item.ID != "1111111111" {
				t.Fatalf("target item id = %q, want the catalog id", target.Item.ID)
			}
			if target.Item.Source != string(api.SourceAppleMusic) {
				t.Fatalf("target source = %q", target.Item.Source)
			}
			if strings.Contains(string(response.Data), ".m4a") || strings.Contains(string(response.Data), "audio-ssl") {
				t.Fatalf("a media asset leaked into the response:\n%s", response.Data)
			}
		})
	}
}

func TestAppleWebMissingCatalogLengthStaysUnverified(t *testing.T) {
	songs := fixtureSongs()
	songs[0].DurationMs = 0
	catalog := &fakePageCatalog{authorized: true, songs: songs}
	_, socket, driver := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
	if !response.OK {
		t.Fatalf("playback.play: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Mode != "unverified" || len(driver.targets) != 1 || driver.targets[0].Duration != 0 {
		t.Fatalf("missing catalog duration should not claim full: mode=%q targets=%+v", state.Mode, driver.targets)
	}
}

// An album ref on a URL-queue source is expanded into its songs before the
// provider sees it. The terminal reported the failure as a bad reference because
// the album reached PreparePlayback whole.
func TestAppleWebAlbumPlaybackExpandsIntoTracks(t *testing.T) {
	catalog := &fakePageCatalog{
		authorized: true,
		songs:      fixtureSongs(),
		albums:     []appleweb.CatalogAlbum{{ID: "2222222222", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/2222222222"}},
		tracks:     fixtureSongs(),
	}
	_, socket, driver := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:2222222222"})
	if !response.OK {
		t.Fatalf("album playback: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(state.Queue) != 2 || state.QueueIndex != 0 {
		t.Fatalf("queue = %d items at %d, want the album's 2 tracks from the first", len(state.Queue), state.QueueIndex)
	}
	if state.Queue[0].Ref != "apple-music:song:1111111111" {
		t.Fatalf("queue head = %q, want the album's first song", state.Queue[0].Ref)
	}
	if state.Mode != "unverified" {
		t.Fatalf("mode = %q, want unverified until media starts", state.Mode)
	}
	if len(driver.targets) != 1 || driver.targets[0].Item.ID != "1111111111" {
		t.Fatalf("driver targets = %+v, want the first track", driver.targets)
	}
}

// "Play from here" carries the row's track id: the queue must start there and
// drop the earlier songs.
func TestAppleWebAlbumPlaybackFromHereStartsAtTheTrack(t *testing.T) {
	catalog := &fakePageCatalog{
		authorized: true,
		songs:      fixtureSongs(),
		albums:     []appleweb.CatalogAlbum{{ID: "2222222222", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/2222222222"}},
		tracks:     fixtureSongs(),
	}
	_, socket, driver := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "playback.play", map[string]any{
		"ref": "apple-music:album:2222222222", "startTrackID": "1111111112", "fromHere": true,
	})
	if !response.OK {
		t.Fatalf("play from here: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(state.Queue) != 1 || state.Queue[0].Ref != "apple-music:song:1111111112" {
		t.Fatalf("queue = %+v, want only the selected track onwards", state.Queue)
	}
	if len(driver.targets) != 1 || driver.targets[0].Item.ID != "1111111112" {
		t.Fatalf("driver targets = %+v", driver.targets)
	}
}

// startAt selects the album's start the same way, without dropping earlier songs
// from the queue.
func TestAppleWebAlbumPlaybackStartAt(t *testing.T) {
	catalog := &fakePageCatalog{
		authorized: true,
		songs:      fixtureSongs(),
		albums:     []appleweb.CatalogAlbum{{ID: "2222222222", Title: "Fixture Album", Artist: "Fixture Artist", URL: "https://music.apple.com/cn/album/fixture/2222222222"}},
		tracks:     fixtureSongs(),
	}
	_, socket, driver := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:album:2222222222", "startAt": 1})
	if !response.OK {
		t.Fatalf("album playback from index 1: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(state.Queue) != 2 || state.QueueIndex != 1 {
		t.Fatalf("queue = %d items at %d, want the whole album starting at the second", len(state.Queue), state.QueueIndex)
	}
	if len(driver.targets) != 1 || driver.targets[0].Item.ID != "1111111112" {
		t.Fatalf("driver targets = %+v, want the second track", driver.targets)
	}
}

// A source that cannot resolve album tracks must say so about itself, not fall
// through to another source's runtime.
func TestAlbumRefOnASourceWithoutAlbumsIsRefusedLocally(t *testing.T) {
	_, socket, _ := startAppleWebServer(t, &fakePageCatalog{authorized: true, songs: fixtureSongs()}, nil)
	response := call(t, socket, "playback.play", map[string]any{"ref": "audius:album:1"})
	if response.OK || response.Error == nil {
		t.Fatalf("audius album ref = %+v, want a refusal", response)
	}
	if response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("code = %q, want unsupported_command (audius has no album lookup)", response.Error.Code)
	}
	if strings.Contains(response.Error.Message, "Apple") {
		t.Fatalf("the refusal blamed Apple: %q", response.Error.Message)
	}
}

func TestAppleWebRefusesUnknownSongs(t *testing.T) {
	catalog := &fakePageCatalog{authorized: true, songs: fixtureSongs()}
	_, socket, _ := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:9999999999"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("playback.play = %+v, want invalid_reference", response.Error)
	}
}

func TestAppleWebMapsFailures(t *testing.T) {
	catalog := &fakePageCatalog{err: errors.New("the page went away")}
	_, socket, _ := startAppleWebServer(t, catalog, nil)
	response := call(t, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "fixture", "type": "song"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeSearchFailed {
		t.Fatalf("discovery.search = %+v, want search_failed", response.Error)
	}

	// A missing browser is a source problem, not a search problem.
	noBrowser := &fakePageCatalog{}
	_, socket, _ = startAppleWebServer(t, noBrowser, appleweb.ErrNoBrowser)
	// The descriptor already flagged it; a play attempt must stay consistent.
	response = call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
	if response.OK || response.Error == nil {
		t.Fatalf("playback.play without a browser = %+v, want a failure", response)
	}
	if response.Error.Code != api.CodeSourceUnavailable && response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("playback.play code = %q, want source_unavailable or invalid_reference", response.Error.Code)
	}
}

// beginFlow drives Begin and returns the pending flow plus a channel of terminal
// flows, which is the contract the server's flow manager relies on.
func beginFlow(t *testing.T, provider AuthProvider) (api.AuthorizationFlow, chan api.AuthorizationFlow) {
	t.Helper()
	terminal := make(chan api.AuthorizationFlow, 1)
	var pending api.AuthorizationFlow
	update := func(flow api.AuthorizationFlow) { pending = flow }
	complete := func(flow api.AuthorizationFlow) { terminal <- flow }
	if err := provider.Begin(context.Background(), "flow-1", update, complete); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return pending, terminal
}

func awaitFlow(t *testing.T, terminal chan api.AuthorizationFlow) api.AuthorizationFlow {
	t.Helper()
	select {
	case flow := <-terminal:
		return flow
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in flow never completed")
		return api.AuthorizationFlow{}
	}
}

// The pending flow is what tells the client to send the user to the browser: the
// interaction carries the URL, and no credential ever travels this way.
func TestAppleWebSignInFlowPointsAtTheBrowser(t *testing.T) {
	catalog := &fakePageCatalog{}
	provider := NewAppleWebAuthProvider(catalog, func() bool { return false })

	pending, terminal := beginFlow(t, provider)
	if pending.Status != api.FlowPending {
		t.Fatalf("pending flow status = %q, want pending", pending.Status)
	}
	if pending.Interaction.Type != api.InteractionBrowser {
		t.Fatalf("interaction = %+v, want browser", pending.Interaction)
	}
	if !strings.Contains(pending.Interaction.URL, "music.apple.com") {
		t.Fatalf("interaction url = %q", pending.Interaction.URL)
	}
	if flow := awaitFlow(t, terminal); flow.Status != api.FlowAuthorized {
		t.Fatalf("terminal flow = %+v, want authorized", flow)
	}
}

func TestAppleWebSignInFlowFailureModes(t *testing.T) {
	cases := []struct {
		name     string
		signIn   error
		want     string
		wantCode string
	}{
		{"cancelled", context.Canceled, api.FlowCancelled, ""},
		// The declared budget expiring is the expired path: the server builds
		// the flow context from the provider's budget, and the context error is
		// what the sign-in loop returns when nobody finished in time.
		{"expired", context.DeadlineExceeded, api.FlowExpired, ""},
		{"error", errors.New("the window could not open"), api.FlowError, api.CodeAuthorizationFailed},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := &fakePageCatalog{signInErr: testCase.signIn}
			provider := NewAppleWebAuthProvider(catalog, func() bool { return false })
			_, terminal := beginFlow(t, provider)
			flow := awaitFlow(t, terminal)
			if flow.Status != testCase.want {
				t.Fatalf("status = %q, want %q", flow.Status, testCase.want)
			}
			if testCase.wantCode != "" {
				if flow.Error == nil || flow.Error.Code != testCase.wantCode {
					t.Fatalf("error = %+v, want %s", flow.Error, testCase.wantCode)
				}
			}
		})
	}
}

// Cancelling a running flow must end it as cancelled, and must reach the window.
func TestAppleWebSignInFlowCancel(t *testing.T) {
	catalog := &fakePageCatalog{signInBlock: make(chan struct{})}
	provider := NewAppleWebAuthProvider(catalog, func() bool { return false })
	_, terminal := beginFlow(t, provider)
	provider.Cancel("flow-1")
	if flow := awaitFlow(t, terminal); flow.Status != api.FlowCancelled {
		t.Fatalf("flow = %+v, want cancelled", flow)
	}
}

func TestAppleWebDisconnectRemovesTheSession(t *testing.T) {
	provider := NewAppleWebAuthProvider(&fakePageCatalog{}, func() bool { return false })
	if err := provider.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %+v", err)
	}

	foreign := NewAppleWebAuthProvider(&fakePageCatalog{disconnect: fmt.Errorf("%w: /tmp/some-other-profile", appleweb.ErrForeignProfile)}, func() bool { return false })
	apiErr := foreign.Disconnect(context.Background())
	if apiErr == nil || apiErr.Code != api.CodeInvalidState {
		t.Fatalf("Disconnect on a foreign profile = %+v, want invalid_state", apiErr)
	}
	if !strings.Contains(apiErr.Message, "not a lilt browser profile") {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestAppleWebAuthProviderReportsTheSession(t *testing.T) {
	// A session that was never started must not launch a browser to answer, and
	// must not claim it needs nothing: the server refuses to begin a flow for a
	// source that reports not_required, which would make `lilt auth` unreachable.
	never := NewAppleWebAuthProvider(&fakePageCatalog{authorized: true}, func() bool { return false })
	status := never.Describe(context.Background())
	if status.Status != api.AuthNotDetermined {
		t.Fatalf("status = %q, want not_determined", status.Status)
	}
	if !strings.Contains(status.Details["message"].(string), "lilt auth apple-music") {
		t.Fatalf("details = %+v, want the sign-in hint", status.Details)
	}

	started := NewAppleWebAuthProvider(&fakePageCatalog{authorized: true}, func() bool { return true })
	if status := started.Describe(context.Background()); status.Status != api.AuthAuthorized {
		t.Fatalf("status = %q, want authorized", status.Status)
	}
	signedOut := NewAppleWebAuthProvider(&fakePageCatalog{authorized: false}, func() bool { return true })
	status = signedOut.Describe(context.Background())
	if status.Status != api.AuthNotDetermined || !strings.Contains(status.Details["message"].(string), "previews") {
		t.Fatalf("signed-out status = %+v", status)
	}

	// Begin only announces the flow; the terminal state arrives asynchronously.
	if err := started.Begin(context.Background(), "flow-1", nil, func(api.AuthorizationFlow) {}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
}

// Before the warm-up the status is "unverified"; once the session is up the server
// must correct it (and republish), so a client that read the state too early is
// not left with a lie.
func TestAppleWebWarmUpSettlesTheAuthorizationState(t *testing.T) {
	warmDone := make(chan struct{})
	catalog := &fakePageCatalog{authorized: false, warmUpAuthorized: true, warmDone: warmDone}
	_, socket, _ := startAppleWebServer(t, catalog, nil)

	// The session is started in the background by the server itself.
	select {
	case <-warmDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never warmed the Apple session up")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := call(t, socket, "authorization.status", map[string]any{"source": "apple-music"})
		if response.OK {
			var status api.SourceAuthorization
			if err := json.Unmarshal(response.Data, &status); err == nil && status.Status == api.AuthAuthorized {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the authorization state never settled to authorized")
}

func TestAppleWebWarmUpWithoutAProfileStaysLazy(t *testing.T) {
	if os.Getenv("LILT_TEST_FAKE_CDP") == "1" {
		t.Skip("fake peer process")
	}
	dir := t.TempDir()
	engine := appleweb.NewEngine(appleweb.Options{ProfileDir: filepath.Join(dir, "absent")})
	engine.WarmUp(context.Background())
	if engine.Started() {
		t.Fatal("no profile means nothing to warm up")
	}
}

// Signing in mid-playback must stop the Apple playback through the ordinary
// stop path: the watch feed shows an explicit stopped transition (never a
// silent death when the sign-in window takes the browser), no warning treats
// the stop as a fault, and once the flow completes the next start reports the
// mode the live session now has. Everything below drives the real socket.
func TestSignInStopsApplePlaybackAndReSamplesMode(t *testing.T) {
	catalog := &fakePageCatalog{
		authorized:       false,
		songs:            fixtureSongs(),
		signInBlock:      make(chan struct{}),
		signInAuthorized: true,
	}
	_, socket, driver := startAppleWebServer(t, catalog, nil)

	watchCtx, watchCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer watchCancel()
	_, watcher, err := api.Watch(watchCtx, socket, []string{"playback", "server"}, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
	if !response.OK {
		t.Fatalf("preview playback.play: %+v", response.Error)
	}
	var playing api.PlaybackState
	if err := json.Unmarshal(response.Data, &playing); err != nil {
		t.Fatal(err)
	}
	if playing.Mode != "preview" {
		t.Fatalf("initial mode = %q, want preview for a signed-out session", playing.Mode)
	}

	begin := call(t, socket, "authorization.begin", map[string]any{"source": "apple-music", "interactive": true})
	if !begin.OK {
		t.Fatalf("authorization.begin: %+v", begin.Error)
	}
	var flow api.AuthorizationFlow
	if err := json.Unmarshal(begin.Data, &flow); err != nil {
		t.Fatal(err)
	}
	if flow.Status != api.FlowPending {
		t.Fatalf("begin flow = %+v, want pending", flow)
	}

	// The stop the begin issued must be visible on the watch feed, and the
	// stopped state is exactly the stop command's shape.
	stopped := false
	deadline := time.After(3 * time.Second)
	for !stopped {
		select {
		case event := <-watcher.Events:
			if event.Event == "server.warning" {
				t.Fatalf("a normal sign-in stop published a warning: %+v", event)
			}
			if event.Event != "playback.changed" {
				continue
			}
			var payload struct {
				State api.PlaybackState `json:"state"`
			}
			if json.Unmarshal(event.Data, &payload) == nil && payload.State.Status == "stopped" {
				if payload.State.Mode != "none" || payload.State.Track != nil || len(payload.State.Queue) != 0 {
					t.Fatalf("stopped transition = %+v, want the stop command's shape", payload.State)
				}
				stopped = true
			}
		case <-deadline:
			t.Fatal("the watch feed never saw the sign-in stop")
		}
	}
	if driver.stops == 0 {
		t.Fatal("the sign-in stop never reached the driver")
	}

	// The flow finishes once the profile is authorized...
	close(catalog.signInBlock)
	final := waitFlow(t, socket, flow.FlowID, api.FlowAuthorized)
	if final.Status != api.FlowAuthorized {
		t.Fatalf("flow = %+v, want authorized", final)
	}
	// ...and the next start reports the mode the live session now has.
	next := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
	if !next.OK {
		t.Fatalf("playback.play after sign-in: %+v", next.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(next.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Mode != "unverified" {
		t.Fatalf("mode after sign-in = %q, want unverified until media starts", state.Mode)
	}
}

// The mode must follow the live session at every item start, not the plan it
// was frozen into: a session that expires mid-queue turns the next item into a
// preview instead of keeping the queue's original unverified/full verdict.
func TestAppleQueueModeReSamplesTheLiveSessionPerItem(t *testing.T) {
	catalog := &fakePageCatalog{authorized: true, songs: fixtureSongs()}
	_, socket, _ := startAppleWebServer(t, catalog, nil)

	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:1111111111", "apple-music:song:1111111112"},
	})
	if !response.OK {
		t.Fatalf("playback.playSongs: %+v", response.Error)
	}
	var playing api.PlaybackState
	if err := json.Unmarshal(response.Data, &playing); err != nil {
		t.Fatal(err)
	}
	if playing.Mode != "unverified" {
		t.Fatalf("initial mode = %q, want unverified", playing.Mode)
	}

	// The Apple-side session expires underneath the live queue.
	catalog.setAuthorized(false)

	next := call(t, socket, "playback.next", nil)
	if !next.OK {
		t.Fatalf("playback.next: %+v", next.Error)
	}
	var advanced api.PlaybackState
	if err := json.Unmarshal(next.Data, &advanced); err != nil {
		t.Fatal(err)
	}
	if advanced.QueueIndex != 1 {
		t.Fatalf("queue index = %d, want the second item", advanced.QueueIndex)
	}
	if advanced.Mode != "preview" {
		t.Fatalf("mode after expiry = %q, want preview re-sampled at the item start", advanced.Mode)
	}
}

// The structural half of provider admission must hold for this composition too.
func TestProviderGateHoldsForTheAppleWebComposition(t *testing.T) {
	server, socket, _ := startAppleWebServer(t, &fakePageCatalog{songs: fixtureSongs()}, nil)
	assertProviderGate(t, server, socket)
}
