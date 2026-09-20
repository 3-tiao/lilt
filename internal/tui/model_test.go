package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// plainText strips styling so assertions can read the UI as a user would.
func plainText(s string) string { return ansi.Strip(s) }

type fake struct {
	mu               sync.Mutex
	state            core.PlaybackState
	played           core.PlaybackRequest
	radioURL         string
	tracks           []core.Item
	stateCalls       int
	stops            int
	queueJumps       int
	probed           []string
	probeResult      core.RadioProbeResult
	probeErr         error
	searches         []searchCall
	trending         []searchCall
	playSongSet      []string
	enqueuePositions []string
	audiusLibrary    []core.Item
	playStarted      chan struct{}
	playBlock        chan struct{}
	nexts            int
}

type searchCall struct{ source, term, kind string }

type fakeRadio struct{}

type recordingRemote struct {
	block chan struct{}
	err   error
	ops   []string
}

func (r *recordingRemote) wait(op string) error {
	r.ops = append(r.ops, op)
	if r.block != nil {
		<-r.block
	}
	return r.err
}
func (r *recordingRemote) SetLastSource(context.Context, string) error {
	return r.wait("ui.set:lastSource")
}
func (r *recordingRemote) SetTheme(context.Context, string) error { return r.wait("ui.set:theme") }
func (r *recordingRemote) SetFavorite(context.Context, string, core.Item, bool) error {
	return r.wait("favorites.set")
}
func (r *recordingRemote) AuthorizationStatus(context.Context, string) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "authorized"}, nil
}

func (fakeRadio) Countries(context.Context) ([]radio.Country, error) {
	return []radio.Country{{Name: "Japan", Code: "JP", StationCount: 1}}, nil
}
func (fakeRadio) Tags(context.Context) ([]radio.Tag, error) {
	return []radio.Tag{{Name: "City Pop", StationCount: 1}}, nil
}
func (fakeRadio) Languages(context.Context) ([]radio.Language, error) {
	return []radio.Language{{Name: "Japanese", StationCount: 1}}, nil
}
func (fakeRadio) Popular(context.Context, radio.Filter, int, int) ([]radio.Station, error) {
	return []radio.Station{{Name: "Popular", URL: "https://radio.example/popular"}}, nil
}
func (fakeRadio) SearchFiltered(context.Context, string, radio.Filter, int, int) ([]radio.Station, error) {
	return []radio.Station{{Name: "Found", URL: "https://radio.example/found"}}, nil
}
func (fakeRadio) StreamName(context.Context, string) string { return "" }

type namingRadio struct{ fakeRadio }

func (namingRadio) StreamName(context.Context, string) string { return "Indie Pop Rocks" }

type recordingRadio struct {
	fakeRadio
	popularLimit, searchLimit   int
	popularOffset, searchOffset int
	filter                      radio.Filter
}

func (r *recordingRadio) Popular(_ context.Context, f radio.Filter, limit, offset int) ([]radio.Station, error) {
	r.popularLimit, r.popularOffset, r.filter = limit, offset, f
	return r.fakeRadio.Popular(context.Background(), f, limit, offset)
}
func (r *recordingRadio) SearchFiltered(_ context.Context, _ string, f radio.Filter, offset, limit int) ([]radio.Station, error) {
	r.searchLimit, r.searchOffset, r.filter = limit, offset, f
	return r.fakeRadio.SearchFiltered(context.Background(), "", f, offset, limit)
}

func (f *fake) Search(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "1", Title: "One", Artist: "Artist"}}, nil
}
func (f *fake) SearchPlaylists(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "p1", Title: "Playlist"}}, nil
}
func (f *fake) SearchSource(_ context.Context, source, term, kind string, _ int) ([]core.Item, error) {
	f.searches = append(f.searches, searchCall{source: source, term: term, kind: kind})
	if source == "audius" {
		if kind == "playlist" {
			return []core.Item{{Source: source, Kind: "playlist", ID: "p1", Ref: "audius:playlist:p1", Title: "Audius Playlist"}}, nil
		}
		return []core.Item{{Source: source, Kind: "song", ID: "s1", Ref: "audius:song:s1", Title: "Audius Song", Artist: "Creator"}}, nil
	}
	if kind == "album" {
		return []core.Item{{Source: source, Kind: "album", ID: "al1", Ref: source + ":album:al1", Title: "Album"}}, nil
	}
	if kind == "playlist" {
		return f.SearchPlaylists(context.Background(), term, 20)
	}
	return f.Search(context.Background(), term, 20)
}
func (f *fake) TrendingSource(_ context.Context, source, kind string, _ int) ([]core.Item, error) {
	f.trending = append(f.trending, searchCall{source: source, kind: kind})
	if source == "audius" {
		if kind == "playlist" {
			return []core.Item{{Source: source, Kind: "playlist", ID: "p1", Ref: "audius:playlist:p1", Title: "Audius Playlist"}}, nil
		}
		return []core.Item{{Source: source, Kind: "song", ID: "s1", Ref: "audius:song:s1", Title: "Audius Song", Artist: "Creator"}}, nil
	}
	return nil, errors.New("trending unsupported")
}
func (f *fake) LibraryPlaylists(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "p1", Title: "My Playlist"}}, nil
}
func (f *fake) LibraryPlaylistsSource(_ context.Context, source string) ([]core.Item, error) {
	if source == "audius" {
		return f.audiusLibrary, nil
	}
	return f.LibraryPlaylists(context.Background())
}
func (f *fake) LibraryAlbumsSource(context.Context, string) ([]core.Item, error) {
	return []core.Item{{Kind: "album", ID: "al1", Ref: "apple-music:album:al1", Title: "Library Album", Artist: "Artist"}}, nil
}
func (f *fake) PlaylistTracks(context.Context, string) ([]core.Item, error) {
	if f.tracks != nil {
		return f.tracks, nil
	}
	return []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}, nil
}
func (f *fake) PlaylistTracksSource(ctx context.Context, _ string, ref string) ([]core.Item, error) {
	return f.PlaylistTracks(ctx, ref)
}

func (f *fake) AlbumTracksSource(ctx context.Context, _ string, ref string) (core.Item, []core.Item, error) {
	album := core.Item{Kind: "album", ID: ref, Ref: ref, Title: "Library Album", Artist: "Artist"}
	return album, []core.Item{
		{Kind: "song", ID: "a1", Title: "Album Song One", Artist: "Artist"},
		{Kind: "song", ID: "a2", Title: "Album Song Two", Artist: "Artist"},
	}, nil
}
func (f *fake) RecentPlayed(context.Context, int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "r1", Title: "Recent"}}, nil
}
func (f *fake) Stations(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "station", ID: "st1", Title: "Station"}}, nil
}
func (f *fake) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "denied"}, nil
}
func (f *fake) Play(_ context.Context, request core.PlaybackRequest) error {
	f.mu.Lock()
	f.played = request
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "One"}, Shuffle: f.state.Shuffle}
	started, block := f.playStarted, f.playBlock
	if started != nil {
		close(started)
		f.playStarted = nil
	}
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}
func (f *fake) Pause(context.Context) error  { f.state.Status = "paused"; return nil }
func (f *fake) Resume(context.Context) error { f.state.Status = "playing"; return nil }
func (f *fake) Next(context.Context) error {
	f.mu.Lock()
	f.nexts++
	f.mu.Unlock()
	return nil
}
func (f *fake) Previous(context.Context) error { return nil }
func (f *fake) State(context.Context) (core.PlaybackState, error) {
	f.stateCalls++
	return f.state, nil
}
func (f *fake) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	err := f.Play(ctx, request)
	return f.state, err
}
func (f *fake) PauseState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Pause(ctx)
	return f.state, err
}
func (f *fake) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Resume(ctx)
	return f.state, err
}
func (f *fake) NextState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Next(ctx)
	return f.state, err
}
func (f *fake) PreviousState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Previous(ctx)
	return f.state, err
}
func (f *fake) SetShuffle(_ context.Context, on bool) (core.PlaybackState, error) {
	f.state.Shuffle = on
	return f.state, nil
}
func (f *fake) SetRepeat(_ context.Context, mode string) (core.PlaybackState, error) {
	f.state.Repeat = mode
	return f.state, nil
}
func (f *fake) Stop(context.Context) (core.PlaybackState, error) {
	f.stops++
	f.state.Status = "stopped"
	return f.state, nil
}
func (f *fake) Enqueue(_ context.Context, _ core.PlaybackRequest, position string, _ uint64) (core.PlaybackState, error) {
	f.enqueuePositions = append(f.enqueuePositions, position)
	return f.state, nil
}
func (f *fake) RadioPlay(_ context.Context, url, name string) (core.PlaybackState, error) {
	f.radioURL = url
	f.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: url, Title: name}}
	return f.state, nil
}
func (f *fake) RadioStop(context.Context) (core.PlaybackState, error) {
	f.state = core.PlaybackState{Status: "stopped", Mode: "none"}
	return f.state, nil
}

func (f *fake) Probe(_ context.Context, url string, _ int) (core.RadioProbeResult, error) {
	f.probed = append(f.probed, url)
	if f.probeErr != nil {
		return core.RadioProbeResult{}, f.probeErr
	}
	if f.probeResult.Status == "" {
		return core.RadioProbeResult{Status: "healthy", LatencyMs: 120}, nil
	}
	return f.probeResult, nil
}
func (f *fake) PlaySongs(_ context.Context, ids []string, startIndex int) (core.PlaybackState, error) {
	f.playSongSet = append([]string(nil), ids...)
	queue := make([]core.Item, 0, len(ids))
	for _, id := range ids {
		queue = append(queue, core.Item{Kind: "song", ID: id})
	}
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: queue, QueueIndex: startIndex}
	return f.state, nil
}
func (f *fake) QueueJump(_ context.Context, index int, _ uint64) (core.PlaybackState, error) {
	f.queueJumps++
	f.state.QueueIndex = index
	return f.state, nil
}
func (f *fake) QueueRemove(_ context.Context, index int, _ uint64) (core.PlaybackState, error) {
	if index >= 0 && index < len(f.state.Queue) {
		f.state.Queue = append(f.state.Queue[:index], f.state.Queue[index+1:]...)
	}
	return f.state, nil
}
func (f *fake) QueueMove(context.Context, int, int, uint64) (core.PlaybackState, error) {
	return f.state, nil
}
func (f *fake) QueueClear(context.Context, uint64) (core.PlaybackState, error) {
	f.state.Queue = nil
	f.state.QueueIndex = 0
	return f.state, nil
}

func newModel(t *testing.T) (Model, *fake, *state.Store) {
	t.Helper()
	f := &fake{}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	opts := Options{
		Provider:      f,
		Player:        f,
		Radio:         fakeRadio{},
		Store:         store,
		Authorization: core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready"},
		Source:        "apple-music",
	}
	m := New(opts)
	// Seed the capability snapshot the TUI fetches at startup.
	descriptors, _ := f.Sources(context.Background())
	next, _ := m.Update(sourcesMsg{descriptors: descriptors})
	return next.(Model), f, store
}

func (f *fake) Sources(context.Context) ([]api.SourceDescriptor, error) {
	return []api.SourceDescriptor{
		{ID: api.SourceAppleMusic, Available: true, Availability: api.AvailabilityReady, Capabilities: map[string]api.Capability{
			api.CapSearchSongs: {Available: true}, api.CapSearchAlbums: {Available: true}, api.CapSearchPlaylists: {Available: true}, api.CapSearchStations: {Available: true},
			api.CapLibrary: {Available: true}, api.CapShuffle: {Available: true}, api.CapRepeat: {Available: true},
			api.CapRecommendations: {Available: true}, api.CapPlaybackFull: {Available: true}, api.CapQueue: {Available: true},
		}},
		{ID: api.SourceAudius, Available: true, Availability: api.AvailabilityReady, Capabilities: map[string]api.Capability{
			api.CapSearchSongs: {Available: true}, api.CapSearchPlaylists: {Available: true}, api.CapSearchTrending: {Available: true},
			api.CapPlaybackFull: {Available: true}, api.CapQueue: {Available: true}, api.CapLibrary: {Available: true},
		}},
		{ID: api.SourceRadio, Available: true, Availability: api.AvailabilityReady, Capabilities: map[string]api.Capability{
			api.CapSearchRadio: {Available: true}, api.CapPlaybackStream: {Available: true},
		}},
	}, nil
}

func run(m Model, cmd tea.Cmd) Model {
	for steps := 0; cmd != nil && steps < 4; steps++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			_ = batch
			return m // Timers/load batches are exercised explicitly by drainAll.
		}
		if _, isTick := msg.(tickMsg); isTick {
			break
		}
		next, follow := m.Update(msg)
		m = next.(Model)
		if persisted, ok := msg.(persistenceMsg); ok && persisted.err == nil {
			// Server commits are observed through state.changed, not through the
			// persistence response. Most model tests use an in-process Remote, so
			// synthesize that authoritative projection after the response.
			projection := appStateFixture(m, persisted)
			m.applyAppState(projection)
		}
		switch value := msg.(type) {
		case sourceSwitchMsg:
			cmd = follow
		case actionMsg:
			if value.addFavorite {
				cmd = follow
			} else {
				cmd = nil
			}
		default:
			cmd = nil
		}
	}
	return m
}

func appStateFixture(m Model, persisted persistenceMsg) api.AppState {
	projection := api.AppState{Revision: m.appRevision + 1, Theme: m.store.Theme, LastSource: api.SourceID(m.store.LastSource)}
	if m.activity != nil {
		projection.Favorites = append(projection.Favorites, m.activity.favorites...)
		projection.Recent = append(projection.Recent, m.activity.recent...)
	}
	switch persisted.kind {
	case "source":
		projection.LastSource = api.SourceID(persisted.source)
	case "theme":
		projection.Theme = persisted.theme
	case "favorite":
		id := canonicalFavoriteID(persisted.source, persisted.item)
		kept := projection.Favorites[:0]
		for _, item := range projection.Favorites {
			if item.Source != api.SourceID(persisted.source) || item.ID != id {
				kept = append(kept, item)
			}
		}
		projection.Favorites = kept
		if persisted.favorited {
			projection.Favorites = append(projection.Favorites, api.Item{Source: api.SourceID(persisted.source), Kind: persisted.item.Kind, ID: id, ProviderID: persisted.item.ID, Ref: persisted.item.Ref, URL: persisted.item.URL, Title: persisted.item.Title, Artist: persisted.item.Artist})
		}
	}
	return projection
}

// canonicalFavoriteID mirrors the server's canonical stable identity for a
// favorite item (docs/internals/local-activity.md §4): client spellings like
// "apple-music:123" or "audius:track-1" must not create a second identity.
func canonicalFavoriteID(source string, item core.Item) string {
	switch source {
	case "radio":
		return stableItemID(source, item)
	case "audius":
		kind := item.Kind
		if kind == "" {
			kind = "song"
		}
		parts := strings.SplitN(strings.TrimPrefix(item.ID, "audius:"), ":", 2)
		id := item.ID
		if len(parts) == 2 {
			id = parts[1]
		}
		return "audius:" + kind + ":" + id
	default:
		id := strings.TrimPrefix(item.ID, "am:")
		id = strings.TrimPrefix(id, "apple-music:")
		if i := strings.Index(id, ":"); i >= 0 && strings.HasPrefix(item.ID, "apple-music:") {
			id = id[i+1:]
		}
		return "am:" + id
	}
}

// seedFavorite toggles a favorite directly in the client mirror, standing in
// for the server-side write the real flow performs.
func seedFavorite(m *Model, source string, item core.Item) bool {
	if m.activity == nil {
		m.activity = &activityMirror{}
	}
	id := stableItemID(source, item)
	kept := m.activity.favorites[:0]
	found := false
	for _, favorite := range m.activity.favorites {
		if favorite.Source == api.SourceID(source) && favorite.ID == id {
			found = true
			continue
		}
		kept = append(kept, favorite)
	}
	m.activity.favorites = kept
	if found {
		return false
	}
	kind := item.Kind
	if kind == "" {
		kind = "song"
	}
	m.activity.favorites = append(m.activity.favorites, api.Item{Source: api.SourceID(source), Kind: kind, ID: canonicalFavoriteID(source, item), ProviderID: item.ID, Ref: item.Ref, URL: item.URL, Title: item.Title, Artist: item.Artist})
	return true
}

// seedRecent prepends a qualified play to the client mirror, standing in for
// the server's threshold write.
func seedRecent(m *Model, source string, item core.Item) {
	if m.activity == nil {
		m.activity = &activityMirror{}
	}
	kind := item.Kind
	if kind == "" {
		kind = "song"
	}
	if source == "radio" {
		kind = "stream"
	}
	m.activity.recent = append([]api.RecentEntry{{
		Item:     api.Item{Source: api.SourceID(source), Kind: kind, ID: stableItemID(source, item), ProviderID: item.ID, Ref: item.Ref, URL: item.URL, Title: item.Title, Artist: item.Artist},
		PlayedAt: time.Now().UTC().Format(time.RFC3339),
	}}, m.activity.recent...)
}

func runMutation(m Model, build func(*Model) tea.Cmd) Model {
	next, cmd := m.startMutation(build)
	return run(next.(Model), cmd)
}

func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestInitLoadsHome(t *testing.T) {
	m, _, _ := newModel(t)
	m = drainAll(m, m.Init())
	if m.title != "Home" || !hasHeader(m.items, "Your Playlists") || m.loading {
		t.Fatalf("title=%q items=%d loading=%v", m.title, len(m.items), m.loading)
	}
}

func TestAudiusDiscoverLoadsTrending(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view, m.title = "audius", "Discover", "Discover"
	m.loading = true
	m = run(m, m.loadView())
	titles := make([]string, 0, len(m.items))
	for _, item := range m.items {
		titles = append(titles, item.Title)
	}
	joined := strings.Join(titles, "|")
	for _, want := range []string{"Trending Songs", "Audius Song", "Trending Playlists", "Audius Playlist"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Discover items missing %q: %v", want, titles)
		}
	}
	if len(f.trending) != 2 || f.trending[0].source != "audius" || f.trending[0].kind != "song" {
		t.Fatalf("trending calls = %#v", f.trending)
	}
}

