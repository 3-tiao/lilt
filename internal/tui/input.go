package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbletea/v2"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/jamendo"
	"github.com/3-tiao/lilt/internal/presentation"
	"github.com/3-tiao/lilt/internal/theme"
)

// acceptsTextEntry identifies inputs where a single KeyMsg may legitimately
// contain a complete pasted or programmatically supplied value. Command-list
// key bursts are expanded above; URLs and search text must remain intact.
func (m Model) acceptsTextEntry() bool {
	return m.input.Focused() || m.overlay == "discovery" || m.overlay == "discovery-text" || m.overlay == "discovery-options"
}

// scrollQueue drags the Up Next window from its current position instead of
// walking the queue cursor. Moving the cursor first re-centred the window on a
// stale cursor (often entry 0), so wheeling jumped to the top of the queue
// before scrolling. The cursor only follows when it would leave the window.
func (m Model) scrollQueue(delta, rows int) Model {
	total := len(m.state.Queue)
	if total == 0 || rows <= 0 {
		return m
	}
	start, _ := m.queueWindow(rows)
	start = clamp(start+delta, 0, max(0, total-rows))
	m.queueFocus = true
	m.queueOffset, m.queueOffsetSet = start, true
	if m.queueCursor < start {
		m.queueCursor = start
	}
	if m.queueCursor >= start+rows {
		m.queueCursor = start + rows - 1
	}
	m.queueCursor = clamp(m.queueCursor, 0, total-1)
	return m
}

// overlayBoxSize mirrors the overlay render geometry so clicks can be
// hit-tested against the box that is actually on screen.
func (m Model) overlayBoxSize() (int, int) {
	l := m.layout()
	width, height := l.width, l.height
	switch m.overlay {
	case "input", "discovery-text":
		if m.inputMode == "jamendo-setup" {
			// The setup modal adds two explanation rows above the input.
			return min(64, max(24, width-4)), min(height, 9)
		}
		return min(64, max(24, width-4)), min(height, 5)
	case "discovery":
		rows := 13
		if height-2 < 16 {
			rows = 9
		}
		return min(72, max(24, width-4)), min(height, min(rows+2, 16))
	case "discovery-options":
		rows := len(m.discoveryOptions) + 2
		if m.discoveryOptionsErr != "" {
			rows = 2
		} else if m.discoveryOptions == nil {
			rows = 1
		}
		return min(72, max(24, width-4)), min(height, min(rows+2, 16))
	case "theme":
		return min(40, width), min(len(m.themeNames)+3+2, height)
	case "source-switcher":
		return min(64, max(28, width-4)), min(height, len(m.sourceChoices())+1+2)
	case "auth":
		rows := len(m.authOverlayRows(60))
		return min(64, max(28, width-4)), min(height, rows+2)
	case "palette":
		rows := 1 + max(1, len(m.paletteMatches())) + 1
		return min(64, max(28, width-4)), min(height, rows+2)
	default:
		help := m.helpOverlay(width, height)
		return help.boxWidth, help.boxHeight
	}
}

// cancelOverlay dismisses the current overlay the way Esc would.
func (m Model) cancelOverlay() Model {
	switch m.overlay {
	case "input":
		return m.closeTextInput()
	case "auth":
		// Dismissal is not flow cancellation: the flow is server-owned and
		// may still complete (Esc is the explicit cancel). Drop local
		// tracking; the next authorization.changed re-reads the truth.
		m.overlay = ""
		m.authConfirm, m.authNotice, m.authNoticeErr = "", "", false
		m.authFlow = nil
		return m
	case "theme":
		m.overlay = ""
		m.themeName = m.store.Theme
		return m.setTheme(m.store.Theme)
	case "discovery", "discovery-text", "discovery-options":
		next, _ := m.cancelDiscovery()
		return next.(Model)
	default:
		m.overlay = ""
		return m
	}
}

