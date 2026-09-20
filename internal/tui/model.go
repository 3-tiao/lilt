// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

type Provider interface {
	Search(context.Context, string, int) ([]core.Item, error)
	SearchPlaylists(context.Context, string, int) ([]core.Item, error)
	SearchSource(context.Context, string, string, string, int) ([]core.Item, error)
	TrendingSource(context.Context, string, string, int) ([]core.Item, error)
	Sources(context.Context) ([]api.SourceDescriptor, error)
	LibraryPlaylists(context.Context) ([]core.Item, error)
	LibraryPlaylistsSource(context.Context, string) ([]core.Item, error)
	LibraryAlbumsSource(context.Context, string) ([]core.Item, error)
	PlaylistTracks(context.Context, string) ([]core.Item, error)
	PlaylistTracksSource(context.Context, string, string) ([]core.Item, error)
	AlbumTracksSource(context.Context, string, string) (core.Item, []core.Item, error)
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

// Remote performs server-owned state mutations so the TUI never writes
// state.json itself. It is nil in tests that use a local store directly.
type Remote interface {
	SetLastSource(context.Context, string) error
	SetTheme(context.Context, string) error
	SetFavorite(context.Context, string, core.Item, bool) error
	AuthorizationStatus(context.Context, string) (core.AuthorizationStatus, error)
}

type Player interface {
	core.PlaybackTarget
	core.Authorizer
	SetShuffle(context.Context, bool) (core.PlaybackState, error)
	SetRepeat(context.Context, string) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	Enqueue(context.Context, core.PlaybackRequest, string, uint64) (core.PlaybackState, error)
	PlaySongs(context.Context, []string, int) (core.PlaybackState, error)
	QueueJump(context.Context, int, uint64) (core.PlaybackState, error)
	QueueRemove(context.Context, int, uint64) (core.PlaybackState, error)
	QueueMove(context.Context, int, int, uint64) (core.PlaybackState, error)
	QueueClear(context.Context, uint64) (core.PlaybackState, error)
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
	trending    []core.Item
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
	addFavorite     bool
	refreshView     bool
}
type sourcesMsg struct {
	descriptors []api.SourceDescriptor
	sequence    uint64
	err         error
}

type authorizationMsg struct {
	source   string
	status   core.AuthorizationStatus
	sequence uint64
	err      error
}
type tickMsg struct{ at time.Time }
type toastMsg struct{ seq int }
type sourceSwitchMsg struct {
	operationID uint64
	phase       string
	state       core.PlaybackState
	err         error
	source      string
}
type persistenceMsg struct {
	operationID         uint64
	kind, source, theme string
	item                core.Item
	favorited           bool
	note                string
	err                 error
}
type watchMsg struct{ update api.WatchUpdate }
type watchClosedMsg struct{}

type page struct {
	source, view, title, detailKind, detailID, pageClass, filter string
	items                                                        []core.Item
	selected, listOffset                                         int
}

type navigationSnapshot struct {
	source, view, title, detailKind, detailID, pageClass, filter string
	items                                                        []core.Item
	history                                                      []page
	cache                                                        map[string][]core.Item
	lastView                                                     map[string]string
	selected, listOffset                                         int
	loading, pageLoading, pageFailed                             bool
	listErr                                                      string
	pageOffset                                                   int
	pageMore                                                     bool
	pageKey                                                      string
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
	// can never disagree about what fits. The shell is symmetric: one blank
	// inset row above the identity band and one below the footer.
	consoleHeaderRows = 2
	consoleMinWidth   = 44
	minWorkspaceRows  = 5 // borders plus 3 content rows
	canvasInsetRows   = 1 // top and bottom canvas margin
	bandGapRows       = 1 // fixed separator between vertical bands
	nowBoxRows        = 4 // NOW PLAYING box: border + 2 body rows + border
	feedbackRows      = 1 // toast band; always present, silent when empty
	footerRows        = 1

	radioProbeWorkers    = 2
	radioProbeTimeoutMs  = 10000
	radioProbeRPCTimeout = 12 * time.Second
	radioPageSize        = 100
	radioPageThreshold   = 3

	// doubleClickWindow is the standard mouse double-click interval: two
	// consecutive clicks on the same row within this window are one
	// double-click and activate the row; anything else is a fresh select.
	doubleClickWindow = 500 * time.Millisecond
)

// queueContext identifies the list that created the current Apple Music queue.
// It is intentionally separate from PlaybackState because MusicKit does not
// consistently expose that source container in its state snapshots.
type queueContext struct {
	Kind, ID, Title string
}

// lastClick remembers the previous mouse click so a consecutive same-row pair
// within doubleClickWindow forms one double-click gesture. It is mouse-only
// state: keyboard activation never consults it.
type lastClick struct {
	target string
	index  int
	at     time.Time
}

var amViews = []string{"Home", "Recent"}
var radioViews = []string{"Home", "Browse", "Recent"}
var audiusViews = []string{"Home", "Discover", "Recent"}

type Options struct {
	Provider       Provider
	Player         Player
	Radio          RadioProvider
	Remote         Remote
	RadioCache     *radio.Cache
	Store          *state.Store
	Authorization  core.AuthorizationStatus
	InitialTerm    string
	AutoPlay       bool
	Source         string
	Log            func(kind string, fields map[string]any)
	InitialWatch   *api.WatchSnapshot
	WatchUpdates   <-chan api.WatchUpdate
	StartupWarning string
}

type Model struct {
	provider   Provider
	player     Player
	radio      RadioProvider
	remote     Remote
	radioCache *radio.Cache
	store      *state.Store
	// activity is the client-side mirror of the server's activity store; the
	// server replaces it on every state.changed snapshot.
	activity *activityMirror
	input    textinput.Model
	renderer renderer

	source     string
	view       string
	title      string
	items      []core.Item
	selected   int
	listOffset int
	history    []page

	state         core.PlaybackState
	queueSource   queueContext
	authorization string
	sourceAuth    core.AuthorizationStatus
	descriptors   []api.SourceDescriptor
	// playbackStartedAt marks when the current session first reported buffering or
	// playing, so a slow URL/stream start can read as "connecting" first.
	playbackStartedAt time.Time
	account           string

	width, height  int
	loading        bool
	listErr        string
	busy           bool
	busySince      time.Time
	persisting     bool
	pendingSource  string
	previousSource string
	sourceRestore  *navigationSnapshot

	message    string
	messageErr bool
	toastSeq   int

	overlay         string
	overlaySelected int
	filter          string
	inputMode       string
	lastView        map[string]string
	// alignedToPlayback records that the first playback snapshot has been seen.
	// On launch the browsing source is moved to whatever is actually playing (a
	// navigation-only change that does not stop playback), so opening the TUI
	// while another source plays does not tempt the user into a stop-and-switch.
	alignedToPlayback   bool
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

	detailKind string
	detailID   string
	// pageClass is the page's activation intent, never inferred from its
	// display title: a container is one deliberately opened sequence
	// (album/playlist), an aggregate is a query-result list. Enter on a song
	// plays the rest of a container but only the row in an aggregate
	// (docs/ui/model.md §6).
	pageClass   string
	queueFocus  bool
	queueCursor int
	queueIntent string
	queueTarget int
	// queueOffset is the visible window start for the focused Up Next panel.
	// Like the main list, it only moves when the cursor would leave the window,
	// so clicks, wheeling and keyboard movement all stay in place.
	queueOffset    int
	queueOffsetSet bool

	themeNames []string
	themeIndex int
	themeName  string

	autoPlay             bool
	sequence             uint64
	watchUpdates         <-chan api.WatchUpdate
	appRevision          uint64
	hasInitialWatch      bool
	startupPersistSource string
	snapshotAt           time.Time
	renderTime           time.Time
	lastClick            lastClick
	connected            bool
	generation           uint64
	actionClock          uint64
	operationID          uint64

	log func(kind string, fields map[string]any)
}

// beginAction advances command ownership synchronously, before asynchronous
// work can run. Model copies share the clock, so issuing a newer action makes
// every older result stale even if its completion wins the scheduler race.
func beginAction(id uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if action, ok := msg.(actionMsg); ok {
			action.actionID = id
			return action
		}
		return msg
	}
}

func (m Model) acquireMutation() (Model, uint64, bool) {
	if m.busy || m.persisting {
		return m, 0, false
	}
	m.actionClock++
	m.operationID = m.actionClock
	m.busy = true
	m.busySince = time.Now()
	return m, m.operationID, true
}

func (m Model) ownsMutation(id uint64) bool {
	return id != 0 && m.busy && m.operationID == id
}

func (m Model) releaseMutation(id uint64) Model {
	if id == 0 || m.operationID == id {
		m.busy, m.persisting, m.operationID, m.busySince = false, false, 0, time.Time{}
	}
	return m
}

func (m Model) startMutation(build func(*Model) tea.Cmd) (tea.Model, tea.Cmd) {
	next, _, ok := m.acquireMutation()
	if !ok {
		return m.withToast("Another playback or source action is still running", true)
	}
	cmd := build(&next)
	if cmd == nil {
		next = next.releaseMutation(next.operationID)
	}
	return next, cmd
}