func TestAudiusSearchPlaybackFavoritesAndRecent(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view, m.title = "audius", "Discover", "Discover"
	next, _ := m.openTextInput("search", "Search: ", "query", "indie")
	m = next.(Model)
	next, cmd := m.submitInput()
	m = next.(Model)
	m = run(m, cmd)
	if m.source != "audius" || m.title != "Search: indie" || len(m.items) != 4 {
		t.Fatalf("Audius search = source=%q title=%q items=%#v", m.source, m.title, m.items)
	}
	if len(f.searches) != 2 || f.searches[0].source != "audius" || f.searches[0].kind != "song" || f.searches[1].kind != "playlist" {
		t.Fatalf("source-aware searches = %#v", f.searches)
	}
	m.selected = 1 // Songs header is first.
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playSelected() })
	if f.played.Ref != "audius:song:s1" {
		t.Fatalf("Audius playback ref = %#v", f.played)
	}
	// The server owns recent writes; seed its source-keyed state projection as a
	// fake provider would expose it after the successful playback.
	seedRecent(&m, "audius", m.items[m.selected])
	if got := m.activity.RecentFor("audius"); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("Audius recent = %#v", got)
	}
	next, cmd = m.toggleFavorite()
	m = run(next.(Model), cmd)
	if got := m.activity.FavoritesFor("audius"); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("Audius favorites = %#v", got)
	}
	next, cmd = m.switchSource("audius")
	if cmd != nil || next.(Model).source != "audius" {
		t.Fatalf("same source switch = %#v cmd=%v", next, cmd != nil)
	}
	m.source, m.view = "audius", "Recent"
	m.loading = true
	m = run(m, m.loadView())
	if len(m.items) != 1 || m.items[0].Title != "Audius Song" {
		t.Fatalf("Audius recent view = %#v", m.items)
	}
}

func TestPlaylistDetailHighlightsCurrentAudiusTrack(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.detailKind, m.detailID = "audius", "Discover", "playlist", "p1"
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Mix"}
	m.state = core.PlaybackState{Source: "audius", Status: "playing", Track: &core.Item{Kind: "song", ID: "s1", Title: "Current"}}
	if !m.isPlayingItem(core.Item{Kind: "song", ID: "s1", Title: "Current"}) {
		t.Fatal("Audius playlist detail did not identify its current track")
	}
}

func TestRecentHeadersAreNotActionable(t *testing.T) {
	m, _, store := newModel(t)
	m.items = []core.Item{{Kind: "header", Title: "Songs"}, {Kind: "song", ID: "s1", Title: "Song"}}
	m.selected = 0
	next, _ := m.toggleFavorite()
	if got := next.(Model); len(m.activity.FavoritesFor("apple-music")) != 0 || got.message != "Nothing selected" {
		t.Fatalf("header favorite = favorites=%#v message=%q", m.activity.FavoritesFor("apple-music"), got.message)
	}
	m.width, m.height = 100, 24
	y := m.layout().listTop + 1
	next, _ = m.handleMouse(mouseClick(5, y))
	if got := next.(Model); got.selected != 0 {
		t.Fatalf("mouse on header = %#v", got.selected)
	}
	_ = store
}

func TestSourceSwitcherReplacesSourceTabs(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey('s'))
	m = next.(Model)
	if m.overlay != "source-switcher" || strings.Contains(plainText(m.sourceLine(100)), "Audius") {
		t.Fatalf("source switcher/header = overlay=%q header=%q", m.overlay, m.sourceLine(100))
	}
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "audius" || m.view != "Home" || strings.Join(viewsFor(m.source), ",") != "Home,Discover,Recent" {
		t.Fatalf("Audius switch = source=%q view=%q views=%v", m.source, m.view, viewsFor(m.source))
	}
}

func TestSourceSwitcherStopsAtomicallyAndEscCancels(t *testing.T) {
	m, f, _ := newModel(t)
	m.state = core.PlaybackState{Source: "apple-music", Status: "playing", Mode: "full", Queue: []core.Item{{Kind: "song", ID: "s1"}}}
	next, _ := m.handleKey(runeKey('s'))
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.source != "apple-music" || m.overlay != "" || f.stops != 0 {
		t.Fatalf("Esc changed source/playback: source=%q overlay=%q stops=%d", m.source, m.overlay, f.stops)
	}
	next, _ = m.handleKey(runeKey('s'))
	m = next.(Model)
	m.overlaySelected = sourceIndex("radio")
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "radio" || m.state.Status != "stopped" || f.stops != 1 || m.overlay != "" {
		t.Fatalf("atomic switch = source=%q status=%q stops=%d overlay=%q", m.source, m.state.Status, f.stops, m.overlay)
	}
}

func TestLongQueueTitlesDoNotWrapOrOverflow(t *testing.T) {
	m, _, _ := newModel(t)
	longTitle := strings.Repeat("Very Long Queue Title ", 5) + "Grand Funk Railroad"
	longArtist := strings.Repeat("Extremely Long Artist ", 4)
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", Duration: 300,
		QueueIndex: 0,
		Queue: []core.Item{
			{Kind: "song", ID: "1", Title: longTitle, Artist: longArtist},
			{Kind: "song", ID: "2", Title: longTitle + " (Live)", Artist: longArtist},
		},
		Track: &core.Item{Kind: "song", ID: "1", Title: longTitle, Artist: longArtist},
	}
	for _, size := range [][2]int{{120, 30}, {60, 24}} {
		m.width, m.height = size[0], size[1]
		view := plainText(m.View().Content)
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size=%v lines=%d, want %d", size, len(lines), size[1])
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != size[0] {
				t.Fatalf("size=%v line %d width=%d, want %d: %q", size, i, got, size[0], line)
			}
		}
		if size[0] >= 88 && !strings.Contains(view, "UP NEXT") {
			t.Fatalf("panel missing at size %v", size)
		}
	}
}

func TestBufferingFreezesInterpolatedProgress(t *testing.T) {
	m, _, _ := newModel(t)
	at := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	m.state = core.PlaybackState{Status: "buffering", Position: 10, Duration: 100}
	m.snapshotAt = at
	if got := m.displayPositionAt(at.Add(5 * time.Second)); got != 10 {
		t.Fatalf("buffering position = %v, want frozen at 10", got)
	}
	if facts := m.playbackFacts(80); !strings.Contains(facts, "Buffering…") {
		t.Fatalf("facts = %q, want buffering state", facts)
	}
}

func TestPlayItemPlaylistKeepsQueueSource(t *testing.T) {
	m, _, _ := newModel(t)
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playItem(core.Item{Kind: "playlist", ID: "p1", Title: "Road"}) })
	if m.queueSource != (queueContext{Kind: "playlist", ID: "p1", Title: "Road"}) {
		t.Fatalf("queue source = %#v", m.queueSource)
	}
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playItem(core.Item{Kind: "song", ID: "s1", Title: "One"}) })
	if m.queueSource != (queueContext{}) {
		t.Fatalf("song should clear queue source: %#v", m.queueSource)
	}
}

func TestNarrowFooterKeepsQueueHint(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}}}
	footer := m.footerLine(60)
	if !strings.Contains(footer, "0 Up Next") {
		t.Fatalf("queue hint lost at width 60: %q", footer)
	}
	if strings.Contains(footer, "Tab source") {
		t.Fatalf("low-priority hints should drop first: %q", footer)
	}
}

func TestPlaybackErrorTextKeepsTransportDetailsOutOfUserCopy(t *testing.T) {
	wrapped := fmt.Errorf("%w: %v", api.ErrTransport, errors.New("dial unix: i/o timeout"))
	if got := playbackErrorText(wrapped); strings.Contains(got, "i/o timeout") || strings.Contains(got, "transport") {
		t.Fatalf("transport error copy = %q, want actionable text", got)
	}
	if got := playbackErrorText(wrapped); got != "Playback start timed out — try again" {
		t.Fatalf("timeout copy = %q", got)
	}
	generic := errors.New("provider_code: station unavailable")
	if got := playbackErrorText(generic); got != "Playback error: provider_code: station unavailable" {
		t.Fatalf("generic copy = %q", got)
	}
}

func TestBusyDockShowsElapsedSecondsAfterLongStart(t *testing.T) {
	m, _, _ := newModel(t)
	m.busy, m.busySince = true, time.Now().Add(-8*time.Second)
	m.renderTime = time.Now()
	lines := m.nowBody(80)
	if !strings.Contains(lines[0], "working… 8s") {
		t.Fatalf("long busy dock = %q, want elapsed seconds", lines[0])
	}

	m.busySince, m.renderTime = time.Now(), time.Now()
	lines = m.nowBody(80)
	if strings.Contains(lines[0], "8s") {
		t.Fatalf("fresh busy dock = %q, want plain working…", lines[0])
	}

	m.busySince = time.Time{}
	lines = m.nowBody(80)
	if !strings.Contains(lines[0], "working…") {
		t.Fatalf("zero busySince dock = %q", lines[0])
	}
}

func TestQueueAppendSendsWirePosition(t *testing.T) {
	m, f, _ := newModel(t)
	m.view = "Discover"
	m.items = []core.Item{
		{Kind: "header", Title: "Songs"},
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One", Artist: "A"},
		{Kind: "song", ID: "s2", Ref: "apple-music:song:s2", Title: "Two", Artist: "B"},
	}
	m.selected = 1

	next, cmd := m.handleKey(runeKey('E'))
	m = run(next.(Model), cmd)
	if len(f.enqueuePositions) != 1 || f.enqueuePositions[0] != "append" {
		t.Fatalf("E enqueue positions = %#v, want [append]", f.enqueuePositions)
	}
	if !strings.Contains(m.message, "Added to queue") {
		t.Fatalf("append feedback = %q", m.message)
	}
	next, cmd = m.handleKey(runeKey('e'))
	m = run(next.(Model), cmd)
	if len(f.enqueuePositions) != 2 || f.enqueuePositions[1] != "next" {
		t.Fatalf("e enqueue positions = %#v, want [append next]", f.enqueuePositions)
	}
	if !strings.Contains(m.message, "Playing next") {
		t.Fatalf("queue-next feedback = %q", m.message)
	}
}

func TestQueueRemoveShowsFeedback(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if !strings.Contains(m.message, "Removed current track — playback advanced") {
		t.Fatalf("current-track removal feedback = %q", m.message)
	}

	m, f, _ = newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}}
	m.state = f.state
	next, _ = m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	next, cmd = m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if !strings.Contains(m.message, "Removed: B") {
		t.Fatalf("queue removal feedback = %q", m.message)
	}
}

func TestQueueReorderFeedback(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}, {Kind: "song", ID: "3", Title: "C"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('K'))
	m = next.(Model)
	m = run(m, cmd)
	if !strings.Contains(m.message, "Queue reordered") {
		t.Fatalf("reorder feedback = %q", m.message)
	}
}

func TestStartupTransientShowsSingleStatus(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	track := core.Item{Kind: "song", ID: "1", Title: "Song"}
	m.state = core.PlaybackState{Status: "paused", Mode: "full", Track: &track, Position: 0}
	m.busy, m.operationID = true, 1
	lines := strings.Join(m.nowBody(100), "\n")
	if !strings.Contains(lines, "Starting") || strings.Contains(lines, "working…") {
		t.Fatalf("startup transient not collapsed:\n%s", lines)
	}

	m.state.Position = 42
	lines = strings.Join(m.nowBody(100), "\n")
	if !strings.Contains(lines, "Paused") || !strings.Contains(lines, "working…") {
		t.Fatalf("mid-track pause should stay paused:\n%s", lines)
	}

	// MusicKit often reports paused at position 0 while the play command is
	// still in flight (the start mutation can run up to 60s).
	m.state.Position = 0
	m.busySince = time.Now()
	m.renderTime = m.busySince
	lines = strings.Join(m.nowBody(100), "\n")
	if !strings.Contains(lines, "Starting") {
		t.Fatalf("in-flight startup transient should read as starting:\n%s", lines)
	}

	// A settled paused-at-zero finite track is a finished queue (MusicKit
	// resets position when the last entry ends): Paused, never "Starting…".
	m.busy, m.busySince = false, time.Time{}
	lines = strings.Join(m.nowBody(100), "\n")
	if !strings.Contains(lines, "Paused") || strings.Contains(lines, "Starting") {
		t.Fatalf("settled paused@0 must read as Paused, not endless Starting:\n%s", lines)
	}
}

func TestEmptyStateHints(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.view = "Browse"
	m.title = "Browse"
	m.loading = false
	m.items = nil
	if view := plainText(m.View().Content); !strings.Contains(view, "press / to search") {
		t.Fatalf("empty hint missing:\n%s", view)
	}
}

func TestPlaybackFactsRowContract(t *testing.T) {
	m, _, _ := newModel(t)
	m.width = 100
	track := core.Item{Kind: "song", ID: "1", Title: "Song", Artist: "Artist"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &track, Position: 47, Duration: 308}
	facts := plainText(m.playbackFacts(96))
	if strings.Contains(facts, "[") {
		t.Fatalf("facts row should not use brackets: %q", facts)
	}
	for _, want := range []string{"▶ Playing", "0:47", "5:08", "━", "─"} {
		if !strings.Contains(facts, want) {
			t.Fatalf("facts row missing %q: %q", want, facts)
		}
	}
	m.state.Shuffle = true
	m.state.Repeat = "all"
	if facts := plainText(m.playbackFacts(96)); !strings.Contains(facts, "⇄") || !strings.Contains(facts, "↻ All") {
		t.Fatalf("modes missing from facts row: %q", facts)
	}
	m.state.Format = "System-selected"
	if facts := plainText(m.playbackFacts(96)); strings.Contains(facts, "System-selected") {
		t.Fatalf("placeholder format leaked as fact: %q", facts)
	}
	m.state.Format = "ALAC 24/48"
	if facts := plainText(m.playbackFacts(96)); !strings.Contains(facts, "ALAC 24/48") {
		t.Fatalf("reported codec missing: %q", facts)
	}
}

func TestNowBodyIdentityRow(t *testing.T) {
	m, _, _ := newModel(t)
	track := core.Item{Kind: "song", ID: "1", Title: "Song", Artist: "Artist"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &track, Position: 5, Duration: 60}
	body := m.nowBody(80)
	if len(body) != 2 {
		t.Fatalf("now body rows = %d, want fixed 2:\n%v", len(body), body)
	}
	if !strings.Contains(body[0], "Song — Artist") {
		t.Fatalf("identity row missing:\n%v", body)
	}
	if strings.TrimSpace(body[1]) == "" {
		t.Fatalf("facts row missing:\n%v", body)
	}
}

func TestListLoadingRefreshAndErrorRendering(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 24
	m.loading = true
	if title := m.listTitle(); title != "" && strings.Contains(title, "loading…") {
		t.Fatalf("header carries load state: %q", title)
	}
	if lines := strings.Join(m.listLines(80, 10), "\n"); !strings.Contains(lines, "loading…") {
		t.Fatalf("initial loading body = %q", lines)
	}
	m.items = []core.Item{{Kind: "song", ID: "1", Title: "Ready"}}
	if title := m.listTitle(); strings.Contains(title, "refreshing…") || strings.Contains(title, "loading…") {
		t.Fatalf("header carries refresh state: %q", title)
	}
	if lines := strings.Join(m.listLines(80, 10), "\n"); !strings.Contains(lines, "Ready") || !strings.Contains(lines, "refreshing…") {
		t.Fatalf("refresh state = %q", lines)
	}
	m.loading, m.items, m.listErr = false, nil, "Unable to load list: offline"
	if lines := strings.Join(m.listLines(80, 10), "\n"); !strings.Contains(lines, "Unable to load list") || strings.Contains(lines, "press / to search") {
		t.Fatalf("load error did not take precedence: %q", lines)
	}
}

func TestBufferingUsesConsistentProgressText(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{
		Status: "buffering",
		Mode:   "full",
		Track:  &core.Item{Kind: "song", ID: "1", Title: "Song"},
	}
	if title := m.nowTitle(); title != "Now Playing" {
		t.Fatalf("buffering title = %q, want fixed identity", title)
	}
	lines := strings.Join(m.nowBody(80), "\n")
	if !strings.Contains(lines, "Buffering…") {
		t.Fatalf("buffering state line = %q", lines)
	}
}

func TestRecentLocalViewsDoNotEnterLoadingState(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Home"
	next, cmd := m.selectView(2) // Radio Recent
	local := next.(Model)
	if cmd != nil || local.loading || local.view != "Recent" {
		t.Fatalf("local Radio view = loading=%v view=%q cmd=%v", local.loading, local.view, cmd != nil)
	}
}

func TestAsyncActionEntryPathsMarkBusy(t *testing.T) {
	for _, key := range []rune{'S', 'R', 'e', 'E'} {
		t.Run(string(key), func(t *testing.T) {
			m, _, _ := newModel(t)
			m.items = []core.Item{{Kind: "song", ID: "1", Title: "Song"}}
			next, cmd := m.handleKey(runeKey(key))
			if cmd == nil || !next.(Model).busy {
				t.Fatalf("%q did not mark action busy", key)
			}
		})
	}
	m, _, _ := newModel(t)
	m.items = []core.Item{{Kind: "song", ID: "1", Title: "Song"}}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !next.(Model).busy {
		t.Fatal("Enter activation did not mark action busy")
	}
}

