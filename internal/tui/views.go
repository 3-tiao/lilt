package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
)

// overlayView renders the overlay as a full-screen centered frame. content()
// composites overlayDialog over the live base frame instead, so overlays keep
// the shell visible behind them.
func (m Model) overlayView(width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.overlayDialog(width, height))
}

// dialogWidth picks an overlay dialog width. When the remaining side margins
// would be narrower than 6 cells, the dialog spans the full width instead:
// a couple of base-frame cells peeking out on each side read as broken borders
// (batch 2026-09-23-postaudit M1).
func dialogWidth(natural, width int) int {
	w := min(natural, max(4, width-4))
	if width-w < 8 && w < width {
		return width
	}
	return w
}

func (m Model) helpOverlay(width, height int) helpOverlay {
	boxWidth := dialogWidth(74, width)
	inner := boxWidth - 2
	title := "Help"
	content := m.helpContent(inner)
	if m.overlay == "info" {
		title = "Track Info"
		content = helpContent{rows: m.infoLines(inner)}
	}
	// The dialog always carries a status row (range + close hint). Count it in
	// the box height: a body that exactly filled the box used to push the
	// status row past the border, so Track Info and a full Help page lost
	// their close hint entirely (batch 2026-09-23-polish p4).
	boxHeight := len(content.rows) + 3
	if boxHeight > height {
		boxHeight = height
	}
	return helpOverlay{title: title, rows: content.rows, starts: content.starts, boxWidth: boxWidth, boxHeight: boxHeight, visible: max(0, boxHeight-2)}
}

// helpWindow returns the visible row range for a scroll offset. The start snaps
// down to an entry start and the end snaps back to the next entry start, so a
// page never opens on an orphan continuation row and never splits an entry
// across pages. The final page shows the tail in full when it fits
// (batch 2026-09-23-postaudit-recheck N4).
func helpWindow(starts []int, offset, contentRows, total int) (int, int) {
	if total <= contentRows {
		return 0, total
	}
	start := 0
	for _, s := range starts {
		if s <= offset {
			start = s
		}
	}
	limit := min(start+contentRows, total)
	end := limit
	// End on the last entry start below the row budget: the page then holds
	// whole entries, and the next page begins on an entry start too.
	for _, s := range starts {
		if s > start && s < limit {
			end = s
		}
	}
	if offset >= total-contentRows {
		// The final page shows the tail in full: take the earliest start whose
		// remaining rows fit.
		for _, s := range starts {
			if total-s <= contentRows {
				start = s
				break
			}
		}
		end = total
	}
	return start, end
}

// helpEntryStarts returns the entry-start rows of the current help body.
func (m Model) helpEntryStarts() []int {
	if m.overlay == "info" {
		return nil
	}
	return m.helpContent(dialogWidth(74, m.width) - 2).starts
}

// helpScrollEntries moves the help offset by delta entries (delta > 0 scrolls
// down). Scrolling is entry-wise, so every page opens on a whole entry: a
// row-wise offset could land on a continuation row and split an entry across
// pages (batch 2026-09-23-postaudit-recheck N4).
func (m Model) helpScrollEntries(delta int) Model {
	maxOffset := m.helpScrollMax()
	if maxOffset <= 0 {
		return m
	}
	starts := m.helpEntryStarts()
	if len(starts) == 0 {
		return m
	}
	// The tail page is the last position; treat it as a scroll stop.
	positions := append([]int(nil), starts...)
	positions = append(positions, maxOffset)
	sort.Ints(positions)
	positions = slices.Compact(positions)
	current := 0
	for i, p := range positions {
		if p <= m.helpOffset {
			current = i
		}
	}
	next := clamp(current+delta, 0, len(positions)-1)
	m.helpOffset = clamp(positions[next], 0, maxOffset)
	return m
}

// helpScrollPage moves by one visible page: the entries that fit in the
// content rows.
func (m Model) helpScrollPage(delta int) Model {
	layout := m.helpOverlay(m.width, m.height)
	contentRows := max(1, layout.visible-1)
	starts := m.helpEntryStarts()
	if len(starts) == 0 || contentRows <= 0 {
		return m
	}
	start, end := helpWindow(starts, clamp(m.helpOffset, 0, max(1, m.helpScrollMax())), contentRows, len(layout.rows))
	// The first entry at or after the current page's end is the next page.
	target := end
	if delta < 0 {
		// The last entry that starts before the current page start.
		target = 0
		for _, s := range starts {
			if s < start {
				target = s
			}
		}
	}
	m.helpOffset = clamp(target, 0, m.helpScrollMax())
	return m
}

func (m Model) helpScrollMax() int {
	layout := m.helpOverlay(m.width, m.height)
	contentRows := max(1, layout.visible-1)
	if layout.visible <= 0 || len(layout.rows) <= layout.visible {
		return 0
	}
	return len(layout.rows) - contentRows
}

func (m Model) layout() layout {
	width := m.width
	if width <= 0 {
		width = 100
	}
	gutter := 0
	if width >= 60 {
		gutter = 1
		width -= gutter * 2
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	// The shell is fixed: symmetric canvas insets, identity + navigation,
	// band gaps, the full-width NOW PLAYING box, and the feedback/footer bands.
	// Async messages live in the feedback band, so playing content changes
	// what is shown, never the location or height of the browsing workspace.
	headerRows := consoleHeaderRows
	trailer := feedbackRows + footerRows + canvasInsetRows
	fixedRows := canvasInsetRows + headerRows + bandGapRows + nowBoxRows + trailer
	listHeight := height - fixedRows
	if listHeight < minWorkspaceRows {
		listHeight = minWorkspaceRows
	}
	listTop := canvasInsetRows + headerRows
	showRail := width >= 88
	mainWidth, panelWidth := width, 0
	if showRail {
		// Queue entries need more room than a web sidebar: terminal text cannot
		// shrink its font for long artist names, so reserve two fifths.
		panelWidth = clamp(width*2/5, 36, 48)
		mainWidth = width - panelWidth - 1
	}
	nowTop := listTop + listHeight + bandGapRows
	return layout{width: width, height: height, gutter: gutter, headerRows: headerRows, listTop: listTop, listHeight: listHeight, nowTop: nowTop, nowHeight: nowBoxRows, showRail: showRail, mainWidth: mainWidth, panelWidth: panelWidth}
}

// consoleFrame adds a quiet terminal-style outer gutter without changing the
// canvas dimensions Bubble Tea owns. Every rendered row remains full width.
func consoleFrame(value string, contentWidth, gutter int) string {
	if gutter == 0 {
		return value
	}
	margin := strings.Repeat(" ", gutter)
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		visible := lipgloss.Width(line)
		if visible < contentWidth {
			line += strings.Repeat(" ", contentWidth-visible)
		}
		lines[i] = margin + line + margin
	}
	return strings.Join(lines, "\n")
}

// clipFrame bounds a rendered frame to the terminal so oversized content can
// never wrap and visually interleave with other panels.
func clipFrame(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = clip(line, width)
	}
	return strings.Join(lines, "\n")
}

// View declares the terminal features lilt wants (alternate screen and mouse
// reporting) alongside the rendered content. Bubble Tea v2 moved these from
// program options to declarative view fields.
func (m Model) View() (out tea.View) {
	// A render-time panic is journaled with its stack before bubbletea takes
	// over (it prints the trace to stderr and tears the program down).
	defer func() {
		if r := recover(); r != nil {
			m.logEvent("tui.panic", map[string]any{
				"panic": fmt.Sprint(r),
				"stack": string(debug.Stack()),
				"scope": "view",
			})
			panic(r)
		}
	}()
	view := tea.NewView(m.content())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

// content renders the frames that View declares.
func (m Model) content() string {
	l := m.layout()
	// Hard floor: nothing, not even a dialog, is drawable below this.
	if l.width < 24 || l.height < 8 {
		return m.canvasFrame(consoleFrame(m.tinyView(l.width, l.height), l.width, l.gutter), l.width+2*l.gutter)
	}
	if m.overlay != "" {
		// Overlays keep the shell alive behind them: a centered modal box over the
		// live frame lets the theme picker preview against real content. Clip the
		// overlay to the terminal: lipgloss.Place centers but does not shrink
		// content, so an oversized dialog would wrap and interleave.
		base := m.baseFrame(l)
		box := m.overlayDialog(l.width, l.height)
		return m.canvasFrame(m.overlayFrame(base, box, l.width+2*l.gutter, l.height), l.width+2*l.gutter)
	}
	if minWidth, minHeight := m.consoleMinimum(); l.width < minWidth || l.height < minHeight {
		return m.canvasFrame(consoleFrame(m.tinyView(l.width, l.height), l.width, l.gutter), l.width+2*l.gutter)
	}
	width, height := l.width, l.height
	inset := strings.Repeat(" ", width)
	header := []string{inset, m.sourceLine(width), m.viewLine(width)}
	// The workspace holds the browsing list and, at sufficient width, the Up
	// Next rail. The rail is part of the workspace, never of the playback band.
	listHeight := l.listHeight
	bodyRows := panelBodyRows(listHeight)
	queueCount := m.queueCount()
	mainActive := !m.queueFocus
	var body string
	if m.queueFocus && !l.showRail {
		body = m.renderPanel(m.queueTitle(), queueCount, m.queueLines(width-4, bodyRows), width, listHeight, true)
	} else if l.showRail {
		mainBox := m.renderPanel(m.listTitle(), m.listCount(), m.listLines(l.mainWidth-4, bodyRows), l.mainWidth, listHeight, mainActive)
		railBox := m.renderPanel(m.queueTitle(), queueCount, m.queueLines(l.panelWidth-4, bodyRows), l.panelWidth, listHeight, m.queueFocus)
		body = joinColumns(mainBox, railBox)
	} else {
		body = m.renderPanel(m.listTitle(), m.listCount(), m.listLines(width-4, bodyRows), width, listHeight, mainActive)
	}
	nowBox := m.renderPanel("Now Playing", "", m.nowBody(width-4), width, l.nowHeight, false)
	feedback := fit("", width)
	if message, isErr := m.feedbackText(); message != "" {
		style := m.renderer.accentStyle
		if isErr {
			style = m.renderer.errorStyle
		}
		feedback = style.Render(fit(message, width))
	}
	lines := append([]string{}, header...)
	lines = append(lines, strings.Split(body, "\n")...)
	lines = append(lines, inset)
	lines = append(lines, strings.Split(nowBox, "\n")...)
	lines = append(lines, feedback, m.footerLine(width), inset)
	// Keep Bubble Tea from scrolling when terminal dimensions are tiny or a
	// focused input makes the header taller than the viewport.
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", max(0, width)))
	}
	return m.canvasFrame(consoleFrame(strings.Join(lines, "\n"), width, l.gutter), width+2*l.gutter)
}