func New(opts Options) Model {
	loadedTheme := theme.Load(opts.Store.Theme)
	if opts.RadioCache == nil {
		opts.RadioCache = radio.NewCache("")
	}
	in := textinput.New()
	in.Prompt = "Search: "
	in.Placeholder = "type a query and press Enter"
	in.SetValue(opts.InitialTerm)
	in.Blur()
	renderer := newRenderer(loadedTheme)
	in.SetStyles(inputStyles(renderer))
	source := opts.Source
	if source != "radio" && source != "audius" {
		source = "apple-music"
	}
	m := Model{
		provider:        opts.Provider,
		player:          opts.Player,
		radio:           opts.Radio,
		remote:          opts.Remote,
		radioCache:      opts.RadioCache,
		store:           opts.Store,
		input:           in,
		renderer:        renderer,
		source:          source,
		view:            viewsFor(source)[0],
		authorization:   opts.Authorization.Status,
		account:         accountSummary(opts.Authorization),
		autoPlay:        opts.AutoPlay,
		filter:          "",
		log:             opts.Log,
		lastView:        map[string]string{source: viewsFor(source)[0]},
		cache:           map[string][]core.Item{},
		probes:          map[string]radioProbe{},
		state:           core.PlaybackState{Status: "stopped", Mode: "preview", Authorization: opts.Authorization.Status},
		watchUpdates:    opts.WatchUpdates,
		renderTime:      time.Now(),
		message:         presentation.Text(opts.StartupWarning),
		messageErr:      opts.StartupWarning != "",
		connected:       true,
		hasInitialWatch: opts.InitialWatch != nil,
	}
	if opts.InitialWatch != nil {
		m.sequence = opts.InitialWatch.Sequence
		m.state = presentation.Playback(apiPlaybackToCore(opts.InitialWatch.Playback))
		m.snapshotAt = m.renderTime
		m.descriptors = append([]api.SourceDescriptor(nil), opts.InitialWatch.Sources...)
		if opts.InitialWatch.State != nil {
			m.applyAppState(*opts.InitialWatch.State)
		}
		for _, authorization := range opts.InitialWatch.Authorizations {
			if string(authorization.Source) == m.source {
				m.sourceAuth = authorizationToCore(authorization)
			}
		}
		m.alignedToPlayback = true
		if playbackSource := m.playbackSource(); playbackActive(m.state.Status) && playbackSource != "" && playbackSource != m.source {
			m.source = playbackSource
			m.view, m.title = "Home", "Home"
			m.lastView[playbackSource] = "Home"
			m.startupPersistSource = playbackSource
			m.actionClock++
			m.operationID, m.busy, m.persisting = m.actionClock, true, true
		}
	}
	if opts.Store.Theme != "" {
		m.themeName = opts.Store.Theme
	} else {
		m.themeName = loadedTheme.Name
	}
	m.title = m.view
	if m.message == "" && m.account == "" {
		m.message = "Apple Music, Audius & radio — s switches source, / searches, : commands"
	}
	m.loading = true
	m.loadLocalView()
	return m
}

func viewsFor(source string) []string {
	switch source {
	case "radio":
		return radioViews
	case "audius":
		return audiusViews
	}
	return amViews
}

func (m Model) views() []string {
	views := []string{"Home"}
	if source := m.source; source == "radio" && m.declares(source, api.CapSearchRadio) {
		views = append(views, "Browse")
	} else if m.declares(source, api.CapSearchTrending) {
		views = append(views, "Discover")
	}
	return append(views, "Recent")
}

// accountSummary returns a one-line explanation when Apple Music is not fully
// usable, and an empty string when everything is ready.
func accountSummary(status core.AuthorizationStatus) string {
	if status.Status != "authorized" {
		switch status.Status {
		case "not_determined":
			return "Account: Apple Music access not granted — preview mode (press s to switch source)"
		case "denied":
			return "Account: Apple Music access denied — preview mode (press s to switch source)"
		case "restricted":
			return "Account: Apple Music restricted on this device"
		default:
			// Unknown/transient statuses are not actionable; showing "unavailable"
			// while playback works would be misleading.
			return ""
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

func (m Model) accountOrReady() string {
	if summary := sourceAccountSummary(m.source, m.sourceAuth); summary != "" {
		return summary
	}
	if m.source == "apple-music" && m.account != "" {
		return m.account
	}
	return "Account: ready"
}

// sourceAccountSummary describes the current source's authorization. Apple
// reuses the MusicKit summary; Audius is an optional account link.
func sourceAccountSummary(source string, status core.AuthorizationStatus) string {
	switch source {
	case "apple-music":
		return accountSummary(status)
	case "audius":
		switch status.Status {
		case "authorized":
			if status.AccountLabel != "" {
				return "Account: " + status.AccountLabel
			}
			return "Account: linked"
		case "not_determined":
			return "Account: not linked (optional — run `lilt auth audius`)"
		case "expired":
			return "Account: link expired — run `lilt auth audius`"
		case "":
			return ""
		default:
			return "Account: unavailable"
		}
	case "radio":
		return "Account: not required"
	default:
		return ""
	}
}

func apiItemToCore(item api.Item) core.Item {
	return core.Item{Source: string(item.Source), Kind: item.Kind, ID: item.ProviderID, Ref: item.Ref, URL: item.URL, Title: item.Title, Artist: item.Artist, PreviewURL: item.PreviewURL}
}

func apiPlaybackToCore(value api.PlaybackState) core.PlaybackState {
	state := core.PlaybackState{Source: string(value.Source), Position: value.Position, Duration: value.Duration, Status: value.Status, AudioVariant: value.AudioVariant, Format: value.Format, Available: value.Available, Shuffle: value.Shuffle, Repeat: value.Repeat, IsLive: value.IsLive, Mode: value.Mode, QueueIndex: value.QueueIndex, QueueRevision: value.QueueRevision}
	if value.Track != nil {
		track := apiItemToCore(*value.Track)
		state.Track = &track
	}
	for _, item := range value.Queue {
		state.Queue = append(state.Queue, apiItemToCore(item))
	}
	if value.PlaybackError != nil {
		state.Error = *value.PlaybackError
	}
	if value.StreamTitle != nil {
		state.StreamTitle = *value.StreamTitle
	}
	if value.StreamArtist != nil {
		state.StreamArtist = *value.StreamArtist
	}
	return state
}

func authorizationToCore(value api.SourceAuthorization) core.AuthorizationStatus {
	return core.AuthorizationStatus{Status: value.Status, AccountLabel: value.AccountLabel}
}

func (m *Model) applyAppState(value api.AppState) {
	if value.Revision < m.appRevision {
		return
	}
	m.appRevision = value.Revision
	if m.store == nil {
		return
	}
	m.store.Theme, m.store.LastSource = value.Theme, string(value.LastSource)
	m.themeName = value.Theme
	*m = m.setTheme(value.Theme)
	mirror := &activityMirror{}
	mirror.favorites = append(mirror.favorites, value.Favorites...)
	mirror.recent = append(mirror.recent, value.Recent...)
	m.activity = mirror
	// Derived views (favorites preview/page, Recent) must never survive a
	// server commit as cached lists: the mirror is the only current copy.
	for _, source := range []string{"apple-music", "audius", "radio"} {
		delete(m.cache, source+"/Favorites")
		delete(m.cache, source+"/Recent")
	}
}

func (m Model) fetchSources() tea.Cmd {
	if m.provider == nil {
		return nil
	}
	sequence := m.sequence
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		descriptors, err := provider.Sources(ctx)
		return sourcesMsg{descriptors: descriptors, sequence: sequence, err: err}
	}
}

// declares reports whether the current capability snapshot marks a source's
// capability available. Unknown descriptors fall back to false so the UI never
// claims an unsupported feature.
func (m Model) declares(source, capability string) bool {
	descriptor, ok := m.descriptor(source)
	if !ok {
		return false
	}
	return descriptor.Capabilities[capability].Available
}

// declaresOrUnknown is the optimistic form used while the capability snapshot
// has not arrived yet: Home must not drop the library/trending preview just
// because sources.list is still in flight. Once the snapshot exists, unknown
// sources are treated as unsupported.
func (m Model) declaresOrUnknown(source, capability string) bool {
	return len(m.descriptors) == 0 || m.declares(source, capability)
}

func (m Model) descriptor(source string) (api.SourceDescriptor, bool) {
	for _, descriptor := range m.descriptors {
		if string(descriptor.ID) == source {
			return descriptor, true
		}
	}
	return api.SourceDescriptor{}, false
}

func (m Model) sourceChoices() []string {
	choices := make([]string, 0, len(m.descriptors))
	for _, descriptor := range m.descriptors {
		choices = append(choices, string(descriptor.ID))
	}
	if len(choices) == 0 && m.source != "" {
		return []string{m.source}
	}
	return choices
}

func (m Model) sourceSwitchable(source string) (bool, string) {
	descriptor, ok := m.descriptor(source)
	if !ok {
		return false, "Source is not present in the latest server snapshot"
	}
	if !descriptor.Available {
		if descriptor.Reason != "" {
			return false, descriptor.Reason
		}
		return false, "Source is unavailable"
	}
	for _, capability := range []string{api.CapPlaybackFull, api.CapPlaybackPreview, api.CapPlaybackStream} {
		if descriptor.Capabilities[capability].Available {
			return true, ""
		}
	}
	return false, "Source has no available playback capability"
}

func (m Model) fetchAuthorization() tea.Cmd {
	if m.remote == nil {
		return nil
	}
	source := m.source
	sequence := m.sequence
	remote := m.remote
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		status, err := remote.AuthorizationStatus(ctx, source)
		return authorizationMsg{source: source, status: status, sequence: sequence, err: err}
	}
}

func (m Model) Init() tea.Cmd {
	commands := []tea.Cmd{tick()}
	if !m.hasInitialWatch {
		if cmd := m.fetchAuthorization(); cmd != nil {
			commands = append(commands, cmd)
		}
		if cmd := m.fetchSources(); cmd != nil {
			commands = append(commands, cmd)
		}
	}
	if m.watchUpdates != nil {
		commands = append(commands, waitForWatchUpdate(m.watchUpdates))
	}
	if m.startupPersistSource != "" {
		commands = append(commands, m.persistLastSourceCmd(m.startupPersistSource, m.operationID))
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
	return tea.Tick(250*time.Millisecond, func(at time.Time) tea.Msg { return tickMsg{at: at} })
}

func waitForWatchUpdate(updates <-chan api.WatchUpdate) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return watchClosedMsg{}
		}
		return watchMsg{update: update}
	}
}

