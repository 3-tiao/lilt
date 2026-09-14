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
	StationsByCountry(context.Context, string, int) ([]radio.Station, error)
	StationsByTag(context.Context, string, int) ([]radio.Station, error)
	Search(context.Context, string, int) ([]radio.Station, error)
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

// queueContext identifies the list that created the current Apple Music queue.
// It is intentionally separate from PlaybackState because MusicKit does not
// consistently expose that source container in its state snapshots.
type queueContext struct {
	Kind, ID, Title string
}

var amViews = []string{"Home", "Playlists", "Recent", "Presets"}
var radioViews = []string{"Home", "Favorites", "Recent", "Countries", "Tags"}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	tabStyle     = lipgloss.NewStyle()
	activeTab    = lipgloss.NewStyle().Bold(true)
	accentStyle  = lipgloss.NewStyle()
	warnStyle    = lipgloss.NewStyle()
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

	overlay   string
	filter    string
	inputMode string
	lastView  map[string]string
	cache     map[string][]core.Item

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

func (m Model) searchRadio(term string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		stations, err := m.radio.Search(ctx, term, 50)
		if err != nil {
			return pushMsg{err: err}
		}
		return pushMsg{items: radio.ToItems(stations)}
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
				note = "Removed: " + m.state.Queue[index].Title
			}
			state, err = m.player.QueueRemove(ctx, index)
		case "movedown":
			state, err = m.player.QueueMove(ctx, index, index+1)
		case "moveup":
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
	// Home combines current state and local history, so never serve a stale page.
	if key != "apple-music/Home" && key != "radio/Home" {
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
	case key == "radio/Home":
		favorites, recent, playback := m.store.FavoritesFor("radio"), m.store.RecentFor("radio"), m.state
		return func() tea.Msg {
			return listMsg{key: key, title: "Home", items: radioHomeItems(playback, favorites, recent)}
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
	case key == "radio/Countries":
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			countries, err := m.radio.Countries(ctx)
			if err != nil {
				return listMsg{key: key, title: "Countries", err: err}
			}
			items := make([]core.Item, 0, len(countries))
			for _, country := range countries {
				items = append(items, core.Item{Kind: "country", ID: country.Code, Title: country.Name, Artist: fmt.Sprintf("%d stations", country.StationCount)})
			}
			return listMsg{key: key, title: "Countries", items: items}
		}
	case key == "radio/Tags":
		return func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			tags, err := m.radio.Tags(ctx)
			if err != nil {
				return listMsg{key: key, title: "Tags", err: err}
			}
			items := make([]core.Item, 0, len(tags))
			for _, tag := range tags {
				items = append(items, core.Item{Kind: "tag", ID: tag.Name, Title: tag.Name, Artist: fmt.Sprintf("%d stations", tag.StationCount)})
			}
			return listMsg{key: key, title: "Tags", items: items}
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

func (m Model) openCountry(item core.Item) tea.Cmd {
	code, title := item.ID, item.Title
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		stations, err := m.radio.StationsByCountry(ctx, code, 100)
		return pushMsg{title: title, items: radio.ToItems(stations), err: err}
	}
}

func (m Model) openTag(item core.Item) tea.Cmd {
	tag := item.ID
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		stations, err := m.radio.StationsByTag(ctx, tag, 100)
		return pushMsg{title: tag, items: radio.ToItems(stations), err: err}
	}
}

