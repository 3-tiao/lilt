package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fake struct {
	state      core.PlaybackState
	played     core.PlaybackRequest
	radioURL   string
	tracks     []core.Item
	stateCalls int
	queueJumps int
}

type fakeRadio struct{}

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

type recordingRadio struct {
	fakeRadio
	popularLimit, searchLimit int
	filter                    radio.Filter
}

func (r *recordingRadio) Popular(_ context.Context, f radio.Filter, limit, _ int) ([]radio.Station, error) {
	r.popularLimit, r.filter = limit, f
	return r.fakeRadio.Popular(context.Background(), f, limit, 0)
}
func (r *recordingRadio) SearchFiltered(_ context.Context, _ string, f radio.Filter, _ int, limit int) ([]radio.Station, error) {
	r.searchLimit, r.filter = limit, f
	return r.fakeRadio.SearchFiltered(context.Background(), "", f, 0, limit)
}

func (f *fake) Search(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "1", Title: "One", Artist: "Artist"}}, nil
}
func (f *fake) SearchPlaylists(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "p1", Title: "Playlist"}}, nil
}
func (f *fake) LibraryPlaylists(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "p1", Title: "My Playlist"}}, nil
}
func (f *fake) PlaylistTracks(context.Context, string) ([]core.Item, error) {
	if f.tracks != nil {
		return f.tracks, nil
	}
	return []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}, nil
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
	f.played = request
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "One"}, Shuffle: f.state.Shuffle}
	return nil
}
func (f *fake) Pause(context.Context) error    { f.state.Status = "paused"; return nil }
func (f *fake) Resume(context.Context) error   { f.state.Status = "playing"; return nil }
func (f *fake) Next(context.Context) error     { return nil }
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
	f.state.Status = "stopped"
	return f.state, nil
}
func (f *fake) Enqueue(context.Context, core.PlaybackRequest, string) (core.PlaybackState, error) {
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
func (f *fake) PlaySongs(_ context.Context, ids []string, startIndex int) (core.PlaybackState, error) {
	queue := make([]core.Item, 0, len(ids))
	for _, id := range ids {
		queue = append(queue, core.Item{Kind: "song", ID: id})
	}
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: queue, QueueIndex: startIndex}
	return f.state, nil
}
func (f *fake) QueueJump(_ context.Context, index int) (core.PlaybackState, error) {
	f.queueJumps++
	f.state.QueueIndex = index
	return f.state, nil
}
func (f *fake) QueueRemove(_ context.Context, index int) (core.PlaybackState, error) {
	if index >= 0 && index < len(f.state.Queue) {
		f.state.Queue = append(f.state.Queue[:index], f.state.Queue[index+1:]...)
	}
	return f.state, nil
}
func (f *fake) QueueMove(context.Context, int, int) (core.PlaybackState, error) {
	return f.state, nil
}
func (f *fake) QueueClear(context.Context) (core.PlaybackState, error) {
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
	return New(opts), f, store
}

