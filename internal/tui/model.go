// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	selected                                          int
}

// radioDiscovery is the query that drives the Browse view. It belongs to the
// TUI session, never to persistent user state.
type radioDiscovery struct {
	Language, Tag, CountryCode, CountryName, Term string
}

const (
	discoveryText = iota
	discoveryLanguage
	discoveryGenre
	discoveryCountry
	discoveryReset
	discoveryConfirm
	discoveryCancel
)

// radioProbe is the in-process health state for one normalized radio URL.
// Terminal states are cached for the life of the process; a URL is never
// auto-probed twice.
type radioProbe struct {
	status  string
	latency int
	code    string
	message string
}

type probeRequest struct {
	key string
	url string
}

type probeMsg struct {
	key    string
	result core.RadioProbeResult
	err    error
}

const (
	radioProbeWorkers    = 2
	radioProbeTimeoutMs  = 6000
	radioProbeRPCTimeout = 10 * time.Second
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
	borderActive = lipgloss.Color("81")
	borderIdle   = lipgloss.Color("240")
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
	borderActive = lipgloss.Color(t.Green)
	borderIdle = lipgloss.Color(t.FG)
}

type Options struct {
	Provider       Provider
	Player         Player
	Radio          RadioProvider
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
	provider Provider
	player   Player
	radio    RadioProvider
	store    *state.Store
	input    textinput.Model

	source   string
	view     string
	title    string
	items    []core.Item
	selected int
	history  []page

	presets []core.Item
	resolve func(context.Context, string, *state.Store) (core.Item, error)

	state         core.PlaybackState
	queueSource   queueContext
	authorization string
	account       string

	width, height int
	loading       bool
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
	if f == (radioDiscovery{}) {
		return "Popular Worldwide"
	}
	return "Showing: " + presentation.Text(f.summary())
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
		return fmt.Sprintf("● %dms", probe.latency), okStyle
	case "failed":
		return "× " + shortProbeError(probe.code), errorStyle
	default:
		return "○ unchecked", dimStyle
	}
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
	start, end := window(m.selected, len(items), rows)
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
		return probeMsg{key: req.key, result: result, err: err}
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