func TestRecentIsSourceScopedAndSongOnly(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Recent"
	seedRecent(&m, "apple-music", core.Item{Kind: "song", ID: "s1", Title: "Song One"})
	seedRecent(&m, "radio", core.Item{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"})
	msg := m.loadView()()
	list, ok := msg.(listMsg)
	if !ok {
		t.Fatalf("unexpected message %#v", msg)
	}
	if len(list.items) != 1 || list.items[0].Title != "Song One" {
		t.Fatalf("recent items = %#v", list.items)
	}
}

func TestSearchResultsShowKindGlyphs(t *testing.T) {
	m, _, _ := newModel(t)
	m.title = "Search: rock"
	m.items = []core.Item{{Kind: "song", ID: "1", Title: "Song"}, {Kind: "playlist", ID: "p1", Title: "List"}}
	lines := plainText(strings.Join(m.listLines(60, 10), "\n"))
	if !strings.Contains(lines, "♪ Song") || !strings.Contains(lines, "≡ List") {
		t.Fatalf("kind glyphs missing:\n%s", lines)
	}
}

func TestTextTasksUseCentralInputOverlay(t *testing.T) {
	for _, test := range []struct {
		name, source, mode, title, hint string
		key                             tea.KeyPressMsg
	}{
		{"radio URL", "radio", "url", "Add Radio URL", "Enter add & play", runeKey('a')},
		{"Apple search", "apple-music", "search", "Search", "Source: Apple Music · Enter search", runeKey('/')},
		{"Apple filter", "apple-music", "filter", "Filter Current List", "Enter apply", runeKey('F')},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.width, m.height, m.source = 100, 24, test.source
			next, _ := m.handleKey(test.key)
			m = next.(Model)
			if m.overlay != "input" || m.inputMode != test.mode || !m.input.Focused() {
				t.Fatalf("input state overlay=%q mode=%q focused=%v", m.overlay, m.inputMode, m.input.Focused())
			}
			view := plainText(m.View().Content)
			if !strings.Contains(view, test.title) || !strings.Contains(view, test.hint) {
				t.Fatalf("input overlay missing copy:\n%s", view)
			}
			next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = next.(Model)
			if m.overlay != "" || m.inputMode != "" || m.input.Focused() {
				t.Fatalf("Esc did not cancel: overlay=%q mode=%q focused=%v", m.overlay, m.inputMode, m.input.Focused())
			}
		})
	}
}

func TestPushedPagesDoNotHighlightAnyViewAndShowBackHint(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Recent"
	if got := m.activeTopView(); got != "Recent" {
		t.Fatalf("top-level active view = %q", got)
	}
	m.history = []page{{source: "radio", view: "Recent", title: "Recent"}}
	if got := m.activeTopView(); got != "" {
		t.Fatalf("pushed page active view = %q, want none", got)
	}
	if footer := m.footerLine(120); !strings.Contains(footer, "esc back") {
		t.Fatalf("pushed page missing back hint: %q", footer)
	}
	m.history = nil
	if footer := m.footerLine(120); strings.Contains(footer, "esc back") {
		t.Fatalf("top-level page should not claim esc back: %q", footer)
	}
}

func radioPageItems(start, count int) []core.Item {
	items := make([]core.Item, count)
	for i := range items {
		items[i] = core.Item{Kind: "stream", ID: fmt.Sprintf("station-%d", start+i), URL: fmt.Sprintf("https://radio.example/%d", start+i), Title: fmt.Sprintf("Station %d", start+i)}
	}
	return items
}

func TestThemeTabMovesThroughThemes(t *testing.T) {
	m, _, _ := newModel(t)
	m.overlay, m.themeNames, m.themeIndex = "theme", []string{"one", "two"}, 0
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.themeIndex != 1 {
		t.Fatalf("theme tab = %d, want 1", m.themeIndex)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(Model)
	if m.themeIndex != 0 {
		t.Fatalf("theme shift+tab = %d, want 0", m.themeIndex)
	}
}

func TestSelectionMarkersAndDynamicConfirm(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay = "radio", "discovery"
	m.discoverySelected = discoveryReset
	view := m.overlayView(80, 20)
	if !strings.Contains(view, "› Reset filters") {
		t.Fatalf("selected row marker missing:\n%s", view)
	}
	if !strings.Contains(view, "[ Show all ]") {
		t.Fatalf("empty query should not claim to search:\n%s", view)
	}

	m.discoveryTerm = "city pop"
	m.discoverySelected = discoveryConfirm
	view = m.overlayView(80, 20)
	if !strings.Contains(view, `[ Search "city pop" ]`) || !strings.Contains(view, "›[ Search") {
		t.Fatalf("dynamic confirm label/marker missing:\n%s", view)
	}

	m.discoveryTerm = ""
	m.discoveryPending = radioDiscovery{Language: "Japanese"}
	view = m.overlayView(80, 20)
	if !strings.Contains(view, "[ Apply filters ]") {
		t.Fatalf("filter-only label missing:\n%s", view)
	}
	m.discoveryTerm = "jazz"
	view = m.overlayView(80, 20)
	if !strings.Contains(view, "[ Search + filters ]") {
		t.Fatalf("combined label missing:\n%s", view)
	}

	m.overlay, m.discoverySelected = "discovery-options", 1
	m.discoveryKind, m.discoveryOptions = "language", []core.Item{{Title: "Any"}, {Title: "Japanese"}}
	view = m.overlayView(80, 20)
	if !strings.Contains(view, "› Japanese") {
		t.Fatalf("option marker missing:\n%s", view)
	}

	m.overlay, m.themeNames, m.themeIndex = "theme", []string{"one", "two"}, 1
	view = m.overlayView(80, 20)
	if !strings.Contains(view, "› two") {
		t.Fatalf("theme marker missing:\n%s", view)
	}
}

func TestTabAndFooterMarkersAndCopy(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Recent"
	if line := plainText(m.sourceLine(100)); !strings.Contains(line, "Radio") || !strings.Contains(line, "lilt") || strings.Contains(line, "RECENT") || strings.Contains(line, "Audius") {
		t.Fatalf("identity band is position + brand only: %q", line)
	}
	if line := plainText(m.viewLine(100)); !strings.Contains(line, "1 Home · 2 Browse · › 3 Recent") {
		t.Fatalf("active surface marker missing: %q", line)
	}

	m.state = core.PlaybackState{Status: "playing", Track: &core.Item{Kind: "stream", URL: "https://radio.example/live"}}
	if footer := m.footerLine(140); !strings.Contains(footer, "v stop") {
		t.Fatalf("stop hint missing while playing: %q", footer)
	}

	m.items, m.selected = nil, 0
	next, _ := m.toggleFavorite()
	m = next.(Model)
	if m.message != "Nothing selected" {
		t.Fatalf("f without selection = %q", m.message)
	}

	m.overlay = "help"
	if view := m.overlayView(100, 40); !strings.Contains(view, "(Radio)") {
		t.Fatalf("help should scope a to Radio:\n%s", view)
	}
}

func TestStartupPositioningLine(t *testing.T) {
	m, _, _ := newModel(t)
	if !strings.Contains(m.message, "s switches source") || !strings.Contains(m.message, ": commands") {
		t.Fatalf("startup positioning missing: %q", m.message)
	}
	denied := New(Options{Provider: &fake{}, Player: &fake{}, Radio: fakeRadio{}, Store: &state.Store{}, Authorization: core.AuthorizationStatus{Status: "denied"}})
	if strings.Contains(denied.message, "s switches source") || denied.message != "" {
		t.Fatalf("positioning should defer to the account hint: %q", denied.message)
	}
}

func TestOptionsFailureShowsShortReason(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay, m.discoveryKind = "radio", "discovery-options", "language"
	m.discoveryOptionsErr = "Get \"https://de1.api.radio-browser.info/json/languages\": context deadline exceeded"
	view := m.overlayView(80, 20)
	if !strings.Contains(view, "Check your connection") {
		t.Fatalf("short failure guidance missing:\n%s", view)
	}
	if strings.Contains(view, "de1.api.radio-browser.info") {
		t.Fatalf("raw URL leaked into the overlay:\n%s", view)
	}
}

func TestTextCommitLandsOnConfirm(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay = "radio", "discovery"
	next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Text: "jazz"})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "discovery" || m.discoveryTerm != "jazz" || m.discoverySelected != discoveryConfirm {
		t.Fatalf("commit = overlay=%q term=%q selected=%d", m.overlay, m.discoveryTerm, m.discoverySelected)
	}
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.browseQuery.Term != "jazz" || m.view != "Browse" {
		t.Fatalf("one extra Enter should search: cmd=%v query=%#v view=%q", cmd != nil, m.browseQuery, m.view)
	}
}

func TestSmallOverlayKeepsActionsVisible(t *testing.T) {
	hasDividerRow := func(view string) bool {
		for _, line := range strings.Split(view, "\n") {
			if strings.Count(line, "─") >= 20 && strings.Contains(line, "│") {
				return true
			}
		}
		return false
	}
	m, _, _ := newModel(t)
	m.source, m.overlay, m.discoverySelected = "radio", "discovery", discoveryReset
	view := m.overlayView(60, 12)
	if !strings.Contains(view, "[ Show all ]") || !strings.Contains(view, "[ Cancel ]") {
		t.Fatalf("small menu hid the actions:\n%s", view)
	}
	if hasDividerRow(view) {
		t.Fatalf("small menu should drop dividers:\n%s", view)
	}
	if tall := m.overlayView(80, 24); !hasDividerRow(tall) {
		t.Fatalf("roomy menu should keep dividers:\n%s", tall)
	}

	m.overlay = "help"
	view = m.overlayView(60, 12)
	if !strings.Contains(view, "scroll · Esc/? close") {
		t.Fatalf("truncated help should be scrollable:\n%s", view)
	}
}

func TestHelpWrapsInsteadOfTruncating(t *testing.T) {
	m, _, _ := newModel(t)
	joined := plainText(strings.Join(m.helpLines(46), "\n"))
	flat := strings.Join(strings.Fields(joined), " ")
	if !strings.Contains(flat, "add a stream URL to Favorites and play it (Radio)") {
		t.Fatalf("long help description was not wrapped in full:\n%s", joined)
	}
	for _, heading := range []string{"NAVIGATION", "PLAYBACK", "UP NEXT", "LIBRARY"} {
		if !strings.Contains(joined, heading) {
			t.Fatalf("help group %q missing:\n%s", heading, joined)
		}
	}
	if strings.Contains(joined, "…") {
		t.Fatalf("help should wrap rather than truncate:\n%s", joined)
	}
}

// An open input lives in its overlay dialog only: the shell behind it must not
// echo the field. It used to append the focused input to the header, which read
// as a stray "Search: t" line above the panels (batch 2026-09-20-search-and-queue L2).
func TestOverlayBackgroundDoesNotEchoTheInput(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	before := strings.Count(m.content(), "\n")
	next, _ := m.openTextInput("search", "Search: ", "type a query and press Enter", "")
	focused := next.(Model)
	frame := focused.content()
	if got := strings.Count(frame, "\n"); got != before {
		t.Fatalf("the input added a frame row: %d lines, want %d", got, before)
	}
	plain := plainText(frame)
	if !strings.Contains(plain, "type a query and press Enter") {
		t.Fatalf("the dialog does not render the field:\n%s", plain)
	}
	// The artifact was a shell row that ended right after the cursor character
	// ("Search: t"), i.e. a clipped echo with no placeholder text behind it.
	for _, line := range strings.Split(plain, "\n") {
		if strings.HasSuffix(strings.TrimRight(line, " │"), "Search: t") {
			t.Fatalf("the shell echoes a clipped input line: %q", line)
		}
	}
}

func TestSmallHelpScrolls(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height, m.overlay = 60, 12, "help"
	first := m.overlayView(60, 12)
	if !strings.Contains(first, "scroll · Esc/? close") {
		t.Fatalf("scroll status missing:\n%s", first)
	}
	if maxOffset := m.helpScrollMax(); maxOffset <= 0 {
		t.Fatalf("help should be scrollable at 60x12, max=%d", maxOffset)
	}

	next, _ := m.handleKey(runeKey('j'))
	m = next.(Model)
	if m.helpOffset != 1 {
		t.Fatalf("j did not scroll: offset=%d", m.helpOffset)
	}
	second := m.overlayView(60, 12)
	if second == first {
		t.Fatal("scrolled view is identical")
	}
	if !strings.Contains(second, "2-10/") {
		t.Fatalf("scroll position not reflected:\n%s", second)
	}

	next, _ = m.handleKey(runeKey('G'))
	m = next.(Model)
	if m.helpOffset != m.helpScrollMax() {
		t.Fatalf("G did not jump to the end: %d/%d", m.helpOffset, m.helpScrollMax())
	}
	next, _ = m.handleKey(runeKey('g'))
	m = next.(Model)
	if m.helpOffset != 0 {
		t.Fatalf("g did not jump to the top: %d", m.helpOffset)
	}

	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.overlay != "" || m.helpOffset != 0 {
		t.Fatalf("esc did not close help: overlay=%q offset=%d", m.overlay, m.helpOffset)
	}

	next, _ = m.handleKey(runeKey('?'))
	m = next.(Model)
	if m.overlay != "help" || m.helpOffset != 0 {
		t.Fatal("reopening help should reset the scroll position")
	}
	next, cmd := m.handleKey(runeKey('q'))
	if next.(Model).overlay != "" || cmd != nil {
		t.Fatalf("q should close help: overlay=%q cmd=%v", next.(Model).overlay, cmd != nil)
	}
}

// A playing detail page must still advertise stop/pause: the detail footer used
// to list only play actions, so `v` was invisible exactly where a queue is open
// (batch 2026-09-20-album-recheck N4).
func TestPlayingDetailFooterAdvertisesStop(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 200, 30
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.history = []page{{title: "Albums"}}
	m.loading = false
	if footer := m.footerLine(200); strings.Contains(footer, "v stop") {
		t.Fatalf("idle detail footer advertises stop: %q", footer)
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", ID: "a1"}}
	footer := m.footerLine(200)
	if !strings.Contains(footer, "v stop") || !strings.Contains(footer, "space pause") {
		t.Fatalf("playing detail footer = %q", footer)
	}
	m.state.Status = "paused"
	if footer := m.footerLine(200); !strings.Contains(footer, "space resume") || !strings.Contains(footer, "v stop") {
		t.Fatalf("paused detail footer = %q", footer)
	}
}

func mouseClick(x, y int) tea.MouseMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseWheel(down int, x, y int) tea.MouseMsg {
	button := tea.MouseWheelUp
	if down == 1 {
		button = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

func TestWideLayoutKeepsNowPlayingInFixedDock(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Title: "One"}}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", ID: "s1", Title: "Current Song", Artist: "Artist"}}

	view := plainText(m.View().Content)
	if !strings.Contains(view, "┌── PLAYLISTS") || !strings.Contains(view, "┌── NOW PLAYING") {
		t.Fatalf("wide fixed dock missing:\n%s", view)
	}
	if strings.Count(view, "NOW PLAYING") != 1 {
		t.Fatalf("wide dock duplicated Now Playing:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != m.width {
			t.Fatalf("wide fixed dock line width = %d, want %d: %q", got, m.width, line)
		}
	}
}

func TestFooterSitsAboveTheTerminalEdge(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	lines := strings.Split(plainText(m.View().Content), "\n")
	if len(lines) != m.height {
		t.Fatalf("line count = %d, want %d", len(lines), m.height)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Fatalf("footer should have a lower spacer: %q", lines[len(lines)-1])
	}
	if !strings.Contains(lines[len(lines)-2], "q quit") {
		t.Fatalf("footer should be centred above lower spacer: %q", lines[len(lines)-2])
	}
}

func TestToastDoesNotMovePlaybackDock(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	before := m.layout()
	m.message = "Playing selection…"
	after := m.layout()
	if before.listTop != after.listTop || before.listHeight != after.listHeight || before.nowTop != after.nowTop || before.nowHeight != after.nowHeight {
		t.Fatalf("toast moved bands: before=%+v after=%+v", before, after)
	}
	view := plainText(m.View().Content)
	if !strings.Contains(view, "Playing selection…") {
		t.Fatalf("toast missing from reserved status row:\n%s", view)
	}
}

func TestWidePlaybackDockUsesHumanSummaryAndQueueRail(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", Position: 43, Duration: 223, QueueIndex: 1,
		Track: &core.Item{Kind: "song", ID: "22", Title: "Your Love", Artist: "The Outfield"},
		Queue: []core.Item{{ID: "22", Title: "Your Love", Artist: "The Outfield"}, {ID: "23", Title: "One Step Closer", Artist: "Linkin Park"}},
	}
	view := plainText(m.View().Content)
	for _, want := range []string{"┌── NOW PLAYING", "Your Love", "Playing", "┌── UP NEXT (2/2)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("playback dock missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "State:") || strings.Contains(view, "From:") {
		t.Fatalf("playback dock retained diagnostic copy:\n%s", view)
	}
}

func TestNowPlayingHidesUnknownFormatButKeepsOffers(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", Format: "System-selected",
		Track:     &core.Item{Kind: "song", Title: "Arsenal", Artist: "Slipknot"},
		Available: []string{"ALAC Hi-Res Lossless · up to 24/192", "AAC 256 kbps"},
	}
	view := plainText(m.View().Content)
	// The server's "System-selected" placeholder is unknown, not a format; the
	// source is already in the breadcrumb, so neither is repeated in the dock.
	if strings.Contains(view, "System-selected") || strings.Contains(view, "Playing · Apple Music") {
		t.Fatalf("unknown format/source should be hidden:\n%s", view)
	}
	m.state.Format = "AAC 256 kbps"
	view = plainText(m.View().Content)
	if !strings.Contains(view, "Playing") || !strings.Contains(view, "AAC 256 kbps") {
		t.Fatalf("known format should be shown:\n%s", view)
	}
	if strings.Contains(view, "Track offers:") || strings.Contains(view, "Available:") {
		t.Fatalf("playback dock retained catalog variants:\n%s", view)
	}
	m.overlay = "info"
	info := plainText(m.View().Content)
	if !strings.Contains(info, "Offer") || !strings.Contains(info, "ALAC Hi-Res Lossless") || !strings.Contains(info, "AAC 256 kbps") {
		t.Fatalf("track info omitted catalog variants:\n%s", info)
	}
}

func TestNowPlayingNeverRepeatsTheSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.state = core.PlaybackState{Source: "apple-music", Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", Title: "Apple Song"}}
	if got := m.nowTitle(); got != "Now Playing" {
		t.Fatalf("title = %q, want fixed identity", got)
	}
	if body := strings.Join(m.nowBody(80), "\n"); strings.Contains(body, "apple") || strings.Contains(body, "Apple Music") {
		t.Fatalf("dock repeats the source:\n%s", body)
	}
	m.source = "apple-music"
	m.state = core.PlaybackState{Source: "audius", Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", Title: "Audius Song"}}
	if body := strings.Join(m.nowBody(80), "\n"); strings.Contains(body, "Audius —") || strings.Contains(body, "Source:") {
		t.Fatalf("dock repeats the source:\n%s", body)
	}
	m.source = "apple-music"
	m.state = core.PlaybackState{Source: "radio", Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", Title: "Radio"}}
	if body := strings.Join(m.nowBody(80), "\n"); !strings.Contains(body, "LIVE") {
		t.Fatalf("live facts missing LIVE badge:\n%s", body)
	}
}

func TestLiveDockDoesNotRepeatLiveInBody(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.state = core.PlaybackState{Source: "radio", Status: "playing", Mode: "stream", IsLive: true, Format: "live stream", Track: &core.Item{Kind: "stream", Title: "Lofi"}}
	if title := m.nowTitle(); title != "Now Playing" {
		t.Fatalf("live title = %q, want fixed identity", title)
	}
	lines := strings.Join(m.nowBody(80), "\n")
	if !strings.Contains(lines, "LIVE") || strings.Contains(lines, "Radio stream") || strings.Contains(lines, "live stream") {
		t.Fatalf("live body is repetitive:\n%s", lines)
	}
}

func TestLivePlaybackErrorExplainsFailure(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{
		Status: "error", Mode: "stream", IsLive: true,
		Track: &core.Item{Kind: "stream", Title: "Lofi"},
		Error: "Stream did not start within 10s — press v to stop, or Enter/p to retry",
	}
	if dock := strings.Join(m.nowBody(100), "\n"); !strings.Contains(dock, "Stream did not start within 10s") {
		t.Fatalf("live dock hides playback error:\n%s", dock)
	}
	if info := strings.Join(m.infoLines(200), "\n"); !strings.Contains(info, "Stream did not start within 10s") {
		t.Fatalf("track info hides playback error:\n%s", info)
	}
}

func TestAddStreamURLFavoritesAndPlays(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "radio"
	m.view = "Recent"
	next, _ := m.handleKey(runeKey('a'))
	m = next.(Model)
	if m.inputMode != "url" || !strings.Contains(m.input.Placeholder, "https://") {
		t.Fatalf("url input = %q placeholder=%q", m.inputMode, m.input.Placeholder)
	}
	m.input.SetValue("https://radio.example/live")
	next, cmd := m.submitInput()
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected a play command")
	}
	m = run(m, cmd)
	if f.radioURL != "https://radio.example/live" || !m.state.IsLive {
		t.Fatalf("stream not played: url=%q live=%v", f.radioURL, m.state.IsLive)
	}
	if !strings.Contains(m.message, "Added to Favorites") {
		t.Fatalf("toast = %q", m.message)
	}
	if len(m.activity.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorites = %#v", m.activity.FavoritesFor("radio"))
	}
}

func TestAddStreamURLUsesICYNameWhenAvailable(t *testing.T) {
	m, f, _ := newModel(t)
	m.radio = namingRadio{}
	m.source, m.view = "radio", "Favorites"
	next, _ := m.handleKey(runeKey('a'))
	m = next.(Model)
	m.input.SetValue("https://radio.example/indiepop")
	next, cmd := m.submitInput()
	m = next.(Model)
	m = run(m, cmd)

	if f.radioURL != "https://radio.example/indiepop" {
		t.Fatalf("stream url = %q", f.radioURL)
	}
	favorites := m.activity.FavoritesFor("radio")
	if len(favorites) != 1 || favorites[0].Title != "Indie Pop Rocks" {
		t.Fatalf("favorite should carry the ICY name: %#v", favorites)
	}
	if !strings.Contains(m.message, "Indie Pop Rocks") {
		t.Fatalf("toast should name the station: %q", m.message)
	}
}

func TestAddStreamURLKeepsRawTitleWithoutICYName(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Favorites"
	next, _ := m.handleKey(runeKey('a'))
	m = next.(Model)
	m.input.SetValue("https://radio.example/plain")
	next, cmd := m.submitInput()
	m = next.(Model)
	m = run(m, cmd)
	favorites := m.activity.FavoritesFor("radio")
	if len(favorites) != 1 || favorites[0].Title != "https://radio.example/plain" {
		t.Fatalf("raw url should be kept when no ICY name exists: %#v", favorites)
	}
}

func TestAccountHintShownWhenNotReady(t *testing.T) {
	f := &fake{}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	m := New(Options{
		Provider:      f,
		Player:        f,
		Radio:         fakeRadio{},
		Store:         store,
		Authorization: core.AuthorizationStatus{Status: "denied"},
		Source:        "apple-music",
	})
	m.width, m.height = 120, 30
	view := plainText(m.View().Content)
	if !strings.Contains(view, "access denied") {
		t.Fatalf("account hint missing:\n%s", view)
	}
	if !strings.Contains(m.emptyText(), "access denied") {
		t.Fatalf("empty text = %q", m.emptyText())
	}
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	if radioView := plainText(m.View().Content); strings.Contains(radioView, "Account:") {
		t.Fatalf("radio inherited Apple account warning:\n%s", radioView)
	}
	m.overlay = "info"
	info := plainText(m.View().Content)
	if !strings.Contains(info, "Auth") || !strings.Contains(info, "denied") {
		t.Fatalf("info overlay missing auth:\n%s", info)
	}
}

func TestAccountHintHiddenWhenReady(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	if view := plainText(m.View().Content); strings.Contains(view, "Account:") {
		t.Fatalf("unexpected account hint:\n%s", view)
	}
}

func TestDefaultThemeIsGruvbox(t *testing.T) {
	m, _, store := newModel(t)
	if m.themeName != "gruvbox" || store.Theme != "gruvbox" {
		t.Fatalf("default theme = %q (store %q), want gruvbox", m.themeName, store.Theme)
	}
}

func drainAll(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			m = drainAll(m, sub)
		}
		return m
	}
	if _, isTick := msg.(tickMsg); isTick {
		return m
	}
	next, follow := m.Update(msg)
	return drainAll(next.(Model), follow)
}

func TestSourceSwitcherResetsNavigationState(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 5; i++ {
		seedFavorite(&m, "radio", core.Item{Kind: "stream", URL: fmt.Sprintf("https://radio.example/%d", i), Title: fmt.Sprintf("S%d", i)})
	}
	m.history = []page{{source: "apple-music", view: "Home"}}
	m.filter = "old"
	m.cache["apple-music/Home"] = []core.Item{{Title: "stale"}}
	next, _ := m.handleKey(runeKey('s'))
	m = next.(Model)
	m.overlaySelected = sourceIndex("radio")
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "radio" || m.view != "Home" || len(m.history) != 0 || m.filter != "" || len(m.cache) != 0 {
		t.Fatalf("source cleanup failed: source=%q view=%q history=%d filter=%q cache=%#v", m.source, m.view, len(m.history), m.filter, m.cache)
	}
}

func TestSpacePausesWhileBuffering(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	station := core.Item{Kind: "stream", URL: "https://radio.example/slow", Title: "Slow FM"}
	f.state = core.PlaybackState{Status: "buffering", Mode: "stream", IsLive: true, Track: &station}
	m.state = f.state

	next, cmd := m.handleKey(runeKey(' '))
	m = next.(Model)
	m = run(m, cmd)
	if f.state.Status != "paused" {
		t.Fatalf("space during buffering should pause, got %q", f.state.Status)
	}
}

func TestAppleMusicViewsStartWithHome(t *testing.T) {
	if got := strings.Join(amViews, ","); got != "Home,Recent" {
		t.Fatalf("views = %q", got)
	}
	m, _, _ := newModel(t)
	if m.view != "Home" || m.title != "Home" {
		t.Fatalf("default = %q / %q", m.view, m.title)
	}
}

func TestAppleMusicFavoritesAreAHomeSection(t *testing.T) {
	m, f, _ := newModel(t)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song One", Artist: "Artist", URL: "https://music.apple.com/song/s1"}
	playlist := core.Item{Kind: "playlist", ID: "p1", Title: "Road Trip"}
	seedFavorite(&m, "apple-music", song)
	seedFavorite(&m, "apple-music", playlist)

	m.items = homeItems("apple-music", core.PlaybackState{}, "", nil, nil, nil, m.activity.FavoritesFor("apple-music"))
	m.selected = firstSelectableIndex(m.items)
	for m.items[m.selected].Title != song.Title {
		m.selected++
	}

	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "song" || f.played.ID != "s1" {
		t.Fatalf("favorite song did not play: %#v", f.played)
	}

	for m.items[m.selected].Title != playlist.Title {
		m.selected++
	}
	next, cmd = m.activate()
	m = next.(Model)
	if m.detailKind != "playlist" || m.detailID != "p1" {
		t.Fatalf("favorite playlist did not open: kind=%q id=%q", m.detailKind, m.detailID)
	}
	_ = cmd
}

func TestAppleMusicUnfavoriteUpdatesLocalState(t *testing.T) {
	m, _, _ := newModel(t)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song One"}
	seedFavorite(&m, "apple-music", song)
	m.source, m.view = "apple-music", "Home"
	m.items = m.activity.FavoritesFor("apple-music")
	m.selected = 0

	next, cmd := m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("apple-music")) != 0 {
		t.Fatalf("unfavorite failed: %#v", m.activity.FavoritesFor("apple-music"))
	}
}

func TestHomeSectionsOmitEmptyAndContinueOpensQueue(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}, Track: &core.Item{Title: "B"}}
	recent := []core.Item{{Kind: "song", ID: "s1", Title: "Song One"}}
	items := homeItems("apple-music", m.state, "Mix", recent, nil, nil, nil)
	if !hasHeader(items, "Continue Playing") || !hasHeader(items, "Recently Played") || !hasHeader(items, "Go to") || items[1].Kind != "continue" {
		t.Fatalf("home items = %#v", items)
	}
	m.items, m.selected = items, 1
	next, cmd := m.activate()
	m = next.(Model)
	if cmd != nil || !m.queueFocus || m.queueCursor != 1 || m.selected != 1 {
		t.Fatalf("continue = focus=%v cursor=%d selected=%d", m.queueFocus, m.queueCursor, m.selected)
	}
	if items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil); !hasHeader(items, "Go to") {
		t.Fatalf("empty home missing Go to entries = %#v", items)
	}
}

func TestHomeSectionsAreSummaries(t *testing.T) {
	many := make([]core.Item, 12)
	for i := range many {
		many[i] = core.Item{Kind: "song", ID: fmt.Sprint(i), Title: fmt.Sprintf("Item %d", i)}
	}
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", many, many, many, many)
	counts := map[string]int{}
	section := ""
	for _, item := range items {
		if item.Kind == "header" {
			section = item.Title
			continue
		}
		counts[section]++
	}
	for _, section := range []string{"Recently Played", "Your Playlists", "Favorites"} {
		if counts[section] != 5 {
			t.Fatalf("%s count = %d, want 5", section, counts[section])
		}
	}
	audius := homeItems("audius", core.PlaybackState{Status: "stopped"}, "", many, many, many, many)
	trending := 0
	section = ""
	for _, item := range audius {
		if item.Kind == "header" {
			section = item.Title
		} else if section == "Trending" {
			trending++
		}
	}
	if trending != 5 {
		t.Fatalf("Audius Trending count = %d, want 5", trending)
	}
}

func TestHomeCompositionGatesSectionsBySource(t *testing.T) {
	item := core.Item{Kind: "song", ID: "s1", Title: "Item"}
	for _, test := range []struct {
		source       string
		want, absent []string
	}{
		{"apple-music", []string{"Recently Played", "Your Playlists", "Favorites", "Go to"}, []string{"Trending"}},
		{"audius", []string{"Recently Played", "Trending", "Your Playlists", "Favorites", "Go to"}, nil},
		{"radio", []string{"Recently Played", "Favorites", "Go to"}, []string{"Trending", "Your Playlists"}},
	} {
		t.Run(test.source, func(t *testing.T) {
			trending, playlists := []core.Item{item}, []core.Item{item}
			for _, header := range test.absent {
				if header == "Trending" {
					trending = nil
				}
				if header == "Your Playlists" {
					playlists = nil
				}
			}
			items := homeItems(test.source, core.PlaybackState{Status: "stopped"}, "", []core.Item{item}, trending, playlists, []core.Item{item})
			for _, header := range test.want {
				if !hasHeader(items, header) {
					t.Fatalf("missing %q: %#v", header, items)
				}
			}
			for _, header := range test.absent {
				if hasHeader(items, header) {
					t.Fatalf("unexpected %q: %#v", header, items)
				}
			}
		})
	}
}

func TestEnterOpensPlaylistDetailAndBack(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Title: "My Playlist"}}
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if len(m.history) != 1 || len(m.items) != 1 || m.items[0].Title != "Track One" {
		t.Fatalf("detail not pushed: history=%d items=%#v", len(m.history), m.items)
	}
	m = m.back()
	if len(m.history) != 0 || m.items[0].Title != "My Playlist" {
		t.Fatalf("back failed: items=%#v", m.items)
	}
}

func TestPTogglesPauseOnCurrentItem(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Recent"
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"}
	m.items = []core.Item{station, {Kind: "stream", URL: "https://radio.example/other", Title: "Other FM"}}
	m.selected = 0
	playing := core.PlaybackState{IsLive: true, Status: "playing", Track: &station}
	f.state, m.state = playing, playing

	next, cmd := m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.state.Status != "paused" {
		t.Fatalf("p did not pause the playing item: %q", f.state.Status)
	}

	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.state.Status != "playing" {
		t.Fatalf("p did not resume the paused item: %q", f.state.Status)
	}

	m.selected = 1
	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.radioURL != "https://radio.example/other" {
		t.Fatalf("p should play a different selection: %q", f.radioURL)
	}

	m.state.Status = "playing"
	m.state.Track = &station
	if footer := m.footerLine(120); !strings.Contains(footer, "space pause") {
		t.Fatalf("footer missing pause hint: %q", footer)
	}
}

func TestFavoriteToggle(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.view = "Browse"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	next, cmd := m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorite not stored: %#v", m.activity.FavoritesFor("radio"))
	}

	next, cmd = m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("radio")) != 0 {
		t.Fatalf("favorite not removed: %#v", m.activity.FavoritesFor("radio"))
	}
}

func TestFavoriteOutsideHomeAppearsInHome(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	m.selected = 0

	next, cmd := m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorite not stored: %#v", m.activity.FavoritesFor("radio"))
	}

	m.view = "Home"
	m = drainAll(m, m.loadView())
	if m.loading || !hasHeader(m.items, "Favorites") {
		t.Fatalf("Home omitted favorite: %#v", m.items)
	}
}

func TestHelpOverlay(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey('?'))
	m = next.(Model)
	view := plainText(m.View().Content)
	if m.overlay != "help" || !strings.Contains(view, "── NAVIGATION ──") || !strings.Contains(view, "remove selected track") || !strings.Contains(view, "┌─ Help") || !strings.Contains(view, "reload the current list") {
		t.Fatal(plainText(m.View().Content))
	}
}

func TestOverlayQClosesAndCtrlCQuits(t *testing.T) {
	for _, overlay := range []string{"help", "info"} {
		t.Run(overlay, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.overlay = overlay
			next, cmd := m.handleKey(runeKey('q'))
			if next.(Model).overlay != "" || cmd != nil {
				t.Fatalf("q = overlay %q cmd=%v, want close", next.(Model).overlay, cmd != nil)
			}

			m.overlay = overlay
			next, cmd = m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if next.(Model).overlay != overlay || cmd == nil {
				t.Fatalf("ctrl+c = overlay %q cmd=%v", next.(Model).overlay, cmd != nil)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("ctrl+c did not quit")
			}
		})
	}
}

func TestFocusedInputFooters(t *testing.T) {
	for _, test := range []struct {
		mode, want string
	}{
		{"search", "typing · Enter search · Esc cancel · Ctrl+C quit"},
		{"filter", "typing · Enter apply · Esc cancel · Ctrl+C quit"},
		{"url", "typing · Enter add & play · Esc cancel · Ctrl+C quit"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.inputMode = test.mode
			m.input.Focus()
			if got := m.footerLine(100); !strings.Contains(got, test.want) {
				t.Fatalf("footer = %q, want %q", got, test.want)
			}
		})
	}
}

func TestQueueHLeavesFocusWithoutChangingMainContext(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Title: "One"}, {Title: "Two"}}
	m.selected = 1
	m.history = []page{{title: "parent"}}
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(runeKey('h'))
	m = next.(Model)
	if m.queueFocus || m.selected != 1 || len(m.history) != 1 {
		t.Fatalf("h changed main context: focus=%v selected=%d history=%d", m.queueFocus, m.selected, len(m.history))
	}
}

func TestLocalFilter(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Title: "Alpha"}, {Title: "Beta"}}
	m.filter = "beta"
	items := m.visibleItems()
	if len(items) != 1 || items[0].Title != "Beta" {
		t.Fatalf("filtered = %#v", items)
	}
}

func TestViewRowsFitWithinHeight(t *testing.T) {
	m, _, _ := newModel(t)
	m.input.Blur()
	m.width, m.height = 100, 24
	view := plainText(m.View().Content)
	if !strings.Contains(view, "Apple Music") || !strings.Contains(view, "lilt") || !strings.Contains(view, "1 Home · 2 Recent") {
		t.Fatalf("header rows missing:\n%s", view)
	}
	if strings.HasSuffix(view, "\n") {
		t.Fatal("view must not end with a trailing newline (scrolls the top row off)")
	}
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Fatalf("view has %d lines, want %d", lines, m.height)
	}
}

func TestPanelShownBesideMainView(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.items = []core.Item{{Kind: "playlist", Title: "Main list"}}
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 0, Queue: []core.Item{{Title: "Queued"}}}
	view := plainText(m.View().Content)
	if !strings.Contains(view, "PLAYLISTS (1)") || !strings.Contains(view, "UP NEXT (1/1)") {
		t.Fatalf("side panel missing:\n%s", view)
	}
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Fatalf("view has %d lines, want %d", lines, m.height)
	}
}

func TestPanelHiddenNarrowFallsBackToFullPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 70, 30
	m.title = "Playlists"
	m.items = []core.Item{{Kind: "playlist", Title: "Main list"}}
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "Queued"}}}
	if strings.Contains(plainText(plainText(m.View().Content)), "┌── UP NEXT") {
		t.Fatal("narrow view should not show a side panel before focus")
	}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	focused := plainText(plainText(m.View().Content))
	if !strings.Contains(focused, "┌── UP NEXT") || !strings.Contains(focused, "Queued") {
		t.Fatalf("narrow focused queue missing:\n%s", focused)
	}
}

