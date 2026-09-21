package tui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
)

func (f radioDiscovery) browseTitle() string {
	title := "Popular Worldwide"
	if f.Term != "" || f.filter() != (radio.Filter{}) {
		title = "Showing: " + presentation.Text(f.summary())
	}
	if sort := normalizedRadioSort(f.Sort); sort != "recommended" {
		title += " · " + radioSortLabel(sort)
	}
	return title
}

func (m Model) browsePageKey() string { return m.browseQuery.browseTitle() }

// browseCacheKey scopes the in-session Browse snapshot to the exact query and
// sort, so changing either starts a clean page instead of reusing stale rows.
func (m Model) browseCacheKey() string { return "radio/Browse|" + m.browsePageKey() }

func (m *Model) invalidateBrowseCache() {
	for key := range m.cache {
		if strings.HasPrefix(key, "radio/Browse|") {
			delete(m.cache, key)
		}
	}
}

// rememberBrowsePage keeps the first page of the active query so a later visit
// can paint immediately instead of flashing LOADING while the directory answers.
func (m Model) rememberBrowsePage() {
	if m.cache == nil || len(m.items) == 0 {
		return
	}
	m.cache[m.browseCacheKey()] = m.items
}

// mergeRadioItems folds fresh directory rows into the existing list without
// changing row order: matching stations update in place, unknown ones append.
// Rows already fetched from later pages are kept, so a first-page refresh
// cannot shrink a paged list.
func mergeRadioItems(existing, fresh []core.Item) ([]core.Item, int) {
	if len(existing) == 0 {
		return fresh, len(fresh)
	}
	updates := make(map[string]core.Item, len(fresh))
	for _, item := range fresh {
		updates[radioProbeKey(item)] = item
	}
	merged := make([]core.Item, 0, len(existing)+len(fresh))
	seen := make(map[string]bool, len(existing)+len(fresh))
	for _, item := range existing {
		key := radioProbeKey(item)
		seen[key] = true
		if updated, ok := updates[key]; ok {
			merged = append(merged, updated)
			continue
		}
		merged = append(merged, item)
	}
	appended := 0
	for _, item := range fresh {
		key := radioProbeKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, item)
		appended++
	}
	return merged, appended
}

// applyCachedList paints the remembered Browse page. It only runs when the view
// is still empty, so a faster live result can never be overwritten by stale rows.
func (m Model) applyCachedList(msg listMsg) (tea.Model, tea.Cmd) {
	if msg.key != m.viewKey() || len(m.items) > 0 || !m.loading {
		return m, nil
	}
	m.loading = false
	m.listErr = ""
	m.title = msg.title
	// The stored page is already in its final order. Sorting again here would
	// re-rank it with probe results gathered since, so merely switching views
	// would reshuffle the list.
	m.items = presentation.Items(msg.items)
	if msg.key == "radio/Browse" {
		m.pageOffset = radioPageSize
		m.pageMore = len(m.items) > 0
		m.pageKey = m.browsePageKey()
	}
	m.selected = 0
	return m.scheduleProbes()
}

// applySilentList folds in the background refresh. Rows and health markers move
// to the fresh result, but the user's cursor stays on the same station.
func (m Model) applySilentList(msg listMsg) (tea.Model, tea.Cmd) {
	if msg.key != m.viewKey() {
		return m, nil
	}
	m.loading = false
	if msg.err != nil {
		// A background refresh failure must not replace a usable cached list.
		m.logEvent("radio.error", map[string]any{"action": "browse.refresh", "error": msg.err.Error()})
		return m, nil
	}
	m.listErr = ""
	selectedKey := ""
	if item, ok := m.selectedItem(); ok {
		selectedKey = radioProbeKey(item)
	}
	m.title = msg.title
	if msg.key == "radio/Browse" {
		// Refresh metadata and health in place. Re-sorting here made the list
		// reshuffle every time the user changed views, so order only changes on
		// a fresh load, a query/sort change, or an explicit `S` re-sort.
		m.items, _ = mergeRadioItems(m.items, presentation.Items(msg.items))
		m.pageLoading = false
		m.pageKey = m.browsePageKey()
		m.rememberBrowsePage()
	} else {
		m.items = presentation.Items(msg.items)
	}
	if selectedKey != "" {
		for index, item := range m.items {
			if radioProbeKey(item) == selectedKey {
				m.selected = index
				break
			}
		}
	}
	return m.scheduleProbes()
}