// discoveryConfirmLabel names what Confirm will do, so an empty query reads as
// "Show all" instead of a mysterious generic button.
func discoveryConfirmLabel(pending radioDiscovery, term string) string {
	term = strings.TrimSpace(term)
	hasFacets := pending.filter() != (radio.Filter{})
	switch {
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
	return stampLoad(m.loadViewUnstamped(), m.generation, m.destination())
}

func (m Model) loadViewUnstamped() tea.Cmd {
	key := m.viewKey()
	// Apple Music Home combines current state and local history, so never serve a stale page.
	if key != "apple-music/Home" {
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
			var stations []radio.Station
			var err error
			if query != (radioDiscovery{}) {
				stations, err = m.radio.SearchFiltered(ctx, query.Term, query.filter(), 0, 20)
			} else {
				stations, err = m.radio.Popular(ctx, radio.Filter{}, 20, 0)
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

func (m Model) playItem(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": item.Kind, "titleLength": len(item.Title)})
	switch {
	case item.Kind == "stream":
		note := ""
		if probe, ok := m.probes[radioProbeKey(item)]; ok && probe.status == "failed" {
			note = "Retrying " + item.Title + " — earlier probe failed (" + shortProbeError(probe.code) + ")"
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

func (m Model) playSelected() tea.Cmd {
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
			return m, m.playPlaylistFrom(item)
		}
	}
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
	m.history = append(m.history, page{source: m.source, view: m.view, title: m.title, detailKind: m.detailKind, detailID: m.detailID, filter: m.filter, items: m.items, selected: m.selected})
	m.title = presentation.Text(title)
	m.items = nil
	m.selected = 0
	m.filter = ""
	m.loading = true
	m.generation++
	return m, stampLoad(cmd, m.generation, m.destination())
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
	return m
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
	m.items = nil
	m.selected = 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	m.generation++
	if err := m.store.UpdateAndSave(func(next *state.Store) { next.LastSource = source }); err != nil {
		m.message, m.messageErr = "State save failed: "+presentation.Text(err.Error()), true
	}
	m.logEvent("navigate", map[string]any{"action": "source"})
	return m, m.loadView()
}

func (m Model) selectView(index int) (tea.Model, tea.Cmd) {
	views := viewsFor(m.source)
	if index < 0 || index >= len(views) {
		return m, nil
	}
	if views[index] == m.view && len(m.history) == 0 {
		return m, nil
	}
	m.view = views[index]
	m.title = m.view
	m.lastView[m.source] = m.view
	m.history = nil
	m.filter = ""
	m.items = nil
	m.selected = 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	m.generation++
	m.logEvent("navigate", map[string]any{"action": "view"})
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
	m.items = nil
	m.selected = 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID = "", ""
	m.generation++
	m.logEvent("navigate", map[string]any{"action": "cycle"})
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
	m.selected = previous.selected
	m.filter = previous.filter
	m.detailKind, m.detailID = previous.detailKind, previous.detailID
	m.loading = false
	m.generation++
	return m
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
		next, cmd := m.scheduleProbes()
		return next, cmd
	case listMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.messageErr = true
			if m.source == "radio" {
				m.message = "Radio directory unavailable — check your connection, then retry (/ to search, 3 to browse)"
			} else {
				m.message = "Error: " + presentation.Text(msg.err.Error())
			}
			return m, nil
		}
		if m.cache != nil {
			m.cache[msg.key] = presentation.Items(msg.items)
		}
		if msg.key == m.viewKey() {
			m.title = msg.title
			m.items = presentation.Items(msg.items)
			m.selected = 0
			m.filter = ""
		}
		next, cmd := m.scheduleProbes()
		return next, cmd
	case homeMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
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
		if msg.err != nil {
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
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastMsg{seq} })
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
			refresh = m.loadView()
		}
		m.messageErr = false
		if msg.note != "" {
			m, toastCmd := m.withToast(msg.note, false)
			return m, tea.Batch(toastCmd, refresh)
		}
		m.message = ""
		return m, refresh
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
		switch {
		case msg.err != nil:
			m.probes[msg.key] = radioProbe{status: "failed", code: "transport", message: "probe unavailable"}
		case msg.result.Status == "healthy":
			m.probes[msg.key] = radioProbe{status: "healthy", latency: msg.result.LatencyMs}
		default:
			m.probes[msg.key] = radioProbe{status: "failed", code: msg.result.ErrorCode, message: msg.result.Message}
		}
		m.logEvent("probe", map[string]any{"event": "done", "status": msg.result.Status, "err": msg.err != nil, "queue": len(m.probeQueue), "active": m.probeActive})
		next, cmd := m.pumpProbes()
		return next, cmd
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		next, cmd := m.handleKey(msg)
		model := next.(Model)
		updated, probeCmd := model.scheduleProbes()
		if probeCmd == nil {
			return updated, cmd
		}
		return updated, tea.Batch(cmd, probeCmd)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	l := m.layout()
	if m.overlay != "" {
		if (m.overlay == "help" || m.overlay == "info") &&
			(msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = -3
			}
			maxOffset := m.helpScrollMax()
			if maxOffset > 0 {
				m.helpOffset = clamp(m.helpOffset+delta, 0, maxOffset)
			}
			return m, nil
		}
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.overlay = ""
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		overList := msg.Y >= l.listTop && msg.Y < l.listTop+l.listHeight
		if !overList {
			return m, nil
		}
		queueInMain := m.queueFocus && !l.showPanel
		overPanel := (l.showPanel && msg.X >= l.mainWidth+1) || queueInMain
		if overPanel {
			if len(m.state.Queue) == 0 {
				return m, nil
			}
			m.queueFocus = true
			last := len(m.state.Queue) - 1
			m.queueCursor = clamp(m.queueCursor+delta, 0, last)
			return m, nil
		}
		m.queueFocus = false
		return m.moveBy(delta), nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.input.Focused() && msg.Y != l.headerRows-1 {
		m.input.Blur()
		m.inputMode = ""
	}
	if msg.Y == 0 {
		if source, ok := sourceTabAt(msg.X); ok {
			return m.switchSource(source)
		}
		return m, nil
	}
	if msg.Y == 1 {
		if index, ok := m.viewTabAt(msg.X); ok {
			return m.selectView(index)
		}
		return m, nil
	}
	if msg.Y < l.listTop || msg.Y >= l.listTop+l.listHeight {
		return m, nil
	}
	row := msg.Y - l.listTop - 1
	if row < 0 {
		return m, nil
	}
	rows := l.listHeight - 2
	queueInMain := m.queueFocus && !l.showPanel
	overPanel := (l.showPanel && msg.X >= l.mainWidth+1) || queueInMain
	if overPanel {
		if len(m.state.Queue) == 0 {
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
	items := m.visibleItems()
	if len(items) == 0 {
		return m, nil
	}
	start, _ := window(clamp(m.selected, 0, len(items)-1), len(items), rows)
	index := start + row
	if index >= len(items) {
		return m, nil
	}
	already := !m.queueFocus && m.selected == index
	m.queueFocus = false
	m.selected = index
	if already {
		return m.activate()
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Do not let an invisible, latent UI react while View can only render the
	// resize notice. WindowSizeMsg is handled by Update before reaching here.
	if m.tinyTerminal() {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			return m, nil
		}
	}
	m.logEvent("key", map[string]any{"key": msg.String(), "inputFocused": m.input.Focused()})
	if m.overlay == "theme" {
		return m.handleThemeKey(msg)
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
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.input.Blur()
			m.inputMode = ""
			return m, nil
		case "enter":
			return m.submitInput()
		case "tab", "shift+tab":
			if m.inputMode == "filter" {
				m.filter = strings.TrimSpace(m.input.Value())
			}
			m.input.Blur()
			m.inputMode = ""
			return m.switchSource(otherSource(m.source))
		case "[", "]":
			if m.inputMode == "filter" {
				m.filter = strings.TrimSpace(m.input.Value())
			}
			m.input.Blur()
			m.inputMode = ""
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
		return m, nil
	case "G", "end":
		m.selected = lastSelectableIndex(m.visibleItems())
		return m, nil
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
	case " ", "c":
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
		return m, m.toggleShuffle()
	case "R":
		if m.state.IsLive {
			return m.withToast("Repeat applies to Apple Music only", true)
		}
		return m, m.cycleRepeat()
	case "e", "E":
		if item, ok := m.selectedItem(); ok && item.Kind == "stream" {
			return m.withToast("Live radio streams cannot be queued", true)
		}
		if msg.String() == "e" {
			return m, m.enqueueSelected("next")
		}
		return m, m.enqueueSelected("tail")
	case "f":
		return m.toggleFavorite()
	case "a":
		if m.source == "radio" {
			m.inputMode = "url"
			m.input.Prompt = "Stream URL: "
			m.input.Placeholder = "https://stream.example/live"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil
	case "F":
		if m.source == "radio" {
			// Radio reserves the f-family for favorite/unfavorite only.
			return m, nil
		}
		m.queueFocus = false
		m.inputMode = "filter"
		m.input.Prompt = "Filter: "
		m.input.Placeholder = "substring to match"
		m.input.SetValue(m.filter)
		m.input.Focus()
		return m, textinput.Blink
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
		m.inputMode = "search"
		m.input.Prompt = "Search: "
		m.input.Placeholder = "type a query and press Enter"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
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
			m.selected = 0
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
			if m.cache != nil {
				delete(m.cache, "radio/Browse")
			}
			m.browseQuery = radioDiscovery{}
			m.view, m.title = "Browse", "Popular Worldwide"
			m.lastView["radio"] = "Browse"
			m.items, m.selected, m.loading = nil, 0, true
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
	if m.cache != nil {
		delete(m.cache, "radio/Browse")
	}
	m.browseQuery = query
	m.view, m.title = "Browse", query.browseTitle()
	m.lastView["radio"] = "Browse"
	m.items, m.selected, m.loading = nil, 0, true
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

func (m Model) handleDiscoveryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		if m.discoverySelected == discoveryText && len(msg.Runes) > 0 {
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
			if m.discoverySelected >= discoveryConfirm {
				m.discoverySelected = discoveryReset
			} else {
				m.discoverySelected = clamp(m.discoverySelected-1, discoveryText, discoveryReset)
			}
		case "down", "j":
			if m.discoverySelected < discoveryReset {
				m.discoverySelected++
			} else if m.discoverySelected == discoveryReset {
				m.discoverySelected = discoveryConfirm
			}
		case "left", "right", "h", "l":
			if m.discoverySelected == discoveryConfirm {
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
			case discoveryReset:
				m.discoveryPending = radioDiscovery{}
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
		if len(msg.Runes) > 0 {
			m.discoveryQuery += string(msg.Runes)
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

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	mode := m.inputMode
	value := strings.TrimSpace(m.input.Value())
	m.logEvent("submit", map[string]any{"mode": mode, "valueLength": len(value), "valueKind": map[bool]string{true: "url", false: "text"}[mode == "url"]})
	m.input.Blur()
	m.inputMode = ""
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
		m.selected = 0
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
	title := "Help · any other key closes · q quit"
	rows := m.helpLines(inner)
	if m.overlay == "info" {
		title = "Track Info · any other key closes · q quit"
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

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
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

func (m Model) handleThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	width      int
	height     int
	headerRows int
	listTop    int
	listHeight int
	showPanel  bool
	mainWidth  int
	panelWidth int
	nowHeight  int
}

func (m Model) layout() layout {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	trailer := 0
	if m.message != "" {
		trailer = 1
	}
	headerRows := 2
	if m.input.Focused() {
		headerRows = 3
	}
	bodyHeight := height - headerRows - 1 - trailer
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	nowHeight := 8
	if bodyHeight < 14 {
		nowHeight = bodyHeight / 2
	}
	if m.state.Track == nil && !m.busy && nowHeight > 3 {
		nowHeight = 3
		if m.account != "" {
			nowHeight = 4
		}
	}
	if nowHeight < 3 {
		nowHeight = 3
	}
	listHeight := bodyHeight - nowHeight
	if listHeight < 5 {
		listHeight = 5
		nowHeight = bodyHeight - listHeight
	}
	showPanel := activeAppleQueue(m.state) && width >= 88
	mainWidth, panelWidth := width, 0
	if showPanel {
		panelWidth = clamp(width/3, 30, 40)
		mainWidth = width - panelWidth - 1
	}
	return layout{width: width, height: height, headerRows: headerRows, listTop: headerRows, listHeight: listHeight, showPanel: showPanel, mainWidth: mainWidth, panelWidth: panelWidth, nowHeight: nowHeight}
}

func (m Model) View() string {
	l := m.layout()
	if l.width < 24 || l.height < 8 {
		return tinyView(l.width, l.height)
	}
	if m.overlay != "" {
		return m.overlayView(l.width, l.height)
	}
	width, height := l.width, l.height
	header := []string{m.sourceLine(width), m.viewLine(width)}
	if m.input.Focused() {
		header = append(header, m.input.View())
	}
	showPanel := l.showPanel
	listHeight := l.listHeight
	mainTitle, mainLines := m.listTitle(), m.listLines(width-2, listHeight-2)
	mainActive := !m.queueFocus
	if m.queueFocus && !showPanel {
		mainTitle = m.queueTitle()
		mainLines = m.queueLines(width-2, listHeight-2)
		mainActive = true
	}
	var listBox string
	if showPanel {
		panelWidth := l.panelWidth
		mainWidth := l.mainWidth
		mainBox := strings.Split(renderBox(mainTitle, m.listLines(mainWidth-2, listHeight-2), mainWidth, listHeight, mainActive), "\n")
		panelBox := strings.Split(renderBox(m.queueTitle(), m.queueLines(panelWidth-2, listHeight-2), panelWidth, listHeight, m.queueFocus), "\n")
		joined := make([]string, len(mainBox))
		for i := range mainBox {
			joined[i] = mainBox[i] + " " + panelBox[i]
		}
		listBox = strings.Join(joined, "\n")
	} else {
		listBox = renderBox(mainTitle, mainLines, width, listHeight, mainActive)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, listBox,
		renderBox(m.nowTitle(), m.nowLines(width-2, l.nowHeight-2), width, l.nowHeight, false))
	lines := append([]string{}, header...)
	lines = append(lines, strings.Split(body, "\n")...)
	if m.message != "" {
		style := accentStyle
		if m.messageErr {
			style = errorStyle
		}
		lines = append(lines, style.Render(fit(m.message, width)))
	}
	lines = append(lines, m.footerLine(width))
	// Keep Bubble Tea from scrolling when terminal dimensions are tiny or a
	// focused input makes the header taller than the viewport.
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", max(0, width)))
	}
	return strings.Join(lines, "\n")
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

func (m Model) tinyTerminal() bool {
	return (m.width > 0 && m.width < 24) || (m.height > 0 && m.height < 8)
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
	if m.loading {
		title += " loading…"
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
	if len(items) == 0 {
		return []string{tabStyle.Render(fit(m.emptyText(), width))}
	}
	start, end := window(m.selected, len(items), rows)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		item := items[i]
		if item.Kind == "header" {
			lines = append(lines, accentStyle.Render(fit("── "+item.Title+" ──", width)))
			continue
		}
		label := item.Title
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
		if radioFavorite {
			label += " " + accentStyle.Render("★")
		}
		if item.Kind == "stream" || item.Kind == "station" {
			text, style := m.probeSegment(item)
			label += " — " + style.Render(text)
			if item.Artist != "" {
				label += " · " + item.Artist
			}
		} else if item.Artist != "" {
			label += " — " + item.Artist
		}
		if appleFavorite {
			label = accentStyle.Render("★") + " " + label
		}
		if strings.HasPrefix(m.title, "Search: ") || m.viewKey() == "apple-music/Home" {
			label = kindGlyph(item.Kind) + label
		}
		current := m.source == "apple-music" && m.detailKind == "playlist" &&
			m.queueSource.Kind == "playlist" && m.queueSource.ID == m.detailID &&
			m.state.Track != nil && item.ID == m.state.Track.ID
		if current {
			label = "▶ " + label
		}
		switch {
		case i == m.selected:
			lines = append(lines, selStyle.Render(fit("> "+label, width)))
		case current:
			lines = append(lines, currentStyle.Render(fit("  "+label, width)))
		default:
			lines = append(lines, rowStyle.Render(fit("  "+label, width)))
		}
	}
	return lines
}

func (m Model) queueTitle() string {
	source := m.queueSource.Title
	if source == "" {
		source = "Queue"
	}
	return fmt.Sprintf("Up Next · %d/%d · %s", m.state.QueueIndex+1, len(m.state.Queue), source)
}

func (m Model) queueLines(width, rows int) []string {
	if len(m.state.Queue) == 0 {
		return []string{tabStyle.Render(fit("(empty)", width))}
	}
	anchor := m.state.QueueIndex
	if m.queueFocus {
		anchor = m.queueCursor
	}
	start, end := window(clamp(anchor, 0, len(m.state.Queue)-1), len(m.state.Queue), rows)
	lines := make([]string, 0, end-start)
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
		lines = append(lines, style.Render(fit(marker+label, width)))
	}
	return lines
}

func (m Model) nowTitle() string {
	if m.busy {
		return "Now Playing · starting…"
	}
	if m.state.IsLive {
		return "Now Playing · LIVE"
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
			return []string{loadingStyle.Render(fit("Starting playback…", width))}
		}
		lines := []string{tabStyle.Render(fit("Nothing playing", width))}
		if m.account != "" {
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
		lines = append(lines, line(fmt.Sprintf("LIVE · %s · %s", status, emptyDash(m.state.Format))))
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
		// MusicKit reports a stale paused/stopped snapshot while a play
		// command is still starting; show a single unambiguous status instead
		// of "paused · working…".
		busyStarting := (status == "stopped" && m.busy) ||
			(status == "paused" && m.state.Mode == "full" && m.state.Position <= 0)
		if busyStarting {
			status = "starting"
		}
		stateLine := fmt.Sprintf("State: %s · Mode: %s · Format: %s", status, m.state.Mode, emptyDash(format))
		if flags := m.modeFlags(); flags != "" {
			stateLine += " · " + flags
		}
		if m.busy && !busyStarting {
			stateLine += " · working…"
		}
		lines = append(lines, line(stateLine))
		if activeAppleQueue(m.state) {
			source := m.queueSource.Title
			if source == "" {
				source = "Queue"
			}
			queueLine := fmt.Sprintf("From: %s · %d/%d", source, m.state.QueueIndex+1, len(m.state.Queue))
			if m.state.Shuffle {
				queueLine += " · shuffle"
			}
			queueLine += " · 0 Up Next"
			lines = append(lines, line(queueLine))
		}
		if m.account != "" {
			lines = append(lines, line(m.account))
		}
		if len(m.state.Available) > 0 {
			lines = append(lines, line("Available: "+strings.Join(m.state.Available, ", ")))
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
	if m.source == "radio" && m.view == "Browse" && m.browseQuery != (radioDiscovery{}) {
		segments = append(segments, "esc popular")
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
			}
			confirmLabel := discoveryConfirmLabel(m.discoveryPending, m.discoveryTerm)
			// Short terminals cannot fit the decorated layout; drop spacers and
			// dividers so the action buttons never fall outside the box.
			compact := height > 2 && height-2 < 13
			switch {
			case compact:
				rows = []string{
					titleStyle.Render("Search"),
					field(0, "Text", discoverySearchValue(m.discoveryTerm)),
					titleStyle.Render("Filters"),
					field(1, "Language", discoveryValue(m.discoveryPending.Language)),
					field(2, "Genre", discoveryValue(m.discoveryPending.Tag)),
					field(3, "Country", discoveryCountryValue(m.discoveryPending)),
					field(4, "Reset filters", ""),
					"  " + button(5, confirmLabel) + "  " + button(6, "Cancel"),
					dimStyle.Render(hint),
				}
				selectedRow = []int{1, 3, 4, 5, 6, 7, 7}[clamp(m.discoverySelected, 0, 6)]
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
					field(4, "Reset filters", ""),
					"",
					"  " + button(5, confirmLabel) + "  " + button(6, "Cancel"),
					dimStyle.Render(hint),
				}
				selectedRow = []int{2, 6, 7, 8, 9, 11, 11}[clamp(m.discoverySelected, 0, 6)]
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
				rows = []string{"Loading..."}
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
	entries := [][2]string{
		{"tab", "switch source (Apple Music / Radio)"},
		{"1 - 9", "select sub-view"},
		{"0", "focus or leave the Up Next panel"},
		{"[ / ]", "cycle sub-view"},
		{"j / k", "move selection (Up Next: move queue cursor)"},
		{"g / G", "jump to top or bottom"},
		{"enter", "open playlist/station or play (Up Next: jump)"},
		{"p", "play selected; toggles pause on the playing item (Up Next: jump)"},
		{"x", "remove the focused Up Next track"},
		{"J / K", "reorder the focused Up Next track"},
		{"space / c", "pause or resume (Up Next focused: c clears)"},
		{"n / b", "next or previous (Apple Music)"},
		{"v", "stop"},
		{"s / R", "shuffle / repeat"},
		{"e / E", "queue next / append (Apple Music)"},
		{"f", "favorite / unfavorite (lilt-local list)"},
		{"a", "add a stream URL to Favorites and play it (Radio)"},
		{"/", "Apple Music search; Radio Search & Filters"},
		{"F", "filter current Apple Music list"},
		{"t", "theme picker"},
		{"i", "track info"},
		{"esc / backspace / h", "back or clear filter (Up Next: leave panel)"},
		{"q", "quit"},
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		for _, row := range wrapHelpRow(entry[0], entry[1], 16, width) {
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
	add("Mode", m.state.Mode)
	add("Auth", emptyDash(m.authorization))
	if m.account != "" {
		add("Account", strings.TrimPrefix(m.account, "Account: "))
	}
	add("Live", fmt.Sprintf("%v", m.state.IsLive))
	add("Format", m.state.Format)
	add("Shuffle", fmt.Sprintf("%v", m.state.Shuffle))
	add("Repeat", m.state.Repeat)
	add("Position", fmt.Sprintf("%.0f / %.0f s", m.state.Position, m.state.Duration))
	add("Queue", fmt.Sprintf("%d entries, index %d", len(m.state.Queue), m.state.QueueIndex))
	if activeAppleQueue(m.state) {
		add("Up Next", "0 focus; Enter/p jump; x remove")
	}
	return lines
}

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
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
