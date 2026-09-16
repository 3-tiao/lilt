// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
)

type Provider interface {
	Search(context.Context, string, int) ([]core.Item, error)
	SearchPlaylists(context.Context, string, int) ([]core.Item, error)
	LibraryPlaylists(context.Context) ([]core.Item, error)
	PlaylistTracks(context.Context, string) ([]core.Item, error)
	RecentPlayed(context.Context, int) ([]core.Item, error)
	Stations(context.Context, string, int) ([]core.Item, error)
}

type RadioProvider interface {
	Countries(context.Context) ([]radio.Country, error)
	Tags(context.Context) ([]radio.Tag, error)
	Languages(context.Context) ([]radio.Language, error)
	Popular(context.Context, radio.Filter, int, int) ([]radio.Station, error)
	SearchFiltered(context.Context, string, radio.Filter, int, int) ([]radio.Station, error)
	StreamName(context.Context, string) string
}

type Player interface {
	core.PlaybackTarget
	core.Authorizer
	SetShuffle(context.Context, bool) (core.PlaybackState, error)
	SetRepeat(context.Context, string) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	Enqueue(context.Context, core.PlaybackRequest, string) (core.PlaybackState, error)
	PlaySongs(context.Context, []string, int) (core.PlaybackState, error)
	QueueJump(context.Context, int) (core.PlaybackState, error)
	QueueRemove(context.Context, int) (core.PlaybackState, error)
	QueueMove(context.Context, int, int) (core.PlaybackState, error)
	QueueClear(context.Context) (core.PlaybackState, error)
	RadioPlay(context.Context, string, string) (core.PlaybackState, error)
	RadioStop(context.Context) (core.PlaybackState, error)
	Probe(context.Context, string, int) (core.RadioProbeResult, error)
	PlayState(context.Context, core.PlaybackRequest) (core.PlaybackState, error)
	PauseState(context.Context) (core.PlaybackState, error)
	ResumeState(context.Context) (core.PlaybackState, error)
	NextState(context.Context) (core.PlaybackState, error)
	PreviousState(context.Context) (core.PlaybackState, error)
}

type listMsg struct {
	generation  uint64
	destination string
	key         string
	title       string
	items       []core.Item
	err         error
	append      bool
	offset      int
	// cached marks the instant first paint served from the last Browse result;
	// silent marks the background refresh that replaces it without resetting
	// the cursor, so re-entering Browse feels instant and still stays current.
	cached bool
	silent bool
}
type homeMsg struct {
	generation  uint64
	destination string
	items       []core.Item
	playlists   []core.Item
}
type pushMsg struct {
	generation  uint64
	destination string
	title       string
	items       []core.Item
	err         error
}
type autoMsg struct {
	generation  uint64
	destination string
	term        string
	items       []core.Item
	err         error
}
type discoveryOptionsMsg struct {
	generation uint64
	kind       string
	values     []core.Item
	err        error
}
type actionMsg struct {
	actionID        uint64
	state           core.PlaybackState
	err             error
	note            string
	afterSequence   uint64
	queueContext    *queueContext
	recentSource    string
	recentItem      *core.Item
	recentContainer *core.Item
	presetKey       string
	presetItem      *core.Item
	addFavorite     bool
	refreshView     bool
}
type stateChangedMsg struct{ update core.PlaybackStateUpdate }
type stateUpdatesClosedMsg struct{}
type tickMsg struct{}
type toastMsg struct{ seq int }

type page struct {
	source, view, title, detailKind, detailID, filter string
	items                                             []core.Item
	selected, listOffset                              int
}

// radioDiscovery is the query that drives the Browse view. It belongs to the
// TUI session, never to persistent user state.
type radioDiscovery struct {
	Language, Tag, CountryCode, CountryName, Term, Sort string
}

const (
	discoveryText = iota
	discoveryLanguage
	discoveryGenre
	discoveryCountry
	discoverySort
	discoveryReset
	discoveryConfirm
	discoveryCancel
)

// radioProbe is the in-process health state for one normalized radio URL.
// Terminal states are cached for the life of the process, except timeouts:
// those become eligible when the user enters a new probe scope.
type radioProbe struct {
	status    string
	latency   int
	code      string
	message   string
	checkedAt time.Time
	persisted bool
}

type probeRequest struct {
	key string
	url string
}

type probeMsg struct {
	key    string
	url    string
	result core.RadioProbeResult
	err    error
}

const (
	// Console geometry shared by layout() and the minimum-size guard so the two
	// can never disagree about what fits.
	consoleHeaderRows = 2
	consoleMinWidth   = 44
	minListRows       = 5
	minDockRows       = 3
	footerBottomRows  = 1

	radioProbeWorkers    = 2
	radioProbeTimeoutMs  = 10000
	radioProbeRPCTimeout = 12 * time.Second
	radioPageSize        = 100
	radioPageThreshold   = 3
)

// queueContext identifies the list that created the current Apple Music queue.
// It is intentionally separate from PlaybackState because MusicKit does not
// consistently expose that source container in its state snapshots.
type queueContext struct {
	Kind, ID, Title string
}

var amViews = []string{"Home", "Playlists", "Favorites", "Recent", "Presets"}
var radioViews = []string{"Favorites", "Recent", "Browse"}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	tabStyle     = lipgloss.NewStyle()
	activeTab    = lipgloss.NewStyle().Bold(true)
	accentStyle  = lipgloss.NewStyle()
	warnStyle    = lipgloss.NewStyle()
	okStyle      = lipgloss.NewStyle()
	errorStyle   = lipgloss.NewStyle()
	selStyle     = lipgloss.NewStyle().Bold(true)
	selInactive  = lipgloss.NewStyle()
	currentStyle = lipgloss.NewStyle().Bold(true)
	trackStyle   = lipgloss.NewStyle().Bold(true)
	rowStyle     = lipgloss.NewStyle()
	dimStyle     = lipgloss.NewStyle().Faint(true)
	loadingStyle = lipgloss.NewStyle()
	// scrollbarStyle matches the panel border so the gutter stays quiet.
	scrollbarStyle = lipgloss.NewStyle()
	borderActive   = lipgloss.Color("81")
	borderIdle     = lipgloss.Color("240")
)

// applyTheme rebuilds the rendering styles from a theme.
func applyTheme(t theme.Theme) {
	onAccent := theme.ActiveForeground(t.Green, t.BG)
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Accent))
	tabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	activeTab = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onAccent)).Background(lipgloss.Color(t.Green))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	okStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Green))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Red))
	selStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	if t.Selection != "" {
		selStyle = selStyle.Background(lipgloss.Color(t.Selection))
	}
	selInactive = lipgloss.NewStyle().Foreground(lipgloss.Color(t.BrightFG))
	trackStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	rowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.BrightFG))
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG)).Faint(true)
	currentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onAccent)).Background(lipgloss.Color(t.Green))
	loadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	scrollbarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	// Borders define structure, not state. Keep both subdued; selection and
	// status text carry the accent so a focused panel never becomes a neon box.
	borderActive = lipgloss.Color(t.FG)
	borderIdle = lipgloss.Color(t.FG)
}

type Options struct {
	Provider       Provider
	Player         Player
	Radio          RadioProvider
	RadioCache     *radio.Cache
	Store          *state.Store
	Authorization  core.AuthorizationStatus
	Presets        []core.Item
	Resolve        func(context.Context, string, *state.Store) (core.Item, error)
	InitialTerm    string
	AutoPlay       bool
	Focus          bool
	Source         string
	Log            func(kind string, fields map[string]any)
	InitialState   *core.PlaybackStateUpdate
	StateUpdates   <-chan core.PlaybackStateUpdate
	StartupWarning string
}

type Model struct {
	provider   Provider
	player     Player
	radio      RadioProvider
	radioCache *radio.Cache
	store      *state.Store
	input      textinput.Model

	source     string
	view       string
	title      string
	items      []core.Item
	selected   int
	listOffset int
	history    []page

	presets []core.Item
	resolve func(context.Context, string, *state.Store) (core.Item, error)

	state         core.PlaybackState
	queueSource   queueContext
	authorization string
	account       string

	width, height int
	loading       bool
	listErr       string
	busy          bool

	message    string
	messageErr bool
	toastSeq   int

	overlay             string
	filter              string
	inputMode           string
	lastView            map[string]string
	cache               map[string][]core.Item
	helpOffset          int
	probes              map[string]radioProbe
	probeQueue          []probeRequest
	probeActive         int
	probeScope          string
	discoveryPending    radioDiscovery
	browseQuery         radioDiscovery
	discoveryOptions    []core.Item
	discoveryOptionsErr string
	discoveryKind       string
	discoverySelected   int
	discoveryQuery      string
	discoveryTerm       string
	pageOffset          int
	pageMore            bool
	pageLoading         bool
	pageFailed          bool
	pageKey             string

	detailKind  string
	detailID    string
	queueFocus  bool
	queueCursor int
	queueIntent string
	queueTarget int

	themeNames []string
	themeIndex int
	themeName  string

	focus        bool
	autoPlay     bool
	sequence     uint64
	stateUpdates <-chan core.PlaybackStateUpdate
	snapshotAt   time.Time
	connected    bool
	generation   uint64
	actionClock  *atomic.Uint64

	log func(kind string, fields map[string]any)
}

// beginAction advances command ownership synchronously, before asynchronous
// work can run. Model copies share the clock, so issuing a newer action makes
// every older result stale even if its completion wins the scheduler race.
func beginAction(clock *atomic.Uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	id := clock.Add(1)
	return func() tea.Msg {
		msg := cmd()
		if action, ok := msg.(actionMsg); ok {
			action.actionID = id
			return action
		}
		return msg
	}
}

func New(opts Options) Model {
	loadedTheme := theme.Load(opts.Store.Theme)
	if opts.RadioCache == nil {
		opts.RadioCache = radio.NewCache("")
	}
	applyTheme(loadedTheme)
	in := textinput.New()
	in.Prompt = "Search: "
	in.Placeholder = "type a query and press Enter"
	in.SetValue(opts.InitialTerm)
	in.Blur()
	source := opts.Source
	if source != "radio" {
		source = "apple-music"
	}
	m := Model{
		provider:      opts.Provider,
		player:        opts.Player,
		radio:         opts.Radio,
		radioCache:    opts.RadioCache,
		store:         opts.Store,
		input:         in,
		source:        source,
		view:          viewsFor(source)[0],
		presets:       opts.Presets,
		resolve:       opts.Resolve,
		authorization: opts.Authorization.Status,
		account:       accountSummary(opts.Authorization),
		autoPlay:      opts.AutoPlay,
		focus:         opts.Focus,
		filter:        "",
		log:           opts.Log,
		lastView:      map[string]string{source: viewsFor(source)[0]},
		cache:         map[string][]core.Item{},
		probes:        map[string]radioProbe{},
		state:         core.PlaybackState{Status: "stopped", Mode: "preview", Authorization: opts.Authorization.Status},
		stateUpdates:  opts.StateUpdates,
		message:       presentation.Text(opts.StartupWarning),
		messageErr:    opts.StartupWarning != "",
		connected:     true,
		actionClock:   &atomic.Uint64{},
	}
	if opts.InitialState != nil {
		m.sequence = opts.InitialState.Sequence
		m.state = presentation.Playback(opts.InitialState.State)
		m.snapshotAt = time.Now()
	}
	if opts.Store.Theme != "" {
		m.themeName = opts.Store.Theme
	} else {
		m.themeName = loadedTheme.Name
	}
	m.title = m.view
	if m.message == "" && m.account == "" {
		m.message = "Apple Music & radio — Tab switches source, / searches"
	}
	if opts.Focus && len(opts.Presets) > 0 {
		m.source = "apple-music"
		m.view = "Presets"
		m.title = "Presets"
		m.items = opts.Presets
		m.lastView["apple-music"] = "Presets"
		m.input.Blur()
	} else {
		m.loading = true
		m.loadLocalView()
	}
	return m
}

func viewsFor(source string) []string {
	if source == "radio" {
		return radioViews
	}
	return amViews
}

// accountSummary returns a one-line explanation when Apple Music is not fully
// usable, and an empty string when everything is ready.
func accountSummary(status core.AuthorizationStatus) string {
	if status.Status != "authorized" {
		switch status.Status {
		case "not_determined":
			return "Account: Apple Music access not granted — preview mode (Tab for Radio)"
		case "denied":
			return "Account: Apple Music access denied — preview mode (Tab for Radio)"
		case "restricted":
			return "Account: Apple Music restricted on this device"
		default:
			return "Account: Apple Music unavailable"
		}
	}
	switch status.AccountStatus {
	case "subscription_required":
		return "Account: an active Apple Music subscription is required for full playback"
	case "cloud_library_disabled":
		return "Account: turn on Sync Library in Music settings to browse personal playlists"
	case "account_unavailable":
		return "Account: unavailable · Open Music and verify that your Apple Account is signed in"
	default:
		return ""
	}
}