func TestPanelCursorIndependentOfMainSelection(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.items = []core.Item{{Title: "One"}, {Title: "Two"}}
	m.selected = 1
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 0, Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	if m.selected != 1 || m.queueCursor != 1 {
		t.Fatalf("selected=%d cursor=%d", m.selected, m.queueCursor)
	}
}

func TestPanelEscapeRestoresMainNavigation(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.items = []core.Item{{Title: "One"}, {Title: "Two"}}
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	if m.queueFocus || m.selected != 1 {
		t.Fatalf("focus=%v selected=%d", m.queueFocus, m.selected)
	}
}

func TestViewRowsFitWithinHeightWithPanel(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 24
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "Queued"}}}
	view := plainText(m.View().Content)
	if strings.HasSuffix(view, "\n") || strings.Count(view, "\n")+1 != m.height {
		t.Fatalf("invalid panel view height:\n%s", view)
	}
}

func TestQueueOpensViaZero(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if !m.queueFocus || m.queueCursor != 1 || len(m.history) != 0 {
		t.Fatalf("focus=%v cursor=%d history=%d", m.queueFocus, m.queueCursor, len(m.history))
	}
}

func TestQueueContextSetOnPlaylistAndClearedOnStopOrStream(t *testing.T) {
	m, _, _ := newModel(t)
	m.detailKind, m.detailID, m.title = "playlist", "p1", "Morning"
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist(false) })
	if got := m.queueSource; got != (queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}) {
		t.Fatalf("queue source = %#v", got)
	}
	next, cmd := m.handleKey(runeKey('v'))
	m = next.(Model)
	m = run(m, cmd)
	if m.queueSource != (queueContext{}) {
		t.Fatalf("queue source after stop = %#v", m.queueSource)
	}
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	m.selected = 0
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playSelected() })
	if m.queueSource != (queueContext{}) {
		t.Fatalf("queue source after stream = %#v", m.queueSource)
	}
}

func TestNowBodyIsPlaybackFactsOnly(t *testing.T) {
	m, _, _ := newModel(t)
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Shuffle: true, QueueIndex: 1,
		Track: &core.Item{ID: "2", Title: "B"}, Queue: []core.Item{{ID: "1", Title: "A"}, {ID: "2", Title: "B"}, {ID: "3", Title: "C"}}}
	m.width = 120
	body := strings.Join(m.nowBody(100), "\n")
	if !strings.Contains(body, "Playing") || !strings.Contains(body, "⇄") {
		t.Fatalf("facts row lost state/mode:\n%s", body)
	}
	if strings.Contains(body, "Up Next") || strings.Contains(body, "Queue") {
		t.Fatalf("dock repeats queue info:\n%s", body)
	}
	// Queue position lives in the rail header count, not the dock.
	if got := m.queueCount(); got != "2/3" {
		t.Fatalf("queue count = %q", got)
	}
}

func TestQueueHeaderGrammar(t *testing.T) {
	m, _, _ := newModel(t)
	m.loading = false
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{QueueIndex: 1, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	if got := m.queueTitle(); got != "Up Next" {
		t.Fatalf("title = %q", got)
	}
	if got := m.queueCount(); got != "2/3" {
		t.Fatalf("count = %q", got)
	}
	// The fraction stays stable for long queues; window info is the scrollbar's
	// job, not header text.
	m.state = core.PlaybackState{QueueIndex: 12, Queue: make([]core.Item, 48)}
	if got := m.queueCount(); got != "13/48" {
		t.Fatalf("long queue count = %q", got)
	}
}

func TestQueueJumpAndCurrentEntryIsNoOp(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 1, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('p'))
	m = next.(Model)
	if cmd != nil || f.queueJumps != 0 || f.played.Kind != "" {
		t.Fatalf("current queue play should be a no-op: cmd=%v jumps=%d played=%#v", cmd != nil, f.queueJumps, f.played)
	}
	m.queueCursor = 2
	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.queueJumps != 1 || m.state.QueueIndex != 2 || f.played.Kind != "" {
		t.Fatalf("queue play did not jump: jumps=%d index=%d played=%#v", f.queueJumps, m.state.QueueIndex, f.played)
	}
}

func TestMainListXIsInertAndEnterAndPPlay(t *testing.T) {
	m, f, _ := newModel(t)
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}

	next, cmd := m.handleKey(runeKey('x'))
	m = next.(Model)
	if cmd != nil || m.busy || f.played.Kind != "" {
		t.Fatalf("main-list x must be inert: cmd=%v busy=%v played=%#v", cmd != nil, m.busy, f.played)
	}

	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	m = run(m, cmd)
	if f.played.ID != "s1" {
		t.Fatalf("Enter did not play selected item: %#v", f.played)
	}

	f.played = core.PlaybackRequest{}
	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.played.ID != "s1" {
		t.Fatalf("p did not play selected item: %#v", f.played)
	}
}

func TestFocusedQueueXRemovesAndDIsInert(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)

	next, cmd := m.handleKey(runeKey('d'))
	m = next.(Model)
	if cmd != nil || len(m.state.Queue) != 2 || m.busy {
		t.Fatalf("focused Up Next d must be inert: cmd=%v queue=%#v busy=%v", cmd != nil, m.state.Queue, m.busy)
	}

	next, cmd = m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if got := len(m.state.Queue); got != 1 || m.state.Queue[0].Title != "B" {
		t.Fatalf("focused Up Next x did not remove selected item: %#v", m.state.Queue)
	}
}

func TestQueueZeroTogglesFocusClosed(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, _ = m.handleKey(runeKey('0'))
	m = next.(Model)
	if m.queueFocus || len(m.history) != 0 {
		t.Fatalf("queue focus remained open: focus=%v history=%d", m.queueFocus, len(m.history))
	}
}

func TestPlaylistDetailMarksCurrentTrack(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.detailKind, m.detailID = "apple-music", "playlist", "p1"
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state.Track = &core.Item{ID: "s2", Title: "Two"}
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "One"}, {Kind: "song", ID: "s2", Title: "Two"}}
	if m.isPlayingItem(m.items[0]) || !m.isPlayingItem(m.items[1]) {
		t.Fatalf("current track detection = %v/%v", m.isPlayingItem(m.items[0]), m.isPlayingItem(m.items[1]))
	}
	// The current track is marked by the same ▶ the Up Next rail uses plus the
	// playing token, so playback is readable even without the row background.
	rows := m.listLines(120, 5)
	if !strings.Contains(plainText(rows[2]), "▶ ♪ Two") {
		t.Fatalf("current row is missing the playing marker: %#v", rows)
	}
	if strings.Contains(plainText(rows[1]), "▶") {
		t.Fatalf("idle row gained a playing marker: %#v", rows)
	}
}

func TestListLabelPlainFormHasNoNestedStyles(t *testing.T) {
	styled, plain := listLabel("RTL", true, false, "")
	if plain != "RTL ★" {
		t.Fatalf("radio favorite plain label = %q", plain)
	}
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain label must carry no escape codes: %q", plain)
	}
	if !strings.Contains(styled, "★") {
		t.Fatalf("styled label lost the star: %q", styled)
	}
	styled, plain = listLabel("Song", false, true, "♪ ")
	if plain != "♪ ★ Song" {
		t.Fatalf("apple favorite + glyph plain label = %q", plain)
	}
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain label must carry no escape codes: %q", plain)
	}
	_, plain = listLabel("Plain", false, false, "")
	if plain != "Plain" {
		t.Fatalf("unmarked plain label = %q", plain)
	}
}

// Favoriting the playing station used to nest the star's style inside the row
// highlight, and the star's reset ended the highlight mid-row.
func TestFavoritedPlayingRowKeepsFlatHighlightText(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Recent", "Recent"
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	m.items = []core.Item{station}
	m.state = core.PlaybackState{IsLive: true, Status: "playing", Track: &station}
	m.loading = false
	m.width, m.height = 120, 30

	seedFavorite(&m, "radio", station)
	lines := m.listLines(80, 5)
	if !strings.Contains(lines[0], "Example FM ★ — ") && !strings.Contains(lines[0], "Example FM ★") {
		t.Fatalf("playing favorited row text = %q", lines[0])
	}
	// The highlighted row is one flat style, so its only reset is the one that
	// closes the row itself.
	if got := strings.Count(lines[0], "\x1b[0m"); got > 1 {
		t.Fatalf("row highlight is interrupted by nested styles (%d resets): %q", got, lines[0])
	}
}

func TestPlayingRowStyleOutranksSelection(t *testing.T) {
	// The playing row must look the same whether or not the cursor is on it;
	// the `>` cursor already says which row is selected.
	if got := listRowKind(true, true); got != rowPlaying {
		t.Fatalf("selected+playing row kind = %d, want rowPlaying", got)
	}
	if got := listRowKind(false, true); got != rowPlaying {
		t.Fatalf("unselected playing row kind = %d, want rowPlaying", got)
	}
	if got := listRowKind(true, false); got != rowSelected {
		t.Fatalf("selected idle row kind = %d, want rowSelected", got)
	}
	if got := listRowKind(false, false); got != rowNormal {
		t.Fatalf("plain row kind = %d, want rowNormal", got)
	}
}

func TestPlayingRowKeepsCursorForSelection(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.title = "Browse"
	m.width, m.height = 120, 24
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"}}
	m.loading = false
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/other", Title: "Other"},
		{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"},
	}
	// Selected playing row keeps the `›` cursor, so selection stays readable.
	m.selected = 1
	lines := m.listLines(120, 5)
	if !strings.Contains(plainText(lines[1]), "›  ▶ Live FM") {
		t.Fatalf("selected playing row lost the cursor:\n%s", lines[1])
	}
	// Selected idle row also uses the cursor.
	m.selected = 0
	lines = m.listLines(120, 5)
	if !strings.Contains(plainText(lines[0]), "›  Other") {
		t.Fatalf("selected idle row lost the cursor:\n%s", lines[0])
	}
	if !strings.HasPrefix(plainText(lines[1]), "   ▶ Live FM") {
		t.Fatalf("unselected playing row should have no cursor:\n%s", lines[1])
	}
	// Highlighted rows carry one blank column of padding on each side.
	if !strings.Contains(plainText(lines[1]), " ▶ Live FM ") {
		t.Fatalf("playing row is missing highlight padding:\n%s", lines[1])
	}
}

func titlesOf(items []core.Item) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func TestStaleListResultIsIgnoredAndDoesNotClearLoading(t *testing.T) {
	m, _, _ := newModel(t)
	m.view = "Recent"
	m.generation = 2
	m.loading = true
	next, _ := m.Update(listMsg{generation: 1, destination: "apple-music|Recent|||0", key: "apple-music/Recent", title: "Recent", items: []core.Item{{Title: "P"}}})
	m = next.(Model)
	if len(m.cache["apple-music/Recent"]) != 0 || len(m.items) != 0 || !m.loading {
		t.Fatalf("stale response changed model: cache=%#v items=%#v loading=%v", m.cache, m.items, m.loading)
	}
}

func TestPlaylistDetailPlaysFromTrack(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "playlist", "p1"
	m.items = []core.Item{{Kind: "song", ID: "s0", Title: "Track Zero"}, {Kind: "song", ID: "s1", Title: "Track One"}}
	m.selected = 1
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "playlist" || f.played.StartTrackID != "s1" || f.played.StartAt != 1 || !f.played.FromHere {
		t.Fatalf("playRequest = %#v", f.played)
	}
}

func TestSearchBackRestoresPlaylistDetailContext(t *testing.T) {
	m, f, _ := newModel(t)
	m.view, m.title, m.detailKind, m.detailID = "Playlists", "Road", "playlist", "p1"
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}
	m.selected, m.filter = 0, "Track"
	m.inputMode = "search"
	m.input.SetValue("other")
	next, _ := m.submitInput()
	child := next.(Model)
	if child.detailKind != "" || len(child.history) != 1 {
		t.Fatalf("search child context = %#v", child)
	}
	child = child.back()
	if child.source != "apple-music" || child.view != "Playlists" || child.detailKind != "playlist" || child.detailID != "p1" || child.filter != "Track" || child.selected != 0 {
		t.Fatalf("restored context = source=%q view=%q detail=%q/%q filter=%q selected=%d", child.source, child.view, child.detailKind, child.detailID, child.filter, child.selected)
	}
	child.filter = ""
	child = run(child, child.playPlaylistFrom(child.items[0]))
	if f.played.ID != "p1" || f.played.StartTrackID != "s1" || !f.played.FromHere {
		t.Fatalf("restored playback semantics lost: %#v", f.played)
	}
}

func TestTinyConsoleShowsResizeNoticeInsteadOfClippedFrame(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Track"}}
	// The console needs header, list, dock, gap, status and footer rows. Eight
	// rows used to render a dock truncated mid-border.
	m.width, m.height = 30, 8
	view := plainText(m.View().Content)
	if !strings.Contains(view, "Terminal too small") || strings.Contains(view, "NOW PLAYING") {
		t.Fatalf("small console view =\n%s", view)
	}
	// The resize notice still explains the app.
	m.overlay = "help"
	if help := plainText(m.View().Content); !strings.Contains(strings.ToLower(help), "help") {
		t.Fatalf("help unavailable from the resize notice:\n%s", help)
	}
}

func TestTinyTerminalAndOverlayNeverOverflow(t *testing.T) {
	m, _, _ := newModel(t)
	for _, size := range [][2]int{{1, 1}, {10, 3}, {23, 7}, {24, 8}} {
		m.width, m.height = size[0], size[1]
		m.overlay = "help"
		view := plainText(m.View().Content)
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size=%v lines=%d", size, len(lines))
		}
		for i, line := range lines {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("size=%v line=%d width=%d: %q", size, i, lipgloss.Width(line), line)
			}
		}
	}
}

func TestExternalMetadataSanitizedBeforeRendering(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.Update(listMsg{generation: m.generation, items: []core.Item{{Title: "evil\x1b[2Jtitle", Artist: "a\u009bb"}}, key: m.viewKey(), title: "Home"})
	m = next.(Model)
	// Our own styling carries ANSI by design, so strip it and then assert that
	// nothing from the external metadata survived.
	view := plainText(plainText(m.View().Content))
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\u009b") {
		t.Fatalf("terminal controls rendered: %q", view)
	}
	if !strings.Contains(view, "evil[2Jtitle") {
		t.Fatalf("sanitized payload text was lost: %q", view)
	}
}

func TestPlaylistDetailPlayAllAndShuffle(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "playlist", "p1"
	m.title = "Playlist"
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}
	next, cmd := m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "playlist" || f.played.ID != "p1" || f.played.StartTrackID != "" {
		t.Fatalf("play all request = %#v", f.played)
	}
	next, cmd = m.handleKey(runeKey('S'))
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Shuffle == nil || !*f.played.Shuffle || f.played.Repeat != "all" || f.played.Kind != "playlist" {
		t.Fatalf("shuffle play request = %#v", f.played)
	}
	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Shuffle != nil {
		t.Fatalf("ordered play must not request shuffle: %#v", f.played)
	}
}

func TestReversePlaylistOrderNames(t *testing.T) {
	for _, title := range []string{"喜爱歌曲", "喜愛歌曲", "Favorite Songs", "Favourite Songs", "  FAVORITE SONGS  "} {
		if !reversePlaylistOrder(title) {
			t.Errorf("reversePlaylistOrder(%q) = false, want true", title)
		}
	}
	for _, title := range []string{"Favorites", "My Favorite Songs", "Songs"} {
		if reversePlaylistOrder(title) {
			t.Errorf("reversePlaylistOrder(%q) = true, want false", title)
		}
	}
}

func TestOpenPlaylistReversesFavoriteSongs(t *testing.T) {
	m, f, _ := newModel(t)
	f.tracks = []core.Item{{Kind: "song", Title: "A"}, {Kind: "song", Title: "B"}, {Kind: "song", Title: "C"}}

	msg := m.openPlaylist(core.Item{Kind: "playlist", ID: "p1", Title: "喜爱歌曲"})()
	push, ok := msg.(pushMsg)
	if !ok {
		t.Fatalf("message = %T, want pushMsg", msg)
	}
	if got := []string{push.items[0].Title, push.items[1].Title, push.items[2].Title}; strings.Join(got, ",") != "C,B,A" {
		t.Fatalf("reversed tracks = %v", got)
	}
	if got := []string{f.tracks[0].Title, f.tracks[1].Title, f.tracks[2].Title}; strings.Join(got, ",") != "A,B,C" {
		t.Fatalf("provider tracks mutated = %v", got)
	}

	msg = m.openPlaylist(core.Item{Kind: "playlist", ID: "p1", Title: "Regular playlist"})()
	push = msg.(pushMsg)
	if got := []string{push.items[0].Title, push.items[1].Title, push.items[2].Title}; strings.Join(got, ",") != "A,B,C" {
		t.Fatalf("normal tracks = %v", got)
	}
}

func TestPlayPlaylistSetsReverse(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailID, m.title = "p1", "喜爱歌曲"
	runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist(false) })
	if !f.played.Reverse {
		t.Fatal("Favorite Songs request did not set Reverse")
	}

	m.title = "Regular playlist"
	runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist(false) })
	if f.played.Reverse {
		t.Fatal("regular playlist request set Reverse")
	}
}

func TestFooterUsesPageContext(t *testing.T) {
	m, _, _ := newModel(t)
	if footer := m.footerLine(200); !strings.Contains(footer, "p play") || !strings.Contains(footer, "s source") || !strings.Contains(footer, "1-9 view") || strings.Contains(footer, "Tab source") {
		t.Fatalf("root footer = %q", footer)
	}
	m.detailKind, m.detailID = "playlist", "p1"
	m.title = "Playlist"
	m.history = []page{{title: "Playlists"}}
	m.loading = false
	if footer := m.footerLine(200); !strings.Contains(footer, "p play all") || strings.Contains(footer, "S save") || strings.Contains(footer, "1-9 view") {
		t.Fatalf("playlist footer = %q", footer)
	}
}

func TestQueueHelpAndInfoUseXForRemoval(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "Queued"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if footer := m.footerLine(200); !strings.Contains(footer, "x remove") || strings.Contains(footer, "d remove") {
		t.Fatalf("queue footer = %q", footer)
	}
	info := strings.Join(m.infoLines(200), "\n")
	if !strings.Contains(info, "x remove") || strings.Contains(info, "d remove") {
		t.Fatalf("queue info = %q", info)
	}
}

