package tui

import (
	"context"
	"errors"
	"fmt"
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

// sgrPrefix is the escape a style writes before its text, so a test can assert
// which token painted a row without pinning the exact sequence.
func sgrPrefix(style lipgloss.Style) string {
	rendered := style.Render("x")
	if end := strings.IndexByte(rendered, 'x'); end > 0 {
		return rendered[:end]
	}
	return ""
}

// sgrParams are the parameters a style writes before its text. Matching the
// parameters instead of the whole escape survives lipgloss merging a fill in.
func sgrParams(style lipgloss.Style) string {
	prefix := sgrPrefix(style)
	if prefix == "" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(prefix, "\x1b["), "m")
}

// fillParams are the SGR parameters that paint a cell with a colour. Lipgloss
// merges foreground and background into one sequence, so a test cannot match the
// standalone background escape that backgroundSGR builds.
func fillParams(value string) string {
	sequence := backgroundSGR(value)
	if sequence == "" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m")
}

type fake struct {
	mu                 sync.Mutex
	state              core.PlaybackState
	played             core.PlaybackRequest
	playForm           core.PlaybackForm
	radioURL           string
	tracks             []core.Item
	stateCalls         int
	stops              int
	queueJumps         int
	probed             []string
	probeResult        core.RadioProbeResult
	probeErr           error
	searches           []searchCall
	trending           []searchCall
	recommendations    []string
	recommendationsErr error
	playSongSet        []string
	enqueuePositions   []string
	audiusLibrary      []core.Item
	playStarted        chan struct{}
	playBlock          chan struct{}
	nexts              int
}

type searchCall struct{ source, term, kind string }

type fakeRadio struct{}

