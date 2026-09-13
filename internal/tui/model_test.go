package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	tea "github.com/charmbracelet/bubbletea"
)

type fake struct {
	state    core.PlaybackState
	played   core.PlaybackRequest
	radioURL string
	tracks   []core.Item
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
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "One"}}
	return nil
}
func (f *fake) Pause(context.Context) error                       { f.state.Status = "paused"; return nil }
func (f *fake) Resume(context.Context) error                      { f.state.Status = "playing"; return nil }
func (f *fake) Next(context.Context) error                        { return nil }
func (f *fake) Previous(context.Context) error                    { return nil }
func (f *fake) State(context.Context) (core.PlaybackState, error) { return f.state, nil }
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
		Radio:         radio.New(),
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
	m = run(m, m.Init())
	if m.title != "Playlists" || len(m.items) != 1 || m.loading {
		t.Fatalf("title=%q items=%d loading=%v", m.title, len(m.items), m.loading)
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
}

func TestHelpOverlay(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey('?'))
	m = next.(Model)
	if m.overlay != "help" || !strings.Contains(m.View(), "switch source") {
		t.Fatal(m.View())
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

func TestQueuePageOpens(t *testing.T) {
	m, _, _ := newModel(t)
	m.state.Queue = []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}
	m.state.QueueIndex = 1
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if m.title != "Now Playing" || len(m.items) != 2 || m.selected != 1 || len(m.history) != 1 {
		t.Fatalf("title=%q items=%d selected=%d history=%d", m.title, len(m.items), m.selected, len(m.history))
	}
	m = m.back()
	if len(m.history) != 0 {
		t.Fatal("back failed")
	}
}

func TestListResultCachedRegardlessOfView(t *testing.T) {
	m, _, _ := newModel(t)
	m.view = "Recent"
	next, _ := m.Update(listMsg{key: "apple-music/Playlists", title: "Playlists", items: []core.Item{{Title: "P"}}})
	m = next.(Model)
	if len(m.cache["apple-music/Playlists"]) != 1 {
		t.Fatalf("cache = %#v", m.cache)
	}
	if len(m.items) != 0 {
		t.Fatal("items should not change for a non-current view")
	}
}

func TestPlaylistDetailPlaysFromTrack(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "playlist", "p1"
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Track One"}}
	next, cmd := m.activate()
	m = next.(Model)
	m = run(m, cmd)
	if f.played.Kind != "playlist" || f.played.StartTrackID != "s1" || f.played.StartTitle != "Track One" {
		t.Fatalf("playRequest = %#v", f.played)
	}
}

func TestQueuePageRemove(t *testing.T) {
	m, f, _ := newModel(t)
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}, {Kind: "song", ID: "3", Title: "C"}}}
	m.state = f.state
	next, _ := m.handleKey(runeKey('0'))
	m = next.(Model)
	if !m.queuePage || len(m.items) != 3 || m.selected != 1 {
		t.Fatalf("queue page: page=%v items=%d selected=%d", m.queuePage, len(m.items), m.selected)
	}
	next, _ = m.handleKey(runeKey('j'))
	m = next.(Model)
	next, cmd := m.handleKey(runeKey('x'))
	m = next.(Model)
	m = run(m, cmd)
	if len(m.state.Queue) != 2 || len(m.items) != 2 {
		t.Fatalf("after remove queue=%d items=%d", len(m.state.Queue), len(m.items))
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
	if m.view != "Presets" {
		t.Fatalf("view = %q, want Presets", m.view)
	}
	next, _ = m.handleKey(runeKey('2'))
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("view = %q, want Recent", m.view)
	}
}

func TestSourceRemembersLastView(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Playlists"
	next, _ := m.selectView(2)
	m = next.(Model)
	if m.view != "Presets" {
		t.Fatalf("selectView(2) = %q, want Presets", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	next, _ = m.selectView(2)
	m = next.(Model)
	if m.view != "Countries" {
		t.Fatalf("radio selectView(2) = %q, want Countries", m.view)
	}
	next, _ = m.switchSource("apple-music")
	m = next.(Model)
	if m.view != "Presets" {
		t.Fatalf("apple music did not remember Presets: %q", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	if m.view != "Countries" {
		t.Fatalf("radio did not remember Countries: %q", m.view)
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