func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			subMsg := sub()
			if _, isTick := subMsg.(tickMsg); isTick {
				continue
			}
			next, _ := m.Update(subMsg)
			return next.(Model)
		}
		return m
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestInitLoadsPlaylists(t *testing.T) {
	m, _, _ := newModel(t)
	m.view = "Playlists"
	m.loading = true
	m = run(m, m.Init())
	if m.title != "Playlists" || len(m.items) != 1 || m.loading {
		t.Fatalf("title=%q items=%d loading=%v", m.title, len(m.items), m.loading)
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
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size=%v lines=%d, want %d", size, len(lines), size[1])
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != size[0] {
				t.Fatalf("size=%v line %d width=%d, want %d: %q", size, i, got, size[0], line)
			}
		}
		if size[0] >= 88 && !strings.Contains(view, "Up Next ·") {
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
	if title := m.nowTitle(); !strings.Contains(title, "buffering") {
		t.Fatalf("nowTitle = %q, want buffering hint", title)
	}
}

func TestPlayItemPlaylistKeepsQueueSource(t *testing.T) {
	m, _, _ := newModel(t)
	m = run(m, m.playItem(core.Item{Kind: "playlist", ID: "p1", Title: "Road"}))
	if m.queueSource != (queueContext{Kind: "playlist", ID: "p1", Title: "Road"}) {
		t.Fatalf("queue source = %#v", m.queueSource)
	}
	m = run(m, m.playItem(core.Item{Kind: "song", ID: "s1", Title: "One"}))
	if m.queueSource != (queueContext{}) {
		t.Fatalf("song should clear queue source: %#v", m.queueSource)
	}
}

func TestQueueFocusKeepsGlobalKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if !m.queueFocus {
		t.Fatal("queue focus did not open")
	}
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	if m.queueCursor != 1 {
		t.Fatalf("queue cursor = %d, want 1", m.queueCursor)
	}
	next, cmd := m.handleKey(runeKey('q'))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("q must quit while the panel is focused")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not produce a quit message")
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.source != "radio" {
		t.Fatalf("tab did not switch source: %q", m.source)
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

func TestQueueRemoveShowsFeedback(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if !strings.Contains(m.message, "Removed: A") {
		t.Fatalf("remove feedback = %q", m.message)
	}
}

func TestEmptyStateHints(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.view = "Favorites"
	m.title = "Favorites"
	m.loading = false
	m.items = nil
	if view := m.View(); !strings.Contains(view, "press a to add a stream URL") {
		t.Fatalf("empty hint missing:\n%s", view)
	}
}

func TestRecentIncludesContainers(t *testing.T) {
	m, _, store := newModel(t)
	m.source = "apple-music"
	m.view = "Recent"
	store.AddRecentContainer(core.Item{Kind: "playlist", ID: "p1", Title: "Road"})
	msg := m.loadView()()
	list, ok := msg.(listMsg)
	if !ok {
		t.Fatalf("unexpected message %#v", msg)
	}
	if len(list.items) < 3 || list.items[0].Title != "Recently Played Lists" || list.items[1].Kind != "playlist" {
		t.Fatalf("recent items = %#v", list.items)
	}
	found := false
	for _, item := range list.items {
		if item.Title == "Recently Played Songs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing song section: %#v", list.items)
	}
}

func TestSearchResultsShowKindGlyphs(t *testing.T) {
	m, _, _ := newModel(t)
	m.title = "Search: rock"
	m.items = []core.Item{{Kind: "song", ID: "1", Title: "Song"}, {Kind: "playlist", ID: "p1", Title: "List"}}
	lines := strings.Join(m.listLines(60, 10), "\n")
	if !strings.Contains(lines, "♪ Song") || !strings.Contains(lines, "≡ List") {
		t.Fatalf("kind glyphs missing:\n%s", lines)
	}
}

func TestRadioBrowseSlashAndFBehavior(t *testing.T) {
	if got := strings.Join(radioViews, ","); got != "Favorites,Recent,Browse" {
		t.Fatalf("radio views = %q", got)
	}
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Favorites"
	next, _ := m.handleKey(runeKey('F'))
	m = next.(Model)
	if m.overlay != "" || m.inputMode != "" {
		t.Fatalf("Radio F should be inert: overlay=%q mode=%q", m.overlay, m.inputMode)
	}
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	if m.overlay != "discovery" {
		t.Fatalf("Radio slash overlay = %q", m.overlay)
	}
	// Esc discards pending edits.
	m.discoveryPending.Language = "Japanese"
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.overlay != "" || m.discoveryPending != (radioDiscovery{}) {
		t.Fatalf("cancel retained filter: %#v", m.discoveryPending)
	}
	m.view = "Browse"
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	m.cache["radio/Browse"] = []core.Item{{Kind: "stream", Title: "stale"}}
	m.discoveryPending = radioDiscovery{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}
	m.discoverySelected = 5
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.overlay != "" || m.view != "Browse" || m.title != "Showing: Japanese · City Pop · JP" || m.browseQuery != (radioDiscovery{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}) {
		t.Fatalf("apply = view=%q title=%q query=%#v overlay=%q", m.view, m.title, m.browseQuery, m.overlay)
	}
	if m.loading != true || len(m.items) != 0 {
		t.Fatalf("apply did not start a clean reload: loading=%v items=%d", m.loading, len(m.items))
	}
	m.view = "Recent"
	next, _ = m.handleKey(runeKey('F'))
	m = next.(Model)
	if m.overlay != "" || m.inputMode != "" {
		t.Fatalf("Radio F should remain inert: overlay=%q mode=%q", m.overlay, m.inputMode)
	}
	m.source = "apple-music"
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	if m.inputMode != "search" || !m.input.Focused() {
		t.Fatalf("Apple slash changed: mode=%q focused=%v", m.inputMode, m.input.Focused())
	}
	m.input.Blur()
	m.inputMode = ""
	next, _ = m.handleKey(runeKey('F'))
	m = next.(Model)
	if m.inputMode != "filter" || !m.input.Focused() {
		t.Fatalf("Apple F changed: mode=%q focused=%v", m.inputMode, m.input.Focused())
	}
}

func TestBrowseResetFiltersAndStaleOptions(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.overlay, m.generation = "radio", "Browse", "discovery-options", 4
	m.discoveryKind = "genre"
	next, _ := m.Update(discoveryOptionsMsg{generation: 3, kind: "genre", values: []core.Item{{Title: "stale"}}})
	m = next.(Model)
	if len(m.discoveryOptions) != 0 {
		t.Fatalf("stale options applied: %#v", m.discoveryOptions)
	}
	m.overlay, m.discoverySelected = "discovery", 4
	m.discoveryPending = radioDiscovery{Language: "Japanese", Tag: "City Pop"}
	m.discoveryTerm = "東京"
	next, cmd := m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.overlay != "discovery" || m.discoveryPending != (radioDiscovery{}) || m.discoveryTerm != "東京" {
		t.Fatalf("reset = pending=%#v text=%q", m.discoveryPending, m.discoveryTerm)
	}
	view := m.overlayView(80, 20)
	for _, want := range []string{"Search", "Text", "東京", "Filters", "Language", "Genre", "Country", "Reset filters", "[ Confirm ]", "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu missing %q:\n%s", want, view)
		}
	}
	if got := discoverySearchValue(""); got != "Not set" {
		t.Fatalf("empty search value = %q", got)
	}
	m.discoveryPending.Language, m.discoveryTerm, m.discoverySelected = "Korean", "Seoul", 5
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.discoverySelected != 6 {
		t.Fatalf("right action selection = %d, want Cancel", m.discoverySelected)
	}
	next, cmd = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.overlay != "" || m.discoveryTerm != "" || m.discoveryPending != (radioDiscovery{}) {
		t.Fatalf("cancel = overlay=%q text=%q pending=%#v", m.overlay, m.discoveryTerm, m.discoveryPending)
	}
}

func TestRadioBrowseTextSubmitCancelAndHistory(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://example.test", Title: "Prior"}}
	next, _ := m.handleKey(runeKey('/'))
	m = next.(Model)
	// Typing on the selected Text row starts editing immediately.
	next, _ = m.handleDiscoveryKey(runeKey('j'))
	m = next.(Model)
	if m.overlay != "discovery-text" || m.input.Value() != "j" {
		t.Fatalf("direct text input = overlay %q value %q", m.overlay, m.input.Value())
	}
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	// Search text editor is a visible, normal text input and Esc discards edits.
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "discovery-text" || !m.input.Focused() || !strings.Contains(m.overlayView(80, 20), "Search text:") {
		t.Fatalf("text editor not visible: overlay=%q", m.overlay)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("日本")})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.discoveryTerm != "" || m.overlay != "discovery" {
		t.Fatalf("text cancel applied %q / %q", m.discoveryTerm, m.overlay)
	}
	// Commit Unicode text, choose a facet, then apply the query to Browse.
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("東京")})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m.discoveryPending.Language = "Japanese"
	m.discoverySelected = 5
	next, cmd := m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.view != "Browse" || m.title != "Showing: 東京 · Japanese" || m.browseQuery != (radioDiscovery{Language: "Japanese", Term: "東京"}) || m.discoveryTerm != "" || m.loading != true || len(m.items) != 0 {
		t.Fatalf("browse query = view=%q title=%q query=%#v term=%q loading=%v", m.view, m.title, m.browseQuery, m.discoveryTerm, m.loading)
	}
	if len(m.history) != 0 {
		t.Fatalf("browse query pushed a history page: %d", len(m.history))
	}
	m = run(m, cmd)
	if m.loading != false || len(m.items) != 1 || m.title != "Showing: 東京 · Japanese" {
		t.Fatalf("browse results = title=%q loading=%v items=%d", m.title, m.loading, len(m.items))
	}
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.view != "Browse" || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" {
		t.Fatalf("esc should reset the query in place: view=%q title=%q query=%#v", m.view, m.title, m.browseQuery)
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

