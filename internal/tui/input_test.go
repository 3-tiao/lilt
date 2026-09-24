package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/theme"
	"strings"
	"testing"
	"time"
)

func TestPaletteNavigationCompletionAndUnknownCommand(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey(':'))
	m = next.(Model)
	if m.overlay != "palette" || !m.input.Focused() {
		t.Fatalf("palette did not open: overlay=%q focused=%v", m.overlay, m.input.Focused())
	}
	if m.overlaySelected != -1 {
		t.Fatalf("empty palette must not preselect, index = %d", m.overlaySelected)
	}
	m.input.SetValue("rec")
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.input.Value() != "rec" {
		t.Fatalf("Tab must not overwrite the typed text, got %q", m.input.Value())
	}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.view != "Recent" || m.overlay != "" {
		t.Fatalf("palette navigation = view=%q overlay=%q", m.view, m.overlay)
	}
	next, _ = m.handleKey(runeKey(':'))
	m = next.(Model)
	m.input.SetValue("wat")
	// "wat" matches nothing, so the palette falls back to the raw text — still
	// an error. The highlighted-first semantics matter when the filter's
	// substring hits aren't prefixes (e.g. "pl" highlighting
	// ":source apple-music").
	if matches := m.paletteMatches(); len(matches) != 0 {
		t.Fatalf("'wat' unexpectedly matched %#v", matches)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.messageErr || m.message != "Unknown command: :wat" {
		t.Fatalf("unknown palette command = %q err=%v", m.message, m.messageErr)
	}

	// ":pl" substring-matches ":source apple-music" (highlighted first) and
	// ":play <ref>". Under the OQ33 fix, Enter runs the highlighted candidate
	// instead of erroring on the raw text.
	m.input.SetValue("pl")
	next, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "apple-music" {
		t.Fatalf("Enter on ':pl' ran the raw text; want the highlighted ':source apple-music', got source=%q msg=%q err=%v", m.source, m.message, m.messageErr)
	}
}

func TestPaletteEmptyEnterIsNoOp(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	next, _ := m.handleKey(runeKey(':'))
	m = next.(Model)
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(Model), cmd)
	if m.source != "audius" || m.overlay != "" || m.messageErr {
		t.Fatalf("empty Enter acted: source=%q overlay=%q err=%v msg=%q", m.source, m.overlay, m.messageErr, m.message)
	}
}

// OQ24 (batch 2026-09-22-jamendo-tui r4): the palette is an advertisement, and
// a command the executor would reject is a bug — ":browse" on Apple Music
// executed as "Unknown command", and ":discover" ran as a silent no-op on a
// source without trending. The list is context-gated: only what the current
// source's views and playback can actually run.
func TestPaletteListsOnlyExecutableCommands(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	contains := func(matches []string, want string) bool {
		for _, match := range matches {
			if match == want {
				return true
			}
		}
		return false
	}
	// Apple Music in the fake capability snapshot declares no trending and
	// nothing is playing.
	matches := m.paletteMatches()
	for _, absent := range []string{":discover", ":browse", ":queue"} {
		if contains(matches, absent) {
			t.Fatalf("palette advertises %q on Apple Music: %#v", absent, matches)
		}
	}
	// Audius declares trending: :discover appears, :browse does not.
	m.source = "audius"
	matches = m.paletteMatches()
	if !contains(matches, ":discover") || contains(matches, ":browse") {
		t.Fatalf("audius palette = %#v", matches)
	}
	// Radio declares radio search: :browse appears, :discover does not.
	m.source = "radio"
	matches = m.paletteMatches()
	if !contains(matches, ":browse") || contains(matches, ":discover") {
		t.Fatalf("radio palette = %#v", matches)
	}
	// A playing finite queue makes :queue runnable again.
	m.source = "apple-music"
	m.state = core.PlaybackState{
		Status:     "playing",
		Mode:       "full",
		QueueIndex: 0,
		Queue:      []core.Item{{Kind: "song", ID: "s1"}},
		Track:      &core.Item{Kind: "song", ID: "s1", Title: "One"},
	}
	if !contains(m.paletteMatches(), ":queue") {
		t.Fatalf("playing queue palette missing :queue: %#v", m.paletteMatches())
	}
}

