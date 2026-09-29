package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/theme"
	"reflect"
	"strings"
	"testing"
)

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

func TestPaletteSourceUsesServerDescriptorCatalog(t *testing.T) {
	m, _, _ := newModel(t)
	m.descriptors = append(m.descriptors, api.SourceDescriptor{
		ID: "custom", Available: true,
		Capabilities: map[string]api.Capability{api.CapPlaybackStream: {Available: true}},
	})
	next, cmd := m.runPaletteCommand("source custom")
	m = run(next.(Model), cmd)
	if m.source != "custom" {
		t.Fatalf("source = %q, want descriptor-provided custom source", m.source)
	}
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
	m.overlaySelected = indexOf(m.sourceChoices(), "radio")
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "radio" || m.view != "Home" || len(m.history) != 0 || m.filter != "" || len(m.cache) != 0 {
		t.Fatalf("source cleanup failed: source=%q view=%q history=%d filter=%q cache=%#v", m.source, m.view, len(m.history), m.filter, m.cache)
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

func TestJamendoSurfacesIncludeSongOnlyDiscover(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.switchSource("jamendo")
	m = next.(Model)
	if m.source != "jamendo" || strings.Join(m.views(), ",") != "Home,Discover,Recent" {
		t.Fatalf("Jamendo surfaces = source=%q views=%v", m.source, m.views())
	}
	if got := sourceTitle("jamendo"); got != "Jamendo" {
		t.Fatalf("source title = %q", got)
	}
	seedRecent(&m, "jamendo", core.Item{Kind: "song", ID: "t1", Ref: "jamendo:song:t1", Title: "Track"})
	m.view, m.title = "Recent", "Recent"
	message := m.loadView()()
	list, ok := message.(listMsg)
	if !ok || len(list.items) != 1 || list.items[0].Ref != "jamendo:song:t1" {
		t.Fatalf("Jamendo recent = %#v", message)
	}
}

func TestJamendoDiscoverLoadsSongsWithoutPlaylistTrending(t *testing.T) {
	m, f, _ := newModel(t)
	next, _ := m.switchSource("jamendo")
	m = next.(Model)
	m.view, m.title = "Discover", "Discover"
	message := m.loadView()()
	list, ok := message.(listMsg)
	if !ok || list.err != nil {
		t.Fatalf("Jamendo Discover failed: %#v", message)
	}
	// Discover issues one "all" request; the server returns only the songs
	// group because Jamendo declares the kind-specific capability.
	if len(f.trending) != 1 || f.trending[0].source != "jamendo" || f.trending[0].kind != "all" {
		t.Fatalf("trending calls = %#v", f.trending)
	}
	if !hasHeader(list.items, "Trending Songs") || hasHeader(list.items, "Trending Playlists") {
		t.Fatalf("Jamendo Discover groups = %#v", list.items)
	}
}

func TestJamendoPlaylistOpensDetail(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "jamendo"
	m.items = []core.Item{{Kind: "playlist", ID: "p1", Ref: "jamendo:playlist:p1", Title: "List"}}
	next, _ := m.activate()
	m = next.(Model)
	if m.detailKind != "playlist" || m.detailID != "p1" || m.pageClass != pageClassContainer {
		t.Fatalf("Jamendo playlist activation = kind=%q id=%q class=%q", m.detailKind, m.detailID, m.pageClass)
	}
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

func TestHomeSectionsAreSummaries(t *testing.T) {
	many := make([]core.Item, 12)
	for i := range many {
		many[i] = core.Item{Kind: "song", ID: fmt.Sprint(i), Title: fmt.Sprintf("Item %d", i)}
	}
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", many, many, many, many, many, true)
	counts := map[string]int{}
	section := ""
	for _, item := range items {
		if item.Kind == "header" {
			section = item.Title
			continue
		}
		counts[section]++
	}
	for _, section := range []string{"Recommended", "Trending", "Recently Played", "Your Playlists", "Favorites"} {
		if counts[section] != 5 {
			t.Fatalf("%s count = %d, want 5", section, counts[section])
		}
	}
	audius := homeItems("audius", core.PlaybackState{Status: "stopped"}, "", nil, many, many, many, many, true)
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

func TestHomeSectionOrderFollowsListenNowHierarchy(t *testing.T) {
	item := []core.Item{{Kind: api.KindSong, ID: "1", Title: "One"}}
	playback := core.PlaybackState{
		Status: "playing", QueueIndex: 0, Queue: item, Track: &item[0],
	}
	items := homeItems("apple-music", playback, "Queue", item, item, item, item, item, true)
	var headers []string
	for _, row := range items {
		if row.Kind == "header" {
			headers = append(headers, row.Title)
		}
	}
	want := []string{"Continue Playing", "Recommended", "Trending", "Recently Played", "Your Playlists", "Favorites", "Go to"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("headers = %q, want %q", headers, want)
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
		{"jamendo", []string{"Recently Played", "Trending", "Favorites", "Go to"}, []string{"Your Playlists"}},
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
			items := homeItems(test.source, core.PlaybackState{Status: "stopped"}, "", nil, trending, []core.Item{item}, playlists, []core.Item{item}, test.source != "radio")
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

func TestSourceSwitcherShowsAvailabilityAndCapabilities(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.overlay = "source-switcher"
	m.overlaySelected = indexOf(m.sourceChoices(), "audius")
	view := plainText(m.View().Content)
	for _, want := range []string{"Switch source", "Apple Music", "Audius", "Jamendo", "Radio", "ready", "full", "queue", "browse"} {
		if !strings.Contains(view, want) {
			t.Fatalf("switcher missing %q:\n%s", want, view)
		}
	}
	// Reverse video alone is invisible in plain text and for color-deficient
	// readers; the selected row carries the lists' › marker (four
	// rounds hit this).
	if !strings.Contains(view, "› Audius") {
		t.Fatalf("selected source has no › marker:\n%s", view)
	}
}

func TestDescriptorArrivalRegatesPendingHome(t *testing.T) {
	m, _, _ := newModel(t)
	m.descriptors = nil
	m.loading = true
	message := homeMsg{generation: m.generation, destination: m.destination(), items: []core.Item{{Kind: "header", Title: "Recommended"}, {Kind: "playlist", ID: "rec", Title: "Recommendation"}, {Kind: "header", Title: "Trending"}, {Kind: "song", ID: "trend", Title: "Trend"}, {Kind: "header", Title: "Your Playlists"}, {Kind: "playlist", ID: "list", Title: "List"}}}
	next, _ := m.Update(sourcesMsg{descriptors: []api.SourceDescriptor{{ID: api.SourceAppleMusic, Available: true, Capabilities: map[string]api.Capability{}}}})
	m = next.(Model)
	next, _ = m.Update(message)
	m = next.(Model)
	if hasHeader(m.items, "Recommended") || hasHeader(m.items, "Trending") || hasHeader(m.items, "Your Playlists") {
		t.Fatalf("stale Home capability rows survived descriptor gate: %#v", m.items)
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
	if got := sourceAccountSummary("jamendo", core.AuthorizationStatus{}); got != "Account: not required" {
		t.Fatalf("jamendo = %q", got)
	}
}

func TestAuthEntryUsesCurrentSource(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m.source = "audius"
	m.sourceAuth = core.AuthorizationStatus{Status: "authorized", AccountLabel: "guocai"}
	m.items = []core.Item{{Kind: "entry-account", Title: "Account"}}
	m.selected = 0
	next, cmd := m.activate()
	m = next.(Model)
	if m.overlay != "auth" {
		t.Fatalf("Home Account entry did not open the auth overlay: overlay=%q", m.overlay)
	}
	// The cursor starts on the browsing source's row, so the entry's context
	// (the current source) carries over instead of always landing on row 0.
	if m.authSelected != indexOf(m.authRowSources(), "audius") {
		t.Fatalf("auth cursor = %d, want the audius row", m.authSelected)
	}
	m = drainAuthFetch(t, m, cmd)
	if remote.listCalls != 1 {
		t.Fatalf("authorization.list calls = %d", remote.listCalls)
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
	items := homeItems("audius", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil, nil, true)
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

func TestAppleHomeHasAlbumsEntry(t *testing.T) {
	items := homeItems("apple-music", core.PlaybackState{Status: "stopped"}, "", nil, nil, nil, nil, nil, true)
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

	// Jamendo supports only songs and playlists; it must never receive an
	// album request or borrow Audius's Discover capability.
	m3, f3, _ := newModel(t)
	m3.source = "jamendo"
	m3 = run(m3, m3.searchSource("album query"))
	kinds = kinds[:0]
	for _, call := range f3.searches {
		if call.source != "jamendo" {
			t.Fatalf("Jamendo search used source %q", call.source)
		}
		kinds = append(kinds, call.kind)
	}
	if strings.Join(kinds, ",") != "song,playlist" {
		t.Fatalf("Jamendo search kinds = %v", kinds)
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

// A query-result page plays only the pointed row: a search hit reads as a song
// to play, not as a queue to start. Chaining stays explicit per row with e/E.
func TestAggregateSongEnterPlaysOnlyThatRow(t *testing.T) {
	m, f, _ := newModel(t)
	m = nextModel(m.pushAggregate("Search: bohemian", nil))
	m.items = []core.Item{
		{Kind: "header", Title: "Songs"},
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"},
		{Kind: "song", ID: "s2", Ref: "apple-music:song:s2", Title: "Two"},
		{Kind: "song", ID: "s3", Ref: "apple-music:song:s3", Title: "Three"},
	}
	m.selected = 2
	next, cmd := m.activate()
	m = run(next.(Model), cmd)
	if cmd == nil {
		t.Fatalf("aggregate Enter on a song produced no command")
	}
	if f.played.ID != "s2" || f.playSongSet != nil {
		t.Fatalf("aggregate play = %#v (playSongs %#v), want a single-row play", f.played, f.playSongSet)
	}
	if m.queueSource != (queueContext{}) {
		t.Fatalf("aggregate play set a queue origin: %#v", m.queueSource)
	}
}

// The pushed page's context row names the group the cursor is in and the [/]
// jump, so playlists and albums after a wall of songs can be found without the
// footer hint (batch 2026-09-23-postaudit M3).
func TestResultGroupContextNamesGroups(t *testing.T) {
	m, _, _ := newModel(t)
	m = nextModel(m.pushAggregate("Search: lofi", nil))
	m.items = []core.Item{
		{Kind: "header", Title: "Songs"},
		{Kind: "song", ID: "s1", Title: "One"},
		{Kind: "header", Title: "Albums"},
		{Kind: "album", ID: "a1", Title: "Album"},
		{Kind: "header", Title: "Playlists"},
		{Kind: "playlist", ID: "p1", Ref: "apple-music:playlist:p1", Title: "List"},
	}
	m.selected = 1
	if got := m.resultGroupContext(); got != "Songs 1/3 · [/] group" {
		t.Fatalf("group context = %q", got)
	}
	m.selected = 5
	if got := m.resultGroupContext(); got != "Playlists 3/3 · [/] group" {
		t.Fatalf("group context on last group = %q", got)
	}
	m.selected = 3
	if got, want := m.resultGroupContext(), "Albums 2/3 · [/] group"; got != want {
		t.Fatalf("group context = %q, want %q", got, want)
	}
	// A single-group page has nothing to jump between.
	m.items = []core.Item{{Kind: "header", Title: "Songs"}, {Kind: "song", ID: "s1", Title: "One"}}
	if got := m.resultGroupContext(); got != "" {
		t.Fatalf("single group context = %q", got)
	}
}

func nextModel(model tea.Model, _ tea.Cmd) Model {
	return model.(Model)
}
