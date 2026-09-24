package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"errors"
	"fmt"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/theme"
	"strings"
	"testing"
	"time"
)

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
	if len(f.trending) != 1 || f.trending[0].source != "audius" || f.trending[0].kind != "all" {
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
	m.overlaySelected = indexOf(m.sourceChoices(), "radio")
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
	// The hint is action-oriented: "0 Up Next" read as navigation and two
	// rounds of participants missed that the queue is editable (batch
	// 2026-09-23-polish p1 + 2026-09-24 r1-recheck).
	if !strings.Contains(footer, "0 edit queue") {
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

func TestRecentLocalViewsDoNotEnterLoadingState(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Home"
	next, cmd := m.selectView(2) // Radio Recent
	local := next.(Model)
	if cmd != nil || local.loading || local.view != "Recent" {
		t.Fatalf("local Radio view = loading=%v view=%q cmd=%v", local.loading, local.view, cmd != nil)
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

func TestAppleMusicFavoritesAreAHomeSection(t *testing.T) {
	m, f, _ := newModel(t)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song One", Artist: "Artist", URL: "https://music.apple.com/song/s1"}
	playlist := core.Item{Kind: "playlist", ID: "p1", Title: "Road Trip"}
	seedFavorite(&m, "apple-music", song)
	seedFavorite(&m, "apple-music", playlist)

	m.items = homeItems("apple-music", core.PlaybackState{}, "", nil, nil, nil, nil, m.activity.FavoritesFor("apple-music"), true)
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

func TestHomeSectionsOmitEmptyAndContinueOpensQueue(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", QueueIndex: 1, Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}}, Track: &core.Item{Title: "B"}}
	recent := []core.Item{{Kind: "song", ID: "s1", Title: "Song One"}}
	items := homeItems("apple-music", m.state, "Mix", nil, nil, recent, nil, nil, true)
	if !hasHeader(items, "Continue Playing") || !hasHeader(items, "Recently Played") || !hasHeader(items, "Go to") || items[1].Kind != "continue" {
		t.Fatalf("home items = %#v", items)
	}
	m.items, m.selected = items, 1
	next, cmd := m.activate()
	m = next.(Model)
	if cmd != nil || !m.queueFocus || m.queueCursor != 1 || m.selected != 1 {
		t.Fatalf("continue = focus=%v cursor=%d selected=%d", m.queueFocus, m.queueCursor, m.selected)
	}
	if items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil, nil, true); !hasHeader(items, "Go to") {
		t.Fatalf("empty home missing Go to entries = %#v", items)
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
	m = runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist() })
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
	// The favorite marker is a prefix on every row, so a long title can never
	// truncate it away (batch 2026-09-23-postaudit H1).
	if !strings.Contains(lines[0], "★ Example FM") {
		t.Fatalf("playing favorited row text = %q", lines[0])
	}
	// The highlighted row is one flat style, so its only reset is the one that
	// closes the row itself.
	if got := strings.Count(lines[0], "\x1b[0m"); got > 1 {
		t.Fatalf("row highlight is interrupted by nested styles (%d resets): %q", got, lines[0])
	}
}

// Every Up Next label starts in the same terminal column: the state marker
// occupies the reserved 2-cell column instead of being appended after it, so a
// row gaining `▶` or `·` never shifts its text sideways
// (docs/ui/design-system.md §4).
func TestQueueRailKeepsLabelsInOneColumn(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 24
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 2,
		Queue: []core.Item{{Kind: "song", Title: "One"}, {Kind: "song", Title: "Two"},
			{Kind: "song", Title: "Three"}, {Kind: "song", Title: "Four"}}}
	rows := m.queueLines(40, 4)
	if len(rows) != 4 {
		t.Fatalf("rail rows = %d, want 4", len(rows))
	}
	// Columns are measured in terminal cells, not runes: `·` and `▶` are glyphs.
	column := func(row, label string) int {
		text := plainText(row)
		index := strings.Index(text, label)
		if index < 0 {
			t.Fatalf("row %q has no label %q", text, label)
		}
		return lipgloss.Width(text[:index])
	}
	labels := []string{"One", "Two", "Three", "Four"}
	want := column(rows[0], labels[0])
	if want == 0 {
		t.Fatalf("label column = 0, the rail reserves a marker column:\n%s", strings.Join(rows, "\n"))
	}
	for i, row := range rows {
		if got := column(row, labels[i]); got != want {
			t.Fatalf("row %d label column = %d, want %d (a marker must not indent the label):\n%s",
				i, got, want, strings.Join(rows, "\n"))
		}
	}
}

// One composer decides what focus and playback mean, so a row cannot end up with
// two fills, a hidden cursor, or a playing row that looks selected.
func TestListRowComposesCapabilities(t *testing.T) {
	m, _, _ := newModel(t)
	fill := fillParams(m.renderer.selection)
	green := sgrParams(m.renderer.currentStyle)
	dim := sgrParams(m.renderer.dimStyle)
	if fill == "" || green == "" || dim == "" {
		t.Fatalf("test theme lacks the colours under test: fill=%q green=%q dim=%q", fill, green, dim)
	}
	cases := []struct {
		name                            string
		state                           rowState
		wantsGutter, wantsFill, wantsOn bool
		wantsGreen, wantsMuted          bool
		wantPrefix                      string
	}{
		{name: "plain", state: rowState{}},
		{name: "focused", state: rowState{focused: true}, wantsGutter: true, wantsFill: true},
		{name: "playing", state: rowState{playing: true}, wantsOn: true, wantsGreen: true, wantPrefix: "▶ "},
		{name: "playing+focused", state: rowState{focused: true, playing: true}, wantsGutter: true, wantsFill: true, wantsOn: true, wantsGreen: true, wantPrefix: "▶ "},
		{name: "played", state: rowState{played: true}, wantsOn: true, wantsMuted: true, wantPrefix: "· "},
		{name: "played+focused", state: rowState{focused: true, played: true}, wantsGutter: true, wantsFill: true, wantsOn: true, wantPrefix: "· "},
	}
	for _, tc := range cases {
		row := m.listRow("plain", "plain", tc.state, 40)
		if got := strings.Contains(row, "› "); got != tc.wantsGutter {
			t.Errorf("%s: gutter = %v, want %v: %q", tc.name, got, tc.wantsGutter, row)
		}
		if got := strings.Contains(row, fill); got != tc.wantsFill {
			t.Errorf("%s: fill = %v, want %v: %q", tc.name, got, tc.wantsFill, row)
		}
		if got := strings.Contains(row, green); got != tc.wantsGreen {
			t.Errorf("%s: playing token = %v, want %v: %q", tc.name, got, tc.wantsGreen, row)
		}
		if got := strings.Contains(row, dim); got != tc.wantsMuted {
			t.Errorf("%s: muted = %v, want %v: %q", tc.name, got, tc.wantsMuted, row)
		}
		if got := strings.Contains(plainText(row), tc.wantPrefix); tc.wantPrefix != "" && !got {
			t.Errorf("%s: missing state prefix %q: %q", tc.name, tc.wantPrefix, plainText(row))
		}
	}
}

// The main list and the Up Next rail must not grow their own row rules again:
// with the same capabilities both surfaces produce the same state markers.
func TestBothListSurfacesShareTheRowGrammar(t *testing.T) {
	fill := func(m Model) string { return fillParams(m.renderer.selection) }
	for _, surface := range []string{"main", "rail"} {
		for _, focused := range []bool{false, true} {
			m, _, _ := newModel(t)
			m.source, m.view, m.title = "radio", "Browse", "Browse"
			m.width, m.height = 120, 24
			m.loading = false
			m.items = []core.Item{
				{Kind: "stream", URL: "https://radio.example/one", Title: "One"},
				{Kind: "stream", URL: "https://radio.example/two", Title: "Two"},
			}
			m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, QueueIndex: 1,
				Track: &m.items[1], Queue: []core.Item{m.items[0], m.items[1]}}
			var row string
			if surface == "main" {
				// Cursor on row 0 while row 1 plays, so both capabilities are visible.
				m.selected, m.queueFocus = 0, !focused
				row = m.listLines(120, 3)[0]
			} else {
				m.queueFocus, m.queueCursor = focused, 0
				row = m.queueLines(40, 2)[0]
			}
			if got := strings.Contains(row, "› "); got != focused {
				t.Errorf("%s focused=%v: cursor = %v: %q", surface, focused, got, row)
			}
			if got := strings.Contains(row, fill(m)); got != focused {
				t.Errorf("%s focused=%v: fill = %v: %q", surface, focused, got, row)
			}
		}
	}
}

// Focus owns the fill and playback owns the text colour, so a cursor row and a
// playing row never render as the same state (docs/ui/design-system.md §4).
func TestPlaybackSetsTextColourAndFocusSetsTheFill(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Browse"
	m.width, m.height = 120, 24
	m.loading = false
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/one", Title: "One"},
		{Kind: "stream", URL: "https://radio.example/two", Title: "Two"},
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &m.items[1]}
	fill := fillParams(m.renderer.selection)
	green := sgrParams(m.renderer.currentStyle)
	if fill == "" || green == "" {
		t.Fatalf("test theme lacks the colours under test: fill=%q green=%q", fill, green)
	}

	// Cursor on row 0, audio on row 1: exactly one filled row, and the playing
	// row says what it is with colour and glyph instead of a fill.
	m.selected = 0
	rows := m.listLines(120, 3)
	if !strings.Contains(rows[0], fill) || !strings.Contains(rows[0], "› ") {
		t.Fatalf("cursor row lost its focus fill: %q", rows[0])
	}
	if strings.Contains(rows[1], fill) {
		t.Fatalf("playing row took the cursor fill: %q", rows[1])
	}
	if !strings.Contains(rows[1], green) || !strings.Contains(plainText(rows[1]), "▶ Two") {
		t.Fatalf("playing row lost its playing token: %q", rows[1])
	}

	// Cursor on the playing row: both signals survive, neither replaces the other.
	m.selected = 1
	rows = m.listLines(120, 3)
	if !strings.Contains(rows[1], fill) {
		t.Fatalf("playing row under the cursor lost the focus fill: %q", rows[1])
	}
	if !strings.Contains(plainText(rows[1]), "›  ▶ Two") {
		t.Fatalf("playing row under the cursor lost a marker: %q", plainText(rows[1]))
	}
	if !strings.Contains(rows[1], green) {
		t.Fatalf("playing row under the cursor lost the playing token: %q", rows[1])
	}
}