func TestRadioBrowseQueryPrefillAndReset(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	next, _ := m.handleKey(runeKey('/'))
	m = next.(Model)
	if m.discoveryTerm != "" || m.discoveryPending != (radioDiscovery{}) {
		t.Fatalf("query did not start blank outside Browse: %#v", m.discoveryPending)
	}
	m.discoveryPending = radioDiscovery{Language: "Japanese"}
	m.discoveryTerm = "Tokyo"
	m.discoverySelected = 5
	next, cmd := m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m = run(m, cmd)
	if m.view != "Browse" || m.browseQuery != (radioDiscovery{Language: "Japanese", Term: "Tokyo"}) {
		t.Fatalf("query did not land in Browse: view=%q query=%#v", m.view, m.browseQuery)
	}
	// Reopening / from Browse prefills the active query.
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	if m.discoveryTerm != "Tokyo" || m.discoveryPending.Language != "Japanese" {
		t.Fatalf("Browse did not prefill query: %#v", m.discoveryPending)
	}
	// An empty Confirm resets Browse to Popular Worldwide.
	m.discoveryPending, m.discoveryTerm, m.discoverySelected = radioDiscovery{}, "", 5
	next, cmd = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m = run(m, cmd)
	if m.view != "Browse" || m.title != "Popular Worldwide" || m.browseQuery != (radioDiscovery{}) || m.loading != false {
		t.Fatalf("reset = view=%q title=%q query=%#v loading=%v", m.view, m.title, m.browseQuery, m.loading)
	}
	// Leaving Browse starts blank again.
	m.view, m.title = "Favorites", "Favorites"
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	if m.discoveryTerm != "" || m.discoveryPending != (radioDiscovery{}) {
		t.Fatalf("query outside Browse was not blank: %#v", m.discoveryPending)
	}
}

func TestRadioBrowseEmptyStateDistinguishesQuery(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Showing: city pop"
	m.browseQuery = radioDiscovery{Term: "city pop"}
	m.items, m.loading, m.width, m.height = nil, false, 100, 24
	if view := m.View(); !strings.Contains(view, "(no stations matched — press / to adjust the query)") {
		t.Fatalf("queried Browse empty state missing:\n%s", view)
	}
	m.browseQuery = radioDiscovery{}
	m.title = "Popular Worldwide"
	if view := m.View(); strings.Contains(view, "no stations matched") {
		t.Fatalf("default Browse should keep its own empty state:\n%s", view)
	}
}

func TestRadioBrowseTextTakesPrintableKeysLiterally(t *testing.T) {
	for _, key := range []rune{'j', 'k', 'h', 'l', 'q', '?', '/'} {
		t.Run(string(key), func(t *testing.T) {
			m, _, _ := newModel(t)
			m.source, m.overlay, m.discoverySelected = "radio", "discovery", 0
			next, _ := m.handleDiscoveryKey(runeKey(key))
			m = next.(Model)
			if m.overlay != "discovery-text" || m.input.Value() != string(key) {
				t.Fatalf("%q = overlay %q text %q", key, m.overlay, m.input.Value())
			}
		})
	}
}

func TestDiscoveryOptionsSupportAnyAndUnicodeQuery(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.overlay = "radio", "Browse", "discovery-options"
	m.discoveryKind = "language"
	if view := m.overlayView(80, 20); !strings.Contains(view, "Loading...") {
		t.Fatalf("loading state missing:\n%s", view)
	}
	m.discoveryOptions = []core.Item{{Title: "Any"}, {ID: "japanese", Title: "japanese"}}
	m.discoveryQuery = "日本"
	if view := m.overlayView(80, 20); !strings.Contains(view, "No matching options") {
		t.Fatalf("empty state missing:\n%s", view)
	}
	m.discoveryOptionsErr = "network unavailable"
	if view := m.overlayView(80, 20); !strings.Contains(view, "Unable to load options") {
		t.Fatalf("error state missing:\n%s", view)
	}
	m.discoveryOptionsErr = ""

	m.discoveryQuery = ""
	var next tea.Model
	for _, key := range []rune{'j', 'k', 'h', 'l'} {
		next, _ = m.handleDiscoveryKey(runeKey(key))
		m = next.(Model)
	}
	if m.discoveryQuery != "jkhl" {
		t.Fatalf("literal option filter = %q", m.discoveryQuery)
	}
	m.discoveryQuery = "日本"
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.discoveryQuery != "日" {
		t.Fatalf("unicode backspace = %q", m.discoveryQuery)
	}

	m.discoveryQuery, m.discoverySelected = "", 0
	m.discoveryPending = radioDiscovery{Language: "japanese", Tag: "city pop", CountryCode: "JP", CountryName: "Japan"}
	for _, kind := range []string{"language", "genre", "country"} {
		m.overlay, m.discoveryKind, m.discoverySelected = "discovery-options", kind, 0
		next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		if m.discoverySelected != discoveryFieldIndex(kind) {
			t.Fatalf("%s returned to menu index %d", kind, m.discoverySelected)
		}
	}
	if m.discoveryPending != (radioDiscovery{}) || m.overlay != "discovery" {
		t.Fatalf("Any did not clear every facet: %#v overlay=%q", m.discoveryPending, m.overlay)
	}
	if got := discoveryValue(""); got != "Any" {
		t.Fatalf("empty discovery value = %q", got)
	}
}

