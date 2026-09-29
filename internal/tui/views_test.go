package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// The paced fill publishes queueFill on every append; the dock must show the
// real 9/16 count instead of a bare elapsed estimate (OQ3's original report:
// a 10-40s fill with only "working…").
func TestBusyDockShowsFillProgressCounts(t *testing.T) {
	m, _, _ := newModel(t)
	m.busy, m.busySince = true, time.Now().Add(-8*time.Second)
	m.renderTime = time.Now()
	m.state = core.PlaybackState{Status: "playing", QueueFill: &core.QueueFill{Queued: 9, Total: 16}}
	lines := m.nowBody(80)
	if !strings.Contains(lines[0], "working… 9/16") {
		t.Fatalf("fill dock = %q, want the queued/total counts", lines[0])
	}
}

func TestNowBodyIdentityRow(t *testing.T) {
	m, _, _ := newModel(t)
	track := core.Item{Kind: "song", ID: "1", Title: "Song", Artist: "Artist"}
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &track, Position: 5, Duration: 60}
	body := m.nowBody(80)
	// Identity row plus the reserved fact area: the height is fixed so a longer
	// fact wraps instead of moving the workspace (design-system.md §5).
	if len(body) != factRowsReserved+1 {
		t.Fatalf("now body rows = %d, want identity + %d fact rows:\n%v", len(body), factRowsReserved, body)
	}
	if !strings.Contains(body[0], "Song — Artist") {
		t.Fatalf("identity row missing:\n%v", body)
	}
	if strings.TrimSpace(body[1]) == "" {
		t.Fatalf("facts row missing:\n%v", body)
	}
}