func (m *Model) resetBrowsePaging() {
	m.pageOffset = 0
	m.pageMore = true
	m.pageLoading = false
	m.pageFailed = false
	m.pageKey = m.browsePageKey()
}

func radioProbeKey(item core.Item) string { return stableItemID("radio", item) }

// probeSegment renders the station health marker. Color, symbol, and text are
// all present so a no-color terminal still distinguishes every state.
func (m Model) probeSegment(item core.Item) (string, lipgloss.Style) {
	probe, ok := m.probes[radioProbeKey(item)]
	if !ok {
		return "○ unchecked", m.renderer.dimStyle
	}
	switch probe.status {
	case "queued":
		return "○ queued", m.renderer.dimStyle
	case "checking":
		return "◌ checking…", m.renderer.warnStyle
	case "healthy":
		segment := "● " + formatProbeLatency(probe.latency)
		if probe.persisted {
			segment += " · checked " + formatProbeAge(m.renderTime.Sub(probe.checkedAt))
		}
		return segment, m.renderer.okStyle
	case "failed":
		return "× " + shortProbeError(probe.code), m.renderer.errorStyle
	default:
		return "○ unchecked", m.renderer.dimStyle
	}
}

// formatProbeLatency keeps sub-100ms readability while showing everything
// slower in seconds, which reads better than four-digit millisecond counts.
func formatProbeLatency(ms int) string {
	if ms < 100 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func formatProbeAge(age time.Duration) string {
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Round(time.Minute)/time.Minute))
	}
	return fmt.Sprintf("%dh ago", int(age.Round(time.Hour)/time.Hour))
}

func (m Model) sortRadioItems(items []core.Item) []core.Item {
	ordered := append([]core.Item(nil), items...)
	mode := normalizedRadioSort(m.browseQuery.Sort)
	now := time.Now()
	familiar := map[string]bool{}
	if m.store != nil {
		for _, item := range m.activity.RecentFor("radio") {
			familiar[radioProbeKey(item)] = true
		}
		for _, item := range m.activity.FavoritesFor("radio") {
			familiar[radioProbeKey(item)] = true
		}
	}
	type rank struct {
		health, latency, clicks, trend int
		familiar                       bool
		name                           string
	}
	ranks := make(map[string]rank, len(ordered))
	for _, item := range ordered {
		value := rank{health: 1, latency: math.MaxInt, name: strings.ToLower(item.Title)}
		value.familiar = familiar[radioProbeKey(item)]
		if item.Radio != nil {
			value.clicks, value.trend = item.Radio.ClickCount, item.Radio.ClickTrend
		}
		if health, ok := m.radioCache.FreshHealth(item.URL, now); ok {
			if health.Status == "healthy" {
				value.health, value.latency = 0, health.LatencyMs
			} else {
				value.health = 2
			}
		}
		ranks[radioProbeKey(item)] = value
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ranks[radioProbeKey(ordered[i])], ranks[radioProbeKey(ordered[j])]
		switch mode {
		case "name":
			return left.name < right.name
		case "popular":
			if left.clicks != right.clicks {
				return left.clicks > right.clicks
			}
			if left.trend != right.trend {
				return left.trend > right.trend
			}
		case "fastest":
			if left.health != right.health {
				return left.health < right.health
			}
			if left.latency != right.latency {
				return left.latency < right.latency
			}
			if left.clicks != right.clicks {
				return left.clicks > right.clicks
			}
		default: // Recommended: known-good to this user, then locally healthy, then popularity.
			if left.familiar != right.familiar {
				return left.familiar
			}
			if left.health != right.health {
				return left.health < right.health
			}
			if left.clicks != right.clicks {
				return left.clicks > right.clicks
			}
			if left.trend != right.trend {
				return left.trend > right.trend
			}
			if left.latency != right.latency {
				return left.latency < right.latency
			}
		}
		return left.name < right.name
	})
	return ordered
}