func TestRadioBrowseLimitAndSearchSummary(t *testing.T) {
	m, _, _ := newModel(t)
	r := &recordingRadio{}
	m.radio = r
	m.source, m.view = "radio", "Browse"
	msg := m.loadView()().(listMsg)
	if msg.title != "Popular Worldwide" || len(msg.items) != 1 || r.popularLimit != 20 || r.filter != (radio.Filter{}) {
		t.Fatalf("browse = %q %#v", msg.title, msg.items)
	}
	m.browseQuery = radioDiscovery{Term: "Tokyo"}
	msg = m.loadView()().(listMsg)
	if msg.title != "Showing: Tokyo" || len(msg.items) != 1 || r.searchLimit != 20 || r.filter != (radio.Filter{}) {
		t.Fatalf("queried browse = %q %#v filter=%#v limit=%d", msg.title, msg.items, r.filter, r.searchLimit)
	}
	m.browseQuery = radioDiscovery{Language: "Japanese", CountryCode: "JP"}
	msg = m.loadView()().(listMsg)
	if msg.title != "Showing: Japanese · JP" || r.filter != (radio.Filter{Language: "Japanese", CountryCode: "JP"}) {
		t.Fatalf("facet browse = %q filter=%#v", msg.title, r.filter)
	}
}

func TestRadioBrowseEscRestoresPopularWorldwide(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Showing: city pop"
	m.browseQuery = radioDiscovery{Term: "city pop"}
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/a", Title: "A"}}
	m.selected = 0
	m.cache["radio/Browse"] = m.items

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd == nil || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" || m.loading != true || len(m.items) != 0 {
		t.Fatalf("esc reset = query=%#v title=%q loading=%v items=%d", m.browseQuery, m.title, m.loading, len(m.items))
	}
	if _, ok := m.cache["radio/Browse"]; ok {
		t.Fatal("esc reset retained the queried Browse cache")
	}
	m = run(m, cmd)
	if m.loading != false || m.title != "Popular Worldwide" || len(m.items) != 1 {
		t.Fatalf("popular reload = title=%q loading=%v items=%d", m.title, m.loading, len(m.items))
	}

	// A local filter peels off first; only the next esc resets the query.
	m.browseQuery = radioDiscovery{Term: "city pop"}
	m.filter, m.loading = "jazz", false
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filter != "" || m.browseQuery != (radioDiscovery{Term: "city pop"}) || cmd != nil {
		t.Fatalf("esc should clear the local filter first: filter=%q query=%#v", m.filter, m.browseQuery)
	}
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd == nil || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" {
		t.Fatalf("second esc should reset the query: query=%#v title=%q", m.browseQuery, m.title)
	}

	// Without a query esc stays inert on Browse.
	m = run(m, cmd)
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd != nil || m.view != "Browse" || m.title != "Popular Worldwide" {
		t.Fatalf("esc should be inert without a query: view=%q title=%q", m.view, m.title)
	}
	if footer := m.footerLine(120); strings.Contains(footer, "esc popular") {
		t.Fatalf("default Browse should not claim esc popular: %q", footer)
	}
}

func TestRadioBrowseFooterShowsPopularHintWithQuery(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.browseQuery = radioDiscovery{Term: "city pop"}
	if footer := m.footerLine(120); !strings.Contains(footer, "esc popular") {
		t.Fatalf("footer missing reset hint: %q", footer)
	}
}

func TestDiscoveryMenuTabCyclesThroughFields(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay = "radio", "discovery"
	for want := 1; want <= 6; want++ {
		next, _ := m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(Model)
		if m.discoverySelected != want {
			t.Fatalf("tab = %d, want %d", m.discoverySelected, want)
		}
	}
	next, _ := m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.discoverySelected != 0 {
		t.Fatalf("tab should wrap to Text, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(Model)
	if m.discoverySelected != 6 {
		t.Fatalf("shift+tab should wrap to Cancel, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(Model)
	if m.discoverySelected != 5 {
		t.Fatalf("shift+tab = %d, want 5", m.discoverySelected)
	}
}

func TestThemeTabMovesThroughThemes(t *testing.T) {
	m, _, _ := newModel(t)
	m.overlay, m.themeNames, m.themeIndex = "theme", []string{"one", "two"}, 0
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.themeIndex != 1 {
		t.Fatalf("theme tab = %d, want 1", m.themeIndex)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(Model)
	if m.themeIndex != 0 {
		t.Fatalf("theme shift+tab = %d, want 0", m.themeIndex)
	}
}

func TestRadioDefaultsToFavoritesOnEntryAndSourceSwitch(t *testing.T) {
	store := &state.Store{}
	radioModel := New(Options{Provider: &fake{}, Player: &fake{}, Radio: &fakeRadio{}, Store: store, Source: "radio"})
	if radioModel.view != "Favorites" {
		t.Fatalf("initial Radio view = %q, want Favorites", radioModel.view)
	}
	m, _, _ := newModel(t)
	m.lastView["radio"] = "Browse"
	next, _ := m.switchSource("radio")
	m = next.(Model)
	if m.view != "Favorites" || m.lastView["radio"] != "Favorites" {
		t.Fatalf("Radio switch view = %q, last view = %q; want Favorites", m.view, m.lastView["radio"])
	}
}

func mouseClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y}
}

func mouseWheel(down int, x, y int) tea.MouseMsg {
	button := tea.MouseButtonWheelUp
	if down == 1 {
		button = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: button, X: x, Y: y}
}

func TestMouseClickSelectsAndActivatesMainList(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.items = []core.Item{
		{Kind: "playlist", ID: "p1", Title: "One"},
		{Kind: "playlist", ID: "p2", Title: "Two"},
	}
	y := m.layout().listTop + 2
	next, _ := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	next, cmd := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatalf("second click should activate: cmd=%v loading=%v", cmd != nil, m.loading)
	}
}

func TestMouseWheelFocusesPanelAndScrolls(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}, {Title: "D"}, {Title: "E"}}}
	l := m.layout()
	if !l.showPanel {
		t.Fatal("panel expected")
	}
	next, _ := m.handleMouse(mouseWheel(1, l.mainWidth+2, l.listTop+2))
	m = next.(Model)
	if !m.queueFocus || m.queueCursor != 3 {
		t.Fatalf("focus=%v cursor=%d, want focused cursor 3", m.queueFocus, m.queueCursor)
	}
}

