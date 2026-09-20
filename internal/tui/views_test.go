package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