func (m Model) applyWatchUpdate(update api.WatchUpdate) (tea.Model, tea.Cmd) {
	rearm := waitForWatchUpdate(m.watchUpdates)
	if update.Err != nil || update.Sequence <= m.sequence {
		return m, rearm
	}
	m.sequence, m.connected = update.Sequence, true
	var follow tea.Cmd
	switch update.Kind {
	case "playback.changed":
		if update.Playback != nil {
			m = m.setState(apiPlaybackToCore(*update.Playback)).refreshQueueCursor()
		}
	case "state.changed":
		if update.State != nil {
			m.applyAppState(*update.State)
			// Derived views must follow the server commit live: Recent gains a
			// row the moment a play qualifies, All Favorites tracks favorite
			// commits, Home is composed from both.
			if m.view == "Home" || m.view == "Favorites" || m.view == "Recent" {
				m.loading, m.generation = true, m.generation+1
				follow = m.loadView()
			}
		}
	case "sources.changed":
		m.descriptors = append([]api.SourceDescriptor(nil), update.Sources...)
		if !contains(m.views(), m.view) && len(m.history) == 0 {
			m.view, m.title, m.items, m.loading = "Home", "Home", nil, true
			m.generation++
			follow = m.loadView()
		} else if m.view == "Home" {
			m.loading, m.generation = true, m.generation+1
			follow = m.loadView()
		}
	case "authorization.changed":
		if update.Authorization != nil && string(update.Authorization.Source) == m.source {
			m.sourceAuth = authorizationToCore(*update.Authorization)
		}
	case "server.warning":
		message := update.WarningMessage
		if update.WarningCode != "" {
			message = update.WarningCode + ": " + message
		}
		m.message, m.messageErr = "Warning: "+presentation.Text(message), true
	case "engine.restarted":
		m.message, m.messageErr = "Playback engine restarted — waiting for authoritative state", false
	case "server.shuttingDown":
		m.connected = false
		m.message, m.messageErr = "Server is shutting down", true
	}
	if follow == nil {
		return m, rearm
	}
	return m, tea.Batch(rearm, follow)
}

// setState records a canonical helper snapshot and when it was received.
func (m Model) setState(playbackState core.PlaybackState) Model {
	previous := m.state
	m.state = presentation.Playback(playbackState)
	active := m.state.Status == "buffering" || m.state.Status == "playing"
	wasActive := previous.Status == "buffering" || previous.Status == "playing"
	if active && (!wasActive || !sameTrackIdentity(previous.Track, m.state.Track)) {
		m.playbackStartedAt = m.renderTime
	}
	if m.state.Authorization != "" {
		m.authorization = m.state.Authorization
		m.account = accountSummary(core.AuthorizationStatus{Status: m.state.Authorization, AccountStatus: m.state.AccountStatus, AccountError: m.state.AccountError})
	}
	m.snapshotAt = m.renderTime
	return m
}

const (
	operationTimeout = 20 * time.Second
	// connectingWindow is how long a starting session still reads as
	// "connecting" before it is called "buffering".
	connectingWindow = 1500 * time.Millisecond
)

func sameTrackIdentity(a, b *core.Item) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.URL == b.URL
}

// connecting reports a just-started session that has not produced audio yet.
func (m Model) connecting() bool {
	return m.state.Status == "buffering" && !m.playbackStartedAt.IsZero() && m.renderTime.Sub(m.playbackStartedAt) < connectingWindow
}

// boundedContext gives generic mutations their operation budget.
func boundedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}

// playbackStartTimeout bounds playback-start mutations only. Starting a finite
// queue is paced per track server-side (~1s per track for a "play from here"
// page), which runs well past the generic 20s budget; a tighter limit made the
// TUI report a failure while the server was still filling the queue
// (batch 2026-09-19-watch-sync-recheck NEW-H3).
const playbackStartTimeout = 60 * time.Second

func boundedStartContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), playbackStartTimeout)
}

func (m Model) destination() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d", m.source, m.view, m.detailKind, m.detailID, m.pageClass, len(m.history))
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

func (m Model) searchSource(term string) tea.Cmd {
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
			return pushMsg{err: songErr}
		}
		return pushMsg{items: grouped(songs, albums, playlists)}
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

// stableItemID is the client-side view of the server's persistent identity. It
// delegates to the shared api.Identity so a TUI highlight can never disagree
// with a stored favorite or history row.
func stableItemID(source string, item core.Item) string {
	return api.NewIdentity(api.SourceID(source), item.Kind, item.ID, item.URL).StableID
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
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		var state core.PlaybackState
		var err error
		var note string
		switch action {
		case "jump":
			state, err = m.player.QueueJump(ctx, index, m.state.QueueRevision)
		case "remove":
			if index >= 0 && index < len(m.state.Queue) {
				if index == m.state.QueueIndex {
					note = "Removed current track — playback advanced"
				} else {
					note = "Removed: " + m.state.Queue[index].Title
				}
			}
			state, err = m.player.QueueRemove(ctx, index, m.state.QueueRevision)
		case "movedown":
			note = "Queue reordered"
			state, err = m.player.QueueMove(ctx, index, index+1, m.state.QueueRevision)
		case "moveup":
			note = "Queue reordered"
			state, err = m.player.QueueMove(ctx, index, index-1, m.state.QueueRevision)
		}
		return actionMsg{state: state, err: err, note: note, afterSequence: m.sequence}
	})
}

func (m Model) queueClear() tea.Cmd {
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.QueueClear(ctx, m.state.QueueRevision)
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
	// Home combines live state and local history; never serve a stale page.
	if m.view != "Home" && key != "radio/Browse" {
		if items, ok := m.cache[key]; ok {
			return func() tea.Msg { return listMsg{key: key, title: m.view, items: items} }
		}
	}
	switch {
	case m.view == "Home":
		return m.loadHome()
	case m.view == "Favorites":
		items := m.activity.FavoritesFor(m.source)
		return func() tea.Msg {
			return listMsg{key: key, title: "All Favorites", items: items}
		}
	case key == "apple-music/Recent":
		items := m.activity.RecentFor("apple-music")
		return func() tea.Msg {
			return listMsg{key: key, title: "Recent", items: items}
		}
	case key == "audius/Discover":
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			songs, songErr := m.provider.TrendingSource(ctx, "audius", "song", 20)
			playlists, playlistErr := m.provider.TrendingSource(ctx, "audius", "playlist", 20)
			if songErr != nil && playlistErr != nil {
				return listMsg{key: key, title: "Discover", err: songErr}
			}
			items := make([]core.Item, 0, len(songs)+len(playlists)+2)
			if len(songs) > 0 {
				items = append(items, core.Item{Kind: "header", Title: "Trending Songs"})
				items = append(items, songs...)
			}
			if len(playlists) > 0 {
				items = append(items, core.Item{Kind: "header", Title: "Trending Playlists"})
				items = append(items, playlists...)
			}
			return listMsg{key: key, title: "Discover", items: items}
		}
	case key == "audius/Recent":
		recent := m.activity.RecentFor("audius")
		return func() tea.Msg {
			return listMsg{key: key, title: "Recent", items: recent}
		}
	case key == "radio/Recent":
		recent := recentWithTitle(m.activity.RecentFor("radio"))
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

// Page classes carry the page's activation intent so Enter never depends on
// display text. A container is one sequence the user deliberately opened
// (album/playlist): Enter plays the row and the rest of that container. An
// aggregate is a query-result list, which is evidence for the query rather than
// an intent to play all of it: Enter plays only the row (docs/ui/model.md §6).
const (
	pageClassContainer = "container"
	pageClassAggregate = "aggregate"
)

func homeItems(source string, playback core.PlaybackState, queueSource string, recent, trending, playlists, favorites []core.Item) []core.Item {
	const sectionLimit = 5
	// The current fixed source catalog is the availability boundary for these
	// optional previews. Callers may supply cached slices, but a slice from a
	// different capability must never make an unavailable Home section appear.
	items := make([]core.Item, 0, 16)
	if activeAppleQueue(playback) {
		// The section header already says "Continue Playing", so the row is just
		// the current track; the footer's `0 Up Next` covers the queue hint.
		title := queueSource
		if playback.Track != nil && playback.Track.Title != "" {
			title = playback.Track.Title
		}
		artist := fmt.Sprintf("%d/%d · Up Next", playback.QueueIndex+1, len(playback.Queue))
		items = append(items, core.Item{Kind: "continue", Title: title, Artist: artist})
	}
	if len(items) > 0 {
		items = append([]core.Item{{Kind: "header", Title: "Continue Playing"}}, items...)
	}
	if len(recent) > sectionLimit {
		recent = recent[:sectionLimit]
	}
	if len(recent) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Recently Played"})
		items = append(items, recent...)
	}
	if len(trending) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Trending"})
		items = append(items, trending[:min(sectionLimit, len(trending))]...)
	}
	if len(playlists) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Your Playlists"})
		if len(playlists) > sectionLimit {
			playlists = playlists[:sectionLimit]
		}
		items = append(items, playlists...)
	}
	if len(favorites) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Favorites"})
		items = append(items, favorites[:min(sectionLimit, len(favorites))]...)
	}
	entries := []core.Item{{Kind: "entry-search", Title: "Search", Artist: "/"}}
	if source == "radio" {
		entries = append(entries, core.Item{Kind: "browse", ID: "Browse", Title: "Browse"})
	}
	if source == "audius" {
		entries = append(entries, core.Item{Kind: "browse", ID: "Discover", Title: "Discover"})
	}
	entries = append(entries, core.Item{Kind: "browse", ID: "Recent", Title: "Recent"})
	entries = append(entries, core.Item{Kind: "entry-favorites", Title: "All Favorites", Artist: "Local"})
	if source == "apple-music" || source == "audius" {
		entries = append(entries, core.Item{Kind: "entry-playlists", Title: "All Playlists", Artist: "Library"})
	}
	if source == "apple-music" {
		entries = append(entries, core.Item{Kind: "entry-albums", Title: "Albums", Artist: "Library"})
	}
	if activeAppleQueue(playback) {
		entries = append(entries, core.Item{Kind: "continue", Title: "Queue", Artist: "Up Next"})
	}
	if source == "apple-music" || source == "audius" {
		entries = append(entries, core.Item{Kind: "entry-account", Title: "Account"})
	}
	items = append(items, core.Item{Kind: "header", Title: "Go to"})
	items = append(items, entries...)
	return items
}