func TestPaletteTabCyclesCandidatesWithoutCommitting(t *testing.T) {
	m, _, _ := newModel(t)
	next, _ := m.handleKey(runeKey(':'))
	m = next.(Model)
	m.input.SetValue("sou")
	m.overlaySelected = 0
	matches := m.paletteMatches()
	if len(matches) != 4 {
		t.Fatalf("matches for sou = %#v", matches)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.input.Value() != "sou" || m.overlaySelected != 1 {
		t.Fatalf("first Tab = input %q index %d", m.input.Value(), m.overlaySelected)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.overlaySelected != 2 {
		t.Fatalf("second Tab index = %d", m.overlaySelected)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.overlaySelected != 3 {
		t.Fatalf("third Tab index = %d", m.overlaySelected)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.overlaySelected != 0 {
		t.Fatalf("fourth Tab must wrap, index = %d", m.overlaySelected)
	}
}

func TestPaletteRejectsUnknownSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	next, _ := m.handleKey(runeKey(':'))
	m = next.(Model)
	m.input.SetValue("source nope")
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.source != "audius" || !m.messageErr {
		t.Fatalf("unknown source switched: source=%q err=%v msg=%q", m.source, m.messageErr, m.message)
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
	next, _ = m.handleKey(runeKey('s'))
	m = next.(Model)
	if m.overlay != "source-switcher" {
		t.Fatalf("source switcher did not open: %q", m.overlay)
	}
}

// Queue keys must be visible where a reader chains tracks: search results, where
// Enter now plays only the pointed row.
func TestFooterAdvertisesQueueKeysForQueuableRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 200, 30
	m.pageClass = pageClassAggregate
	m.history = []page{{title: "Home"}}
	m.title = "Search: blinding"
	m.items = []core.Item{
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"},
		{Kind: "song", ID: "s2", Ref: "apple-music:song:s2", Title: "Two"},
	}
	m.selected = 0
	footer := m.footerLine(200)
	if !strings.Contains(footer, "e queue next") || !strings.Contains(footer, "E append") {
		t.Fatalf("search footer hides the queue keys: %q", footer)
	}

	// Radio declares no finite queue, so the keys would be a lie there.
	m.source = "radio"
	m.items = []core.Item{{Kind: "stream", URL: "https://radio.example/live", Title: "Example FM"}}
	m.selected = 0
	if footer := m.footerLine(200); strings.Contains(footer, "E append") {
		t.Fatalf("radio footer advertises queueing: %q", footer)
	}
}

// OQ22/OQ23 (batch 2026-09-22-jamendo-tui): the skip keys n/b were help-only,
// so "skip this track" — the highest-frequency playback control — had no
// visible hint, and two participants misread "e next" as skipping. The
// footer must show n/b while a finite queue is playing, and never for a live
// stream or a single-item queue.
func TestFooterShowsSkipKeysWhileAQueuePlays(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 200, 30
	m.pageClass = pageClassAggregate
	m.history = []page{{title: "Home"}}
	m.items = []core.Item{
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"},
		{Kind: "song", ID: "s2", Ref: "apple-music:song:s2", Title: "Two"},
	}
	m.selected = 0
	m.state = core.PlaybackState{
		Status:     "playing",
		Track:      &core.Item{Kind: "song", ID: "s1", Title: "One"},
		QueueIndex: 0,
		Queue:      []core.Item{{Kind: "song", ID: "s1"}, {Kind: "song", ID: "s2"}},
	}
	footer := m.footerLine(200)
	if !strings.Contains(footer, "n next") || !strings.Contains(footer, "b prev") {
		t.Fatalf("playing footer hides the skip keys: %q", footer)
	}

	// A live stream has nothing to skip to.
	m.state.IsLive = true
	if footer := m.footerLine(200); strings.Contains(footer, "n next") {
		t.Fatalf("live footer advertises skipping: %q", footer)
	}

	// A single-item queue has nowhere to go.
	m.state.IsLive = false
	m.state.Queue = m.state.Queue[:1]
	if footer := m.footerLine(200); strings.Contains(footer, "n next") {
		t.Fatalf("single-item footer advertises skipping: %q", footer)
	}
}

// An unrelated key must not dismiss help: dismissing it would swallow the key
// the reader pressed to act, so the action only runs on a second press
// (batch 2026-09-20-album-recheck N3).
func TestHelpIgnoresUnrelatedKeys(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.overlay = "help"
	for _, key := range []rune{'v', 'p', 'x', '0', 's'} {
		next, _ := m.handleKey(runeKey(key))
		m = next.(Model)
		if m.overlay != "help" {
			t.Fatalf("key %q dismissed help", key)
		}
	}
}

func TestMouseDoubleClickActivatesMainList(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	m.loading = false
	m.items = []core.Item{
		{Kind: "playlist", ID: "p1", Title: "One"},
		{Kind: "playlist", ID: "p2", Title: "Two"},
	}
	y := m.layout().listTop + 1 + 1
	next, _ := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}

	// Two consecutive same-row clicks within the window are one double-click.
	next, cmd := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatalf("double-click should activate: cmd=%v loading=%v", cmd != nil, m.loading)
	}
	if m.lastClick.target != "" {
		t.Fatalf("consumed gesture = %#v, want reset", m.lastClick)
	}

}

func TestMouseDoubleClickWindowAndContinuity(t *testing.T) {
	newList := func(t *testing.T) Model {
		t.Helper()
		m, _, _ := newModel(t)
		m.width, m.height = 120, 30
		m.title = "Playlists"
		m.loading = false
		m.items = []core.Item{
			{Kind: "playlist", ID: "p1", Title: "One"},
			{Kind: "playlist", ID: "p2", Title: "Two"},
		}
		return m
	}

	// A stale second click (after the double-click window) only re-selects.
	m := newList(t)
	y := m.layout().listTop + 1 + 1
	m.lastClick = lastClick{target: "list", index: 1, at: time.Now().Add(-2 * time.Second)}
	next, cmd := m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("a click after the window elapsed should not activate: cmd=%v", cmd)
	}
	if m.selected != 1 {
		t.Fatalf("stale click lost selection: %d", m.selected)
	}

	// Not consecutive: row A, row B, row A again never activates.
	m = newList(t)
	next, _ = m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	next, _ = m.handleMouse(mouseClick(5, m.layout().listTop+1)) // row 0
	m = next.(Model)
	next, cmd = m.handleMouse(mouseClick(5, y)) // back to row 1
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("A-B-A clicks should not activate: cmd=%v", cmd)
	}

	// An intervening click on another target (nav row) breaks the gesture.
	m = newList(t)
	next, _ = m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	next, _ = m.handleMouse(mouseClick(5, 1)) // source row
	m = next.(Model)
	m.overlay = "" // dismiss the switcher without its own click path
	next, cmd = m.handleMouse(mouseClick(5, y))
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("nav click between two row clicks should break the gesture: cmd=%v", cmd)
	}
}