// radioHealthCoverage counts how many visible stations have a fresh local
// measurement. Sort modes that depend on latency are only as good as this
// ratio, so the title reports it instead of pretending the order is complete.
func (m Model) radioHealthCoverage() (measured, total int) {
	now := time.Now()
	for _, item := range m.visibleItems() {
		if item.Kind != "stream" && item.Kind != "station" {
			continue
		}
		total++
		if _, ok := m.radioCache.FreshHealth(item.URL, now); ok {
			measured++
		}
	}
	return measured, total
}

// resortRadioBrowse re-applies the active sort to already loaded rows. Startup
// sorting runs while most rows are unmeasured, so an explicit re-sort lets the
// user fold in background probe results without a full directory reload.
func (m Model) resortRadioBrowse() (tea.Model, tea.Cmd) {
	if len(m.items) == 0 {
		return m.withToast("Nothing to sort yet", true)
	}
	selectedKey := ""
	if item, ok := m.selectedItem(); ok {
		selectedKey = radioProbeKey(item)
	}
	m.items = m.sortRadioItems(m.items)
	if selectedKey != "" {
		for index, item := range m.items {
			if radioProbeKey(item) == selectedKey {
				m.selected = index
				break
			}
		}
	}
	m = m.keepMainSelectionVisible()
	measured, total := m.radioHealthCoverage()
	return m.withToast(fmt.Sprintf("Sorted by %s — %d/%d measured", radioSortLabel(m.browseQuery.Sort), measured, total), false)
}

func shortProbeError(code string) string {
	switch code {
	case "tls":
		return "TLS error"
	case "timeout":
		return "timeout"
	case "http":
		return "HTTP error"
	case "unsupported":
		return "unsupported"
	case "network":
		return "network error"
	case "transport":
		return "probe unavailable"
	default:
		return "probe failed"
	}
}

// probeWindow returns the items currently rendered in the list window with the
// selected row first, matching the spec's probe priority order.
func (m Model) probeWindow() []core.Item {
	items := m.visibleItems()
	if len(items) == 0 {
		return nil
	}
	rows := m.layout().listHeight
	if rows <= 0 || rows > len(items) {
		rows = len(items)
	}
	start, end := m.mainListWindow(rows)
	ordered := make([]core.Item, 0, end-start)
	if m.selected >= start && m.selected < end {
		ordered = append(ordered, items[m.selected])
	}
	for i := start; i < end; i++ {
		if i == m.selected {
			continue
		}
		ordered = append(ordered, items[i])
	}
	return ordered
}