// handleOverlayClick acts on a click inside the overlay box. Coordinates are
// relative to the box; the first body row is y=1.
func (m Model) handleOverlayClick(x, y int) (tea.Model, tea.Cmd) {
	body := y - 1
	switch m.overlay {
	case "input":
		// Keep the editor and its text; a single-line field has no click-to-place.
		m.input.Focus()
		return m, textinput.Blink
	case "theme":
		rows := len(m.themeNames) + 3
		_, h := m.overlayBoxSize()
		visible := max(0, h-2)
		start, _ := window(clamp(m.themeIndex, 0, max(0, rows-1)), rows, visible)
		index := start + body
		if index < 0 || index >= len(m.themeNames) {
			return m, nil
		}
		if index == m.themeIndex {
			// Second click on the highlighted row saves, like Enter.
			return m.handleThemeKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		m.themeIndex, m.themeName = index, m.themeNames[index]
		return m.setTheme(m.themeName), nil
	case "discovery-options":
		options := m.filteredDiscoveryOptions()
		_, h := m.overlayBoxSize()
		visible := max(0, h-2)
		total := len(options) + 2
		start, _ := window(clamp(m.discoverySelected, 0, max(0, total-1)), total, visible)
		index := start + body
		if index < 0 || index >= len(options) {
			return m, nil
		}
		if index == m.discoverySelected {
			return m.handleDiscoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		m.discoverySelected = index
		return m, nil
	case "source-switcher":
		sources := m.sourceChoices()
		if body < 0 || body >= len(sources) {
			return m, nil
		}
		m.overlaySelected = body
		if sources[body] == m.source {
			m.overlay = ""
			return m, nil
		}
		return m.beginSourceSwitch(sources[body])
	case "auth":
		sources := m.authRowSources()
		if body < 0 || body >= len(sources) {
			return m, nil
		}
		if body == m.authSelected {
			// A second click on the selected row confirms it, like Enter
			// (the discovery options list uses the same gesture).
			return m.handleAuthOverlayKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		m.authSelected = body
		return m, nil
	case "palette":
		matches := m.paletteMatches()
		index := body - 1
		if index < 0 || index >= len(matches) {
			return m, nil
		}
		m.overlaySelected = index
		m.overlay = ""
		m.input.Blur()
		return m.runPaletteCommand(strings.TrimPrefix(matches[index], ":"))
	default:
		// Help, info and the discovery menu keep keyboard focus; an inside click
		// simply does nothing instead of discarding the overlay.
		return m, nil
	}
}

// goBack pops a pushed page (search results, playlist detail). Mouse users reach
// it by clicking the panel title bar or the active view tab.
func (m Model) goBack() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		return m, nil
	}
	m = m.back()
	if m.source == "apple-music" && m.view == "Home" {
		m.loading = true
		return m, m.loadView()
	}
	return m, nil
}

// selectQueueRow focuses one visible queue entry; a consecutive same-entry
// click within doubleClickWindow (one double-click) jumps to it. Only the
// mouse reaches this function; the keyboard Enter jump is unconditional.
func (m Model) selectQueueRow(row, rows int) (tea.Model, tea.Cmd) {
	if len(m.state.Queue) == 0 || row < 0 {
		return m, nil
	}
	// Map the clicked row through the window that is actually on screen. Using
	// a freshly centred window here re-anchored the panel to the queue cursor
	// once it had been scrolled, so a click jumped the list and selected an
	// entry the pointer was not over.
	start, _ := m.queueWindow(rows)
	index := start + row
	if index >= len(m.state.Queue) {
		return m, nil
	}
	now := time.Now()
	gesture := m.lastClick.target == "queue" && m.lastClick.index == index && now.Sub(m.lastClick.at) <= doubleClickWindow
	m.lastClick = lastClick{target: "queue", index: index, at: now}
	m.queueFocus = true
	m.queueCursor = index
	m.queueOffset, m.queueOffsetSet = start, true
	if gesture && index != m.state.QueueIndex && !m.busy {
		// Consume the gesture so a third rapid click does not jump twice.
		m.lastClick = lastClick{}
		m.queueIntent, m.queueTarget = "jump", index
		return m.startMutation(func(next *Model) tea.Cmd { return next.queueCommand("jump") })
	}
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	l := m.layout()
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		m.logEvent("mouse", map[string]any{
			"event": "wheel", "x": msg.X, "y": msg.Y,
			"button": mouseButtonName(msg.Button), "up": msg.Button == tea.MouseWheelUp,
			"target": m.mouseTarget(msg.X-l.gutter, msg.Y, l),
		})
		return m.handleWheel(msg.X-l.gutter, msg.Y, msg.Button, l)
	case tea.MouseClickMsg:
		m.logEvent("mouse", map[string]any{
			"event": "click", "x": msg.X, "y": msg.Y,
			"button": mouseButtonName(msg.Button), "target": m.mouseTarget(msg.X-l.gutter, msg.Y, l),
		})
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.handleClick(msg.X-l.gutter, msg.Y, l)
	default:
		// Releases and motion carry no list action of their own.
		return m, nil
	}
}

// mouseTarget names what a pointer event lands on, for the session log. It
// mirrors handleClick's geometry so a logged mouse action can be replayed.
func (m Model) mouseTarget(x, y int, l layout) string {
	if m.overlay != "" {
		return "overlay." + m.overlay
	}
	if y == 1 {
		return "source"
	}
	if y == 2 {
		return "surface"
	}
	if len(m.history) > 0 && y == l.listTop {
		return "back"
	}
	if l.showRail && x >= l.mainWidth+1 && y >= l.listTop && y < l.listTop+l.listHeight {
		return "queue"
	}
	if y >= l.listTop && y < l.listTop+l.listHeight {
		if m.queueFocus && !l.showRail {
			return "queue"
		}
		return "list"
	}
	return "none"
}

func mouseButtonName(button tea.MouseButton) string {
	switch button {
	case tea.MouseLeft:
		return "left"
	case tea.MouseMiddle:
		return "middle"
	case tea.MouseRight:
		return "right"
	case tea.MouseWheelUp:
		return "wheelUp"
	case tea.MouseWheelDown:
		return "wheelDown"
	default:
		return "other"
	}
}

