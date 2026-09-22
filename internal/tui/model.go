// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/theme"
)

type Provider interface {
	SearchSource(context.Context, string, string, string, int) ([]core.Item, error)
	TrendingSource(context.Context, string, string, int) ([]core.Item, error)
	Sources(context.Context) ([]api.SourceDescriptor, error)
	LibraryPlaylistsSource(context.Context, string) ([]core.Item, error)
	LibraryAlbumsSource(context.Context, string) ([]core.Item, error)
	PlaylistTracksSource(context.Context, string, string) ([]core.Item, error)
	AlbumTracksSource(context.Context, string, string) (core.Item, []core.Item, error)
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
	SetShuffle(context.Context, bool) (core.PlaybackState, error)
	SetRepeat(context.Context, string) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	Enqueue(context.Context, core.PlaybackRequest, string, uint64) (core.PlaybackState, error)
	PlaySongs(context.Context, []string, int, core.PlaybackForm) (core.PlaybackState, error)
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

// jamendoSetupMsg carries the in-process validate-and-save result for the
// Jamendo setup modal (same Keychain path as `lilt jamendo setup`).
type jamendoSetupMsg struct {
	clientID string
	err      error
}
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
var jamendoViews = []string{"Home", "Discover", "Recent"}

type Options struct {
	Provider      Provider
	Player        Player
	Radio         RadioProvider
	Remote        Remote
	RadioCache    *radio.Cache
	Store         *state.Store
	Authorization core.AuthorizationStatus
	InitialTerm   string
	AutoPlay      bool
	Source        string
	Log           func(kind string, fields map[string]any)
	InitialWatch  *api.WatchSnapshot
	WatchUpdates  <-chan api.WatchUpdate
	// JamendoSetup validates and persists the user-owned developer client_id in
	// this process (Keychain), mirroring `lilt jamendo setup`. The setup modal
	// is the TUI's local entry point; the Client API stays unchanged.
	JamendoSetup func(ctx context.Context, clientID string) error
	// OpenURL opens an external URL (the Jamendo devportal). Injectable for
	// hermetic tests.
	OpenURL func(url string)
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

	message           string
	messageErr        bool
	persistentWarning string
	toastSeq          int

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
	// Jamendo setup modal state: the in-process setup hook plus its transient
	// validating/error presentation. The overlay itself reuses "input" with
	// inputMode "jamendo-setup".
	jamendoSetup      func(ctx context.Context, clientID string) error
	openURL           func(url string)
	jamendoValidating bool
	jamendoSetupErr   string
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
	startupWarning := ""
	if opts.InitialWatch != nil && opts.InitialWatch.Warning != nil {
		startupWarning = watchWarningText(*opts.InitialWatch.Warning)
	}
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
	if source == "" {
		source = string(api.SourceAppleMusic)
	}
	m := Model{
		provider:          opts.Provider,
		player:            opts.Player,
		radio:             opts.Radio,
		remote:            opts.Remote,
		radioCache:        opts.RadioCache,
		store:             opts.Store,
		input:             in,
		renderer:          renderer,
		source:            source,
		view:              viewsFor(source)[0],
		authorization:     opts.Authorization.Status,
		account:           accountSummary(opts.Authorization),
		autoPlay:          opts.AutoPlay,
		filter:            "",
		log:               opts.Log,
		lastView:          map[string]string{source: viewsFor(source)[0]},
		cache:             map[string][]core.Item{},
		probes:            map[string]radioProbe{},
		state:             core.PlaybackState{Status: "stopped", Mode: "preview", Authorization: opts.Authorization.Status},
		watchUpdates:      opts.WatchUpdates,
		jamendoSetup:      opts.JamendoSetup,
		openURL:           opts.OpenURL,
		renderTime:        time.Now(),
		message:           startupWarning,
		messageErr:        startupWarning != "",
		persistentWarning: startupWarning,
		connected:         true,
		hasInitialWatch:   opts.InitialWatch != nil,
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
		m.message = "Apple Music, Audius, Jamendo & radio — s switches source, / searches, : commands"
	}
	m.loading = true
	m.loadLocalView()
	return m
}

const (
	operationTimeout = 20 * time.Second
	// connectingWindow is how long a starting session still reads as
	// "connecting" before it is called "buffering".
	connectingWindow = 1500 * time.Millisecond
)

// playbackStartTimeout bounds playback-start mutations only. Starting a finite
// queue is paced per track server-side (~1s per track for a "play from here"
// page), which runs well past the generic 20s budget; a tighter limit made the
// TUI report a failure while the server was still filling the queue
// (batch 2026-09-19-watch-sync-recheck NEW-H3).
const playbackStartTimeout = 60 * time.Second

// Page classes carry the page's activation intent so Enter never depends on
// display text. A container is one sequence the user deliberately opened
// (album/playlist): Enter plays the row and the rest of that container. An
// aggregate is a query-result list, which is evidence for the query rather than
// an intent to play all of it: Enter plays only the row (docs/ui/model.md §6).
const (
	pageClassContainer = "container"
	pageClassAggregate = "aggregate"
)

// Row emphasis kinds. Playing outranks selection so the playing row keeps one
// fixed appearance; selection is already carried by the left cursor, and
// recoloring the row when it becomes selected would make one state look like two.
const (
	rowNormal = iota
	rowSelected
	rowPlaying
)

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
			m.message, m.messageErr = "", false
		}
	case jamendoSetupMsg:
		m.jamendoValidating = false
		if msg.err != nil {
			// Stay in the modal with the sanitized error so the user can fix the
			// client_id and retry without retyping it.
			m.jamendoSetupErr = presentation.Text(msg.err.Error())
			return m, nil
		}
		m.jamendoSetupErr = ""
		m = m.closeTextInput()
		prefix := msg.clientID
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		next, toast := m.withToast("Jamendo configured ("+prefix+"…) — press s to switch to it", false)
		// The server reads the credential lazily, so a sources.list refresh
		// flips the descriptor to ready without a restart.
		var refresh tea.Cmd
		if next.provider != nil {
			refresh = next.fetchSources()
		}
		return next, tea.Batch(toast, refresh)
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
		m.message, m.messageErr = "Server watch closed", true
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

// persistTimeout bounds state-save commands. The server may be momentarily
// busy (for example probing freshly browsed radio stations); a tight budget
// here reported "State save failed" while the server still applied the write,
// and the user's retry toggle flipped the just-saved state
// (batch 2026-09-19-watch-sync-recheck NEW-M5).
const persistTimeout = 20 * time.Second

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