func (m Model) feedbackText() (string, bool) {
	if m.message != "" {
		return m.message, m.messageErr
	}
	return m.persistentWarning, m.persistentWarning != ""
}

// baseFrame renders the full console without overlays; overlayFrame draws the
// modal on top of it.
func (m Model) baseFrame(l layout) string {
	width, height := l.width, l.height
	inset := strings.Repeat(" ", width)
	header := []string{inset, m.sourceLine(width), m.viewLine(width)}
	listHeight := l.listHeight
	bodyRows := panelBodyRows(listHeight)
	queueCount := m.queueCount()
	mainActive := !m.queueFocus
	var body string
	if m.queueFocus && !l.showRail {
		body = m.renderPanel(m.queueTitle(), queueCount, m.queueLines(width-4, bodyRows), width, listHeight, true)
	} else if l.showRail {
		mainBox := m.renderPanel(m.listTitle(), m.listCount(), m.listLines(l.mainWidth-4, bodyRows), l.mainWidth, listHeight, mainActive)
		railBox := m.renderPanel(m.queueTitle(), queueCount, m.queueLines(l.panelWidth-4, bodyRows), l.panelWidth, listHeight, m.queueFocus)
		body = joinColumns(mainBox, railBox)
	} else {
		body = m.renderPanel(m.listTitle(), m.listCount(), m.listLines(width-4, bodyRows), width, listHeight, mainActive)
	}
	nowBox := m.renderPanel("Now Playing", "", m.nowBody(width-4), width, l.nowHeight, false)
	feedback := fit("", width)
	if message, isErr := m.feedbackText(); message != "" {
		style := m.renderer.accentStyle
		if isErr {
			style = m.renderer.errorStyle
		}
		feedback = style.Render(fit(message, width))
	}
	lines := append([]string{}, header...)
	lines = append(lines, strings.Split(body, "\n")...)
	lines = append(lines, inset)
	lines = append(lines, strings.Split(nowBox, "\n")...)
	lines = append(lines, feedback, m.footerLine(width), inset)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", max(0, width)))
	}
	return consoleFrame(strings.Join(lines, "\n"), width, l.gutter)
}

// overlayFrame centers a modal box over the base frame, keeping the shell
// visible around and behind the box so overlays stay in context.
func (m Model) overlayFrame(base, box string, width, height int) string {
	boxLines := strings.Split(box, "\n")
	boxWidth, boxHeight := 0, len(boxLines)
	for _, line := range boxLines {
		if w := lipgloss.Width(line); w > boxWidth {
			boxWidth = w
		}
	}
	top := max(0, (height-boxHeight)/2)
	left := max(0, (width-boxWidth)/2)
	lines := strings.Split(base, "\n")
	for dy, boxLine := range boxLines {
		y := top + dy
		if y >= len(lines) {
			break
		}
		// Left and right of the box: keep the base row; inside: replace with the
		// box row. Rows are ANSI-styled, so cells are moved with their escapes.
		line := lines[y]
		prefix, suffix := overlaySplit(line, left), overlaySplitTail(line, left, boxWidth, width)
		lines[y] = prefix + boxLine + suffix
	}
	return strings.Join(lines, "\n")
}

// overlaySplit keeps the first `left` cells of a styled row.
func overlaySplit(line string, cells int) string {
	if cells <= 0 {
		return ""
	}
	truncated := ansi.Truncate(line, cells, "")
	if w := lipgloss.Width(truncated); w < cells {
		truncated += strings.Repeat(" ", cells-w)
	}
	return truncated
}

// overlaySplitTail keeps the cells after the box region of a styled row.
func overlaySplitTail(line string, left, boxWidth, width int) string {
	if left+boxWidth >= width {
		return ""
	}
	return ansi.Cut(line, left+boxWidth, width)
}

// canvasFrame fills every cell of a rendered frame with the theme background.
// Lipgloss closes each styled span with a reset, which would clear the canvas
// colour for the rest of the line, so the canvas escape is re-asserted after
// every reset. Palettes without a bg are returned untouched.
func (m Model) canvasFrame(value string, width int) string {
	if m.renderer.canvasEscape == "" {
		return value
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if visible := lipgloss.Width(line); visible < width {
			line += strings.Repeat(" ", width-visible)
		}
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+m.renderer.canvasEscape)
		line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+m.renderer.canvasEscape)
		lines[i] = m.renderer.canvasEscape + line + "\x1b[m"
	}
	return strings.Join(lines, "\n")
}

// joinColumns places two fixed-height boxes side by side with one spacer
// column. Both boxes always render exactly height rows.
func joinColumns(left, right string) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	joined := make([]string, max(len(leftLines), len(rightLines)))
	for i := range joined {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		joined[i] = l + " " + r
	}
	return strings.Join(joined, "\n")
}