// handleWheel scrolls an overlay, the Up Next panel, or the main list view.
func (m Model) handleWheel(x, y int, button tea.MouseButton, l layout) (tea.Model, tea.Cmd) {
	if m.overlay != "" {
		if (m.overlay == "help" || m.overlay == "info") && button == tea.MouseWheelUp {
			m = m.helpScrollEntries(-3)
		} else if (m.overlay == "help" || m.overlay == "info") && button == tea.MouseWheelDown {
			m = m.helpScrollEntries(3)
		}
		return m, nil
	}
	delta := 3
	if button == tea.MouseWheelUp {
		delta = -3
	}
	dockQueue := l.showRail && x >= l.mainWidth+1 && y >= l.listTop && y < l.listTop+l.listHeight
	if dockQueue {
		return m.scrollQueue(delta, panelBodyRows(l.listHeight)), nil
	}
	if y < l.listTop || y >= l.listTop+l.listHeight {
		m.lastClick = lastClick{}
		return m, nil
	}
	if m.queueFocus && !l.showRail {
		return m.scrollQueue(delta, panelBodyRows(l.listHeight)), nil
	}
	m.queueFocus = false
	return m.scrollMainList(delta), nil
}

// handleClick moves the cursor, activates a row, switches tabs, or closes an
// overlay. The viewport stays fixed so a second click at the same cell targets
// the same row.
func (m Model) handleClick(x, y int, l layout) (tea.Model, tea.Cmd) {
	if m.overlay != "" {
		// Clicks outside the box dismiss it; clicks inside must not throw away
		// state. The input overlay in particular would otherwise lose whatever
		// the user had typed when they clicked the field.
		bw, bh := m.overlayBoxSize()
		bx, by := max(0, (l.width-bw)/2), max(0, (l.height-bh)/2)
		if x >= bx && x < bx+bw && y >= by && y < by+bh {
			m.lastClick = lastClick{}
			return m.handleOverlayClick(x-bx, y-by)
		}
		m.lastClick = lastClick{}
		return m.cancelOverlay(), nil
	}
	if m.input.Focused() && y != consoleHeaderRows+1 {
		m.input.Blur()
		m.inputMode = ""
	}
	if y == 1 {
		// Source switching is explicit and atomic, so the source breadcrumb opens
		// the switcher instead of switching on a stray click. Any click that does
		// not continue a row gesture ends the pending double-click.
		// -1 (not found) leaves the switcher unhighlighted instead of lying that
		// the first source is current.
		m.overlay = "source-switcher"
		if index := indexOf(m.sourceChoices(), m.source); index >= 0 {
			m.overlaySelected = index
		}
		m.lastClick = lastClick{}
		return m, nil
	}
	if len(m.history) > 0 && y == l.listTop {
		// The panel title bar doubles as a back button on pushed pages.
		m.lastClick = lastClick{}
		return m.goBack()
	}
	if l.showRail && x >= l.mainWidth+1 && y >= l.listTop && y < l.listTop+l.listHeight {
		return m.selectQueueRow(y-l.listTop-1, l.listHeight-2)
	}
	if y < l.listTop || y >= l.listTop+l.listHeight {
		return m, nil
	}
	if m.queueFocus && !l.showRail {
		// The focused narrow queue occupies the main area without context rows.
		return m.selectQueueRow(y-l.listTop-1, l.listHeight-2)
	}
	row := y - l.listTop - 1 - m.mainPrefixRows()
	if row < 0 {
		return m, nil
	}
	items := m.visibleItems()
	if len(items) == 0 {
		return m, nil
	}
	start, end := m.mainListWindow(panelBodyRows(l.listHeight) - m.mainPrefixRows())
	if row >= end-start {
		return m, nil
	}
	index := start + row
	if index >= len(items) {
		return m, nil
	}
	if !selectable(items[index]) {
		return m, nil
	}
	// Standard mouse semantics: the first click selects; a second consecutive
	// click on the same row within doubleClickWindow is one double-click and
	// activates it like Enter. A click on a different row, or a second click
	// after the window elapsed, is a fresh selection. Keyboard activation never
	// goes through this gesture state.
	now := time.Now()
	gesture := m.lastClick.target == "list" && m.lastClick.index == index && now.Sub(m.lastClick.at) <= doubleClickWindow
	m.lastClick = lastClick{target: "list", index: index, at: now}
	m.queueFocus = false
	m.selected, m.listOffset = index, start
	if gesture {
		// Consume the gesture: a third rapid click starts a new gesture.
		m.lastClick = lastClick{}
		return m.activate()
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The page-load hold belongs to the keystroke that asked for it, so every
	// keystroke starts without it.
	m.holdBrowsePage = false
	// A queued clear confirmation belongs to `c` alone; any other key drops it.
	if msg.String() != "c" {
		m.queueClearArmedUntil = time.Time{}
	}
	// Do not let an invisible, latent UI react while View can only render the
	// resize notice. WindowSizeMsg is handled by Update before reaching here.
	if m.tinyTerminal() {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			// The resize notice still answers "what is this app?"
			m.overlay, m.helpOffset = "help", 0
			return m, nil
		default:
			return m, nil
		}
	}
	m.logEvent("key", map[string]any{
		"key":          msg.String(),
		"inputFocused": m.input.Focused(),
		"overlay":      m.overlay,
		"queueFocus":   m.queueFocus,
		"detailKind":   m.detailKind,
	})
	// q exits from every non-text state, including modal overlays. In text
	// editors it is a letter, not a destructive shortcut.
	if msg.String() == "q" && m.overlay != "input" && m.overlay != "palette" && m.overlay != "discovery-text" {
		return m, tea.Quit
	}
	if m.overlay == "theme" {
		return m.handleThemeKey(msg)
	}
	if m.overlay == "source-switcher" {
		return m.handleSourceSwitcherKey(msg)
	}
	if m.overlay == "palette" {
		return m.handlePaletteKey(msg)
	}
	if m.overlay == "input" {
		return m.handleTextInputKey(msg)
	}
	if m.overlay == "auth" {
		return m.handleAuthOverlayKey(msg)
	}
	if m.overlay == "discovery" || m.overlay == "discovery-text" || m.overlay == "discovery-options" {
		return m.handleDiscoveryKey(msg)
	}
	if m.overlay == "help" || m.overlay == "info" {
		return m.handleHelpKey(msg)
	}
	if m.overlay != "" {
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
		m.overlay = ""
		return m, nil
	}
	if msg.String() == "u" && m.queueUndo != nil {
		if !time.Now().Before(m.queueUndo.expiresAt) {
			m.queueUndo = nil
			return m.withToast("Undo expired", true)
		}
		if m.busy || m.persisting {
			return m.withToast("Playback action already in progress", true)
		}
		var ok bool
		m, _, ok = m.acquireMutation()
		if !ok {
			return m.withToast("Another playback or source action is still running", true)
		}
		undoCmd := m.queueUndoCommand()
		m.queueUndo = nil
		return m, undoCmd
	}
	if msg.String() == "1" {
		return m.focusHome()
	}
	if msg.String() == "2" {
		return m.focusQueue(), nil
	}
	if m.queueFocus && (msg.String() == "esc" || msg.String() == "h") {
		m.queueFocus = false
		return m, nil
	}
	if m.queueFocus {
		last := len(m.state.Queue) - 1
		if last < 0 {
			m.queueFocus, m.queueCursor = false, 0
			return m, nil
		}
		owned := true
		switch msg.String() {
		case "up", "k":
			m.queueCursor = clamp(m.queueCursor-1, 0, last)
		case "down", "j":
			m.queueCursor = clamp(m.queueCursor+1, 0, last)
		case "g", "home":
			m.queueCursor = 0
		case "G", "end":
			m.queueCursor = last
		case "ctrl+d":
			m.queueCursor = clamp(m.queueCursor+5, 0, last)
		case "ctrl+u":
			m.queueCursor = clamp(m.queueCursor-5, 0, last)
		case "ctrl+f":
			m.queueCursor = clamp(m.queueCursor+10, 0, last)
		case "ctrl+b":
			m.queueCursor = clamp(m.queueCursor-10, 0, last)
		case "enter", "p":
			if m.queueCursor != m.state.QueueIndex && !m.busy {
				m.queueIntent, m.queueTarget = "jump", m.queueCursor
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueCommand("jump") })
			}
		case "x":
			if !m.busy {
				m.queueIntent, m.queueTarget = "remove", m.queueCursor
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueCommand("remove") })
			}
		case "J":
			if m.queueCursor < last && !m.busy {
				m.queueIntent, m.queueTarget = "movedown", m.queueCursor
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueCommand("movedown") })
			}
		case "K":
			if m.queueCursor > 0 && !m.busy {
				m.queueIntent, m.queueTarget = "moveup", m.queueCursor
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueCommand("moveup") })
			}
		case "c":
			if !m.busy {
				// Clearing drops every queued track and nothing can put them
				// back: the undo receipt covers one removal (`x`), not a whole
				// queue. So the destructive key takes a second press, like the
				// Account disconnect (usability batch 2026-09-16 M4).
				if m.queueClearArmedUntil.IsZero() || time.Now().After(m.queueClearArmedUntil) {
					m.queueClearArmedUntil = time.Now().Add(clearConfirmWindow)
					return m.withToast("Clear the whole queue? Press c again to confirm", true)
				}
				m.queueClearArmedUntil = time.Time{}
				m.queueIntent = "clear"
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueClear() })
			}
		case "f":
			return m.toggleFavorite()
		case "F", "e", "E":
			// These target the main list; the focused panel owns the cursor.
		default:
			owned = false
		}
		if owned {
			return m, nil
		}
		// Other keys fall through to the global bindings (q, tab, space, ...).
	}
	if m.input.Focused() {
		return m.handleTextInputKey(msg)
	}
	if m.busy {
		switch msg.String() {
		case "enter", "p", "space", "c", "n", "b", "v", "S", "R", "e", "E", "f":
			return m.withToast("Playback action already in progress", true)
		}
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "]":
		if len(m.history) > 0 {
			return m.jumpResultGroup(1), nil
		}
		if m.queueFocus {
			return m.focusHome()
		}
		return m.focusQueue(), nil
	case "[":
		if len(m.history) > 0 {
			return m.jumpResultGroup(-1), nil
		}
		if m.queueFocus {
			return m.focusHome()
		}
		return m.focusQueue(), nil
	case "up", "k":
		return m.moveBy(-1), nil
	case "down", "j":
		return m.moveBy(1), nil
	case "g", "home":
		m.selected = firstSelectableIndex(m.visibleItems())
		return m.keepMainSelectionVisible(), nil
	case "G", "end":
		m.selected = lastSelectableIndex(m.visibleItems())
		// `G` asks for the end of what is already loaded. Without this hold the
		// paging rule below turned every press into another page request: the
		// count grew forever and the cursor never left the first row (usability
		// batch 2026-09-16 M2). Stepping past the last row still pages.
		m.holdBrowsePage = true
		return m.keepMainSelectionVisible(), nil
	case "ctrl+d":
		return m.moveBy(5), nil
	case "ctrl+u":
		return m.moveBy(-5), nil
	case "ctrl+f":
		return m.moveBy(10), nil
	case "ctrl+b":
		return m.moveBy(-10), nil
	case "enter":
		return m.activate()
	case "p":
		if m.state.Track != nil {
			if item, ok := m.selectedItem(); ok && samePlayingTrack(*m.state.Track, item) {
				if m.state.Status == "playing" || m.state.Status == "buffering" {
					return m.startMutation(func(next *Model) tea.Cmd { return next.control("pause") })
				}
				if m.state.Status == "paused" {
					return m.startMutation(func(next *Model) tea.Cmd { return next.control("resume") })
				}
			}
		}
		// Navigation rows (Recent, All Favorites, Account, …) carry no playable
		// identity. Sending one made the server reject it and the footer print
		// its internal reference, e.g. `reference "apple-music:entry-account:"
		// has an empty id` (usability batch 2026-09-16 L1).
		if item, ok := m.selectedItem(); ok && !playable(item) {
			return m.withToast("Only songs, albums, playlists and stations can play", true)
		}
		if m.detailKind == "playlist" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playPlaylist() })
		}
		if m.detailKind == "album" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playAlbum() })
		}
		return m.startMutation(func(next *Model) tea.Cmd { return next.playSelected() })
	case "space", "c":
		if m.state.Status == "playing" || m.state.Status == "buffering" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.control("pause") })
		}
		if m.state.Status == "paused" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.control("resume") })
		}
		if item, ok := m.selectedItem(); ok {
			switch item.Kind {
			case "stream", "station", "song":
				return m.startMutation(func(next *Model) tea.Cmd { return next.playSelected() })
			}
		}
		return m, nil
	case "n":
		if m.state.IsLive {
			return m, nil
		}
		return m.startMutation(func(next *Model) tea.Cmd { return next.control("next") })
	case "b":
		if m.state.IsLive {
			return m, nil
		}
		return m.startMutation(func(next *Model) tea.Cmd { return next.control("previous") })
	case "v":
		return m.startMutation(func(next *Model) tea.Cmd { return next.stopPlayback() })
	case "s":
		// -1 (not found) leaves the switcher unhighlighted instead of lying that
		// the first source is current.
		m.overlay = "source-switcher"
		if index := indexOf(m.sourceChoices(), m.source); index >= 0 {
			m.overlaySelected = index
		}
		return m, nil
	case ":":
		m.overlay = "palette"
		m.input.SetValue("")
		m.input.Prompt = ":"
		m.input.Placeholder = "command"
		m.input.Focus()
		m.overlaySelected = -1
		return m, nil
	case "S":
		if m.source == "radio" && m.view == "Browse" {
			return m.resortRadioBrowse()
		}
		// Gate shuffle by the viewed source's declared capability so it never
		// reaches a stale server active source or an unsupported provider.
		if !m.declares(m.source, api.CapShuffle) {
			return m.withToast("This source does not support shuffle", true)
		}
		if m.state.IsLive {
			return m.withToast("Shuffle applies to finite queues only", true)
		}
		// One meaning everywhere: S toggles shuffle. It used to restart an open
		// playlist or album shuffled instead, so pressing it again could never
		// turn shuffle off. Shuffle-play is
		// now S followed by Enter or p.
		return m.startMutation(func(next *Model) tea.Cmd { return next.toggleShuffle() })
	case "r":
		return m.reloadView()
	case "R":
		if !m.declares(m.source, api.CapRepeat) {
			return m.withToast("This source does not support repeat", true)
		}
		if m.state.IsLive {
			return m.withToast("Repeat applies to finite queues only", true)
		}
		return m.startMutation(func(next *Model) tea.Cmd { return next.cycleRepeat() })
	case "e", "E":
		if !m.declares(m.source, api.CapQueue) {
			return m.withToast("This source does not support a finite queue", true)
		}
		item, ok := m.selectedItem()
		if !ok || !selectable(item) {
			return m, nil
		}
		if item.Kind == "stream" {
			return m.withToast("Live radio streams cannot be queued", true)
		}
		if msg.String() == "e" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.enqueueSelected("next") })
		}
		return m.startMutation(func(next *Model) tea.Cmd { return next.enqueueSelected("append") })
	case "f":
		return m.toggleFavorite()
	case "a":
		if m.source == "radio" {
			return m.openTextInput("url", "Stream URL: ", "https://stream.example/live", "")
		}
		return m, nil
	case "F":
		if m.source == "radio" {
			// Radio reserves the f-family for favorite/unfavorite only.
			return m, nil
		}
		m.queueFocus = false
		return m.openTextInput("filter", "Filter: ", "substring to match", m.filter)
	case "/":
		if m.source == "radio" {
			m.queueFocus = false
			m.overlay = "discovery"
			m.discoverySelected, m.discoveryQuery = 0, ""
			if m.view == "Browse" && m.browseQuery != (radioDiscovery{}) {
				m.discoveryPending, m.discoveryTerm = m.browseQuery, m.browseQuery.Term
			} else {
				m.discoveryPending, m.discoveryTerm = radioDiscovery{}, ""
			}
			return m, nil
		}
		m.queueFocus = false
		return m.openTextInput("search", "Search: ", "type a query and press Enter", "")
	case "i":
		m.overlay, m.helpOffset = "info", 0
		return m, nil
	case "?":
		m.overlay, m.helpOffset = "help", 0
		return m, nil
	case "t":
		m.themeNames = theme.Names()
		if index := indexOf(m.themeNames, m.themeName); index >= 0 {
			m.themeIndex = index
		}
		m.overlay = "theme"
		return m, nil
	case "esc", "backspace", "h":
		if m.filter != "" {
			m.filter = ""
			m.selected, m.listOffset = 0, 0
			return m, nil
		}
		if len(m.history) > 0 {
			return m.goBack()
		}
		if m.viewKey() == "radio/Browse" && m.browseQuery != (radioDiscovery{}) {
			m.invalidateBrowseCache()
			m.browseQuery = radioDiscovery{}
			m.view, m.title = "Browse", "Popular Worldwide"
			m.resetBrowsePaging()
			m.lastView["radio"] = "Browse"
			m.items, m.selected, m.loading, m.listErr = nil, 0, true, ""
			m.generation++
			return m, m.loadView()
		}
		return m, nil
	}
	return m, nil
}