// Search rows that differ only by album must carry the album in their
// secondary metadata: the same title/artist across re-releases and
// compilations otherwise reads as N identical rows (usability probe
// 2026-09-23-apple-browser-preview, finding 1).
func TestListRowsDisambiguateByAlbum(t *testing.T) {
	m, _, _ := newModel(t)
	m.input.Blur()
	m.title = "Search: blinding lights"
	m.items = []core.Item{
		{Kind: "song", ID: "1", Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours"},
		{Kind: "song", ID: "2", Title: "Blinding Lights", Artist: "The Weeknd", Album: "The Highlights"},
		{Kind: "song", ID: "3", Title: "Blinding Lights", Artist: "The Weeknd"},
	}
	lines := plainText(strings.Join(m.listLines(100, 10), "\n"))
	if !strings.Contains(lines, "Blinding Lights — The Weeknd · After Hours") {
		t.Fatalf("album missing from the metadata chain:\n%s", lines)
	}
	if !strings.Contains(lines, "Blinding Lights — The Weeknd · The Highlights") {
		t.Fatalf("second album missing:\n%s", lines)
	}
	if strings.Count(lines, "Blinding Lights — The Weeknd · ") != 2 {
		t.Fatalf("album chained onto a row without one:\n%s", lines)
	}
}

// A fact that does not fit one row wraps inside the reserved area instead of
// being cut off — the whole point of reserving a second row.
func TestNowBodyFactAreaWrapsInsteadOfTruncating(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	// Long enough to need the second row at 60 columns, short enough to fit both.
	m.account = "Account: not signed in — previews only (:auth to sign in to Apple Music)"
	m.state = core.PlaybackState{Status: "stopped", Mode: "none", Source: "apple-music"}

	body := m.nowBody(56)
	if len(body) != factRowsReserved+1 {
		t.Fatalf("now body rows = %d, want %d", len(body), factRowsReserved+1)
	}
	plain := plainText(strings.Join(body, "\n"))
	if !strings.Contains(plain, "Account: not signed in") {
		t.Fatalf("the warning lost its head:\n%s", plain)
	}
	if !strings.Contains(plain, "Apple Music") {
		t.Fatalf("the warning was truncated instead of wrapped:\n%s", plain)
	}
	if strings.Contains(plain, "…") {
		t.Fatalf("a wrapped warning should not be ellipsized:\n%s", plain)
	}
}

// Only content that cannot fit even the reserved area is ellipsized, and it stays
// inside the fixed height.
func TestNowBodyFactAreaEllipsizesBeyondTheReservedRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.account = "Account: " + strings.Repeat("very long warning ", 12)
	m.state = core.PlaybackState{Status: "stopped", Mode: "none", Source: "apple-music"}

	body := m.nowBody(40)
	if len(body) != factRowsReserved+1 {
		t.Fatalf("now body rows = %d, want the fixed height", len(body))
	}
	if !strings.Contains(plainText(body[factRowsReserved]), "…") {
		t.Fatalf("overflowing content must end in an ellipsis:\n%s", plainText(strings.Join(body, "\n")))
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
	// The status counts entries (batch 2026-09-23-postaudit-recheck N4 moved
	// scrolling to entries); one `j` advanced the page by exactly one entry,
	// so the first entry number is now 2. The full entry contract is pinned by
	// TestHelpStatusReportsEntryRangeNotWrappedRows.
	if !strings.Contains(second, "Entries 2–") {
		t.Fatalf("scroll position not reflected:\n%s", second)
	}
	if strings.Contains(second, "Entries 1–") {
		t.Fatalf("scrolled page still reports the top range:\n%s", second)
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
	if cmd == nil {
		t.Fatal("q should quit from help")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not produce QuitMsg")
	}
}

// The two-column workspace is split by readable width, not a fixed
// percentage (docs/ui/design-system.md §2.2): the rail must fit a full
// "title — artist" row while main keeps its floor, so narrow terminals fall
// back to a single column.
func TestWorkspaceBreakpointsFollowReadableWidths(t *testing.T) {
	cases := []struct {
		width            int
		showRail         bool
		mainWidth, railW int
	}{
		{width: 88, showRail: false, mainWidth: 86},
		{width: 100, showRail: false, mainWidth: 98},
		{width: 108, showRail: true, mainWidth: 60, railW: 45},
		{width: 112, showRail: true, mainWidth: 60, railW: 49},
		{width: 130, showRail: true, mainWidth: 69, railW: 58},
		{width: 160, showRail: true, mainWidth: 99, railW: 58},
	}
	for _, test := range cases {
		m, _, _ := newModel(t)
		m.width, m.height = test.width, 30
		l := m.layout()
		if l.showRail != test.showRail {
			t.Fatalf("width %d: showRail=%v, want %v", test.width, l.showRail, test.showRail)
		}
		if l.mainWidth != test.mainWidth {
			t.Fatalf("width %d: mainWidth=%d, want %d", test.width, l.mainWidth, test.mainWidth)
		}
		if l.panelWidth != test.railW {
			t.Fatalf("width %d: panelWidth=%d, want %d", test.width, l.panelWidth, test.railW)
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

func TestDefaultThemeIsGruvbox(t *testing.T) {
	m, _, store := newModel(t)
	if m.themeName != "gruvbox" || store.Theme != "gruvbox" {
		t.Fatalf("default theme = %q (store %q), want gruvbox", m.themeName, store.Theme)
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

func TestOverlayQAndCtrlCQuit(t *testing.T) {
	for _, overlay := range []string{"help", "info"} {
		t.Run(overlay, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.overlay = overlay
			next, cmd := m.handleKey(runeKey('q'))
			if next.(Model).overlay != overlay || cmd == nil {
				t.Fatalf("q = overlay %q cmd=%v, want quit", next.(Model).overlay, cmd != nil)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("q did not quit")
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

func TestSearchOverlayTitleFollowsSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.source, m.overlay, m.inputMode = "audius", "input", "search"
	if view := plainText(m.View().Content); !strings.Contains(view, "Source: Audius") || strings.Contains(view, "Source: Apple Music") {
		t.Fatalf("search overlay title = %q", view)
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

// Palette candidates and source choices are text the user must read; bare rows
// inherit the terminal colours and vanish on a painted canvas (light themes).
func TestOverlayListRowsUseThemeTokens(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.renderer = newRenderer(theme.Load("print-room"))
	m.themeNames = theme.Names()

	m.overlay = "palette"
	view := m.overlayView(80, 20)
	// Rows carry their description since the polish batch, and stay inside the
	// theme token (the escape prefix pattern mirrors the source-row check).
	paletteNeedle := strings.TrimSuffix(m.renderer.tabStyle.Render(""), "\x1b[m") + "  :help — open Help"
	if !strings.Contains(view, paletteNeedle) {
		t.Fatalf("palette rows are unstyled:\n%s", view)
	}

	m.overlay = "source-switcher"
	view = m.overlayView(80, 20)
	// The selected row keeps the selection token and carries the lists' ›
	// marker (the label is appended to the styled prefix, so check the row
	// starts inside a styled span rather than with a bare reset).
	needle := strings.TrimSuffix(m.renderer.selStyle.Render(""), "\x1b[m") + "› Apple Music"
	if !strings.Contains(view, needle) {
		t.Fatalf("selected source row lost the selection token:\n%s", view)
	}
	// A bare line start would mean the row inherits the terminal colours.
	for _, bare := range []string{"\n  Audius", "\n  Radio"} {
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

// The quit key is the safety affordance: it must survive every width budget,
// dropping later hints first (usability r13: Radio and 80×18 hid it entirely).
func TestFooterKeepsQuitVisibleAtAnyWidth(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &core.Item{Kind: "song", Title: "One"}}
	for _, width := range []int{120, 100, 80, 60, 44} {
		footer := plainText(m.footerLine(width))
		if !strings.Contains(footer, "q quit") {
			t.Fatalf("width %d lost the quit hint: %q", width, footer)
		}
		if !strings.Contains(footer, "enter open/play") {
			t.Fatalf("width %d lost the primary hint: %q", width, footer)
		}
	}
}

// The too-small screen must be actionable: current size, the console minimum,
// and the quit key (r13 r3 scored 1/10 because none of these were shown).
func TestTinyScreenStatesSizeAndQuit(t *testing.T) {
	m, _, _ := newModel(t)
	minWidth, minHeight := m.consoleMinimum()
	out := plainText(m.tinyView(60, 12))
	if !strings.Contains(out, "Terminal too small") {
		t.Fatalf("tiny view lost its identity: %q", out)
	}
	if !strings.Contains(out, fmt.Sprintf("needs at least %d×%d", minWidth, minHeight)) || !strings.Contains(out, "now 60×12") {
		t.Fatalf("tiny view lacks the size facts: %q", out)
	}
	if !strings.Contains(out, "q quit") {
		t.Fatalf("tiny view lacks the quit key: %q", out)
	}
}

// The capability-gated Help must follow the live descriptor snapshot: after
// the server reports Apple shuffle available (resource runtime ready, account
// capabilities settled), the S/R row appears without a TUI restart
// (usability batch 2026-09-21-r13).
func TestHelpShowsShuffleWhenCapabilityReportsIt(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "apple-music"
	m.descriptors = []api.SourceDescriptor{{ID: api.SourceAppleMusic, Available: true, Capabilities: map[string]api.Capability{}}}
	for _, row := range m.helpLines(60) {
		if strings.Contains(row, "shuffle") {
			t.Fatalf("stale snapshot claims shuffle: %q", row)
		}
	}
	m.descriptors = []api.SourceDescriptor{{
		ID: api.SourceAppleMusic, Available: true,
		Capabilities: map[string]api.Capability{
			api.CapShuffle: {Available: true},
			api.CapRepeat:  {Available: true},
		},
	}}
	found := false
	for _, row := range m.helpLines(60) {
		if strings.Contains(row, "shuffle") {
			found = true
		}
	}
	if !found {
		t.Fatal("refreshed capability did not surface the shuffle help row")
	}
}

// With shuffle on the rail says so: its rows are the submitted order, which is
// not the order the audio plays in.
func TestShuffledRailSaysTheOrderIsNotThePlayOrder(t *testing.T) {
	m, _, _ := newModel(t)
	m.state.Queue = []core.Item{
		{Kind: "song", ID: "am:1", Ref: "apple-music:song:1", Title: "One"},
		{Kind: "song", ID: "am:2", Ref: "apple-music:song:2", Title: "Two"},
	}
	m.state.QueueIndex = 0
	m.width, m.height = 120, 32

	plain := m.queueTitle()
	if plain != "Up Next" {
		t.Fatalf("unshuffled title = %q, want Up Next", plain)
	}
	m.state.Shuffle = true
	if shuffled := m.queueTitle(); !strings.Contains(shuffled, "shuffled") {
		t.Fatalf("shuffled title = %q, want it to name the shuffle", shuffled)
	}
	view := plainText(m.View().Content)
	if !strings.Contains(view, "UP NEXT · SHUFFLED") {
		t.Fatalf("rail header does not annotate the shuffle:\n%s", view)
	}
	// The rows keep the submitted order and their indices.
	if strings.Index(view, "One") > strings.Index(view, "Two") {
		t.Fatalf("shuffle reordered the rail:\n%s", view)
	}
}

// Shuffle history: without shuffle, rows before the current one are dimmed
// played history; with shuffle, they were skipped, not played, and stay
// upcoming (docs/ui/ux.md).
func TestShuffledQueueJumpDoesNotDimSkippedRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.state.Queue = []core.Item{
		{Kind: "song", ID: "am:1", Ref: "apple-music:song:1", Title: "One"},
		{Kind: "song", ID: "am:2", Ref: "apple-music:song:2", Title: "Two"},
		{Kind: "song", ID: "am:3", Ref: "apple-music:song:3", Title: "Three"},
	}
	m.state.QueueIndex = 2 // jumped from row 1 to row 3
	m.width, m.height = 120, 32

	history := m.queueLines(60, 3)
	if !strings.Contains(plainText(history[0]), "· One") {
		t.Fatalf("unshuffled jump did not dim the skipped rows: %q", plainText(history[0]))
	}

	m.state.Shuffle = true
	shuffled := m.queueLines(60, 3)
	for i, row := range shuffled[:2] {
		if strings.Contains(plainText(row), "·") {
			t.Fatalf("shuffled row %d was marked as played history: %q", i, plainText(row))
		}
	}
	if !strings.Contains(plainText(shuffled[2]), "▶") {
		t.Fatalf("current row lost its marker: %q", plainText(shuffled[2]))
	}
}

// A fill in progress reports its own counts instead of an elapsed-time guess.
func TestFillProgressIsShownWhileFilling(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.busy = true
	m.busySince = m.renderTime.Add(-30 * time.Second)

	if got := m.busyLabel(); !strings.Contains(got, "30s") || strings.Contains(got, "large queues") {
		t.Fatalf("without progress the label should fall back to elapsed time: %q", got)
	}
	m.state.QueueFill = &core.QueueFill{Queued: 9, Total: 16}
	got := m.busyLabel()
	if !strings.Contains(got, "9/16") {
		t.Fatalf("label = %q, want the fill counts", got)
	}
	if strings.Contains(got, "30s") {
		t.Fatalf("label still guesses from time: %q", got)
	}
	m.state.QueueFill = &core.QueueFill{Queued: 2, Total: 5}
	if got := m.busyLabel(); !strings.Contains(got, "2/5") || strings.Contains(got, "large") {
		t.Fatalf("short queue claimed to be large: %q", got)
	}
}

func TestCompactFooterKeepsStopAndQuitBeforeOptionalHints(t *testing.T) {
	m, _, _ := newModel(t)
	m.source = "audius"
	m.state = core.PlaybackState{
		Status: "playing", Track: &core.Item{Kind: "song", Title: "Song"},
		Queue: []core.Item{{Kind: "song", Title: "Song"}},
	}
	for _, width := range []int{78, 80} {
		line := plainText(m.footerLine(width))
		if !strings.Contains(line, "v stop") || !strings.Contains(line, "q quit") {
			t.Fatalf("width %d lost playback safety keys: %q", width, line)
		}
	}
}

func TestPlaybackInfoAuthBelongsToCurrentSource(t *testing.T) {
	m, _, _ := newModel(t)
	m.authorization = "denied" // Apple helper's playback state, not Radio's.
	m.account = "Account: Apple account"
	m.state = core.PlaybackState{Status: "playing", Source: "apple-music"}
	m.source = "radio"
	info := strings.Join(m.infoLines(80), "\n")
	if strings.Contains(info, "Auth") || strings.Contains(info, "Apple account") {
		t.Fatalf("radio playback info leaked Apple authorization: %q", info)
	}
	m.source = "audius"
	m.sourceAuth = core.AuthorizationStatus{Status: "authorized", AccountLabel: "Audius account"}
	info = strings.Join(m.infoLines(80), "\n")
	if !strings.Contains(info, "Auth       authorized") || strings.Contains(info, "Auth       denied") || strings.Contains(info, "Apple account") {
		t.Fatalf("Audius playback info used the wrong source: %q", info)
	}
}

func TestSwitchSourceUsesSourceKeyedWatchAuthorization(t *testing.T) {
	_, provider, store := newModel(t)
	snapshot := api.WatchSnapshot{
		Sequence: 10,
		Authorizations: []api.SourceAuthorization{
			{Source: api.SourceAppleMusic, Status: api.AuthDenied},
			{Source: api.SourceAudius, Status: api.AuthAuthorized},
			{Source: api.SourceJamendo, Status: api.AuthNotRequired},
		},
	}
	m := New(Options{Provider: provider, Player: provider, Radio: fakeRadio{}, Store: store,
		Source: "apple-music", InitialWatch: &snapshot})
	m.authorization = "denied" // Global playback status must not leak to Audius.
	for _, check := range []struct{ source, status string }{
		{"audius", api.AuthAuthorized},
		{"jamendo", api.AuthNotRequired},
		{"apple-music", api.AuthDenied},
	} {
		next, _ := m.switchSource(check.source)
		m = next.(Model)
		if m.sourceAuth.Status != check.status {
			t.Fatalf("%s source authorization = %q, want %q", check.source, m.sourceAuth.Status, check.status)
		}
		if info := strings.Join(m.infoLines(80), "\n"); !strings.Contains(info, "Auth       "+check.status) {
			t.Fatalf("%s Playback Info has stale auth:\n%s", check.source, info)
		}
	}
	// An update for a source that is not selected must still be available when
	// the user switches to that source later, without an unversioned RPC read.
	next, _ := m.applyWatchUpdate(api.WatchUpdate{Kind: "authorization.changed", Sequence: 11,
		Authorization: &api.SourceAuthorization{Source: api.SourceAudius, Status: api.AuthExpired}})
	m = next.(Model)
	next, _ = m.switchSource("audius")
	m = next.(Model)
	if m.sourceAuth.Status != api.AuthExpired {
		t.Fatalf("switch ignored another source's watch update: %q", m.sourceAuth.Status)
	}
}

func TestFailedSourceSaveRestoresSourceAuthorization(t *testing.T) {
	m, _, _ := newModel(t)
	m.setAuthorizations([]api.SourceAuthorization{
		{Source: api.SourceAppleMusic, Status: api.AuthDenied},
		{Source: api.SourceAudius, Status: api.AuthAuthorized},
	})
	m.overlay = "source-switcher"
	next, stopCmd := m.beginSourceSwitch("audius")
	m = next.(Model)
	if stopCmd == nil {
		t.Fatal("source switch did not start")
	}
	next, persistCmd := m.Update(stopCmd())
	m = next.(Model)
	if persistCmd == nil || m.source != "audius" || m.sourceAuth.Status != api.AuthAuthorized {
		t.Fatalf("target source was not projected: source=%q auth=%q", m.source, m.sourceAuth.Status)
	}
	next, _ = m.Update(persistenceMsg{operationID: m.operationID, kind: "source", source: "audius", err: fmt.Errorf("disk full")})
	m = next.(Model)
	if m.source != "apple-music" || m.sourceAuth.Status != api.AuthDenied || !m.messageErr {
		t.Fatalf("failed save did not restore source and auth: source=%q auth=%q message=%q", m.source, m.sourceAuth.Status, m.message)
	}
	if info := strings.Join(m.infoLines(80), "\n"); !strings.Contains(info, "Auth       denied") || strings.Contains(info, "Auth       authorized") {
		t.Fatalf("Playback Info still shows target authorization after rollback:\n%s", info)
	}
}

// A fill another client started still shows its progress here: the dock reads
// the committed state, not this model's own busy flag.
func TestFillFromAnotherClientShowsProgress(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.busy = false
	m.state.Track = nil
	m.state.QueueFill = &core.QueueFill{Queued: 4, Total: 12}

	body := strings.Join(m.nowBody(60), "\n")
	if !strings.Contains(body, "4/12") {
		t.Fatalf("dock = %q, want the fill progress", body)
	}
}

// Overlays must span the full width when side margins would shrink below 6
// cells: a couple of base-frame cells peeking out on each side read as broken
// borders (batch 2026-09-23-postaudit M1).
func TestDialogWidthSpansNarrowTerminals(t *testing.T) {
	if got := dialogWidth(74, 110); got != 74 {
		t.Fatalf("wide terminal dialog width = %d", got)
	}
	if got := dialogWidth(74, 80); got != 80 {
		t.Fatalf("80-col dialog width = %d, want full width", got)
	}
	if got := dialogWidth(64, 70); got != 70 {
		t.Fatalf("70-col dialog width = %d, want full width", got)
	}
	if got := dialogWidth(64, 100); got != 64 {
		t.Fatalf("100-col dialog width = %d", got)
	}
}

func TestHelpOverlaySpansFullWidthAt80Cols(t *testing.T) {
	m, _, _ := newModel(t)
	layout := m.helpOverlay(80, 18)
	if layout.boxWidth != 80 {
		t.Fatalf("help box width = %d, want full 80", layout.boxWidth)
	}
	view := m.overlayView(80, 18)
	// Row 0 of the base frame ("Apple Music … lilt") must not peek out beside
	// the dialog.
	for _, line := range strings.Split(view, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "Apple Music") || strings.HasSuffix(strings.TrimRight(line, " "), "lilt") {
			t.Fatalf("base frame leaked beside the help dialog: %q", line)
		}
	}
}

// The context row of a pushed results page names the group jump (M3).
func TestListContextNamesResultGroups(t *testing.T) {
	m, _, _ := newModel(t)
	m = nextModel(m.pushAggregate("Search: lofi", nil))
	m.items = []core.Item{
		{Kind: "header", Title: "Songs"},
		{Kind: "song", ID: "s1", Title: "One"},
		{Kind: "header", Title: "Playlists"},
		{Kind: "playlist", ID: "p1", Title: "List"},
	}
	if ctx := m.listContext(); !strings.Contains(ctx, "Songs 1/2 · [/] group") {
		t.Fatalf("list context = %q", ctx)
	}
}

func TestSearchEmptyStateDoesNotReportTrendingUnavailable(t *testing.T) {
	for _, source := range []string{"audius", "jamendo"} {
		t.Run(source, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.source, m.view, m.title = source, "Discover", "Discover"
			m.loading = false
			if got := m.emptyText(); got != "(empty) — no trending available right now" {
				t.Fatalf("Discover empty text = %q", got)
			}

			m = nextModel(m.pushAggregate("Search: impossible-query", nil))
			m.loading = false
			want := "(empty) — no matching results; press / to search again"
			if got := m.emptyText(); got != want {
				t.Fatalf("search empty text = %q, want %q", got, want)
			}
			if view := plainText(m.View().Content); !strings.Contains(view, want) || strings.Contains(view, "no trending available right now") {
				t.Fatalf("search frame has wrong empty state:\n%s", view)
			}
		})
	}
}

func TestAggregateEmptyStateUsesPageClassNotTitle(t *testing.T) {
	m, _, _ := newModel(t)
	m.source, m.view, m.title = "audius", "Discover", "Discover"
	m = nextModel(m.pushAggregate("Unusual aggregate title", nil))
	m.loading = false
	if got := m.emptyText(); got != "(empty) — no matching results; press / to search again" {
		t.Fatalf("aggregate empty text = %q", got)
	}
	if got := m.listTitle(); got != "Search" {
		t.Fatalf("aggregate list title = %q", got)
	}
	m.pageClass = pageClassContainer
	m.title = "Search: looks like aggregate"
	if got := m.emptyText(); got == "(empty) — no matching results; press / to search again" {
		t.Fatal("container title was treated as a search")
	}
}

func TestHelpSearchHintDescribesCurrentSource(t *testing.T) {
	for _, source := range []string{"apple-music", "audius", "jamendo", "radio"} {
		t.Run(source, func(t *testing.T) {
			m, _, _ := newModel(t)
			m.source = source
			help := plainText(strings.Join(m.helpContent(100).rows, "\n"))
			if !strings.Contains(help, "search current source") || !strings.Contains(help, "Radio Search & Filters") {
				t.Fatalf("search help missing source-aware behavior:\n%s", help)
			}
			if strings.Contains(help, "Apple Music search") {
				t.Fatalf("search help falsely limits other sources:\n%s", help)
			}
		})
	}
}

func TestHelpBackHintLimitsBackToPushedPages(t *testing.T) {
	m, _, _ := newModel(t)
	help := plainText(strings.Join(m.helpContent(100).rows, "\n"))
	if !strings.Contains(help, "back from pushed page or clear filter") || strings.Contains(help, "back or clear filter") {
		t.Fatalf("help implies top-level Esc goes back:\n%s", help)
	}
}

// Help pages never split an entry: a page starts and ends on an entry start, so
// no page opens on an orphan continuation row (batch 2026-09-23-postaudit-recheck
// N4).
func TestHelpWindowNeverSplitsEntries(t *testing.T) {
	starts := []int{0, 3, 7, 9, 14, 20}
	// A window whose row budget would cut the entry at 3 stops there instead.
	if start, end := helpWindow(starts, 0, 6, 24); start != 0 || end != 3 {
		t.Fatalf("first page = %d-%d, want 0-3", start, end)
	}
	// An offset inside an entry snaps down to that entry's start.
	if start, _ := helpWindow(starts, 5, 6, 24); start != 3 {
		t.Fatalf("mid-entry offset started the page at %d, want 3", start)
	}
	// The final page shows the tail: the earliest start whose rows all fit.
	if start, end := helpWindow(starts, 18, 6, 24); end != 24 || start != 20 {
		t.Fatalf("last page = %d-%d, want 20-24", start, end)
	}
	// A short body is one page.
	if start, end := helpWindow(starts, 0, 30, 24); start != 0 || end != 24 {
		t.Fatalf("short body = %d-%d, want 0-24", start, end)
	}
}

// The favorite hint survives playback: the playback hints used to push it past
// the width budget, so `f favorite` vanished from the footer while playing even
// though the key worked (batch 2026-09-23-postaudit-recheck N1).
func TestFooterKeepsFavoriteHintWhilePlaying(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.source, m.view, m.title = "apple-music", "Home", "Home"
	m.items = []core.Item{{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"}}
	m.selected = 0
	track := m.items[0]
	m.state = core.PlaybackState{Status: "playing", Mode: "full", Track: &track, Queue: []core.Item{track, {Kind: "song", ID: "s2", Title: "Two"}}, QueueIndex: 0}
	footer := m.footerLine(110)
	if !strings.Contains(footer, "f favorite") {
		t.Fatalf("playing footer hides the favorite hint: %q", footer)
	}
}

// A container row opens its detail page; the hint says open, not open/play
// (batch 2026-09-23-postaudit-recheck N2).
func TestFooterSaysOpenForContainerRows(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 140, 30
	m.source, m.view, m.title = "apple-music", "Home", "Home"
	m.items = []core.Item{
		{Kind: "song", ID: "s1", Ref: "apple-music:song:s1", Title: "One"},
		{Kind: "playlist", ID: "p1", Ref: "apple-music:playlist:p1", Title: "List"},
	}
	m.selected = 0
	if footer := m.footerLine(140); !strings.Contains(footer, "enter open/play") {
		t.Fatalf("song row hint = %q, want enter open/play", footer)
	}
	m.selected = 1
	if footer := m.footerLine(140); !strings.Contains(footer, "enter open") || strings.Contains(footer, "enter open/play") {
		t.Fatalf("playlist row hint = %q, want enter open", footer)
	}
}

// A playback start names its target instead of a bare "working…"
// (batch 2026-09-23-postaudit-recheck N5).
func TestBusyLabelNamesThePlayTarget(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.busy, m.busySince = true, time.Now()
	m.renderTime = m.busySince
	m.playTarget = "Bohemian Rhapsody"
	if got := m.busyLabel(); !strings.Contains(got, "Bohemian Rhapsody") || !strings.Contains(got, "loading") {
		t.Fatalf("short wait label = %q", got)
	}
	m.renderTime = m.busySince.Add(12 * time.Second)
	if got := m.busyLabel(); !strings.Contains(got, "12s") || !strings.Contains(got, "Bohemian Rhapsody") {
		t.Fatalf("long wait label = %q", got)
	}
	// The fill counts still win: they are real progress, not a guess.
	m.state.QueueFill = &core.QueueFill{Queued: 3, Total: 9}
	if got := m.busyLabel(); !strings.Contains(got, "3/9") {
		t.Fatalf("fill label = %q", got)
	}
}

// The too-small notice keeps the current size readable at its narrowest: the
// size lives on its own row (batch 2026-09-23-polish p2).
func TestTinyViewKeepsCurrentSizeAtThirtyColumns(t *testing.T) {
	m, _, _ := newModel(t)
	view := plainText(m.tinyView(30, 8))
	if !strings.Contains(view, "needs at least") || !strings.Contains(view, "now 30×8") {
		t.Fatalf("tiny view lost the size facts:\n%s", view)
	}
}

// A Help page that fills its box still shows the close hint, and a narrow
// status drops the scroll keys before the close keys (batch 2026-09-23-polish p4/p5).
func TestHelpStatusKeepsCloseHintWhenBodyFillsTheBox(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height, m.overlay = 44, 17, "help"
	view := plainText(m.overlayView(44, 17))
	if !strings.Contains(view, "Esc/? close") {
		t.Fatalf("narrow help lost the close hint:\n%s", view)
	}
	// Playback Info at the same width keeps its close hint too.
	m.overlay = "info"
	view = plainText(m.overlayView(44, 17))
	if !strings.Contains(view, "Esc/? close") {
		t.Fatalf("playback info lost the close hint:\n%s", view)
	}
}

// The help status counts entries, not wrapped rows (OQ41: the row-based
// "1-14/30 → 2-14/30" read as row trivia). The range derives from the entry
// starts: one `j` moves the first entry by exactly one, the tail page ends on
// the entry total, and continuation rows never inflate the numbers. Dialogs
// without entry starts (Playback Info) keep reporting rows.
func TestHelpStatusReportsEntryRangeNotWrappedRows(t *testing.T) {
	const w, h = 46, 14
	m, _, _ := newModel(t)
	m.width, m.height, m.overlay = w, h, "help"
	layout := m.helpOverlay(w, h)
	// A narrow body wraps descriptions onto continuation rows; those must not
	// be countable units.
	if len(layout.rows) <= len(layout.starts) {
		t.Fatalf("expected wrapped continuation rows: rows=%d starts=%d", len(layout.rows), len(layout.starts))
	}
	contentRows := max(1, layout.visible-1)
	start, end := helpWindow(layout.starts, 0, contentRows, len(layout.rows))
	if start != 0 {
		t.Fatalf("first page starts at row %d, want 0", start)
	}
	lastEntry := 0
	for _, s := range layout.starts {
		if s < end {
			lastEntry++
		}
	}
	firstPage := plainText(m.overlayView(w, h))
	want := fmt.Sprintf("Entries 1–%d of %d", lastEntry, len(layout.starts))
	if !strings.Contains(firstPage, want) {
		t.Fatalf("first page status missing %q:\n%s", want, firstPage)
	}
	if rowForm := fmt.Sprintf("1–%d of %d", end, len(layout.rows)); strings.Contains(firstPage, rowForm) {
		t.Fatalf("status reports wrapped rows (%q) instead of entries:\n%s", rowForm, firstPage)
	}

	// One `j` scrolls exactly one entry: the first entry number advances by one.
	// (The page end may gain an entry — only the start is pinned to +1.)
	next, _ := m.handleKey(runeKey('j'))
	m = next.(Model)
	if m.helpOffset != layout.starts[1] {
		t.Fatalf("j scrolled to offset %d, want entry start %d", m.helpOffset, layout.starts[1])
	}
	scrolled := plainText(m.overlayView(w, h))
	if !strings.Contains(scrolled, "Entries 2–") {
		t.Fatalf("one j must advance the first entry by exactly one:\n%s", scrolled)
	}
	if strings.Contains(scrolled, "Entries 1–") {
		t.Fatalf("the entry range did not move:\n%s", scrolled)
	}

	// The tail page reports the last entry as its end.
	next, _ = m.handleKey(runeKey('G'))
	m = next.(Model)
	tail := plainText(m.overlayView(w, h))
	if !strings.Contains(tail, fmt.Sprintf("–%d of %d", len(layout.starts), len(layout.starts))) {
		t.Fatalf("tail page does not end on the entry total:\n%s", tail)
	}

	// Playback Info has no entry starts: it keeps reporting rows and never
	// claims entries.
	info, _, _ := newModel(t)
	info.width, info.height, info.overlay = 44, 17, "info"
	info.state = core.PlaybackState{Status: "playing", Track: &core.Item{Kind: "song", ID: "s1", Title: "T", URL: "https://example.test/t"}}
	info.state.Available = []string{"AAC 64", "AAC 96", "AAC 128", "AAC 256", "AAC 320", "ALAC 16/44", "ALAC 24/48", "ALAC 24/96"}
	infoLayout := info.helpOverlay(44, 17)
	if len(infoLayout.starts) != 0 || len(infoLayout.rows) <= infoLayout.visible {
		t.Fatalf("playback info should scroll without entry starts: starts=%d rows=%d visible=%d", len(infoLayout.starts), len(infoLayout.rows), infoLayout.visible)
	}
	infoView := plainText(info.overlayView(44, 17))
	if !strings.Contains(infoView, "Rows 1–") || strings.Contains(infoView, "Entries") {
		t.Fatalf("playback info status must report rows, not entries:\n%s", infoView)
	}
	// The row keys really scroll the row window the status advertises; the
	// overlay used to show the hint while j/k were inert (OQ41 review).
	nextInfo, _ := info.handleKey(runeKey('j'))
	info = nextInfo.(Model)
	if info.helpOffset != 1 {
		t.Fatalf("j did not scroll playback info: offset=%d", info.helpOffset)
	}
	scrolledInfo := plainText(info.overlayView(44, 17))
	if !strings.Contains(scrolledInfo, "Rows 2–") || strings.Contains(scrolledInfo, "Rows 1–") {
		t.Fatalf("playback info row range did not move:\n%s", scrolledInfo)
	}
	nextInfo, _ = info.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	info = nextInfo.(Model)
	if info.helpOffset <= 1 {
		t.Fatalf("pgdown did not page playback info: offset=%d", info.helpOffset)
	}
}

// `i` opens playback diagnostics: the overlay is named for what it shows, and
// it never doubles as a details view of the selected list row (OQ41).
func TestPlaybackInfoOverlayNamedForPlayback(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 100, 30
	m.items = []core.Item{{Kind: "song", ID: "s1", Title: "Selected Song", Artist: "Nobody"}}
	m.selected = 0
	next, _ := m.handleKey(runeKey('i'))
	m = next.(Model)
	view := plainText(m.overlayView(100, 30))
	if !strings.Contains(view, "┌─ Playback Info") {
		t.Fatalf("overlay title is not Playback Info:\n%s", view)
	}
	if strings.Contains(view, "Selected Song") {
		t.Fatalf("playback info adopted the selected row's details:\n%s", view)
	}
	// Help describes the key by the same name.
	help := plainText(strings.Join(m.helpContent(100).rows, "\n"))
	if !strings.Contains(help, "theme picker / playback info") || strings.Contains(help, "track info") {
		t.Fatalf("help row for t/i = %q", help)
	}
}

// The palette row names what the command does (batch 2026-09-23-polish p4).
func TestPaletteRowsExplainCommands(t *testing.T) {
	if got := paletteDescription(":play <ref>"); !strings.Contains(got, "apple-music:song:") {
		t.Fatalf(":play description = %q", got)
	}
	if got := paletteDescription(":queue"); !strings.Contains(got, "Up Next") {
		t.Fatalf(":queue description = %q", got)
	}
	if got := paletteDescription(":unknown"); got != "" {
		t.Fatalf("unknown command described: %q", got)
	}
}