func (m Model) Init() tea.Cmd {
	commands := []tea.Cmd{tick()}
	if m.stateUpdates != nil {
		commands = append(commands, waitForStateUpdate(m.stateUpdates))
	}
	if m.focus && len(m.presets) > 0 {
		return tea.Batch(commands...)
	}
	if m.autoPlay && m.input.Value() != "" {
		commands = append(commands, m.autoSearch(m.input.Value()))
		return tea.Batch(commands...)
	}
	if !m.loading {
		return tea.Batch(commands...)
	}
	commands = append(commands, m.loadView())
	return tea.Batch(commands...)
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func waitForStateUpdate(updates <-chan core.PlaybackStateUpdate) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return stateUpdatesClosedMsg{}
		}
		return stateChangedMsg{update: update}
	}
}

// setState records a canonical helper snapshot and when it was received.
func (m Model) setState(playbackState core.PlaybackState) Model {
	m.state = presentation.Playback(playbackState)
	if m.state.Authorization != "" {
		m.authorization = m.state.Authorization
		m.account = accountSummary(core.AuthorizationStatus{Status: m.state.Authorization, AccountStatus: m.state.AccountStatus, AccountError: m.state.AccountError})
	}
	m.snapshotAt = time.Now()
	return m
}

const operationTimeout = 20 * time.Second

func boundedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}

func (m Model) destination() string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", m.source, m.view, m.detailKind, m.detailID, len(m.history))
}

func (m Model) accepts(generation uint64, destination string) bool {
	return generation == m.generation && (destination == "" || destination == m.destination())
}

func stampLoad(cmd tea.Cmd, generation uint64, destination string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch value := msg.(type) {
		case listMsg:
			value.generation, value.destination = generation, destination
			return value
		case homeMsg:
			value.generation, value.destination = generation, destination
			return value
		case pushMsg:
			value.generation, value.destination = generation, destination
			return value
		case autoMsg:
			value.generation, value.destination = generation, destination
			return value
		default:
			return msg
		}
	}
}

// displayPositionAt derives visual progress without changing canonical state.
func (m Model) displayPositionAt(now time.Time) float64 {
	position := m.state.Position
	if m.state.Status != "playing" || m.state.IsLive || m.snapshotAt.IsZero() {
		return position
	}
	position += now.Sub(m.snapshotAt).Seconds()
	if position < 0 || math.IsNaN(position) {
		return 0
	}
	if m.state.Duration > 0 && position > m.state.Duration {
		return m.state.Duration
	}
	return position
}

func (m Model) viewKey() string { return m.source + "/" + m.view }

func (m Model) searchAM(term string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		songs, songErr := m.provider.Search(ctx, term, 20)
		playlists, _ := m.provider.SearchPlaylists(ctx, term, 20)
		if songErr != nil && len(playlists) == 0 {
			return pushMsg{err: songErr}
		}
		return pushMsg{items: grouped(songs, playlists)}
	}
}

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

func radioProbeKey(item core.Item) string { return state.ItemID("radio", item) }

// probeSegment renders the station health marker. Color, symbol, and text are
// all present so a no-color terminal still distinguishes every state.
func (m Model) probeSegment(item core.Item) (string, lipgloss.Style) {
	probe, ok := m.probes[radioProbeKey(item)]
	if !ok {
		return "○ unchecked", dimStyle
	}
	switch probe.status {
	case "queued":
		return "○ queued", dimStyle
	case "checking":
		return "◌ checking…", warnStyle
	case "healthy":
		segment := "● " + formatProbeLatency(probe.latency)
		if probe.persisted {
			segment += " · checked " + formatProbeAge(time.Since(probe.checkedAt))
		}
		return segment, okStyle
	case "failed":
		return "× " + shortProbeError(probe.code), errorStyle
	default:
		return "○ unchecked", dimStyle
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
		for _, item := range m.store.RecentFor("radio") {
			familiar[radioProbeKey(item)] = true
		}
		for _, item := range m.store.FavoritesFor("radio") {
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
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		songs, songErr := m.provider.Search(ctx, term, 20)
		playlists, _ := m.provider.SearchPlaylists(ctx, term, 20)
		if songErr != nil && len(playlists) == 0 {
			return autoMsg{term: term, err: songErr}
		}
		return autoMsg{term: term, items: grouped(songs, playlists)}
	}
}

func grouped(songs, playlists []core.Item) []core.Item {
	items := make([]core.Item, 0, len(songs)+len(playlists)+2)
	if len(songs) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Songs"})
		items = append(items, songs...)
	}
	if len(playlists) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Playlists"})
		items = append(items, playlists...)
	}
	return items
}

func selectable(item core.Item) bool { return item.Kind != "header" }

func (m Model) refreshQueueCursor() Model {
	if len(m.state.Queue) == 0 {
		m.queueFocus, m.queueCursor = false, 0
		m.queueIntent = ""
		return m
	}
	last := len(m.state.Queue) - 1
	switch m.queueIntent {
	case "jump":
		m.queueCursor = clamp(m.state.QueueIndex, 0, last)
	case "remove":
		m.queueCursor = clamp(m.queueTarget, 0, last)
	case "movedown":
		m.queueCursor = clamp(m.queueTarget+1, 0, last)
	case "moveup":
		m.queueCursor = clamp(m.queueTarget-1, 0, last)
	default:
		m.queueCursor = clamp(m.queueCursor, 0, last)
	}
	m.queueIntent = ""
	return m
}

func (m Model) queueCommand(action string) tea.Cmd {
	index := m.queueCursor
	// Log the target so a failed jump can be traced to the exact entry.
	fields := map[string]any{"action": action, "index": index, "queueLength": len(m.state.Queue)}
	if index >= 0 && index < len(m.state.Queue) {
		fields["targetKind"] = m.state.Queue[index].Kind
		fields["targetTitleLength"] = len(m.state.Queue[index].Title)
	}
	m.logEvent("queue", fields)
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		var state core.PlaybackState
		var err error
		var note string
		switch action {
		case "jump":
			state, err = m.player.QueueJump(ctx, index)
		case "remove":
			if index >= 0 && index < len(m.state.Queue) {
				if index == m.state.QueueIndex {
					note = "Removed current track — playback advanced"
				} else {
					note = "Removed: " + m.state.Queue[index].Title
				}
			}
			state, err = m.player.QueueRemove(ctx, index)
		case "movedown":
			note = "Queue reordered"
			state, err = m.player.QueueMove(ctx, index, index+1)
		case "moveup":
			note = "Queue reordered"
			state, err = m.player.QueueMove(ctx, index, index-1)
		}
		return actionMsg{state: state, err: err, note: note, afterSequence: m.sequence}
	})
}

func (m Model) queueClear() tea.Cmd {
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.QueueClear(ctx)
		return actionMsg{state: state, err: err, note: "Queue cleared", afterSequence: m.sequence}
	})
}

func sortByName(items []core.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
}

func firstSelectableIndex(items []core.Item) int {
	for i, item := range items {
		if selectable(item) {
			return i
		}
	}
	return 0
}

func (m Model) loadView() tea.Cmd {
	// Re-entering Browse paints the remembered page first and refreshes it in
	// the background, so the directory round trip no longer gates the view.
	if m.viewKey() == "radio/Browse" {
		if items, ok := m.cache[m.browseCacheKey()]; ok {
			title := m.browseQuery.browseTitle()
			cached := stampLoad(func() tea.Msg {
				return listMsg{key: m.viewKey(), title: title, items: items, cached: true}
			}, m.generation, m.destination())
			refresh := stampLoad(m.silentBrowseFetch(), m.generation, m.destination())
			return tea.Batch(cached, refresh)
		}
	}
	return stampLoad(m.loadViewUnstamped(), m.generation, m.destination())
}

// silentBrowseFetch runs the normal first-page fetch and marks the result as a
// background refresh so it preserves the cursor instead of resetting it.
func (m Model) silentBrowseFetch() tea.Cmd {
	inner := m.loadViewUnstamped()
	return func() tea.Msg {
		msg := inner()
		if value, ok := msg.(listMsg); ok {
			value.silent = true
		}
		return msg
	}
}

func (m Model) loadViewUnstamped() tea.Cmd {
	key := m.viewKey()
	// Apple Music Home combines current state and local history, so never serve a stale page.
	if key != "apple-music/Home" && key != "radio/Browse" {
		if items, ok := m.cache[key]; ok {
			return func() tea.Msg { return listMsg{key: key, title: m.view, items: items} }
		}
	}
	switch {
	case key == "apple-music/Home":
		return m.loadHome()
	case key == "apple-music/Playlists":
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			items, err := m.provider.LibraryPlaylists(ctx)
			sortByName(items)
			return listMsg{key: key, title: "Playlists", items: items, err: err}
		}
	case key == "apple-music/Recent":
		containers := append([]state.RecentContainer(nil), m.store.RecentContainers...)
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			songs, err := m.provider.RecentPlayed(ctx, 50)
			items := make([]core.Item, 0, len(songs)+len(containers)+2)
			shown := 0
			for _, container := range containers {
				if container.Kind != "playlist" {
					continue
				}
				if shown == 0 {
					items = append(items, core.Item{Kind: "header", Title: "Recently Played Lists"})
				}
				id := strings.TrimPrefix(container.ID, container.Kind+":")
				items = append(items, core.Item{Kind: container.Kind, ID: id, Title: container.Title, Artist: "Open details"})
				shown++
				if shown >= 20 {
					break
				}
			}
			if len(songs) > 0 {
				items = append(items, core.Item{Kind: "header", Title: "Recently Played Songs"})
				items = append(items, songs...)
			}
			if err != nil && len(items) == 0 {
				return listMsg{key: key, title: "Recent", err: err}
			}
			return listMsg{key: key, title: "Recent", items: items}
		}
	case key == "apple-music/Presets":
		return func() tea.Msg { return listMsg{key: key, title: "Presets", items: m.presets} }
	case key == "apple-music/Favorites":
		favorites := m.store.FavoritesFor("apple-music")
		return func() tea.Msg {
			return listMsg{key: key, title: "Favorites · local", items: favorites}
		}
	case key == "radio/Favorites":
		favorites := m.store.FavoritesFor("radio")
		return func() tea.Msg {
			return listMsg{key: key, title: "Favorites", items: favorites}
		}
	case key == "radio/Recent":
		recent := m.store.RecentFor("radio")
		return func() tea.Msg {
			return listMsg{key: key, title: "Recent", items: recent}
		}
	case key == "radio/Browse":
		query := m.browseQuery
		title := query.browseTitle()
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			m.logEvent("radio.browse", map[string]any{"offset": 0, "limit": radioPageSize})
			var stations []radio.Station
			var err error
			if query != (radioDiscovery{}) {
				stations, err = m.radio.SearchFiltered(ctx, query.Term, query.filter(), 0, radioPageSize)
			} else {
				stations, err = m.radio.Popular(ctx, radio.Filter{}, radioPageSize, 0)
			}
			if err != nil {
				m.logEvent("radio.error", map[string]any{"action": "browse", "error": err.Error()})
				return listMsg{key: key, title: title, err: err}
			}
			return listMsg{key: key, title: title, items: radio.ToItems(stations)}
		}
	}
	return nil
}

// maybeLoadMore fetches the next Radio Browse page once the cursor approaches
// the end of the unfiltered list. The append is stamped like the first page so
// navigation invalidates it before it can change a new destination.
func (m *Model) maybeLoadMore() tea.Cmd {
	if m.source != "radio" || m.view != "Browse" || m.filter != "" || !m.pageMore || m.pageLoading || m.pageFailed || m.loading || m.busy || m.overlay != "" {
		return nil
	}
	if key := m.browsePageKey(); m.pageKey == "" {
		// A fresh model can enter Browse directly, before any reset ran.
		m.pageKey = key
	} else if key != m.pageKey {
		// The query changed without a first-page load yet; paging state belongs
		// to the previous query and must not leak into the new one.
		m.resetBrowsePaging()
		return nil
	}
	items := m.visibleItems()
	if len(items) == 0 || m.selected < len(items)-radioPageThreshold {
		return nil
	}
	offset, query, title := m.pageOffset, m.browseQuery, m.title
	m.pageLoading = true
	m.logEvent("radio.browse", map[string]any{"offset": offset, "limit": radioPageSize})
	cmd := func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		var stations []radio.Station
		var err error
		if query != (radioDiscovery{}) {
			stations, err = m.radio.SearchFiltered(ctx, query.Term, query.filter(), offset, radioPageSize)
		} else {
			stations, err = m.radio.Popular(ctx, radio.Filter{}, radioPageSize, offset)
		}
		if err != nil {
			m.logEvent("radio.error", map[string]any{"action": "browse", "offset": offset, "error": err.Error()})
			return listMsg{key: "radio/Browse", title: title, append: true, offset: offset, err: err}
		}
		return listMsg{key: "radio/Browse", title: title, items: radio.ToItems(stations), append: true, offset: offset}
	}
	return stampLoad(cmd, m.generation, m.destination())
}

func activeAppleQueue(playback core.PlaybackState) bool {
	return !playback.IsLive && playback.Status != "" && playback.Status != "stopped" && playback.Status != "none" && len(playback.Queue) > 0
}