func TestMouseClickPanelSelectsAndJumps(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 120, 30
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m.state = f.state
	l := m.layout()
	x, y := l.mainWidth+3, l.listTop+3
	next, _ := m.handleMouse(mouseClick(x, y))
	m = next.(Model)
	if !m.queueFocus || m.queueCursor != 2 {
		t.Fatalf("focus=%v cursor=%d, want focused cursor 2", m.queueFocus, m.queueCursor)
	}
	next, cmd := m.handleMouse(mouseClick(x, y))
	m = next.(Model)
	m = run(m, cmd)
	if f.queueJumps != 1 {
		t.Fatalf("queue jumps = %d, want 1", f.queueJumps)
	}
}

func TestMouseWheelOnMainUnfocusesPanel(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	m.queueFocus = true
	m.title = "Playlists"
	m.items = []core.Item{{Title: "One"}, {Title: "Two"}, {Title: "Three"}}
	next, _ := m.handleMouse(mouseWheel(1, 5, m.layout().listTop+1))
	m = next.(Model)
	if m.queueFocus {
		t.Fatal("wheel over the main list should unfocus the panel")
	}
	if m.selected == 0 {
		t.Fatal("wheel did not move the main selection")
	}
}

func TestMouseClickViewTab(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	x := -1
	for candidate := 0; candidate < 120; candidate++ {
		if index, ok := m.viewTabAt(candidate); ok && index == 2 {
			x = candidate
			break
		}
	}
	if x < 0 {
		t.Fatal("view tab not found")
	}
	next, _ := m.handleMouse(mouseClick(x, 1))
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("view = %q, want Recent", m.view)
	}
}

func TestAddStreamURLFavoritesAndPlays(t *testing.T) {
	m, f, store := newModel(t)
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
	if len(store.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorites = %#v", store.FavoritesFor("radio"))
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
	view := m.View()
	if !strings.Contains(view, "access denied") {
		t.Fatalf("account hint missing:\n%s", view)
	}
	if !strings.Contains(m.emptyText(), "access denied") {
		t.Fatalf("empty text = %q", m.emptyText())
	}
	m.overlay = "info"
	info := m.View()
	if !strings.Contains(info, "Auth") || !strings.Contains(info, "denied") {
		t.Fatalf("info overlay missing auth:\n%s", info)
	}
}

func TestAccountHintHiddenWhenReady(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	if view := m.View(); strings.Contains(view, "Account:") {
		t.Fatalf("unexpected account hint:\n%s", view)
	}
}

func TestRadioItemsHaveNoRedundantKindGlyph(t *testing.T) {
	if got := kindGlyph("stream"); got != "" {
		t.Fatalf("stream glyph = %q", got)
	}
	if got := kindGlyph("station"); got != "" {
		t.Fatalf("station glyph = %q", got)
	}
}

func TestRadioFavoriteStateAppearsInListNowPlayingAndFooter(t *testing.T) {
	m, _, store := newModel(t)
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	m.source, m.view, m.title = "radio", "Recent", "Recent"
	m.items = []core.Item{station}
	m.state = core.PlaybackState{IsLive: true, Status: "playing", Track: &station}
	m.width, m.height = 120, 30

	view := m.View()
	if strings.Contains(view, "☆") || !strings.Contains(view, "  Example FM") || !strings.Contains(view, "f favorite") {
		t.Fatalf("unfavorited station state missing:\n%s", view)
	}

	store.ToggleFavorite("radio", station)
	view = m.View()
	lines := m.listLines(60, 10)
	if len(lines) != 1 || !strings.Contains(lines[0], "Example FM ★") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("favorited station state missing:\n%s", view)
	}
}

func TestRadioFavoritesRootOmitsRedundantFavoriteIcon(t *testing.T) {
	m, _, store := newModel(t)
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	store.ToggleFavorite("radio", station)
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items, m.width, m.height = []core.Item{station}, 100, 24
	view := m.View()
	if strings.Contains(view, "★") || !strings.Contains(view, "Example FM") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("Favorites root has redundant marker or missing action:\n%s", view)
	}

	m.history = []page{{source: "radio", view: "Favorites", title: "Favorites"}}
	if view := m.View(); !strings.Contains(view, "★") || strings.Contains(view, "★ Example FM") {
		t.Fatalf("temporary result page lost favorite state:\n%s", view)
	}
}

func TestDefaultThemeIsGruvbox(t *testing.T) {
	m, _, _ := newModel(t)
	if m.themeName != "gruvbox" {
		t.Fatalf("default theme = %q, want gruvbox", m.themeName)
	}
}

func TestAppleMusicViewsStartWithHome(t *testing.T) {
	if got := strings.Join(amViews, ","); got != "Home,Playlists,Recent,Presets" {
		t.Fatalf("views = %q", got)
	}
	m, _, _ := newModel(t)
	if m.view != "Home" || m.title != "Home" {
		t.Fatalf("default = %q / %q", m.view, m.title)
	}
}

func TestHomeSectionsOmitEmptyAndContinueOpensQueue(t *testing.T) {
	m, _, store := newModel(t)
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}, Track: &core.Item{Title: "B"}}
	store.AddRecentContainer(core.Item{Kind: "playlist", ID: "p1", Title: "Morning"})
	items := homeItems(m.state, "Mix", nil, nil, nil, store.RecentContainers)
	if len(items) != 4 || items[0].Title != "Continue Playing" || items[1].Kind != "continue" || items[2].Title != "Recently Played" || items[3].Kind != "playlist" {
		t.Fatalf("home items = %#v", items)
	}
	m.items, m.selected = items, 1
	next, cmd := m.activate()
	m = next.(Model)
	if cmd != nil || !m.queueFocus || m.queueCursor != 1 || m.selected != 1 {
		t.Fatalf("continue = focus=%v cursor=%d selected=%d", m.queueFocus, m.queueCursor, m.selected)
	}
	if items := homeItems(core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil); len(items) != 0 {
		t.Fatalf("empty home = %#v", items)
	}
}