// scheduleProbes queues unchecked visible stations and starts up to the worker
// limit. It is called after key presses, list loads, and resizes; it performs
// no IO itself and pauses while a playback command is in flight.
func (m Model) scheduleProbes() (Model, tea.Cmd) {
	if m.source != "radio" || m.player == nil || m.overlay != "" || m.busy {
		if m.probeActive > 0 || len(m.probeQueue) > 0 {
			m.logEvent("probe", map[string]any{"event": "paused", "source": m.source, "overlay": m.overlay, "busy": m.busy, "active": m.probeActive, "queue": len(m.probeQueue)})
		}
		return m, nil
	}
	if m.probes == nil {
		m.probes = map[string]radioProbe{}
	}
	if scope := m.viewKey(); scope != m.probeScope {
		// Leaving a page drops its pending work, but the dropped items must
		// become eligible again: forgetting them keeps a later visit from
		// seeing them as "known" and stranding them on queued forever.
		m.probeScope = scope
		// A timeout is transient by definition. Forget it only on a new scope,
		// never during this scheduling pass, to avoid a tight re-probe loop.
		for key, probe := range m.probes {
			if probe.status == "failed" && probe.code == "timeout" {
				delete(m.probes, key)
			}
		}
		for _, req := range m.probeQueue {
			if probe, ok := m.probes[req.key]; ok && probe.status == "queued" {
				delete(m.probes, req.key)
			}
		}
		m.probeQueue = nil
	}
	queuedBefore := len(m.probeQueue)
	for _, item := range m.probeWindow() {
		if item.Kind != "stream" && item.Kind != "station" {
			continue
		}
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		key := radioProbeKey(item)
		if _, known := m.probes[key]; known {
			continue
		}
		if cached, ok := m.radioCache.FreshHealth(item.URL, time.Now()); ok {
			m.probes[key] = radioProbe{status: cached.Status, latency: cached.LatencyMs, code: cached.Code, checkedAt: cached.CheckedAt, persisted: true}
			continue
		}
		m.probes[key] = radioProbe{status: "queued"}
		m.probeQueue = append(m.probeQueue, probeRequest{key: key, url: item.URL})
	}
	if len(m.probeQueue) != queuedBefore {
		m.logEvent("probe", map[string]any{"event": "schedule", "scope": m.viewKey(), "visible": len(m.probeWindow()), "queue": len(m.probeQueue), "active": m.probeActive})
	}
	return m.pumpProbes()
}

func (m Model) pumpProbes() (Model, tea.Cmd) {
	var cmds []tea.Cmd
	for m.probeActive < radioProbeWorkers && len(m.probeQueue) > 0 {
		req := m.probeQueue[0]
		m.probeQueue = m.probeQueue[1:]
		if probe, ok := m.probes[req.key]; !ok || probe.status != "queued" {
			m.logEvent("probe", map[string]any{"event": "drop", "queue": len(m.probeQueue), "active": m.probeActive})
			continue
		}
		m.probes[req.key] = radioProbe{status: "checking"}
		m.probeActive++
		m.logEvent("probe", map[string]any{"event": "start", "url": req.url, "queue": len(m.probeQueue), "active": m.probeActive})
		cmds = append(cmds, m.probeCmd(req))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) probeCmd(req probeRequest) tea.Cmd {
	player := m.player
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), radioProbeRPCTimeout)
		defer cancel()
		result, err := player.Probe(ctx, req.url, radioProbeTimeoutMs)
		return probeMsg{key: req.key, url: req.url, result: result, err: err}
	}
}

// clearProbesOnDisconnect drops in-flight work so a dead transport cannot leave
// rows stuck on "checking".
func (m Model) clearProbesOnDisconnect() Model {
	m.probeQueue = nil
	m.probeActive = 0
	for key, probe := range m.probes {
		if probe.status == "queued" || probe.status == "checking" {
			delete(m.probes, key)
		}
	}
	return m
}

func (f radioDiscovery) filter() radio.Filter {
	return radio.Filter{Language: f.Language, Tag: f.Tag, CountryCode: f.CountryCode}
}

func (f radioDiscovery) summary() string {
	parts := []string{}
	if f.Term != "" {
		parts = append(parts, f.Term)
	}
	if f.Language != "" {
		parts = append(parts, f.Language)
	}
	if f.Tag != "" {
		parts = append(parts, f.Tag)
	}
	if f.CountryName != "" {
		parts = append(parts, f.CountryName)
	} else if f.CountryCode != "" {
		parts = append(parts, f.CountryCode)
	}
	return strings.Join(parts, " · ")
}

func discoveryValue(value string) string {
	if value == "" {
		return "Any"
	}
	return value
}

func discoverySearchValue(value string) string {
	if value == "" {
		return "Not set"
	}
	return value
}