func homeItems(playback core.PlaybackState, queueSource string, recent, playlists, presets []core.Item, containers []state.RecentContainer) []core.Item {
	const sectionLimit = 8
	items := make([]core.Item, 0, 1+len(recent)+len(playlists)+len(presets)+len(containers)+4)
	if activeAppleQueue(playback) {
		source := queueSource
		if source == "" {
			source = "Queue"
		}
		title := "Continue Playing: " + source
		if playback.Track != nil && playback.Track.Title != "" {
			title += " — " + playback.Track.Title
		}
		artist := fmt.Sprintf("%d/%d · opens Up Next", playback.QueueIndex+1, len(playback.Queue))
		items = append(items, core.Item{Kind: "continue", Title: title, Artist: artist})
	}
	if len(items) > 0 {
		items = append([]core.Item{{Kind: "header", Title: "Continue Playing"}}, items...)
	}
	recentItems := make([]core.Item, 0, len(containers)+len(recent))
	for _, container := range containers {
		if container.Kind != "playlist" {
			continue
		}
		if len(recentItems) >= sectionLimit {
			break
		}
		id := strings.TrimPrefix(container.ID, container.Kind+":")
		recentItems = append(recentItems, core.Item{Kind: container.Kind, ID: id, Title: container.Title, Artist: "Open details"})
	}
	remaining := sectionLimit - len(recentItems)
	if len(recent) > remaining {
		recent = recent[:remaining]
	}
	recentItems = append(recentItems, recent...)
	if len(recentItems) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Recently Played"})
		items = append(items, recentItems...)
	}
	if len(presets) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Quick Start"})
		if len(presets) > sectionLimit {
			presets = presets[:sectionLimit]
		}
		items = append(items, presets...)
	}
	if len(playlists) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Your Playlists"})
		if len(playlists) > sectionLimit {
			playlists = playlists[:sectionLimit]
		}
		items = append(items, playlists...)
	}
	return items
}

func (m Model) loadHome() tea.Cmd {
	playlists := append([]core.Item(nil), m.cache["apple-music/Playlists"]...)
	presets := append([]core.Item(nil), m.presets...)
	containers := append([]state.RecentContainer(nil), m.store.RecentContainers...)
	playback, queueTitle := m.state, m.queueSource.Title
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		// Recent playback is optional: Home remains useful without a Music User Token.
		recent, _ := m.provider.RecentPlayed(ctx, 8)
		if len(playlists) == 0 {
			playlists, _ = m.provider.LibraryPlaylists(ctx)
			sortByName(playlists)
		}
		return homeMsg{items: homeItems(playback, queueTitle, recent, playlists, presets, containers), playlists: playlists}
	}
}

func (m Model) openPlaylist(item core.Item) tea.Cmd {
	id := item.ID
	title := item.Title
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		tracks, err := m.provider.PlaylistTracks(ctx, id)
		if err == nil && reversePlaylistOrder(title) {
			// Providers may return a cached slice; reverse a copy instead of it.
			tracks = append([]core.Item(nil), tracks...)
			for left, right := 0, len(tracks)-1; left < right; left, right = left+1, right-1 {
				tracks[left], tracks[right] = tracks[right], tracks[left]
			}
		}
		return pushMsg{title: title, items: tracks, err: err}
	}
}

// reversePlaylistOrder identifies Apple's Favorite Songs mix by localized name.
// MusicKit provides no playlist type marker, so this is name-based best effort.
func reversePlaylistOrder(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "喜爱歌曲", "喜愛歌曲", "favorite songs", "favourite songs":
		return true
	default:
		return false
	}
}

func (m *Model) playItem(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": item.Kind, "titleLength": len(item.Title)})
	switch {
	case item.Kind == "stream":
		note := ""
		if probe, ok := m.probes[radioProbeKey(item)]; ok && probe.status == "failed" {
			note = "Retrying " + item.Title + " — earlier probe failed (" + shortProbeError(probe.code) + ")"
			delete(m.probes, radioProbeKey(item))
			m.radioCache.DeleteHealth(item.URL)
			if err := m.radioCache.Save(); err != nil {
				m.logEvent("probe", map[string]any{"event": "clear_failed", "error": err.Error()})
			}
		}
		return beginAction(m.actionClock, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			playback, err := m.player.RadioPlay(ctx, item.URL, item.Title)
			return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: "radio", recentItem: &item}
		})
	case item.Kind == "preset":
		return m.playPreset(item)
	default:
		source := m.source
		queueCtx := &queueContext{}
		if item.Kind == "playlist" {
			queueCtx = &queueContext{Kind: "playlist", ID: item.ID, Title: item.Title}
		}
		return beginAction(m.actionClock, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			request := core.PlaybackRequest{Kind: item.Kind, ID: item.ID, URL: item.URL}
			playback, err := m.player.PlayState(ctx, request)
			return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: queueCtx, recentSource: source, recentItem: &item}
		})
	}
}

func (m Model) playPreset(item core.Item) tea.Cmd {
	ranking := m.store.Snapshot()
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		resolved := item
		if item.Kind == "preset" && m.resolve != nil {
			value, err := m.resolve(ctx, item.ID, ranking)
			if err != nil {
				return actionMsg{err: err, afterSequence: m.sequence}
			}
			resolved = value
		}
		playback, err := m.player.PlayState(ctx, core.PlaybackRequest{Kind: resolved.Kind, ID: resolved.ID, URL: resolved.URL})
		queueSource := &queueContext{}
		if resolved.Kind == "playlist" {
			queueSource = &queueContext{Kind: "playlist", ID: resolved.ID, Title: resolved.Title}
		}
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: queueSource, recentSource: m.source, recentItem: &resolved, presetKey: item.ID, presetItem: &resolved}
	})
}

func (m *Model) playSelected() tea.Cmd {
	item, ok := m.selectedItem()
	if !ok || !selectable(item) {
		return nil
	}
	return m.playItem(item)
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	item, ok := m.selectedItem()
	if !ok || !selectable(item) {
		return m, nil
	}
	switch item.Kind {
	case "continue":
		if !activeAppleQueue(m.state) {
			return m, m.loadView()
		}
		m.queueFocus = true
		m.queueCursor = m.state.QueueIndex
		return m, nil
	case "playlist":
		if m.source == "apple-music" {
			next, cmd := m.push(item.Title, m.openPlaylist(item))
			child := next.(Model)
			child.detailKind, child.detailID = "playlist", item.ID
			return child, stampLoad(cmd, child.generation, child.destination())
		}
	case "browse":
		for index, view := range viewsFor(m.source) {
			if view == item.ID {
				return m.selectView(index)
			}
		}
		return m, nil
	case "song":
		if m.detailKind == "playlist" && m.detailID != "" {
			m.busy = true
			return m, m.playPlaylistFrom(item)
		}
	}
	m.busy = true
	return m, m.playSelected()
}

func (m Model) playPlaylistFrom(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": "playlistFrom", "titleLength": len(item.Title)})
	container := core.Item{Kind: "playlist", ID: m.detailID, Title: m.title}
	startAt := m.selectedOriginalIndex()
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		playback, err := m.player.PlayState(ctx, core.PlaybackRequest{Kind: "playlist", ID: m.detailID, StartAt: startAt, StartTrackID: item.ID, StartTitle: item.Title, Reverse: reversePlaylistOrder(m.title)})
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{Kind: "playlist", ID: m.detailID, Title: m.title}, recentContainer: &container}
	})
}

func (m Model) playPlaylist(shuffle bool) tea.Cmd {
	title := m.title
	m.logEvent("play", map[string]any{"itemKind": "playlist", "titleLength": len(title), "shuffle": shuffle})
	container := core.Item{Kind: "playlist", ID: m.detailID, Title: title}
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		if _, err := m.player.SetShuffle(ctx, shuffle); err != nil {
			return actionMsg{err: err, afterSequence: m.sequence}
		}
		playback, err := m.player.PlayState(ctx, core.PlaybackRequest{Kind: "playlist", ID: m.detailID, Reverse: reversePlaylistOrder(title)})
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{Kind: "playlist", ID: m.detailID, Title: title}, recentContainer: &container}
	})
}

// push optimistically opens a child page and shows the loading state.
func (m Model) push(title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.history = append(m.history, page{source: m.source, view: m.view, title: m.title, detailKind: m.detailKind, detailID: m.detailID, filter: m.filter, items: m.items, selected: m.selected, listOffset: m.listOffset})
	m.title = presentation.Text(title)
	m.items = nil
	m.selected, m.listOffset = 0, 0
	m.filter = ""
	m.loading = true
	m.listErr = ""
	m.generation++
	return m, stampLoad(cmd, m.generation, m.destination())
}

// Row emphasis kinds. Playing outranks selection so the playing row keeps one
// fixed appearance; selection is already carried by the left cursor, and
// recoloring the row when it becomes selected would make one state look like two.
const (
	rowNormal = iota
	rowSelected
	rowPlaying
)

func listRowKind(selected, playing bool) int {
	switch {
	case playing:
		return rowPlaying
	case selected:
		return rowSelected
	default:
		return rowNormal
	}
}

// isPlayingItem reports whether a list row is the item currently playing, so it
// can be highlighted. The distinction is style-only: adding a text prefix would
// duplicate what the highlight already says and add noise to every row.
func (m Model) isPlayingItem(item core.Item) bool {
	if m.state.Track == nil {
		return false
	}
	if m.source == "apple-music" && m.detailKind == "playlist" &&
		m.queueSource.Kind == "playlist" && m.queueSource.ID == m.detailID {
		return item.ID != "" && item.ID == m.state.Track.ID
	}
	if item.Kind == "stream" || item.Kind == "station" {
		return samePlayingTrack(*m.state.Track, item)
	}
	return false
}

// samePlayingTrack reports whether a selected item is the item currently
// playing, so p can act as an intuitive pause/resume toggle on it.
func samePlayingTrack(current, selected core.Item) bool {
	if current.Kind == "stream" || selected.Kind == "stream" {
		return current.URL != "" && current.URL == selected.URL
	}
	return current.ID != "" && current.ID == selected.ID
}

func (m Model) control(kind string) tea.Cmd {
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		var state core.PlaybackState
		var err error
		switch kind {
		case "pause":
			state, err = m.player.PauseState(ctx)
		case "resume":
			state, err = m.player.ResumeState(ctx)
		case "next":
			state, err = m.player.NextState(ctx)
		case "previous":
			state, err = m.player.PreviousState(ctx)
		}
		return actionMsg{state: state, err: err, afterSequence: m.sequence}
	})
}

func (m Model) toggleShuffle() tea.Cmd {
	on := !m.state.Shuffle
	note := "Shuffle off"
	if on {
		note = "Shuffle on"
	}
	m.logEvent("control", map[string]any{"action": "shuffle", "on": on})
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.SetShuffle(ctx, on)
		return actionMsg{state: state, err: err, note: note, afterSequence: m.sequence}
	})
}

func (m Model) cycleRepeat() tea.Cmd {
	order := []string{"off", "all", "one"}
	index := 0
	for i, value := range order {
		if value == m.state.Repeat {
			index = i
		}
	}
	mode := order[(index+1)%len(order)]
	m.logEvent("control", map[string]any{"action": "repeat", "mode": mode})
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.SetRepeat(ctx, mode)
		return actionMsg{state: state, err: err, note: "Repeat " + mode, afterSequence: m.sequence}
	})
}

func (m Model) stopPlayback() tea.Cmd {
	m.logEvent("control", map[string]any{"action": "stop"})
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.Stop(ctx)
		return actionMsg{state: state, err: err, note: "Stopped", afterSequence: m.sequence}
	})
}

func (m Model) enqueueSelected(position string) tea.Cmd {
	item, ok := m.selectedItem()
	if !ok || !selectable(item) {
		return nil
	}
	label := "Added to queue"
	ranking := m.store.Snapshot()
	if position == "next" {
		label = "Playing next"
	}
	return beginAction(m.actionClock, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		resolved := item
		if item.Kind == "preset" && m.resolve != nil {
			value, err := m.resolve(ctx, item.ID, ranking)
			if err != nil {
				return actionMsg{err: err, afterSequence: m.sequence}
			}
			resolved = value
		}
		state, err := m.player.Enqueue(ctx, core.PlaybackRequest{Kind: resolved.Kind, ID: resolved.ID, URL: resolved.URL}, position)
		if err != nil {
			return actionMsg{err: err, afterSequence: m.sequence}
		}
		return actionMsg{state: state, note: label + ": " + resolved.Title, afterSequence: m.sequence}
	})
}

func (m Model) withToast(text string, isErr bool) (Model, tea.Cmd) {
	m.message = presentation.Text(text)
	m.messageErr = isErr
	m.toastSeq++
	seq := m.toastSeq
	return m, tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastMsg{seq} })
}