// openTextInput presents short, intentional text tasks in the same central
// location as Radio filters instead of hiding focus in the top navigation.
func (m Model) openTextInput(mode, prompt, placeholder, value string) (tea.Model, tea.Cmd) {
	m.overlay, m.inputMode = "input", mode
	m.input.Prompt, m.input.Placeholder = prompt, placeholder
	m.input.SetValue(value)
	m.input.Focus()
	return m, textinput.Blink
}

func (m Model) closeTextInput() Model {
	m.input.Blur()
	m.inputMode = ""
	if m.overlay == "input" {
		m.overlay = ""
	}
	m.jamendoValidating, m.jamendoSetupErr = false, ""
	return m
}

// openJamendoSetup opens the setup modal for an unconfigured Jamendo source.
// It reuses the central input overlay; the explanatory rows render in
// overlayDialog and the submit path validates in-process (Keychain), exactly
// like `lilt jamendo setup`.
func (m Model) openJamendoSetup() (tea.Model, tea.Cmd) {
	next, cmd := m.openTextInput("jamendo-setup", "Client ID: ", "paste the app client_id", "")
	model := next.(Model)
	model.jamendoValidating, model.jamendoSetupErr = false, ""
	return model, cmd
}

// handleAuthOverlayKey drives the Account overlay. Like every overlay it owns
// the keyboard; Esc cancels a pending sign-in flow first and closes only when
// nothing is in flight (docs/ui/model.md §10).
func (m Model) handleAuthOverlayKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		if m.authFlow != nil && m.authFlow.Status == api.FlowPending {
			return m.cancelAuthFlow()
		}
		m.overlay = ""
		m.authConfirm = ""
		return m, nil
	case "up", "k":
		return m.moveAuthSelection(-1), nil
	case "down", "j", "tab":
		return m.moveAuthSelection(1), nil
	case "enter":
		return m.activateAuthRow()
	case "d":
		return m.toggleAuthDisconnect()
	case "ctrl+o":
		return m.openAuthFlowURL()
	}
	return m, nil
}

