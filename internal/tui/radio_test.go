package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/radio"
	"github.com/3-tiao/lilt/internal/state"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitialRadioHomeLoadsDynamically(t *testing.T) {
	f := &fake{}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	m := New(Options{
		Provider:      f,
		Player:        f,
		Radio:         fakeRadio{},
		Store:         store,
		Authorization: core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready"},
		Source:        "radio",
	})
	if m.view != "Home" || !m.loading || len(m.items) != 0 {
		t.Fatalf("initial radio view = view=%q loading=%v items=%d", m.view, m.loading, len(m.items))
	}
	seedFavorite(&m, "radio", core.Item{Kind: "stream", URL: "https://example.test/live", Title: "Local Radio"})
	m = drainAll(m, m.Init())
	if m.loading || !hasHeader(m.items, "Favorites") {
		t.Fatalf("radio Home = loading=%v items=%#v", m.loading, m.items)
	}
}

func TestRadioBrowseSlashAndFBehavior(t *testing.T) {
	if got := strings.Join(radioViews, ","); got != "Home,Browse,Recent" {
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
	if cmd == nil || m.overlay != "" || m.view != "Browse" || m.title != "Showing: Language=Japanese · Genre=City Pop · Country=JP" || m.browseQuery != (radioDiscovery{Language: "Japanese", Tag: "City Pop", CountryCode: "JP"}) {
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
	// Commit Unicode text with no facets pending: the commit Enter applies the
	// query right away (batch 2026-09-23-postaudit M2).
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Text: "東京"})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.view != "Browse" || m.title != "Showing: Text=東京" || m.browseQuery != (radioDiscovery{Term: "東京"}) || m.overlay != "" || m.discoveryTerm != "" {
		t.Fatalf("plain browse query = view=%q title=%q query=%#v overlay=%q", m.view, m.title, m.browseQuery, m.overlay)
	}
	// A facet changes the picture: committing text lands on the action row and
	// one more Enter applies "Search + filters".
	next, _ = m.handleKey(runeKey('/'))
	m = next.(Model)
	m.discoveryPending.Language = "Japanese"
	m.discoverySelected = discoveryText
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "discovery" || m.discoverySelected != discoveryConfirm {
		t.Fatalf("facets pending: commit should land on confirm, got overlay=%q selected=%d", m.overlay, m.discoverySelected)
	}
	next, cmd := m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.view != "Browse" || m.title != "Showing: Text=東京 · Language=Japanese" || m.browseQuery != (radioDiscovery{Language: "Japanese", Term: "東京"}) || m.discoveryTerm != "" || m.loading != true || len(m.items) != 0 {
		t.Fatalf("browse query = view=%q title=%q query=%#v term=%q loading=%v", m.view, m.title, m.browseQuery, m.discoveryTerm, m.loading)
	}
	if len(m.history) != 0 {
		t.Fatalf("browse query pushed a history page: %d", len(m.history))
	}
	m = run(m, cmd)
	if m.loading != false || len(m.items) != 1 || m.title != "Showing: Text=東京 · Language=Japanese" {
		t.Fatalf("browse results = title=%q loading=%v items=%d", m.title, m.loading, len(m.items))
	}
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.view != "Browse" || m.browseQuery != (radioDiscovery{}) || m.title != "Popular Worldwide" {
		t.Fatalf("esc should reset the query in place: view=%q title=%q query=%#v", m.view, m.title, m.browseQuery)
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
	m.source, m.view, m.title = "radio", "Browse", "Showing: Text=city pop"
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

func TestCompactDiscoveryOptionsKeepFilterAndKeysVisible(t *testing.T) {
	for _, kind := range []string{"genre", "country"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.source, m.view, m.overlay, m.discoveryKind = "radio", "Browse", "discovery-options", kind
			m.discoveryQuery = "Jazz"
			for i := 0; i < 25; i++ {
				m.discoveryOptions = append(m.discoveryOptions, core.Item{Title: fmt.Sprintf("Jazz Option %02d", i)})
			}
			for _, selected := range []int{0, 12, 24} {
				m.discoverySelected = selected
				view := plainText(m.overlayView(80, 18))
				for _, want := range []string{"Filter: Jazz", "Typing filters", fmt.Sprintf("› Jazz Option %02d", selected)} {
					if !strings.Contains(view, want) {
						t.Fatalf("%s option %d hid %q:\n%s", kind, selected, want, view)
					}
				}
				if strings.Index(view, "Filter: Jazz") > strings.Index(view, "Typing filters") {
					t.Fatalf("%s status lines out of order:\n%s", kind, view)
				}
			}
		})
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
	if msg.title != "Showing: Text=Tokyo" || len(msg.items) != 1 || r.searchLimit != radioPageSize || r.filter != (radio.Filter{}) {
		t.Fatalf("queried browse = %q %#v filter=%#v limit=%d", msg.title, msg.items, r.filter, r.searchLimit)
	}
	m.browseQuery = radioDiscovery{Language: "Japanese", CountryCode: "JP"}
	msg = m.loadView()().(listMsg)
	if msg.title != "Showing: Language=Japanese · Country=JP" || r.filter != (radio.Filter{Language: "Japanese", CountryCode: "JP"}) {
		t.Fatalf("facet browse = %q filter=%#v", msg.title, r.filter)
	}
}

// The Browse summary carries field labels: a term that doubles as a genre stays
// readable, the fields keep their fixed order, the sort stays its own labeled
// segment, and the longer title still fits the panel row (OQ41).
func TestBrowseTitleLabelsDuplicateTermAndGenre(t *testing.T) {
	if got := (radioDiscovery{Term: "jazz", Tag: "jazz"}).browseTitle(); got != "Showing: Text=jazz · Genre=jazz" {
		t.Fatalf("duplicate term/genre summary = %q", got)
	}
	if got := (radioDiscovery{Term: "東京", Language: "japanese", Tag: "city pop", CountryCode: "JP"}).browseTitle(); got != "Showing: Text=東京 · Language=japanese · Genre=city pop · Country=JP" {
		t.Fatalf("labeled summary = %q", got)
	}
	if got := (radioDiscovery{Tag: "jazz", Sort: "POPULAR"}).browseTitle(); got != "Showing: Genre=jazz · Sort: Popular" {
		t.Fatalf("sort segment = %q", got)
	}
	if got := (radioDiscovery{Sort: "name"}).browseTitle(); got != "Popular Worldwide · Sort: Name" {
		t.Fatalf("sort-only page = %q", got)
	}
	if got := (radioDiscovery{}).browseTitle(); got != "Popular Worldwide" {
		t.Fatalf("default page = %q", got)
	}

	// The labeled summary is longer than the bare values; it must still fit the
	// panel row: the fixed field order keeps the leading fields visible while
	// the tail is ellipsized, and the frame does not grow.
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	m.browseQuery = radioDiscovery{Term: strings.Repeat("jazz ", 16), Language: "german", Tag: "smooth jazz", CountryName: "United States"}
	m.title = m.browseQuery.browseTitle()
	m.loading = false
	m.items = radioPageItems(0, 3)
	m.width, m.height = 80, 24
	view := plainText(m.View().Content)
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Fatalf("long summary grew the frame: %d lines, want %d", lines, m.height)
	}
	var showing string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Showing: Text=") {
			showing = line
			break
		}
	}
	if showing == "" {
		t.Fatalf("summary row missing:\n%s", view)
	}
	if !strings.Contains(showing, "…") {
		t.Fatalf("overlong summary was not ellipsized: %q", showing)
	}
	if strings.Contains(showing, "Country=") {
		t.Fatalf("summary overflowed its row: %q", showing)
	}
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
	if cmd == nil || m.pageOffset != 0 || !m.pageMore || m.pageLoading || m.pageFailed || m.pageKey != "Showing: Text=jazz" {
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
	next, cmd := m.selectView(1)
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
	m.source, m.view, m.title = "radio", "Browse", "Showing: Text=city pop"
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
	next, _ = m.Update(listMsg{generation: m.generation, key: "apple-music/Library", title: "Library", err: errors.New("boom")})
	m = next.(Model)
	if m.message != "Error: boom" {
		t.Fatalf("non-radio errors keep the detail: %q", m.message)
	}
}