func (m Model) tinyView(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	minWidth, minHeight := m.consoleMinimum()
	text := "Terminal too small"
	if width >= 20 && height > 1 {
		text = "Terminal too small — resize"
	}
	lines := []string{fit(text, width)}
	// The notice must be actionable: without the numbers a user does not know
	// how far to resize, and without the quit key they are stuck (r13 r3).
	if width >= 30 && height > 2 {
		// Two rows so the current size never gets truncated away: at 30 cols
		// the joined one-liner clipped "now 30×8" down to "now 30…" (batch
		// 2026-09-23-polish p2).
		lines = append(lines, fit(fmt.Sprintf("needs at least %d×%d", minWidth, minHeight), width))
		lines = append(lines, fit(fmt.Sprintf("now %d×%d", width, height), width))
	} else if width >= 30 {
		lines = append(lines, fit(fmt.Sprintf("min %d×%d · now %d×%d", minWidth, minHeight, width, height), width))
	}
	if height > 2 && width >= 20 {
		lines = append(lines, fit("q quit", width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// consoleMinimum reports the smallest canvas the console layout can draw
// honestly: canvas insets, identity + navigation, band gaps, the workspace
// list, the full-width NOW PLAYING box, and the feedback/footer bands. Below
// it every panel would be clipped mid-border, which reads as a broken frame,
// so the resize notice is the correct answer.
func (m Model) consoleMinimum() (int, int) {
	rows := canvasInsetRows + consoleHeaderRows + minWorkspaceRows + bandGapRows +
		nowBoxRows + feedbackRows + footerRows + canvasInsetRows
	return consoleMinWidth, rows
}

func (m Model) tinyTerminal() bool {
	if m.overlay != "" {
		// Overlays size themselves and stay scrollable, so help and filters keep
		// working on terminals that are too short for the console page.
		return (m.width > 0 && m.width < 24) || (m.height > 0 && m.height < 8)
	}
	width, height := m.consoleMinimum()
	usable := m.width
	if usable >= 60 {
		usable -= 2 // the visual gutter is not drawable canvas
	}
	return (m.width > 0 && usable < width) || (m.height > 0 && m.height < height)
}

func (m Model) sourceLine(width int) string {
	// Identity band: the browsing source sits on the left as a position label,
	// the brand sits on the right. The source is a location, not a control;
	// clicking this row still opens the explicit source switcher.
	left := m.renderer.accentStyle.Render(sourceTitle(m.source))
	right := m.renderer.titleStyle.Render("lilt")
	pad := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return fit(left+strings.Repeat(" ", pad)+right, width)
}

func sourceTitle(source string) string {
	switch source {
	case "radio":
		return "Radio"
	case "audius":
		return "Audius"
	case "jamendo":
		return "Jamendo"
	}
	return "Apple Music"
}

func sourceCapabilitySummary(descriptor api.SourceDescriptor) string {
	labels := []struct{ capability, label string }{
		{api.CapPlaybackFull, "full"}, {api.CapPlaybackPreview, "preview"}, {api.CapPlaybackStream, "stream"},
		{api.CapQueue, "queue"}, {api.CapLibrary, "library"}, {api.CapSearchTrending, "trending"}, {api.CapSearchTrendingSongs, "trending"}, {api.CapSearchRadio, "browse"},
	}
	parts := make([]string, 0, len(labels))
	for _, value := range labels {
		if descriptor.Capabilities[value.capability].Available {
			parts = append(parts, value.label)
		}
	}
	if len(parts) == 0 {
		return "no playback capability"
	}
	return strings.Join(parts, ", ")
}

func (m Model) sourceChoiceLabel(source string) string {
	descriptor, ok := m.descriptor(source)
	if !ok {
		return sourceTitle(source) + " · unavailable"
	}
	name := descriptor.Label
	if name == "" {
		name = sourceTitle(source)
	}
	availability := descriptor.Availability
	if availability == "" {
		if descriptor.Available {
			availability = api.AvailabilityReady
		} else {
			availability = api.AvailabilityUnavailable
		}
	}
	parts := []string{name, availability, sourceCapabilitySummary(descriptor)}
	if !descriptor.Available && descriptor.Reason != "" {
		parts = append(parts, presentation.Text(descriptor.Reason))
	}
	return strings.Join(parts, " · ")
}

// viewLine is the surface navigation band: `N Name` entries with a `› ` marker
// on the active surface. The marker plus emphasis carries the current state
// without relying on colour alone.
func (m Model) viewLine(width int) string {
	parts := []string{}
	for i, view := range m.views() {
		label := fmt.Sprintf("%d %s", i+1, view)
		if view == m.view {
			parts = append(parts, m.renderer.accentStyle.Render("› "+label))
		} else {
			parts = append(parts, m.renderer.tabStyle.Render(label))
		}
	}
	return fit(m.renderer.dimStyle.Render(strings.Join(parts, " · ")), width)
}

// listTitle is the fixed panel identity for the main list. Per the design
// system the header carries no state, filters, or progress — only the label.
func (m Model) listTitle() string {
	if len(m.history) > 0 {
		switch {
		case m.detailKind == "playlist":
			return "Playlist"
		case m.detailKind == "album":
			return "Album"
		case m.pageClass == pageClassAggregate:
			return "Search"
		}
		return m.title
	}
	if m.source == "radio" && m.view == "Browse" {
		return "Browse"
	}
	return m.title
}

// listCount is the only number allowed in a panel header: how many selectable
// entries the list currently holds.
func (m Model) listCount() string {
	count := 0
	for _, item := range m.visibleItems() {
		if selectable(item) {
			count++
		}
	}
	if count == 0 && m.loading {
		return ""
	}
	return fmt.Sprintf("%d", count)
}

// listContext is the page scope that used to crowd the panel title: the
// playlist name, the search term, the Browse query, and the active filter. It
// renders as the first body row, never as part of the header.
func (m Model) listContext() string {
	parts := []string{}
	if len(m.history) > 0 {
		switch {
		case m.detailKind == "playlist":
			parts = append(parts, m.title)
		case m.detailKind == "album":
			parts = append(parts, m.title)
		case m.pageClass == pageClassAggregate:
			parts = append(parts, strings.TrimPrefix(m.title, "Search: "))
		}
		// A pushed page with several groups names its groups here: the `[ / ]`
		// jump was footer-only, so playlists and albums after a wall of songs
		// stayed undiscovered (batch 2026-09-23-postaudit M3).
		if part := m.resultGroupContext(); part != "" {
			parts = append(parts, part)
		}
	} else if m.source == "radio" && m.view == "Browse" && m.title != "Browse" {
		parts = append(parts, m.title)
	}
	if m.filter != "" {
		parts = append(parts, "filter: "+presentation.Text(m.filter))
	}
	return strings.Join(parts, " · ")
}

// mainPrefixRows counts the context/status body rows rendered above the item
// window. Mouse mapping and the viewport window subtract them so clicks keep
// targeting the row they land on.
func (m Model) mainPrefixRows() int {
	rows := 0
	if m.listContext() != "" {
		rows++
	}
	if m.pageLoading || (m.loading && len(m.items) > 0) {
		rows++
	}
	return rows
}

// emptyText explains what to do next instead of showing a bare "(empty)".
func (m Model) emptyText() string {
	if m.filter != "" {
		return "(no match for " + presentation.Text(m.filter) + ")"
	}
	if len(m.history) > 0 && m.pageClass == pageClassAggregate {
		return "(empty) — no matching results; press / to search again"
	}
	if m.viewKey() == "radio/Browse" && m.browseQuery != (radioDiscovery{}) {
		return "(no stations matched — press / to adjust the query)"
	}
	if m.view == "Home" {
		if m.account != "" {
			return "(empty) — " + strings.TrimPrefix(m.account, "Account: ")
		}
		return "(empty) — press / to search, : for commands, s to switch source"
	}
	switch m.viewKey() {
	case "radio/Recent", "apple-music/Recent", "audius/Recent", "jamendo/Recent":
		// The 30s listening threshold is server policy; the empty state must
		// say so or a just-listened user reads it as a failed record
		// (batch 2026-09-19-watch-sync-recheck NEW-M4).
		return "(empty) — tracks show here after 30s of listening"
	case "radio/Browse":
		return "(empty) — press / to search and filter stations"
	case "audius/Discover", "jamendo/Discover":
		return "(empty) — no trending available right now"
	case "apple-music/Favorites", "audius/Favorites", "jamendo/Favorites", "radio/Favorites":
		// The favorites page is lilt-local, so an empty page must teach how the
		// first favorite gets created (batch 2026-09-22-jamendo-tui OQ25: a
		// bare "(empty)" left readers guessing, unlike Recent's explanation).
		return "(empty) — play something and press f to favorite it"
	}
	return "(empty)"
}

// kindGlyph marks item types in mixed lists such as search results and Home.
func kindGlyph(kind string) string {
	switch kind {
	case "song":
		return "♪ "
	case "playlist":
		return "≡ "
	case "browse":
		return "▸ "
	}
	return ""
}

// rowState is what a list row can express, as independent capabilities. A list
// without keyboard selection simply leaves focused false, which is why the
// marker column stays reserved instead of every surface inventing its own width
// budget. Playback state and focus are separate on purpose: the fill marks the
// cursor and the text token plus glyph mark the track.
type rowState struct {
	focused bool
	playing bool
	played  bool
	// markerWidth is the column this list reserves for the state marker in front
	// of the label. The Up Next rail reserves 2 so every label starts at the same
	// cell; the main list leaves 0 and lets the marker indent a playing row.
	markerWidth int
}

// listRow renders one row in the shared grammar: `[› ]·[state prefix] content`
// with the padding and the fill that belong to the row's capabilities. Both list
// surfaces (the main list and the Up Next rail) call it, so the cursor and
// playback rules cannot drift apart; the caller only supplies the content in its
// two forms.
//
// styled is the form used when the row carries no wrap of its own: it keeps the
// caller's inline tokens (favorite star, kind glyph) alive, which a nested style
// would cut off at its first reset. plain is used whenever the row is wrapped in
// one state style, because there the token has to come from the wrapper.
// textWidth is the visible text budget inside the row.
func (m Model) listRow(styled, plain string, state rowState, textWidth int) string {
	cursor := "  "
	if state.focused {
		cursor = m.renderer.accentStyle.Render("› ")
	}
	prefix := ""
	switch {
	case state.playing:
		prefix = "▶ "
	case state.played:
		prefix = "· "
	}
	if pad := state.markerWidth - lipgloss.Width(prefix); pad > 0 {
		prefix = strings.Repeat(" ", pad) + prefix
	}
	// Muted played history yields to the cursor: muted text under the fill would
	// hide the row the user is pointing at, and `·` still marks it as played.
	style := m.renderer.rowStyle
	wrapped := false
	switch {
	case state.playing:
		style, wrapped = m.renderer.currentStyle, true
	case state.played && !state.focused:
		style, wrapped = m.renderer.dimStyle, true
	case state.focused:
		wrapped = true
	}
	if !wrapped {
		return cursor + " " + fit(prefix+styled, textWidth) + " "
	}
	if state.focused {
		style = m.renderer.cursorFill(style)
	}
	return cursor + style.Render(" "+fit(prefix+plain, textWidth)+" ")
}

func (m Model) listLines(width, rows int) []string {
	items := m.visibleItems()
	if m.loading && len(items) == 0 {
		return []string{m.renderer.loadingStyle.Render(fit("loading…", width))}
	}
	if m.listErr != "" && len(items) == 0 {
		return []string{m.renderer.errorStyle.Render(fit(m.listErr, width))}
	}
	if len(items) == 0 {
		return []string{m.renderer.tabStyle.Render(fit(m.emptyText(), width))}
	}
	contentWidth := max(1, width-1)
	// Context and transient load state are body rows, never header text. They
	// consume viewport rows, and the click mapping knows about them via
	// mainPrefixRows.
	var prefix []string
	if ctx := m.listContext(); ctx != "" {
		prefix = append(prefix, m.renderer.dimStyle.Render(fit(ctx, contentWidth)))
	}
	if m.loading && len(items) > 0 {
		prefix = append(prefix, m.renderer.loadingStyle.Render(fit("refreshing…", contentWidth)))
	} else if m.pageLoading {
		prefix = append(prefix, m.renderer.loadingStyle.Render(fit("loading more…", contentWidth)))
	}
	itemRows := max(0, rows-len(prefix))
	start, end := m.mainListWindow(itemRows)
	// The rightmost column is a scrollbar gutter, so the view has a visible
	// position indicator and mouse scrolling reads as dragging the bar.
	bar := m.scrollbarColumn(rows, len(items), start)
	// Only the active panel owns the keyboard cursor. An unfocused list keeps its
	// rows and their ▶/· markers but must not paint a cursor row, or both panes
	// read as selected at once (docs/ui/design-system.md §4).
	active := !m.queueFocus
	lines := make([]string, 0, rows)
	for _, text := range prefix {
		lines = append(lines, fit(text, contentWidth)+bar[len(lines)])
	}
	for i := start; i < end; i++ {
		item := items[i]
		if item.Kind == "header" {
			lines = append(lines, m.renderer.accentStyle.Render(fit("── "+item.Title+" ──", contentWidth))+bar[len(lines)])
			continue
		}
		label := item.Title
		metadata, secondary := "", ""
		radioFavorite := false
		appleFavorite := false
		if m.store != nil {
			source := m.source
			if item.Kind == "stream" || item.Kind == "station" {
				source = "radio"
			}
			showRadioFavorite := source == "radio" && (item.Kind == "stream" || item.Kind == "station") && !(m.source == "radio" && m.view == "Favorites" && len(m.history) == 0)
			if showRadioFavorite {
				radioFavorite = m.activity.IsFavorite(source, stableItemID(source, item))
			} else if source != "radio" && m.activity.IsFavorite(source, stableItemID(source, item)) {
				appleFavorite = true
			}
		}
		if item.Kind == "stream" || item.Kind == "station" {
			text, style := m.probeSegment(item)
			metadata = " — " + text
			secondary = m.renderer.dimStyle.Render(" — ") + style.Render(text)
			if item.Artist != "" {
				metadata += " · " + item.Artist
				secondary += m.renderer.dimStyle.Render(" · " + item.Artist)
			}
		} else if item.Artist != "" || item.Album != "" {
			// The secondary metadata chain disambiguates rows that differ only
			// by album: same title/artist across re-releases or compilations
			// reads as five identical rows without it.
			parts := make([]string, 0, 2)
			if item.Artist != "" {
				parts = append(parts, item.Artist)
			}
			if item.Album != "" {
				parts = append(parts, item.Album)
			}
			metadata = " — " + strings.Join(parts, " · ")
			secondary = m.renderer.dimStyle.Render(metadata)
		}
		glyph := ""
		if m.pageClass == pageClassAggregate || m.viewKey() == "apple-music/Home" {
			glyph = kindGlyph(item.Kind)
		}
		label, plainLabel := m.listLabel(item.Title, radioFavorite, appleFavorite, glyph)
		// focus and playback are separate capabilities: focus owns the gutter `›`
		// and the fill, playback owns the text token and the `▶`. listRow keeps that
		// rule in one place, so this surface cannot drift from the Up Next rail.
		focused := active && i == m.selected
		textWidth := max(1, contentWidth-4)
		// The playing row carries the same ▶ marker as the Up Next rail, so
		// playback stays readable as text even where the row has no fill.
		// Station health, codec, country and tags support comparison but are
		// secondary to the station/song name: `secondary` keeps that lower contrast
		// in rows that carry their own tokens, `metadata` is its plain twin.
		row := m.listRow(label+secondary, plainLabel+metadata, rowState{
			focused: focused,
			playing: m.isPlayingItem(item),
		}, textWidth)
		lines = append(lines, row+bar[len(lines)])
	}
	// Pad to the full window so every scrollbar cell lines up with its row.
	for len(lines) < rows {
		lines = append(lines, fit("", contentWidth)+bar[len(lines)])
	}
	return lines
}

// listLabel builds the styled and plain forms of a row label. Rows wrapped in
// their own background (playing/selected) must use the plain form: a nested
// style's reset would otherwise cut the row highlight off partway through the
// row, for example right after the favorite star.
func (m Model) listLabel(title string, radioFavorite, appleFavorite bool, glyph string) (styled, plain string) {
	// Every styled-form segment carries its own token. A nested style ends with
	// a reset, which would drop the outer row style for the rest of the label:
	// the title after a favorite star rendered in the terminal's own foreground
	// and vanished on a painted canvas. With per-segment styles the label is
	// safe in any wrapper — and needs no wrapper at all.
	styled, plain = m.renderer.rowStyle.Render(title), title
	// Both favorite markers are prefixes: a suffix star sits after the title and
	// a long title that fills the row width would truncate the star away, so a
	// favorited station showed no feedback at all (batch 2026-09-23-postaudit
	// H1, r9 replay). One shared form also keeps the favorite marker a single
	// semantic across sources.
	if radioFavorite || appleFavorite {
		styled = m.renderer.accentStyle.Render("★") + " " + styled
		plain = "★ " + plain
	}
	if glyph != "" {
		styled = m.renderer.rowStyle.Render(glyph) + styled
		plain = glyph + plain
	}
	return styled, plain
}

func listLabel(title string, radioFavorite, appleFavorite bool, glyph string) (string, string) {
	return (Model{renderer: defaultRenderer}).listLabel(title, radioFavorite, appleFavorite, glyph)
}

// scrollbarColumn renders the right-edge scrollbar for a list window. A list
// that fits has no track, so the gutter stays quiet until it can move.
func (m Model) scrollbarColumn(rows, total, start int) []string {
	column := make([]string, max(0, rows))
	if rows <= 0 || total <= rows {
		return column
	}
	thumb := max(1, rows*rows/total)
	if thumb > rows {
		thumb = rows
	}
	maxStart := total - rows
	offset := 0
	if maxStart > 0 {
		offset = start * (rows - thumb) / maxStart
	}
	// The scrollbar belongs to the frame, not to the state: it uses the border
	// colour and a line glyph so it reads as structure rather than an accent.
	for i := 0; i < rows; i++ {
		if i >= offset && i < offset+thumb {
			column[i] = m.renderer.scrollbarStyle.Render("┃")
			continue
		}
		column[i] = m.renderer.dimStyle.Render("│")
	}
	return column
}

// queueTitle labels the queue panel; the fraction lives in the header count.
//
// With shuffle on, the rows stay in the submitted order — that is the space
// queue.jump/remove/move index into — while the audio follows MusicKit's own
// order. Saying so in the title is what stops "the shuffle did not work" from
// being read off the rail.
func (m Model) queueTitle() string {
	if m.state.Shuffle {
		return "Up Next · shuffled"
	}
	return "Up Next"
}

// queueCount renders the header count: current position within the queue. An
// empty queue has no count — the body explains the state instead.
func (m Model) queueCount() string {
	if len(m.state.Queue) == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d", m.state.QueueIndex+1, len(m.state.Queue))
}

// queueWindow returns the visible entry range for a panel of rows entries.
func (m Model) queueWindow(rows int) (int, int) {
	total := len(m.state.Queue)
	if total == 0 || rows <= 0 {
		return 0, 0
	}
	rows = min(rows, total)
	anchor := m.state.QueueIndex
	if m.queueFocus {
		anchor = m.queueCursor
	}
	anchor = clamp(anchor, 0, total-1)
	if !m.queueFocus || !m.queueOffsetSet {
		// Unfocused panels follow the current track; a focused panel keeps the
		// window the user scrolled or clicked to.
		return window(anchor, total, rows)
	}
	start := clamp(m.queueOffset, 0, total-rows)
	if anchor < start {
		start = anchor
	} else if anchor >= start+rows {
		start = anchor - rows + 1
	}
	return start, start + rows
}

// panelBodyRows is the number of content rows inside a panel of height rows:
// the two border rows are not content.
func panelBodyRows(height int) int { return max(0, height-2) }

// queuePanelRows is the number of body rows the Up Next panel shows in the
// current layout: the workspace rail when wide, or the full page when narrow.
func (m Model) queuePanelRows() int {
	l := m.layout()
	return panelBodyRows(l.listHeight)
}

// centerQueueWindow re-anchors the window on the current entry. It is used when
// the panel is entered; afterwards the window only moves at the cursor's edge.
func (m Model) centerQueueWindow() Model {
	rows := m.queuePanelRows()
	if rows <= 0 {
		return m
	}
	m.queueOffsetSet = false
	start, _ := m.queueWindow(rows)
	m.queueOffset, m.queueOffsetSet = start, true
	return m
}

func (m Model) queueLines(width, rows int) []string {
	if len(m.state.Queue) == 0 {
		// The rail is a permanent workspace column, so it states why it is
		// empty instead of disappearing or pretending to be another panel.
		if m.state.IsLive || m.playbackSource() == "radio" {
			return []string{m.renderer.tabStyle.Render(fit("Live radio has no finite queue.", width))}
		}
		return []string{m.renderer.tabStyle.Render(fit("Nothing queued yet — play something to build it.", width))}
	}
	start, end := m.queueWindow(rows)
	bar := m.scrollbarColumn(rows, len(m.state.Queue), start)
	// renderPanel places the row inside one blank cell of padding on each side,
	// so the row budget here must match listLines: cursor + text + bar == width.
	contentWidth := max(1, width-3)
	lines := make([]string, 0, max(rows, end-start))
	for i := start; i < end; i++ {
		entry := m.state.Queue[i]
		label := entry.Title
		if entry.Artist != "" {
			label += " — " + entry.Artist
		}
		// Played history is only observable without shuffle: MusicKit advances in
		// its own order when shuffle is on, so rows before the current one were
		// skipped, not played, and must stay upcoming (docs/ui/ux.md).
		played := i < m.state.QueueIndex && !m.state.Shuffle
		// Mirror the main-list grammar through the shared composer. The label keeps
		// its own token when no state wraps the row, so it cannot fall back to the
		// terminal's foreground on a painted canvas.
		row := m.listRow(m.renderer.rowStyle.Render(label), label, rowState{
			focused:     m.queueFocus && i == m.queueCursor,
			playing:     i == m.state.QueueIndex,
			played:      played,
			markerWidth: 2,
		}, contentWidth-2)
		lines = append(lines, row+bar[len(lines)])
	}
	for len(lines) < rows {
		lines = append(lines, fit("", contentWidth)+bar[len(lines)])
	}
	return lines
}

// nowTitle is the fixed panel identity. Temporal states live in the body's
// facts row; they never redefine the panel.
func (m Model) nowTitle() string {
	return "Now Playing"
}

// audioFormat is the meaningful display format. The server's "System-selected"
// placeholder means "unknown", so it is omitted instead of shown as fact.
func audioFormat(state core.PlaybackState) string {
	format := strings.TrimSpace(state.Format)
	if format == "" && state.AudioVariant != nil {
		format = strings.TrimSpace(*state.AudioVariant)
	}
	if format == "" || format == "System-selected" {
		return ""
	}
	return format
}

// nowBody renders the Now Playing contract: identity first, then the reserved
// fact area. It never repeats the source, the queue, or page context, and it
// never grows beyond the rows the shell reserves: a longer fact wraps inside the
// area instead of moving the workspace.
// busyLabel renders the in-flight mutation feedback in the empty dock. The
// first seconds read as "working…"; a long playback start (lazy engine start
// plus MusicKit's paced per-track queue fill can take tens of seconds —
// batch 2026-09-19-watch-sync M1) shows the elapsed time so waiting reads as
// intentional rather than stuck.
// playbackErrorText keeps transport-level failures out of user copy: raw
// "session transport failed: …i/o timeout" names nothing a listener can act
// on (batch 2026-09-19-watch-sync-recheck NEW-H3).
func playbackErrorText(err error) string {
	text := err.Error()
	// The client error is the stable shape: show only the user-facing message,
	// not the "code: message" wire form (a raw "Playback error: playback_error:
	// …" double prefix is noise, batch 2026-09-23-postaudit M4).
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		text = apiErr.Message
	}
	switch {
	case strings.Contains(text, "i/o timeout") || errors.Is(err, context.DeadlineExceeded):
		return "Playback start timed out — try again"
	case errors.Is(err, api.ErrTransport):
		return "Playback could not be started — retry shortly"
	default:
		return "Playback error: " + presentation.Text(text)
	}
}

func (m Model) busyLabel() string {
	// A paced fill reports its own progress, which beats guessing from elapsed
	// time: a 10-40s fill with only "working…" is the exact complaint that
	// introduced the queueFill counts.
	if fill := m.state.QueueFill; fill != nil && fill.Total > 0 {
		return fmt.Sprintf("working… %d/%d — large queues are added track by track", fill.Queued, fill.Total)
	}
	// A single play names its target: "working…" alone left the reader unable to
	// tell whether the app was connecting, loading, or stuck (batch
	// 2026-09-23-postaudit-recheck N5).
	target := ""
	if m.playTarget != "" {
		target = " " + presentation.Text(m.playTarget)
	}
	if m.busySince.IsZero() || m.renderTime.IsZero() {
		return "working…"
	}
	elapsed := int(m.renderTime.Sub(m.busySince).Seconds())
	if target != "" {
		if elapsed < 5 {
			return "working… loading" + target
		}
		return fmt.Sprintf("working… %ds loading%s", elapsed, target)
	}
	if elapsed < 5 {
		return "working…"
	}
	return fmt.Sprintf("working… %ds — large queues are added track by track", elapsed)
}

func (m Model) nowBody(width int) []string {
	if m.state.Track == nil {
		// A fill started by another client is visible here too: the progress
		// comes from the committed state, not from this model's own busy flag.
		if m.busy || m.state.QueueFill != nil {
			return m.nowRows(m.renderer.loadingStyle.Render(m.busyLabel()), "", width)
		}
		// An Apple Music authorization warning belongs to its own source. Showing
		// it in Radio's empty dock makes a working radio browser look broken.
		if warning := m.accountWarning(); warning != "" && m.source == "apple-music" {
			return m.nowRows(m.renderer.tabStyle.Render("Nothing playing"), m.renderer.rowStyle.Render(warning), width)
		}
		return m.nowRows(m.renderer.tabStyle.Render("Nothing playing"), "", width)
	}
	title := m.state.Track.Title
	if m.state.Track.Artist != "" {
		title += " — " + m.state.Track.Artist
	}
	titleLine := m.renderer.trackStyle.Render(fit(title, width))
	if m.state.IsLive && m.store != nil {
		marker := " "
		if m.activity.IsFavorite("radio", stableItemID("radio", *m.state.Track)) {
			marker = m.renderer.accentStyle.Render("★")
		}
		titleLine = marker + " " + m.renderer.trackStyle.Render(fit(title, max(0, width-2)))
	}
	// Inline ICY metadata is the live identity the stream announces; it replaces
	// the placeholder title rather than adding a third row.
	if m.state.IsLive {
		if streamTitle := strings.TrimSpace(m.state.StreamTitle); streamTitle != "" {
			display := streamTitle
			if artist := strings.TrimSpace(m.state.StreamArtist); artist != "" && !strings.Contains(streamTitle, artist) {
				display = artist + " — " + streamTitle
			}
			titleLine = m.renderer.accentStyle.Render(fit("♪ "+display, width))
		}
	}
	return m.nowRows(titleLine, m.playbackFacts(width), width)
}

// nowRows lays the identity line and the fact area into the rows the shell
// reserves. The identity line stays a single line (a track title is a band, not
// a paragraph); the fact area wraps inside its reserved rows.
func (m Model) nowRows(identity, facts string, width int) []string {
	rows := []string{fit(identity, width)}
	if width < 1 {
		return append(rows, "", "")
	}
	rows = append(rows, strings.Split(factArea(facts, width), "\n")...)
	for len(rows) < factRowsReserved+1 {
		rows = append(rows, "")
	}
	return rows
}

// factArea wraps one fact line into the reserved rows. Content that still does
// not fit is ellipsized on the last row rather than dropped silently. Wrapping
// happens here, once, so every fact (state, progress, format, warnings) obeys the
// same rule instead of each calling fit() and truncating.
func factArea(content string, width int) string {
	if width < 1 {
		return ""
	}
	wrapped := strings.Split(ansi.Wrap(content, width, " "), "\n")
	if len(wrapped) > factRowsReserved {
		wrapped = wrapped[:factRowsReserved]
		// The last kept row is usually shorter than the width (it is a wrap
		// boundary), so the mark has to be forced: without it the cut looks like
		// the end of the sentence.
		last := ansi.Truncate(wrapped[factRowsReserved-1], max(1, width-1), "")
		wrapped[factRowsReserved-1] = strings.TrimRight(last, " ") + "…"
	}
	return strings.Join(wrapped, "\n")
}

// playbackFacts renders the compact facts area: playback state, progress and
// time, the helper-reported current codec, and enabled playback modes. The text
// is wrapped into the reserved fact rows, so segments drop from the right only
// when even the reserved area cannot hold them.
func (m Model) playbackFacts(width int) string {
	glyph := "■"
	label := "Stopped"
	style := m.renderer.dimStyle
	switch status := m.statusLabel(); status.kind {
	case "playing":
		glyph, label, style = "▶", "Playing", m.renderer.okStyle
	case "paused":
		glyph, label, style = "❚❚", "Paused", m.renderer.warnStyle
	case "ended":
		glyph, label, style = "■", "Finished", m.renderer.dimStyle
	case "buffering":
		glyph, label, style = "◌", status.text, m.renderer.loadingStyle
	case "starting":
		glyph, label, style = "◌", "Starting…", m.renderer.loadingStyle
	case "error":
		glyph, label, style = "×", "Error", m.renderer.errorStyle
	}
	status := m.statusLabel()
	stateSeg := style.Render(glyph + " " + label)
	if status.kind == "error" && m.state.Error != "" {
		// The error is the fact that matters here: keep it whole and let the fact
		// area wrap it instead of clipping it to the space left over.
		stateSeg += m.renderer.dimStyle.Render(" — ") + m.renderer.errorStyle.Render(m.state.Error)
		return stateSeg
	}
	elapsed := clock(m.displayPositionAt(m.renderTime))
	segs := []string{stateSeg, m.renderer.dimStyle.Render(elapsed)}
	if m.state.Mode == "unverified" {
		// The page has not proved this is a full track. Keep that uncertainty
		// ahead of optional progress/format facts so narrow frames do not hide it.
		segs = append(segs, m.renderer.warnStyle.Render("Full length unverified"))
	}
	if m.state.IsLive {
		segs = append(segs, m.renderer.accentStyle.Render("LIVE"))
	} else if m.state.Duration > 0 {
		duration := m.renderer.dimStyle.Render(clock(m.state.Duration))
		bar := m.progressSeg(elapsed)
		segs = append(segs, bar, duration)
	}
	if m.state.Mode == "preview" {
		segs = append(segs, m.renderer.warnStyle.Render("Preview"))
	}
	// Only surface the Apple account warning when playback is actually limited
	// to previews; during full playback it is stale and misleading.
	if warning := m.accountWarning(); warning != "" && m.source == "apple-music" && !m.state.IsLive && m.state.Mode != "full" {
		segs = append(segs, m.renderer.warnStyle.Render(warning))
	}
	if format := audioFormat(m.state); format != "" && !m.state.IsLive {
		segs = append(segs, m.renderer.dimStyle.Render(format))
	}
	if modes := m.modeFlags(); modes != "" {
		segs = append(segs, m.renderer.accentStyle.Render(modes))
	}
	if m.busy && status.kind != "buffering" && status.kind != "starting" {
		segs = append(segs, m.renderer.loadingStyle.Render("working…"))
	}
	// Shed right-side facts only when even the reserved area cannot hold them; the
	// wrapping itself happens in nowRows, once, for every fact.
	if width > 0 {
		for lipgloss.Width(strings.Join(segs, "  ")) > width*factRowsReserved && len(segs) > 3 {
			segs = slices.Delete(segs, len(segs)-1, len(segs))
		}
	}
	return strings.Join(segs, "  ")
}

// statusLabel resolves the displayed playback state, including the MusicKit
// starting transient and the connecting/buffering distinction.
func (m Model) statusLabel() nowStatus {
	status := m.state.Status
	if status == "" {
		status = "stopped"
	}
	if status == "buffering" {
		if m.connecting() {
			return nowStatus{"buffering", "Connecting…"}
		}
		return nowStatus{"buffering", "Buffering…"}
	}
	// MusicKit reports a stale paused/stopped snapshot while a play command is
	// still in flight; show a single unambiguous status instead of two. Once
	// the command has settled, a paused-at-zero finite track means playback
	// finished or never started — Paused is the truthful answer (a finished
	// queue resets position to 0), not an endless "Starting…".
	if m.busy && (status == "stopped" ||
		(status == "paused" && m.state.Mode == "full" && m.state.Position <= 0)) {
		return nowStatus{"starting", ""}
	}
	if m.state.Error != "" {
		return nowStatus{"error", ""}
	}
	return nowStatus{status, strings.ToUpper(status[:1]) + status[1:]}
}

// progressSeg renders the fill/track bar as its own segment so the elapsed and
// total clocks bracket it on the same row.
func (m Model) progressSeg(elapsed string) string {
	width := 40
	if m.width > 0 {
		width = clamp(m.width-72, 12, 48)
	}
	position := m.displayPositionAt(m.renderTime)
	ratio := 0.0
	if m.state.Duration > 0 {
		ratio = position / m.state.Duration
		if ratio < 0 || math.IsNaN(ratio) {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
	}
	filled := int(ratio * float64(width))
	return m.renderer.accentStyle.Render(strings.Repeat("━", filled)) + m.renderer.dimStyle.Render(strings.Repeat("─", width-filled))
}

func (m Model) modeFlags() string {
	parts := []string{}
	if m.state.Shuffle {
		parts = append(parts, "⇄")
	}
	switch m.state.Repeat {
	case "all":
		parts = append(parts, "↻ All")
	case "one":
		parts = append(parts, "↻ One")
	}
	return strings.Join(parts, " ")
}

// playingSegments returns the playback-state footer hints. The skip keys are
// the highest-frequency playback control and used to be help-only: two
// usability participants (batch 2026-09-22-jamendo-tui, OQ22/OQ23) could not
// find n/b in the footer and misread "e next" as skip-to-next.
func (m Model) playingSegments() []string {
	var segments []string
	// Skipping needs a finite queue with somewhere to go and no live stream.
	if !m.state.IsLive && len(m.state.Queue) > 1 {
		segments = append(segments, "n next · b prev")
	}
	switch m.state.Status {
	case "playing", "buffering":
		segments = append(segments, "space pause", "v stop")
	case "paused":
		segments = append(segments, "space resume", "v stop")
	}
	return segments
}

// footerSegments orders keys by usefulness so narrow terminals drop the least
// important hints first instead of losing the queue hint.
func (m Model) footerSegments() []string {
	if m.input.Focused() {
		switch m.inputMode {
		case "search":
			return []string{"typing", "Enter search", "Esc cancel", "Ctrl+C quit"}
		case "filter":
			return []string{"typing", "Enter apply", "Esc cancel", "Ctrl+C quit"}
		case "url":
			return []string{"typing", "Enter add & play", "Esc cancel", "Ctrl+C quit"}
		}
	}
	if m.queueFocus {
		segments := []string{"j/k move", "enter/p jump", "x remove", "J/K reorder", "c clear"}
		if item, ok := m.favoriteTarget(); ok && favoritable(item) && m.store != nil {
			hint := "f favorite"
			if m.activity.IsFavorite(m.favoriteSource(item), stableItemID(m.favoriteSource(item), item)) {
				hint = "f unfavorite"
			}
			segments = append(segments, hint)
		}
		return append(segments, "0/esc/h back", "? help")
	}
	if (m.detailKind == "playlist" || m.detailKind == "album") && !m.loading {
		playHint := "p play all"
		if m.detailKind == "album" {
			playHint = "p play album"
		}
		segments := []string{playHint}
		if m.declares(m.source, api.CapShuffle) {
			segments = append(segments, "S shuffle")
		}
		segments = append(segments, "enter play from here")
		if m.declares(m.source, api.CapQueue) && activeAppleQueue(m.state) {
			segments = append(segments, "0 edit queue")
		}
		if m.state.Track != nil {
			segments = append(segments, m.playingSegments()...)
		}
		return append(segments, "esc back", "? help")
	}
	if m.listErr != "" {
		return []string{"r retry", "esc back", "/ search", "? help", "q quit"}
	}
	enterHint := "enter open/play"
	if item, ok := m.selectedItem(); ok && (item.Kind == "playlist" || item.Kind == "album") {
		// A container row opens its detail page; only `p` starts it. The generic
		// "open/play" promised a play that Enter does not do (batch
		// 2026-09-23-postaudit-recheck N2).
		enterHint = "enter open"
	} else if m.pageClass != pageClassAggregate && m.detailKind != "playlist" && m.detailKind != "album" {
		if refs, ok := m.playRefsFromSelected(); ok && len(refs) > 1 {
			enterHint = "enter play from here"
		}
	}
	segments := []string{enterHint, "p play"}
	// A selected row that can be queued advertises the two queue keys. Search
	// results are the main place a reader chains tracks now that Enter plays
	// only the pointed row (batch 2026-09-20-search-and-queue N1), so the keys
	// must be visible without opening help. The wording names the queue
	// action: "e next" read as skip-to-next (batch 2026-09-22-jamendo-tui
	// OQ22), while actual skipping is n/b.
	if m.declares(m.source, api.CapQueue) {
		if item, ok := m.selectedItem(); ok && queuable(item) {
			segments = append(segments, "e queue next · E append")
		}
	}
	// Favoriting ranks above the playback hints: it is the selected row's
	// library action, and while playing the n/b + space/v hints pushed it past
	// the width budget so `f favorite` vanished from the footer even though the
	// key worked (batch 2026-09-23-postaudit-recheck N1).
	if m.store != nil {
		if item, ok := m.selectedItem(); ok {
			source := m.favoriteSource(item)
			hint := "f favorite"
			if m.activity.IsFavorite(source, stableItemID(source, item)) {
				hint = "f unfavorite"
			}
			segments = append(segments, hint)
		}
	}
	if m.state.Track != nil {
		segments = append(segments, m.playingSegments()...)
	}
	if len(m.history) > 0 {
		segments = append(segments, "esc back")
		headers := 0
		for _, item := range m.items {
			if item.Kind == "header" {
				headers++
			}
		}
		if headers > 1 {
			segments = append(segments, "[/] group")
		}
	}
	if m.source == "radio" && m.view == "Browse" {
		if m.browseQuery != (radioDiscovery{}) {
			segments = append(segments, "esc popular")
		}
		if normalizedRadioSort(m.browseQuery.Sort) != "recommended" {
			segments = append(segments, "S re-sort")
		}
	}
	if m.declares(m.source, api.CapQueue) && activeAppleQueue(m.state) {
		segments = append(segments, "0 edit queue")
	}
	if m.source == "radio" {
		segments = append(segments, "/ search & filters")
	} else {
		segments = append(segments, "F filter")
	}
	if m.source != "radio" {
		segments = append(segments, "/ search")
	}
	segments = append(segments, "? help", "s source", ": commands", "1-9 view", "q quit")
	return segments
}

func (m Model) footerLine(width int) string {
	segments := m.footerSegments()
	if len(segments) == 0 {
		return m.renderer.tabStyle.Render(fit("", width))
	}
	// `q quit` is the safety affordance: it is dropped from the generic hint
	// list and appended last so it survives every width budget (usability r13:
	// Radio and 80×18 hid the quit key entirely).
	quit := ""
	for i, segment := range segments {
		if segment == "q quit" {
			quit = segment
			segments = append(segments[:i], segments[i+1:]...)
			break
		}
	}
	line := segments[0]
	for _, segment := range segments[1:] {
		candidate := line + " · " + segment
		if lipgloss.Width(candidate) > width {
			break
		}
		line = candidate
	}
	if quit != "" {
		// Fit `q quit` even at the cost of the last optional hint: the quit key
		// must stay visible at any width.
		for lipgloss.Width(line+" · "+quit) > width {
			parts := strings.Split(line, " · ")
			if len(parts) <= 1 {
				break // nothing left to drop; fit clips the tail instead
			}
			line = strings.Join(parts[:len(parts)-1], " · ")
		}
		line += " · " + quit
	}
	return m.renderer.tabStyle.Render(fit(line, width))
}

// paletteDescription names what a palette command does. Empty keeps the bare
// command (the source switches already name their target).
func paletteDescription(command string) string {
	switch command {
	case ":home":
		return "go to Home"
	case ":discover":
		return "open Discover"
	case ":browse":
		return "open Radio Browse"
	case ":recent":
		return "open Recent"
	case ":queue":
		return "focus the Up Next panel"
	case ":auth":
		return "account & authorization"
	case ":play <ref>":
		return "play a ref, e.g. apple-music:song:1440845629"
	case ":help":
		return "open Help"
	case ":source apple-music", ":source audius", ":source jamendo", ":source radio":
		return "switch source"
	}
	return ""
}

func (m Model) overlayDialog(width, height int) string {
	titleStyle := m.renderer.titleStyle
	dimStyle, selStyle, rowStyle := m.renderer.dimStyle, m.renderer.selStyle, m.renderer.rowStyle
	tabStyle, loadingStyle := m.renderer.tabStyle, m.renderer.loadingStyle
	if m.overlay == "source-switcher" {
		sources := m.sourceChoices()
		rows := make([]string, 0, len(sources)+1)
		for i, source := range sources {
			style := tabStyle
			marker := "  "
			if i == m.overlaySelected {
				style = selStyle
				// The palette and lists mark the selection with › as well: reverse
				// video alone is invisible in plain-text captures and for users
				// with color vision deficiencies (batch 2026-09-22-jamendo-tui
				// OQ27, four rounds).
				marker = "› "
			}
			rows = append(rows, style.Render(marker+m.sourceChoiceLabel(source)))
		}
		rows = append(rows, dimStyle.Render("1-4 pick · Enter/click switch · Esc cancel"))
		return m.renderBox("Switch source", rows, min(64, max(28, width-4)), min(height, len(rows)+2))
	}
	if m.overlay == "palette" {
		input := m.input
		input.SetWidth(max(1, min(64, width-6)-4))
		rows := []string{input.View()}
		matches := m.paletteMatches()
		if len(matches) == 0 {
			rows = append(rows, dimStyle.Render("no matching command"))
		} else {
			// Each row says what the command does: `:play <ref>` demanded a
			// "ref" nobody defined, and the palette read as a bare keyword list
			// (batch 2026-09-23-polish p4).
			inner := min(64, width-4) - 2
			for i, command := range matches {
				label := command
				if description := paletteDescription(command); description != "" {
					label = command + " — " + description
				}
				if i == m.overlaySelected {
					rows = append(rows, selStyle.Render("› "+fit(label, inner-2)))
				} else {
					rows = append(rows, tabStyle.Render("  "+fit(label, inner-2)))
				}
			}
		}
		rows = append(rows, dimStyle.Render("Tab/↑↓ select · Enter run · Esc cancel"))
		return m.renderBox("Command palette", rows, min(64, max(28, width-4)), min(height, len(rows)+2))
	}
	if m.overlay == "input" {
		title, hint := "Input", "Enter submit · Esc cancel"
		switch m.inputMode {
		case "search":
			title, hint = "Search", "Source: "+sourceTitle(m.source)+" · Enter search · Esc cancel"
		case "filter":
			title, hint = "Filter Current List", "Enter apply · Esc cancel"
		case "url":
			title, hint = "Add Radio URL", "Enter add & play · Esc cancel"
		case "jamendo-setup":
			title, hint = "Jamendo Setup", "ctrl+o open devportal · Enter validate & save · Esc cancel"
		}
		boxWidth := dialogWidth(64, width)
		inner := boxWidth - 2
		input := m.input
		// bubbles/textinput renders a cursor cell in addition to its prompt and
		// configured field width; reserve it so renderBox never adds an ellipsis.
		input.SetWidth(max(1, inner-lipgloss.Width(input.Prompt)-1))
		if m.inputMode == "jamendo-setup" {
			hintLine := dimStyle.Render(fit(hint, inner))
			if m.jamendoValidating {
				hintLine = loadingStyle.Render(fit("validating…", inner))
			} else if m.jamendoSetupErr != "" {
				hintLine = m.renderer.errorStyle.Render(fit(m.jamendoSetupErr, inner))
			}
			rows := []string{
				rowStyle.Render(fit("Create a free read-only app at devportal.jamendo.com, then", inner)),
				rowStyle.Render(fit("paste its client_id (app-level, not a secret).", inner)),
				"",
				input.View(),
				"",
				hintLine,
			}
			return m.renderBox(title, rows, boxWidth, min(height, len(rows)+2))
		}
		rows := []string{input.View(), "", dimStyle.Render(hint)}
		boxHeight := min(height, len(rows)+2)
		return m.renderBox(title, rows, boxWidth, boxHeight)
	}
	if m.overlay == "discovery" || m.overlay == "discovery-text" || m.overlay == "discovery-options" {
		boxWidth := dialogWidth(72, width)
		inner := boxWidth - 2
		title := "Browse filters"
		var rows []string
		selectedRow := 0
		if m.overlay == "discovery" {
			title = "Search & Filters"
			field := func(index int, label, value string) string {
				marker := "  "
				if m.discoverySelected == index {
					marker = "› "
				}
				line := marker + fmt.Sprintf("%-14s %s", label, value)
				if m.discoverySelected == index {
					return selStyle.Render(fit(line, inner))
				}
				return rowStyle.Render(fit(line, inner))
			}
			button := func(index int, label string) string {
				value := "[ " + label + " ]"
				if m.discoverySelected == index {
					return selStyle.Render("›" + value)
				}
				return rowStyle.Render(value)
			}
			hint := "j/k, ↑↓ or Tab move · Enter selects · Confirm applies · Esc cancels"
			if m.discoverySelected == 0 {
				hint = "Type to search (j/k included) · Enter edit · ↑↓/Tab move · Esc cancel"
			} else if m.discoverySelected == discoverySort {
				hint = "←/→ or Enter change sort · ↑↓/Tab move · Esc cancel"
			}
			confirmLabel := discoveryConfirmLabel(m.discoveryPending, m.discoveryTerm)
			// Short terminals cannot fit the decorated layout; drop spacers and
			// dividers so the action buttons never fall outside the box.
			compact := height > 2 && height-2 < 16
			switch {
			case compact:
				rows = []string{
					titleStyle.Render("Search"),
					field(0, "Text", discoverySearchValue(m.discoveryTerm)),
					titleStyle.Render("Filters"),
					field(1, "Language", discoveryValue(m.discoveryPending.Language)),
					field(2, "Genre", discoveryValue(m.discoveryPending.Tag)),
					field(3, "Country", discoveryCountryValue(m.discoveryPending)),
					field(4, "Sort", radioSortLabel(m.discoveryPending.Sort)),
					field(5, "Reset filters", ""),
					"  " + button(6, confirmLabel) + "  " + button(7, "Cancel"),
					dimStyle.Render(hint),
				}
				selectedRow = []int{1, 3, 4, 5, 6, 7, 8, 8}[clamp(m.discoverySelected, 0, 7)]
			default:
				rows = []string{
					titleStyle.Render("Search"),
					dimStyle.Render(strings.Repeat("─", max(0, inner))),
					field(0, "Text", discoverySearchValue(m.discoveryTerm)),
					"",
					titleStyle.Render("Filters"),
					dimStyle.Render(strings.Repeat("─", max(0, inner))),
					field(1, "Language", discoveryValue(m.discoveryPending.Language)),
					field(2, "Genre", discoveryValue(m.discoveryPending.Tag)),
					field(3, "Country", discoveryCountryValue(m.discoveryPending)),
					field(4, "Sort", radioSortLabel(m.discoveryPending.Sort)),
					field(5, "Reset filters", ""),
					"",
					"  " + button(6, confirmLabel) + "  " + button(7, "Cancel"),
					dimStyle.Render(hint),
				}
				selectedRow = []int{2, 6, 7, 8, 9, 10, 12, 12}[clamp(m.discoverySelected, 0, 7)]
			}
		} else if m.overlay == "discovery-text" {
			title = "Search text"
			rows = []string{m.input.View(), "", "Enter use text · Esc discard"}
		} else {
			title = "Choose " + strings.Title(m.discoveryKind)
			switch {
			case m.discoveryOptionsErr != "":
				rows = []string{"Unable to load options", "Check your connection, then Esc back and reopen"}
			case m.discoveryOptions == nil:
				rows = []string{loadingStyle.Render("loading…")}
			default:
				for _, item := range m.filteredDiscoveryOptions() {
					rows = append(rows, item.Title+" · "+item.Artist)
				}
				if len(rows) == 0 {
					rows = []string{"No matching options"}
				}
			}
			rows = append(rows, "", "Filter: "+discoveryValue(m.discoveryQuery), "Typing filters (j/k included) · ↑↓ move · Enter choose · Esc back")
		}
		boxHeight := min(height, min(len(rows)+2, 16))
		visible := max(0, boxHeight-2)
		rowSelected := m.discoverySelected
		if m.overlay == "discovery" {
			rowSelected = selectedRow
		}
		start, end := window(clamp(rowSelected, 0, max(0, len(rows)-1)), len(rows), visible)
		shown := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			if m.overlay == "discovery" {
				shown = append(shown, fit(rows[i], inner))
				continue
			}
			style := rowStyle
			if i == m.discoverySelected {
				style = selStyle
			}
			marker := "  "
			if i == m.discoverySelected {
				marker = "› "
			}
			shown = append(shown, style.Render(fit(marker+rows[i], inner)))
		}
		return m.renderBox(title, shown, boxWidth, boxHeight)
	}
	if m.overlay == "theme" {
		boxWidth := dialogWidth(40, width)
		inner := boxWidth - 2
		rows := make([]string, 0, len(m.themeNames))
		for i, name := range m.themeNames {
			style := rowStyle
			marker := "  "
			if i == m.themeIndex {
				style = selStyle
				marker = "› "
			}
			rows = append(rows, style.Render(fit(marker+name, inner)))
		}
		rows = append(rows, "", dimStyle.Render("j/k, ↑↓ or Tab preview · Enter save"), dimStyle.Render("Esc cancel · q quit"))
		boxHeight := min(len(rows)+2, height)
		visible := max(0, boxHeight-2)
		start, end := window(clamp(m.themeIndex, 0, max(0, len(rows)-1)), len(rows), visible)
		rows = rows[start:end]
		return m.renderBox("Theme", rows, boxWidth, boxHeight)
	}
	if m.overlay == "auth" {
		boxWidth := dialogWidth(64, width)
		rows := m.authOverlayRows(boxWidth - 2)
		return m.renderBox("Account", rows, boxWidth, min(height, len(rows)+2))
	}
	layout := m.helpOverlay(width, height)
	rows := layout.rows
	title := layout.title
	if layout.visible > 0 && len(rows) > layout.visible {
		contentRows := max(1, layout.visible-1)
		maxOffset := len(rows) - contentRows
		offset := clamp(m.helpOffset, 0, maxOffset)
		start, end := helpWindow(layout.starts, offset, contentRows, len(rows))
		window := append([]string(nil), rows[start:end]...)
		// Keep the status row at the bottom of the box: pad the short last page
		// instead of letting the status float up.
		for len(window) < contentRows {
			window = append(window, "")
		}
		// The close hint must survive any width: drop the scroll-keys hint
		// first when the status does not fit (at 44 cols the full line was
		// clipped to "Esc/? clo…", batch 2026-09-23-polish p5).
		inner := layout.boxWidth - 2
		rangePart := fmt.Sprintf("%d-%d/%d", start+1, end, len(rows))
		status := rangePart + " · ↑↓/PgUp/PgDn scroll · Esc/? close"
		if lipgloss.Width(status) > inner {
			status = rangePart + " · ↑↓ scroll · Esc/? close"
		}
		if lipgloss.Width(status) > inner {
			status = rangePart + " · Esc/? close"
		}
		rows = append(window, m.renderer.dimStyle.Render(status))
	} else {
		rows = append(rows, m.renderer.dimStyle.Render("Esc/? close"))
	}
	return m.renderBox(title, rows, layout.boxWidth, layout.boxHeight)
}

// helpContent is the rendered help body plus the row index where each entry
// starts. Paging uses the starts so a page boundary always lands on a whole
// entry (batch 2026-09-23-postaudit-recheck N4).
type helpContent struct {
	rows   []string
	starts []int
}

func (m Model) helpLines(width int) []string {
	return m.helpContent(width).rows
}

func (m Model) helpContent(width int) helpContent {
	titleStyle, rowStyle := m.renderer.titleStyle, m.renderer.rowStyle
	type entry struct{ group, key, description string }
	entries := []entry{
		{"Navigation", "s", "switch source (explicit; stops current playback)"},
		{"Navigation", "1 - 9", "select sub-view"},
		{"Navigation", "[ / ]", "cycle sub-view; jump result groups on a pushed page"},
		{"Navigation", "j / k", "move selection"},
		{"Navigation", "g / G", "jump to top or bottom"},
		{"Navigation", "enter", "open playlist/album/station or play"},
		{"Navigation", "esc / backspace / h", "back from pushed page or clear filter"},
		{"Navigation", "r", "reload the current list (retry after an error)"},
		{"Playback", "p", "play selected; toggle the playing item"},
		{"Playback", "space / c", "pause or resume"},
		{"Playback", "n / b", "next or previous track (not on a live stream)"},
		{"Playback", "v", "stop"},
		{"Playback", "S / R", "shuffle (restarts a playlist or album) / repeat"},
		{"Playback", "e / E", "queue the selected item next / append it (sources with a queue)"},
		{"Up Next", "0", "focus or leave the panel"},
		{"Up Next", "enter / p", "jump to selected track"},
		{"Up Next", "x", "remove selected track"},
		{"Up Next", "J / K", "reorder selected track"},
		{"Up Next", "c", "clear the queue"},
		{"Library", "f", "favorite / unfavorite (lilt-local list)"},
		{"Library", "a", "add a stream URL to Favorites and play it (Radio)"},
		{"Library", "/", "search current source; Radio Search & Filters"},
		{"Library", "S", "re-sort loaded Radio stations with fresh probe results"},
		{"Library", "F", "filter the current list (all sources except Radio)"},
		{"Interface", "t / i", "theme picker / track info"},
		{"Interface", "q", "quit"},
	}
	lines := make([]string, 0, len(entries)+5)
	starts := make([]int, 0, len(entries)+4)
	group := ""
	for _, entry := range entries {
		if entry.key == "S / R" && !m.declares(m.source, api.CapShuffle) && !m.declares(m.source, api.CapRepeat) {
			continue
		}
		// Queue editing rows describe the Up Next panel and the e/E keys; a
		// source without the queue capability must not advertise them, or the
		// keys fail right after being advertised (fake rounds with a
		// preview-only Apple descriptor, batch 2026-09-28-rounds F1).
		if !m.declares(m.source, api.CapQueue) && (entry.group == "Up Next" || entry.key == "e / E") {
			continue
		}
		if entry.group != group {
			group = entry.group
			// The first four groups are navigation landmarks. Keep the compact
			// interface shortcuts unheaded so Help still fits a 30-row terminal.
			if group != "Interface" {
				starts = append(starts, len(lines))
				lines = append(lines, titleStyle.Render(fit("── "+strings.ToUpper(group)+" ──", width)))
			}
		}
		starts = append(starts, len(lines))
		for _, row := range wrapHelpRow(entry.key, entry.description, 16, width) {
			lines = append(lines, rowStyle.Render(fit(row, width)))
		}
	}
	return helpContent{rows: lines, starts: starts}
}

// wrapHelpRow renders "<key> <description>" and wraps the description onto
// aligned continuation lines so narrow terminals can still read it in full.
// The key column widens for keys longer than the usual width.
func wrapHelpRow(key, description string, minKeyWidth, width int) []string {
	keyWidth := max(minKeyWidth, lipgloss.Width(key))
	prefix := fmt.Sprintf("%-*s ", keyWidth, key)
	indent := strings.Repeat(" ", keyWidth+1)
	descWidth := max(1, width-keyWidth-1)

	var wrapped []string
	current := ""
	for _, word := range strings.Fields(description) {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if current != "" && lipgloss.Width(candidate) > descWidth {
			wrapped = append(wrapped, current)
			current = word
			continue
		}
		current = candidate
	}
	if current != "" {
		wrapped = append(wrapped, current)
	}
	if len(wrapped) == 0 {
		return []string{prefix}
	}
	rows := []string{prefix + wrapped[0]}
	for _, continuation := range wrapped[1:] {
		rows = append(rows, indent+continuation)
	}
	return rows
}

func (m Model) infoLines(width int) []string {
	rowStyle := m.renderer.rowStyle
	lines := []string{}
	add := func(key, value string) {
		lines = append(lines, rowStyle.Render(fit(fmt.Sprintf("%-10s %s", key, emptyDash(value)), width)))
	}
	if m.state.Track != nil {
		add("Title", m.state.Track.Title)
		add("Artist", m.state.Track.Artist)
		add("Kind", m.state.Track.Kind)
		add("ID", m.state.Track.ID)
		add("URL", m.state.Track.URL)
	}
	add("Status", m.state.Status)
	if m.state.Error != "" {
		add("Error", m.state.Error)
	}
	add("Mode", m.state.Mode)
	add("Auth", emptyDash(m.authorization))
	if summary := sourceAccountSummary(m.source, m.sourceAuth); summary != "" {
		add("Account", strings.TrimPrefix(summary, "Account: "))
	} else if m.account != "" {
		add("Account", strings.TrimPrefix(m.account, "Account: "))
	}
	add("Live", fmt.Sprintf("%v", m.state.IsLive))
	if format := audioFormat(m.state); format != "" {
		add("Format", format)
	}
	for index, format := range m.state.Available {
		key := "Offer"
		if index > 0 {
			key = ""
		}
		// Catalog variants are useful diagnostic metadata, but do not identify
		// the active stream. Keep them in Track Info rather than Now Playing.
		add(key, format)
	}
	add("Shuffle", fmt.Sprintf("%v", m.state.Shuffle))
	add("Repeat", m.state.Repeat)
	// Track Info must agree with the Now Playing dock: both show the displayed
	// position, so a session whose authoritative position lags (fake engine
	// publishes no position updates) cannot read as two different facts
	// (batch 2026-09-28-rounds F3).
	add("Position", fmt.Sprintf("%.0f / %.0f s", m.displayPositionAt(m.renderTime), m.state.Duration))
	add("Queue", fmt.Sprintf("%d entries, index %d", len(m.state.Queue), m.state.QueueIndex))
	if m.declares(m.source, api.CapQueue) && activeAppleQueue(m.state) {
		add("Up Next", "0 focus; Enter/p jump; x remove")
	}
	return lines
}

// renderBox keeps overlays dense so text-heavy controls retain their full
// instructions on smaller terminals.
func (m Model) renderBox(title string, lines []string, width, height int) string {
	if width < 4 {
		width = 4
	}
	if height < 3 {
		height = 3
	}
	inner := width - 2
	color := m.renderer.border
	border := lipgloss.NewStyle().Foreground(color)
	// The title keeps the panel-title hierarchy: the leading rule stays in the
	// border colour, the name itself is the accent token (renderPanel parity).
	label := "─ " + title + " "
	if lipgloss.Width(label) > inner-1 {
		label = fit(label, inner-1)
	}
	rule := "─ "
	titleText := clip(title, max(1, inner-lipgloss.Width(rule)-1))
	labelWidth := lipgloss.Width(rule) + lipgloss.Width(titleText) + 1
	top := border.Render("┌"+rule) + m.renderer.titleStyle.Render(titleText) +
		border.Render(" "+strings.Repeat("─", max(0, inner-labelWidth))+"┐")
	bottom := border.Render("└" + strings.Repeat("─", inner) + "┘")
	body := make([]string, 0, height-2)
	for i := 0; i < height-2; i++ {
		text := strings.Repeat(" ", inner)
		if i < len(lines) && lines[i] != "" {
			text = clip(lines[i], inner)
			if visible := lipgloss.Width(text); visible < inner {
				text += strings.Repeat(" ", inner-visible)
			}
		}
		body = append(body, border.Render("│")+text+border.Render("│"))
	}
	return strings.Join(append(append([]string{top}, body...), bottom), "\n")
}

// renderSpaciousBox is reserved for the persistent browsing and playback
// panels. Overlays retain their denser geometry so no instruction is clipped.
// renderPanel draws a panel box with the grammar from the design system: a
// fixed short title, an optional count in a dimmer style, and body lines. The
// count is the only decoration the header may carry; when the label alone is
// too wide the count is dropped first and the title is clipped last.
func (m Model) renderPanel(title, count string, lines []string, width, height int, activeBox bool) string {
	if width < 6 {
		width = 6
	}
	if height < 3 {
		height = 3
	}
	inner := width - 2
	contentWidth := max(1, inner-2) // one cell of breathing room on both sides
	color := m.renderer.border
	border := lipgloss.NewStyle().Foreground(color)
	labelStyle := m.renderer.dimStyle
	if activeBox {
		labelStyle = m.renderer.accentStyle.Bold(true)
	}
	// The leading rule is part of the box frame, not part of the title. Keep it
	// in the border colour so the title colour starts at the first letter.
	prefix, suffix := "── ", " "
	titleText := strings.ToUpper(clip(title, max(1, inner-4)))
	countText := ""
	if count != "" {
		countText = m.renderer.dimStyle.Render(" (" + count + ")")
	}
	// The count is secondary: drop it before clipping the title.
	if lipgloss.Width(prefix)+lipgloss.Width(titleText)+lipgloss.Width(countText)+lipgloss.Width(suffix)+1 > inner {
		countText = ""
		titleText = clip(strings.ToUpper(title), max(1, inner-lipgloss.Width(prefix)-lipgloss.Width(suffix)-1))
	}
	labelWidth := lipgloss.Width(prefix) + lipgloss.Width(titleText) + lipgloss.Width(countText) + lipgloss.Width(suffix)
	top := border.Render("┌"+prefix) + labelStyle.Render(titleText) + countText + border.Render(suffix+strings.Repeat("─", max(0, inner-labelWidth))+"┐")
	bottom := border.Render("└" + strings.Repeat("─", inner) + "┘")
	body := make([]string, 0, height-2)
	for i := 0; i < height-2; i++ {
		text := ""
		if i < len(lines) && lines[i] != "" {
			text = clip(lines[i], contentWidth)
		}
		if visible := lipgloss.Width(text); visible < contentWidth {
			text += strings.Repeat(" ", contentWidth-visible)
		}
		body = append(body, border.Render("│")+" "+text+" "+border.Render("│"))
	}
	return strings.Join(append(append([]string{top}, body...), bottom), "\n")
}

func window(selected, total, rows int) (int, int) {
	if total <= rows {
		return 0, total
	}
	start := selected - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > total {
		start = total - rows
	}
	return start, start + rows
}

// mainListWindow keeps a stable viewport for the browse list. Unlike the
// queue's cursor-centred window, a mouse-selected song must remain below the
// pointer so a second click targets that same song.
func (m Model) mainListWindow(rows int) (int, int) {
	items := m.visibleItems()
	if len(items) == 0 || rows <= 0 {
		return 0, 0
	}
	rows = min(rows, len(items))
	selected := clamp(m.selected, 0, len(items)-1)
	start := clamp(m.listOffset, 0, len(items)-rows)
	if selected < start {
		start = selected
	}
	if selected >= start+rows {
		start = selected - rows + 1
	}
	return start, start + rows
}

// keepMainSelectionVisible advances the viewport only after keyboard movement
// leaves it. A click already supplies a stable viewport directly.
func (m Model) keepMainSelectionVisible() Model {
	if m.queueFocus {
		return m
	}
	start, _ := m.mainListWindow(panelBodyRows(m.layout().listHeight) - m.mainPrefixRows())
	m.listOffset = start
	return m
}

// scrollMainList drags the viewport like a scrollbar instead of walking the
// selection cursor. The cursor keeps its item and only follows when it would
// otherwise leave the window, so wheeling reads as moving the list itself.
func (m Model) scrollMainList(delta int) Model {
	items := m.visibleItems()
	rows := panelBodyRows(m.layout().listHeight) - m.mainPrefixRows()
	if len(items) == 0 || rows <= 0 || len(items) <= rows {
		return m
	}
	start, _ := m.mainListWindow(rows)
	start = clamp(start+delta, 0, len(items)-rows)
	m.listOffset = start
	windowItems := items[start : start+rows]
	switch {
	case m.selected < start:
		m.selected = start + firstSelectableIndex(windowItems)
	case m.selected >= start+rows:
		m.selected = start + lastSelectableIndex(windowItems)
	}
	return m
}

// progressBar renders a bar that spans the available row with the elapsed /
// total clock pinned to the right edge, so the time reads as the bar's scale
// instead of trailing it mid-row.
func progressBar(position, duration float64, width int) string {
	durationText := clock(duration)
	if duration <= 0 {
		durationText = "--:--"
	}
	clockText := clock(position) + " / " + durationText
	barWidth := width - lipgloss.Width(clockText) - 2
	if barWidth < 8 {
		barWidth = 8
	}
	if duration <= 0 {
		return strings.Repeat("░", barWidth) + "  " + clockText
	}
	ratio := position / duration
	if ratio < 0 || math.IsNaN(ratio) {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(barWidth))
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + "  " + clockText
}

func clock(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// clip truncates text to a single line of at most width cells. It is the final
// guard so a long title can never wrap or overflow its box.
func clip(value string, width int) string {
	if width < 1 {
		return ""
	}
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(value) + "…"
}

func fit(value string, width int) string {
	if width < 1 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value + strings.Repeat(" ", width-lipgloss.Width(value))
	}
	if width == 1 {
		return "…"
	}
	truncated := lipgloss.NewStyle().MaxWidth(width - 1).Render(value)
	if visible := lipgloss.Width(truncated); visible < width-1 {
		truncated += strings.Repeat(" ", width-1-visible)
	}
	return truncated + "…"
}

func emptyDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}