// Two panes must not both look selected: the unfocused list keeps its rows and
// their markers but drops the cursor glyph and the fill.
func TestOnlyActivePanelPaintsTheCursor(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Browse"
	m.width, m.height = 120, 24
	m.loading = false
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/one", Title: "One"},
		{Kind: "stream", URL: "https://radio.example/two", Title: "Two"},
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, QueueIndex: 1,
		Track: &m.items[1], Queue: []core.Item{m.items[0], m.items[1]}}
	fill := fillParams(m.renderer.selection)

	m.selected = 0
	m.queueFocus, m.queueCursor = false, 0
	main := strings.Join(m.listLines(120, 3), "\n")
	rail := strings.Join(m.queueLines(40, 3), "\n")
	if !strings.Contains(main, "› ") || !strings.Contains(main, fill) {
		t.Fatalf("active main list has no cursor:\n%s", main)
	}
	if strings.Contains(rail, "› ") || strings.Contains(rail, fill) {
		t.Fatalf("unfocused rail kept a cursor or a fill:\n%s", rail)
	}

	m.queueFocus, m.queueCursor = true, 0
	main = strings.Join(m.listLines(120, 3), "\n")
	rail = strings.Join(m.queueLines(40, 3), "\n")
	if strings.Contains(main, "› ") || strings.Contains(main, fill) {
		t.Fatalf("focused rail left the main list looking selected:\n%s", main)
	}
	if !strings.Contains(rail, "› ") || !strings.Contains(rail, fill) {
		t.Fatalf("focused rail has no cursor:\n%s", rail)
	}
}