func TestHomeSectionsAreSummaries(t *testing.T) {
	many := make([]core.Item, 12)
	for i := range many {
		many[i] = core.Item{Kind: "song", ID: fmt.Sprint(i), Title: fmt.Sprintf("Item %d", i)}
	}
	containers := make([]state.RecentContainer, 5)
	for i := range containers {
		containers[i] = state.RecentContainer{ID: fmt.Sprintf("playlist:p%d", i), Kind: "playlist", Title: fmt.Sprintf("Playlist %d", i)}
	}
	items := homeItems(core.PlaybackState{Status: "stopped"}, "", many, many, many, containers)
	counts := map[string]int{}
	section := ""
	for _, item := range items {
		if item.Kind == "header" {
			section = item.Title
			continue
		}
		counts[section]++
	}
	for _, section := range []string{"Recently Played", "Quick Start", "Your Playlists"} {
		if counts[section] != 8 {
			t.Fatalf("%s count = %d, want 8", section, counts[section])
		}
	}
}

func TestHomeRecentContainerOpensDetailWithoutPlaying(t *testing.T) {
	m, f, _ := newModel(t)
	m.items = homeItems(core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, []state.RecentContainer{{ID: "playlist:p1", Kind: "playlist", Title: "Road"}})
	m.selected = 1
	next, cmd := m.activate()
	m = next.(Model)
	if cmd == nil || m.detailKind != "playlist" || m.detailID != "p1" || m.title != "Road" || f.played.Kind != "" {
		t.Fatalf("opened=%q/%q title=%q played=%#v", m.detailKind, m.detailID, m.title, f.played)
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

func TestRadioPlay(t *testing.T) {
	m, f, _ := newModel(t)
	m.source = "radio"
	m.view = "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.radioURL != "https://radio.example/lofi" || !m.state.IsLive {
		t.Fatalf("radio not played: url=%q live=%v", f.radioURL, m.state.IsLive)
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

func TestSpaceStartsSelectedStationWhenNothingPlaying(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	m.selected = 0
	m.state = core.PlaybackState{Status: "stopped"}
	next, cmd := m.handleKey(runeKey(' '))
	m = next.(Model)
	m = run(m, cmd)
	if f.radioURL != "https://radio.example/lofi" || !m.state.IsLive {
		t.Fatalf("space did not start selected station: url=%q live=%v", f.radioURL, m.state.IsLive)
	}
}

func TestFavoriteToggle(t *testing.T) {
	m, _, store := newModel(t)
	m.source = "radio"
	m.view = "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	next, _ := m.toggleFavorite()
	m = next.(Model)
	if len(store.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorite not stored: %#v", store.FavoritesFor("radio"))
	}
	if len(m.items) != 1 || m.items[0].Title != "lofi" {
		t.Fatalf("favorite view not refreshed after add: %#v", m.items)
	}

	next, _ = m.toggleFavorite()
	m = next.(Model)
	if len(store.FavoritesFor("radio")) != 0 {
		t.Fatalf("favorite not removed: %#v", store.FavoritesFor("radio"))
	}
	if len(m.items) != 0 {
		t.Fatalf("favorite view not refreshed after removal: %#v", m.items)
	}
}

func TestFavoriteOutsideFavoritesInvalidatesCache(t *testing.T) {
	m, _, store := newModel(t)
	m.source, m.view = "radio", "Favorites"
	m.cache["radio/Favorites"] = nil
	m.view = "Browse"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/lofi", Title: "lofi"}}
	m.selected = 0

	next, _ := m.toggleFavorite()
	m = next.(Model)
	if len(store.FavoritesFor("radio")) != 1 {
		t.Fatalf("favorite not stored: %#v", store.FavoritesFor("radio"))
	}
	if _, cached := m.cache["radio/Favorites"]; cached {
		t.Fatal("toggle retained the stale Favorites cache")
	}

	m.view = "Favorites"
	m.loading = true
	m = run(m, m.loadView())
	if m.loading != false || len(m.items) != 1 || m.items[0].Title != "lofi" {
		t.Fatalf("Favorites served stale cache: %#v", m.items)
	}
}

func TestHelpOverlay(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey('?'))
	m = next.(Model)
	view := m.View()
	if m.overlay != "help" || !strings.Contains(view, "switch source") || !strings.Contains(view, "remove the focused Up Next track") || !strings.Contains(view, "any other key closes · q quit") {
		t.Fatal(m.View())
	}
}

func TestOverlayQQuitsAndOtherKeysClose(t *testing.T) {
	for _, overlay := range []string{"help", "info"} {
		t.Run(overlay, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.overlay = overlay
			next, cmd := m.handleKey(runeKey('q'))
			if next.(Model).overlay != overlay || cmd == nil {
				t.Fatalf("q = overlay %q cmd=%v", next.(Model).overlay, cmd != nil)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("q did not quit")
			}
			next, cmd = m.handleKey(runeKey('x'))
			if next.(Model).overlay != "" || cmd != nil {
				t.Fatalf("ordinary key = overlay %q cmd=%v", next.(Model).overlay, cmd != nil)
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

func TestKeyboardPolicyHints(t *testing.T) {
	m, _, _ := newModel(t)
	m.overlay, m.discoverySelected = "discovery", 0
	if view := m.overlayView(80, 20); !strings.Contains(view, "Type to search (j/k included) · Enter edit · ↑↓/Tab move · Esc cancel") {
		t.Fatalf("text hint missing:\n%s", view)
	}
	m.overlay, m.discoveryKind = "discovery-options", "language"
	m.discoveryOptions = []core.Item{{Title: "Any"}}
	if view := m.overlayView(80, 20); !strings.Contains(view, "Typing filters (j/k included) · ↑↓ move") {
		t.Fatalf("option hint missing:\n%s", view)
	}
	m.overlay, m.themeNames, m.themeIndex = "theme", []string{"one", "two"}, 0
	if view := m.overlayView(80, 20); !strings.Contains(view, "j/k, ↑↓ or Tab preview · Enter save") || !strings.Contains(view, "Esc cancel · q quit") {
		t.Fatalf("theme hints missing:\n%s", view)
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

func TestTinyTerminalSuppressesLatentKeyActions(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 20, 20
	m.source, m.selected, m.filter = "apple-music", 1, "kept"
	m.items = []core.Item{{Title: "One"}, {Title: "Two"}}
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}}}
	for _, key := range []tea.KeyMsg{runeKey('j'), runeKey('0'), runeKey('/'), runeKey('x'), tea.KeyMsg{Type: tea.KeyEnter}} {
		next, cmd := m.handleKey(key)
		m = next.(Model)
		if cmd != nil || m.source != "apple-music" || m.selected != 1 || m.filter != "kept" || m.queueFocus || m.input.Focused() || m.overlay != "" {
			t.Fatalf("%q mutated latent state: %#v", key.String(), m)
		}
	}
	for _, key := range []tea.KeyMsg{runeKey('q'), tea.KeyMsg{Type: tea.KeyCtrlC}} {
		_, cmd := m.handleKey(key)
		if cmd == nil {
			t.Fatalf("%q did not quit", key.String())
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := next.(Model); got.width != 100 || got.height != 30 {
		t.Fatalf("resize ignored: %dx%d", got.width, got.height)
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
	view := m.View()
	if !strings.Contains(view, "SOURCE") || !strings.Contains(view, "VIEW") {
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
	view := m.View()
	if !strings.Contains(view, "Playlists (1)") || !strings.Contains(view, "Up Next ·") {
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
	if strings.Contains(m.View(), "┌─ Up Next") {
		t.Fatal("narrow view should not show a side panel before focus")
	}
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if !strings.Contains(m.View(), "┌─ Up Next") || !strings.Contains(m.View(), "Queued") {
		t.Fatalf("narrow focused queue missing:\n%s", m.View())
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
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
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
	view := m.View()
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
	m = run(m, m.playPlaylist(false))
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
	m = run(m, m.playSelected())
	if m.queueSource != (queueContext{}) {
		t.Fatalf("queue source after stream = %#v", m.queueSource)
	}
}

func TestNowLinesShowQueueSourceAndPosition(t *testing.T) {
	m, _, _ := newModel(t)
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Shuffle: true, QueueIndex: 1,
		Track: &core.Item{ID: "2", Title: "B"}, Queue: []core.Item{{ID: "1", Title: "A"}, {ID: "2", Title: "B"}, {ID: "3", Title: "C"}}}
	got := strings.Join(m.nowLines(120, 8), "\n")
	for _, want := range []string{"From: Morning", "2/3", "shuffle", "0 Up Next"} {
		if !strings.Contains(got, want) {
			t.Fatalf("now lines missing %q:\n%s", want, got)
		}
	}
}

func TestQueueTitleIncludesSourceAndPosition(t *testing.T) {
	m, _, _ := newModel(t)
	m.loading = false
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{QueueIndex: 1, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	if got := m.queueTitle(); got != "Up Next · 2/3 · Morning" {
		t.Fatalf("title = %q", got)
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

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
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
	lines := m.listLines(120, 5)
	if len(lines) < 2 || !strings.Contains(lines[1], "▶") {
		t.Fatalf("current playlist row not marked: %#v", lines)
	}
}

func TestStaleListResultIsIgnoredAndDoesNotClearLoading(t *testing.T) {
	m, _, _ := newModel(t)
	m.view = "Recent"
	m.generation = 2
	m.loading = true
	next, _ := m.Update(listMsg{generation: 1, destination: "apple-music|Playlists|||0", key: "apple-music/Playlists", title: "Playlists", items: []core.Item{{Title: "P"}}})
	m = next.(Model)
	if len(m.cache["apple-music/Playlists"]) != 0 || len(m.items) != 0 || !m.loading {
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
	if f.played.Kind != "playlist" || f.played.StartTrackID != "s1" || f.played.StartTitle != "Track One" || f.played.StartAt != 1 {
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
	if f.played.ID != "p1" || f.played.StartTrackID != "s1" {
		t.Fatalf("restored playback semantics lost: %#v", f.played)
	}
}

func TestStateStreamClosureStopsInterpolationAndShowsRestart(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Position: 10, Duration: 100}
	m.snapshotAt = time.Now().Add(-time.Second)
	next, _ := m.Update(stateUpdatesClosedMsg{})
	m = next.(Model)
	if m.state.Status != "disconnected" || !m.snapshotAt.IsZero() || !strings.Contains(m.message, "restart lilt") {
		t.Fatalf("disconnect state=%#v message=%q", m.state, m.message)
	}
	if got := m.displayPositionAt(time.Now().Add(time.Minute)); got != 10 {
		t.Fatalf("disconnected interpolation = %v", got)
	}
}

func TestTinyTerminalAndOverlayNeverOverflow(t *testing.T) {
	m, _, _ := newModel(t)
	for _, size := range [][2]int{{1, 1}, {10, 3}, {23, 7}, {24, 8}} {
		m.width, m.height = size[0], size[1]
		m.overlay = "help"
		view := m.View()
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
	view := m.View()
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\u009b") {
		t.Fatalf("terminal controls rendered: %q", view)
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
	next, cmd = m.handleKey(runeKey('s'))
	m = next.(Model)
	m = run(m, cmd)
	if !f.state.Shuffle || f.played.Kind != "playlist" {
		t.Fatalf("shuffle play state=%#v request=%#v", f.state, f.played)
	}
	next, cmd = m.handleKey(runeKey('p'))
	m = next.(Model)
	m = run(m, cmd)
	if f.state.Shuffle {
		t.Fatal("ordered play must turn shuffle off")
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
	run(m, m.playPlaylist(false))
	if !f.played.Reverse {
		t.Fatal("Favorite Songs request did not set Reverse")
	}

	m.title = "Regular playlist"
	run(m, m.playPlaylist(false))
	if f.played.Reverse {
		t.Fatal("regular playlist request set Reverse")
	}
}

func TestFooterUsesPageContext(t *testing.T) {
	m, _, _ := newModel(t)
	if footer := m.footerLine(200); !strings.Contains(footer, "p play") || !strings.Contains(footer, "Tab source") || !strings.Contains(footer, "1-9 view") {
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
	next, _ := m.Update(stateChangedMsg{update: core.PlaybackStateUpdate{Sequence: 6, State: core.PlaybackState{Status: "playing", Position: 6}}})
	m = next.(Model)
	if m.sequence != 6 || m.state.Position != 6 {
		t.Fatalf("new notification not applied: sequence=%d state=%#v", m.sequence, m.state)
	}
	next, _ = m.Update(stateChangedMsg{update: core.PlaybackStateUpdate{Sequence: 5, State: core.PlaybackState{Status: "paused", Position: 5}}})
	m = next.(Model)
	if m.sequence != 6 || m.state.Position != 6 {
		t.Fatal("older notification overwrote canonical state")
	}
	next, _ = m.Update(actionMsg{afterSequence: 4, state: core.PlaybackState{Status: "paused", Position: 4}})
	m = next.(Model)
	if m.state.Position != 6 {
		t.Fatal("delayed command response overwrote a newer notification")
	}
	next, _ = m.Update(actionMsg{afterSequence: 6, state: core.PlaybackState{Status: "paused", Position: 7}})
	m = next.(Model)
	if m.state.Position != 7 {
		t.Fatal("current command response did not update immediately")
	}
}

func TestNewerActionRejectsStaleActionMetadata(t *testing.T) {
	m, _, store := newModel(t)
	oldItem := core.Item{Kind: "song", ID: "old", Title: "Old"}
	newItem := core.Item{Kind: "song", ID: "new", Title: "New"}
	m.actionClock.Store(12)
	next, _ := m.Update(actionMsg{actionID: 12, afterSequence: m.sequence, state: core.PlaybackState{Status: "playing", Mode: "full"}, queueContext: &queueContext{Kind: "playlist", ID: "new"}, recentSource: "apple-music", recentItem: &newItem, presetKey: "focus", presetItem: &newItem})
	m = next.(Model)
	next, _ = m.Update(actionMsg{actionID: 11, afterSequence: m.sequence, state: core.PlaybackState{Status: "paused"}, queueContext: &queueContext{Kind: "playlist", ID: "old"}, recentSource: "apple-music", recentItem: &oldItem, presetKey: "focus", presetItem: &oldItem})
	m = next.(Model)
	if m.queueSource.ID != "new" || m.state.Status != "playing" {
		t.Fatalf("stale action changed metadata/state: queue=%#v state=%#v", m.queueSource, m.state)
	}
	if len(store.Recent) != 1 || store.Recent[0].ID != "am:new" || store.Presets["focus"].Last != "new" {
		t.Fatalf("stale action changed persisted metadata: %#v %#v", store.Recent, store.Presets)
	}
}

func TestOverlappingCommandsUseStartOrderForMetadataFreshness(t *testing.T) {
	m, _, store := newModel(t)
	oldItem := core.Item{Kind: "song", ID: "old", Title: "Old"}
	newItem := core.Item{Kind: "song", ID: "new", Title: "New"}

	oldWork := m.playItem(oldItem)
	newWork := m.playItem(newItem)

	// Complete the newer operation first, then deliver the older late result.
	next, _ := m.Update(newWork())
	m = next.(Model)
	next, _ = m.Update(oldWork())
	m = next.(Model)
	if m.state.Track == nil || m.state.Track.Title != "One" || len(store.Recent) != 1 || store.Recent[0].ID != "am:new" {
		t.Fatalf("late action changed authoritative metadata: state=%#v recent=%#v", m.state, store.Recent)
	}
}

func TestNewerNotificationSkipsStaleStateButKeepsCompletedMetadata(t *testing.T) {
	m, _, store := newModel(t)
	m.sequence = 9
	m.state = core.PlaybackState{Status: "playing", Position: 42, Track: &core.Item{Kind: "song", ID: "current", Title: "Current"}}
	m.queueSource = queueContext{Kind: "playlist", ID: "current"}
	item := core.Item{Kind: "stream", URL: "https://radio.example/late", Title: "Late"}
	next, _ := m.Update(actionMsg{afterSequence: 8, state: core.PlaybackState{Status: "paused"}, queueContext: &queueContext{Kind: "playlist", ID: "stale"}, recentSource: "radio", recentItem: &item, addFavorite: true, refreshView: false})
	m = next.(Model)
	if m.queueSource.ID != "current" || m.state.Status != "playing" || m.state.Position != 42 {
		t.Fatalf("sequence-stale state applied: queue=%#v state=%#v", m.queueSource, m.state)
	}
	if len(store.Recent) != 1 || store.Recent[0].ID != "radio:https://radio.example/late" {
		t.Fatalf("completed action metadata dropped: %#v", store.Recent)
	}
	if len(store.FavoritesFor("radio")) != 1 {
		t.Fatalf("completed auto-favorite dropped: %#v", store.FavoritesFor("radio"))
	}
}

func TestFilteredPlaylistSelectionUsesOriginalIndex(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID, m.title = "playlist", "p1", "Road"
	m.items = []core.Item{{Kind: "song", ID: "a", Title: "Alpha"}, {Kind: "song", ID: "b", Title: "Beta"}, {Kind: "song", ID: "c", Title: "Gamma"}}
	m.filter = "gamma"
	m.selected = 0
	m = run(m, m.playPlaylistFrom(m.visibleItems()[0]))
	if f.played.StartAt != 2 || f.played.StartTrackID != "c" {
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

func TestTabFromInputSwitchesSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Playlists"
	next, _ := m.handleKey(runeKey('/'))
	m = next.(Model)
	if !m.input.Focused() {
		t.Fatal("input not focused after /")
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.input.Focused() || m.source != "radio" {
		t.Fatalf("tab did not switch source: source=%q focused=%v", m.source, m.input.Focused())
	}
}

func TestDigitSelectsView(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Playlists"
	next, _ := m.handleKey(runeKey('3'))
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("view = %q, want Recent", m.view)
	}
	next, _ = m.handleKey(runeKey('2'))
	m = next.(Model)
	if m.view != "Playlists" {
		t.Fatalf("view = %q, want Playlists", m.view)
	}
}

func TestAppleMusicRemembersLastViewAndRadioDefaultsToFavorites(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Playlists"
	next, _ := m.selectView(3)
	m = next.(Model)
	if m.view != "Presets" {
		t.Fatalf("selectView(3) = %q, want Presets", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	next, _ = m.selectView(2)
	m = next.(Model)
	if m.view != "Browse" {
		t.Fatalf("radio selectView(2) = %q, want Browse", m.view)
	}
	next, _ = m.switchSource("apple-music")
	m = next.(Model)
	if m.view != "Presets" {
		t.Fatalf("apple music did not remember Presets: %q", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	if m.view != "Favorites" {
		t.Fatalf("radio did not default to Favorites: %q", m.view)
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