func TestMouseSecondClickKeepsTheOriginallyClickedMainListItem(t *testing.T) {
	m, _, _ := newModel(t)
	// Three content rows makes the old cursor-centred window shift after a
	// click on the last visible row. The next click at the same coordinates
	// used to open the following playlist.
	m.width, m.height = 120, 16
	m.title = "Playlists"
	m.loading = false
	m.items = []core.Item{
		{Kind: "playlist", ID: "p1", Title: "One"},
		{Kind: "playlist", ID: "p2", Title: "Two"},
		{Kind: "playlist", ID: "p3", Title: "Three"},
		{Kind: "playlist", ID: "p4", Title: "Four"},
		{Kind: "playlist", ID: "p5", Title: "Five"},
	}
	l := m.layout()
	if rows := panelBodyRows(l.listHeight); rows != 3 {
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

// The rail is separated from the header by a spacer row, so a hit test that
// ignores the gap selects the queue entry one row away from the pointer.
func TestQueueClickSelectsTheRowUnderThePointer(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	queue := make([]core.Item, 30)
	for i := range queue {
		queue[i] = core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Track %d", i+1)}
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: queue}
	m.loading = false

	l := m.layout()
	if !l.showRail {
		t.Fatalf("test needs the wide workspace rail: rail=%v", l.showRail)
	}
	rows := panelBodyRows(l.listHeight)
	start, _ := m.queueWindow(rows)
	x := l.gutter + l.mainWidth + 2

	// The panel's first content row sits one row below its top border.
	for offset := 0; offset < 3; offset++ {
		y := l.listTop + 1 + offset
		next, _ := m.handleMouse(mouseClick(x, y))
		m = next.(Model)
		if m.queueCursor != start+offset {
			t.Fatalf("click at y=%d selected %d, want %d (window starts at %d)", y, m.queueCursor, start+offset, start)
		}
	}

	// The workspace-gap row above the rail must not select anything.
	m.queueCursor = start
	m.queueFocus = true
	next, _ := m.handleMouse(mouseClick(x, l.listTop-1))
	if got := next.(Model); got.queueCursor != start {
		t.Fatalf("spacer row moved the queue cursor to %d", got.queueCursor)
	}
}

// The queue panel used to re-centre on every click, so the second click at the
// same cell selected a different entry and jumped somewhere the user did not
// point at.
func TestQueueSecondClickOnSameCellJumpsToThatEntry(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	queue := make([]core.Item, 30)
	for i := range queue {
		queue[i] = core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Track %d", i+1)}
	}
	f.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 0, Queue: queue}
	m.state = f.state
	m.loading = false

	l := m.layout()
	rows := panelBodyRows(l.listHeight)
	start, _ := m.queueWindow(rows)
	x := l.gutter + l.mainWidth + 2
	y := l.listTop + 1 + 3

	next, _ := m.handleMouse(mouseClick(x, y))
	m = next.(Model)
	want := start + 3
	if m.queueCursor != want {
		t.Fatalf("first click selected %d, want %d", m.queueCursor, want)
	}

	next, cmd := m.handleMouse(mouseClick(x, y))
	m = next.(Model)
	if m.queueCursor != want {
		t.Fatalf("second click at the same cell selected %d, want %d", m.queueCursor, want)
	}
	m = run(m, cmd)
	if f.queueJumps != 1 {
		t.Fatalf("second click jumps = %d, want 1", f.queueJumps)
	}
}

// A click selects or activates; it must never move the visible window. If it
// did, the row under the pointer would change between two clicks.
func TestListClicksDoNotScrollTheViewport(t *testing.T) {
	t.Run("main list", func(t *testing.T) {
		m, _, _ := newModel(t)
		m.width, m.height = 120, 30
		m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
		m.loading = false
		m.items = radioPageItems(0, 200)
		rows := panelBodyRows(m.layout().listHeight) - m.mainPrefixRows()
		m = m.scrollMainList(rows * 2)
		before, _ := m.mainListWindow(rows)

		y := m.layout().listTop + 1 + 3 + m.mainPrefixRows()
		next, _ := m.handleMouse(mouseClick(10, y))
		m = next.(Model)
		after, _ := m.mainListWindow(rows)
		if before != after {
			t.Fatalf("click scrolled the list: %d -> %d", before, after)
		}
		if m.selected != after+3 {
			t.Fatalf("click selected %d, want %d", m.selected, after+3)
		}
	})

	t.Run("queue panel", func(t *testing.T) {
		m, _, _ := newModel(t)
		m.width, m.height = 120, 30
		m.title = "Playlists"
		queue := make([]core.Item, 30)
		for i := range queue {
			queue[i] = core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Track %d", i+1)}
		}
		m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 5, Queue: queue}
		m.loading = false
		m.queueFocus = true
		m.queueCursor = 12
		l := m.layout()
		rows := panelBodyRows(l.listHeight)
		before, _ := m.queueWindow(rows)

		x := l.gutter + l.mainWidth + 2
		y := l.listTop + 1 + 2
		next, _ := m.handleMouse(mouseClick(x, y))
		m = next.(Model)
		after, _ := m.queueWindow(rows)
		if before != after {
			t.Fatalf("queue click scrolled the panel: %d -> %d", before, after)
		}
		if m.queueCursor != after+2 {
			t.Fatalf("queue click selected %d, want %d", m.queueCursor, after+2)
		}
	})
}