func discoveryCountryValue(filter radioDiscovery) string {
	if filter.CountryName != "" {
		return filter.CountryName
	}
	return discoveryValue(filter.CountryCode)
}

func normalizedRadioSort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "popular", "fastest", "name":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "recommended"
	}
}

func radioSortLabel(value string) string {
	switch normalizedRadioSort(value) {
	case "popular":
		return "Popular"
	case "fastest":
		return "Fastest"
	case "name":
		return "Name"
	default:
		return "Recommended"
	}
}

func nextRadioSort(value string, delta int) string {
	values := []string{"recommended", "popular", "fastest", "name"}
	index := indexOf(values, normalizedRadioSort(value))
	index = (index + delta + len(values)) % len(values)
	if values[index] == "recommended" {
		return ""
	}
	return values[index]
}

// discoveryConfirmLabel names what Confirm will do, so an empty query reads as
// "Show all" instead of a mysterious generic button.
func discoveryConfirmLabel(pending radioDiscovery, term string) string {
	term = strings.TrimSpace(term)
	hasFacets := pending.filter() != (radio.Filter{})
	hasSort := normalizedRadioSort(pending.Sort) != "recommended"
	switch {
	case term == "" && !hasFacets && hasSort:
		return "Apply sort"
	case term == "" && !hasFacets:
		return "Show all"
	case term != "" && !hasFacets:
		runes := []rune(presentation.Text(term))
		if len(runes) > 18 {
			runes = append(runes[:18], '…')
		}
		return `Search "` + string(runes) + `"`
	case term == "":
		return "Apply filters"
	default:
		return "Search + filters"
	}
}

func (m Model) autoSearch(term string) tea.Cmd {
	source := m.source
	withAlbums := m.declares(source, api.CapSearchAlbums)
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		songs, songErr := m.provider.SearchSource(ctx, source, term, "song", 20)
		var albums []core.Item
		if withAlbums {
			albums, _ = m.provider.SearchSource(ctx, source, term, "album", 20)
		}
		playlists, _ := m.provider.SearchSource(ctx, source, term, "playlist", 20)
		if songErr != nil && len(albums) == 0 && len(playlists) == 0 {
			return autoMsg{term: term, err: songErr}
		}
		return autoMsg{term: term, items: grouped(songs, albums, playlists)}
	}
}

func grouped(songs, albums, playlists []core.Item) []core.Item {
	items := make([]core.Item, 0, len(songs)+len(albums)+len(playlists)+3)
	if len(songs) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Songs"})
		items = append(items, songs...)
	}
	if len(albums) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Albums"})
		items = append(items, albums...)
	}
	if len(playlists) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Playlists"})
		items = append(items, playlists...)
	}
	return items
}

func selectable(item core.Item) bool { return item.Kind != "header" }

// favoritable restricts favorites to real playable items. Container and
// navigation rows (continue/entry/browse) have no item identity, so the server
// rejects them; catching it here keeps the action from surfacing a save error.
func favoritable(item core.Item) bool {
	switch item.Kind {
	case "song", "playlist", "album", "station", "stream":
		return true
	default:
		return false
	}
}

// queuable reports whether a row can be added to a finite queue: playable items
// and containers qualify, live streams and navigation rows do not.
func queuable(item core.Item) bool {
	switch item.Kind {
	case "song", "playlist", "album", "station":
		return true
	default:
		return false
	}
}