func (m Model) gateHomeItems(items []core.Item) []core.Item {
	if len(m.descriptors) == 0 {
		return items
	}
	allowTrending := m.declares(m.source, api.CapSearchTrending)
	allowLibrary := m.declares(m.source, api.CapLibrary)
	allowQueue := m.declares(m.source, api.CapQueue)
	result := make([]core.Item, 0, len(items))
	skipSection := false
	for _, item := range items {
		if item.Kind == "header" {
			skipSection = (item.Title == "Trending" && !allowTrending) || (item.Title == "Your Playlists" && !allowLibrary) || (item.Title == "Continue Playing" && !allowQueue)
			if !skipSection {
				result = append(result, item)
			}
			continue
		}
		if skipSection {
			continue
		}
		switch item.Kind {
		case "entry-playlists", "entry-albums":
			if !allowLibrary {
				continue
			}
		case "continue":
			if !allowQueue {
				continue
			}
		case "browse":
			if item.ID == "Discover" && !allowTrending {
				continue
			}
			if item.ID == "Browse" && !m.declares(m.source, api.CapSearchRadio) {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

// recentWithTitle drops stream history recorded before its name resolved;
// such rows render blank and cannot be replayed meaningfully.
func recentWithTitle(recent []core.Item) []core.Item {
	kept := make([]core.Item, 0, len(recent))
	for _, item := range recent {
		if strings.TrimSpace(item.Title) != "" {
			kept = append(kept, item)
		}
	}
	return kept
}

func (m Model) loadHome() tea.Cmd {
	source := m.source
	playlists := append([]core.Item(nil), m.cache[source+"/Library"]...)
	recent := append([]core.Item(nil), m.activity.RecentFor(source)...)
	favorites := append([]core.Item(nil), m.activity.FavoritesFor(source)...)
	playback, queueTitle := m.state, m.queueSource.Title
	provider := m.provider
	loadTrending := m.declaresOrUnknown(source, api.CapSearchTrending)
	loadLibrary := m.declaresOrUnknown(source, api.CapLibrary)
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		// Recent is the source-scoped local history; recent.list is cross-source
		// and MUST NOT leak other sources into a per-source view.
		trending := []core.Item(nil)
		if loadTrending {
			trending, _ = provider.TrendingSource(ctx, source, "song", 5)
		}
		if loadLibrary && len(playlists) == 0 {
			playlists, _ = provider.LibraryPlaylistsSource(ctx, source)
			sortByName(playlists)
		}
		return homeMsg{items: homeItems(source, playback, queueTitle, recent, trending, playlists, favorites), playlists: playlists, trending: trending}
	}
}

// openLibraryPlaylists pushes the full account-playlist list for sources whose
// library can be read; Home only shows a capped preview.
func (m Model) openLibraryPlaylists() tea.Cmd {
	source := m.source
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		items, err := m.provider.LibraryPlaylistsSource(ctx, source)
		sortByName(items)
		return pushMsg{title: "Playlists", items: items, err: err}
	}
}

// pushFavorites opens the full local favorites list as its own view so
// favorites changes reload it live (applyAppState watches view == "Favorites").
func (m Model) pushFavorites() (tea.Model, tea.Cmd) {
	next, cmd := m.push("All Favorites", m.openFavorites())
	child := next.(Model)
	child.view = "Favorites"
	return child, stampLoad(cmd, child.generation, child.destination())
}

// openFavorites pushes the full local favorites list for the current source;
// Home only shows a capped preview.
func (m Model) openFavorites() tea.Cmd {
	source := m.source
	return func() tea.Msg {
		return pushMsg{title: "All Favorites", items: m.activity.FavoritesFor(source)}
	}
}

// openAlbum pushes the album detail page: the album's songs with Enter meaning
// "play from here" (docs/ui/ux.md). The album row is not repeated as a list
// row — the page header and context row already carry its identity.
func (m Model) openAlbum(item core.Item) tea.Cmd {
	ref := item.Ref
	if ref == "" {
		ref = m.source + ":album:" + item.ID
	}
	source := m.source
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		_, tracks, err := m.provider.AlbumTracksSource(ctx, source, ref)
		if err != nil {
			return pushMsg{err: err}
		}
		return pushMsg{items: tracks}
	}
}

// openLibraryAlbums pushes the account's albums. Only Apple's library exposes
// them today, so the entry is Apple-only.
func (m Model) openLibraryAlbums() tea.Cmd {
	source := m.source
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		items, err := m.provider.LibraryAlbumsSource(ctx, source)
		sortByName(items)
		return pushMsg{title: "Albums", items: items, err: err}
	}
}

func (m Model) openPlaylist(item core.Item) tea.Cmd {
	ref := item.Ref
	if ref == "" {
		ref = m.source + ":playlist:" + item.ID
	}
	source := m.source
	title := item.Title
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		tracks, err := m.provider.PlaylistTracksSource(ctx, source, ref)
		if err == nil && source == "apple-music" && reversePlaylistOrder(title) {
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

// playbackRequestFor carries the item's canonical ref across the client boundary.
// Local state items predate Ref, so derive the documented stable ref only when it
// is absent; discovered items always retain the server-provided value.
func playbackRequestFor(item core.Item, source string) core.PlaybackRequest {
	request := core.PlaybackRequest{Ref: item.Ref, Kind: item.Kind, ID: item.ID, URL: item.URL}
	if request.Ref == "" && source != "radio" && item.Kind != "" {
		id := strings.TrimPrefix(item.ID, source+":"+item.Kind+":")
		request.Ref = source + ":" + item.Kind + ":" + id
	}
	return request
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
		return beginAction(m.operationID, func() tea.Msg {
			ctx, cancel := boundedStartContext()
			defer cancel()
			playback, err := m.player.RadioPlay(ctx, item.URL, item.Title)
			return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: "radio", recentItem: &item}
		})
	default:
		source := m.source
		queueCtx := &queueContext{}
		if item.Kind == "playlist" {
			queueCtx = &queueContext{Kind: "playlist", ID: item.ID, Title: item.Title}
		}
		return beginAction(m.operationID, func() tea.Msg {
			ctx, cancel := boundedStartContext()
			defer cancel()
			request := playbackRequestFor(item, source)
			playback, err := m.player.PlayState(ctx, request)
			return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: queueCtx, recentSource: source, recentItem: &item}
		})
	}
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
		m = m.centerQueueWindow()
		return m, nil
	case "playlist":
		if m.source == "apple-music" || m.source == "audius" {
			return m.pushContainer("playlist", item.ID, item.Title, m.openPlaylist(item))
		}
	case "browse":
		for index, view := range m.views() {
			if view == item.ID {
				return m.selectView(index)
			}
		}
		return m, nil
	case "entry-search":
		if m.source == "radio" {
			m.overlay, m.discoverySelected, m.discoveryQuery = "discovery", 0, ""
			return m, nil
		}
		return m.openTextInput("search", "Search: ", "type a query and press Enter", "")
	case "entry-account":
		return m.withToast(m.accountOrReady(), false)
	case "entry-favorites":
		next, cmd := m.pushFavorites()
		return next, cmd
	case "entry-playlists":
		next, cmd := m.push("Playlists", m.openLibraryPlaylists())
		return next, cmd
	case "entry-albums":
		next, cmd := m.push("Albums", m.openLibraryAlbums())
		return next, cmd
	case "album":
		return m.pushContainer("album", item.ID, item.Title, m.openAlbum(item))
	case "song":
		if m.detailKind == "playlist" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playPlaylistFrom(item) })
		}
		if m.detailKind == "album" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playAlbumFrom(item) })
		}
		// A query-result page is not a container the user assembled, so Enter
		// plays only the pointed row; surfaces keep the "keep listening" run.
		if m.pageClass == pageClassAggregate {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playSelected() })
		}
		// In a list, Enter means "play from here": queue this song and the rest
		// of its section, so the user keeps listening instead of getting one track.
		if refs, ok := m.playRefsFromSelected(); ok {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playSongsFrom(refs, item) })
		}
	}
	return m.startMutation(func(next *Model) tea.Cmd { return next.playSelected() })
}

// playAlbumFrom queues the album starting at the selected song. The server
// resolves the album ref through the helper's album path (playbackRequestFor
// carries the kind), so the queue fills with the album's remaining songs.
func (m Model) playAlbumFrom(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": "albumFrom", "titleLength": len(item.Title)})
	startAt := m.selectedOriginalIndex()
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedStartContext()
		defer cancel()
		request := core.PlaybackRequest{Ref: m.source + ":album:" + m.detailID, Kind: "album", ID: m.detailID, StartAt: startAt, StartTrackID: item.ID, FromHere: true}
		playback, err := m.player.PlayState(ctx, request)
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{Kind: "album", ID: m.detailID, Title: m.title}}
	})
}

// playAlbum starts the album in the open detail page from its first track.
// Albums are not recorded as recent containers: only playlists reopen as a
// stored context (docs/internals/state.md).
func (m Model) playAlbum(shuffle bool) tea.Cmd {
	title := m.title
	m.logEvent("play", map[string]any{"itemKind": "album", "titleLength": len(title), "shuffle": shuffle})
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedStartContext()
		defer cancel()
		request := core.PlaybackRequest{Ref: m.source + ":album:" + m.detailID, Kind: "album", ID: m.detailID}
		if shuffle && m.declares(m.source, api.CapShuffle) {
			request.Shuffle = &shuffle
			request.Repeat = "all"
		}
		playback, err := m.player.PlayState(ctx, request)
		note := ""
		if shuffle {
			note = "Shuffling: " + title
		}
		return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{Kind: "album", ID: m.detailID, Title: title}}
	})
}