// A click in a queue panel that the user already scrolled must stay on the row
// under the pointer. Re-centring on the cursor there moved the window and
// selected a different entry than the one clicked.
func TestQueueClickKeepsScrolledWindowOnPointedRow(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.title = "Playlists"
	queue := make([]core.Item, 30)
	for i := range queue {
		queue[i] = core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Track %d", i+1)}
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 20, Queue: queue}
	m.loading = false
	// A populated browse list sits on the left; a queue click must not touch it.
	m.items = []core.Item{{Kind: "song", ID: "p1", Title: "First"}, {Kind: "song", ID: "p2", Title: "Second"}, {Kind: "song", ID: "p3", Title: "Third"}}
	m.selected, m.listOffset = 2, 0
	rows := panelBodyRows(m.layout().listHeight)
	m = m.scrollQueue(-9, rows)

	l := m.layout()
	before, _ := m.queueWindow(rows)
	if before == 0 {
		t.Fatalf("expected a scrolled queue window, got %d", before)
	}
	x := l.gutter + l.mainWidth + 2
	y := l.listTop + 1 + 2
	next, _ := m.handleMouse(mouseClick(x, y))
	m = next.(Model)
	after, _ := m.queueWindow(rows)
	if before != after {
		t.Fatalf("queue click scrolled the panel: %d -> %d", before, after)
	}
	if m.queueCursor != before+2 {
		t.Fatalf("queue click selected %d, want %d", m.queueCursor, before+2)
	}
	if m.selected != 2 || m.listOffset != 0 {
		t.Fatalf("queue click moved the left list: selected=%d offset=%d", m.selected, m.listOffset)
	}
}