func (m Model) loadDiscoveryOptions(kind string) tea.Cmd {
	generation := m.generation
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		var values []core.Item
		var err error
		switch kind {
		case "language":
			var entries []radio.Language
			entries, err = m.radio.Languages(ctx)
			for _, entry := range entries {
				values = append(values, core.Item{ID: entry.Name, Title: entry.Name, Artist: fmt.Sprintf("%d stations", entry.StationCount)})
			}
		case "genre":
			var entries []radio.Tag
			entries, err = m.radio.Tags(ctx)
			for _, entry := range entries {
				values = append(values, core.Item{ID: entry.Name, Title: entry.Name, Artist: fmt.Sprintf("%d stations", entry.StationCount)})
			}
		case "country":
			var entries []radio.Country
			entries, err = m.radio.Countries(ctx)
			for _, entry := range entries {
				values = append(values, core.Item{ID: entry.Code, Title: entry.Name, Artist: fmt.Sprintf("%d stations", entry.StationCount)})
			}
		}
		values = append([]core.Item{{Title: "Any"}}, values...)
		if err != nil {
			m.logEvent("radio.error", map[string]any{"action": "options", "kind": kind, "error": err.Error()})
		}
		return discoveryOptionsMsg{generation: generation, kind: kind, values: values, err: err}
	}
}

func (m Model) filteredDiscoveryOptions() []core.Item {
	if m.discoveryQuery == "" {
		return m.discoveryOptions
	}
	needle := strings.ToLower(m.discoveryQuery)
	out := []core.Item{}
	for _, value := range m.discoveryOptions {
		if strings.Contains(strings.ToLower(value.Title+" "+value.Artist), needle) {
			out = append(out, value)
		}
	}
	return out
}

func (m Model) applyDiscoveryFilter(query radioDiscovery, term string) (tea.Model, tea.Cmd) {
	m.overlay, m.discoveryOptions, m.discoveryOptionsErr, m.discoveryQuery, m.discoveryTerm = "", nil, "", "", ""
	m.discoveryPending = radioDiscovery{}
	query.Term = strings.TrimSpace(term)
	m.invalidateBrowseCache()
	m.browseQuery = query
	m.view, m.title = "Browse", query.browseTitle()
	m.resetBrowsePaging()
	m.lastView["radio"] = "Browse"
	m.items, m.selected, m.loading, m.listErr = nil, 0, true, ""
	m.filter = ""
	m.detailKind, m.detailID, m.pageClass = "", "", ""
	m.generation++
	return m, m.loadView()
}

func (m Model) cancelDiscovery() (tea.Model, tea.Cmd) {
	m.input.Blur()
	m.overlay, m.inputMode = "", ""
	m.discoveryPending = radioDiscovery{}
	m.discoveryOptions, m.discoveryOptionsErr, m.discoveryQuery, m.discoveryTerm = nil, "", "", ""
	m.discoverySelected = 0
	return m, nil
}