func (m Model) playPlaylistFrom(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": "playlistFrom", "titleLength": len(item.Title)})
	container := core.Item{Source: m.source, Kind: "playlist", ID: m.detailID, Ref: m.source + ":playlist:" + m.detailID, Title: m.title}
	startAt := m.selectedOriginalIndex()
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedStartContext()
		defer cancel()
		request := core.PlaybackRequest{Ref: m.source + ":playlist:" + m.detailID, Kind: "playlist", ID: m.detailID, StartAt: startAt, StartTrackID: item.ID, FromHere: true}
		if m.source == "apple-music" {
			request.Reverse = reversePlaylistOrder(m.title)
		}
		playback, err := m.player.PlayState(ctx, request)
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{Kind: "playlist", ID: m.detailID, Title: m.title}, recentContainer: &container}
	})
}

// playRefsFromSelected returns canonical refs for the contiguous run of songs
// from the selected row to the end of its section. It reports false when fewer
// than two songs are playable, so a lone song stays a single play.
func (m Model) playRefsFromSelected() ([]string, bool) {
	if m.source == "radio" {
		return nil, false
	}
	index := m.selectedOriginalIndex()
	if index < 0 || index >= len(m.items) || m.items[index].Kind != "song" {
		return nil, false
	}
	refs := make([]string, 0, len(m.items)-index)
	for i := index; i < len(m.items); i++ {
		item := m.items[i]
		if item.Kind != "song" || item.Ref == "" {
			break
		}
		refs = append(refs, item.Ref)
	}
	if len(refs) < 2 {
		return nil, false
	}
	return refs, true
}

func (m Model) playSongsFrom(refs []string, first core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": "listFrom", "count": len(refs)})
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedStartContext()
		defer cancel()
		playback, err := m.player.PlaySongs(ctx, refs, 0)
		return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: m.source, recentItem: &first}
	})
}

func (m Model) playPlaylist(shuffle bool) tea.Cmd {
	title := m.title
	m.logEvent("play", map[string]any{"itemKind": "playlist", "titleLength": len(title), "shuffle": shuffle})
	container := core.Item{Source: m.source, Kind: "playlist", ID: m.detailID, Ref: m.source + ":playlist:" + m.detailID, Title: title}
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedStartContext()
		defer cancel()
		// Apply shuffle as part of the play request so it lands on the playlist
		// being started, not on a stale server active source from a stopped
		// session. A separate setShuffle would target whatever source was last
		// active and fail (for example Audius does not support shuffle).
		request := core.PlaybackRequest{Ref: m.source + ":playlist:" + m.detailID, Kind: "playlist", ID: m.detailID}
		if m.source == "apple-music" {
			request.Reverse = reversePlaylistOrder(title)
		}
		if shuffle && m.declares(m.source, api.CapShuffle) {
			request.Shuffle = &shuffle
			request.Repeat = "all"
		}
		playback, err := m.player.PlayState(ctx, request)
		// Say what S did: it restarts the playlist shuffled, which rebuilds the
		// queue and replaces the current track (batch 2026-09-19-watch-sync-recheck
		// NEW-M2).
		note := ""
		if shuffle {
			note = "Shuffling: " + title
		}
		return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{Kind: "playlist", ID: m.detailID, Title: title}, recentContainer: &container}
	})
}

// pushContainer opens a deliberately chosen album or playlist: Enter keeps the
// "play from here to the end of the container" contract.
func (m Model) pushContainer(kind, id, title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	next, cmd := m.push(title, cmd)
	child := next.(Model)
	child.detailKind, child.detailID, child.pageClass = kind, id, pageClassContainer
	return child, stampLoad(cmd, child.generation, child.destination())
}

// pushAggregate opens a query-result page: the list is evidence for the query,
// so Enter plays only the selected row.
func (m Model) pushAggregate(title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	next, cmd := m.push(title, cmd)
	child := next.(Model)
	child.detailKind, child.detailID, child.pageClass = "", "", pageClassAggregate
	return child, stampLoad(cmd, child.generation, child.destination())
}