func TestQueueEditUsesCursor(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}, {Kind: "song", ID: "3", Title: "C"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if !m.queueFocus || m.queueCursor != 1 {
		t.Fatalf("queue focus: focus=%v cursor=%d", m.queueFocus, m.queueCursor)
	}
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if got := []string{m.state.Queue[0].Title, m.state.Queue[1].Title}; strings.Join(got, ",") != "A,B" {
		t.Fatalf("removed wrong queue entry: %#v", m.state.Queue)
	}
}

func TestRedrawTickNeverPollsState(t *testing.T) {
	m, f, _ := newModel(t)
	next, cmd := m.Update(tickMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("tick must retain the redraw schedule")
	}
	if f.stateCalls != 0 {
		t.Fatalf("redraw tick made %d State RPCs", f.stateCalls)
	}
}

func TestNotificationsAreStrictlyOrderedAndProtectNewerState(t *testing.T) {
	m, _, _ := newModel(t)
	m.sequence = 4
	m.state = core.PlaybackState{Status: "paused", Position: 4}
	next, _ := m.Update(watchMsg{update: api.WatchUpdate{Kind: "playback.changed", Sequence: 6, Playback: &api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "playing", Position: 6}}}})
	m = next.(Model)
	if m.sequence != 6 || m.state.Position != 6 {
		t.Fatalf("new notification not applied: sequence=%d state=%#v", m.sequence, m.state)
	}
	next, _ = m.Update(watchMsg{update: api.WatchUpdate{Kind: "playback.changed", Sequence: 5, Playback: &api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "paused", Position: 5}}}})
	m = next.(Model)
	if m.sequence != 6 || m.state.Position != 6 {
		t.Fatal("older notification overwrote canonical state")
	}
	m.busy, m.operationID = true, 1
	next, _ = m.Update(actionMsg{actionID: 1, afterSequence: 4, state: core.PlaybackState{Status: "paused", Position: 4}})
	m = next.(Model)
	if m.state.Position != 6 {
		t.Fatal("delayed command response overwrote a newer notification")
	}
	m.busy, m.operationID = true, 2
	next, _ = m.Update(actionMsg{actionID: 2, afterSequence: 6, state: core.PlaybackState{Status: "paused", Position: 7}})
	m = next.(Model)
	if m.state.Position != 7 {
		t.Fatal("current command response did not update immediately")
	}
}

func TestNewerActionRejectsStaleActionMetadata(t *testing.T) {
	m, _, _ := newModel(t)
	oldItem := core.Item{Kind: "song", ID: "old", Title: "Old"}
	newItem := core.Item{Kind: "song", ID: "new", Title: "New"}
	m.actionClock, m.operationID, m.busy = 12, 12, true
	next, _ := m.Update(actionMsg{actionID: 12, afterSequence: m.sequence, state: core.PlaybackState{Status: "playing", Mode: "full"}, queueContext: &queueContext{Kind: "playlist", ID: "new"}, recentSource: "apple-music", recentItem: &newItem})
	m = next.(Model)
	next, _ = m.Update(actionMsg{actionID: 11, afterSequence: m.sequence, state: core.PlaybackState{Status: "paused"}, queueContext: &queueContext{Kind: "playlist", ID: "old"}, recentSource: "apple-music", recentItem: &oldItem})
	m = next.(Model)
	if m.queueSource.ID != "new" || m.state.Status != "playing" {
		t.Fatalf("stale action changed metadata/state: queue=%#v state=%#v", m.queueSource, m.state)
	}
}

func TestNewerNotificationSkipsStaleStateButKeepsCompletedMetadata(t *testing.T) {
	m, _, _ := newModel(t)
	m.sequence = 9
	m.state = core.PlaybackState{Status: "playing", Position: 42, Track: &core.Item{Kind: "song", ID: "current", Title: "Current"}}
	m.queueSource = queueContext{Kind: "playlist", ID: "current"}
	item := core.Item{Kind: "stream", URL: "https://radio.example/late", Title: "Late"}
	m.busy, m.operationID = true, 1
	next, cmd := m.Update(actionMsg{actionID: 1, afterSequence: 8, state: core.PlaybackState{Status: "paused"}, queueContext: &queueContext{Kind: "playlist", ID: "stale"}, recentSource: "radio", recentItem: &item, addFavorite: true, refreshView: false})
	m = next.(Model)
	if m.queueSource.ID != "current" || m.state.Status != "playing" || m.state.Position != 42 {
		t.Fatalf("sequence-stale state applied: queue=%#v state=%#v", m.queueSource, m.state)
	}
	if len(m.activity.RecentFor("radio")) != 0 {
		t.Fatalf("client recorded server-owned recent: %#v", m.activity.RecentFor("radio"))
	}
	m = run(m, cmd)
	if len(m.activity.FavoritesFor("radio")) != 1 {
		t.Fatalf("completed auto-favorite dropped: %#v", m.activity.FavoritesFor("radio"))
	}
}

func TestFilteredPlaylistSelectionUsesOriginalIndex(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID, m.title = "playlist", "p1", "Road"
	m.items = []core.Item{{Kind: "song", ID: "a", Title: "Alpha"}, {Kind: "song", ID: "b", Title: "Beta"}, {Kind: "song", ID: "c", Title: "Gamma"}}
	m.filter = "gamma"
	m.selected = 0
	item := m.visibleItems()[0]
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylistFrom(item) })
	if f.played.StartAt != 2 || f.played.StartTrackID != "c" || !f.played.FromHere {
		t.Fatalf("play request = %#v, want original index 2 and stable id c", f.played)
	}
}

func TestDisplayPositionInterpolatesAndIsBounded(t *testing.T) {
	m, _, _ := newModel(t)
	at := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	m.state = core.PlaybackState{Status: "playing", Position: 10, Duration: 12}
	m.snapshotAt = at
	if got := m.displayPositionAt(at.Add(time.Second)); got != 11 {
		t.Fatalf("display position = %v, want 11", got)
	}
	if got := m.displayPositionAt(at.Add(10 * time.Second)); got != 12 {
		t.Fatalf("bounded display position = %v, want 12", got)
	}
	if m.state.Position != 10 {
		t.Fatalf("interpolation mutated canonical position: %v", m.state.Position)
	}
	m.state.IsLive = true
	if got := m.displayPositionAt(at.Add(time.Second)); got != 10 {
		t.Fatalf("live display position = %v, want snapshot position", got)
	}
}

func TestLoadingOnPush(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Title: "My Playlist"}}
	next, _ := m.activate()
	m = next.(Model)
	if !m.loading || len(m.items) != 0 || len(m.history) != 1 {
		t.Fatalf("loading=%v items=%d history=%d", m.loading, len(m.items), len(m.history))
	}
}

func TestTabInInputDoesNotSwitchSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Home"
	next, _ := m.handleKey(runeKey('/'))
	m = next.(Model)
	if !m.input.Focused() {
		t.Fatal("input not focused after /")
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if !m.input.Focused() || m.source != "apple-music" {
		t.Fatalf("tab changed input/source: source=%q focused=%v", m.source, m.input.Focused())
	}
}

func TestDigitSelectsView(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Home"
	next, _ := m.handleKey(runeKey('2'))
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("view = %q, want Recent", m.view)
	}
	next, _ = m.handleKey(runeKey('1'))
	m = next.(Model)
	if m.view != "Home" {
		t.Fatalf("view = %q, want Home", m.view)
	}
}

func TestSearchPushesTemporaryList(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Playlists"
	m.title = "Playlists"
	m.inputMode = "search"
	m.input.SetValue("one")
	next, cmd := m.submitInput()
	m = next.(Model)
	if len(m.history) != 1 || !m.loading {
		t.Fatalf("search did not push loading page: history=%d loading=%v", len(m.history), m.loading)
	}
	m = run(m, cmd)
	if m.loading || m.title != "Search: one" || len(m.items) == 0 || m.items[0].Kind != "header" {
		t.Fatalf("search results wrong: title=%q items=%#v", m.title, m.items)
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want first selectable after header", m.selected)
	}
	m = m.back()
	if m.title != "Playlists" || len(m.history) != 0 {
		t.Fatalf("back failed: title=%q history=%d", m.title, len(m.history))
	}
}

// Every built-in palette ships an explicit selection colour; the focused cursor
// must stay visibly distinct from a plain row.
func TestDefaultThemeSelectionIsVisible(t *testing.T) {
	for _, name := range theme.Names() {
		renderer := newRenderer(theme.Load(name))
		selected := renderer.selStyle.Render("row")
		plain := renderer.rowStyle.Render("row")
		if selected == plain {
			t.Fatalf("%s: selected row is not visually distinct", name)
		}
		if !strings.Contains(selected, "48;") && !strings.Contains(selected, ";7;") && !strings.Contains(selected, ";7m") {
			t.Fatalf("%s: selected row has no background or reverse to mark the cursor: %q", name, selected)
		}
	}
}

// The theme canvas is painted by lilt itself, so a palette's bg holds on any
// terminal (docs/ui/theme.md). Lipgloss closes every styled span with a reset,
// so the canvas escape has to be re-asserted after each one.
func TestCanvasPaintsThemedFrames(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "One"}}
	m.state = core.PlaybackState{Status: "playing", Position: 1, Duration: 10, Track: &core.Item{ID: "s1", Title: "One"}}
	m.renderer = newRenderer(theme.Load("gruvbox"))
	canvas := backgroundSGR("#282828")
	if canvas == "" {
		t.Fatal("gruvbox canvas escape is empty")
	}
	lines := strings.Split(m.content(), "\n")
	if len(lines) != m.height {
		t.Fatalf("frame rows = %d, want %d", len(lines), m.height)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, canvas) {
			t.Fatalf("row %d is not painted with the canvas: %q", i, line)
		}
		resets := strings.Count(line, "\x1b[m") + strings.Count(line, "\x1b[0m")
		if got := strings.Count(line, canvas); got != resets {
			t.Fatalf("row %d: canvas escapes = %d, resets = %d: %q", i, got, resets, line)
		}
	}
}

// A custom palette without a bg owns no canvas: lilt must leave the terminal's
// own background alone instead of imposing a colour. Every built-in palette
// has a bg, so this needs a user theme file.
func TestCustomThemeWithoutBGLeavesTheTerminalBackgroundAlone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LILT_CONFIG", root)
	if err := os.MkdirAll(filepath.Join(root, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	content := "bright_fg = \"#ffffff\"\nfg = \"#888888\"\naccent = \"#00ff00\"\ngreen = \"#00ff00\"\nyellow = \"#ffff00\"\nred = \"#ff0000\"\n"
	if err := os.WriteFile(filepath.Join(root, "themes", "bare.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m = m.setTheme("bare")
	if got := m.content(); strings.Contains(got, "\x1b[48") {
		t.Fatalf("bg-less theme painted a background: %q", got)
	}
}

// Bubbles' text input defaults to the terminal's own colours, which vanish once
// lilt paints a canvas: on the light print-room theme the search query was
// invisible. The input has to be themed, and re-themed on every theme change.
func TestSearchOverlayRendersQueryInThemeColor(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m = m.setTheme("print-room")
	m.overlay, m.inputMode = "input", "search"
	m.input.Focus()
	m.input.SetValue("李玖哲")

	view := m.overlayView(m.width, m.height)
	if !strings.Contains(view, m.renderer.rowStyle.Render("李玖哲")) {
		t.Fatalf("search query is not painted with the theme text colour:\n%s", view)
	}
	if strings.Contains(view, "\x1b[m李玖哲") || strings.Contains(view, " \x1b[mSearch") {
		t.Fatalf("query text falls back to the terminal colours:\n%s", view)
	}

	// Switching themes must move the input with the rest of the shell.
	before := m.input.Styles().Focused.Text.Render("q")
	m = m.setTheme("gruvbox")
	if after := m.input.Styles().Focused.Text.Render("q"); after == before || after == "q" {
		t.Fatalf("input text style did not follow the theme: %q -> %q", before, after)
	}
}

// The prompt and placeholder are part of the same input: they need theme tokens
// too, not bubbles' ANSI 7/240 defaults.
func TestSearchInputPromptAndPlaceholderUseThemeTokens(t *testing.T) {
	m, _, _ := newModel(t)
	m = m.setTheme("print-room")
	styles := m.input.Styles()
	if got, want := styles.Focused.Prompt.Render("Search: "), m.renderer.accentStyle.Render("Search: "); got != want {
		t.Fatalf("prompt = %q, want the accent token %q", got, want)
	}
	if got, want := styles.Focused.Placeholder.Render("hint"), m.renderer.dimStyle.Render("hint"); got != want {
		t.Fatalf("placeholder = %q, want the muted token %q", got, want)
	}
}

// The playing marker (▶) must survive the cursor landing on the same row, so the
// current track never loses its "this is playing" indicator.
func TestQueuePlayingMarkerPersistsWhenCursorSelectsIt(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", QueueIndex: 1,
		Queue: []core.Item{{Kind: "song", ID: "a", Title: "A"}, {Kind: "song", ID: "b", Title: "B"}, {Kind: "song", ID: "c", Title: "C"}},
	}
	m.queueFocus = true
	m.queueCursor = 1
	lines := m.queueLines(30, 3)
	if len(lines) < 2 {
		t.Fatalf("queue lines = %d", len(lines))
	}
	if !strings.Contains(plainText(lines[1]), "▶") {
		t.Fatalf("playing marker lost when selected: %q", plainText(lines[1]))
	}
	if !strings.Contains(plainText(lines[1]), "›") {
		t.Fatalf("cursor marker missing on selected row: %q", plainText(lines[1]))
	}
	// The playing colour survives the cursor: the current entry keeps the
	// playing token instead of collapsing into the plain selection style.
	// gruvbox green fg over its selection bg (newModel resolves the default
	// theme to gruvbox).
	if !strings.Contains(lines[1], "\x1b[1;38;2;184;187;38;48;2;60;56;54m") {
		t.Fatalf("playing highlight lost when selected: %q", lines[1])
	}
}

// A queue jump is asynchronous; a repeated click or Enter before it completes
// must not fire a second jump.
func TestQueueJumpNotRepeatedWhileBusy(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 120, 30
	f.state = core.PlaybackState{
		Status: "playing", Mode: "full", QueueIndex: 0,
		Queue: []core.Item{{Kind: "song", ID: "a", Title: "A"}, {Kind: "song", ID: "b", Title: "B"}, {Kind: "song", ID: "c", Title: "C"}},
	}
	m.state = f.state
	m.queueFocus = true
	m.queueCursor = 2
	m.busy = true
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || f.queueJumps != 0 {
		t.Fatalf("busy queue issued another jump: cmd=%v jumps=%d", cmd != nil, f.queueJumps)
	}
}

// A live stream that announces ICY metadata shows the current song in the dock.
func TestLiveStreamShowsICYTitle(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source = "radio"
	m.state = core.PlaybackState{
		Status: "playing", IsLive: true, Mode: "stream",
		Track:        &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"},
		StreamTitle:  "Around the World",
		StreamArtist: "Daft Punk",
	}
	view := plainText(m.content())
	if !strings.Contains(view, "Around the World") || !strings.Contains(view, "Daft Punk") {
		t.Fatalf("ICY metadata missing from dock:\n%s", view)
	}
}

func hasHeader(items []core.Item, title string) bool {
	for _, item := range items {
		if item.Kind == "header" && item.Title == title {
			return true
		}
	}
	return false
}

func TestEnterOnSongPlaysListFromHere(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "audius"
	m.items = []core.Item{
		{Kind: "header", Title: "Trending Songs"},
		{Kind: "song", ID: "s1", Ref: "audius:song:1", Title: "One"},
		{Kind: "song", ID: "s2", Ref: "audius:song:2", Title: "Two"},
		{Kind: "song", ID: "s3", Ref: "audius:song:3", Title: "Three"},
		{Kind: "header", Title: "Trending Playlists"},
		{Kind: "playlist", ID: "p1", Ref: "audius:playlist:p1", Title: "Mix"},
	}
	m.selected = 1
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	want := []string{"audius:song:1", "audius:song:2", "audius:song:3"}
	if len(f.playSongSet) != len(want) {
		t.Fatalf("play set = %#v, want %#v", f.playSongSet, want)
	}
	for i := range want {
		if f.playSongSet[i] != want[i] {
			t.Fatalf("play set = %#v, want %#v", f.playSongSet, want)
		}
	}
}

// A search result page is a query's evidence, not a container the user
// assembled, so Enter plays only the pointed song (docs/ui/model.md §6). The
// surfaces keep the "play from here" run, covered by
// TestEnterOnSongPlaysListFromHere.
func TestSearchResultEnterPlaysOnlyThatSong(t *testing.T) {
	m, f, _ := newModel(t)
	next, _ := m.openTextInput("search", "Search: ", "query", "")
	m = next.(Model)
	m.input.SetValue("blinding")
	next, cmd := m.submitInput()
	m = run(next.(Model), cmd)
	if m.pageClass != pageClassAggregate {
		t.Fatalf("search page class = %q", m.pageClass)
	}
	firstSong := -1
	for i, item := range m.items {
		if item.Kind == "song" {
			firstSong = i
			break
		}
	}
	if firstSong < 0 {
		t.Fatalf("search page has no song rows: %#v", m.items)
	}
	m.selected = firstSong
	next, cmd = m.activate()
	_ = run(next.(Model), cmd)
	if len(f.playSongSet) != 0 {
		t.Fatalf("aggregate Enter queued a section: %#v", f.playSongSet)
	}
	if f.played.Kind != "song" || f.played.FromHere || f.played.Ref == "" {
		t.Fatalf("single play request = %#v", f.played)
	}
}

func TestSearchResultFooterDoesNotPromisePlayFromHere(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 200, 30
	m.pageClass = pageClassAggregate
	m.history = []page{{title: "Home"}}
	m.title = "Search: blinding"
	m.items = []core.Item{
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"},
		{Kind: "song", ID: "s2", Ref: "apple-music:song:s2", Title: "Two"},
	}
	if footer := m.footerLine(200); strings.Contains(footer, "play from here") {
		t.Fatalf("aggregate footer promises a section run: %q", footer)
	}
}

func TestAggregatePageClassSurvivesBack(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.pushAggregate("Search: one", nil)
	m = next.(Model)
	if m.pageClass != pageClassAggregate {
		t.Fatalf("pushed page class = %q", m.pageClass)
	}
	m.items = []core.Item{{Kind: "album", ID: "al1", Ref: "apple-music:album:al1", Title: "Album"}}
	m.selected = 0
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	if m.pageClass != pageClassContainer {
		t.Fatalf("container page class = %q", m.pageClass)
	}
	m = m.back()
	if m.pageClass != pageClassAggregate || m.title != "Search: one" {
		t.Fatalf("back restored class=%q title=%q", m.pageClass, m.title)
	}
}

func TestSourceSwitchClearsAggregatePageClass(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.pushAggregate("Search: one", nil)
	m = next.(Model)
	next, _ = m.switchSource("audius")
	m = next.(Model)
	if m.pageClass != "" {
		t.Fatalf("source switch kept page class %q", m.pageClass)
	}
}

func TestEnterOnLoneSongUsesSinglePlay(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "audius"
	m.items = []core.Item{
		{Kind: "header", Title: "Trending Songs"},
		{Kind: "song", ID: "s1", Ref: "audius:song:1", Title: "One"},
	}
	m.selected = 1
	next, cmd := m.activate()
	_ = run(next.(Model), cmd)
	if len(f.playSongSet) != 0 {
		t.Fatalf("lone song used list play: %#v", f.playSongSet)
	}
	if f.played.Ref != "audius:song:1" {
		t.Fatalf("single play ref = %q", f.played.Ref)
	}
}

func TestSourceSwitcherShowsAvailabilityAndCapabilities(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.overlay = "source-switcher"
	view := plainText(m.View().Content)
	for _, want := range []string{"Switch source", "Apple Music", "Audius", "Radio", "ready", "full", "queue", "browse"} {
		if !strings.Contains(view, want) {
			t.Fatalf("switcher missing %q:\n%s", want, view)
		}
	}
}

func TestNowPlayingHidesAccountWarningDuringFullPlayback(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.source = "apple-music"
	m.account = "Account: Apple Music unavailable"
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", Source: "apple-music",
		Track: &core.Item{Kind: "song", Title: "Song"},
	}
	if view := plainText(m.View().Content); strings.Contains(view, "Account:") {
		t.Fatalf("account warning shown during full playback:\n%s", view)
	}
	m.state.Mode = "preview"
	if view := plainText(m.View().Content); !strings.Contains(view, "Account:") {
		t.Fatalf("account warning hidden in preview mode:\n%s", view)
	}
}

func TestFavoriteRejectsContainerRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Kind: "continue", Title: "Continue Playing"}}
	m.selected = 0
	next, _ := m.toggleFavorite()
	m = next.(Model)
	if !m.messageErr || !strings.Contains(m.message, "favorited") {
		t.Fatalf("container favorite toast = %q err=%v", m.message, m.messageErr)
	}
	if len(m.activity.FavoritesFor("apple-music")) != 0 {
		t.Fatalf("container row was favorited: %#v", m.activity.FavoritesFor("apple-music"))
	}
}

func TestSearchOverlayTitleFollowsSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.source, m.overlay, m.inputMode = "audius", "input", "search"
	if view := plainText(m.View().Content); !strings.Contains(view, "Source: Audius") || strings.Contains(view, "Source: Apple Music") {
		t.Fatalf("search overlay title = %q", view)
	}
}

func TestFavoritePersistenceIsAsynchronousAndCommitsOnlyOnSuccess(t *testing.T) {
	m, _, _ := newModel(t)
	remote := &recordingRemote{block: make(chan struct{})}
	m.remote = remote
	m.items = []core.Item{{Kind: "song", ID: "song", Title: "Song"}}
	started := time.Now()
	next, cmd := m.Update(runeKey('f'))
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond || cmd == nil {
		t.Fatalf("favorite Update blocked or returned no command: elapsed=%v cmd=%v", elapsed, cmd != nil)
	}
	m = next.(Model)
	if len(m.activity.FavoritesFor("apple-music")) != 0 || !m.persisting {
		t.Fatalf("favorite committed before RPC result: favorites=%#v persisting=%v", m.activity.FavoritesFor("apple-music"), m.persisting)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-result:
		t.Fatal("blocking remote unexpectedly completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(remote.block)
	next, _ = m.Update(<-result)
	m = next.(Model)
	if len(m.activity.FavoritesFor("apple-music")) != 0 || m.persisting || len(remote.ops) != 1 {
		t.Fatalf("persistence response wrote projection: favorites=%#v persisting=%v ops=%v", m.activity.FavoritesFor("apple-music"), m.persisting, remote.ops)
	}
	next, _ = m.Update(watchMsg{update: api.WatchUpdate{Kind: "state.changed", Sequence: m.sequence + 1, State: &api.AppState{Revision: m.appRevision + 1, Favorites: []api.Item{{Source: api.SourceAppleMusic, Kind: "song", ID: "song", ProviderID: "song", Title: "Song"}}}}})
	m = next.(Model)
	if len(m.activity.FavoritesFor("apple-music")) != 1 {
		t.Fatalf("authoritative watch did not commit favorite: %#v", m.activity.FavoritesFor("apple-music"))
	}
}

func TestFavoritePersistenceErrorRollsBack(t *testing.T) {
	m, _, _ := newModel(t)
	m.remote = &recordingRemote{err: errors.New("disk full")}
	m.items = []core.Item{{Kind: "song", ID: "song", Title: "Song"}}
	next, cmd := m.Update(runeKey('f'))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(m.activity.FavoritesFor("apple-music")) != 0 || !m.messageErr || !strings.Contains(m.message, "State save failed") {
		t.Fatalf("failed favorite was not rolled back: favorites=%#v message=%q", m.activity.FavoritesFor("apple-music"), m.message)
	}
}

func TestSourceSwitchSerializesStopBeforePersistence(t *testing.T) {
	m, player, _ := newModel(t)
	remote := &recordingRemote{}
	m.remote = remote
	m.state = core.PlaybackState{Status: "playing", Source: "apple-music"}
	m.overlay = "source-switcher"
	next, stopCmd := m.beginSourceSwitch("radio")
	m = next.(Model)
	if !m.busy || stopCmd == nil || player.stops != 0 {
		t.Fatalf("source switch did not enter serialized in-flight state: busy=%v stops=%d", m.busy, player.stops)
	}
	// A second source mutation is rejected before it can issue another stop.
	next, duplicate := m.beginSourceSwitch("audius")
	m = next.(Model)
	if duplicate == nil || player.stops != 0 || len(remote.ops) != 0 || m.source != "apple-music" || !strings.Contains(m.message, "still running") {
		t.Fatalf("duplicate source action was not rejected: stops=%d ops=%v source=%q message=%q", player.stops, remote.ops, m.source, m.message)
	}
	next, persistCmd := m.Update(stopCmd())
	m = next.(Model)
	if player.stops != 1 || m.source != "radio" || persistCmd == nil {
		t.Fatalf("stop did not precede local transition: stops=%d source=%q", player.stops, m.source)
	}
	next, _ = m.Update(persistCmd())
	m = next.(Model)
	if got := strings.Join(remote.ops, ","); got != "ui.set:lastSource" || m.busy || m.overlay != "" {
		t.Fatalf("source mutation order/state = ops=%q busy=%v overlay=%q", got, m.busy, m.overlay)
	}
}

func TestWatchEventsApplyInSequenceAndRearm(t *testing.T) {
	m, _, store := newModel(t)
	updates := make(chan api.WatchUpdate)
	m.watchUpdates = updates
	cases := []api.WatchUpdate{
		{Kind: "playback.changed", Sequence: 1, Playback: &api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "playing", Source: api.SourceRadio, Track: &api.Item{Source: api.SourceRadio, Kind: "stream", ID: "radio:x", Title: "Live"}}}},
		{Kind: "state.changed", Sequence: 2, State: &api.AppState{Theme: "gruvbox", LastSource: api.SourceRadio}},
		{Kind: "sources.changed", Sequence: 3, Sources: []api.SourceDescriptor{{ID: api.SourceRadio, Available: true, Capabilities: map[string]api.Capability{api.CapPlaybackStream: {Available: true}, api.CapSearchRadio: {Available: true}}}}},
		{Kind: "authorization.changed", Sequence: 4, Authorization: &api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthNotRequired}},
		{Kind: "server.warning", Sequence: 5, WarningCode: "engine", WarningMessage: "restarting"},
	}
	for _, update := range cases {
		next, rearm := m.Update(watchMsg{update: update})
		m = next.(Model)
		if rearm == nil {
			t.Fatalf("%s did not re-arm watch", update.Kind)
		}
	}
	if m.state.Status != "playing" || store.Theme != "gruvbox" || m.sourceAuth.Status != api.AuthNotRequired || !strings.Contains(m.message, "Warning: engine") {
		t.Fatalf("watch projections missing: state=%#v theme=%q auth=%#v warning=%q", m.state, store.Theme, m.sourceAuth, m.message)
	}
	previous := m.state.Status
	next, _ := m.Update(watchMsg{update: api.WatchUpdate{Kind: "playback.changed", Sequence: 5, Playback: &api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "paused"}}}})
	m = next.(Model)
	if m.state.Status != previous {
		t.Fatalf("stale watch sequence mutated model: %q", m.state.Status)
	}
	next, resync := m.Update(watchMsg{update: api.WatchUpdate{Kind: "engine.restarted", Sequence: 6}})
	if resync == nil || next.(Model).sequence != 6 {
		t.Fatalf("restart did not request resync")
	}
}

func TestDescriptorArrivalRegatesPendingHome(t *testing.T) {
	m, _, _ := newModel(t)
	m.descriptors = nil
	m.loading = true
	message := homeMsg{generation: m.generation, destination: m.destination(), items: []core.Item{{Kind: "header", Title: "Trending"}, {Kind: "song", ID: "trend", Title: "Trend"}, {Kind: "header", Title: "Your Playlists"}, {Kind: "playlist", ID: "list", Title: "List"}}}
	next, _ := m.Update(sourcesMsg{descriptors: []api.SourceDescriptor{{ID: api.SourceAppleMusic, Available: true, Capabilities: map[string]api.Capability{}}}})
	m = next.(Model)
	next, _ = m.Update(message)
	m = next.(Model)
	if hasHeader(m.items, "Trending") || hasHeader(m.items, "Your Playlists") {
		t.Fatalf("stale Home capability rows survived descriptor gate: %#v", m.items)
	}
}

func TestViewUsesModelTimeAndThemeWithoutGlobalMutation(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 24
	m.state = core.PlaybackState{Status: "playing", Position: 10, Duration: 120, Track: &core.Item{Title: "Song"}}
	m.snapshotAt = time.Unix(100, 0)
	m.renderTime = time.Unix(105, 0)
	first := m.content()
	second := m.content()
	if first != second || !strings.Contains(plainText(first), "0:15") {
		t.Fatalf("view depends on wall clock: first=%q second=%q", plainText(first), plainText(second))
	}
	beforeThemeChange := m.renderer.accentStyle.Render("theme")
	m.applyAppState(api.AppState{Theme: "tokyo-night"})
	if m.themeName != "tokyo-night" || m.renderer.accentStyle.Render("theme") == beforeThemeChange {
		t.Fatal("state update did not update this model's theme renderer")
	}
	themed := m.content()
	other := New(Options{Store: state.NewMemory(), RadioCache: radio.NewCache(""), Source: "apple-music"})
	other.renderer = newRenderer(theme.Load("gruvbox"))
	if again := m.content(); again != themed {
		t.Fatal("another model's theme changed this model's renderer")
	}
}

func TestShuffleBlockedOutsideAppleMusic(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "audius"
	m.detailKind, m.detailID = "playlist", "p1"
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Track"}}
	next, _ := m.handleKey(runeKey('S'))
	m = next.(Model)
	if !m.messageErr || !strings.Contains(m.message, "shuffle") {
		t.Fatalf("shuffle toast = %q err=%v", m.message, m.messageErr)
	}
	if f.played.Kind != "" {
		t.Fatalf("S played for a non-Apple source: %#v", f.played)
	}
}

func TestNarrowOverlayIsClippedToTerminal(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 60, 20
	m.overlay = "help"
	content := m.content()
	lines := strings.Split(content, "\n")
	if len(lines) > m.height {
		t.Fatalf("overlay height = %d lines, want <= %d", len(lines), m.height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("line %d width = %d, want <= %d", i, got, m.width)
		}
	}
}

func TestSourceAccountSummaryPerSource(t *testing.T) {
	if got := sourceAccountSummary("audius", core.AuthorizationStatus{Status: "authorized", AccountLabel: "guocai"}); got != "Account: guocai" {
		t.Fatalf("audius linked = %q", got)
	}
	if got := sourceAccountSummary("audius", core.AuthorizationStatus{Status: "not_determined"}); !strings.Contains(got, "not linked") {
		t.Fatalf("audius unlinked = %q", got)
	}
	if got := sourceAccountSummary("radio", core.AuthorizationStatus{}); got != "Account: not required" {
		t.Fatalf("radio = %q", got)
	}
}

func TestAuthEntryUsesCurrentSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	m.sourceAuth = core.AuthorizationStatus{Status: "authorized", AccountLabel: "guocai"}
	m.items = []core.Item{{Kind: "entry-account", Title: "Account"}}
	m.selected = 0
	next, _ := m.activate()
	m = next.(Model)
	if !strings.Contains(m.message, "guocai") {
		t.Fatalf("audius auth toast = %q", m.message)
	}
}

func TestAuthorizationMsgIsSourceScoped(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	next, _ := m.Update(authorizationMsg{source: "radio", status: core.AuthorizationStatus{Status: "authorized"}})
	m = next.(Model)
	if m.sourceAuth.Status != "" {
		t.Fatalf("another source's authorization leaked: %+v", m.sourceAuth)
	}
	next, _ = m.Update(authorizationMsg{source: "audius", status: core.AuthorizationStatus{Status: "authorized", AccountLabel: "guocai"}})
	m = next.(Model)
	if m.sourceAuth.Status != "authorized" || m.sourceAuth.AccountLabel != "guocai" {
		t.Fatalf("audius authorization not stored: %+v", m.sourceAuth)
	}
}

func TestAudiusHomeHasAccountEntry(t *testing.T) {
	items := homeItems("audius", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Kind == "entry-account" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audius Home has no Account entry: %#v", items)
	}
}

func TestResultGroupJumpOnPushedPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.history = []page{{source: "apple-music", view: "Home", title: "Home"}}
	m.items = []core.Item{
		{Kind: "header", Title: "Songs"},
		{Kind: "song", ID: "s1", Title: "One"},
		{Kind: "song", ID: "s2", Title: "Two"},
		{Kind: "header", Title: "Playlists"},
		{Kind: "playlist", ID: "p1", Title: "List"},
	}
	m.selected = 1
	next, _ := m.handleKey(runeKey(']'))
	m = next.(Model)
	if m.items[m.selected].Kind != "playlist" {
		t.Fatalf("] did not jump to the next group: selected=%d %#v", m.selected, m.items[m.selected])
	}
	next, _ = m.handleKey(runeKey('['))
	m = next.(Model)
	if m.items[m.selected].ID != "s1" {
		t.Fatalf("[ did not jump back: selected=%d %#v", m.selected, m.items[m.selected])
	}
	if footer := m.footerLine(200); !strings.Contains(footer, "[/] group") {
		t.Fatalf("footer missing group hint: %q", footer)
	}
}

func TestConnectingThenBufferingLabel(t *testing.T) {
	m, _, _ := newModel(t)
	m = m.setState(core.PlaybackState{Status: "buffering", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", Title: "Radio"}})
	if !m.connecting() {
		t.Fatal("a fresh buffering session should read as connecting")
	}
	lines := strings.Join(m.nowBody(80), "\n")
	if !strings.Contains(lines, "Connecting…") || strings.Contains(lines, "Buffering…") {
		t.Fatalf("fresh start label = %q", lines)
	}
	m.playbackStartedAt = time.Now().Add(-2 * time.Second)
	if m.connecting() {
		t.Fatal("an aged buffering session should not read as connecting")
	}
	if lines := strings.Join(m.nowBody(80), "\n"); !strings.Contains(lines, "Buffering…") {
		t.Fatalf("aged start label = %q", lines)
	}
}

func TestTrackChangeResetsConnecting(t *testing.T) {
	m, _, _ := newModel(t)
	m = m.setState(core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{ID: "1", Title: "One"}})
	m.playbackStartedAt = time.Now().Add(-2 * time.Second)
	m = m.setState(core.PlaybackState{Status: "buffering", Mode: "full", Track: &core.Item{ID: "2", Title: "Two"}})
	if !m.connecting() {
		t.Fatal("a new track should reset the connecting window")
	}
}

func TestQueueMarksPlayedHistory(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", QueueIndex: 2,
		Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}},
	}
	lines := m.queueLines(40, 5)
	if !strings.Contains(lines[0], "· ") {
		t.Fatalf("played entry not marked: %q", lines[0])
	}
	if !strings.Contains(lines[2], "▶ ") {
		t.Fatalf("current entry not marked: %q", lines[2])
	}
}

func TestHelpHidesUnsupportedShuffle(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 40
	m.source = "audius"
	if lines := strings.Join(m.helpLines(100), "\n"); strings.Contains(lines, "shuffle (restarts a playlist or album) / repeat") {
		t.Fatalf("Audius help advertises shuffle:\n%s", lines)
	}
	m.source = "apple-music"
	if lines := strings.Join(m.helpLines(100), "\n"); !strings.Contains(lines, "shuffle (restarts a playlist or album) / repeat") {
		t.Fatalf("Apple help omits shuffle:\n%s", lines)
	}
}

func TestHomeLoadsLibraryBeforeCapabilitiesArrive(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.descriptors = nil // sources.list not yet received
	m.cache = map[string][]core.Item{}
	msg, ok := m.loadHome()().(homeMsg)
	if !ok {
		t.Fatal("loadHome did not return homeMsg")
	}
	found := false
	for _, item := range msg.items {
		if item.Kind == "header" && item.Title == "Your Playlists" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Home dropped the library preview before capabilities arrived: %#v", msg.items)
	}
}

func TestAppleHomeHasAllPlaylistsEntry(t *testing.T) {
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Kind == "entry-playlists" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Apple Home has no All Playlists entry: %#v", items)
	}
}

