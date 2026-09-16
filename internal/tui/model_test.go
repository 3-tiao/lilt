package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/charmbracelet/x/ansi"
)

// plainText strips styling so assertions can read the UI as a user would.
func plainText(s string) string { return ansi.Strip(s) }

type fake struct {
	state       core.PlaybackState
	played      core.PlaybackRequest
	radioURL    string
	tracks      []core.Item
	stateCalls  int
	queueJumps  int
	probed      []string
	probeResult core.RadioProbeResult
	probeErr    error
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

func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
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

func TestInitialLocalRadioViewSkipsLoadingAndListCommand(t *testing.T) {
	f := &fake{}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	store.ToggleFavorite("radio", core.Item{Kind: "stream", URL: "https://example.test/live", Title: "Local Radio"})
	m := New(Options{
		Provider:      f,
		Player:        f,
		Radio:         fakeRadio{},
		Store:         store,
		Authorization: core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready"},
		Source:        "radio",
	})
	if m.view != "Favorites" || m.loading || len(m.items) != 1 {
		t.Fatalf("initial local view = view=%q loading=%v items=%d", m.view, m.loading, len(m.items))
	}
	if _, ok := m.Init()().(tickMsg); !ok {
		t.Fatalf("local Init command = %#v, want only tick", m.Init()())
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
		if size[0] >= 88 && !strings.Contains(view, "UP NEXT ·") {
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
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
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
	m.busy = true
	lines := strings.Join(m.nowLines(110, 10), "\n")
	if !strings.Contains(lines, "Starting · Apple Music") || strings.Contains(lines, "working…") {
		t.Fatalf("startup transient not collapsed:\n%s", lines)
	}

	m.state.Position = 42
	lines = strings.Join(m.nowLines(110, 10), "\n")
	if !strings.Contains(lines, "Paused · Apple Music") || !strings.Contains(lines, "working…") {
		t.Fatalf("mid-track pause should stay paused:\n%s", lines)
	}

	// MusicKit often reports paused at position 0 right after the play
	// response, when busy has already cleared.
	m.busy = false
	m.state.Position = 0
	lines = strings.Join(m.nowLines(110, 10), "\n")
	if !strings.Contains(lines, "Starting · Apple Music") {
		t.Fatalf("post-response startup transient should read as starting:\n%s", lines)
	}
}

func TestEmptyStateHints(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.view = "Favorites"
	m.title = "Favorites"
	m.loading = false
	m.items = nil
	if view := plainText(m.View().Content); !strings.Contains(view, "press a to add a stream URL") {
		t.Fatalf("empty hint missing:\n%s", view)
	}
}

func TestListLoadingRefreshAndErrorRendering(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 24
	m.loading = true
	if title := m.listTitle(); !strings.Contains(title, "loading…") {
		t.Fatalf("initial title = %q", title)
	}
	if lines := strings.Join(m.listLines(80, 10), "\n"); !strings.Contains(lines, "loading…") {
		t.Fatalf("initial loading body = %q", lines)
	}
	m.items = []core.Item{{Kind: "song", ID: "1", Title: "Ready"}}
	if title := m.listTitle(); !strings.Contains(title, "refreshing…") || strings.Contains(title, "loading…") {
		t.Fatalf("refresh title = %q", title)
	}
	if lines := strings.Join(m.listLines(80, 10), "\n"); !strings.Contains(lines, "Ready") {
		t.Fatalf("refresh hid list = %q", lines)
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
	if title := m.nowTitle(); title != "Now Playing · buffering…" {
		t.Fatalf("buffering title = %q", title)
	}
	lines := strings.Join(m.nowLines(80, 10), "\n")
	if !strings.Contains(lines, "Buffering… · Apple Music") {
		t.Fatalf("buffering state line = %q", lines)
	}
}

func TestLocalViewsDoNotEnterLoadingState(t *testing.T) {
	m, _, _ := newModel(t)
	next, cmd := m.selectView(2) // Apple Music Favorites
	local := next.(Model)
	if cmd != nil || local.loading || local.title != "Favorites · local" {
		t.Fatalf("local Apple view = loading=%v title=%q cmd=%v", local.loading, local.title, cmd != nil)
	}
	m.source, m.view = "radio", "Browse"
	next, cmd = m.selectView(1) // Radio Recent
	local = next.(Model)
	if cmd != nil || local.loading || local.view != "Recent" {
		t.Fatalf("local Radio view = loading=%v view=%q cmd=%v", local.loading, local.view, cmd != nil)
	}
}

func TestAsyncActionEntryPathsMarkBusy(t *testing.T) {
	for _, key := range []rune{'s', 'R', 'e', 'E'} {
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
	lines := plainText(strings.Join(m.listLines(60, 10), "\n"))
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
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.overlay != "" || m.discoveryPending != (radioDiscovery{}) {
		t.Fatalf("cancel retained filter: %#v", m.discoveryPending)
	}
	m.view = "Browse"
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	m.cache["radio/Browse"] = []core.Item{{Kind: "stream", Title: "stale"}}
	m.discoveryPending = radioDiscovery{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}
	m.discoverySelected = discoveryConfirm
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if m.overlay != "input" || m.inputMode != "search" || !m.input.Focused() {
		t.Fatalf("Apple slash changed: overlay=%q mode=%q focused=%v", m.overlay, m.inputMode, m.input.Focused())
	}
	m = m.closeTextInput()
	next, _ = m.handleKey(runeKey('F'))
	m = next.(Model)
	if m.overlay != "input" || m.inputMode != "filter" || !m.input.Focused() {
		t.Fatalf("Apple F changed: overlay=%q mode=%q focused=%v", m.overlay, m.inputMode, m.input.Focused())
	}
}

func TestTextTasksUseCentralInputOverlay(t *testing.T) {
	for _, test := range []struct {
		name, source, mode, title, hint string
		key                             tea.KeyPressMsg
	}{
		{"radio URL", "radio", "url", "Add Radio URL", "Enter add & play", runeKey('a')},
		{"Apple search", "apple-music", "search", "Search Apple Music", "Enter search", runeKey('/')},
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

func TestBrowseResetFiltersAndStaleOptions(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.overlay, m.generation = "radio", "Browse", "discovery-options", 4
	m.discoveryKind = "genre"
	next, _ := m.Update(discoveryOptionsMsg{generation: 3, kind: "genre", values: []core.Item{{Title: "stale"}}})
	m = next.(Model)
	if len(m.discoveryOptions) != 0 {
		t.Fatalf("stale options applied: %#v", m.discoveryOptions)
	}
	m.overlay, m.discoverySelected = "discovery", discoveryReset
	m.discoveryPending = radioDiscovery{Language: "Japanese", Tag: "City Pop"}
	m.discoveryTerm = "東京"
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.overlay != "discovery" || m.discoveryPending != (radioDiscovery{}) || m.discoveryTerm != "東京" {
		t.Fatalf("reset = pending=%#v text=%q", m.discoveryPending, m.discoveryTerm)
	}
	view := m.overlayView(80, 20)
	for _, want := range []string{"Search", "Text", "東京", "Filters", "Language", "Genre", "Country", "Reset filters", `[ Search "東京" ]`, "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu missing %q:\n%s", want, view)
		}
	}
	if got := discoverySearchValue(""); got != "Not set" {
		t.Fatalf("empty search value = %q", got)
	}
	m.discoveryPending.Language, m.discoveryTerm, m.discoverySelected = "Korean", "Seoul", discoveryConfirm
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(Model)
	if m.discoverySelected != discoveryCancel {
		t.Fatalf("right action selection = %d, want Cancel", m.discoverySelected)
	}
	next, cmd = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	// Search text editor is a visible, normal text input and Esc discards edits.
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "discovery-text" || !m.input.Focused() || !strings.Contains(m.overlayView(80, 20), "Search text:") {
		t.Fatalf("text editor not visible: overlay=%q", m.overlay)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Text: "日本"})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.discoveryTerm != "" || m.overlay != "discovery" {
		t.Fatalf("text cancel applied %q / %q", m.discoveryTerm, m.overlay)
	}
	// Commit Unicode text, choose a facet, then apply the query to Browse.
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Text: "東京"})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	m.discoveryPending.Language = "Japanese"
	m.discoverySelected = discoveryConfirm
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	m.discoverySelected = discoveryConfirm
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m.discoveryPending, m.discoveryTerm, m.discoverySelected = radioDiscovery{}, "", discoveryConfirm
	next, cmd = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if view := plainText(m.View().Content); !strings.Contains(view, "(no stations matched — press / to adjust the query)") {
		t.Fatalf("queried Browse empty state missing:\n%s", view)
	}
	m.browseQuery = radioDiscovery{}
	m.title = "Popular Worldwide"
	if view := plainText(m.View().Content); strings.Contains(view, "no stations matched") {
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
	if view := m.overlayView(80, 20); !strings.Contains(view, "loading…") {
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
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(Model)
	if m.discoveryQuery != "日" {
		t.Fatalf("unicode backspace = %q", m.discoveryQuery)
	}

	m.discoveryQuery, m.discoverySelected = "", 0
	m.discoveryPending = radioDiscovery{Language: "japanese", Tag: "city pop", CountryCode: "JP", CountryName: "Japan"}
	for _, kind := range []string{"language", "genre", "country"} {
		m.overlay, m.discoveryKind, m.discoverySelected = "discovery-options", kind, 0
		next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if msg.title != "Popular Worldwide" || len(msg.items) != 1 || r.popularLimit != radioPageSize || r.filter != (radio.Filter{}) {
		t.Fatalf("browse = %q %#v", msg.title, msg.items)
	}
	m.browseQuery = radioDiscovery{Term: "Tokyo"}
	msg = m.loadView()().(listMsg)
	if msg.title != "Showing: Tokyo" || len(msg.items) != 1 || r.searchLimit != radioPageSize || r.filter != (radio.Filter{}) {
		t.Fatalf("queried browse = %q %#v filter=%#v limit=%d", msg.title, msg.items, r.filter, r.searchLimit)
	}
	m.browseQuery = radioDiscovery{Language: "Japanese", CountryCode: "JP"}
	msg = m.loadView()().(listMsg)
	if msg.title != "Showing: Japanese · JP" || r.filter != (radio.Filter{Language: "Japanese", CountryCode: "JP"}) {
		t.Fatalf("facet browse = %q filter=%#v", msg.title, r.filter)
	}
}

func radioPageItems(start, count int) []core.Item {
	items := make([]core.Item, count)
	for i := range items {
		items[i] = core.Item{Kind: "stream", ID: fmt.Sprintf("station-%d", start+i), URL: fmt.Sprintf("https://radio.example/%d", start+i), Title: fmt.Sprintf("Station %d", start+i)}
	}
	return items
}

func TestRadioBrowseAppendDeduplicatesAndPreservesSelection(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items, m.loading = radioPageItems(0, radioPageSize), false
	m.selected, m.pageOffset, m.pageMore, m.pageLoading = 42, radioPageSize, true, true
	page := append([]core.Item{m.items[0]}, radioPageItems(radioPageSize, radioPageSize-1)...)
	next, _ := m.Update(listMsg{key: "radio/Browse", append: true, offset: radioPageSize, items: page})
	m = next.(Model)
	selected, _ := m.selectedItem()
	if len(m.items) != radioPageSize*2-1 || selected.Title != "Station 42" || m.pageOffset != radioPageSize*2 || !m.pageMore || m.pageLoading || m.pageFailed {
		t.Fatalf("append = items=%d selected=%q offset=%d more=%v loading=%v failed=%v", len(m.items), selected.Title, m.pageOffset, m.pageMore, m.pageLoading, m.pageFailed)
	}
}

func TestRadioBrowseMaybeLoadMoreGuardsAndShortPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items, m.loading = radioPageItems(0, radioPageSize), false
	m.selected, m.pageOffset, m.pageMore = radioPageSize-radioPageThreshold, radioPageSize, true
	if cmd := m.maybeLoadMore(); cmd == nil || !m.pageLoading {
		t.Fatal("near-end Browse should start an append")
	}
	for _, set := range []func(*Model){
		func(m *Model) { m.pageLoading = true },
		func(m *Model) { m.pageFailed = true },
		func(m *Model) { m.loading = true },
		func(m *Model) { m.busy = true },
		func(m *Model) { m.overlay = "help" },
		func(m *Model) { m.filter = "local" },
	} {
		blocked := m
		blocked.pageLoading, blocked.pageFailed, blocked.loading, blocked.busy, blocked.overlay, blocked.filter = false, false, false, false, "", ""
		set(&blocked)
		if cmd := blocked.maybeLoadMore(); cmd != nil {
			t.Fatal("guarded Browse started an append")
		}
	}
	m.pageLoading = true
	next, _ := m.Update(listMsg{key: "radio/Browse", append: true, items: radioPageItems(radioPageSize, 3)})
	m = next.(Model)
	if !m.pageMore {
		t.Fatal("a short but non-empty page must not end paging")
	}
	if m.pageOffset != radioPageSize*2 {
		t.Fatalf("append offset = %d, want the fixed %d stride", m.pageOffset, radioPageSize*2)
	}
	m.pageLoading = true
	next, _ = m.Update(listMsg{key: "radio/Browse", append: true, items: nil})
	m = next.(Model)
	if m.pageMore || m.maybeLoadMore() != nil {
		t.Fatal("an empty append page should stop paging")
	}
}

func TestRadioBrowsePagingResetsAndAppendFailureRetriesWithG(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.pageOffset, m.pageMore, m.pageLoading, m.pageFailed, m.pageKey = 100, true, true, true, "old"
	next, cmd := m.applyDiscoveryFilter(radioDiscovery{Term: "jazz"}, "jazz")
	m = next.(Model)
	if cmd == nil || m.pageOffset != 0 || !m.pageMore || m.pageLoading || m.pageFailed || m.pageKey != "Showing: jazz" {
		t.Fatalf("query reset = offset=%d more=%v loading=%v failed=%v key=%q", m.pageOffset, m.pageMore, m.pageLoading, m.pageFailed, m.pageKey)
	}
	m.items, m.loading, m.pageOffset, m.pageMore, m.pageLoading = radioPageItems(0, radioPageSize), false, radioPageSize, true, true
	next, _ = m.Update(listMsg{generation: m.generation, key: "radio/Browse", append: true, err: errors.New("offline")})
	m = next.(Model)
	if len(m.items) != radioPageSize || !m.pageFailed || m.pageLoading || m.maybeLoadMore() != nil || !strings.Contains(m.message, "press G to retry") {
		t.Fatalf("append failure = items=%d failed=%v loading=%v message=%q", len(m.items), m.pageFailed, m.pageLoading, m.message)
	}
	next, _ = m.Update(runeKey('G'))
	m = next.(Model)
	if m.pageFailed {
		t.Fatal("G should clear append failure")
	}
}

func TestRadioBrowseFullFirstPageContinues(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title, m.loading = "radio", "Browse", "Popular Worldwide", true
	next, _ := m.Update(listMsg{key: "radio/Browse", title: "Popular Worldwide", items: radioPageItems(0, radioPageSize)})
	m = next.(Model)
	if m.pageOffset != radioPageSize || !m.pageMore {
		t.Fatalf("first page = offset=%d more=%v", m.pageOffset, m.pageMore)
	}
}

func TestRadioBrowseErrorCanBeRetried(t *testing.T) {
	newErrored := func(t *testing.T) Model {
		t.Helper()
		m, _, _ := newModel(t)
		m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
		m.loading = false
		m.listErr = "Radio directory timed out"
		m.message, m.messageErr = "Radio directory timed out — press r to retry", true
		return m
	}

	// Pressing the view key again must retry instead of doing nothing.
	m := newErrored(t)
	before := m.generation
	next, cmd := m.selectView(2)
	retried := next.(Model)
	if cmd == nil || retried.generation == before || retried.listErr != "" || !retried.loading {
		t.Fatalf("3 retry = cmd=%v generation=%d->%d listErr=%q loading=%v", cmd != nil, before, retried.generation, retried.listErr, retried.loading)
	}

	// `r` is the explicit retry key on any view.
	m = newErrored(t)
	before = m.generation
	next, cmd = m.handleKey(runeKey('r'))
	retried = next.(Model)
	if cmd == nil || retried.generation == before || retried.listErr != "" || !retried.loading {
		t.Fatalf("r retry = cmd=%v generation=%d->%d listErr=%q loading=%v", cmd != nil, before, retried.generation, retried.listErr, retried.loading)
	}

	// A healthy view must stay a no-op so re-selecting does not reload.
	m, _, _ = newModel(t)
	m.source, m.view, m.loading = "radio", "Browse", false
	if _, cmd := m.selectView(2); cmd != nil {
		t.Fatal("re-selecting a healthy view should not reload")
	}
}

func TestRadioDirectoryErrorCopyDistinguishesTimeout(t *testing.T) {
	timeoutErr := &url.Error{Op: "Get", URL: "https://de1.api.radio-browser.info/json", Err: context.DeadlineExceeded}
	if !isTimeoutError(timeoutErr) {
		t.Fatal("client timeout should classify as a timeout")
	}
	if isTimeoutError(errors.New("HTTP 500")) {
		t.Fatal("a plain error is not a timeout")
	}

	for _, test := range []struct {
		name, wantMessage, wantListErr string
		err                            error
	}{
		{"timeout", "Radio directory timed out — press r to retry", "Radio directory timed out", timeoutErr},
		{"unavailable", "Radio directory unavailable — press r to retry", "Radio directory unavailable", errors.New("HTTP 500")},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.source, m.view, m.title, m.loading = "radio", "Browse", "Popular Worldwide", true
			next, _ := m.Update(listMsg{generation: m.generation, destination: m.destination(), key: "radio/Browse", title: "Popular Worldwide", err: test.err})
			got := next.(Model)
			if got.message != test.wantMessage || got.listErr != test.wantListErr {
				t.Fatalf("message=%q listErr=%q", got.message, got.listErr)
			}
			if !got.messageErr || strings.Contains(got.message, "check your connection") {
				t.Fatalf("copy still blames the connection: %q", got.message)
			}
		})
	}
}

func TestRadioDirectoryFailureFallsBackToCachedStations(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title, m.loading = "radio", "Browse", "Popular Worldwide", true
	m.radioCache.RememberItems([]core.Item{
		{Kind: "stream", ID: "a", URL: "https://radio.example/a", Title: "Cached A", Radio: &core.RadioMetadata{StationUUID: "a", ClickCount: 10}},
		{Kind: "stream", ID: "b", URL: "https://radio.example/b", Title: "Cached B", Radio: &core.RadioMetadata{StationUUID: "b", ClickCount: 20}},
	}, time.Now())

	next, _ := m.Update(listMsg{generation: m.generation, destination: m.destination(), key: "radio/Browse", title: "Popular Worldwide", err: errors.New("HTTP 500")})
	got := next.(Model)
	if len(got.items) != 2 || got.items[0].Title != "Cached B" {
		t.Fatalf("cache fallback items = %v", titlesOf(got.items))
	}
	if got.listErr != "" || !strings.Contains(got.message, "cached stations") {
		t.Fatalf("cache fallback copy = listErr=%q message=%q", got.listErr, got.message)
	}
	if got.pageMore {
		t.Fatal("cached fallback must not claim more pages")
	}

	// With no cached profile the screen still reports the real failure.
	empty, _, _ := newModel(t)
	empty.source, empty.view, empty.title, empty.loading = "radio", "Browse", "Popular Worldwide", true
	next, _ = empty.Update(listMsg{generation: empty.generation, destination: empty.destination(), key: "radio/Browse", title: "Popular Worldwide", err: errors.New("HTTP 500")})
	if bare := next.(Model); bare.listErr != "Radio directory unavailable" {
		t.Fatalf("no-cache error = %q", bare.listErr)
	}
}

func TestRadioBrowseEscRestoresPopularWorldwide(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Showing: city pop"
	m.browseQuery = radioDiscovery{Term: "city pop"}
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/a", Title: "A"}}
	m.selected = 0
	m.cache[m.browseCacheKey()] = m.items

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if cmd == nil || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" || m.loading != true || len(m.items) != 0 {
		t.Fatalf("esc reset = query=%#v title=%q loading=%v items=%d", m.browseQuery, m.title, m.loading, len(m.items))
	}
	for key := range m.cache {
		if strings.HasPrefix(key, "radio/Browse|") {
			t.Fatalf("esc reset retained the queried Browse cache: %q", key)
		}
	}
	m = run(m, cmd)
	if m.loading != false || m.title != "Popular Worldwide" || len(m.items) != 1 {
		t.Fatalf("popular reload = title=%q loading=%v items=%d", m.title, m.loading, len(m.items))
	}

	// A local filter peels off first; only the next esc resets the query.
	m.browseQuery = radioDiscovery{Term: "city pop"}
	m.filter, m.loading = "jazz", false
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.filter != "" || m.browseQuery != (radioDiscovery{Term: "city pop"}) || cmd != nil {
		t.Fatalf("esc should clear the local filter first: filter=%q query=%#v", m.filter, m.browseQuery)
	}
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if cmd == nil || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" {
		t.Fatalf("second esc should reset the query: query=%#v title=%q", m.browseQuery, m.title)
	}

	// Without a query esc stays inert on Browse.
	m = run(m, cmd)
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	for want := 1; want <= discoveryCancel; want++ {
		next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyTab})
		m = next.(Model)
		if m.discoverySelected != want {
			t.Fatalf("tab = %d, want %d", m.discoverySelected, want)
		}
	}
	next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.discoverySelected != 0 {
		t.Fatalf("tab should wrap to Text, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(Model)
	if m.discoverySelected != discoveryCancel {
		t.Fatalf("shift+tab should wrap to Cancel, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(Model)
	if m.discoverySelected != discoveryConfirm {
		t.Fatalf("shift+tab = %d, want Confirm", m.discoverySelected)
	}
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
	if line := m.sourceLine(100); !strings.Contains(line, "[Radio]") {
		t.Fatalf("active source marker missing: %q", line)
	}
	if line := m.viewLine(100); !strings.Contains(line, "[2 Recent]") {
		t.Fatalf("active view marker missing: %q", line)
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
	if !strings.Contains(m.message, "Tab switches source") {
		t.Fatalf("startup positioning missing: %q", m.message)
	}
	denied := New(Options{Provider: &fake{}, Player: &fake{}, Radio: fakeRadio{}, Store: &state.Store{}, Authorization: core.AuthorizationStatus{Status: "denied"}})
	if strings.Contains(denied.message, "Tab switches source") || denied.message != "" {
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

func TestRadioDirectoryFailureUsesFriendlyCopy(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.generation = 3
	m.loading = true
	next, _ := m.Update(listMsg{generation: 3, key: "radio/Browse", title: "Popular Worldwide", err: errors.New(`Get "https://de1.api.radio-browser.info/json/stations/topclick/20": context deadline exceeded`)})
	m = next.(Model)
	if !strings.Contains(m.message, "Radio directory unavailable") || !m.messageErr {
		t.Fatalf("friendly copy missing: %q", m.message)
	}
	if strings.Contains(m.message, "de1.api.radio-browser.info") {
		t.Fatalf("raw URL leaked: %q", m.message)
	}

	m.source = "apple-music"
	next, _ = m.Update(listMsg{generation: m.generation, key: "apple-music/Playlists", title: "Playlists", err: errors.New("boom")})
	m = next.(Model)
	if m.message != "Error: boom" {
		t.Fatalf("non-radio errors keep the detail: %q", m.message)
	}
}

func TestRadioFavoritesEmptyHintPointsAtBrowse(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Favorites"
	if text := m.emptyText(); !strings.Contains(text, "3 to browse") {
		t.Fatalf("empty hint = %q", text)
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
	if !strings.Contains(view, "scroll · Esc close") {
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

func TestSmallHelpScrolls(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height, m.overlay = 60, 12, "help"
	first := m.overlayView(60, 12)
	if !strings.Contains(first, "1-10/") {
		t.Fatalf("scroll title missing:\n%s", first)
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
	if !strings.Contains(second, "2-11/") {
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
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseWheel(down int, x, y int) tea.MouseMsg {
	button := tea.MouseWheelUp
	if down == 1 {
		button = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
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

func TestMouseSecondClickKeepsTheOriginallyClickedMainListItem(t *testing.T) {
	m, _, _ := newModel(t)
	// Three content rows makes the old cursor-centred window shift after a
	// click on the last visible row. The next click at the same coordinates
	// used to open the following playlist.
	m.width, m.height = 120, 16
	m.title = "Playlists"
	m.items = []core.Item{
		{Kind: "playlist", ID: "p1", Title: "One"},
		{Kind: "playlist", ID: "p2", Title: "Two"},
		{Kind: "playlist", ID: "p3", Title: "Three"},
		{Kind: "playlist", ID: "p4", Title: "Four"},
		{Kind: "playlist", ID: "p5", Title: "Five"},
	}
	l := m.layout()
	if rows := l.listHeight - 2; rows != 3 {
		t.Fatalf("content rows = %d, want 3", rows)
	}
	y := l.listTop + 1 + 2 // final currently visible content row: "Three"
	next, _ := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if m.selected != 2 || m.listOffset != 0 {
		t.Fatalf("first click selected=%d offset=%d, want 2/0", m.selected, m.listOffset)
	}
	next, cmd := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if cmd == nil || !m.loading || m.title != "Three" {
		t.Fatalf("second click opened %q (loading=%v cmd=%v), want Three", m.title, m.loading, cmd != nil)
	}
}

func TestWideLayoutKeepsNowPlayingInFixedDock(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Title: "One"}}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", ID: "s1", Title: "Current Song", Artist: "Artist"}}

	view := plainText(m.View().Content)
	if m.layout().showPanel || !strings.Contains(view, "┌── PLAYLISTS") || !strings.Contains(view, "┌── NOW PLAYING") {
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
	if before.listTop != after.listTop || before.listHeight != after.listHeight || before.nowHeight != after.nowHeight || before.dockGap != after.dockGap {
		t.Fatalf("toast moved dock: before=%+v after=%+v", before, after)
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
	for _, want := range []string{"┌── NOW PLAYING", "Your Love", "Playing · Apple Music", "┌── UP NEXT · 2/2 · QUEUE"} {
		if !strings.Contains(view, want) {
			t.Fatalf("playback dock missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "State:") || strings.Contains(view, "From:") {
		t.Fatalf("playback dock retained diagnostic copy:\n%s", view)
	}
}

func TestNowPlayingSeparatesSelectedAndAvailableFormats(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.state = core.PlaybackState{
		Status: "playing", Mode: "full", Format: "System-selected",
		Track:     &core.Item{Kind: "song", Title: "Arsenal", Artist: "Slipknot"},
		Available: []string{"ALAC Hi-Res Lossless · up to 24/192", "AAC 256 kbps"},
	}
	view := plainText(m.View().Content)
	if !strings.Contains(view, "Playing · Apple Music · System-selected") {
		t.Fatalf("selected stream label missing:\n%s", view)
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

func TestNowPlayingIdentifiesPlaybackSourceWhenBrowsingElsewhere(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", Title: "Apple Song"}}
	if got := m.nowTitle(); got != "Now Playing · Apple Music" {
		t.Fatalf("Apple playback title = %q", got)
	}
	m.source = "apple-music"
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", Title: "Radio"}}
	if got := m.nowTitle(); got != "Now Playing · Radio · LIVE" {
		t.Fatalf("Radio playback title = %q", got)
	}
}

func TestLiveDockDoesNotRepeatLiveInBody(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "radio"
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Format: "live stream", Track: &core.Item{Kind: "stream", Title: "Lofi"}}
	if title := m.nowTitle(); title != "Now Playing · LIVE" {
		t.Fatalf("live title = %q", title)
	}
	lines := strings.Join(m.nowLines(80, 8), "\n")
	if !strings.Contains(lines, "Playing · Radio stream") || strings.Contains(lines, "LIVE ·") || strings.Contains(lines, "live stream") {
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
	if dock := strings.Join(m.nowLines(100, 8), "\n"); !strings.Contains(dock, "Stream did not start within 10s") {
		t.Fatalf("live dock hides playback error:\n%s", dock)
	}
	if info := strings.Join(m.infoLines(200), "\n"); !strings.Contains(info, "Stream did not start within 10s") {
		t.Fatalf("track info hides playback error:\n%s", info)
	}
}

func TestWideRadioUsesTheSameNowPlayingDock(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}}
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}}

	view := plainText(m.View().Content)
	if m.layout().showPanel || !strings.Contains(view, "┌── FAVORITES") || !strings.Contains(view, "┌── NOW PLAYING · LIVE") {
		t.Fatalf("wide Radio dock missing:\n%s", view)
	}
}

func TestMouseWheelFocusesPanelAndScrolls(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Current"}, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}, {Title: "D"}, {Title: "E"}}}
	l := m.layout()
	if !l.showPanel {
		t.Fatal("queue rail expected")
	}
	next, _ := m.handleMouse(mouseWheel(1, l.gutter+l.mainWidth+2, l.listTop+l.listHeight+2))
	m = next.(Model)
	if !m.queueFocus || m.queueCursor != 3 {
		t.Fatalf("focus=%v cursor=%d, want focused cursor 3", m.queueFocus, m.queueCursor)
	}
}

func TestMouseClickPanelSelectsAndJumps(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 120, 30
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Current"}, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m.state = f.state
	l := m.layout()
	x, y := l.gutter+l.mainWidth+3, l.listTop+l.listHeight+3
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
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Current"}, Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	m.queueFocus = true
	m.title = "Playlists"
	m.items = radioPageItems(0, 60)
	rows := m.layout().listHeight - 2
	m.selected = 10
	next, _ := m.handleMouse(mouseWheel(1, 5, m.layout().listTop+1))
	m = next.(Model)
	if m.queueFocus {
		t.Fatal("wheel over the main list should unfocus the panel")
	}
	// Wheeling drags the viewport; the cursor keeps its item while it stays in
	// the window and only follows when it would leave.
	if start, _ := m.mainListWindow(rows); start != 3 {
		t.Fatalf("wheel did not scroll the viewport: start=%d", start)
	}
	if item, _ := m.selectedItem(); item.Title != "Station 10" {
		t.Fatalf("wheel moved the cursor off its visible item: %q", item.Title)
	}
	// Scrolling far past it pulls the cursor to the window edge instead of
	// leaving it off-screen.
	m = m.scrollMainList(rows * 4)
	start, end := m.mainListWindow(rows)
	if m.selected < start || m.selected >= end {
		t.Fatalf("cursor left the window: selected=%d window=%d-%d", m.selected, start, end)
	}
}

func TestMouseWheelScrollsViewportAndScrollbarTracksIt(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items = radioPageItems(0, 200)
	rows := m.layout().listHeight - 2

	first := m.listLines(80, rows)
	if len(first) != rows {
		t.Fatalf("list should fill the window for the scrollbar, got %d rows", len(first))
	}
	if !strings.HasSuffix(strings.Split(first[0], "\x1b")[0], "█") && !strings.Contains(first[0], "█") {
		t.Fatalf("scrollbar thumb missing at the top:\n%s", first[0])
	}

	m = m.scrollMainList(rows)
	start, _ := m.mainListWindow(rows)
	if start != rows {
		t.Fatalf("viewport start = %d, want %d", start, rows)
	}
	after := m.listLines(80, rows)
	if first[0] == after[0] {
		t.Fatal("scrollbar did not move with the viewport")
	}

	// A list that fits has no scrollbar track at all.
	short, _, _ := newModel(t)
	short.width, short.height = 120, 30
	short.items = radioPageItems(0, 3)
	if lines := short.listLines(80, rows); strings.Contains(strings.Join(lines, "\n"), "█") || strings.Contains(strings.Join(lines, "\n"), "│") {
		t.Fatalf("short list should not draw a scrollbar:\n%s", lines[0])
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
	next, _ := m.handleMouse(mouseClick(x+m.layout().gutter, 1))
	m = next.(Model)
	if m.view != "Favorites" {
		t.Fatalf("view = %q, want Favorites", m.view)
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

func TestAddStreamURLUsesICYNameWhenAvailable(t *testing.T) {
	m, f, store := newModel(t)
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
	favorites := store.FavoritesFor("radio")
	if len(favorites) != 1 || favorites[0].Title != "Indie Pop Rocks" {
		t.Fatalf("favorite should carry the ICY name: %#v", favorites)
	}
	if !strings.Contains(m.message, "Indie Pop Rocks") {
		t.Fatalf("toast should name the station: %q", m.message)
	}
}

func TestAddStreamURLKeepsRawTitleWithoutICYName(t *testing.T) {
	m, _, store := newModel(t)
	m.source, m.view = "radio", "Favorites"
	next, _ := m.handleKey(runeKey('a'))
	m = next.(Model)
	m.input.SetValue("https://radio.example/plain")
	next, cmd := m.submitInput()
	m = next.(Model)
	m = run(m, cmd)
	favorites := store.FavoritesFor("radio")
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

	view := plainText(m.View().Content)
	if strings.Contains(view, "☆") || !strings.Contains(view, "  Example FM") || !strings.Contains(view, "f favorite") {
		t.Fatalf("unfavorited station state missing:\n%s", view)
	}

	store.ToggleFavorite("radio", station)
	view = plainText(m.View().Content)
	lines := plainText(strings.Join(m.listLines(60, 10), "\n"))
	if !strings.Contains(lines, "Example FM ★") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("favorited station state missing:\n%s", view)
	}
}

func TestRadioFavoritesRootOmitsRedundantFavoriteIcon(t *testing.T) {
	m, _, store := newModel(t)
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	store.ToggleFavorite("radio", station)
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items, m.width, m.height = []core.Item{station}, 100, 24
	view := plainText(m.View().Content)
	if strings.Contains(view, "★") || !strings.Contains(view, "Example FM") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("Favorites root has redundant marker or missing action:\n%s", view)
	}

	m.history = []page{{source: "radio", view: "Favorites", title: "Favorites"}}
	if view := plainText(m.View().Content); !strings.Contains(view, "★") || strings.Contains(view, "★ Example FM") {
		t.Fatalf("temporary result page lost favorite state:\n%s", view)
	}
}

func TestDefaultThemeFollowsTerminalPalette(t *testing.T) {
	m, _, _ := newModel(t)
	if m.themeName != "default" {
		t.Fatalf("default theme = %q, want default", m.themeName)
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

func TestCompletedActionResumesProbes(t *testing.T) {
	m, _, store := newModel(t)
	for i := 0; i < 3; i++ {
		store.ToggleFavorite("radio", core.Item{Kind: "stream", URL: fmt.Sprintf("https://radio.example/%d", i), Title: fmt.Sprintf("S%d", i)})
	}
	m.source, m.view = "radio", "Favorites"
	m.width, m.height = 90, 20
	m.items = store.FavoritesFor("radio")
	m.busy = true
	if paused, cmd := m.scheduleProbes(); len(paused.probes) != 0 || cmd != nil {
		t.Fatalf("probes scheduled while busy: probes=%d cmd=%v", len(paused.probes), cmd != nil)
	}
	model, _ := m.Update(actionMsg{state: core.PlaybackState{Status: "playing"}})
	done := model.(Model)
	if done.busy {
		t.Fatalf("action completion left busy set")
	}
	if len(done.probes) == 0 {
		t.Fatalf("completed action did not resume probing")
	}
}

func TestMouseNavigationSchedulesProbes(t *testing.T) {
	m, _, store := newModel(t)
	for i := 0; i < 5; i++ {
		store.ToggleFavorite("radio", core.Item{Kind: "stream", URL: fmt.Sprintf("https://radio.example/%d", i), Title: fmt.Sprintf("S%d", i)})
	}
	m.width, m.height = 90, 20
	x := -1
	for i := 0; i < 90; i++ {
		if name, ok := sourceTabAt(i); ok && name == "radio" {
			x = i
			break
		}
	}
	if x < 0 {
		t.Fatalf("could not locate the Radio source tab")
	}
	model, _ := m.Update(mouseClick(x+m.layout().gutter, 0))
	radio := model.(Model)
	if radio.source != "radio" {
		t.Fatalf("source after click = %q, want radio", radio.source)
	}
	if len(radio.probes) == 0 {
		t.Fatalf("clicking the Radio tab did not schedule probes")
	}
}

func TestRadioProbeSchedulesVisibleWithTwoWorkers(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	items := make([]core.Item, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, core.Item{Kind: "stream", URL: fmt.Sprintf("https://radio.example/%d", i), Title: fmt.Sprintf("S%d", i)})
	}
	m.items = items
	m.selected = 0

	m, _ = m.scheduleProbes()
	if m.probeActive != 2 {
		t.Fatalf("active workers = %d, want 2", m.probeActive)
	}
	visible := len(m.probeWindow())
	if visible == 0 || visible >= 20 {
		t.Fatalf("unexpected visible window size %d", visible)
	}
	if len(m.probes) != visible {
		t.Fatalf("scheduled %d probes, want only the %d visible items", len(m.probes), visible)
	}
	checking, queued := 0, 0
	for _, probe := range m.probes {
		switch probe.status {
		case "checking":
			checking++
		case "queued":
			queued++
		}
	}
	if checking != 2 || checking+queued != visible {
		t.Fatalf("checking=%d queued=%d visible=%d", checking, queued, visible)
	}
	if f.probed != nil {
		t.Fatalf("probes should not run until their commands execute: %#v", f.probed)
	}
}

func TestRadioProbeTerminalStatesAreCachedAndDoNotReorder(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "A"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "B"},
	}
	m.selected = 1
	f.probeResult = core.RadioProbeResult{Status: "healthy", LatencyMs: 42}

	m, cmd := m.scheduleProbes()
	m = drainAll(m, cmd)
	if m.probeActive != 0 {
		t.Fatalf("workers still active: %d", m.probeActive)
	}
	for _, item := range m.items {
		if probe := m.probes[radioProbeKey(item)]; probe.status != "healthy" || probe.latency != 42 {
			t.Fatalf("probe = %#v for %s", probe, item.Title)
		}
	}
	if m.items[0].Title != "A" || m.items[1].Title != "B" || m.selected != 1 {
		t.Fatalf("probe state changed the list: %#v selected=%d", m.items, m.selected)
	}

	before := len(f.probed)
	m, cmd = m.scheduleProbes()
	if cmd != nil {
		t.Fatal("cached terminal states should not schedule new probes")
	}
	if len(f.probed) != before {
		t.Fatalf("re-probed cached URLs: %#v", f.probed)
	}
}

func TestRadioProbeReusesFreshPersistentResult(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	item := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Saved"}
	m.items = []core.Item{item}
	checkedAt := time.Now().Add(-3 * time.Hour)
	m.radioCache.RecordHealth(item.URL, "healthy", 382, "", checkedAt)

	next, cmd := m.scheduleProbes()
	if cmd != nil || len(f.probed) != 0 {
		t.Fatalf("fresh persisted probe was re-run: cmd=%v calls=%v", cmd != nil, f.probed)
	}
	probe := next.probes[radioProbeKey(item)]
	if !probe.persisted || probe.status != "healthy" || probe.latency != 382 {
		t.Fatalf("persisted probe = %#v", probe)
	}
	if segment, _ := next.probeSegment(item); segment != "● 0.4s · checked 3h ago" {
		t.Fatalf("persisted segment = %q", segment)
	}
}

func TestRadioBrowseSortUsesDirectoryAndEndpointSignals(t *testing.T) {
	m, _, _ := newModel(t)
	items := []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "Alpha", Radio: &core.RadioMetadata{StationUUID: "a", ClickCount: 100}},
		{Kind: "stream", URL: "https://radio.example/b", Title: "Beta", Radio: &core.RadioMetadata{StationUUID: "b", ClickCount: 1000}},
		{Kind: "stream", URL: "https://radio.example/c", Title: "Charlie", Radio: &core.RadioMetadata{StationUUID: "c", ClickCount: 50}},
		{Kind: "stream", URL: "https://radio.example/d", Title: "Delta", Radio: &core.RadioMetadata{StationUUID: "d", ClickCount: 2000}},
	}
	now := time.Now()
	m.radioCache.RecordHealth(items[1].URL, "healthy", 300, "", now)
	m.radioCache.RecordHealth(items[2].URL, "healthy", 80, "", now)
	m.radioCache.RecordHealth(items[3].URL, "failed", 0, "tls", now)

	for _, test := range []struct {
		mode string
		want []string
	}{
		{"", []string{"Beta", "Charlie", "Alpha", "Delta"}},
		{"popular", []string{"Delta", "Beta", "Alpha", "Charlie"}},
		{"fastest", []string{"Charlie", "Beta", "Alpha", "Delta"}},
		{"name", []string{"Alpha", "Beta", "Charlie", "Delta"}},
	} {
		m.browseQuery.Sort = test.mode
		ordered := m.sortRadioItems(items)
		for index, want := range test.want {
			if ordered[index].Title != want {
				t.Fatalf("sort %q index %d = %q, want %q", test.mode, index, ordered[index].Title, want)
			}
		}
	}
}

func TestDiscoverySortCyclesAndAppearsInBrowseTitle(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay, m.discoverySelected = "radio", "discovery", discoverySort
	next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.discoveryPending.Sort != "popular" || !strings.Contains(m.overlayView(80, 20), "Sort           Popular") {
		t.Fatalf("sort did not cycle: pending=%#v\n%s", m.discoveryPending, m.overlayView(80, 20))
	}
	m.discoveryPending.Sort = "fastest"
	m.discoverySelected = discoveryConfirm
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.browseQuery.Sort != "fastest" || m.title != "Popular Worldwide · Fastest" {
		t.Fatalf("sort apply = query=%#v title=%q", m.browseQuery, m.title)
	}
}

func TestRadioFastestTitleReportsMeasurementCoverage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide · Fastest"
	m.browseQuery = radioDiscovery{Sort: "fastest"}
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "A"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "B"},
		{Kind: "stream", URL: "https://radio.example/c", Title: "C"},
	}
	if title := m.listTitle(); !strings.Contains(title, "0/3 measured") {
		t.Fatalf("cold coverage title = %q", title)
	}
	m.radioCache.RecordHealth(m.items[0].URL, "healthy", 50, "", time.Now())
	m.radioCache.RecordHealth(m.items[1].URL, "healthy", 70, "", time.Now())
	if title := m.listTitle(); !strings.Contains(title, "2/3 measured") {
		t.Fatalf("partial coverage title = %q", title)
	}
	m.radioCache.RecordHealth(m.items[2].URL, "healthy", 90, "", time.Now())
	if title := m.listTitle(); strings.Contains(title, "measured") {
		t.Fatalf("complete coverage should not add noise: %q", title)
	}
}

func TestResortRadioBrowseAppliesFreshMeasurementsAndKeepsSelection(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.browseQuery = radioDiscovery{Sort: "fastest"}
	m.width, m.height = 100, 24
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/slow", Title: "Slow"},
		{Kind: "stream", URL: "https://radio.example/fast", Title: "Fast"},
	}
	now := time.Now()
	m.radioCache.RecordHealth(m.items[0].URL, "healthy", 900, "", now)
	m.radioCache.RecordHealth(m.items[1].URL, "healthy", 100, "", now)
	m.selected = 0
	next, cmd := m.resortRadioBrowse()
	sorted := next.(Model)
	if sorted.items[0].Title != "Fast" {
		t.Fatalf("re-sort order = %q, %q", sorted.items[0].Title, sorted.items[1].Title)
	}
	if item, _ := sorted.selectedItem(); item.Title != "Slow" {
		t.Fatalf("re-sort lost selection: %q", item.Title)
	}
	if sorted.message == "" || cmd == nil {
		t.Fatalf("re-sort should announce itself: message=%q cmd=%v", sorted.message, cmd != nil)
	}
}

func TestRecommendedPrefersPreviouslyPlayedStations(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	items := []core.Item{
		{Kind: "stream", URL: "https://radio.example/new", Title: "New", Radio: &core.RadioMetadata{StationUUID: "new", ClickCount: 900}},
		{Kind: "stream", URL: "https://radio.example/known", Title: "Known", Radio: &core.RadioMetadata{StationUUID: "known", ClickCount: 1}},
	}
	if ordered := m.sortRadioItems(items); ordered[0].Title != "New" {
		t.Fatalf("without history popularity should win: %q", ordered[0].Title)
	}
	m.store.AddRecent("radio", items[1])
	if ordered := m.sortRadioItems(items); ordered[0].Title != "Known" {
		t.Fatalf("Recommended should prefer a previously played station: %q", ordered[0].Title)
	}
	m.browseQuery.Sort = "popular"
	if ordered := m.sortRadioItems(items); ordered[0].Title != "New" {
		t.Fatalf("Popular should stay pure clickcount: %q", ordered[0].Title)
	}
}

func TestDiscoveryArrowKeysWrapWithoutDeadEnds(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.overlay, m.discoverySelected = "radio", "discovery", discoveryConfirm
	next, _ := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if m.discoverySelected != discoveryCancel {
		t.Fatalf("down from Confirm should reach Cancel, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if m.discoverySelected != discoveryText {
		t.Fatalf("down from Cancel should wrap to Text, got %d", m.discoverySelected)
	}
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(Model)
	if m.discoverySelected != discoveryCancel {
		t.Fatalf("up from Text should wrap to Cancel, got %d", m.discoverySelected)
	}
}

func TestRadioProbeRecordsFailureCodes(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/bad", Title: "Bad"}}
	f.probeResult = core.RadioProbeResult{Status: "failed", ErrorCode: "tls", Message: "cert rejected"}

	m, cmd := m.scheduleProbes()
	m = drainAll(m, cmd)
	probe := m.probes[radioProbeKey(m.items[0])]
	if probe.status != "failed" || probe.code != "tls" {
		t.Fatalf("failure probe = %#v", probe)
	}
	if !strings.Contains(strings.Join(m.listLines(110, 5), "\n"), "TLS error") {
		t.Fatalf("failure row missing text:\n%s", strings.Join(m.listLines(110, 5), "\n"))
	}

	m.probes = map[string]radioProbe{}
	m.radioCache = radio.NewCache("")
	f.probeErr = context.DeadlineExceeded
	m, cmd = m.scheduleProbes()
	m = drainAll(m, cmd)
	probe = m.probes[radioProbeKey(m.items[0])]
	if probe.status != "failed" || probe.code != "transport" {
		t.Fatalf("transport failure probe = %#v", probe)
	}
}

func TestFormatProbeLatency(t *testing.T) {
	for _, test := range []struct {
		ms   int
		want string
	}{
		{0, "0ms"},
		{3, "3ms"},
		{99, "99ms"},
		{100, "0.1s"},
		{382, "0.4s"},
		{999, "1.0s"},
		{2000, "2.0s"},
		{4915, "4.9s"},
		{5999, "6.0s"},
	} {
		if got := formatProbeLatency(test.ms); got != test.want {
			t.Fatalf("formatProbeLatency(%d) = %q, want %q", test.ms, got, test.want)
		}
	}
}

func TestRadioProbeRenderingStates(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	item := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Power POP", Artist: "HLS · Türkiye"}
	m.items = []core.Item{item}
	key := radioProbeKey(item)

	for _, test := range []struct {
		probe radioProbe
		want  string
	}{
		{radioProbe{}, "○ unchecked"},
		{radioProbe{status: "queued"}, "○ queued"},
		{radioProbe{status: "checking"}, "◌ checking…"},
		{radioProbe{status: "healthy", latency: 87}, "● 87ms"},
		{radioProbe{status: "healthy", latency: 382}, "● 0.4s"},
		{radioProbe{status: "healthy", latency: 2000}, "● 2.0s"},
		{radioProbe{status: "healthy", latency: 4915}, "● 4.9s"},
		{radioProbe{status: "failed", code: "tls"}, "× TLS error"},
		{radioProbe{status: "failed", code: "timeout"}, "× timeout"},
		{radioProbe{status: "failed", code: "network"}, "× network error"},
		{radioProbe{status: "failed", code: "unsupported"}, "× unsupported"},
		{radioProbe{status: "failed", code: "weird"}, "× probe failed"},
	} {
		m.probes = map[string]radioProbe{key: test.probe}
		lines := strings.Join(m.listLines(110, 5), "\n")
		if !strings.Contains(lines, test.want) {
			t.Fatalf("probe %#v missing %q:\n%s", test.probe, test.want, lines)
		}
	}
}

func TestRadioProbeScopeSwitchDropsQueue(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/a", Title: "A"}}
	m, _ = m.scheduleProbes()
	if len(m.probes) == 0 {
		t.Fatal("expected a scheduled probe")
	}
	m.probeActive = 0 // simulate the worker finishing without a message

	m.source = "apple-music"
	m.view = "Home"
	m, cmd := m.scheduleProbes()
	if cmd != nil || len(m.probeQueue) != 0 {
		t.Fatalf("apple-music should not probe: queue=%d cmd=%v", len(m.probeQueue), cmd != nil)
	}
}

func TestRadioProbeDisconnectClearsInFlight(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/a", Title: "A"}}
	m, _ = m.scheduleProbes()
	m = m.clearProbesOnDisconnect()
	for key, probe := range m.probes {
		if probe.status == "checking" || probe.status == "queued" {
			t.Fatalf("in-flight probe survived disconnect: %#v", key)
		}
	}
	if m.probeActive != 0 || len(m.probeQueue) != 0 {
		t.Fatalf("active=%d queue=%d", m.probeActive, len(m.probeQueue))
	}
}

func TestRadioProbeFailureWarnsBeforeRetry(t *testing.T) {
	m, f, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	station := core.Item{Kind: "stream", URL: "https://radio.example/dead", Title: "Dead FM"}
	m.items = []core.Item{station}
	m.selected = 0
	m.probes[radioProbeKey(station)] = radioProbe{status: "failed", code: "timeout"}

	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.radioURL != station.URL {
		t.Fatalf("failed station must still be playable: %q", f.radioURL)
	}
	if !strings.Contains(m.message, "earlier probe failed (timeout)") {
		t.Fatalf("retry warning missing: %q", m.message)
	}
	if probe, ok := m.probes[radioProbeKey(station)]; ok && probe.status == "failed" {
		t.Fatalf("manual play left the failed probe cached: %+v", probe)
	}
}

func TestRadioProbeTimeoutIsRetriedAfterScopeChange(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	station := core.Item{Kind: "stream", URL: "https://radio.example/slow", Title: "Slow FM"}
	m.items = []core.Item{station}
	m.probeScope = m.viewKey()
	m.probes[radioProbeKey(station)] = radioProbe{status: "failed", code: "timeout"}

	// Staying on the same scope preserves the result and cannot spin retries.
	m, cmd := m.scheduleProbes()
	if cmd != nil || m.probes[radioProbeKey(station)].code != "timeout" {
		t.Fatalf("same scope retried timeout: cmd=%v probe=%#v", cmd != nil, m.probes[radioProbeKey(station)])
	}

	m.view, m.items = "Recent", nil
	m, _ = m.scheduleProbes()
	if _, ok := m.probes[radioProbeKey(station)]; ok {
		t.Fatal("scope change should forget timeout probe results")
	}

	m.view, m.items = "Browse", []core.Item{station}
	m, cmd = m.scheduleProbes()
	if cmd == nil || m.probes[radioProbeKey(station)].status != "checking" {
		t.Fatalf("returning to view did not retry timeout: cmd=%v probe=%#v", cmd != nil, m.probes[radioProbeKey(station)])
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

func TestRadioProbeDroppedQueueBecomesEligibleAgain(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.width, m.height = 90, 20
	items := []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "A"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "B"},
		{Kind: "stream", URL: "https://radio.example/c", Title: "C"},
	}
	m.items = items
	m, _ = m.scheduleProbes()
	if m.probeActive != 2 || len(m.probeQueue) != 1 {
		t.Fatalf("active=%d queue=%d, want 2/1", m.probeActive, len(m.probeQueue))
	}
	dropped := m.probeQueue[0].key

	m.view, m.items = "Recent", nil
	m, _ = m.scheduleProbes()
	if len(m.probeQueue) != 0 {
		t.Fatalf("leaving the page should drop pending work: %#v", m.probeQueue)
	}
	if _, known := m.probes[dropped]; known {
		t.Fatal("dropped queued probe must not stay cached as known")
	}

	m.probeActive, m.view, m.items = 0, "Browse", items
	m, cmd := m.scheduleProbes()
	if cmd == nil {
		t.Fatal("returning to the page should probe the forgotten item")
	}
	if probe, ok := m.probes[dropped]; !ok || probe.status != "checking" {
		t.Fatalf("forgotten item not restarted: %#v", probe)
	}
}

func TestAppleMusicViewsStartWithHome(t *testing.T) {
	if got := strings.Join(amViews, ","); got != "Home,Playlists,Favorites,Recent,Presets" {
		t.Fatalf("views = %q", got)
	}
	m, _, _ := newModel(t)
	if m.view != "Home" || m.title != "Home" {
		t.Fatalf("default = %q / %q", m.view, m.title)
	}
}

func TestAppleMusicFavoritesView(t *testing.T) {
	m, f, store := newModel(t)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song One", Artist: "Artist", URL: "https://music.apple.com/song/s1"}
	playlist := core.Item{Kind: "playlist", ID: "p1", Title: "Road Trip"}
	store.ToggleFavorite("apple-music", song)
	store.ToggleFavorite("apple-music", playlist)

	m.source, m.view, m.loading = "apple-music", "Favorites", true
	msg := m.loadView()().(listMsg)
	if msg.title != "Favorites · local" || len(msg.items) != 2 || msg.items[0].Kind != "song" || msg.items[1].Kind != "playlist" {
		t.Fatalf("am favorites = %q %#v", msg.title, msg.items)
	}
	m = run(m, m.loadView())
	if m.loading || len(m.items) != 2 {
		t.Fatalf("favorites view did not load: loading=%v items=%d", m.loading, len(m.items))
	}
	m.cache["apple-music/Favorites"] = m.items

	m.selected = 0
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "song" || f.played.ID != "s1" {
		t.Fatalf("favorite song did not play: %#v", f.played)
	}

	m.selected = 1
	next, cmd = m.activate()
	m = next.(Model)
	if m.detailKind != "playlist" || m.detailID != "p1" {
		t.Fatalf("favorite playlist did not open: kind=%q id=%q", m.detailKind, m.detailID)
	}
	_ = cmd
}

func TestAppleMusicUnfavoriteRefreshesAndInvalidates(t *testing.T) {
	m, _, store := newModel(t)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song One"}
	store.ToggleFavorite("apple-music", song)
	m.source, m.view = "apple-music", "Favorites"
	m.items = store.FavoritesFor("apple-music")
	m.cache["apple-music/Favorites"] = m.items
	m.selected = 0

	next, _ := m.toggleFavorite()
	m = next.(Model)
	if len(store.FavoritesFor("apple-music")) != 0 {
		t.Fatalf("unfavorite failed: %#v", store.FavoritesFor("apple-music"))
	}
	if len(m.items) != 0 {
		t.Fatalf("favorites view not refreshed: %#v", m.items)
	}
	if _, cached := m.cache["apple-music/Favorites"]; cached {
		t.Fatal("toggle retained the stale Apple Music Favorites cache")
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
	view := plainText(m.View().Content)
	if m.overlay != "help" || !strings.Contains(view, "── NAVIGATION ──") || !strings.Contains(view, "remove selected track") || !strings.Contains(view, "Help ·") || !strings.Contains(view, "reload the current list") {
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
	for _, key := range []tea.KeyPressMsg{runeKey('j'), runeKey('0'), runeKey('/'), runeKey('x'), tea.KeyPressMsg{Code: tea.KeyEnter}} {
		next, cmd := m.handleKey(key)
		m = next.(Model)
		if cmd != nil || m.source != "apple-music" || m.selected != 1 || m.filter != "kept" || m.queueFocus || m.input.Focused() || m.overlay != "" {
			t.Fatalf("%q mutated latent state: %#v", key.String(), m)
		}
	}
	for _, key := range []tea.KeyPressMsg{runeKey('q'), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}} {
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
	view := plainText(m.View().Content)
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
	view := plainText(m.View().Content)
	if !strings.Contains(view, "PLAYLISTS (1)") || !strings.Contains(view, "UP NEXT ·") {
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

func TestNowLinesUseCompactQueueSummary(t *testing.T) {
	m, _, _ := newModel(t)
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Shuffle: true, QueueIndex: 1,
		Track: &core.Item{ID: "2", Title: "B"}, Queue: []core.Item{{ID: "1", Title: "A"}, {ID: "2", Title: "B"}, {ID: "3", Title: "C"}}}
	m.width = 120
	wide := strings.Join(m.nowLines(120, 8), "\n")
	if !strings.Contains(wide, "Playing · Apple Music") || !strings.Contains(wide, "shuffle") || strings.Contains(wide, "Up Next ·") {
		t.Fatalf("wide now lines =\n%s", wide)
	}
	m.width = 80
	narrow := strings.Join(m.nowLines(80, 8), "\n")
	if !strings.Contains(narrow, "Up Next · 2 of 3 · 0 focus") {
		t.Fatalf("unfocused narrow now lines =\n%s", narrow)
	}
	m.queueFocus = true
	focused := strings.Join(m.nowLines(80, 8), "\n")
	if !strings.Contains(focused, "Up Next · 2 of 3 · 0 back") || strings.Contains(focused, "0 focus") {
		t.Fatalf("focused narrow now lines =\n%s", focused)
	}
}

func TestQueueTitleIncludesSourceAndPosition(t *testing.T) {
	m, _, _ := newModel(t)
	m.loading = false
	m.queueSource = queueContext{Kind: "playlist", ID: "p1", Title: "Morning"}
	m.state = core.PlaybackState{QueueIndex: 1, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	if got := m.queueTitle(0); got != "Up Next · 2/3 · Morning" {
		t.Fatalf("title = %q", got)
	}
	// A panel that cannot show the whole queue must report its visible window,
	// otherwise a short dock hides the remaining tracks.
	long := core.PlaybackState{QueueIndex: 12, Queue: make([]core.Item, 48)}
	m.state = long
	got := m.queueTitle(minDockRows)
	if !strings.Contains(got, "13/48") || !strings.Contains(got, "shown") {
		t.Fatalf("long queue title = %q", got)
	}
	if got := m.queueTitle(60); strings.Contains(got, "shown") {
		t.Fatalf("fully visible queue should not report a window: %q", got)
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
	// The distinction is highlight-only; a text prefix would add row noise.
	if strings.Contains(strings.Join(m.listLines(120, 5), "\n"), "▶") {
		t.Fatalf("current row should be highlighted, not prefixed: %#v", m.listLines(120, 5))
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
	m, _, store := newModel(t)
	m.source, m.view, m.title = "radio", "Recent", "Recent"
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	m.items = []core.Item{station}
	m.state = core.PlaybackState{IsLive: true, Status: "playing", Track: &station}
	m.width, m.height = 120, 30

	store.ToggleFavorite("radio", station)
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

func TestRadioListMarksCurrentlyPlayingStation(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"}}
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/other", Title: "Other"},
		{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"},
	}
	if m.isPlayingItem(m.items[0]) || !m.isPlayingItem(m.items[1]) {
		t.Fatalf("playing station detection = %v/%v", m.isPlayingItem(m.items[0]), m.isPlayingItem(m.items[1]))
	}
	lines := m.listLines(120, 5)
	if len(lines) < 2 || !strings.Contains(lines[1], "Live FM") {
		t.Fatalf("playing row missing: %#v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "▶") {
		t.Fatalf("highlight must not add a prefix:\n%s", strings.Join(lines, "\n"))
	}

	// An Apple Music track must not mark an unrelated radio station.
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", ID: "s1", URL: "https://music.apple.com/song/1", Title: "Apple Song"}}
	if m.isPlayingItem(m.items[1]) {
		t.Fatal("Apple Music playback marked a radio row")
	}
}

func TestBrowseReentryPaintsCacheThenRefreshesSilently(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.width, m.height = 120, 24
	m.generation = 3
	first := radioPageItems(0, radioPageSize)
	m.cache[m.browseCacheKey()] = first
	m.loading, m.items = true, nil

	cmd := m.loadView()
	if cmd == nil {
		t.Fatal("cached Browse entry returned no command")
	}
	gotBatch, ok := cmd().(tea.BatchMsg)
	if !ok || len(gotBatch) != 2 {
		t.Fatalf("cached Browse entry should batch paint + refresh, got %#v", cmd())
	}

	// The instant paint only fills an empty view.
	next, _ := m.Update(gotBatch[0]())
	painted := next.(Model)
	if painted.loading || len(painted.items) != len(first) {
		t.Fatalf("cached paint = loading=%v items=%d", painted.loading, len(painted.items))
	}
	if painted.cache[painted.browseCacheKey()] == nil {
		t.Fatal("cached paint lost the remembered page")
	}

	// The silent refresh replaces rows without moving the cursor.
	painted.selected = 5
	selected, _ := painted.selectedItem()
	fresh := radioPageItems(0, radioPageSize)
	for i := range fresh {
		fresh[i].Title = "Fresh " + fresh[i].Title
	}
	next, _ = painted.Update(listMsg{generation: painted.generation, destination: painted.destination(), key: "radio/Browse", title: "Popular Worldwide", items: fresh, silent: true})
	refreshed := next.(Model)
	if len(refreshed.items) != len(fresh) || !strings.HasPrefix(refreshed.items[0].Title, "Fresh ") {
		t.Fatalf("silent refresh did not replace rows: %d", len(refreshed.items))
	}
	if after, _ := refreshed.selectedItem(); after.Title != "Fresh "+selected.Title {
		t.Fatalf("silent refresh moved the cursor: %q -> %q", selected.Title, after.Title)
	}

	// A second cached paint must not overwrite the already-populated view.
	next, _ = refreshed.Update(listMsg{generation: refreshed.generation, destination: refreshed.destination(), key: "radio/Browse", title: "Popular Worldwide", items: first, cached: true})
	if kept := next.(Model); !strings.HasPrefix(kept.items[0].Title, "Fresh ") {
		t.Fatalf("stale cached paint overwrote fresh rows: %q", kept.items[0].Title)
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
	m.width, m.height = 120, 24
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"}}
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/other", Title: "Other"},
		{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"},
	}
	// Selected playing row keeps the `>` cursor, so selection stays readable.
	m.selected = 1
	lines := m.listLines(120, 5)
	if !strings.Contains(plainText(lines[1]), ">  Live FM") {
		t.Fatalf("selected playing row lost the cursor:\n%s", lines[1])
	}
	// Selected idle row also uses the cursor.
	m.selected = 0
	lines = m.listLines(120, 5)
	if !strings.Contains(plainText(lines[0]), ">  Other") {
		t.Fatalf("selected idle row lost the cursor:\n%s", lines[0])
	}
	if !strings.HasPrefix(plainText(lines[1]), "   Live FM") {
		t.Fatalf("unselected playing row should have no cursor:\n%s", lines[1])
	}
	// Highlighted rows carry one blank column of padding on each side.
	if !strings.Contains(plainText(lines[1]), "  Live FM ") {
		t.Fatalf("playing row is missing highlight padding:\n%s", lines[1])
	}
}

func TestBrowseCachedPaintDoesNotResort(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.browseQuery = radioDiscovery{Sort: "fastest"}
	m.width, m.height = 120, 24
	// Deliberately not latency-ordered: the stored page must be shown as-is.
	stored := []core.Item{
		{Kind: "stream", URL: "https://radio.example/slow", Title: "Slow"},
		{Kind: "stream", URL: "https://radio.example/fast", Title: "Fast"},
	}
	now := time.Now()
	m.radioCache.RecordHealth(stored[0].URL, "healthy", 900, "", now)
	m.radioCache.RecordHealth(stored[1].URL, "healthy", 100, "", now)
	m.cache[m.browseCacheKey()] = stored
	m.loading, m.items = true, nil

	next, _ := m.applyCachedList(listMsg{key: "radio/Browse", title: "Popular Worldwide · Fastest", items: stored, cached: true})
	painted := next.(Model)
	if painted.items[0].Title != "Slow" || painted.items[1].Title != "Fast" {
		t.Fatalf("cached paint re-sorted the stored page: %v", titlesOf(painted.items))
	}
}

func TestBrowseBackgroundRefreshKeepsRowOrder(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.width, m.height = 120, 24
	// The user already sees this order; a directory refresh returns the same
	// stations in a different order (for example new click counts).
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "Alpha"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "Beta"},
		{Kind: "stream", URL: "https://radio.example/c", Title: "Charlie"},
	}
	m.selected = 1
	fresh := []core.Item{
		{Kind: "stream", URL: "https://radio.example/c", Title: "Charlie"},
		{Kind: "stream", URL: "https://radio.example/a", Title: "Alpha"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "Beta"},
		{Kind: "stream", URL: "https://radio.example/d", Title: "Delta"},
	}
	next, _ := m.Update(listMsg{generation: m.generation, destination: m.destination(), key: "radio/Browse", title: "Popular Worldwide", items: fresh, silent: true})
	refreshed := next.(Model)
	want := []string{"Alpha", "Beta", "Charlie", "Delta"}
	for index, title := range want {
		if refreshed.items[index].Title != title {
			t.Fatalf("order changed at %d: %q, want %q (%v)", index, refreshed.items[index].Title, title, titlesOf(refreshed.items))
		}
	}
	if item, _ := refreshed.selectedItem(); item.Title != "Beta" {
		t.Fatalf("refresh moved the cursor to %q", item.Title)
	}
}

func titlesOf(items []core.Item) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func TestBrowseRefreshFailureKeepsCachedRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items = radioPageItems(0, 5)
	m.loading = false
	next, cmd := m.Update(listMsg{generation: m.generation, destination: m.destination(), key: "radio/Browse", title: "Popular Worldwide", err: errors.New("offline"), silent: true})
	got := next.(Model)
	if len(got.items) != 5 || cmd != nil {
		t.Fatalf("refresh failure dropped rows: items=%d cmd=%v", len(got.items), cmd != nil)
	}
	if got.messageErr {
		t.Fatalf("background refresh failure should stay quiet: %q", got.message)
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

func TestCoalescedKeyBurstIsReplayedAsIndividualKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.loading = false
	m.items = []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}, {Title: "D"}, {Title: "E"}}

	// Fast typing and key auto-repeat can arrive as one event. Every rune must
	// still take effect; otherwise the burst is silently swallowed.
	next, _ := m.Update(tea.KeyPressMsg{Text: "jjj"})
	m = next.(Model)
	if m.selected != 3 {
		t.Fatalf("burst selection = %d, want 3", m.selected)
	}

	// A real paste must never be replayed as shortcuts.
	pasted := m
	next, _ = pasted.Update(tea.PasteMsg{Content: "jj"})
	pasted = next.(Model)
	if pasted.selected != 3 {
		t.Fatalf("paste moved the cursor to %d", pasted.selected)
	}
}

func TestUnbracketedLongTextInputIsNotTruncated(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.inputMode = "radio", "url"
	m.input.Focus()
	url := "https://www.getsubwave.com/stream.mp3"
	next, _ := m.Update(tea.KeyPressMsg{Text: url})
	m = next.(Model)
	if got := m.input.Value(); got != url {
		t.Fatalf("input URL = %q, want %q", got, url)
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
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
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
	if m.view != "Favorites" {
		t.Fatalf("view = %q, want Favorites", m.view)
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
	next, _ := m.selectView(4)
	m = next.(Model)
	if m.view != "Presets" {
		t.Fatalf("selectView(4) = %q, want Presets", m.view)
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