// push optimistically opens a child page and shows the loading state.
func (m Model) push(title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.history = append(m.history, page{source: m.source, view: m.view, title: m.title, detailKind: m.detailKind, detailID: m.detailID, pageClass: m.pageClass, filter: m.filter, items: m.items, selected: m.selected, listOffset: m.listOffset})
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
	if m.detailKind == "album" &&
		m.queueSource.Kind == "album" && m.queueSource.ID == m.detailID {
		return item.ID != "" && item.ID == m.state.Track.ID
	}
	if m.detailKind == "playlist" &&
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
	return beginAction(m.operationID, func() tea.Msg {
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
	return beginAction(m.operationID, func() tea.Msg {
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
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.SetRepeat(ctx, mode)
		return actionMsg{state: state, err: err, note: "Repeat " + mode, afterSequence: m.sequence}
	})
}

func (m Model) stopPlayback() tea.Cmd {
	m.logEvent("control", map[string]any{"action": "stop"})
	return beginAction(m.operationID, func() tea.Msg {
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
	if position == "next" {
		label = "Playing next"
	}
	return beginAction(m.operationID, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		state, err := m.player.Enqueue(ctx, playbackRequestFor(item, m.source), position, m.state.QueueRevision)
		if err != nil {
			return actionMsg{err: err, afterSequence: m.sequence}
		}
		return actionMsg{state: state, note: label + ": " + item.Title, afterSequence: m.sequence}
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
	view := "Home"
	if source != "radio" {
		view = m.lastView[source]
		if !contains(m.views(), view) {
			view = "Home"
		}
	}
	m.view = view
	m.title = view
	m.lastView[source] = view
	// A source change is a new navigation session. Do not carry cached results,
	// pushed pages, or a local filter across providers.
	m.cache, m.history, m.filter = map[string][]core.Item{}, nil, ""
	m.items, m.listErr = nil, ""
	m.selected, m.listOffset = 0, 0
	m.loading = true
	m.queueFocus = false
	m.detailKind, m.detailID, m.pageClass = "", "", ""
	if m.source == "radio" && m.view == "Browse" {
		m.resetBrowsePaging()
	}
	m.generation++
	_ = m.loadLocalView()
	m.logEvent("navigate", map[string]any{"action": "source"})
	return m, nil
}

func clonePages(values []page) []page {
	result := append([]page(nil), values...)
	for i := range result {
		result[i].items = append([]core.Item(nil), result[i].items...)
	}
	return result
}

func cloneItemCache(values map[string][]core.Item) map[string][]core.Item {
	result := make(map[string][]core.Item, len(values))
	for key, items := range values {
		result[key] = append([]core.Item(nil), items...)
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (m Model) navigationSnapshot() navigationSnapshot {
	return navigationSnapshot{
		source: m.source, view: m.view, title: m.title, detailKind: m.detailKind, detailID: m.detailID, pageClass: m.pageClass,
		filter: m.filter, items: append([]core.Item(nil), m.items...), history: clonePages(m.history), cache: cloneItemCache(m.cache),
		lastView: cloneStringMap(m.lastView),
		selected: m.selected, listOffset: m.listOffset, loading: m.loading, listErr: m.listErr,
		pageLoading: m.pageLoading, pageFailed: m.pageFailed, pageOffset: m.pageOffset, pageMore: m.pageMore, pageKey: m.pageKey,
	}
}

func (m Model) restoreNavigation(value navigationSnapshot) Model {
	m.source, m.view, m.title, m.detailKind, m.detailID, m.pageClass, m.filter = value.source, value.view, value.title, value.detailKind, value.detailID, value.pageClass, value.filter
	m.items, m.history, m.cache = append([]core.Item(nil), value.items...), clonePages(value.history), cloneItemCache(value.cache)
	m.lastView = cloneStringMap(value.lastView)
	m.selected, m.listOffset, m.loading, m.listErr = value.selected, value.listOffset, value.loading, value.listErr
	m.pageLoading, m.pageFailed, m.pageOffset, m.pageMore, m.pageKey = value.pageLoading, value.pageFailed, value.pageOffset, value.pageMore, value.pageKey
	m.generation++
	return m
}

func (m Model) beginSourceSwitch(source string) (tea.Model, tea.Cmd) {
	if source == m.source {
		m.overlay = ""
		return m, nil
	}
	if ok, reason := m.sourceSwitchable(source); !ok {
		return m.withToast("Source unavailable: "+presentation.Text(reason), true)
	}
	previous := m.navigationSnapshot()
	var acquired bool
	m, _, acquired = m.acquireMutation()
	if !acquired {
		return m.withToast("Another playback or source action is still running", true)
	}
	m.pendingSource, m.previousSource, m.sourceRestore = source, m.source, &previous
	operationID := m.operationID
	if m.state.Status == "playing" || m.state.Status == "paused" || m.state.Status == "buffering" {
		player := m.player
		return m, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			state, err := player.Stop(ctx)
			return sourceSwitchMsg{operationID: operationID, phase: "stopped", state: state, err: err, source: source}
		}
	}
	state := m.state
	return m, func() tea.Msg {
		return sourceSwitchMsg{operationID: operationID, phase: "stopped", state: state, source: source}
	}
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
	views := m.views()
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
	m.detailKind, m.detailID, m.pageClass = "", "", ""
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

// jumpResultGroup moves the cursor to the first item of the next/previous
// header group on a pushed results page. It is a no-op when there is only one
// group.
func (m Model) jumpResultGroup(delta int) Model {
	headers := make([]int, 0, 4)
	for i, item := range m.items {
		if item.Kind == "header" {
			headers = append(headers, i)
		}
	}
	if len(headers) < 2 {
		return m
	}
	current := m.selectedOriginalIndex()
	target := -1
	if delta > 0 {
		for _, header := range headers {
			if header > current {
				target = header
				break
			}
		}
	} else {
		// Find the current group's header, then step to the one before it.
		currentHeader := -1
		for _, header := range headers {
			if header <= current {
				currentHeader = header
			}
		}
		for _, header := range headers {
			if header < currentHeader {
				target = header
			}
		}
	}
	if target < 0 {
		return m
	}
	index := target + 1
	for index < len(m.items) && !selectable(m.items[index]) {
		index++
	}
	if index >= len(m.items) {
		return m
	}
	m.filter = ""
	m.selected = index
	return m.keepMainSelectionVisible()
}

func (m Model) cycleView(delta int) (tea.Model, tea.Cmd) {
	views := m.views()
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
	m.detailKind, m.detailID, m.pageClass = "", "", ""
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
	m.pageClass = previous.pageClass
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
	case "audius/Recent":
		items = m.activity.RecentFor("audius")
	case "radio/Recent":
		items = recentWithTitle(m.activity.RecentFor("radio"))
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
				seen[stableItemID("radio", item)] = struct{}{}
			}
			for _, item := range presentation.Items(msg.items) {
				key := stableItemID("radio", item)
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
			// The favorites page reloads after every favorite commit; keep the
			// cursor on its (possibly shifted) row instead of jumping to the top.
			keepCursor := m.view == "Favorites" && len(m.history) > 0
			previousCursor := m.selected
			m.items = presentation.Items(msg.items)
			if msg.key == "radio/Browse" {
				m.items = m.sortRadioItems(m.items)
			}
			m.selected = 0
			if keepCursor && previousCursor >= 0 && previousCursor < len(m.items) {
				m.selected = previousCursor
			}
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
			m.cache[m.source+"/Library"] = presentation.Items(msg.playlists)
		}
		if m.view == "Home" && len(m.history) == 0 {
			m.title = "Home"
			m.items = presentation.Items(m.gateHomeItems(msg.items))
			m.selected = firstSelectableIndex(msg.items)
			m.filter = ""
		}
	case sourceSwitchMsg:
		if !m.ownsMutation(msg.operationID) {
			return m, nil
		}
		if msg.err != nil {
			m = m.releaseMutation(msg.operationID)
			m.pendingSource, m.sourceRestore = "", nil
			return m.withToast("Source switch failed: "+presentation.Text(msg.err.Error()), true)
		}
		if msg.phase == "stopped" {
			m = m.setState(msg.state)
			next, _ := m.switchSource(msg.source)
			m = next.(Model)
			m.persisting = true
			return m, m.persistLastSourceCmd(msg.source, msg.operationID)
		}
		return m, nil
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
		m.title = "Search: " + presentation.Text(msg.term)
		m.items = presentation.Items(msg.items)
		m.selected = firstSelectableIndex(msg.items)
		m.filter = ""
		if len(msg.items) > 0 {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playSelected() })
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
		if !m.ownsMutation(msg.actionID) {
			return m, nil
		}
		m = m.releaseMutation(msg.actionID)
		if msg.err != nil {
			m.message = playbackErrorText(msg.err)
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
		if msg.addFavorite && msg.recentItem != nil {
			var ok bool
			m, _, ok = m.acquireMutation()
			if !ok {
				return m.withToast("Playback started, but favorite could not be queued", true)
			}
			m.persisting = true
			return m, m.persistFavoriteCmd("radio", presentation.Item(*msg.recentItem), true, msg.note, m.operationID)
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
		m.renderTime = msg.at
		return m, tick()
	case sourcesMsg:
		if msg.err != nil || msg.sequence < m.sequence {
			return m, nil
		}
		m.descriptors = append([]api.SourceDescriptor(nil), msg.descriptors...)
		if !contains(m.views(), m.view) && len(m.history) == 0 {
			m.view, m.title, m.items = "Home", "Home", nil
			m.loading, m.generation = true, m.generation+1
			return m, m.loadView()
		}
		if m.view == "Home" && !m.loading {
			m.loading, m.generation = true, m.generation+1
			return m, m.loadView()
		}
		return m, nil
	case persistenceMsg:
		if !m.ownsMutation(msg.operationID) {
			return m, nil
		}
		m = m.releaseMutation(msg.operationID)
		switch msg.kind {
		case "source":
			if msg.err != nil {
				if m.sourceRestore != nil && m.source == msg.source {
					m = m.restoreNavigation(*m.sourceRestore)
				}
				m.pendingSource, m.previousSource, m.sourceRestore = "", "", nil
				return m.withToast("State save failed: "+presentation.Text(msg.err.Error()), true)
			}
			m.pendingSource, m.previousSource, m.sourceRestore, m.overlay = "", "", nil, ""
			m.loading = true
			return m, m.loadView()
		case "theme":
			if msg.err != nil {
				m.themeName = m.store.Theme
				return m.setTheme(m.store.Theme).withToast("State save failed: "+presentation.Text(msg.err.Error()), true)
			}
			m.overlay = ""
			return m.withToast("Theme: "+msg.theme, false)
		case "favorite":
			if msg.err != nil {
				return m.withToast("State save failed: "+presentation.Text(msg.err.Error()), true)
			}
			delete(m.cache, msg.source+"/Home")
			delete(m.cache, msg.source+"/Favorites")
			text := msg.note
			if text == "" && !msg.favorited {
				text = "Unfavorited: " + msg.item.Title
			}
			if text == "" && msg.favorited {
				text = "★ Favorited: " + msg.item.Title
			}
			next, toast := m.withToast(text, false)
			if next.view == "Home" || next.view == "Favorites" {
				next.loading, next.generation = true, next.generation+1
				return next, tea.Batch(toast, next.loadView())
			}
			return next, toast
		}
	case watchMsg:
		return m.applyWatchUpdate(msg.update)
	case watchClosedMsg:
		m.watchUpdates, m.connected = nil, false
		m.snapshotAt = time.Time{}
		if m.state.Status == "playing" || m.state.Status == "buffering" {
			m.state.Status = "disconnected"
		}
		m = m.clearProbesOnDisconnect()
		m.message, m.messageErr = "Server watch disconnected — quit and restart lilt to reconnect", true
		return m, nil
	case authorizationMsg:
		if msg.source == m.source && msg.err == nil && msg.sequence >= m.sequence {
			m.sourceAuth = msg.status
		}
		return m, nil
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

// viewTabAt maps an x coordinate on the VIEW row to a sub-view index.
// viewTabAt maps a click on the navigation row to a surface. The active
// surface renders with a `› ` marker, which widens its hit region.
func (m Model) viewTabAt(x int) (int, bool) {
	start := 0
	for i, view := range m.views() {
		label := fmt.Sprintf("%d %s", i+1, view)
		if view == m.view {
			label = "› " + label
		}
		width := lipgloss.Width(label)
		if x >= start && x < start+width {
			return i, true
		}
		start += width + lipgloss.Width(" · ")
	}
	return 0, false
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
		m.overlay, m.overlaySelected = "source-switcher", indexOf(m.sourceChoices(), m.source)
		m.lastClick = lastClick{}
		return m, nil
	}
	if y == 2 {
		if index, ok := m.viewTabAt(x); ok {
			m.lastClick = lastClick{}
			return m.selectView(index)
		}
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
	if m.overlay == "source-switcher" {
		return m.handleSourceSwitcherKey(msg)
	}
	if m.overlay == "palette" {
		return m.handlePaletteKey(msg)
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
				m.queueIntent = "clear"
				return m.startMutation(func(next *Model) tea.Cmd { return next.queueClear() })
			}
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
	if m.busy {
		switch msg.String() {
		case "enter", "p", "space", "c", "n", "b", "v", "S", "R", "e", "E", "f":
			return m.withToast("Playback action already in progress", true)
		}
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.selectView(int(msg.String()[0] - '1'))
	case "0":
		if !m.declares(m.source, api.CapQueue) || !activeAppleQueue(m.state) {
			return m.withToast("Nothing is queued", true)
		}
		m.queueFocus = true
		m.queueCursor = m.state.QueueIndex
		m = m.centerQueueWindow()
		return m, nil
	case "]":
		if len(m.history) > 0 {
			return m.jumpResultGroup(1), nil
		}
		return m.cycleView(1)
	case "[":
		if len(m.history) > 0 {
			return m.jumpResultGroup(-1), nil
		}
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
					return m.startMutation(func(next *Model) tea.Cmd { return next.control("pause") })
				}
				if m.state.Status == "paused" {
					return m.startMutation(func(next *Model) tea.Cmd { return next.control("resume") })
				}
			}
		}
		if m.detailKind == "playlist" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playPlaylist(false) })
		}
		if m.detailKind == "album" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playAlbum(false) })
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
		m.overlay, m.overlaySelected = "source-switcher", indexOf(m.sourceChoices(), m.source)
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
		if m.detailKind == "playlist" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playPlaylist(true) })
		}
		if m.detailKind == "album" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playAlbum(true) })
		}
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
		// Source switching is an explicit action (`s`), not a tab cycle.
		return m, nil
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

// persistTimeout bounds state-save commands. The server may be momentarily
// busy (for example probing freshly browsed radio stations); a tight budget
// here reported "State save failed" while the server still applied the write,
// and the user's retry toggle flipped the just-saved state
// (batch 2026-09-19-watch-sync-recheck NEW-M5).
const persistTimeout = 20 * time.Second

func (m Model) persistLastSourceCmd(source string, operationID uint64) tea.Cmd {
	remote := m.remote
	return func() tea.Msg {
		var err error
		if remote != nil {
			ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			defer cancel()
			err = remote.SetLastSource(ctx, source)
		}
		return persistenceMsg{operationID: operationID, kind: "source", source: source, err: err}
	}
}

func (m Model) persistThemeCmd(name string, operationID uint64) tea.Cmd {
	remote := m.remote
	return func() tea.Msg {
		var err error
		if remote != nil {
			ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			defer cancel()
			err = remote.SetTheme(ctx, name)
		}
		return persistenceMsg{operationID: operationID, kind: "theme", theme: name, err: err}
	}
}

func (m Model) persistFavoriteCmd(source string, item core.Item, favorited bool, note string, operationID uint64) tea.Cmd {
	remote := m.remote
	return func() tea.Msg {
		var err error
		if remote != nil {
			ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			defer cancel()
			err = remote.SetFavorite(ctx, source, item, favorited)
		}
		return persistenceMsg{operationID: operationID, kind: "favorite", source: source, item: item, favorited: favorited, note: note, err: err}
	}
}