func TestAllPlaylistsEntryPushesLibraryPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.items = []core.Item{{Kind: "entry-playlists", Title: "All Playlists"}}
	m.selected = 0
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	if m.title != "Playlists" {
		t.Fatalf("title = %q", m.title)
	}
	found := false
	for _, item := range m.items {
		if item.Kind == "playlist" {
			found = true
		}
	}
	if !found {
		t.Fatalf("playlists page empty: %#v", m.items)
	}
}

func TestAppleHomeHasAlbumsEntry(t *testing.T) {
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Kind == "entry-albums" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Apple Home has no Albums entry: %#v", items)
	}
}

func TestAlbumsEntryPushesLibraryPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.items = []core.Item{{Kind: "entry-albums", Title: "Albums"}}
	m.selected = 0
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	if m.title != "Albums" {
		t.Fatalf("title = %q", m.title)
	}
	if len(m.items) == 0 || m.items[0].Kind != "album" {
		t.Fatalf("albums page missing items: %#v", m.items)
	}
}

func TestAlbumRowPushesDetailPage(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "apple-music"
	m.items = []core.Item{{Kind: "album", ID: "al1", Ref: "apple-music:album:al1", Title: "Library Album", Artist: "Artist"}}
	m.selected = 0
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	if m.detailKind != "album" || m.detailID != "al1" || m.title != "Library Album" {
		t.Fatalf("context = title=%q detail=%q/%q", m.title, m.detailKind, m.detailID)
	}
	// The detail page lists songs, not a repeated album row.
	if len(m.items) != 2 || m.items[0].Kind != "song" || m.items[0].ID != "a1" {
		t.Fatalf("album detail items = %#v", m.items)
	}
	if f.played.ID != "" {
		t.Fatalf("pushing detail must not start playback: %#v", f.played)
	}
	if m.listTitle() != "Album" || m.listContext() != "Library Album" {
		t.Fatalf("header = %q context = %q", m.listTitle(), m.listContext())
	}
}

func TestAlbumDetailPlaysFromTrack(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.items = []core.Item{{Kind: "song", ID: "a1", Title: "Album Song One"}, {Kind: "song", ID: "a2", Title: "Album Song Two"}}
	m.selected = 1
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "album" || f.played.StartTrackID != "a2" || f.played.StartAt != 1 || !f.played.FromHere {
		t.Fatalf("playRequest = %#v", f.played)
	}
}

func TestSearchGroupsAlbumsOnlyWhenDeclared(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "apple-music"
	m = run(m, m.searchSource("album query"))
	kinds := make([]string, 0, len(f.searches))
	for _, call := range f.searches {
		kinds = append(kinds, call.kind)
	}
	if strings.Join(kinds, ",") != "song,album,playlist" {
		t.Fatalf("apple search kinds = %v", kinds)
	}
	headers := make([]string, 0, 3)
	for _, item := range m.items {
		if item.Kind == "header" {
			headers = append(headers, item.Title)
		}
	}
	if strings.Join(headers, ",") != "Songs,Albums,Playlists" {
		t.Fatalf("apple search groups = %v", headers)
	}

	// Audius does not declare search.albums, so the TUI must not request it.
	m2, f2, _ := newModel(t)
	m2.source = "audius"
	m2 = run(m2, m2.searchSource("album query"))
	for _, call := range f2.searches {
		if call.kind == "album" {
			t.Fatalf("audius search requested albums: %#v", f2.searches)
		}
	}
}

func TestAlbumDetailShuffleRestartsAlbum(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.items = []core.Item{{Kind: "song", ID: "a1"}}
	m.selected = 0
	next, cmd := m.handleKey(runeKey('S'))
	m = run(next.(Model), cmd)
	if f.played.Kind != "album" || f.played.Shuffle == nil || !*f.played.Shuffle || f.played.Repeat != "all" {
		t.Fatalf("shuffle request = %#v", f.played)
	}
	if !strings.Contains(m.message, "Shuffling") {
		t.Fatalf("message = %q", m.message)
	}
}

func TestAlbumDetailFooterAndPlayingHighlight(t *testing.T) {
	m, _, _ := newModel(t)
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.history = []page{{title: "Albums"}}
	m.loading = false
	if footer := m.footerLine(200); !strings.Contains(footer, "p play album") || strings.Contains(footer, "p play all") {
		t.Fatalf("album footer = %q", footer)
	}
	m.items = []core.Item{{Kind: "song", ID: "a1"}, {Kind: "song", ID: "a2"}}
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 1, Track: &core.Item{Kind: "song", ID: "a2"}, Queue: m.items}
	m.queueSource = queueContext{Kind: "album", ID: "al1", Title: "Library Album"}
	if !m.isPlayingItem(m.items[1]) || m.isPlayingItem(m.items[0]) {
		t.Fatal("album detail did not highlight the current track")
	}
}

// queueLines builds rows for renderPanel's text slot: cursor + text + scrollbar
// must equal the panel width, or the panel clips its own rows (the Up Next rail
// used to end every entry with an ellipsis and lose the scrollbar column).
func TestQueueRowsFitTheirPanel(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.renderer = newRenderer(theme.Load("print-room"))
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0,
		Queue: []core.Item{{Kind: "song", Title: "In the End", Artist: "LINKIN PARK"}, {Kind: "song", Title: "Schism", Artist: "TOOL"}}}
	l := m.layout()
	rows := m.queueLines(l.panelWidth-4, panelBodyRows(l.listHeight))
	panel := m.renderPanel("Up Next", m.queueCount(), rows, l.panelWidth, l.listHeight, false)
	if strings.Contains(plainText(panel), "…") {
		t.Fatalf("panel clipped its own rows:\n%s", panel)
	}
	for _, line := range strings.Split(panel, "\n") {
		if got := lipgloss.Width(line); got != l.panelWidth {
			t.Fatalf("panel line width = %d, want %d:\n%s", got, l.panelWidth, line)
		}
	}
}

// Palette candidates and source choices are text the user must read; bare rows
// inherit the terminal colours and vanish on a painted canvas (light themes).
func TestOverlayListRowsUseThemeTokens(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.renderer = newRenderer(theme.Load("print-room"))
	m.themeNames = theme.Names()

	m.overlay = "palette"
	view := m.overlayView(80, 20)
	if !strings.Contains(view, m.renderer.tabStyle.Render("  :help")) {
		t.Fatalf("palette rows are unstyled:\n%s", view)
	}

	m.overlay = "source-switcher"
	view = m.overlayView(80, 20)
	// The selected row keeps the selection token; unselected rows carry a theme
	// foreground (the label is appended to the styled prefix, so check the row
	// starts inside a styled span rather than with a bare reset).
	needle := strings.TrimSuffix(m.renderer.selStyle.Render(""), "\x1b[m") + "  Apple Music"
	if !strings.Contains(view, needle) {
		t.Fatalf("selected source row lost the selection token:\n%s", view)
	}
	// A bare line start would mean the row inherits the terminal colours.
	for _, bare := range []string{"\n  Audius", "\n  Radio", "\n  Apple Music"} {
		if strings.Contains(view, bare) {
			t.Fatalf("source row %q is unstyled:\n%s", bare, view)
		}
	}
}

// Overlay titles keep the panel-title hierarchy (accent, bold): renderBox used
// to paint them in the quiet border colour.
func TestOverlayTitleUsesAccentToken(t *testing.T) {
	m, _, _ := newModel(t)
	m.renderer = newRenderer(theme.Load("print-room"))
	box := m.renderBox("Theme", []string{"row"}, 40, 4)
	if !strings.Contains(box, m.renderer.titleStyle.Render("Theme")) {
		t.Fatalf("overlay title is not the accent token:\n%s", box)
	}
}

// Overlays composite over the live shell instead of replacing it, so the theme
// picker can preview against real content.
func TestOverlayKeepsTheShellBehindIt(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.renderer = newRenderer(theme.Load("gruvbox"))
	m.title = "Home"
	m.items = []core.Item{{Kind: "playlist", Title: "Adele Essentials", Artist: "Apple Music"}}
	m.themeNames = theme.Names()
	m.overlay = "theme"
	content := m.content()
	if !strings.Contains(plainText(content), "Adele Essentials") {
		t.Fatalf("overlay hid the shell:\n%s", plainText(content))
	}
	if !strings.Contains(plainText(content), "gruvbox") || !strings.Contains(plainText(content), "tokyo-night") {
		t.Fatalf("theme list missing from overlay:\n%s", plainText(content))
	}
}

// The cursor marker is painted text, not bare terminal output: bare `>`
// inherited the terminal foreground colour and vanished on a painted canvas
// (print-room), and read like the muted search-input text.
func TestCursorMarkerUsesAccentToken(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.renderer = newRenderer(theme.Load("print-room"))
	m = m.setTheme("print-room")
	m.loading = false
	m.items = []core.Item{{Kind: "song", Title: "One", Artist: "A"}, {Kind: "song", Title: "Two", Artist: "B"}}
	m.selected = 1
	lines := m.listLines(80, 3)
	if !strings.Contains(lines[1], m.renderer.accentStyle.Render("› ")) {
		t.Fatalf("list cursor is not the accent token: %q", lines[1])
	}
	if strings.Contains(plainText(lines[0]), "›") {
		t.Fatalf("unselected row gained a cursor marker: %q", plainText(lines[0]))
	}

	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 1,
		Queue: []core.Item{{Kind: "song", Title: "A"}, {Kind: "song", Title: "B"}}}
	m.queueFocus = true
	m.queueCursor = 0
	queue := m.queueLines(40, 2)
	if !strings.Contains(queue[0], m.renderer.accentStyle.Render("› ")) {
		t.Fatalf("queue cursor is not the accent token: %q", queue[0])
	}
}

// A nested style inside a row label ends with a reset, which drops the outer
// style for everything after it: the title following a favorite star rendered
// in the terminal's own foreground and vanished on a painted canvas. Every
// styled-form segment must carry its own token (docs/ui/theme.md).
func TestRowLabelSegmentsCarryTheirOwnTokens(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.renderer = newRenderer(theme.Load("print-room"))
	m = m.setTheme("print-room")
	m.loading = false
	m.source = "audius"
	item := core.Item{Kind: "playlist", ID: "audius:playlist:x", Title: "Electronic Butterflies", Artist: "Seb Park"}
	seedFavorite(&m, "audius", item)
	m.items = []core.Item{item}
	// The selected row renders the plain form inside the selection style, which
	// sets the text colour itself; the styled-form tokens matter on plain rows.
	m.selected = -1
	lines := m.listLines(100, 2)
	row := lines[0]
	if !strings.Contains(row, m.renderer.accentStyle.Render("★")) {
		t.Fatalf("favorite star lost the accent token: %q", row)
	}
	if !strings.Contains(row, m.renderer.rowStyle.Render(item.Title)) {
		t.Fatalf("title after a nested style lost the text token: %q", row)
	}
	// The plain form stays clean for the filled-row branches.
	_, plain := m.listLabel(item.Title, false, true, "")
	if plain != "★ "+item.Title {
		t.Fatalf("plain form drifted: %q", plain)
	}
}

// Filled rows (playing/selection background) keep one blank cell of padding on
// each side of the text, matching the main list: the band must never touch the
// panel padding or run text against the panel edge.
func TestQueueFilledRowsKeepPadding(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.renderer = newRenderer(theme.Load("gruvbox"))
	m = m.setTheme("gruvbox")
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0,
		Queue: []core.Item{{Kind: "song", Title: "写一条歌,写你我尔尔（feat. 黄奇斌）", Artist: "Vup"}}}
	lines := m.queueLines(m.layout().panelWidth-4, 2)
	row := lines[0]
	if !strings.Contains(row, "\x1b[m \x1b[m") && !strings.HasSuffix(strings.TrimRight(plainText(row), "…"), " ") {
		// The band's trailing cell must be a blank, not the ellipsis.
		if strings.HasSuffix(plainText(row), "…") {
			t.Fatalf("filled row runs text against the panel edge: %q", plainText(row))
		}
	}
	if !strings.Contains(plainText(row), "  ▶ ") && !strings.Contains(plainText(row), " ▶ ") {
		t.Fatalf("filled row lost its leading blank: %q", plainText(row))
	}
}

func TestAppleHomeHasAllFavoritesEntry(t *testing.T) {
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Kind == "entry-favorites" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Home has no All Favorites entry: %#v", items)
	}
}

// The All Favorites page shows the full local list (Home caps previews at
// five); unfavorite keeps the cursor on a stable row.
func TestAllFavoritesPageShowsFullListAndUnfavorite(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "apple-music", "Home", "Home"
	for i := 0; i < 7; i++ {
		seedFavorite(&m, "apple-music", core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Song %d", i)})
	}
	if got := m.activity.FavoritesFor("apple-music"); len(got) != 7 {
		t.Fatalf("favorites seeded = %d", len(got))
	}
	m.items = []core.Item{{Kind: "entry-favorites", Title: "All Favorites"}}
	m.selected = 0
	next, cmd := m.activate()
	m = next.(Model)
	m = run(next.(Model), cmd)
	if m.title != "All Favorites" || len(m.items) != 7 {
		t.Fatalf("favorites page = title=%q items=%d", m.title, len(m.items))
	}
	// Unfavorite the selected row: the mirror (authoritative via state.changed)
	// drops it and the page reload keeps the cursor on a valid row.
	m.selected = 0
	next, cmd = m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("apple-music")) != 6 {
		t.Fatalf("unfavorite failed: %d", len(m.activity.FavoritesFor("apple-music")))
	}
}

func TestAllFavoritesPageIsEmptyWithoutFavorites(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Favorites", "All Favorites"
	m.loading = true
	m = run(m, m.loadView())
	if m.title != "All Favorites" || len(m.items) != 0 || m.loading {
		t.Fatalf("empty favorites page = title=%q items=%d loading=%v", m.title, len(m.items), m.loading)
	}
}

// The All Favorites page is its own view: unfavorite reloads the list live
// (row count shrinks) instead of only clearing the star.
func TestAllFavoritesPageReloadsAfterUnfavorite(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "apple-music", "Home", "Home"
	for i := 0; i < 7; i++ {
		seedFavorite(&m, "apple-music", core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Song %d", i)})
	}
	m.items = []core.Item{{Kind: "entry-favorites", Title: "All Favorites"}}
	m.selected = 0
	next, cmd := m.activate()
	m = next.(Model)
	m = run(next.(Model), cmd)
	if m.view != "Favorites" || m.title != "All Favorites" || len(m.items) != 7 {
		t.Fatalf("favorites page = view=%q title=%q items=%d", m.view, m.title, len(m.items))
	}
	m.selected = 3
	next, cmd = m.toggleFavorite()
	m = run(next.(Model), cmd)
	if len(m.activity.FavoritesFor("apple-music")) != 6 {
		t.Fatalf("mirror did not drop the favorite: %d", len(m.activity.FavoritesFor("apple-music")))
	}
	// The server's state.changed replaces the mirror; applyAppState must clear
	// the cached favorites list so the reload below cannot paint stale rows.
	state := api.AppState{Revision: m.appRevision + 1, Theme: m.store.Theme, LastSource: api.SourceID(m.store.LastSource)}
	state.Favorites = append(state.Favorites, m.activity.favorites...)
	m.applyAppState(state)
	m = run(m, m.loadView())
	if len(m.items) != 6 {
		t.Fatalf("favorites page did not reload after unfavorite: items=%d", len(m.items))
	}
	if m.selected != 3 {
		t.Fatalf("cursor jumped on reload: selected=%d, want preserved 3", m.selected)
	}
}

// TUI spellings and server canonical identities agree, so the favorite star
// shows on rows favorited through the client path.
func TestFavoriteStarMatchesCanonicalIdentity(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.items = []core.Item{{Kind: "song", ID: "1721843001", Title: "Aruarian Dance", Artist: "Nujabes"}}
	m.selected = 0
	next, cmd := m.toggleFavorite()
	m = run(next.(Model), cmd)
	if got := m.activity.FavoritesFor("apple-music"); len(got) != 1 || got[0].ID != "1721843001" {
		t.Fatalf("favorites = %#v", got)
	}
	_, plain := m.listLabel("Aruarian Dance", false, m.activity.IsFavorite("apple-music", stableItemID("apple-music", core.Item{Kind: "song", ID: "1721843001"})), "")
	if !strings.HasPrefix(plain, "★") {
		t.Fatalf("favorite star missing for canonical identity: %q", plain)
	}
}

// A qualified play committed by the server must appear in an open Recent view
// without leaving and re-entering it.
func TestRecentViewFollowsStateCommitLive(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "apple-music", "Recent", "Recent"
	m.loading = true
	m = run(m, m.loadView())
	if len(m.items) != 0 {
		t.Fatalf("recent preloaded: %#v", m.items)
	}
	item := api.Item{Source: api.SourceAppleMusic, Kind: "song", ID: "am:1721843001", ProviderID: "1721843001", Ref: "apple-music:song:1721843001", Title: "Aruarian Dance", Artist: "Nujabes"}
	state := api.AppState{Revision: m.appRevision + 1, Theme: m.store.Theme, LastSource: api.SourceAppleMusic, Recent: []api.RecentEntry{{Item: item, PlayedAt: "2026-09-20T09:42:26Z"}}}
	m.applyAppState(state)
	m = run(m, m.loadView())
	if len(m.items) != 1 || m.items[0].Title != "Aruarian Dance" {
		t.Fatalf("open Recent did not follow the commit: %#v", m.items)
	}
}

// A finished finite queue reads as Finished, not as a user pause: the two are
// indistinguishable in the raw MusicKit status (see docs/product/open-questions.md
// OQ11), so the helper reports "ended" and the dock must show it.
func TestFinishedQueueIsNotShownAsPaused(t *testing.T) {
	model, _, _ := newModel(t)
	model.state.Status = "paused"
	paused := model.playbackFacts(120)
	if !strings.Contains(paused, "Paused") {
		t.Fatalf("paused dock = %q", paused)
	}
	model.state.Status = "ended"
	finished := model.playbackFacts(120)
	if !strings.Contains(finished, "Finished") {
		t.Fatalf("ended dock = %q, want a Finished label", finished)
	}
	if strings.Contains(finished, "Paused") {
		t.Fatalf("ended dock still reads as paused: %q", finished)
	}
}