// A cursor on played history keeps the fill and the `·` glyph: muted text under
// the fill would hide the exact row the user is pointing at.
func TestQueueCursorOnPlayedRowKeepsFillAndGlyph(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 24
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 2,
		Queue: []core.Item{{Kind: "song", Title: "One"}, {Kind: "song", Title: "Two"}, {Kind: "song", Title: "Three"}}}
	fill := fillParams(m.renderer.selection)
	m.queueFocus, m.queueCursor = true, 0
	rows := m.queueLines(40, 3)
	if !strings.Contains(rows[0], fill) {
		t.Fatalf("cursor on a played row has no fill: %q", rows[0])
	}
	if !strings.Contains(plainText(rows[0]), "›  · One") {
		t.Fatalf("cursor on a played row lost its markers: %q", plainText(rows[0]))
	}
	if dim := sgrParams(m.renderer.dimStyle); dim != "" && strings.Contains(rows[0], dim) {
		t.Fatalf("cursor row is muted under its own fill: %q", rows[0])
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

// p plays the whole playlist, and S toggles shuffle independently of it: the
// order the playlist starts in follows the shuffle state instead of S
// restarting playback (docs/product/open-questions.md OQ18).
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
	if !m.state.Shuffle {
		t.Fatalf("S did not enable shuffle: %+v", m.state)
	}
	if f.played.Shuffle != nil {
		t.Fatalf("S restarted playback instead of toggling: %#v", f.played)
	}

	next, cmd = m.handleKey(runeKey('S'))
	m = next.(Model)
	m = run(m, cmd)
	if m.state.Shuffle {
		t.Fatalf("S did not disable shuffle again: %+v", m.state)
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
	runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist() })
	if !f.played.Reverse {
		t.Fatal("Favorite Songs request did not set Reverse")
	}

	m.title = "Regular playlist"
	runMutation(m, func(next *Model) tea.Cmd { return next.playPlaylist() })
	if f.played.Reverse {
		t.Fatal("regular playlist request set Reverse")
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

func TestLoadingOnPush(t *testing.T) {
	m, _, _ := newModel(t)
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Title: "My Playlist"}}
	next, _ := m.activate()
	m = next.(Model)
	if !m.loading || len(m.items) != 0 || len(m.history) != 1 {
		t.Fatalf("loading=%v items=%d history=%d", m.loading, len(m.items), len(m.history))
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

// The Now Playing account row must follow the live authorization snapshot: the
// server republishes it when an Apple session settles at boot or after sign-in,
// and a row stuck on the first snapshot showed "not signed in" forever.
func TestAccountRowFollowsLiveAuthorization(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.source = "apple-music"
	m.account = "Account: not signed in — previews only (:auth)"
	m.sourceAuth = core.AuthorizationStatus{Status: "not_determined"}
	if view := plainText(m.View().Content); !strings.Contains(view, "not signed in") {
		t.Fatalf("the warning is missing before the snapshot settles:\n%s", view)
	}

	// The server settles the session and republishes.
	// Sequence must advance: a stale kind is dropped by the ordering rule.
	updated, _ := m.applyWatchUpdate(api.WatchUpdate{
		Kind:          "authorization.changed",
		Sequence:      m.sequence + 1,
		Authorization: &api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthAuthorized},
	})
	settled := updated.(Model)
	if view := plainText(settled.View().Content); strings.Contains(view, "not signed in") {
		t.Fatalf("the warning survived a settled authorization:\n%s", view)
	}

	// And a signed-out snapshot brings it back, in the current wording.
	signedOut, _ := settled.applyWatchUpdate(api.WatchUpdate{
		Kind:          "authorization.changed",
		Sequence:      settled.sequence + 1,
		Authorization: &api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthNotDetermined},
	})
	if view := plainText(signedOut.(Model).View().Content); !strings.Contains(view, "previews only") {
		t.Fatalf("a signed-out snapshot did not restore the warning:\n%s", view)
	}
}