func (m Model) handleDiscoveryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.overlay == "discovery-text" {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.input.Blur()
			m.overlay, m.inputMode = "discovery", ""
			return m, nil
		case "enter":
			m.discoveryTerm = strings.TrimSpace(m.input.Value())
			m.input.Blur()
			m.overlay, m.inputMode = "discovery", ""
			// Land on the action button so committing text is one Enter away
			// from applying the query instead of a hunt through the rows.
			m.discoverySelected = discoveryConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	if m.overlay == "discovery" {
		if m.discoverySelected == discoveryText && len(msg.Text) > 0 {
			m.overlay, m.inputMode = "discovery-text", "discovery-text"
			m.input.Prompt = "Search text: "
			m.input.Placeholder = "optional station name"
			m.input.SetValue(m.discoveryTerm)
			m.input.Focus()
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "esc":
			return m.cancelDiscovery()
		case "tab":
			m.discoverySelected = (m.discoverySelected + 1) % (discoveryCancel + 1)
		case "shift+tab":
			m.discoverySelected = (m.discoverySelected + discoveryCancel) % (discoveryCancel + 1)
		case "up", "k":
			// Wrap like Tab so no field (including the action row) leaves the
			// user on a dead key when they commit text and want Sort next.
			m.discoverySelected = (m.discoverySelected - 1 + discoveryCancel + 1) % (discoveryCancel + 1)
		case "down", "j":
			m.discoverySelected = (m.discoverySelected + 1) % (discoveryCancel + 1)
		case "left", "h":
			if m.discoverySelected == discoverySort {
				m.discoveryPending.Sort = nextRadioSort(m.discoveryPending.Sort, -1)
			} else if m.discoverySelected == discoveryConfirm {
				m.discoverySelected = discoveryCancel
			} else if m.discoverySelected == discoveryCancel {
				m.discoverySelected = discoveryConfirm
			}
		case "right", "l":
			if m.discoverySelected == discoverySort {
				m.discoveryPending.Sort = nextRadioSort(m.discoveryPending.Sort, 1)
			} else if m.discoverySelected == discoveryConfirm {
				m.discoverySelected = discoveryCancel
			} else if m.discoverySelected == discoveryCancel {
				m.discoverySelected = discoveryConfirm
			}
		case "enter":
			switch m.discoverySelected {
			case discoveryText:
				m.overlay, m.inputMode = "discovery-text", "discovery-text"
				m.input.Prompt = "Search text: "
				m.input.Placeholder = "optional station name"
				m.input.SetValue(m.discoveryTerm)
				m.input.Focus()
				return m, textinput.Blink
			case discoveryLanguage:
				m.overlay, m.discoveryKind, m.discoveryOptions, m.discoveryOptionsErr = "discovery-options", "language", nil, ""
				return m, m.loadDiscoveryOptions("language")
			case discoveryGenre:
				m.overlay, m.discoveryKind, m.discoveryOptions, m.discoveryOptionsErr = "discovery-options", "genre", nil, ""
				return m, m.loadDiscoveryOptions("genre")
			case discoveryCountry:
				m.overlay, m.discoveryKind, m.discoveryOptions, m.discoveryOptionsErr = "discovery-options", "country", nil, ""
				return m, m.loadDiscoveryOptions("country")
			case discoverySort:
				m.discoveryPending.Sort = nextRadioSort(m.discoveryPending.Sort, 1)
				return m, nil
			case discoveryReset:
				sortMode := m.discoveryPending.Sort
				m.discoveryPending = radioDiscovery{Sort: sortMode}
				return m, nil
			case discoveryConfirm:
				return m.applyDiscoveryFilter(m.discoveryPending, m.discoveryTerm)
			case discoveryCancel:
				return m.cancelDiscovery()
			}
		}
		return m, nil
	}
	values := m.filteredDiscoveryOptions()
	switch msg.String() {
	case "esc":
		m.overlay, m.discoveryQuery = "discovery", ""
		m.discoverySelected = discoveryFieldIndex(m.discoveryKind)
		return m, nil
	case "up":
		m.discoverySelected = clamp(m.discoverySelected-1, 0, max(0, len(values)-1))
	case "down":
		m.discoverySelected = clamp(m.discoverySelected+1, 0, max(0, len(values)-1))
	case "backspace":
		if runes := []rune(m.discoveryQuery); len(runes) > 0 {
			m.discoveryQuery = string(runes[:len(runes)-1])
			m.discoverySelected = 0
		}
	case "enter":
		if len(values) == 0 {
			return m, nil
		}
		selected := values[clamp(m.discoverySelected, 0, len(values)-1)]
		switch m.discoveryKind {
		case "language":
			m.discoveryPending.Language = selected.ID
		case "genre":
			m.discoveryPending.Tag = selected.ID
		case "country":
			m.discoveryPending.CountryCode = selected.ID
			if selected.ID == "" {
				m.discoveryPending.CountryName = ""
			} else {
				m.discoveryPending.CountryName = selected.Title
			}
		}
		m.overlay, m.discoveryQuery = "discovery", ""
		m.discoverySelected = discoveryFieldIndex(m.discoveryKind)
	default:
		if len(msg.Text) > 0 {
			m.discoveryQuery += msg.Text
			m.discoverySelected = 0
		}
	}
	return m, nil
}

func discoveryFieldIndex(kind string) int {
	switch kind {
	case "language":
		return 1
	case "genre":
		return 2
	case "country":
		return 3
	default:
		return 0
	}
}