// Wheeling over Up Next scrolls the panel from where it is. Moving the queue
// cursor first re-centred the window on a stale cursor (often entry 0), so the
// panel jumped to the top of a long queue before scrolling.
func TestMouseWheelScrollsQueueFromItsCurrentPosition(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	queue := make([]core.Item, 60)
	for i := range queue {
		queue[i] = core.Item{Kind: "song", ID: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("Track %d", i+1)}
	}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", QueueIndex: 30, Queue: queue}
	l := m.layout()
	if !l.showRail {
		t.Fatal("queue rail expected")
	}
	rows := panelBodyRows(l.listHeight)
	x, y := l.gutter+l.mainWidth+2, l.listTop+1
	before, _ := m.queueWindow(rows)
	if before == 0 {
		t.Fatalf("expected a scrolled starting window, got %d", before)
	}

	next, _ := m.handleMouse(mouseWheel(1, x, y))
	m = next.(Model)
	if !m.queueFocus {
		t.Fatal("wheel over Up Next should focus the panel")
	}
	after, _ := m.queueWindow(rows)
	if after != before+3 {
		t.Fatalf("wheel did not continue from the current position: %d -> %d", before, after)
	}
	if m.queueCursor < after || m.queueCursor >= after+rows {
		t.Fatalf("cursor %d left the window %d-%d", m.queueCursor, after, after+rows)
	}

	// Wheeling back returns to where it started.
	next, _ = m.handleMouse(mouseWheel(0, x, y))
	m = next.(Model)
	if back, _ := m.queueWindow(rows); back != before {
		t.Fatalf("wheel up returned to %d, want %d", back, before)
	}

	// A queue that fits has nothing to scroll, but wheeling still focuses it.
	short, _, _ := newModel(t)
	short.width, short.height = 120, 30
	short.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Current"}, Queue: []core.Item{{Title: "A"}, {Title: "B"}}}
	next, _ = short.handleMouse(mouseWheel(1, x, y))
	if focused := next.(Model); !focused.queueFocus {
		t.Fatal("wheel over a short queue should still focus the panel")
	}
}