func (m Model) handleTextInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeTextInput(), nil
	case "enter":
		return m.submitInput()
	case "ctrl+o":
		// Only the Jamendo setup modal binds a launcher key, and as a control
		// key it never competes with typing a client_id.
		if m.inputMode == "jamendo-setup" && m.openURL != nil && !m.jamendoValidating {
			open := m.openURL
			m.logEvent("jamendo", map[string]any{"event": "open-devportal"})
			return m, func() tea.Msg { open(jamendo.DeveloperPortalURL); return nil }
		}
		return m, nil
	case "tab", "shift+tab":
		// Source switching is an explicit action (`s`), not a tab cycle.
		return m, nil
	}
	// `[` and `]` are ordinary characters here. Reading them as panel or group
	// navigation made every query, filter and URL containing a bracket
	// impossible to type, and closed the input with the typed text silently
	// dropped (usability batch 2026-09-16 H1). Panel focus and result-group
	// jumps keep their global bindings; a text editor owns its own keys.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	mode := m.inputMode
	value := strings.TrimSpace(m.input.Value())
	m.logEvent("submit", map[string]any{"mode": mode, "valueLength": len(value), "valueKind": map[bool]string{true: "url", false: "text"}[mode == "url"]})
	if mode == "jamendo-setup" {
		// The modal stays open while the request runs; a failure keeps the
		// typed value for a retry.
		return m.submitJamendoSetup(value)
	}
	if value == "" && mode != "filter" {
		// Submitting nothing closed the input and reported nothing, so an empty
		// Enter looked like a dead key (usability batch 2026-09-16 L3). The
		// filter keeps its own meaning: an empty value clears the filter.
		return m.withToast("Type something, or Esc to cancel", true)
	}
	m = m.closeTextInput()
	switch mode {
	case "search":
		if value == "" {
			return m, nil
		}
		m.input.SetValue("")
		if m.source == "radio" {
			return m.applyDiscoveryFilter(radioDiscovery{}, value)
		}
		next, cmd := m.pushAggregate("Search: "+presentation.Text(value), m.searchSource(value))
		return next, cmd
	case "filter":
		m.filter = value
		m.selected, m.listOffset = 0, 0
		return m, nil
	case "url":
		if value == "" {
			return m, nil
		}
		item := core.Item{Kind: "stream", URL: value, Title: value}
		added := !m.activity.IsFavorite("radio", stableItemID("radio", item))
		m.logEvent("play", map[string]any{"itemKind": "stream", "titleLength": len(value)})
		var ok bool
		m, _, ok = m.acquireMutation()
		if !ok {
			return m.withToast("Another playback or source action is still running", true)
		}
		playCmd := beginAction(m.operationID, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			resolved := item
			if name := m.radio.StreamName(ctx, value); name != "" {
				resolved.Title = name
			}
			note := "Playing: " + resolved.Title
			if added {
				note = "Added to Favorites and playing: " + resolved.Title
			}
			playback, err := m.player.RadioPlay(ctx, resolved.URL, resolved.Title)
			return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: "radio", recentItem: &resolved, addFavorite: added, refreshView: added && m.source == "radio" && m.view == "Favorites"}
		})
		return m, playCmd
	}
	return m, nil
}