func (m Model) toggleFavorite() (tea.Model, tea.Cmd) {
	if m.busy || m.persisting {
		return m.withToast("A state change is still being saved", true)
	}
	item, ok := m.selectedItem()
	if !ok || !selectable(item) {
		return m.withToast("Nothing selected", true)
	}
	if !favoritable(item) {
		return m.withToast("This row can't be favorited", true)
	}
	source := m.source
	if item.Kind == "stream" {
		source = "radio"
	}
	added := !m.activity.IsFavorite(source, stableItemID(source, item))
	var acquired bool
	m, _, acquired = m.acquireMutation()
	if !acquired {
		return m.withToast("Another playback or source action is still running", true)
	}
	m.persisting = true
	m.logEvent("favorite", map[string]any{"titleLength": len(item.Title), "on": added})
	return m, m.persistFavoriteCmd(source, presentation.Item(item), added, "", m.operationID)
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

// overlayView renders the overlay as a full-screen centered frame. content()
// composites overlayDialog over the live base frame instead, so overlays keep
// the shell visible behind them.
func (m Model) overlayView(width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.overlayDialog(width, height))
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
	title := "Help"
	rows := m.helpLines(inner)
	if m.overlay == "info" {
		title = "Track Info"
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
	contentRows := max(1, layout.visible-1)
	if layout.visible <= 0 || len(layout.rows) <= layout.visible {
		return 0
	}
	return len(layout.rows) - contentRows
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "?":
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
		m, _, ok = m.acquireMutation()
		if !ok {
			return m.withToast("Another playback or source action is still running", true)
		}
		m.persisting = true
		m.logEvent("theme", map[string]any{"name": m.themeName})
		return m, m.persistThemeCmd(m.themeName, m.operationID)
	case "esc":
		m.overlay = ""
		m.themeName = m.store.Theme
		return m.setTheme(m.store.Theme), nil
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
	listHeight int  // workspace rows; main list and Up Next rail share it
	nowTop     int  // first row of the full-width NOW PLAYING box
	nowHeight  int  // NOW PLAYING box rows, including borders
	showRail   bool // interactive Up Next rail beside the main list
	mainWidth  int
	panelWidth int
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
		return m.canvasFrame(consoleFrame(tinyView(l.width, l.height), l.width, l.gutter), l.width+2*l.gutter)
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
		return m.canvasFrame(consoleFrame(tinyView(l.width, l.height), l.width, l.gutter), l.width+2*l.gutter)
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
		body = m.renderPanel("Up Next", queueCount, m.queueLines(width-4, bodyRows), width, listHeight, true)
	} else if l.showRail {
		mainBox := m.renderPanel(m.listTitle(), m.listCount(), m.listLines(l.mainWidth-4, bodyRows), l.mainWidth, listHeight, mainActive)
		railBox := m.renderPanel("Up Next", queueCount, m.queueLines(l.panelWidth-4, bodyRows), l.panelWidth, listHeight, m.queueFocus)
		body = joinColumns(mainBox, railBox)
	} else {
		body = m.renderPanel(m.listTitle(), m.listCount(), m.listLines(width-4, bodyRows), width, listHeight, mainActive)
	}
	nowBox := m.renderPanel("Now Playing", "", m.nowBody(width-4), width, l.nowHeight, false)
	feedback := fit("", width)
	if m.message != "" {
		style := m.renderer.accentStyle
		if m.messageErr {
			style = m.renderer.errorStyle
		}
		feedback = style.Render(fit(m.message, width))
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
		body = m.renderPanel("Up Next", queueCount, m.queueLines(width-4, bodyRows), width, listHeight, true)
	} else if l.showRail {
		mainBox := m.renderPanel(m.listTitle(), m.listCount(), m.listLines(l.mainWidth-4, bodyRows), l.mainWidth, listHeight, mainActive)
		railBox := m.renderPanel("Up Next", queueCount, m.queueLines(l.panelWidth-4, bodyRows), l.panelWidth, listHeight, m.queueFocus)
		body = joinColumns(mainBox, railBox)
	} else {
		body = m.renderPanel(m.listTitle(), m.listCount(), m.listLines(width-4, bodyRows), width, listHeight, mainActive)
	}
	nowBox := m.renderPanel("Now Playing", "", m.nowBody(width-4), width, l.nowHeight, false)
	feedback := fit("", width)
	if m.message != "" {
		style := m.renderer.accentStyle
		if m.messageErr {
			style = m.renderer.errorStyle
		}
		feedback = style.Render(fit(m.message, width))
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
	}
	return "Apple Music"
}