func TestRadioBrowseEmptyHintPointsAtSearch(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view = "radio", "Browse"
	if text := m.emptyText(); !strings.Contains(text, "/ to search") {
		t.Fatalf("empty hint = %q", text)
	}
}

func TestRadioDefaultsToHomeOnEntryAndSourceSwitch(t *testing.T) {
	store := &state.Store{}
	radioModel := New(Options{Provider: &fake{}, Player: &fake{}, Radio: &fakeRadio{}, Store: store, Source: "radio"})
	if radioModel.view != "Home" {
		t.Fatalf("initial Radio view = %q, want Home", radioModel.view)
	}
	m, _, _ := newModel(t)
	m.lastView["radio"] = "Browse"
	next, _ := m.switchSource("radio")
	m = next.(Model)
	if m.view != "Home" || m.lastView["radio"] != "Home" {
		t.Fatalf("Radio switch view = %q, last view = %q; want Home", m.view, m.lastView["radio"])
	}
}

func TestWideRadioUsesTheSameNowPlayingDock(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}}
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}}

	view := plainText(m.View().Content)
	if !strings.Contains(view, "┌── FAVORITES") || !strings.Contains(view, "┌── NOW PLAYING") || !strings.Contains(view, "LIVE") {
		t.Fatalf("wide Radio layout missing:\n%s", view)
	}
	if !strings.Contains(view, "Live radio has no finite queue.") {
		t.Fatalf("Radio rail must state its capability:\n%s", view)
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
	m, _, _ := newModel(t)
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	m.source, m.view, m.title = "radio", "Recent", "Recent"
	m.items = []core.Item{station}
	m.state = core.PlaybackState{IsLive: true, Status: "playing", Track: &station}
	m.loading = false
	m.width, m.height = 120, 30

	view := plainText(m.View().Content)
	if strings.Contains(view, "☆") || !strings.Contains(view, "  Example FM") || !strings.Contains(view, "f favorite") {
		t.Fatalf("unfavorited station state missing:\n%s", view)
	}

	seedFavorite(&m, "radio", station)
	view = plainText(m.View().Content)
	lines := plainText(strings.Join(m.listLines(60, 10), "\n"))
	if !strings.Contains(lines, "★ Example FM") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("favorited station state missing:\n%s", view)
	}
}

