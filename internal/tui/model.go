// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// stableItemID is the client-side view of the server's persistent identity. It
// delegates to the shared api.Identity so a TUI highlight can never disagree
// with a stored favorite or history row.
func stableItemID(source string, item core.Item) string {
	return api.NewIdentity(api.SourceID(source), item.Kind, item.ID, item.URL).StableID
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

var sourceIDs = []string{"apple-music", "audius", "radio"}

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

type nowStatus struct {
	kind string
	text string
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