// submitJamendoSetup validates and saves the pasted client_id through the
// in-process setup hook. The modal remains open until the typed message lands.
func (m Model) submitJamendoSetup(clientID string) (tea.Model, tea.Cmd) {
	if clientID == "" {
		// An empty submit must not look like a dead key: the modal stays open
		// with the validation error (batch 2026-09-28-rounds F7).
		m.jamendoSetupErr = "Client ID is required"
		return m, nil
	}
	if m.jamendoValidating || m.jamendoSetup == nil {
		return m, nil
	}
	m.jamendoValidating = true
	m.jamendoSetupErr = ""
	setup := m.jamendoSetup
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
		defer cancel()
		return jamendoSetupMsg{clientID: clientID, err: setup(ctx, clientID)}
	}
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "?":
		m.overlay, m.helpOffset = "", 0
		return m, nil
	}
	if maxOffset := m.helpScrollMax(); maxOffset > 0 {
		switch msg.String() {
		case "up", "k":
			return m.helpScrollEntries(-1), nil
		case "down", "j":
			return m.helpScrollEntries(1), nil
		case "pgup":
			return m.helpScrollPage(-1), nil
		case "pgdown":
			return m.helpScrollPage(1), nil
		case "g", "home":
			m.helpOffset = 0
			return m, nil
		case "G", "end":
			m.helpOffset = maxOffset
			return m, nil
		}
	}
	// Any other key is inert. Dismissing on it would swallow the key that the
	// reader pressed to act, so the action only runs on the second press
	// (batch 2026-09-20-album-recheck N3).
	return m, nil
}