func TestMouseClickPanelSelectsAndJumps(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 120, 30
	f.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Title: "Current"}, Queue: []core.Item{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m.state = f.state
	l := m.layout()
	// Third queue row: one border row plus two body rows below the rail top.
	x, y := l.gutter+l.mainWidth+3, l.listTop+3
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
	m.loading = false
	m.items = radioPageItems(0, 60)
	rows := panelBodyRows(m.layout().listHeight)
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
	if !strings.Contains(first[0], "┃") {
		t.Fatalf("scrollbar thumb missing at the top:\n%s", first[0])
	}
	if strings.Contains(first[0], "█") {
		t.Fatalf("scrollbar should use the border glyph, not a solid block:\n%s", first[0])
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
	if lines := short.listLines(80, rows); strings.Contains(strings.Join(lines, "\n"), "┃") || strings.Contains(strings.Join(lines, "\n"), "│") {
		t.Fatalf("short list should not draw a scrollbar:\n%s", lines[0])
	}
}

func TestMouseClickViewTab(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	x := -1
	for candidate := 0; candidate < 120; candidate++ {
		if index, ok := m.viewTabAt(candidate); ok && index == 1 {
			x = candidate
			break
		}
	}
	if x < 0 {
		t.Fatal("view tab not found")
	}
	next, _ := m.handleMouse(mouseClick(x+m.layout().gutter, 2))
	m = next.(Model)
	if m.view != "Recent" {
		t.Fatalf("view = %q, want Recent", m.view)
	}
}

// A click outside the dialog dismisses it. A click inside must never throw away
// state, and list overlays must let a mouse user pick a row.
func TestOverlayInsideClickKeepsState(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source, m.view = "radio", "Favorites"
	next, _ := m.handleKey(runeKey('a'))
	m = next.(Model)
	m.input.SetValue("https://example.test/live")

	bw, bh := m.overlayBoxSize()
	l := m.layout()
	bx, by := max(0, (l.width-bw)/2), max(0, (l.height-bh)/2)
	next, _ = m.handleMouse(mouseClick(bx+2+l.gutter, by+1))
	inside := next.(Model)
	if inside.overlay != "input" || inside.input.Value() != "https://example.test/live" {
		t.Fatalf("inside click discarded the input: overlay=%q value=%q", inside.overlay, inside.input.Value())
	}

	next, _ = inside.handleMouse(mouseClick(l.gutter, 0))
	if cancelled := next.(Model); cancelled.overlay != "" {
		t.Fatalf("outside click should cancel the dialog: %q", cancelled.overlay)
	}
}

func TestThemeRowClickSelectsThenSaves(t *testing.T) {
	m, _, store := newModel(t)
	m.width, m.height = 120, 30
	next, _ := m.handleKey(runeKey('t'))
	m = next.(Model)
	if len(m.themeNames) < 2 {
		t.Skip("needs at least two themes")
	}
	m.themeIndex = 0
	m.themeName = m.themeNames[0]

	bw, bh := m.overlayBoxSize()
	l := m.layout()
	bx, by := max(0, (l.width-bw)/2), max(0, (l.height-bh)/2)
	x, y := bx+3+l.gutter, by+2 // second theme row

	next, cmd := m.handleMouse(mouseClick(x, y))
	m = run(next.(Model), cmd)
	if m.themeIndex != 1 || m.themeName != m.themeNames[1] {
		t.Fatalf("row click did not select: index=%d name=%q", m.themeIndex, m.themeName)
	}
	if m.overlay != "theme" {
		t.Fatalf("selecting a row should keep the picker open: %q", m.overlay)
	}

	next, cmd = m.handleMouse(mouseClick(x, y))
	m = run(next.(Model), cmd)
	if m.overlay != "" {
		t.Fatalf("second click should save and close: %q", m.overlay)
	}
	if store.Theme != m.themeNames[1] {
		t.Fatalf("theme not saved: %q", store.Theme)
	}
}

func TestHelpInsideClickKeepsTheOverlay(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	next, _ := m.handleKey(runeKey('?'))
	m = next.(Model)
	bw, bh := m.overlayBoxSize()
	l := m.layout()
	bx, by := max(0, (l.width-bw)/2), max(0, (l.height-bh)/2)
	next, _ = m.handleMouse(mouseClick(bx+3+l.gutter, by+3))
	if kept := next.(Model); kept.overlay != "help" {
		t.Fatalf("inside click closed help: %q", kept.overlay)
	}
}

func TestTitleBarClickGoesBackFromPushedPage(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.source, m.view, m.title = "apple-music", "Playlists", "Search: jazz"
	m.loading = false
	m.history = []page{{source: "apple-music", view: "Playlists", title: "Playlists"}}
	if got := m.listTitle(); got != "Search" {
		t.Fatalf("pushed page header = %q, want fixed identity", got)
	}
	if ctx := m.listContext(); ctx != "jazz" {
		t.Fatalf("pushed page context = %q, want the search term", ctx)
	}
	l := m.layout()
	next, _ := m.handleMouse(mouseClick(40+l.gutter, l.listTop))
	got := next.(Model)
	if len(got.history) != 0 || got.title == "Search: jazz" {
		t.Fatalf("title click did not go back: history=%d title=%q", len(got.history), got.title)
	}
	// A click on the list body still selects instead of going back.
	m.history = []page{{source: "apple-music", view: "Playlists", title: "Playlists"}}
	m.items = radioPageItems(0, 20)
	next, _ = m.handleMouse(mouseClick(10+l.gutter, l.listTop+2))
	if kept := next.(Model); len(kept.history) == 0 {
		t.Fatal("a body click should not go back")
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

func TestUnicodeKeyEventsDoNotRecurse(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 120, 30
	m.loading = false
	m.items = []core.Item{{Title: "A"}, {Title: "B"}}

	// A multibyte character is one key, not an unbracketed burst. The
	// previous byte-count check recursively re-entered Update until the
	// process ran out of stack.
	for _, r := range []rune{'：', '歌', '🎵'} {
		next, cmd := m.Update(runeKey(r))
		m = next.(Model)
		if cmd != nil || m.overlay != "" || m.selected != 0 {
			t.Fatalf("%q changed navigation: overlay=%q selected=%d cmd=%v", r, m.overlay, m.selected, cmd != nil)
		}
	}

	// A burst containing Unicode still replays each rune exactly once.
	next, _ := m.Update(tea.KeyPressMsg{Text: "歌j"})
	m = next.(Model)
	if m.selected != 1 || m.overlay != "" {
		t.Fatalf("mixed burst: selected=%d overlay=%q", m.selected, m.overlay)
	}

	// Only the ASCII shortcut opens the palette.
	next, _ = m.Update(runeKey(':'))
	if got := next.(Model).overlay; got != "palette" {
		t.Fatalf("ASCII colon overlay = %q, want palette", got)
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

func TestMutationSlotSerializesKeyboardAndMouseEntryPaths(t *testing.T) {
	m, f, _ := newModel(t)
	m.width, m.height = 100, 30
	m.loading = false
	m.items = []core.Item{{Kind: "song", ID: "one", Ref: "apple-music:song:one", Title: "One"}}
	playStarted, playBlock := make(chan struct{}), make(chan struct{})
	f.playStarted, f.playBlock = playStarted, playBlock

	next, first := m.activate()
	m = next.(Model)
	result := make(chan tea.Msg, 1)
	go func() { result <- first() }()
	<-playStarted

	// A different keyboard mutation must not reach the server while play owns
	// the slot.
	next, second := m.handleKey(runeKey('n'))
	m = next.(Model)
	if second == nil {
		// Rejection may be represented without a toast command, but no RPC is the
		// invariant under test.
	}
	f.mu.Lock()
	nextCalls := f.nexts
	f.mu.Unlock()
	if nextCalls != 0 {
		t.Fatalf("second server operation issued early: next calls=%d", nextCalls)
	}

	// A real mouse double-click reaches activate(), not the keyboard binding;
	// it must observe the same owner and issue no second play.
	l := m.layout()
	y := l.listTop + 1
	next, _ = m.handleClick(5, y, l)
	m = next.(Model)
	next, _ = m.handleClick(5, y, l)
	m = next.(Model)
	f.mu.Lock()
	played := f.played
	f.mu.Unlock()
	if played.Ref != "apple-music:song:one" || !m.busy {
		t.Fatalf("mouse activation bypassed mutation slot: played=%#v busy=%v", played, m.busy)
	}

	close(f.playBlock)
	next, _ = m.Update(<-result)
	m = next.(Model)
	if m.busy {
		t.Fatal("completed mutation did not release slot")
	}
}

// Palette signal colours are text colours. Filling a row with one (the old
// green playing block) overrode each theme's own visual language.
func TestRendererNeverFillsWithAPaletteSignalColour(t *testing.T) {
	t.Setenv("LILT_CONFIG", t.TempDir())
	for _, name := range theme.Names() {
		loaded := theme.Load(name)
		green := backgroundSGR(loaded.Green)
		if green == "" {
			continue
		}
		r := newRenderer(loaded)
		for label, style := range map[string]lipgloss.Style{"playing": r.currentStyle, "selection": r.selStyle} {
			if rendered := style.Render("row"); strings.Contains(rendered, green) {
				t.Fatalf("%s %s row fills with palette green: %q", name, label, rendered)
			}
		}
	}
}

func TestSourceSwitcherMouseClickSwitches(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.state = core.PlaybackState{Status: "stopped"}
	m.overlay, m.overlaySelected = "source-switcher", 0
	// Content row 0 = Apple Music, row 1 = Audius; y=2 -> body=1.
	next, cmd := m.handleOverlayClick(1, 2)
	m = run(next.(Model), cmd)
	if m.source != "audius" || m.overlay != "" {
		t.Fatalf("mouse switch = source=%q overlay=%q", m.source, m.overlay)
	}
}

func TestSourceSwitcherMouseClickCurrentCloses(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	m.overlay, m.overlaySelected = "source-switcher", 1
	// Row 1 is the current source; clicking it just dismisses.
	next, _ := m.handleOverlayClick(1, 2)
	m = next.(Model)
	if m.source != "audius" || m.overlay != "" {
		t.Fatalf("click current = source=%q overlay=%q", m.source, m.overlay)
	}
}

func TestPaletteMouseClickRunsCommand(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.overlay = "palette"
	m.input.SetValue("rec")
	m.input.Focus()
	m.overlaySelected = 0
	// Content row 0 is the input, row 1 is the first match (":recent").
	next, cmd := m.handleOverlayClick(1, 2)
	m = run(next.(Model), cmd)
	if m.view != "Recent" || m.overlay != "" {
		t.Fatalf("palette mouse run = view=%q overlay=%q", m.view, m.overlay)
	}
}

func TestMouseActionsAreLogged(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	var calls []map[string]any
	m.log = func(kind string, fields map[string]any) {
		if kind == "mouse" {
			calls = append(calls, fields)
		}
	}
	_, _ = m.Update(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if len(calls) != 1 || calls[0]["event"] != "click" || calls[0]["target"] != "none" {
		t.Fatalf("canvas inset click log = %#v", calls)
	}
	_, _ = m.Update(tea.MouseClickMsg{X: 5, Y: 1, Button: tea.MouseLeft})
	if len(calls) != 2 || calls[1]["target"] != "source" {
		t.Fatalf("identity click log = %#v", calls)
	}
	_, _ = m.Update(tea.MouseWheelMsg{X: 5, Y: 6, Button: tea.MouseWheelDown})
	if len(calls) != 3 || calls[2]["event"] != "wheel" || calls[2]["button"] != "wheelDown" {
		t.Fatalf("wheel log = %#v", calls)
	}
}

func TestClickSelectedRowDoesNotToggleQueueFocus(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.source = "apple-music"
	m.state = core.PlaybackState{
		Status: "playing", Source: "apple-music", Mode: "full", QueueIndex: 0,
		Queue: []core.Item{{Kind: "song", ID: "1", Title: "A"}, {Kind: "song", ID: "2", Title: "B"}},
		Track: &core.Item{Kind: "song", ID: "1", Title: "A"},
	}
	m.loading = false
	m.items = []core.Item{
		{Kind: "header", Title: "Continue Playing"},
		{Kind: "continue", Title: "A"},
		{Kind: "song", ID: "2", Ref: "apple-music:song:2", Title: "B"},
	}
	m.selected = 1
	l := m.layout()
	y := l.listTop + 1 + 1 // content row 1 == the selected continue row
	next, _ := m.handleClick(5, y, l)
	m = next.(Model)
	if m.queueFocus {
		t.Fatalf("a single click must not activate")
	}
	// Activation needs a double-click on the already-selected row.
	next, _ = m.handleClick(5, y, l)
	m = next.(Model)
	if !m.queueFocus {
		t.Fatalf("double-clicking the continue row did not focus Up Next")
	}
	// A third rapid click starts a fresh gesture: it selects again (leaving the
	// queue focus) but must not re-activate.
	next, _ = m.handleClick(5, y, l)
	m = next.(Model)
	if m.queueFocus {
		t.Fatalf("a fresh single click after activation should leave Up Next")
	}
}

func TestAlbumDetailPlayKeyPlaysWholeAlbum(t *testing.T) {
	m, f, _ := newModel(t)
	m.detailKind, m.detailID = "album", "al1"
	m.title = "Library Album"
	m.items = []core.Item{{Kind: "song", ID: "a1"}, {Kind: "song", ID: "a2"}}
	m.selected = 1
	next, cmd := m.handleKey(runeKey('p'))
	m = run(next.(Model), cmd)
	if f.played.Kind != "album" || f.played.StartAt != 0 || f.played.StartTrackID != "" || f.played.FromHere {
		t.Fatalf("playRequest = %#v", f.played)
	}
	if m.queueSource.Kind != "album" || m.queueSource.ID != "al1" {
		t.Fatalf("queue context = %#v", m.queueSource)
	}
}

// `:queue` must respect the same gate as `0`: with the queue capability
// undeclared (preview mode) the palette command says "Nothing is queued"
// instead of focusing a panel whose edits the engine would refuse
// (batch 2026-09-23-polish P2).
func TestPaletteQueueRespectsCapabilityGate(t *testing.T) {
	m, _, _ := newModel(t)
	track := core.Item{Kind: "song", ID: "s1", Title: "One"}
	m.state = core.PlaybackState{Status: "paused", Mode: "preview", Track: &track, Queue: []core.Item{track, {Kind: "song", ID: "s2", Title: "Two"}}, QueueIndex: 0}

	// Capability present: :queue focuses the panel.
	if indexOf(m.paletteCommands(), ":queue") < 0 {
		t.Fatalf("palette hides :queue with the capability declared: %v", m.paletteCommands())
	}
	next, _ := m.handleKey(runeKey(':'))
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Text: "queue"})
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.queueFocus {
		t.Fatalf(":queue did not focus the panel")
	}

	// Capability undeclared (preview mode): same answer as `0`.
	descriptors, _ := (&fake{}).Sources(context.Background())
	for i := range descriptors {
		if descriptors[i].ID == api.SourceAppleMusic {
			descriptors[i].Capabilities[api.CapQueue] = api.Capability{Available: false}
		}
	}
	next, _ = m.Update(sourcesMsg{descriptors: descriptors})
	m = next.(Model)
	m.queueFocus = false
	if indexOf(m.paletteCommands(), ":queue") >= 0 {
		t.Fatalf("palette advertises :queue without the capability: %v", m.paletteCommands())
	}
	next, _ = m.handleKey(runeKey(':'))
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Text: "queue"})
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.queueFocus || m.message != "Nothing is queued" {
		t.Fatalf(":queue bypassed the capability gate: focus=%v message=%q", m.queueFocus, m.message)
	}
}