// The live snapshot carries the account conclusion in Apple's namespaced
// details; the account row must warn from it directly instead of collapsing
// every "authorized" event to "Account: ready". The stale m.account stays
// wrong on purpose: only the live projection may answer here.
func TestAccountRowWarnsFromLiveAuthorizationDetails(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.source = "apple-music"
	m.account = "Account: not signed in — previews only (:auth)"

	live := func(model Model, status string, details map[string]any) Model {
		next, _ := model.applyWatchUpdate(api.WatchUpdate{
			Kind:          "authorization.changed",
			Sequence:      model.sequence + 1,
			Authorization: &api.SourceAuthorization{Source: api.SourceAppleMusic, Status: status, Details: details},
		})
		return next.(Model)
	}

	// authorized + subscription_required: the warning the fallback used to hide.
	m = live(m, api.AuthAuthorized, map[string]any{"accountStatus": "subscription_required", "canPlayCatalogContent": false, "hasCloudLibraryEnabled": true})
	if m.sourceAuth.AccountStatus != "subscription_required" || m.sourceAuth.CanPlayCatalogContent {
		t.Fatalf("subscription_required not projected: %+v", m.sourceAuth)
	}
	if view := plainText(m.View().Content); !strings.Contains(view, "subscription is required") {
		t.Fatalf("subscription warning missing from Now Playing:\n%s", view)
	}
	if got := m.accountWarning(); !strings.Contains(got, "subscription is required") {
		t.Fatalf("account summary = %q", got)
	}

	// authorized + cloud_library_disabled: Sync Library guidance, immediately.
	m = live(m, api.AuthAuthorized, map[string]any{"accountStatus": "cloud_library_disabled", "canPlayCatalogContent": true, "hasCloudLibraryEnabled": false})
	if m.sourceAuth.AccountStatus != "cloud_library_disabled" || m.sourceAuth.HasCloudLibraryEnabled {
		t.Fatalf("cloud_library_disabled not projected: %+v", m.sourceAuth)
	}
	if view := plainText(m.View().Content); !strings.Contains(view, "Sync Library") || strings.Contains(view, "subscription is required") {
		t.Fatalf("cloud library warning did not replace the subscription one:\n%s", view)
	}
	if got := m.accountWarning(); !strings.Contains(got, "Sync Library") {
		t.Fatalf("account summary = %q", got)
	}

	// authorized + ready: no warning claims a limit, and the live summary
	// settles to silence (the Account overlay's row carries the positive
	// "authorized" wording instead of the retired toast).
	m = live(m, api.AuthAuthorized, map[string]any{"accountStatus": "ready", "canPlayCatalogContent": true, "hasCloudLibraryEnabled": true})
	if m.sourceAuth.AccountStatus != "ready" || !m.sourceAuth.CanPlayCatalogContent || !m.sourceAuth.HasCloudLibraryEnabled {
		t.Fatalf("ready conclusion not projected: %+v", m.sourceAuth)
	}
	if view := plainText(m.View().Content); strings.Contains(view, "Sync Library") || strings.Contains(view, "subscription is required") || strings.Contains(view, "not signed in") {
		t.Fatalf("warnings survived a settled ready account:\n%s", view)
	}
	if got := m.accountWarning(); got != "" {
		t.Fatalf("account summary survived a settled ready account: %q", got)
	}

	// not_determined: the signed-out wording returns with the event.
	m = live(m, api.AuthNotDetermined, nil)
	if m.sourceAuth.Status != api.AuthNotDetermined || m.sourceAuth.AccountStatus != "" {
		t.Fatalf("signed-out projection = %+v", m.sourceAuth)
	}
	if view := plainText(m.View().Content); !strings.Contains(view, "not signed in") {
		t.Fatalf("signed-out warning missing from Now Playing:\n%s", view)
	}
	if got := m.accountWarning(); !strings.Contains(got, "not signed in") {
		t.Fatalf("account summary = %q", got)
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
	m, f, _ := newModel(t)
	m.source = "apple-music"
	m.descriptors = nil // sources.list not yet received
	m.cache = map[string][]core.Item{}
	msg, ok := m.loadHome()().(homeMsg)
	if !ok {
		t.Fatal("loadHome did not return homeMsg")
	}
	found := false
	recommended := false
	for _, item := range msg.items {
		if item.Kind == "header" && item.Title == "Your Playlists" {
			found = true
		}
		if item.Kind == "header" && item.Title == "Recommended" {
			recommended = true
		}
	}
	if !found {
		t.Fatalf("Home dropped the library preview before capabilities arrived: %#v", msg.items)
	}
	if !recommended || len(f.recommendations) != 1 {
		t.Fatalf("Home dropped recommendations before capabilities arrived: items=%#v calls=%v", msg.items, f.recommendations)
	}
}

func TestHomeRecommendationsAreCapabilityGated(t *testing.T) {
	m, f, _ := newModel(t)
	for i := range m.descriptors {
		if m.descriptors[i].ID == api.SourceAppleMusic {
			delete(m.descriptors[i].Capabilities, api.CapRecommendations)
		}
	}
	msg := m.loadHome()().(homeMsg)
	if len(f.recommendations) != 0 {
		t.Fatalf("recommendations requested without capability: %v", f.recommendations)
	}
	if hasHeader(msg.items, "Recommended") {
		t.Fatalf("Recommended rendered without capability: %#v", msg.items)
	}
}

func TestHomeHidesRecommendationErrors(t *testing.T) {
	m, f, _ := newModel(t)
	f.recommendationsErr = errors.New("signed out")
	msg := m.loadHome()().(homeMsg)
	if len(f.recommendations) != 1 {
		t.Fatalf("recommendation calls = %v, want one", f.recommendations)
	}
	if hasHeader(msg.items, "Recommended") {
		t.Fatalf("Recommended survived an empty/error response: %#v", msg.items)
	}
}

func TestAppleHomeHasAllPlaylistsEntry(t *testing.T) {
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil, nil, true)
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

// S in an album detail page toggles shuffle like everywhere else. It used to
// restart the album shuffled, which made the key one-way: pressing it again
// could not turn shuffle off (docs/product/open-questions.md OQ18).
func TestAlbumDetailShuffleToggles(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.items = []core.Item{{Kind: "song", ID: "a1"}}
	m.selected = 0

	next, cmd := m.handleKey(runeKey('S'))
	m = run(next.(Model), cmd)
	if !m.state.Shuffle {
		t.Fatalf("S did not enable shuffle: %+v", m.state)
	}
	if f.played.Kind != "" {
		t.Fatalf("S restarted playback instead of toggling: %#v", f.played)
	}

	next, cmd = m.handleKey(runeKey('S'))
	m = run(next.(Model), cmd)
	if m.state.Shuffle {
		t.Fatalf("S did not disable shuffle again: %+v", m.state)
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
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil, nil, true)
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
	// The empty page must teach how the first favorite is created, not just
	// show a bare "(empty)" (batch 2026-09-22-jamendo-tui OQ25).
	if text := m.emptyText(); !strings.Contains(text, "press f to favorite") {
		t.Fatalf("empty favorites text = %q", text)
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

// S toggles shuffle both ways. The fake engine mirrors the state it is given,
// so this pins the client half of the contract: press once for on, again for
// off (docs/product/open-questions.md OQ18 — the real MusicKit path reports it
// stays on).
func TestShuffleKeyTogglesBackOff(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 32

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 'S', Text: "S"})
	m = drainAll(next.(Model), cmd)
	if !m.state.Shuffle {
		t.Fatalf("first S did not enable shuffle: %+v", m.state)
	}

	next, cmd = m.handleKey(tea.KeyPressMsg{Code: 'S', Text: "S"})
	m = drainAll(next.(Model), cmd)
	if m.state.Shuffle {
		t.Fatalf("second S did not disable shuffle: %+v", m.state)
	}
}

// A play keeps the shuffle/repeat the user turned on. The server starts every
// playback from a known form (an omitted parameter means off), so a play that
// does not send the current form silently clears the toggle.
func TestPlayCarriesTheCurrentForm(t *testing.T) {
	m, f, _ := newModel(t)
	m.state.Shuffle = true
	m.state.Repeat = "all"
	item := core.Item{Kind: "song", ID: "s1", Ref: "apple-music:song:s1"}

	runMutation(m, func(next *Model) tea.Cmd { return next.playItem(item) })
	if f.played.Shuffle == nil || !*f.played.Shuffle {
		t.Fatalf("play request dropped shuffle: %#v", f.played)
	}
	if f.played.Repeat != "all" {
		t.Fatalf("play request dropped repeat: %#v", f.played)
	}

	runMutation(m, func(next *Model) tea.Cmd { return next.playSongsFrom([]string{"apple-music:song:s1"}, item) })
	if f.playForm.Shuffle == nil || !*f.playForm.Shuffle || f.playForm.Repeat != "all" {
		t.Fatalf("playSongs dropped the form: %#v", f.playForm)
	}

	// With shuffle off the request stays plain: the server default is off.
	m.state.Shuffle = false
	m.state.Repeat = "off"
	runMutation(m, func(next *Model) tea.Cmd { return next.playItem(item) })
	if f.played.Shuffle != nil || f.played.Repeat != "" {
		t.Fatalf("plain play should not send a form: %#v", f.played)
	}
}