func (m Model) handleThemeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// preview moves the picker and applies the candidate theme to both the
	// renderer and the input in one step.
	preview := func(index int) Model {
		if len(m.themeNames) == 0 {
			return m
		}
		m.themeIndex = clamp(index, 0, len(m.themeNames)-1)
		m.themeName = m.themeNames[m.themeIndex]
		return m.setTheme(m.themeName)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "down", "j":
		return preview(m.themeIndex + 1), nil
	case "shift+tab", "up", "k":
		return preview(m.themeIndex - 1), nil
	case "enter":
		if m.busy || m.persisting {
			return m.withToast("A state change is still being saved", true)
		}
		var ok bool
		m, _, ok = m.acquirePersist()
		if !ok {
			return m.withToast("Another playback or source action is still running", true)
		}
		m.logEvent("theme", map[string]any{"name": m.themeName})
		return m, m.persistThemeCmd(m.themeName, m.operationID)
	case "esc":
		m.overlay = ""
		m.themeName = m.store.Theme
		return m.setTheme(m.store.Theme), nil
	}
	return m, nil
}

func (m Model) handleSourceSwitcherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sources := m.sourceChoices()
	if len(sources) == 0 {
		return m.withToast("No source is currently available", true)
	}
	// A number picks that row directly, matching the 1-9 sub-view convention
	// (batch 2026-09-22-jamendo-tui: two rounds asked for this and none
	// of the switcher rounds used the arrow-only flow without friction).
	if index, err := strconv.Atoi(msg.String()); err == nil && index >= 1 && index <= len(sources) {
		return m.beginSourceSwitch(sources[index-1])
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.overlay = ""
		return m, nil
	case "up", "k":
		m.overlaySelected = (m.overlaySelected + len(sources) - 1) % len(sources)
	case "down", "j", "tab":
		m.overlaySelected = (m.overlaySelected + 1) % len(sources)
	case "enter":
		return m.beginSourceSwitch(sources[clamp(m.overlaySelected, 0, len(sources)-1)])
	}
	return m, nil
}