type recordingRemote struct {
	block                chan struct{}
	err                  error
	ops                  []string
	favoriteSource       string
	favoriteItem         core.Item
	favoriteDesiredState bool
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
func (r *recordingRemote) SetFavorite(_ context.Context, source string, item core.Item, favorited bool) error {
	r.favoriteSource, r.favoriteItem, r.favoriteDesiredState = source, item, favorited
	return r.wait("favorites.set")
}
func (r *recordingRemote) AuthorizationStatus(context.Context, string) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "authorized"}, nil
}
func (r *recordingRemote) AuthList(context.Context) ([]api.SourceAuthorization, error) {
	return nil, nil
}
func (r *recordingRemote) BeginAuth(context.Context, string) (api.AuthorizationFlow, error) {
	return api.AuthorizationFlow{}, nil
}
func (r *recordingRemote) FlowStatus(context.Context, string) (api.AuthorizationFlow, error) {
	return api.AuthorizationFlow{}, nil
}
func (r *recordingRemote) CancelAuth(context.Context, string) (api.AuthorizationFlow, error) {
	return api.AuthorizationFlow{}, nil
}
func (r *recordingRemote) DisconnectAuth(context.Context, string) (api.SourceAuthorization, error) {
	return api.SourceAuthorization{}, nil
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
		// "all" and "song" both return the songs group; "all" also carries
		// playlists so the TUI's single-request Discover sees both kinds.
		songs := []core.Item{{Source: source, Kind: "song", ID: "s1", Ref: "audius:song:s1", Title: "Audius Song", Artist: "Creator"}}
		if kind == "all" {
			return append(songs, core.Item{Source: source, Kind: "playlist", ID: "p1", Ref: "audius:playlist:p1", Title: "Audius Playlist"}), nil
		}
		return songs, nil
	}
	if source == "jamendo" {
		// Jamendo is song-only trending; a playlist request reaching this fake
		// means the TUI ignored the kind-specific capability.
		if kind == "playlist" {
			return nil, errors.New("jamendo playlist trending unsupported")
		}
		return []core.Item{{Source: source, Kind: "song", ID: "t1", Ref: "jamendo:song:t1", Title: "Jamendo Featured", Artist: "Artist"}}, nil
	}
	return nil, errors.New("trending unsupported")
}
func (f *fake) RecommendationsSource(_ context.Context, source string, _ int) ([]core.Item, error) {
	f.recommendations = append(f.recommendations, source)
	if f.recommendationsErr != nil {
		return nil, f.recommendationsErr
	}
	if source != "apple-music" {
		return nil, errors.New("recommendations unsupported")
	}
	return []core.Item{
		{Source: source, Kind: api.KindPlaylist, ID: "rp1", Ref: "apple-music:playlist:rp1", Title: "Recommended Mix"},
		{Source: source, Kind: api.KindAlbum, ID: "ra1", Ref: "apple-music:album:ra1", Title: "Recommended Album"},
	}, nil
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
func (f *fake) PlaySongs(_ context.Context, ids []string, startIndex int, form core.PlaybackForm) (core.PlaybackState, error) {
	f.playForm = form
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
		{ID: api.SourceJamendo, Available: true, Availability: api.AvailabilityReady, Capabilities: map[string]api.Capability{
			api.CapSearchSongs: {Available: true}, api.CapSearchPlaylists: {Available: true}, api.CapSearchTrendingSongs: {Available: true}, api.CapPlaybackFull: {Available: true}, api.CapQueue: {Available: true},
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
// favorite item (docs/internals/persistence/local-activity.md §4): client spellings like
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

func TestInitialWatchWarningRemainsVisibleAfterTransientFeedback(t *testing.T) {
	store := state.NewMemory()
	snapshot := api.WatchSnapshot{
		State:   &api.AppState{Theme: "gruvbox", LastSource: api.SourceAppleMusic},
		Warning: &api.WatchWarning{Code: api.CodeStorageUnavailable, Message: "activity unavailable"},
	}
	m := New(Options{Provider: &fake{}, Player: &fake{}, Radio: fakeRadio{}, Store: store, InitialWatch: &snapshot})
	if !strings.Contains(m.message, api.CodeStorageUnavailable) || m.persistentWarning == "" {
		t.Fatalf("startup warning = message:%q persistent:%q", m.message, m.persistentWarning)
	}
	m.message = ""
	message, isErr := m.feedbackText()
	if !isErr || !strings.Contains(message, api.CodeStorageUnavailable) {
		t.Fatalf("fallback warning = %q err=%v", message, isErr)
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

func radioPageItems(start, count int) []core.Item {
	items := make([]core.Item, count)
	for i := range items {
		items[i] = core.Item{Kind: "stream", ID: fmt.Sprintf("station-%d", start+i), URL: fmt.Sprintf("https://radio.example/%d", start+i), Title: fmt.Sprintf("Station %d", start+i)}
	}
	return items
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

func TestTextCommitSearchesWithoutExtraConfirm(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay = "radio", "discovery"
	next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Text: "jazz"})
	m = next.(Model)
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	// A plain text search applies on the commit Enter itself: the old second
	// confirm on an empty filter form read as a dead key (batch
	// 2026-09-23-postaudit M2).
	if cmd == nil || m.overlay != "" || m.browseQuery.Term != "jazz" || m.view != "Browse" {
		t.Fatalf("plain commit should search: cmd=%v overlay=%q query=%#v view=%q", cmd != nil, m.overlay, m.browseQuery, m.view)
	}
}

func TestTextCommitWithFacetsLandsOnConfirm(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay = "radio", "discovery"
	m.discoveryPending.Language = "Japanese"
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
	if strings.Contains(info, "Auth") || strings.Contains(info, "denied") {
		t.Fatalf("Radio info inherited Apple authorization:\n%s", info)
	}
	m.source, m.sourceAuth = "apple-music", core.AuthorizationStatus{Status: "denied"}
	info = plainText(m.View().Content)
	if !strings.Contains(info, "Auth") || !strings.Contains(info, "denied") {
		t.Fatalf("Apple info overlay missing its own authorization:\n%s", info)
	}
}

func TestAccountHintHiddenWhenReady(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	if view := plainText(m.View().Content); strings.Contains(view, "Account:") {
		t.Fatalf("unexpected account hint:\n%s", view)
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

func TestLocalFilter(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Title: "Alpha"}, {Title: "Beta"}}
	m.filter = "beta"
	items := m.visibleItems()
	if len(items) != 1 || items[0].Title != "Beta" {
		t.Fatalf("filtered = %#v", items)
	}
}

func TestListLabelPlainFormHasNoNestedStyles(t *testing.T) {
	styled, plain := listLabel("RTL", true, false, "")
	// Radio and Apple favorites share the prefix form so a truncated title
	// cannot hide the star (batch 2026-09-23-postaudit H1).
	if plain != "★ RTL" {
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

func titlesOf(items []core.Item) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
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

// A live stream that announces ICY metadata shows the current song in the dock.
// Pausing only pauses the local audio backend: the dock keeps showing the
// station's latest announcement instead of a frozen copy of the paused moment
// (accepted limitation, docs/product/limitations.md §11) — lilt never freezes
// or replays ICY metadata, so the announced title may change while paused.
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
	// The stream announces a new program while the local audio is paused: the
	// dock follows the announcement, exactly as it does while playing.
	m.state.Status = "paused"
	m.state.StreamTitle = "One More Time"
	view = plainText(m.content())
	if !strings.Contains(view, "❚❚ Paused") {
		t.Fatalf("paused status missing from dock:\n%s", view)
	}
	if !strings.Contains(view, "One More Time") || strings.Contains(view, "Around the World") {
		t.Fatalf("paused dock must show the current announcement, not the paused-moment copy:\n%s", view)
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

// A favorite is a local persist: it must not flip playback into the busy
// state, which rendered the empty Now Playing dock as "working…" for seconds
// (batch 2026-09-23-postaudit H1, r9 replay).
func TestFavoritePersistsWithoutBusyState(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "apple-music", "Home", "Home"
	m.items = []core.Item{{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"}}
	m.selected = 0
	next, cmd := m.toggleFavorite()
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("favorite produced no persist command")
	}
	if m.busy || m.busySince != (time.Time{}) {
		t.Fatalf("favorite set the playback busy state: busy=%v busySince=%v", m.busy, m.busySince)
	}
	if !m.persisting || m.operationID == 0 {
		t.Fatalf("favorite did not hold the persist slot: persisting=%v id=%d", m.persisting, m.operationID)
	}

	// The empty Now Playing dock keeps "Nothing playing" while the save is in
	// flight: the busy label is only reachable from the playback busy flag.
	if m.state.Track == nil && m.busy {
		t.Fatalf("busy flag would render the dock as working…")
	}
}

// The playback error surfaces the stable message only, not the wire form
// "playback_error: <message>" (batch 2026-09-23-postaudit M4).
func TestPlaybackErrorTextUsesStableMessageOnly(t *testing.T) {
	err := &api.Error{Code: api.CodePlaybackError, Message: "Playback could not be started"}
	if got := playbackErrorText(err); got != "Playback error: Playback could not be started" {
		t.Fatalf("playback error text = %q", got)
	}
}