func (m Model) moveBy(delta int) Model {
	items := m.visibleItems()
	if len(items) == 0 {
		return m
	}
	start := clamp(m.selected, 0, len(items)-1)
	step := 1
	if delta < 0 {
		step = -1
	}
	target := start
	for n := 0; n < abs(delta); n++ {
		next := target + step
		if next < 0 || next >= len(items) {
			break
		}
		target = next
	}
	if !selectable(items[target]) {
		found := -1
		for i := target; i >= 0 && i < len(items); i += step {
			if selectable(items[i]) {
				found = i
				break
			}
		}
		if found < 0 {
			target = start
		} else {
			target = found
		}
	}
	m.selected = target
	return m.keepMainSelectionVisible()
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func lastSelectableIndex(items []core.Item) int {
	for i := len(items) - 1; i >= 0; i-- {
		if selectable(items[i]) {
			return i
		}
	}
	return 0
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (m Model) switchSource(source string) (tea.Model, tea.Cmd) {
	if source == m.source {
		return m, nil
	}
	if m.lastView == nil {
		m.lastView = map[string]string{}
	}
	m.lastView[m.source] = m.view
	m.source = source
	view := viewsFor(source)[0]
	if source != "radio" {
		view = m.lastView[source]
		if !contains(viewsFor(source), view) {
			view = viewsFor(source)[0]
		}
	}
	m.view = view
	m.title = view
	m.lastView[source] = view
	m.history = nil
	m.filter = ""
	m.items, m.listErr = nil, ""
	m.selected, m.listOffset = 0, 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	if m.source == "radio" && m.view == "Browse" {
		m.resetBrowsePaging()
	}
	m.generation++
	local := m.loadLocalView()
	if err := m.store.UpdateAndSave(func(next *state.Store) { next.LastSource = source }); err != nil {
		m.message, m.messageErr = "State save failed: "+presentation.Text(err.Error()), true
	}
	m.logEvent("navigate", map[string]any{"action": "source"})
	if local {
		return m, nil
	}
	return m, m.loadView()
}

// isTimeoutError distinguishes a slow directory (our own timeout budget
// expired) from an actually unreachable one, so the message does not blame the
// user's connection for a server that is merely slow.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// reloadView re-runs the current view's loader, ignoring session caches that
// would otherwise return the same stale page. It is the manual retry path for
// directory errors and the `r` key.
func (m Model) reloadView() (tea.Model, tea.Cmd) {
	if m.loading || m.busy {
		return m, nil
	}
	if m.cache != nil {
		delete(m.cache, m.viewKey())
	}
	m.invalidateBrowseCache()
	m.listErr = ""
	m.pageFailed = false
	m.message, m.messageErr = "", false
	m.loading = true
	m.generation++
	m.logEvent("navigate", map[string]any{"action": "reload"})
	return m, m.loadView()
}

func (m Model) selectView(index int) (tea.Model, tea.Cmd) {
	views := viewsFor(m.source)
	if index < 0 || index >= len(views) {
		return m, nil
	}
	if views[index] == m.view && len(m.history) == 0 {
		// Re-selecting the current view is normally a no-op, but it must retry
		// when that view is showing a load error: otherwise the error message
		// tells the user to press a key that does nothing.
		if m.listErr == "" && !m.pageFailed {
			return m, nil
		}
		return m.reloadView()
	}
	m.view = views[index]
	m.title = m.view
	m.lastView[m.source] = m.view
	m.history = nil
	m.filter = ""
	m.items, m.listErr = nil, ""
	m.selected, m.listOffset = 0, 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	if m.source == "radio" && m.view == "Browse" {
		m.resetBrowsePaging()
	}
	m.generation++
	local := m.loadLocalView()
	m.logEvent("navigate", map[string]any{"action": "view"})
	if local {
		return m, nil
	}
	return m, m.loadView()
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (m Model) cycleView(delta int) (tea.Model, tea.Cmd) {
	views := viewsFor(m.source)
	index := 0
	for i, view := range views {
		if view == m.view {
			index = i
		}
	}
	index = (index + delta + len(views)) % len(views)
	m.view = views[index]
	m.title = m.view
	m.lastView[m.source] = m.view
	m.history = nil
	m.filter = ""
	m.items, m.listErr = nil, ""
	m.selected, m.listOffset = 0, 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	if m.source == "radio" && m.view == "Browse" {
		m.resetBrowsePaging()
	}
	m.generation++
	local := m.loadLocalView()
	m.logEvent("navigate", map[string]any{"action": "cycle"})
	if local {
		return m, nil
	}
	return m, m.loadView()
}

func (m Model) back() Model {
	if len(m.history) == 0 {
		return m
	}
	previous := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	m.source, m.view, m.title = previous.source, previous.view, previous.title
	m.items = previous.items
	m.selected, m.listOffset = previous.selected, previous.listOffset
	m.filter = previous.filter
	m.detailKind, m.detailID = previous.detailKind, previous.detailID
	m.loading = false
	m.listErr = ""
	m.generation++
	return m
}

// loadLocalView avoids a transient loading frame for views backed entirely by
// state already held in this process.
func (m *Model) loadLocalView() bool {
	var items []core.Item
	switch m.viewKey() {
	case "apple-music/Presets":
		items = m.presets
	case "apple-music/Favorites":
		items = m.store.FavoritesFor("apple-music")
		m.title = "Favorites · local"
	case "radio/Favorites":
		items = m.store.FavoritesFor("radio")
	case "radio/Recent":
		items = m.store.RecentFor("radio")
	default:
		return false
	}
	m.items = presentation.Items(items)
	m.selected = firstSelectableIndex(m.items)
	m.loading = false
	return true
}

func (m Model) visibleItems() []core.Item {
	if m.filter == "" {
		return m.items
	}
	needle := strings.ToLower(m.filter)
	filtered := make([]core.Item, 0, len(m.items))
	for _, item := range m.items {
		if item.Kind == "header" {
			continue
		}
		if strings.Contains(strings.ToLower(item.Title+" "+item.Artist), needle) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (m Model) selectedItem() (core.Item, bool) {
	items := m.visibleItems()
	if len(items) == 0 {
		return core.Item{}, false
	}
	index := clamp(m.selected, 0, len(items)-1)
	return items[index], true
}

// selectedOriginalIndex maps a filtered cursor back to the stable ordering of
// the unfiltered detail page. Playlist startAt is defined in that full order.
func (m Model) selectedOriginalIndex() int {
	if len(m.items) == 0 {
		return 0
	}
	if m.filter == "" {
		return clamp(m.selected, 0, len(m.items)-1)
	}
	needle := strings.ToLower(m.filter)
	want := clamp(m.selected, 0, max(0, len(m.visibleItems())-1))
	visible := 0
	for index, item := range m.items {
		if item.Kind == "header" || !strings.Contains(strings.ToLower(item.Title+" "+item.Artist), needle) {
			continue
		}
		if visible == want {
			return index
		}
		visible++
	}
	return 0
}

func (m Model) logEvent(kind string, fields map[string]any) {
	if m.log == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["source"] = m.source
	fields["view"] = m.view
	if item, ok := m.selectedItem(); ok {
		fields["selectedKind"] = item.Kind
		fields["selectedTitleLength"] = len(item.Title)
	}
	m.log(kind, fields)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.keepMainSelectionVisible()
		next, cmd := m.scheduleProbes()
		return next, cmd
	case listMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		if msg.err == nil {
			msg.items = presentation.Items(msg.items)
		}
		if msg.err == nil && m.radioCache.RememberItems(msg.items, time.Now()) {
			if err := m.radioCache.Save(); err != nil {
				m.logEvent("radio.cache", map[string]any{"event": "save_failed", "error": err.Error()})
			}
		}
		if msg.append {
			m.pageLoading = false
			if msg.err != nil {
				m.pageFailed = true
				m.message, m.messageErr = "Couldn't load more stations — press G to retry", true
				m.toastSeq++
				seq := m.toastSeq
				return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastMsg{seq} })
			}
			selectedKey := ""
			if item, ok := m.selectedItem(); ok {
				selectedKey = radioProbeKey(item)
			}
			seen := make(map[string]struct{}, len(m.items))
			for _, item := range m.items {
				seen[state.ItemID("radio", item)] = struct{}{}
			}
			for _, item := range presentation.Items(msg.items) {
				key := state.ItemID("radio", item)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				m.items = append(m.items, item)
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
			// The directory hides broken and duplicate entries, so a short page
			// does not imply the end. Only an empty page exhausts the query.
			m.pageOffset += radioPageSize
			m.pageMore = len(msg.items) > 0
			m.pageFailed = false
			next, probeCmd := m.scheduleProbes()
			return next, probeCmd
		}
		if msg.cached {
			return m.applyCachedList(msg)
		}
		if msg.silent {
			return m.applySilentList(msg)
		}
		m.loading = false
		if msg.err != nil {
			if m.source == "radio" {
				// The directory has multi-second latency, so a timeout usually means
				// "slow directory", not a broken connection. Say which one happened
				// and point at a key that actually retries.
				if isTimeoutError(msg.err) {
					m.listErr = "Radio directory timed out"
					m.message = "Radio directory timed out — press r to retry"
				} else {
					m.listErr = "Radio directory unavailable"
					m.message = "Radio directory unavailable — press r to retry"
				}
				m.messageErr = true
				if len(m.items) == 0 {
					if cached := m.radioCache.StationItems(time.Now()); len(cached) > 0 {
						m.items = presentation.Items(cached)
						if m.viewKey() == "radio/Browse" {
							m.items = m.sortRadioItems(m.items)
							m.pageMore, m.pageOffset, m.pageKey = false, 0, m.browsePageKey()
						}
						m.listErr = ""
						m.message = "Radio directory unavailable — showing " + fmt.Sprint(len(m.items)) + " cached stations · press r to retry"
					}
				}
				return m.scheduleProbes()
			}
			m.listErr = "Unable to load list: " + presentation.Text(msg.err.Error())
			m.messageErr = true
			m.message = "Error: " + presentation.Text(msg.err.Error())
			return m, nil
		}
		m.listErr = ""
		if m.cache != nil && msg.key != "radio/Browse" {
			m.cache[msg.key] = presentation.Items(msg.items)
		}
		if msg.key == m.viewKey() {
			m.title = msg.title
			m.items = presentation.Items(msg.items)
			if msg.key == "radio/Browse" {
				m.items = m.sortRadioItems(m.items)
			}
			m.selected = 0
			m.filter = ""
			if msg.key == "radio/Browse" {
				// A fixed stride keeps the next request aligned with the page
				// size even when hidden or duplicate entries shorten a page.
				m.pageOffset = radioPageSize
				m.pageMore = len(msg.items) > 0
				m.pageLoading = false
				m.pageFailed = false
				m.pageKey = m.browsePageKey()
				m.rememberBrowsePage()
			}
		}
		next, probeCmd := m.scheduleProbes()
		return next, tea.Batch(probeCmd, next.maybeLoadMore())
	case homeMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
		m.listErr = ""
		if m.cache != nil && msg.playlists != nil {
			m.cache["apple-music/Playlists"] = presentation.Items(msg.playlists)
		}
		if m.source == "apple-music" && m.view == "Home" && len(m.history) == 0 {
			m.title = "Home"
			m.items = presentation.Items(msg.items)
			m.selected = firstSelectableIndex(msg.items)
			m.filter = ""
		}
	case pushMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
		m.listErr = ""
		if msg.err != nil {
			m = m.back()
			m.message = "Error: " + presentation.Text(msg.err.Error())
			m.messageErr = true
			return m, nil
		}
		m.items = presentation.Items(msg.items)
		m.selected = firstSelectableIndex(msg.items)
		m.filter = ""
	case autoMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
		m.listErr = ""
		if msg.err != nil {
			m.listErr = "Unable to load list: " + presentation.Text(msg.err.Error())
			m.message = "Search error: " + presentation.Text(msg.err.Error())
			m.messageErr = true
			return m, nil
		}
		m.source = "apple-music"
		m.title = "Search: " + presentation.Text(msg.term)
		m.items = presentation.Items(msg.items)
		m.selected = firstSelectableIndex(msg.items)
		m.filter = ""
		if len(msg.items) > 0 {
			m.busy = true
			return m, m.playSelected()
		}
	case discoveryOptionsMsg:
		if msg.generation != m.generation || m.overlay != "discovery-options" || msg.kind != m.discoveryKind {
			return m, nil
		}
		if msg.err != nil {
			m.message, m.messageErr = "Browse options: "+presentation.Text(msg.err.Error()), true
			m.discoveryOptions = []core.Item{}
			m.discoveryOptionsErr = presentation.Text(msg.err.Error())
			return m, nil
		}
		m.discoveryOptions, m.discoveryOptionsErr, m.discoverySelected = msg.values, "", 0
	case actionMsg:
		if msg.actionID != 0 && (m.actionClock == nil || msg.actionID != m.actionClock.Load()) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.message = "Playback error: " + presentation.Text(msg.err.Error())
			m.messageErr = true
			m.toastSeq++
			seq := m.toastSeq
			next, probeCmd := m.scheduleProbes()
			return next, tea.Batch(tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastMsg{seq} }), probeCmd, next.maybeLoadMore())
		}
		// A notification newer than the command's starting snapshot makes the
		// command's state snapshot stale, but never its completed side effects:
		// for live streams the helper publishes a stateChanged notification that
		// races ahead of this response, and dropping metadata here would lose
		// favorites and recents for plays that actually succeeded.
		if msg.afterSequence >= m.sequence {
			if msg.queueContext != nil {
				m.queueSource = *msg.queueContext
			}
			m = m.setState(msg.state)
			if m.state.Mode == "none" || m.state.IsLive || m.state.Status == "stopped" {
				m.queueSource = queueContext{}
			}
			m = m.refreshQueueCursor()
		}
		if msg.recentItem != nil || msg.recentContainer != nil || msg.presetKey != "" || msg.addFavorite {
			if err := m.store.UpdateAndSave(func(next *state.Store) {
				if msg.recentItem != nil {
					next.AddRecent(msg.recentSource, presentation.Item(*msg.recentItem))
				}
				if msg.recentContainer != nil {
					next.AddRecentContainer(presentation.Item(*msg.recentContainer))
				}
				if msg.presetKey != "" && msg.presetItem != nil {
					next.Record(msg.presetKey, presentation.Item(*msg.presetItem))
				}
				if msg.addFavorite && msg.recentItem != nil && !next.IsFavorite("radio", state.ItemID("radio", *msg.recentItem)) {
					next.ToggleFavorite("radio", presentation.Item(*msg.recentItem))
				}
			}); err != nil {
				m.message, m.messageErr = "Playback started, but state save failed: "+presentation.Text(err.Error()), true
				return m, nil
			}
		}
		var refresh tea.Cmd
		if msg.refreshView {
			m.cache = map[string][]core.Item{}
			m.loading = true
			m.listErr = ""
			refresh = m.loadView()
		}
		m.messageErr = false
		// A finished action releases the busy pause, so probes paused while it
		// was in flight must resume without waiting for the next key press.
		next, probeCmd := m.scheduleProbes()
		if msg.note != "" {
			next, toastCmd := next.withToast(msg.note, false)
			return next, tea.Batch(toastCmd, refresh, probeCmd, next.maybeLoadMore())
		}
		next.message = ""
		return next, tea.Batch(refresh, probeCmd, next.maybeLoadMore())
	case toastMsg:
		if msg.seq == m.toastSeq {
			m.message = ""
		}
	case tickMsg:
		return m, tick()
	case stateChangedMsg:
		if msg.update.Sequence > m.sequence {
			m.sequence = msg.update.Sequence
			m = m.setState(msg.update.State)
			m.connected = true
			m.authorization = m.state.Authorization
			m.account = accountSummary(core.AuthorizationStatus{Status: m.state.Authorization, AccountStatus: m.state.AccountStatus, AccountError: m.state.AccountError})
			if m.state.Mode == "none" || m.state.IsLive || m.state.Status == "stopped" {
				m.queueSource = queueContext{}
			}
			m = m.refreshQueueCursor()
		}
		return m, waitForStateUpdate(m.stateUpdates)
	case stateUpdatesClosedMsg:
		m.stateUpdates = nil
		m.connected = false
		m.snapshotAt = time.Time{}
		if m.state.Status == "playing" || m.state.Status == "buffering" {
			m.state.Status = "disconnected"
		}
		m = m.clearProbesOnDisconnect()
		m.message = "Playback helper disconnected — quit and restart lilt to reconnect"
		m.messageErr = true
	case probeMsg:
		if m.probes == nil {
			m.probes = map[string]radioProbe{}
		}
		m.probeActive = max(0, m.probeActive-1)
		checkedAt := time.Now()
		status, latency, code, message := "failed", 0, "", ""
		switch {
		case msg.err != nil:
			code, message = "transport", "probe unavailable"
		case msg.result.Status == "healthy":
			status, latency = "healthy", msg.result.LatencyMs
		default:
			code, message = msg.result.ErrorCode, msg.result.Message
		}
		m.probes[msg.key] = radioProbe{status: status, latency: latency, code: code, message: message, checkedAt: checkedAt}
		m.radioCache.RecordHealth(msg.url, status, latency, code, checkedAt)
		if err := m.radioCache.Save(); err != nil {
			m.logEvent("probe", map[string]any{"event": "save_failed", "error": err.Error()})
		}
		m.logEvent("probe", map[string]any{"event": "done", "status": msg.result.Status, "err": msg.err != nil, "queue": len(m.probeQueue), "active": m.probeActive})
		next, cmd := m.pumpProbes()
		return next, cmd
	case tea.MouseMsg:
		next, cmd := m.handleMouse(msg)
		model := next.(Model)
		updated, probeCmd := model.scheduleProbes()
		moreCmd := updated.maybeLoadMore()
		if probeCmd == nil && moreCmd == nil {
			return updated, cmd
		}
		return updated, tea.Batch(cmd, probeCmd, moreCmd)
	case tea.PasteMsg:
		// Bracketed paste is its own message in v2. Forward it to the focused
		// editor instead of expanding it as unrelated single-key presses.
		if m.input.Focused() {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		// Fast typing and key auto-repeat can deliver several runes in one
		// event ("jjj"). Lists only understand single-key events, so without
		// expanding them the whole burst is silently dropped.
		if len(msg.Text) > 1 && !m.acceptsTextEntry() {
			var model tea.Model = m
			var cmd tea.Cmd
			// Bound the expansion so an unexpected unbracketed bulk write cannot
			// stall the event loop.
			runes := []rune(msg.Text)
			if len(runes) > 32 {
				runes = runes[:32]
			}
			for _, r := range runes {
				next, follow := model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				model, cmd = next, tea.Batch(cmd, follow)
			}
			return model, cmd
		}
		next, cmd := m.handleKey(msg)
		model := next.(Model)
		if model.pageFailed && (msg.String() == "G" || msg.String() == "ctrl+d" || msg.String() == "ctrl+f") {
			model.pageFailed = false
		}
		updated, probeCmd := model.scheduleProbes()
		moreCmd := updated.maybeLoadMore()
		if probeCmd == nil && moreCmd == nil {
			return updated, cmd
		}
		return updated, tea.Batch(cmd, probeCmd, moreCmd)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// acceptsTextEntry identifies inputs where a single KeyMsg may legitimately
// contain a complete pasted or programmatically supplied value. Command-list
// key bursts are expanded above; URLs and search text must remain intact.
func (m Model) acceptsTextEntry() bool {
	return m.input.Focused() || m.overlay == "discovery" || m.overlay == "discovery-text" || m.overlay == "discovery-options"
}

// sourceTabAt maps an x coordinate on the SOURCE row to a source name.
func sourceTabAt(x int) (string, bool) {
	start := lipgloss.Width("lilt") + 2 + lipgloss.Width("SOURCE") + 2
	for _, source := range []string{"apple-music", "radio"} {
		width := lipgloss.Width(" " + sourceTitle(source) + " ")
		if x >= start && x < start+width {
			return source, true
		}
		start += width + 2
	}
	return "", false
}

// viewTabAt maps an x coordinate on the VIEW row to a sub-view index.
func (m Model) viewTabAt(x int) (int, bool) {
	start := lipgloss.Width("VIEW  ")
	for i, view := range viewsFor(m.source) {
		width := lipgloss.Width(fmt.Sprintf(" %d %s ", i+1, view))
		if x >= start && x < start+width {
			return i, true
		}
		start += width + 1
	}
	return 0, false
}

// dockTop is the first terminal row of the playback dock. When dockGap is set,
// a blank spacer separates the list box from the dock, and hit tests that forget
// it select the queue entry one row away from the pointer.
func (l layout) dockTop() int { return l.listTop + l.listHeight + l.dockGap }

// selectQueueRow focuses one visible queue entry and jumps on a second click.
func (m Model) selectQueueRow(row, rows int) (tea.Model, tea.Cmd) {
	if len(m.state.Queue) == 0 || row < 0 {
		return m, nil
	}
	anchor := m.state.QueueIndex
	if m.queueFocus {
		anchor = m.queueCursor
	}
	start, _ := window(clamp(anchor, 0, len(m.state.Queue)-1), len(m.state.Queue), rows)
	index := start + row
	if index >= len(m.state.Queue) {
		return m, nil
	}
	already := m.queueFocus && m.queueCursor == index
	m.queueFocus = true
	m.queueCursor = index
	if already && index != m.state.QueueIndex {
		m.queueIntent, m.queueTarget, m.busy = "jump", index, true
		return m, m.queueCommand("jump")
	}
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	l := m.layout()
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(msg.X-l.gutter, msg.Y, msg.Button, l)
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.handleClick(msg.X-l.gutter, msg.Y, l)
	default:
		// Releases and motion carry no list action of their own.
		return m, nil
	}
}

// handleWheel scrolls an overlay, the Up Next panel, or the main list view.
func (m Model) handleWheel(x, y int, button tea.MouseButton, l layout) (tea.Model, tea.Cmd) {
	if m.overlay != "" {
		if (m.overlay == "help" || m.overlay == "info") && button == tea.MouseWheelUp {
			if maxOffset := m.helpScrollMax(); maxOffset > 0 {
				m.helpOffset = clamp(m.helpOffset-3, 0, maxOffset)
			}
		} else if (m.overlay == "help" || m.overlay == "info") && button == tea.MouseWheelDown {
			if maxOffset := m.helpScrollMax(); maxOffset > 0 {
				m.helpOffset = clamp(m.helpOffset+3, 0, maxOffset)
			}
		}
		return m, nil
	}
	delta := 3
	if button == tea.MouseWheelUp {
		delta = -3
	}
	dockQueue := l.showPanel && x >= l.mainWidth+1 && y >= l.dockTop() && y < l.dockTop()+l.nowHeight
	if dockQueue {
		if len(m.state.Queue) == 0 {
			return m, nil
		}
		m.queueFocus = true
		m.queueCursor = clamp(m.queueCursor+delta, 0, len(m.state.Queue)-1)
		return m, nil
	}
	if y < l.listTop || y >= l.listTop+l.listHeight {
		return m, nil
	}
	if m.queueFocus && !l.showPanel {
		if len(m.state.Queue) == 0 {
			return m, nil
		}
		last := len(m.state.Queue) - 1
		m.queueCursor = clamp(m.queueCursor+delta, 0, last)
		return m, nil
	}
	m.queueFocus = false
	return m.scrollMainList(delta), nil
}

// handleClick moves the cursor, activates a row, switches tabs, or closes an
// overlay. The viewport stays fixed so a second click at the same cell targets
// the same row.
func (m Model) handleClick(x, y int, l layout) (tea.Model, tea.Cmd) {
	if m.overlay != "" {
		if m.overlay == "input" {
			m = m.closeTextInput()
		} else {
			m.overlay = ""
		}
		return m, nil
	}
	if m.input.Focused() && y != l.headerRows-1 {
		m.input.Blur()
		m.inputMode = ""
	}
	if y == 0 {
		if source, ok := sourceTabAt(x); ok {
			return m.switchSource(source)
		}
		return m, nil
	}
	if y == 1 {
		if index, ok := m.viewTabAt(x); ok {
			return m.selectView(index)
		}
		return m, nil
	}
	if l.showPanel && x >= l.mainWidth+1 && y >= l.dockTop() && y < l.dockTop()+l.nowHeight {
		return m.selectQueueRow(y-l.dockTop()-1, l.nowHeight-2)
	}
	if y < l.listTop || y >= l.listTop+l.listHeight {
		return m, nil
	}
	row := y - l.listTop - 1
	if row < 0 {
		return m, nil
	}
	rows := l.listHeight - 2
	if m.queueFocus && !l.showPanel {
		return m.selectQueueRow(row, rows)
	}
	items := m.visibleItems()
	if len(items) == 0 {
		return m, nil
	}
	start, _ := m.mainListWindow(rows)
	index := start + row
	if index >= len(items) {
		return m, nil
	}
	already := !m.queueFocus && m.selected == index
	m.queueFocus = false
	m.selected, m.listOffset = index, start
	if already {
		return m.activate()
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
	m.logEvent("key", map[string]any{"key": msg.String(), "inputFocused": m.input.Focused()})
	if m.overlay == "theme" {
		return m.handleThemeKey(msg)
	}
	if m.overlay == "input" {
		return m.handleTextInputKey(msg)
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
		case "0":
			m.queueFocus = false
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
			if m.queueCursor != m.state.QueueIndex {
				m.queueIntent, m.queueTarget, m.busy = "jump", m.queueCursor, true
				return m, m.queueCommand("jump")
			}
		case "x":
			m.queueIntent, m.queueTarget, m.busy = "remove", m.queueCursor, true
			return m, m.queueCommand("remove")
		case "J":
			if m.queueCursor < last {
				m.queueIntent, m.queueTarget, m.busy = "movedown", m.queueCursor, true
				return m, m.queueCommand("movedown")
			}
		case "K":
			if m.queueCursor > 0 {
				m.queueIntent, m.queueTarget, m.busy = "moveup", m.queueCursor, true
				return m, m.queueCommand("moveup")
			}
		case "c":
			m.queueIntent, m.busy = "clear", true
			return m, m.queueClear()
		case "f", "F", "e", "E":
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
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "shift+tab":
		return m.switchSource(otherSource(m.source))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.selectView(int(msg.String()[0] - '1'))
	case "0":
		if !activeAppleQueue(m.state) {
			return m.withToast("Nothing is queued", true)
		}
		m.queueFocus = true
		m.queueCursor = m.state.QueueIndex
		return m, nil
	case "]":
		return m.cycleView(1)
	case "[":
		return m.cycleView(-1)
	case "up", "k":
		return m.moveBy(-1), nil
	case "down", "j":
		return m.moveBy(1), nil
	case "g", "home":
		m.selected = firstSelectableIndex(m.visibleItems())
		return m.keepMainSelectionVisible(), nil
	case "G", "end":
		m.selected = lastSelectableIndex(m.visibleItems())
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
					m.busy = true
					return m, m.control("pause")
				}
				if m.state.Status == "paused" {
					m.busy = true
					return m, m.control("resume")
				}
			}
		}
		m.busy = true
		if m.detailKind == "playlist" && m.detailID != "" {
			return m, m.playPlaylist(false)
		}
		return m, m.playSelected()
	case "space", "c":
		if m.state.Status == "playing" || m.state.Status == "buffering" {
			m.busy = true
			return m, m.control("pause")
		}
		if m.state.Status == "paused" {
			m.busy = true
			return m, m.control("resume")
		}
		if item, ok := m.selectedItem(); ok {
			switch item.Kind {
			case "stream", "station", "song":
				m.busy = true
				return m, m.playSelected()
			}
		}
		return m, nil
	case "n":
		if m.state.IsLive {
			return m, nil
		}
		m.busy = true
		return m, m.control("next")
	case "b":
		if m.state.IsLive {
			return m, nil
		}
		m.busy = true
		return m, m.control("previous")
	case "v":
		m.busy = true
		return m, m.stopPlayback()
	case "s":
		if m.state.IsLive {
			return m.withToast("Shuffle applies to Apple Music only", true)
		}
		if m.detailKind == "playlist" && m.detailID != "" {
			m.busy = true
			return m, m.playPlaylist(true)
		}
		m.busy = true
		return m, m.toggleShuffle()
	case "S":
		if m.source == "radio" && m.view == "Browse" {
			return m.resortRadioBrowse()
		}
		return m, nil
	case "r":
		return m.reloadView()
	case "R":
		if m.state.IsLive {
			return m.withToast("Repeat applies to Apple Music only", true)
		}
		m.busy = true
		return m, m.cycleRepeat()
	case "e", "E":
		item, ok := m.selectedItem()
		if !ok || !selectable(item) {
			return m, nil
		}
		if item.Kind == "stream" {
			return m.withToast("Live radio streams cannot be queued", true)
		}
		if msg.String() == "e" {
			m.busy = true
			return m, m.enqueueSelected("next")
		}
		m.busy = true
		return m, m.enqueueSelected("tail")
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
		m.themeIndex = indexOf(m.themeNames, m.themeName)
		m.overlay = "theme"
		return m, nil
	case "esc", "backspace", "h":
		if m.filter != "" {
			m.filter = ""
			m.selected, m.listOffset = 0, 0
			return m, nil
		}
		if len(m.history) > 0 {
			m = m.back()
			if m.source == "apple-music" && m.view == "Home" {
				m.loading = true
				return m, m.loadView()
			}
			return m, nil
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
	m.detailKind, m.detailID = "", ""
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
	return m
}

func (m Model) handleTextInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeTextInput(), nil
	case "enter":
		return m.submitInput()
	case "tab", "shift+tab":
		if m.inputMode == "filter" {
			m.filter = strings.TrimSpace(m.input.Value())
		}
		m = m.closeTextInput()
		return m.switchSource(otherSource(m.source))
	case "[", "]":
		if m.inputMode == "filter" {
			m.filter = strings.TrimSpace(m.input.Value())
		}
		m = m.closeTextInput()
		delta := 1
		if msg.String() == "[" {
			delta = -1
		}
		return m.cycleView(delta)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	mode := m.inputMode
	value := strings.TrimSpace(m.input.Value())
	m.logEvent("submit", map[string]any{"mode": mode, "valueLength": len(value), "valueKind": map[bool]string{true: "url", false: "text"}[mode == "url"]})
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
		next, cmd := m.push("Search: "+presentation.Text(value), m.searchAM(value))
		child := next.(Model)
		child.detailKind, child.detailID = "", ""
		return child, stampLoad(cmd, child.generation, child.destination())
	case "filter":
		m.filter = value
		m.selected, m.listOffset = 0, 0
		return m, nil
	case "url":
		if value == "" {
			return m, nil
		}
		item := core.Item{Kind: "stream", URL: value, Title: value}
		added := !m.store.IsFavorite("radio", state.ItemID("radio", item))
		m.logEvent("play", map[string]any{"itemKind": "stream", "titleLength": len(value)})
		m.busy = true
		playCmd := beginAction(m.actionClock, func() tea.Msg {
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

func (m Model) toggleFavorite() (tea.Model, tea.Cmd) {
	item, ok := m.selectedItem()
	if !ok {
		return m.withToast("Nothing selected", true)
	}
	source := m.source
	if item.Kind == "stream" {
		source = "radio"
	}
	added := !m.store.IsFavorite(source, state.ItemID(source, item))
	if err := m.store.UpdateAndSave(func(next *state.Store) {
		next.ToggleFavorite(source, presentation.Item(item))
	}); err != nil {
		return m.withToast("State save failed: "+presentation.Text(err.Error()), true)
	}
	if m.cache != nil {
		delete(m.cache, source+"/Favorites")
	}
	m.logEvent("favorite", map[string]any{"titleLength": len(item.Title), "on": added})
	text := "Unfavorited: " + item.Title
	if added {
		text = "★ Favorited: " + item.Title
	}
	model, cmd := m.withToast(text, false)
	if model.viewKey() == source+"/Favorites" {
		model.items = model.store.FavoritesFor(source)
		model.selected = clamp(model.selected, 0, max(0, len(model.items)-1))
	}
	return model, cmd
}

// helpOverlay lays out the read-only help/info overlay for a terminal size.
// When the content is taller than the box it becomes scrollable instead of
// silently truncating on small terminals.
type helpOverlay struct {
	title     string
	rows      []string
	boxWidth  int
	boxHeight int
	visible   int
}

func (m Model) helpOverlay(width, height int) helpOverlay {
	boxWidth := 74
	if width-4 < boxWidth {
		boxWidth = width - 4
	}
	if boxWidth < 4 {
		boxWidth = width
	}
	inner := boxWidth - 2
	title := "Help · Esc close"
	rows := m.helpLines(inner)
	if m.overlay == "info" {
		title = "Track Info · Esc close"
		rows = m.infoLines(inner)
	}
	boxHeight := len(rows) + 2
	if boxHeight > height {
		boxHeight = height
	}
	return helpOverlay{title: title, rows: rows, boxWidth: boxWidth, boxHeight: boxHeight, visible: max(0, boxHeight-2)}
}

func (m Model) helpScrollMax() int {
	layout := m.helpOverlay(m.width, m.height)
	if layout.visible <= 0 || len(layout.rows) <= layout.visible {
		return 0
	}
	return len(layout.rows) - layout.visible
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.overlay, m.helpOffset = "", 0
		return m, nil
	}
	if maxOffset := m.helpScrollMax(); maxOffset > 0 {
		switch msg.String() {
		case "up", "k":
			m.helpOffset = clamp(m.helpOffset-1, 0, maxOffset)
			return m, nil
		case "down", "j":
			m.helpOffset = clamp(m.helpOffset+1, 0, maxOffset)
			return m, nil
		case "pgup":
			m.helpOffset = clamp(m.helpOffset-10, 0, maxOffset)
			return m, nil
		case "pgdown":
			m.helpOffset = clamp(m.helpOffset+10, 0, maxOffset)
			return m, nil
		case "g", "home":
			m.helpOffset = 0
			return m, nil
		case "G", "end":
			m.helpOffset = maxOffset
			return m, nil
		}
	}
	m.overlay, m.helpOffset = "", 0
	return m, nil
}

func (m Model) handleThemeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		if m.themeIndex+1 < len(m.themeNames) {
			m.themeIndex++
		}
		m.themeName = m.themeNames[m.themeIndex]
		applyTheme(theme.Load(m.themeName))
		return m, nil
	case "shift+tab":
		if m.themeIndex > 0 {
			m.themeIndex--
		}
		m.themeName = m.themeNames[m.themeIndex]
		applyTheme(theme.Load(m.themeName))
		return m, nil
	case "up", "k":
		if m.themeIndex > 0 {
			m.themeIndex--
		}
		m.themeName = m.themeNames[m.themeIndex]
		applyTheme(theme.Load(m.themeName))
		return m, nil
	case "down", "j":
		if m.themeIndex+1 < len(m.themeNames) {
			m.themeIndex++
		}
		m.themeName = m.themeNames[m.themeIndex]
		applyTheme(theme.Load(m.themeName))
		return m, nil
	case "enter":
		if err := m.store.UpdateAndSave(func(next *state.Store) { next.Theme = m.themeName }); err != nil {
			return m.withToast("State save failed: "+presentation.Text(err.Error()), true)
		}
		m.overlay = ""
		m.logEvent("theme", map[string]any{"name": m.themeName})
		return m.withToast("Theme: "+m.themeName, false)
	case "esc":
		m.overlay = ""
		applyTheme(theme.Load(m.store.Theme))
		return m, nil
	}
	return m, nil
}

// layout mirrors View's geometry so mouse events can be hit-tested.
type layout struct {
	width      int // usable content width, excluding the visual gutter
	height     int
	gutter     int
	headerRows int
	listTop    int
	listHeight int
	dockGap    int
	showPanel  bool // an interactive Apple Music queue in the playback dock
	mainWidth  int
	panelWidth int
	nowHeight  int
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
	// Reserve the status row even when no toast is visible. Async playback and
	// probe messages must never move the playback dock by one terminal row. A
	// final spacer centres the shortcut footer in its own lower band instead of
	// pinning its descenders to the terminal edge.
	trailer := 1 + footerBottomRows
	headerRows := consoleHeaderRows
	if m.input.Focused() {
		headerRows++
	}
	bodyHeight := height - headerRows - 1 - trailer
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	// The playback dock always occupies the same lower band. Playing changes
	// its content, never the location or height of the browsing workspace.
	nowHeight := 8
	if bodyHeight < 14 {
		nowHeight = bodyHeight / 2
	}
	if nowHeight < 3 {
		nowHeight = 3
	}
	dockGap := 1
	listHeight := bodyHeight - nowHeight - dockGap
	if listHeight < minListRows {
		dockGap = 0
		listHeight = minListRows
		nowHeight = bodyHeight - listHeight
	}
	showPanel := activeAppleQueue(m.state) && width >= 88 && nowHeight > minDockRows
	mainWidth, panelWidth := width, 0
	if showPanel {
		// Queue entries need more room than a web sidebar: terminal text cannot
		// shrink its font for long artist names, so reserve two fifths.
		panelWidth = clamp(width*2/5, 36, 48)
		mainWidth = width - panelWidth - 1
	}
	return layout{width: width, height: height, gutter: gutter, headerRows: headerRows, listTop: headerRows, listHeight: listHeight, dockGap: dockGap, showPanel: showPanel, mainWidth: mainWidth, panelWidth: panelWidth, nowHeight: nowHeight}
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

// View declares the terminal features lilt wants (alternate screen and mouse
// reporting) alongside the rendered content. Bubble Tea v2 moved these from
// program options to declarative view fields.
func (m Model) View() tea.View {
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
		return consoleFrame(tinyView(l.width, l.height), l.width, l.gutter)
	}
	if m.overlay != "" {
		return consoleFrame(m.overlayView(l.width, l.height), l.width, l.gutter)
	}
	if minWidth, minHeight := m.consoleMinimum(); l.width < minWidth || l.height < minHeight {
		return consoleFrame(tinyView(l.width, l.height), l.width, l.gutter)
	}
	width, height := l.width, l.height
	header := []string{m.sourceLine(width), m.viewLine(width)}
	if m.input.Focused() {
		header = append(header, m.input.View())
	}
	listHeight := l.listHeight
	mainTitle, mainLines := m.listTitle(), m.listLines(width-4, listHeight-2)
	mainActive := !m.queueFocus
	if m.queueFocus && !l.showPanel {
		mainTitle = m.queueTitle(listHeight - 2)
		mainLines = m.queueLines(width-4, listHeight-2)
		mainActive = true
	}
	listBox := renderSpaciousBox(mainTitle, mainLines, width, listHeight, mainActive)
	var dock string
	if l.showPanel {
		nowBox := renderSpaciousBox(m.nowTitle(), m.nowLines(l.mainWidth-4, l.nowHeight-2), l.mainWidth, l.nowHeight, false)
		queueBox := renderSpaciousBox(m.queueTitle(l.nowHeight-2), m.queueLines(l.panelWidth-4, l.nowHeight-2), l.panelWidth, l.nowHeight, m.queueFocus)
		nowLines, queueLines := strings.Split(nowBox, "\n"), strings.Split(queueBox, "\n")
		joined := make([]string, len(nowLines))
		for i := range joined {
			joined[i] = nowLines[i] + " " + queueLines[i]
		}
		dock = strings.Join(joined, "\n")
	} else {
		dock = renderSpaciousBox(m.nowTitle(), m.nowLines(width-4, l.nowHeight-2), width, l.nowHeight, false)
	}
	bodyParts := []string{listBox}
	if l.dockGap > 0 {
		bodyParts = append(bodyParts, strings.Repeat(" ", width))
	}
	bodyParts = append(bodyParts, dock)
	body := lipgloss.JoinVertical(lipgloss.Left, bodyParts...)
	lines := append([]string{}, header...)
	lines = append(lines, strings.Split(body, "\n")...)
	statusLine := fit("", width)
	if m.message != "" {
		style := accentStyle
		if m.messageErr {
			style = errorStyle
		}
		statusLine = style.Render(fit(m.message, width))
	}
	lines = append(lines, statusLine, m.footerLine(width))
	for range footerBottomRows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	// Keep Bubble Tea from scrolling when terminal dimensions are tiny or a
	// focused input makes the header taller than the viewport.
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", max(0, width)))
	}
	return consoleFrame(strings.Join(lines, "\n"), width, l.gutter)
}