// paletteCommands lists the commands the current context can actually run.
// The palette is an advertisement: a command the executor would reject is a
// bug (batch 2026-09-22-jamendo-tui r4 executed ":browse" on Apple Music and
// got "Unknown command"). Same capability gating as the Help table and the
// footer: :discover/:browse follow the source's views; :queue is always
// available because it targets the fixed Up Next area and can show its empty
// state without promising an edit.
func (m Model) paletteCommands() []string {
	commands := []string{":home"}
	if indexOf(m.views(), "Discover") >= 0 {
		commands = append(commands, ":discover")
	}
	if m.source == "radio" && m.declares(m.source, api.CapSearchRadio) {
		commands = append(commands, ":browse")
	}
	commands = append(commands, ":recent")
	commands = append(commands, ":queue")
	return append(commands, ":auth", ":source apple-music", ":source audius", ":source jamendo", ":source radio", ":play <ref>", ":help")
}

// paletteMatches returns the commands matching the current input. Empty input
// shows every command.
func (m Model) paletteMatches() []string {
	needle := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if needle == "" {
		return m.paletteCommands()
	}
	matches := make([]string, 0, len(m.paletteCommands()))
	for _, command := range m.paletteCommands() {
		if strings.Contains(strings.ToLower(command[1:]), needle) {
			matches = append(matches, command)
		}
	}
	return matches
}

func (m Model) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.overlay = ""
		m.input.Blur()
		return m, nil
	case "tab", "down":
		// Tab moves the highlight through candidates; it never overwrites the
		// typed text. With nothing highlighted yet, Tab selects the first.
		matches := m.paletteMatches()
		if len(matches) > 0 {
			if m.overlaySelected < 0 {
				m.overlaySelected = 0
			} else {
				m.overlaySelected = (m.overlaySelected + 1) % len(matches)
			}
		}
		return m, nil
	case "shift+tab", "up":
		matches := m.paletteMatches()
		if len(matches) > 0 {
			if m.overlaySelected < 0 {
				m.overlaySelected = len(matches) - 1
			} else {
				m.overlaySelected = (m.overlaySelected + len(matches) - 1) % len(matches)
			}
		}
		return m, nil
	case "enter":
		command := m.paletteCommandToRun()
		m.input.Blur()
		m.overlay = ""
		if command == "" {
			return m, nil
		}
		return m.runPaletteCommand(command)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Typing highlights the first match; an empty input highlights nothing.
	if len(m.paletteMatches()) > 0 && strings.TrimSpace(m.input.Value()) != "" {
		m.overlaySelected = 0
	} else {
		m.overlaySelected = -1
	}
	return m, cmd
}

// paletteCommandToRun resolves what Enter should execute: the highlighted
// candidate, always. Typing filters, Enter confirms — the standard palette
// contract (OQ33: the old prefix rule executed the raw text whenever the
// filter's substring hit wasn't a prefix, so ":pl" errored instead of running
// the highlighted candidate). Nothing highlighted and nothing typed is a
// no-op; free-form entries like `play am:123` are themselves palette
// commands, so they still run verbatim.
func (m Model) paletteCommandToRun() string {
	matches := m.paletteMatches()
	if m.overlaySelected < 0 || len(matches) == 0 {
		return strings.TrimSpace(m.input.Value())
	}
	return strings.TrimPrefix(matches[clamp(m.overlaySelected, 0, len(matches)-1)], ":")
}

func (m Model) runPaletteCommand(command string) (tea.Model, tea.Cmd) {
	switch {
	case command == "home":
		return m.focusHome()
	case command == "recent":
		// The palette opens the same page as the Home entry, so it must use the
		// same path: `push` alone left the page parented to Home and the
		// resolved list was dropped (usability batch 2026-09-16 M3).
		return m.pushRecent()
	case command == "discover":
		if index := indexOf(m.views(), "Discover"); index >= 0 {
			return m.selectView(index)
		}
		return m.withToast("This source has no trending discovery", true)
	case command == "browse" && m.source == "radio":
		return m.selectView(indexOf(m.views(), "Browse"))
	case command == "queue":
		return m.focusQueue(), nil
	case command == "auth":
		return m.openAuthOverlay()
	case command == "help":
		m.overlay, m.helpOffset = "help", 0
		return m, nil
	case command == "source":
		return m.withToast("source requires a source id ("+strings.Join(m.sourceChoices(), ", ")+")", true)
	case strings.HasPrefix(command, "source "):
		id := strings.TrimSpace(strings.TrimPrefix(command, "source "))
		if _, ok := m.descriptor(id); !ok {
			return m.withToast("Unknown source: "+presentation.Text(id), true)
		}
		return m.beginSourceSwitch(id)
	case strings.HasPrefix(command, "play "):
		ref := strings.TrimSpace(strings.TrimPrefix(command, "play "))
		if ref == "" || ref == "<ref>" {
			return m.withToast("play requires a ref", true)
		}
		return m.startMutation(func(next *Model) tea.Cmd {
			return next.playItem(core.Item{Kind: "song", Ref: ref, ID: ref})
		})
	default:
		return m.withToast("Unknown command: :"+command, true)
	}
}