func (m Model) playItem(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": item.Kind, "titleLength": len(item.Title)})
	switch {
	case item.Kind == "stream":
		return beginAction(m.actionClock, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			playback, err := m.player.RadioPlay(ctx, item.URL, item.Title)
			return actionMsg{state: playback, err: err, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: "radio", recentItem: &item}
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
	case "country":
		return m.push(item.Title, m.openCountry(item))
	case "tag":
		return m.push(item.ID, m.openTag(item))
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

// radioHomeItems mirrors the Apple Music Home: current stream, favorites,
// recent stations, and browse entry points. It only reads local state.
func radioHomeItems(playback core.PlaybackState, favorites, recent []core.Item) []core.Item {
	const sectionLimit = 8
	items := make([]core.Item, 0, len(favorites)+len(recent)+6)
	if playback.IsLive && playback.Track != nil {
		items = append(items, core.Item{Kind: "stream", URL: playback.Track.URL, Title: "Now Playing: " + playback.Track.Title, Artist: "LIVE · enter to replay"})
		items = append([]core.Item{{Kind: "header", Title: "Now Playing"}}, items...)
	}
	if len(favorites) > sectionLimit {
		favorites = favorites[:sectionLimit]
	}
	if len(favorites) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Favorites"})
		items = append(items, favorites...)
	}
	if len(recent) > sectionLimit {
		recent = recent[:sectionLimit]
	}
	if len(recent) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Recently Played"})
		items = append(items, recent...)
	}
	items = append(items,
		core.Item{Kind: "header", Title: "Browse"},
		core.Item{Kind: "browse", ID: "Countries", Title: "Countries", Artist: "browse stations by country"},
		core.Item{Kind: "browse", ID: "Tags", Title: "Tags", Artist: "browse stations by tag"},
	)
	return items
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
	view := m.lastView[source]
	if !contains(viewsFor(source), view) {
		view = viewsFor(source)[0]
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
	case listMsg:
		if !m.accepts(msg.generation, msg.destination) {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.message = "Error: " + presentation.Text(msg.err.Error())
			m.messageErr = true
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
		// A notification newer than the command's starting snapshot makes every
		// action-owned field stale, not only the playback State.
		if msg.afterSequence < m.sequence {
			return m, nil
		}
		if msg.queueContext != nil {
			m.queueSource = *msg.queueContext
		}
		m = m.setState(msg.state)
		if m.state.Mode == "none" || m.state.IsLive || m.state.Status == "stopped" {
			m.queueSource = queueContext{}
		}
		m = m.refreshQueueCursor()
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
		m.message = "Playback helper disconnected — quit and restart lilt to reconnect"
		m.messageErr = true
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
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
	m.logEvent("key", map[string]any{"key": msg.String(), "inputFocused": m.input.Focused()})
	if m.overlay == "theme" {
		return m.handleThemeKey(msg)
	}
	if m.overlay != "" {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.overlay = ""
		return m, nil
	}
	if m.queueFocus && msg.String() == "esc" {
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
		m.busy = true
		if m.detailKind == "playlist" && m.detailID != "" {
			return m, m.playPlaylist(false)
		}
		return m, m.playSelected()
	case " ", "c":
		m.busy = true
		if m.state.Status == "playing" {
			return m, m.control("pause")
		}
		return m, m.control("resume")
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
		m.queueFocus = false
		m.inputMode = "filter"
		m.input.Prompt = "Filter: "
		m.input.Placeholder = "substring to match"
		m.input.SetValue(m.filter)
		m.input.Focus()
		return m, textinput.Blink
	case "/":
		m.queueFocus = false
		m.inputMode = "search"
		m.input.Prompt = "Search: "
		m.input.Placeholder = "type a query and press Enter"
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "i":
		m.overlay = "info"
		return m, nil
	case "?":
		m.overlay = "help"
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
		return m, nil
	}
	return m, nil
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
		var next tea.Model
		var cmd tea.Cmd
		if m.source == "radio" {
			next, cmd = m.push("Search: "+presentation.Text(value), m.searchRadio(value))
		} else {
			next, cmd = m.push("Search: "+presentation.Text(value), m.searchAM(value))
		}
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
		note := "Playing: " + value
		if added {
			note = "Added to Favorites and playing: " + value
		}
		m.busy = true
		playCmd := beginAction(m.actionClock, func() tea.Msg {
			ctx, cancel := boundedContext()
			defer cancel()
			playback, err := m.player.RadioPlay(ctx, item.URL, item.Title)
			return actionMsg{state: playback, err: err, note: note, afterSequence: m.sequence, queueContext: &queueContext{}, recentSource: "radio", recentItem: &item, addFavorite: added, refreshView: added && m.source == "radio" && (m.view == "Favorites" || m.view == "Home")}
		})
		return m, playCmd
	}
	return m, nil
}

func (m Model) toggleFavorite() (tea.Model, tea.Cmd) {
	item, ok := m.selectedItem()
	if !ok {
		return m, nil
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
	m.logEvent("favorite", map[string]any{"titleLength": len(item.Title), "on": added})
	text := "Unfavorited: " + item.Title
	if added {
		text = "★ Favorited: " + item.Title
	}
	model, cmd := m.withToast(text, false)
	if model.source == "radio" && model.view == "Favorites" {
		model.items = model.store.FavoritesFor("radio")
	}
	return model, cmd
}

func (m Model) handleThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
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
			tabs = append(tabs, activeTab.Render(label))
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

func (m Model) viewLine(width int) string {
	tabs := []string{tabStyle.Render("VIEW  ")}
	for i, view := range viewsFor(m.source) {
		label := fmt.Sprintf(" %d %s ", i+1, view)
		if view == m.view {
			tabs = append(tabs, activeTab.Render(label))
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
	switch m.viewKey() {
	case "radio/Favorites":
		return "(empty) — press a to add a stream URL, / to search stations"
	case "radio/Home":
		return "(empty) — play a station to build your Home"
	case "radio/Recent":
		return "(empty) — nothing played yet"
	case "radio/Countries", "radio/Tags":
		return "(empty) — check your connection and reopen this view"
	case "apple-music/Playlists":
		return "(empty) — no playlists in your Apple Music library"
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
	case "station", "stream":
		return "∿ "
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
		if item.Artist != "" {
			label += " — " + item.Artist
		}
		if m.store != nil {
			source := m.source
			if item.Kind == "stream" {
				source = "radio"
			}
			if m.store.IsFavorite(source, state.ItemID(source, item)) {
				label = "★ " + label
			}
		}
		if strings.HasPrefix(m.title, "Search: ") || m.viewKey() == "apple-music/Home" || m.viewKey() == "radio/Home" {
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
	lines := []string{trackStyle.Render(fit(title, width))}
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
		stateLine := fmt.Sprintf("State: %s · Mode: %s · Format: %s", status, m.state.Mode, emptyDash(format))
		if flags := m.modeFlags(); flags != "" {
			stateLine += " · " + flags
		}
		if m.busy {
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
	if m.queueFocus {
		return []string{"j/k move", "enter/p jump", "x remove", "J/K reorder", "c clear", "0/esc back", "? help"}
	}
	if m.source == "apple-music" && m.detailKind == "playlist" && !m.loading {
		segments := []string{"p play all", "s shuffle", "enter play from here"}
		if activeAppleQueue(m.state) {
			segments = append(segments, "0 Up Next")
		}
		return append(segments, "esc back", "? help")
	}
	segments := []string{"enter open/play", "p play"}
	if activeAppleQueue(m.state) {
		segments = append(segments, "0 Up Next")
	}
	segments = append(segments, "/ search", "? help", "q quit", "Tab source", "1-9 view")
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
	if m.overlay == "theme" {
		boxWidth := min(40, width)
		inner := boxWidth - 2
		rows := make([]string, 0, len(m.themeNames))
		for i, name := range m.themeNames {
			style := rowStyle
			if i == m.themeIndex {
				style = selStyle
			}
			rows = append(rows, style.Render(fit("  "+name, inner)))
		}
		boxHeight := min(len(rows)+2, height)
		visible := max(0, boxHeight-2)
		start, end := window(clamp(m.themeIndex, 0, max(0, len(rows)-1)), len(rows), visible)
		rows = rows[start:end]
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox("Theme", rows, boxWidth, boxHeight, true))
	}
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
	if len(rows) > max(0, boxHeight-2) {
		rows = rows[:max(0, boxHeight-2)]
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox(title, rows, boxWidth, boxHeight, true))
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
		{"p", "play selected (Up Next: jump)"},
		{"x", "remove the focused Up Next track"},
		{"J / K", "reorder the focused Up Next track"},
		{"space / c", "pause or resume (Up Next focused: c clears)"},
		{"n / b", "next or previous (Apple Music)"},
		{"v", "stop"},
		{"s / R", "shuffle / repeat"},
		{"e / E", "queue next / append (Apple Music)"},
		{"f", "favorite / unfavorite"},
		{"a", "add a stream URL to Favorites and play it"},
		{"/", "search Apple Music catalog or radio"},
		{"F", "filter current list"},
		{"t", "theme picker"},
		{"i", "track info"},
		{"esc / backspace", "back or clear filter (Up Next: leave panel)"},
		{"q", "quit"},
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, rowStyle.Render(fit(fmt.Sprintf("%-16s %s", entry[0], entry[1]), width)))
	}
	return lines
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