func tinyView(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	text := "Terminal too small"
	if width >= 20 && height > 1 {
		text = "Terminal too small — resize"
	}
	lines := []string{fit(text, width)}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// consoleMinimum reports the smallest canvas the console layout can draw
// honestly: header, one browsing list, one playback dock, a gap, the status row
// and the footer. Below it every panel would be clipped mid-border, which reads
// as a broken frame, so the resize notice is the correct answer.
func (m Model) consoleMinimum() (int, int) {
	rows := consoleHeaderRows + 1 + minListRows + minDockRows + 1 + 1 + footerBottomRows
	if m.input.Focused() {
		rows++
	}
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

func otherSource(source string) string {
	if source == "radio" {
		return "apple-music"
	}
	return "radio"
}

func (m Model) sourceLine(width int) string {
	tabs := []string{tabStyle.Render("SOURCE")}
	for _, source := range []string{"apple-music", "radio"} {
		label := " " + sourceTitle(source) + " "
		if source == m.source {
			tabs = append(tabs, activeTab.Render("["+strings.TrimSpace(label)+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	return fit(titleStyle.Render("lilt")+"  "+strings.Join(tabs, "  ")+tabStyle.Render("   (Tab)"), width)
}

func sourceTitle(source string) string {
	if source == "radio" {
		return "Radio"
	}
	return "Apple Music"
}

func (m Model) activeTopView() string {
	if len(m.history) > 0 {
		return ""
	}
	return m.view
}

func (m Model) viewLine(width int) string {
	tabs := []string{tabStyle.Render("VIEW  ")}
	active := m.activeTopView()
	for i, view := range viewsFor(m.source) {
		label := fmt.Sprintf(" %d %s ", i+1, view)
		if view == active {
			tabs = append(tabs, activeTab.Render("["+strings.TrimSpace(label)+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	return fit(strings.Join(tabs, " "), width)
}

func (m Model) listTitle() string {
	title := m.title
	if m.filter != "" {
		title += " filter:" + presentation.Text(m.filter)
	}
	count := 0
	for _, item := range m.visibleItems() {
		if selectable(item) {
			count++
		}
	}
	title += fmt.Sprintf(" (%d)", count)
	if m.viewKey() == "radio/Browse" && normalizedRadioSort(m.browseQuery.Sort) == "fastest" {
		if measured, total := m.radioHealthCoverage(); total > 0 && measured < total {
			title += fmt.Sprintf(" · %d/%d measured", measured, total)
		}
	}
	if m.pageLoading {
		title += " loading more…"
	} else if m.loading {
		if len(m.items) > 0 {
			title += " refreshing…"
		} else {
			title += " loading…"
		}
	}
	return title
}

// emptyText explains what to do next instead of showing a bare "(empty)".
func (m Model) emptyText() string {
	if m.filter != "" {
		return "(no match for " + presentation.Text(m.filter) + ")"
	}
	if m.viewKey() == "radio/Browse" && m.browseQuery != (radioDiscovery{}) {
		return "(no stations matched — press / to adjust the query)"
	}
	switch m.viewKey() {
	case "radio/Favorites":
		return "(empty) — press a to add a stream URL, / to search stations, 3 to browse"
	case "radio/Recent":
		return "(empty) — nothing played yet"
	case "radio/Browse":
		return "(empty) — press / to search and filter stations"
	case "apple-music/Playlists":
		return "(empty) — no playlists in your Apple Music library"
	case "apple-music/Favorites":
		return "(empty) — press f on a song or playlist to favorite it"
	case "apple-music/Recent":
		return "(empty) — nothing played yet"
	case "apple-music/Presets":
		return "(empty) — define presets in ~/.config/lilt/presets.toml"
	case "apple-music/Home":
		if m.account != "" {
			return "(empty) — " + strings.TrimPrefix(m.account, "Account: ")
		}
		return "(empty) — press / to search or open a playlist"
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

func (m Model) listLines(width, rows int) []string {
	items := m.visibleItems()
	if m.loading && len(items) == 0 {
		return []string{loadingStyle.Render(fit("loading…", width))}
	}
	if m.listErr != "" && len(items) == 0 {
		return []string{errorStyle.Render(fit(m.listErr, width))}
	}
	if len(items) == 0 {
		return []string{tabStyle.Render(fit(m.emptyText(), width))}
	}
	start, end := m.mainListWindow(rows)
	// The rightmost column is a scrollbar gutter, so the view has a visible
	// position indicator and mouse scrolling reads as dragging the bar.
	bar := scrollbarColumn(rows, len(items), start)
	contentWidth := max(1, width-1)
	lines := make([]string, 0, max(rows, end-start))
	for i := start; i < end; i++ {
		item := items[i]
		if item.Kind == "header" {
			lines = append(lines, accentStyle.Render(fit("── "+item.Title+" ──", contentWidth))+bar[len(lines)])
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
				radioFavorite = m.store.IsFavorite(source, state.ItemID(source, item))
			} else if source != "radio" && m.store.IsFavorite(source, state.ItemID(source, item)) {
				appleFavorite = true
			}
		}
		if item.Kind == "stream" || item.Kind == "station" {
			text, style := m.probeSegment(item)
			metadata = " — " + text
			secondary = dimStyle.Render(" — ") + style.Render(text)
			if item.Artist != "" {
				metadata += " · " + item.Artist
				secondary += dimStyle.Render(" · " + item.Artist)
			}
		} else if item.Artist != "" {
			metadata = " — " + item.Artist
			secondary = dimStyle.Render(metadata)
		}
		glyph := ""
		if strings.HasPrefix(m.title, "Search: ") || m.viewKey() == "apple-music/Home" {
			glyph = kindGlyph(item.Kind)
		}
		label, plainLabel := listLabel(item.Title, radioFavorite, appleFavorite, glyph)
		// The cursor column is rendered outside the row style so selection and the
		// playing highlight stay independent: the `>` marks the cursor, the style
		// marks playback, and neither paints over the other's gutter. Every row
		// then gets one blank column of padding on each side so highlighted text
		// never touches the edge of its fill.
		cursor := "  "
		if i == m.selected {
			cursor = "> "
		}
		textWidth := max(1, contentWidth-4)
		row := ""
		switch listRowKind(i == m.selected, m.isPlayingItem(item)) {
		case rowPlaying:
			row = cursor + currentStyle.Render(" "+fit(plainLabel+metadata, textWidth)+" ")
		case rowSelected:
			row = cursor + selStyle.Render(" "+fit(plainLabel+metadata, textWidth)+" ")
		default:
			// Station health, codec, country and tags support comparison but are
			// secondary to the station/song name. Lower contrast makes long rows
			// scannable without throwing away that information.
			row = cursor + " " + fit(rowStyle.Render(label)+secondary, textWidth) + " "
		}
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
func listLabel(title string, radioFavorite, appleFavorite bool, glyph string) (styled, plain string) {
	styled, plain = title, title
	if radioFavorite {
		styled += " " + accentStyle.Render("★")
		plain += " ★"
	}
	if appleFavorite {
		styled = accentStyle.Render("★") + " " + styled
		plain = "★ " + plain
	}
	if glyph != "" {
		styled = glyph + styled
		plain = glyph + plain
	}
	return styled, plain
}

// scrollbarColumn renders the right-edge scrollbar for a list window. A list
// that fits has no track, so the gutter stays quiet until it can move.
func scrollbarColumn(rows, total, start int) []string {
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
			column[i] = scrollbarStyle.Render("┃")
			continue
		}
		column[i] = dimStyle.Render("│")
	}
	return column
}

// queueTitle labels the queue panel. rows is the number of entries the panel
// can show; a longer queue also reports its visible window so a short dock does
// not hide the fact that more tracks exist.
func (m Model) queueTitle(rows int) string {
	source := m.queueSource.Title
	if source == "" {
		source = "Queue"
	}
	title := fmt.Sprintf("Up Next · %d/%d · %s", m.state.QueueIndex+1, len(m.state.Queue), source)
	if rows > 0 && len(m.state.Queue) > rows {
		start, end := m.queueWindow(rows)
		title = fmt.Sprintf("Up Next · %d/%d · %d-%d shown · %s", m.state.QueueIndex+1, len(m.state.Queue), start+1, end, source)
	}
	return title
}

// queueWindow returns the visible entry range for a panel of rows entries.
func (m Model) queueWindow(rows int) (int, int) {
	anchor := m.state.QueueIndex
	if m.queueFocus {
		anchor = m.queueCursor
	}
	return window(clamp(anchor, 0, len(m.state.Queue)-1), len(m.state.Queue), rows)
}

func (m Model) queueLines(width, rows int) []string {
	if len(m.state.Queue) == 0 {
		return []string{tabStyle.Render(fit("(empty)", width))}
	}
	start, end := m.queueWindow(rows)
	bar := scrollbarColumn(rows, len(m.state.Queue), start)
	contentWidth := max(1, width-1)
	lines := make([]string, 0, max(rows, end-start))
	for i := start; i < end; i++ {
		entry := m.state.Queue[i]
		label := entry.Title
		if entry.Artist != "" {
			label += " — " + entry.Artist
		}
		marker := "  "
		if m.queueFocus && i == m.queueCursor {
			marker = "> "
		}
		if i == m.state.QueueIndex {
			marker += "▶ "
		}
		style := rowStyle
		switch {
		case m.queueFocus && i == m.queueCursor:
			style = selStyle
		case i == m.state.QueueIndex:
			style = currentStyle
		case i < m.state.QueueIndex:
			style = dimStyle
		}
		lines = append(lines, style.Render(fit(marker+label, contentWidth))+bar[len(lines)])
	}
	for len(lines) < rows {
		lines = append(lines, fit("", contentWidth)+bar[len(lines)])
	}
	return lines
}

func (m Model) nowTitle() string {
	if m.busy {
		return "Now Playing · working…"
	}
	if m.state.IsLive {
		if m.source != "radio" {
			return "Now Playing · Radio · LIVE"
		}
		return "Now Playing · LIVE"
	}
	if m.state.Mode == "full" && m.source != "apple-music" {
		return "Now Playing · Apple Music"
	}
	if m.state.Status == "buffering" {
		return "Now Playing · buffering…"
	}
	if !m.connected && m.stateUpdates == nil {
		return "Now Playing · disconnected"
	}
	if m.state.Error != "" {
		return "Now Playing · error"
	}
	return "Now Playing"
}

func (m Model) nowLines(width, height int) []string {
	line := func(text string) string { return rowStyle.Render(fit(text, width)) }
	if m.state.Track == nil {
		if m.busy {
			return []string{loadingStyle.Render(fit("working…", width))}
		}
		lines := []string{tabStyle.Render(fit("Nothing playing", width))}
		// An Apple Music authorization warning belongs to its own source. Showing
		// it in Radio's empty dock makes a working radio browser look broken.
		if m.account != "" && m.source == "apple-music" {
			lines = append(lines, line(m.account))
		}
		return lines
	}
	title := m.state.Track.Title
	if m.state.Track.Artist != "" {
		title += " — " + m.state.Track.Artist
	}
	titleLine := trackStyle.Render(fit(title, width))
	if m.state.IsLive && m.store != nil {
		marker := " "
		if m.store.IsFavorite("radio", state.ItemID("radio", *m.state.Track)) {
			marker = accentStyle.Render("★")
		}
		titleLine = marker + " " + trackStyle.Render(fit(title, max(0, width-2)))
	}
	lines := []string{titleLine}
	if m.state.IsLive {
		status := m.state.Status
		if status == "" {
			status = "stopped"
		}
		if status == "buffering" {
			status = "buffering…"
		}
		// LIVE is already a persistent badge in the dock title. Repeating it in
		// the body and again in "live stream" adds noise without new information.
		lines = append(lines, line(fmt.Sprintf("%s · Radio stream", strings.ToUpper(status[:1])+status[1:])))
		if m.state.Error != "" {
			lines = append(lines, errorStyle.Render(fit("Error: "+m.state.Error, width)))
		}
	} else {
		barWidth := width - 18
		if barWidth < 8 {
			barWidth = 8
		}
		if barWidth > 40 {
			barWidth = 40
		}
		lines = append(lines, line(progressBar(m.displayPositionAt(time.Now()), m.state.Duration, barWidth)))
		format := m.state.Format
		if format == "" && m.state.AudioVariant != nil {
			format = *m.state.AudioVariant
		}
		status := m.state.Status
		if status == "" {
			status = "stopped"
		}
		if status == "buffering" {
			status = "buffering…"
		}
		// MusicKit reports a stale paused/stopped snapshot while a play
		// command is still starting; show a single unambiguous status instead
		// of "paused · working…".
		busyStarting := (status == "stopped" && m.busy) ||
			(status == "paused" && m.state.Mode == "full" && m.state.Position <= 0)
		if busyStarting {
			status = "starting"
		}
		source := "Apple Music"
		if m.state.Mode == "preview" {
			source = "Preview"
		}
		statusLabel := strings.ToUpper(status[:1]) + status[1:]
		stateLine := strings.Join([]string{statusLabel, source, emptyDash(format)}, " · ")
		if flags := m.modeFlags(); flags != "" {
			stateLine += " · " + flags
		}
		if m.busy && !busyStarting {
			stateLine += " · working…"
		}
		lines = append(lines, line(stateLine))
		// Wide Apple Music docks render the queue beside the track, so repeating
		// its count here only adds noise. Narrow docks retain a concise route to it.
		if activeAppleQueue(m.state) && !m.layout().showPanel {
			action := "0 focus"
			if m.queueFocus {
				action = "0 back"
			}
			lines = append(lines, line(fmt.Sprintf("Up Next · %d of %d · %s", m.state.QueueIndex+1, len(m.state.Queue), action)))
		}
		if m.account != "" && m.source == "apple-music" && !m.state.IsLive {
			lines = append(lines, line(m.account))
		}
		if m.state.Error != "" {
			lines = append(lines, errorStyle.Render(fit("Error: "+m.state.Error, width)))
		}
	}
	return lines
}

func (m Model) modeFlags() string {
	parts := []string{}
	if m.state.Shuffle {
		parts = append(parts, "shuffle")
	}
	if m.state.Repeat != "" && m.state.Repeat != "off" {
		parts = append(parts, "repeat:"+m.state.Repeat)
	}
	return strings.Join(parts, " ")
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
		return []string{"j/k move", "enter/p jump", "x remove", "J/K reorder", "c clear", "0/esc/h back", "? help"}
	}
	if m.source == "apple-music" && m.detailKind == "playlist" && !m.loading {
		segments := []string{"p play all", "s shuffle", "enter play from here"}
		if activeAppleQueue(m.state) {
			segments = append(segments, "0 Up Next")
		}
		return append(segments, "esc back", "? help")
	}
	if m.listErr != "" {
		return []string{"r retry", "esc back", "/ search", "? help", "q quit"}
	}
	segments := []string{"enter open/play", "p play"}
	if m.state.Track != nil {
		switch m.state.Status {
		case "playing", "buffering":
			segments = append(segments, "space pause", "v stop")
		case "paused":
			segments = append(segments, "space resume", "v stop")
		}
	}
	if len(m.history) > 0 {
		segments = append(segments, "esc back")
	}
	if m.source == "radio" && m.view == "Browse" {
		if m.browseQuery != (radioDiscovery{}) {
			segments = append(segments, "esc popular")
		}
		if normalizedRadioSort(m.browseQuery.Sort) != "recommended" {
			segments = append(segments, "S re-sort")
		}
	}
	if m.store != nil {
		if item, ok := m.selectedItem(); ok {
			source := m.source
			if item.Kind == "stream" || item.Kind == "station" {
				source = "radio"
			}
			hint := "f favorite"
			if m.store.IsFavorite(source, state.ItemID(source, item)) {
				hint = "f unfavorite"
			}
			segments = append(segments, hint)
		}
	}
	if activeAppleQueue(m.state) {
		segments = append(segments, "0 Up Next")
	}
	if m.source == "radio" {
		segments = append(segments, "/ search & filters")
	} else {
		segments = append(segments, "F filter")
	}
	if m.source != "radio" {
		segments = append(segments, "/ search")
	}
	segments = append(segments, "? help", "q quit", "Tab source", "1-9 view")
	return segments
}

func (m Model) footerLine(width int) string {
	segments := m.footerSegments()
	if len(segments) == 0 {
		return tabStyle.Render(fit("", width))
	}
	line := segments[0]
	for _, segment := range segments[1:] {
		candidate := line + " · " + segment
		if lipgloss.Width(candidate) > width {
			break
		}
		line = candidate
	}
	return tabStyle.Render(fit(line, width))
}

func (m Model) overlayView(width, height int) string {
	if m.overlay == "input" {
		title, hint := "Input", "Enter submit · Esc cancel"
		switch m.inputMode {
		case "search":
			title, hint = "Search Apple Music", "Enter search · Esc cancel"
		case "filter":
			title, hint = "Filter Current List", "Enter apply · Esc cancel"
		case "url":
			title, hint = "Add Radio URL", "Enter add & play · Esc cancel"
		}
		boxWidth := min(64, max(24, width-4))
		inner := boxWidth - 2
		input := m.input
		// bubbles/textinput renders a cursor cell in addition to its prompt and
		// configured field width; reserve it so renderBox never adds an ellipsis.
		input.SetWidth(max(1, inner-lipgloss.Width(input.Prompt)-1))
		rows := []string{input.View(), "", dimStyle.Render(hint)}
		boxHeight := min(height, len(rows)+2)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox(title, rows, boxWidth, boxHeight, true))
	}
	if m.overlay == "discovery" || m.overlay == "discovery-text" || m.overlay == "discovery-options" {
		boxWidth := min(72, max(24, width-4))
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
			title = "Choose " + strings.Title(m.discoveryKind) + " · Filter: " + discoveryValue(m.discoveryQuery)
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
			rows = append(rows, "", "Typing filters (j/k included) · ↑↓ move · Enter choose · Esc back")
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
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox(title, shown, boxWidth, boxHeight, true))
	}
	if m.overlay == "theme" {
		boxWidth := min(40, width)
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
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox("Theme", rows, boxWidth, boxHeight, true))
	}
	layout := m.helpOverlay(width, height)
	rows := layout.rows
	title := layout.title
	if layout.visible > 0 && len(rows) > layout.visible {
		maxOffset := len(rows) - layout.visible
		offset := clamp(m.helpOffset, 0, maxOffset)
		name := "Help"
		if m.overlay == "info" {
			name = "Track Info"
		}
		title = fmt.Sprintf("%s · %d-%d/%d · ↑↓/PgUp/PgDn scroll · Esc close", name, offset+1, offset+layout.visible, len(rows))
		rows = rows[offset : offset+layout.visible]
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox(title, rows, layout.boxWidth, layout.boxHeight, true))
}

func (m Model) helpLines(width int) []string {
	type entry struct{ group, key, description string }
	entries := []entry{
		{"Navigation", "tab", "switch source (Apple Music / Radio)"},
		{"Navigation", "1 - 9", "select sub-view"},
		{"Navigation", "[ / ]", "cycle sub-view"},
		{"Navigation", "j / k", "move selection"},
		{"Navigation", "g / G", "jump to top or bottom"},
		{"Navigation", "enter", "open playlist/station or play"},
		{"Navigation", "esc / backspace / h", "back or clear filter"},
		{"Navigation", "r", "reload the current list (retry after an error)"},
		{"Playback", "p", "play selected; toggle the playing item"},
		{"Playback", "space / c", "pause or resume"},
		{"Playback", "n / b", "next or previous (Apple Music)"},
		{"Playback", "v", "stop"},
		{"Playback", "s / R", "shuffle / repeat"},
		{"Playback", "e / E", "queue next / append (Apple Music)"},
		{"Up Next", "0", "focus or leave the panel"},
		{"Up Next", "enter / p", "jump to selected track"},
		{"Up Next", "x", "remove selected track"},
		{"Up Next", "J / K", "reorder selected track"},
		{"Up Next", "c", "clear the queue"},
		{"Library", "f", "favorite / unfavorite (lilt-local list)"},
		{"Library", "a", "add a stream URL to Favorites and play it (Radio)"},
		{"Library", "/", "Apple Music search; Radio Search & Filters"},
		{"Library", "S", "re-sort loaded Radio stations with fresh probe results"},
		{"Library", "F", "filter current Apple Music list"},
		{"Interface", "t / i", "theme picker / track info"},
	}
	lines := make([]string, 0, len(entries)+5)
	group := ""
	for _, entry := range entries {
		if entry.group != group {
			group = entry.group
			// The first four groups are navigation landmarks. Keep the compact
			// interface shortcuts unheaded so Help still fits a 30-row terminal.
			if group != "Interface" {
				lines = append(lines, titleStyle.Render(fit("── "+strings.ToUpper(group)+" ──", width)))
			}
		}
		for _, row := range wrapHelpRow(entry.key, entry.description, 16, width) {
			lines = append(lines, rowStyle.Render(fit(row, width)))
		}
	}
	return lines
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
	if m.account != "" {
		add("Account", strings.TrimPrefix(m.account, "Account: "))
	}
	add("Live", fmt.Sprintf("%v", m.state.IsLive))
	add("Format", m.state.Format)
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
	add("Position", fmt.Sprintf("%.0f / %.0f s", m.state.Position, m.state.Duration))
	add("Queue", fmt.Sprintf("%d entries, index %d", len(m.state.Queue), m.state.QueueIndex))
	if activeAppleQueue(m.state) {
		add("Up Next", "0 focus; Enter/p jump; x remove")
	}
	return lines
}

// renderBox keeps overlays dense so text-heavy controls retain their full
// instructions on smaller terminals.
func renderBox(title string, lines []string, width, height int, activeBox bool) string {
	if width < 4 {
		width = 4
	}
	if height < 3 {
		height = 3
	}
	inner := width - 2
	color := borderIdle
	if activeBox {
		color = borderActive
	}
	border := lipgloss.NewStyle().Foreground(color)
	label := "─ " + title + " "
	if lipgloss.Width(label) > inner-1 {
		label = fit(label, inner-1)
	}
	top := border.Render("┌" + label + strings.Repeat("─", max(0, inner-lipgloss.Width(label))) + "┐")
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
func renderSpaciousBox(title string, lines []string, width, height int, activeBox bool) string {
	if width < 6 {
		width = 6
	}
	if height < 3 {
		height = 3
	}
	inner := width - 2
	contentWidth := max(1, inner-2) // one cell of breathing room on both sides
	color := borderIdle
	if activeBox {
		color = borderActive
	}
	border := lipgloss.NewStyle().Foreground(color)
	labelStyle := dimStyle
	if activeBox {
		labelStyle = accentStyle.Bold(true)
	}
	// The leading rule is part of the box frame, not part of the title. Keep it
	// in the border colour so the title colour starts at the first letter.
	prefix, suffix := "── ", " "
	titleWidth := max(1, inner-lipgloss.Width(prefix)-lipgloss.Width(suffix)-1)
	titleText := clip(strings.ToUpper(title), titleWidth)
	labelWidth := lipgloss.Width(prefix) + lipgloss.Width(titleText) + lipgloss.Width(suffix)
	top := border.Render("┌"+prefix) + labelStyle.Render(titleText) + border.Render(suffix+strings.Repeat("─", max(0, inner-labelWidth))+"┐")
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
	start, _ := m.mainListWindow(m.layout().listHeight - 2)
	m.listOffset = start
	return m
}

// scrollMainList drags the viewport like a scrollbar instead of walking the
// selection cursor. The cursor keeps its item and only follows when it would
// otherwise leave the window, so wheeling reads as moving the list itself.
func (m Model) scrollMainList(delta int) Model {
	items := m.visibleItems()
	rows := m.layout().listHeight - 2
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

func progressBar(position, duration float64, width int) string {
	if duration <= 0 {
		return "[" + strings.Repeat("░", width) + "] " + clock(position) + " / --:--"
	}
	ratio := position / duration
	if ratio < 0 || math.IsNaN(ratio) {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "] " + clock(position) + " / " + clock(duration)
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
	return 0
}

// Run owns the interactive program. The caller owns helper and socket cleanup.
func Run(opts Options) error {
	var subscriber core.PlaybackStateSubscriber
	if value, ok := opts.Player.(core.PlaybackStateSubscriber); ok {
		subscriber = value
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		subscription, err := subscriber.SubscribeState(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("subscribe to playback state: %w", err)
		}
		opts.InitialState = &subscription.Initial
		opts.StateUpdates = subscription.Updates
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = subscriber.UnsubscribeState(ctx)
		}()
	}
	m := New(opts)
	if opts.Log != nil {
		opts.Log("tui.run", nil)
	}
	// Alt screen and mouse reporting are declared on tea.View in v2.
	p := tea.NewProgram(m)
	_, err := p.Run()
	if opts.Log != nil {
		fields := map[string]any{}
		if err != nil {
			fields["error"] = err.Error()
		}
		opts.Log("tui.quit", fields)
	}
	return err
}