func TestRadioFavoritesRootOmitsRedundantFavoriteIcon(t *testing.T) {
	m, _, _ := newModel(t)
	station := core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}
	seedFavorite(&m, "radio", station)
	m.source, m.view, m.title = "radio", "Favorites", "Favorites"
	m.items, m.width, m.height = []core.Item{station}, 100, 24
	view := plainText(m.View().Content)
	if strings.Contains(view, "★") || !strings.Contains(view, "Example FM") || !strings.Contains(view, "f unfavorite") {
		t.Fatalf("Favorites root has redundant marker or missing action:\n%s", view)
	}

	m.history = []page{{source: "radio", view: "Favorites", title: "Favorites"}}
	if view := plainText(m.View().Content); strings.Count(view, "★") != 1 || !strings.Contains(view, "★ Example FM") {
		t.Fatalf("temporary result page lost favorite state:\n%s", view)
	}
}

func TestCompletedActionResumesProbes(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 3; i++ {
		seedFavorite(&m, "radio", core.Item{Kind: "stream", URL: fmt.Sprintf("https://radio.example/%d", i), Title: fmt.Sprintf("S%d", i)})
	}
	m.source, m.view = "radio", "Favorites"
	m.width, m.height = 90, 20
	m.items = m.activity.FavoritesFor("radio")
	m.busy, m.operationID = true, 1
	if paused, cmd := m.scheduleProbes(); len(paused.probes) != 0 || cmd != nil {
		t.Fatalf("probes scheduled while busy: probes=%d cmd=%v", len(paused.probes), cmd != nil)
	}
	model, _ := m.Update(actionMsg{actionID: 1, state: core.PlaybackState{Status: "playing"}})
	done := model.(Model)
	if done.busy {
		t.Fatalf("action completion left busy set")
	}
	if len(done.probes) == 0 {
		t.Fatalf("completed action did not resume probing")
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
	if m.browseQuery.Sort != "fastest" || m.title != "Popular Worldwide · Sort: Fastest" {
		t.Fatalf("sort apply = query=%#v title=%q", m.browseQuery, m.title)
	}
}

func TestRadioBrowseHeaderGrammar(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide · Sort: Fastest"
	m.browseQuery = radioDiscovery{Sort: "fastest"}
	m.items = []core.Item{
		{Kind: "stream", URL: "https://radio.example/a", Title: "A"},
		{Kind: "stream", URL: "https://radio.example/b", Title: "B"},
		{Kind: "stream", URL: "https://radio.example/c", Title: "C"},
	}
	// The header is the fixed panel identity; coverage stays on the per-row
	// probe markers and the context row, never in the title.
	if title := m.listTitle(); title != "Browse" {
		t.Fatalf("browse header = %q", title)
	}
	if ctx := m.listContext(); ctx != "Popular Worldwide · Sort: Fastest" {
		t.Fatalf("browse context = %q", ctx)
	}
	m.radioCache.RecordHealth(m.items[0].URL, "healthy", 50, "", time.Now())
	if title := m.listTitle(); strings.Contains(title, "measured") {
		t.Fatalf("measurement leaked into the header: %q", title)
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
	seedRecent(&m, "radio", items[1])
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

func TestRadioListMarksCurrentlyPlayingStation(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Browse"
	m.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Track: &core.Item{Kind: "stream", URL: "https://radio.example/live", Title: "Live FM"}}
	m.loading = false
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
	if strings.Contains(plainText(lines[0]), "▶") {
		t.Fatalf("idle row gained a playing marker:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(plainText(lines[1]), "▶ Live FM") {
		t.Fatalf("playing row is missing the marker:\n%s", strings.Join(lines, "\n"))
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

	next, _ := m.applyCachedList(listMsg{key: "radio/Browse", title: "Popular Worldwide · Sort: Fastest", items: stored, cached: true})
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

func TestWatchDisconnectStopsInterpolationThenSnapshotReplacesServerState(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Position: 10, Duration: 100}
	m.snapshotAt = time.Now().Add(-time.Second)
	m.probes["checking"] = radioProbe{status: "checking"}
	m.probeActive = 1
	next, _ := m.Update(watchMsg{update: api.WatchUpdate{Kind: api.WatchKindDisconnected}})
	m = next.(Model)
	if m.state.Status != "disconnected" || !m.snapshotAt.IsZero() || !strings.Contains(m.message, "reconnecting") || m.probeActive != 0 || len(m.probes) != 0 {
		t.Fatalf("disconnect state=%#v message=%q", m.state, m.message)
	}
	if got := m.displayPositionAt(time.Now().Add(time.Minute)); got != 10 {
		t.Fatalf("disconnected interpolation = %v", got)
	}

	snapshot := api.WatchSnapshot{
		Sequence: 1,
		Playback: api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "paused", Source: api.SourceAudius}},
		State:    &api.AppState{Revision: 1, Theme: "tokyo-night", LastSource: api.SourceAudius},
		Warning:  &api.WatchWarning{Code: api.CodeStorageUnavailable, Message: "activity unavailable"},
	}
	next, _ = m.Update(watchMsg{update: api.WatchUpdate{Kind: api.WatchKindSnapshot, Snapshot: &snapshot}})
	m = next.(Model)
	if !m.connected || m.sequence != 1 || m.state.Status != "paused" || m.store.Theme != "tokyo-night" {
		t.Fatalf("reconnected model = connected:%v sequence:%d state:%#v theme:%q", m.connected, m.sequence, m.state, m.store.Theme)
	}
	m.message = ""
	if message, isErr := m.feedbackText(); !isErr || !strings.Contains(message, api.CodeStorageUnavailable) {
		t.Fatalf("persistent warning = %q err=%v", message, isErr)
	}
}

func TestAppleMusicRemembersLastViewAndRadioDefaultsToHome(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.view = "Home"
	next, _ := m.selectView(1)
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("selectView(3) = %q, want Recent", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	next, _ = m.selectView(1)
	m = next.(Model)
	if m.view != "Browse" {
		t.Fatalf("radio selectView(1) = %q, want Browse", m.view)
	}
	next, _ = m.switchSource("apple-music")
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("apple music did not remember Recent: %q", m.view)
	}
	next, _ = m.switchSource("radio")
	m = next.(Model)
	if m.view != "Home" {
		t.Fatalf("radio did not default to Home: %q", m.view)
	}
}

func TestInitialWatchAlignsBrowseSourceToActivePlayback(t *testing.T) {
	_, f, store := newModel(t)
	sources, err := f.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := api.WatchSnapshot{Sequence: 1, Playback: api.PlaybackState{PlaybackStatus: api.PlaybackStatus{Status: "playing", Mode: "full", Source: api.SourceAudius, Track: &api.Item{Source: api.SourceAudius, Kind: "song", Title: "Audius Song"}}}, State: &api.AppState{Revision: 1, LastSource: api.SourceAppleMusic}, Sources: sources}
	m := New(Options{Provider: f, Player: f, Radio: fakeRadio{}, Remote: &recordingRemote{}, Store: store, Source: "apple-music", InitialWatch: &snapshot})
	if m.source != "audius" {
		t.Fatalf("browse source = %q, want audius (aligned to playback)", m.source)
	}
	if f.stops != 0 {
		t.Fatalf("alignment must not stop playback (stops=%d)", f.stops)
	}
}

// `G` means "show the end of what is loaded". It used to double as a page
// request, so the station count grew forever and the cursor stayed on the first
// row (usability batch 2026-09-16 M2).
func TestRadioBrowseJumpToEndDoesNotRequestAnotherPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items = make([]core.Item, 40)
	for i := range m.items {
		m.items[i] = core.Item{Kind: "station", ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Title: "Station"}
	}
	m.pageMore, m.pageKey, m.pageOffset = true, m.browsePageKey(), radioPageSize
	m.selected, m.loading = 0, false

	next, _ := m.handleKey(runeKey('G'))
	m = next.(Model)
	if m.selected != len(m.items)-1 {
		t.Fatalf("G selected row %d, want the last loaded row %d", m.selected, len(m.items)-1)
	}
	if !m.holdBrowsePage {
		t.Fatalf("G must hold the automatic page load for this keystroke")
	}
	if loadMore := m.maybeLoadMore(); loadMore != nil {
		t.Fatalf("G requested another page")
	}
	if m.holdBrowsePage {
		t.Fatalf("the page-load hold must last exactly one keystroke")
	}
	// Reaching past the end still pages, so the list can grow on demand.
	if loadMore := m.maybeLoadMore(); loadMore == nil {
		t.Fatalf("moving to the end must still load the next page")
	}
}

// `G` after a failed page is the retry the notice promises, not a jump; the
// jump hold must not swallow it.
func TestRadioBrowseJumpToEndStillRetriesAFailedPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
	m.items = make([]core.Item, 10)
	m.pageMore, m.pageKey, m.pageOffset, m.loading = true, "", radioPageSize, false
	m.pageFailed, m.pageLoading = true, false

	next, cmd := m.Update(runeKey('G'))
	m = run(next.(Model), cmd)
	if m.pageFailed {
		t.Fatalf("G did not clear the page failure")
	}
	if m.holdBrowsePage {
		t.Fatalf("G must not hold the page load when it is the retry")
	}
}