func sourceCapabilitySummary(descriptor api.SourceDescriptor) string {
	labels := []struct{ capability, label string }{
		{api.CapPlaybackFull, "full"}, {api.CapPlaybackPreview, "preview"}, {api.CapPlaybackStream, "stream"},
		{api.CapQueue, "queue"}, {api.CapLibrary, "library"}, {api.CapSearchTrending, "trending"}, {api.CapSearchRadio, "browse"},
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

var sourceIDs = []string{"apple-music", "audius", "radio"}

func sourceIndex(source string) int { return indexOf(sourceIDs, source) }

func (m Model) handleSourceSwitcherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sources := m.sourceChoices()
	if len(sources) == 0 {
		return m.withToast("No source is currently available", true)
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

func (m Model) paletteCommands() []string {
	return []string{":home", ":discover", ":browse", ":recent", ":queue", ":auth", ":source apple-music", ":source audius", ":source radio", ":play <ref>", ":help"}
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
// candidate, or the raw typed text when it has free-form arguments that match
// no candidate (e.g. `play am:123`). Nothing highlighted and nothing typed is a
// no-op.
func (m Model) paletteCommandToRun() string {
	typed := strings.TrimSpace(m.input.Value())
	matches := m.paletteMatches()
	if m.overlaySelected < 0 || len(matches) == 0 {
		return typed
	}
	candidate := matches[clamp(m.overlaySelected, 0, len(matches)-1)]
	if typed != "" && !strings.HasPrefix(candidate[1:], typed) {
		return typed
	}
	return strings.TrimPrefix(candidate, ":")
}

func (m Model) runPaletteCommand(command string) (tea.Model, tea.Cmd) {
	switch {
	case command == "home":
		return m.selectView(indexOf(m.views(), "Home"))
	case command == "recent":
		return m.selectView(indexOf(m.views(), "Recent"))
	case command == "discover" && m.source == "audius":
		return m.selectView(indexOf(m.views(), "Discover"))
	case command == "browse" && m.source == "radio":
		return m.selectView(indexOf(m.views(), "Browse"))
	case command == "queue":
		if !activeAppleQueue(m.state) {
			return m.withToast("Nothing is queued", true)
		}
		m.queueFocus = true
		return m, nil
	case command == "auth":
		return m.withToast(m.accountOrReady(), false)
	case command == "help":
		m.overlay, m.helpOffset = "help", 0
		return m, nil
	case command == "source":
		return m.withToast("source requires a source id (apple-music, audius, radio)", true)
	case strings.HasPrefix(command, "source "):
		id := strings.TrimSpace(strings.TrimPrefix(command, "source "))
		if !contains(sourceIDs, id) {
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

// playbackSource is the committed playback session's source, independent of the
// selected tab.
func (m Model) playbackSource() string {
	if m.state.Source != "" {
		return m.state.Source
	}
	if m.state.Track != nil && m.state.Track.Source != "" {
		return m.state.Track.Source
	}
	return m.source
}

// playbackActive reports whether a status denotes a live playback session that
// a launch-time source alignment should follow.
func playbackActive(status string) bool {
	switch status {
	case "playing", "paused", "buffering":
		return true
	default:
		return false
	}
}

func (m Model) activeTopView() string {
	if len(m.history) > 0 {
		return ""
	}
	return m.view
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
		case strings.HasPrefix(m.title, "Search: "):
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
		case strings.HasPrefix(m.title, "Search: "):
			parts = append(parts, strings.TrimPrefix(m.title, "Search: "))
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
	case "radio/Recent", "apple-music/Recent", "audius/Recent":
		// The 30s listening threshold is server policy; the empty state must
		// say so or a just-listened user reads it as a failed record
		// (batch 2026-09-19-watch-sync-recheck NEW-M4).
		return "(empty) — tracks show here after 30s of listening"
	case "radio/Browse":
		return "(empty) — press / to search and filter stations"
	case "audius/Discover":
		return "(empty) — no trending available right now"
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
		} else if item.Artist != "" {
			metadata = " — " + item.Artist
			secondary = m.renderer.dimStyle.Render(metadata)
		}
		glyph := ""
		if strings.HasPrefix(m.title, "Search: ") || m.viewKey() == "apple-music/Home" {
			glyph = kindGlyph(item.Kind)
		}
		label, plainLabel := m.listLabel(item.Title, radioFavorite, appleFavorite, glyph)
		// The cursor column is rendered outside the row style so selection and the
		// playing highlight stay independent: the `›` marks the cursor, the style
		// marks playback, and neither paints over the other's gutter. The marker
		// carries the accent token: as bare text it would inherit the terminal's
		// foreground colour and vanish on a painted canvas. Every row then gets
		// one blank column of padding on each side so highlighted text never
		// touches the edge of its fill.
		cursor := "  "
		if i == m.selected {
			cursor = m.renderer.accentStyle.Render("› ")
		}
		textWidth := max(1, contentWidth-4)
		// The playing row carries the same ▶ marker as the Up Next rail, so
		// playback stays readable as text even where the row has no background.
		marker := ""
		if m.isPlayingItem(item) {
			marker = "▶ "
		}
		row := ""
		switch listRowKind(i == m.selected, marker != "") {
		case rowPlaying:
			row = cursor + m.renderer.currentStyle.Render(" "+fit(marker+plainLabel+metadata, textWidth)+" ")
		case rowSelected:
			row = cursor + m.renderer.selStyle.Render(" "+fit(plainLabel+metadata, textWidth)+" ")
		default:
			// Station health, codec, country and tags support comparison but are
			// secondary to the station/song name. Lower contrast makes long rows
			// scannable without throwing away that information.
			row = cursor + " " + fit(label+secondary, textWidth) + " "
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
func (m Model) listLabel(title string, radioFavorite, appleFavorite bool, glyph string) (styled, plain string) {
	// Every styled-form segment carries its own token. A nested style ends with
	// a reset, which would drop the outer row style for the rest of the label:
	// the title after a favorite star rendered in the terminal's own foreground
	// and vanished on a painted canvas. With per-segment styles the label is
	// safe in any wrapper — and needs no wrapper at all.
	styled, plain = m.renderer.rowStyle.Render(title), title
	if radioFavorite {
		styled += " " + m.renderer.accentStyle.Render("★")
		plain += " ★"
	}
	if appleFavorite {
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
func (m Model) queueTitle() string {
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
		state := "  "
		if i < m.state.QueueIndex {
			// Dimmed history uses a different glyph so played entries are not
			// mistaken for upcoming ones.
			state = "· "
		}
		if i == m.state.QueueIndex {
			state += "▶ "
		}
		// Mirror the main-list grammar: the selection cursor and the state marker
		// sit outside the row style, so the playing highlight never includes the
		// accent-marked gutter and both states stay independently readable.
		cursor := "  "
		if m.queueFocus && i == m.queueCursor {
			cursor = m.renderer.accentStyle.Render("› ")
		}
		// The playing highlight outranks the selection cursor (theme spec): a
		// cursor on the current entry keeps the playing colour and expresses
		// selection through the `>` marker, so the state never looks lost.
		style := m.renderer.rowStyle
		switch {
		case i == m.state.QueueIndex:
			style = m.renderer.currentStyle
		case i < m.state.QueueIndex:
			style = m.renderer.dimStyle
		case m.queueFocus && i == m.queueCursor:
			style = m.renderer.selStyle
		}
		// Filled rows keep one blank cell of padding on each side, matching the
		// main list (docs/ui/design-system.md §4).
		lines = append(lines, cursor+style.Render(" "+fit(state+label, contentWidth-2)+" ")+bar[len(lines)])
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

// nowBody renders the fixed two-row Now Playing contract: identity first,
// playback facts second. It never repeats the source, the queue, or page
// context, and it never grows beyond the two body rows the shell reserves.
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
	if m.busySince.IsZero() || m.renderTime.IsZero() {
		return "working…"
	}
	elapsed := int(m.renderTime.Sub(m.busySince).Seconds())
	if elapsed < 5 {
		return "working…"
	}
	return fmt.Sprintf("working… %ds — large queues are added track by track", elapsed)
}

func (m Model) nowBody(width int) []string {
	if m.state.Track == nil {
		if m.busy {
			return []string{m.renderer.loadingStyle.Render(fit(m.busyLabel(), width)), ""}
		}
		// An Apple Music authorization warning belongs to its own source. Showing
		// it in Radio's empty dock makes a working radio browser look broken.
		if m.account != "" && m.source == "apple-music" {
			return []string{m.renderer.tabStyle.Render(fit("Nothing playing", width)), m.renderer.rowStyle.Render(fit(m.account, width))}
		}
		return []string{m.renderer.tabStyle.Render(fit("Nothing playing", width)), ""}
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
	return []string{titleLine, m.playbackFacts(width)}
}

// playbackFacts renders the compact facts row: playback state, progress and
// time, the helper-reported current codec, and enabled playback modes. It is
// always one row; segments drop from the right when the terminal is narrow.
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
		stateSeg += m.renderer.dimStyle.Render(" — ") + m.renderer.errorStyle.Render(clip(m.state.Error, max(0, width-lipgloss.Width(stateSeg))))
		return fit(stateSeg, width)
	}
	elapsed := clock(m.displayPositionAt(m.renderTime))
	segs := []string{stateSeg, m.renderer.dimStyle.Render(elapsed)}
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
	if m.account != "" && m.source == "apple-music" && !m.state.IsLive && m.state.Mode != "full" {
		segs = append(segs, m.renderer.warnStyle.Render(clip(m.account, max(0, width-lipgloss.Width(strings.Join(segs, "  "))))))
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
	// Fixed row: drop right-side facts before shrinking the bar below legibility.
	for lipgloss.Width(strings.Join(segs, "  ")) > width && len(segs) > 3 {
		segs = slices.Delete(segs, len(segs)-1, len(segs))
	}
	return fit(strings.Join(segs, "  "), width)
}

type nowStatus struct {
	kind string
	text string
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
		if activeAppleQueue(m.state) {
			segments = append(segments, "0 Up Next")
		}
		if m.state.Track != nil {
			switch m.state.Status {
			case "playing", "buffering":
				segments = append(segments, "space pause", "v stop")
			case "paused":
				segments = append(segments, "space resume", "v stop")
			}
		}
		return append(segments, "esc back", "? help")
	}
	if m.listErr != "" {
		return []string{"r retry", "esc back", "/ search", "? help", "q quit"}
	}
	enterHint := "enter open/play"
	if m.pageClass != pageClassAggregate && m.detailKind != "playlist" && m.detailKind != "album" {
		if refs, ok := m.playRefsFromSelected(); ok && len(refs) > 1 {
			enterHint = "enter play from here"
		}
	}
	segments := []string{enterHint, "p play"}
	// A selected row that can be queued advertises the two queue keys. Search
	// results are the main place a reader chains tracks now that Enter plays
	// only the pointed row (batch 2026-09-20-search-and-queue N1), so the keys
	// must be visible without opening help.
	if m.declares(m.source, api.CapQueue) {
		if item, ok := m.selectedItem(); ok && queuable(item) {
			segments = append(segments, "e next · E append")
		}
	}
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
	if m.store != nil {
		if item, ok := m.selectedItem(); ok {
			source := m.source
			if item.Kind == "stream" || item.Kind == "station" {
				source = "radio"
			}
			hint := "f favorite"
			if m.activity.IsFavorite(source, stableItemID(source, item)) {
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
	segments = append(segments, "? help", "s source", ": commands", "1-9 view", "q quit")
	return segments
}

func (m Model) footerLine(width int) string {
	segments := m.footerSegments()
	if len(segments) == 0 {
		return m.renderer.tabStyle.Render(fit("", width))
	}
	line := segments[0]
	for _, segment := range segments[1:] {
		candidate := line + " · " + segment
		if lipgloss.Width(candidate) > width {
			break
		}
		line = candidate
	}
	return m.renderer.tabStyle.Render(fit(line, width))
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
			if i == m.overlaySelected {
				style = selStyle
			}
			rows = append(rows, style.Render("  "+m.sourceChoiceLabel(source)))
		}
		rows = append(rows, dimStyle.Render("Enter/click switch · Esc cancel"))
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
			for i, command := range matches {
				if i == m.overlaySelected {
					rows = append(rows, selStyle.Render("› "+command))
				} else {
					rows = append(rows, tabStyle.Render("  "+command))
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
		}
		boxWidth := min(64, max(24, width-4))
		inner := boxWidth - 2
		input := m.input
		// bubbles/textinput renders a cursor cell in addition to its prompt and
		// configured field width; reserve it so renderBox never adds an ellipsis.
		input.SetWidth(max(1, inner-lipgloss.Width(input.Prompt)-1))
		rows := []string{input.View(), "", dimStyle.Render(hint)}
		boxHeight := min(height, len(rows)+2)
		return m.renderBox(title, rows, boxWidth, boxHeight)
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
		return m.renderBox("Theme", rows, boxWidth, boxHeight)
	}
	layout := m.helpOverlay(width, height)
	rows := layout.rows
	title := layout.title
	if layout.visible > 0 && len(rows) > layout.visible {
		contentRows := max(1, layout.visible-1)
		maxOffset := len(rows) - contentRows
		offset := clamp(m.helpOffset, 0, maxOffset)
		status := fmt.Sprintf("%d-%d/%d · ↑↓/PgUp/PgDn scroll · Esc/? close", offset+1, offset+contentRows, len(rows))
		rows = append(rows[offset:offset+contentRows], m.renderer.dimStyle.Render(status))
	} else {
		rows = append(rows, m.renderer.dimStyle.Render("Esc/? close"))
	}
	return m.renderBox(title, rows, layout.boxWidth, layout.boxHeight)
}

func (m Model) helpLines(width int) []string {
	titleStyle, rowStyle := m.renderer.titleStyle, m.renderer.rowStyle
	type entry struct{ group, key, description string }
	entries := []entry{
		{"Navigation", "s", "switch source (explicit; stops current playback)"},
		{"Navigation", "1 - 9", "select sub-view"},
		{"Navigation", "[ / ]", "cycle sub-view; jump result groups on a pushed page"},
		{"Navigation", "j / k", "move selection"},
		{"Navigation", "g / G", "jump to top or bottom"},
		{"Navigation", "enter", "open playlist/album/station or play"},
		{"Navigation", "esc / backspace / h", "back or clear filter"},
		{"Navigation", "r", "reload the current list (retry after an error)"},
		{"Playback", "p", "play selected; toggle the playing item"},
		{"Playback", "space / c", "pause or resume"},
		{"Playback", "n / b", "next or previous (Apple Music)"},
		{"Playback", "v", "stop"},
		{"Playback", "S / R", "shuffle (restarts a playlist or album) / repeat"},
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
		if entry.key == "S / R" && !m.declares(m.source, api.CapShuffle) && !m.declares(m.source, api.CapRepeat) {
			continue
		}
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
	add("Position", fmt.Sprintf("%.0f / %.0f s", m.state.Position, m.state.Duration))
	add("Queue", fmt.Sprintf("%d entries, index %d", len(m.state.Queue), m.state.QueueIndex))
	if activeAppleQueue(m.state) {
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
	return 0
}

// Run owns the interactive program. The caller owns helper and socket cleanup.
func Run(opts Options) error {
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
