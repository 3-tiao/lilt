// Package tui provides the foreground lilt browser.
package tui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
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
}

type listMsg struct {
	key   string
	title string
	items []core.Item
	err   error
}
type pushMsg struct {
	title string
	items []core.Item
	err   error
}
type autoMsg struct {
	term  string
	items []core.Item
	err   error
}
type actionMsg struct {
	state core.PlaybackState
	err   error
	note  string
}
type stateMsg struct {
	state core.PlaybackState
	err   error
}
type tickMsg struct{}
type toastMsg struct{ seq int }

type page struct {
	title    string
	items    []core.Item
	selected int
}

var amViews = []string{"Playlists", "Recent", "Presets"}
var radioViews = []string{"Favorites", "Builtin", "Countries", "Tags"}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	tabStyle     = lipgloss.NewStyle()
	activeTab    = lipgloss.NewStyle().Bold(true)
	accentStyle  = lipgloss.NewStyle()
	warnStyle    = lipgloss.NewStyle()
	errorStyle   = lipgloss.NewStyle()
	selStyle     = lipgloss.NewStyle().Bold(true)
	selInactive  = lipgloss.NewStyle()
	trackStyle   = lipgloss.NewStyle().Bold(true)
	rowStyle     = lipgloss.NewStyle()
	loadingStyle = lipgloss.NewStyle()
	borderActive = lipgloss.Color("81")
	borderIdle   = lipgloss.Color("240")
)

// applyTheme rebuilds the rendering styles from a theme.
func applyTheme(t theme.Theme) {
	onAccent := t.BG
	if onAccent == "" {
		onAccent = "0"
	}
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Accent))
	tabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	activeTab = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onAccent)).Background(lipgloss.Color(t.Accent))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Red))
	selStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(onAccent)).Background(lipgloss.Color(t.Accent))
	selInactive = lipgloss.NewStyle().Foreground(lipgloss.Color(t.BrightFG)).Background(lipgloss.Color(t.FG))
	trackStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	rowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	loadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	borderActive = lipgloss.Color(t.Accent)
	borderIdle = lipgloss.Color(t.FG)
}

type Options struct {
	Provider      Provider
	Player        Player
	Radio         RadioProvider
	Store         *state.Store
	Authorization core.AuthorizationStatus
	Presets       []core.Item
	Resolve       func(context.Context, string) (core.Item, error)
	InitialTerm   string
	AutoPlay      bool
	Focus         bool
	Source        string
	Log           func(kind string, fields map[string]any)
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
	resolve func(context.Context, string) (core.Item, error)

	state         core.PlaybackState
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
	queuePage   bool
	queueIntent string
	queueTarget int

	themeNames []string
	themeIndex int
	themeName  string

	focus    bool
	autoPlay bool
	polling  bool

	log func(kind string, fields map[string]any)
}

func New(opts Options) Model {
	applyTheme(theme.Load(opts.Store.Theme))
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
	}
	if opts.Store.Theme != "" {
		m.themeName = opts.Store.Theme
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

func accountSummary(status core.AuthorizationStatus) string {
	switch status.AccountStatus {
	case "ready":
		location := ""
		if status.CountryCode != "" {
			location = " · " + strings.ToUpper(status.CountryCode)
		}
		return "Account: Apple Music ready · Cloud library on" + location
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
	if m.focus && len(m.presets) > 0 {
		return nil
	}
	if m.autoPlay && m.input.Value() != "" {
		return m.autoSearch(m.input.Value())
	}
	return m.loadView()
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) pollState() tea.Cmd {
	return func() tea.Msg {
		state, err := m.player.State(context.Background())
		return stateMsg{state, err}
	}
}

func (m Model) viewKey() string { return m.source + "/" + m.view }

func (m Model) searchAM(term string) tea.Cmd {
	return func() tea.Msg {
		songs, songErr := m.provider.Search(context.Background(), term, 20)
		playlists, _ := m.provider.SearchPlaylists(context.Background(), term, 20)
		if songErr != nil && len(playlists) == 0 {
			return pushMsg{err: songErr}
		}
		return pushMsg{items: grouped(songs, playlists)}
	}
}

func (m Model) searchRadio(term string) tea.Cmd {
	return func() tea.Msg {
		stations, err := m.radio.Search(context.Background(), term, 50)
		if err != nil {
			return pushMsg{err: err}
		}
		return pushMsg{items: radio.ToItems(stations)}
	}
}

func (m Model) autoSearch(term string) tea.Cmd {
	return func() tea.Msg {
		songs, songErr := m.provider.Search(context.Background(), term, 20)
		playlists, _ := m.provider.SearchPlaylists(context.Background(), term, 20)
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

func queueItems(state core.PlaybackState) []core.Item {
	items := make([]core.Item, 0, len(state.Queue))
	for i, entry := range state.Queue {
		title := entry.Title
		if i == state.QueueIndex {
			title = "▶ " + title
		}
		items = append(items, core.Item{Kind: entry.Kind, ID: entry.ID, URL: entry.URL, Title: title, Artist: entry.Artist})
	}
	return items
}

func (m Model) refreshQueuePage() Model {
	if !m.queuePage {
		return m
	}
	m.items = queueItems(m.state)
	last := max(0, len(m.items)-1)
	switch m.queueIntent {
	case "jump":
		m.selected = clamp(m.state.QueueIndex, 0, last)
	case "remove":
		m.selected = clamp(m.queueTarget, 0, last)
	case "movedown":
		m.selected = clamp(m.queueTarget+1, 0, last)
	case "moveup":
		m.selected = clamp(m.queueTarget-1, 0, last)
	default:
		m.selected = clamp(m.selected, 0, last)
	}
	m.queueIntent = ""
	return m
}

func (m Model) queueCommand(action string) tea.Cmd {
	index := m.selected
	return func() tea.Msg {
		var state core.PlaybackState
		var err error
		switch action {
		case "jump":
			state, err = m.player.QueueJump(context.Background(), index)
		case "remove":
			state, err = m.player.QueueRemove(context.Background(), index)
		case "movedown":
			state, err = m.player.QueueMove(context.Background(), index, index+1)
		case "moveup":
			state, err = m.player.QueueMove(context.Background(), index, index-1)
		}
		return actionMsg{state: state, err: err}
	}
}

func (m Model) queueClear() tea.Cmd {
	return func() tea.Msg {
		state, err := m.player.QueueClear(context.Background())
		return actionMsg{state: state, err: err, note: "Queue cleared"}
	}
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
	key := m.viewKey()
	if items, ok := m.cache[key]; ok {
		return func() tea.Msg { return listMsg{key: key, title: m.view, items: items} }
	}
	switch {
	case key == "apple-music/Playlists":
		return func() tea.Msg {
			items, err := m.provider.LibraryPlaylists(context.Background())
			sortByName(items)
			return listMsg{key: key, title: "Playlists", items: items, err: err}
		}
	case key == "apple-music/Recent":
		return func() tea.Msg {
			items, err := m.provider.RecentPlayed(context.Background(), 50)
			return listMsg{key: key, title: "Recent", items: items, err: err}
		}
	case key == "apple-music/Presets":
		return func() tea.Msg { return listMsg{key: key, title: "Presets", items: m.presets} }
	case key == "radio/Favorites":
		return func() tea.Msg {
			return listMsg{key: key, title: "Favorites", items: m.store.FavoritesFor("radio")}
		}
	case key == "radio/Builtin":
		return func() tea.Msg {
			return listMsg{key: key, title: "Builtin", items: radio.ToItems(radio.Builtin(context.Background()))}
		}
	case key == "radio/Countries":
		return func() tea.Msg {
			countries, err := m.radio.Countries(context.Background())
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
			tags, err := m.radio.Tags(context.Background())
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

func (m Model) openPlaylist(item core.Item) tea.Cmd {
	id := item.ID
	title := item.Title
	return func() tea.Msg {
		tracks, err := m.provider.PlaylistTracks(context.Background(), id)
		return pushMsg{title: title, items: tracks, err: err}
	}
}

func (m Model) openCountry(item core.Item) tea.Cmd {
	code, title := item.ID, item.Title
	return func() tea.Msg {
		stations, err := m.radio.StationsByCountry(context.Background(), code, 100)
		return pushMsg{title: title, items: radio.ToItems(stations), err: err}
	}
}

func (m Model) openTag(item core.Item) tea.Cmd {
	tag := item.ID
	return func() tea.Msg {
		stations, err := m.radio.StationsByTag(context.Background(), tag, 100)
		return pushMsg{title: tag, items: radio.ToItems(stations), err: err}
	}
}

func (m Model) playItem(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": item.Kind, "title": item.Title})
	switch {
	case item.Kind == "stream":
		m.store.AddRecent("radio", item)
		_ = m.store.Save()
		return func() tea.Msg {
			state, err := m.player.RadioPlay(context.Background(), item.URL, item.Title)
			return actionMsg{state: state, err: err}
		}
	case item.Kind == "preset":
		return m.playPreset(item)
	default:
		source := m.source
		m.store.AddRecent(source, item)
		_ = m.store.Save()
		return func() tea.Msg {
			request := core.PlaybackRequest{Kind: item.Kind, ID: item.ID, URL: item.URL}
			err := m.player.Play(context.Background(), request)
			state, stateErr := m.player.State(context.Background())
			if err == nil {
				err = stateErr
			}
			return actionMsg{state: state, err: err}
		}
	}
}

func (m Model) playPreset(item core.Item) tea.Cmd {
	return func() tea.Msg {
		resolved := item
		if item.Kind == "preset" && m.resolve != nil {
			value, err := m.resolve(context.Background(), item.ID)
			if err != nil {
				return actionMsg{err: err}
			}
			resolved = value
		}
		err := m.player.Play(context.Background(), core.PlaybackRequest{Kind: resolved.Kind, ID: resolved.ID, URL: resolved.URL})
		state, stateErr := m.player.State(context.Background())
		if err == nil {
			err = stateErr
		}
		return actionMsg{state: state, err: err}
	}
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
	case "playlist":
		if m.source == "apple-music" {
			m.detailKind, m.detailID, m.queuePage = "playlist", item.ID, false
			return m.push(item.Title, m.openPlaylist(item))
		}
	case "country":
		m.detailKind, m.detailID, m.queuePage = "", "", false
		return m.push(item.Title, m.openCountry(item))
	case "tag":
		m.detailKind, m.detailID, m.queuePage = "", "", false
		return m.push(item.ID, m.openTag(item))
	case "song":
		if m.detailKind == "playlist" && m.detailID != "" {
			return m, m.playPlaylistFrom(item)
		}
	}
	return m, m.playSelected()
}

func (m Model) playPlaylistFrom(item core.Item) tea.Cmd {
	m.logEvent("play", map[string]any{"itemKind": "playlistFrom", "title": item.Title})
	return func() tea.Msg {
		err := m.player.Play(context.Background(), core.PlaybackRequest{Kind: "playlist", ID: m.detailID, StartTrackID: item.ID, StartTitle: item.Title})
		state, stateErr := m.player.State(context.Background())
		if err == nil {
			err = stateErr
		}
		return actionMsg{state: state, err: err}
	}
}

// push optimistically opens a child page and shows the loading state.
func (m Model) push(title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.history = append(m.history, page{title: m.title, items: m.items, selected: m.selected})
	m.title = title
	m.items = nil
	m.selected = 0
	m.filter = ""
	m.loading = true
	return m, cmd
}

// pushLocal opens a child page that already has its items (no fetch).
func (m Model) pushLocal(title string, items []core.Item, selected int) Model {
	m.history = append(m.history, page{title: m.title, items: m.items, selected: m.selected})
	m.title = title
	m.items = items
	m.filter = ""
	m.loading = false
	if len(items) == 0 {
		m.selected = 0
	} else {
		m.selected = clamp(selected, 0, len(items)-1)
	}
	return m
}

func (m Model) control(kind string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch kind {
		case "pause":
			err = m.player.Pause(context.Background())
		case "resume":
			err = m.player.Resume(context.Background())
		case "next":
			err = m.player.Next(context.Background())
		case "previous":
			err = m.player.Previous(context.Background())
		}
		state, stateErr := m.player.State(context.Background())
		if err == nil {
			err = stateErr
		}
		return actionMsg{state: state, err: err}
	}
}

func (m Model) toggleShuffle() tea.Cmd {
	on := !m.state.Shuffle
	note := "Shuffle off"
	if on {
		note = "Shuffle on"
	}
	m.logEvent("control", map[string]any{"action": "shuffle", "on": on})
	return func() tea.Msg {
		state, err := m.player.SetShuffle(context.Background(), on)
		return actionMsg{state: state, err: err, note: note}
	}
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
	return func() tea.Msg {
		state, err := m.player.SetRepeat(context.Background(), mode)
		return actionMsg{state: state, err: err, note: "Repeat " + mode}
	}
}

func (m Model) stopPlayback() tea.Cmd {
	m.logEvent("control", map[string]any{"action": "stop"})
	return func() tea.Msg {
		state, err := m.player.Stop(context.Background())
		return actionMsg{state: state, err: err, note: "Stopped"}
	}
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
	return func() tea.Msg {
		resolved := item
		if item.Kind == "preset" && m.resolve != nil {
			value, err := m.resolve(context.Background(), item.ID)
			if err != nil {
				return actionMsg{err: err}
			}
			resolved = value
		}
		state, err := m.player.Enqueue(context.Background(), core.PlaybackRequest{Kind: resolved.Kind, ID: resolved.ID, URL: resolved.URL}, position)
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{state: state, note: label + ": " + resolved.Title}
	}
}

func (m Model) withToast(text string, isErr bool) (Model, tea.Cmd) {
	m.message = text
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
	m.queuePage = false
	m.detailKind, m.detailID = "", ""
	m.store.LastSource = source
	_ = m.store.Save()
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
	m.queuePage = false
	m.detailKind, m.detailID = "", ""
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
	m.queuePage = false
	m.detailKind, m.detailID = "", ""
	m.logEvent("navigate", map[string]any{"action": "cycle"})
	return m, m.loadView()
}

func (m Model) back() Model {
	if len(m.history) == 0 {
		return m
	}
	previous := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	m.title = previous.title
	m.items = previous.items
	m.selected = previous.selected
	m.filter = ""
	m.queuePage = false
	m.detailKind, m.detailID = "", ""
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
		fields["selected"] = item.Title
	}
	m.log(kind, fields)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case listMsg:
		m.loading = false
		if msg.err != nil {
			m.message = "Error: " + msg.err.Error()
			m.messageErr = true
			return m, nil
		}
		if m.cache != nil {
			m.cache[msg.key] = msg.items
		}
		if msg.key == m.viewKey() {
			m.title = msg.title
			m.items = msg.items
			m.selected = 0
			m.filter = ""
		}
	case pushMsg:
		m.loading = false
		if msg.err != nil {
			m = m.back()
			m.message = "Error: " + msg.err.Error()
			m.messageErr = true
			return m, nil
		}
		m.items = msg.items
		m.selected = firstSelectableIndex(msg.items)
		m.filter = ""
	case autoMsg:
		m.loading = false
		if msg.err != nil {
			m.message = "Search error: " + msg.err.Error()
			m.messageErr = true
			return m, nil
		}
		m.source = "apple-music"
		m.title = "Search: " + msg.term
		m.items = msg.items
		m.selected = firstSelectableIndex(msg.items)
		m.filter = ""
		if len(msg.items) > 0 {
			m.busy = true
			m.polling = true
			return m, tea.Batch(m.playSelected(), tick())
		}
	case actionMsg:
		m.busy = false
		if msg.err != nil {
			m.message = "Playback error: " + msg.err.Error()
			m.messageErr = true
			m.toastSeq++
			seq := m.toastSeq
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastMsg{seq} })
		}
		m.state, m.messageErr = msg.state, false
		m = m.refreshQueuePage()
		if msg.note != "" {
			return m.withToast(msg.note, false)
		}
		m.message = ""
	case toastMsg:
		if msg.seq == m.toastSeq {
			m.message = ""
		}
	case tickMsg:
		if !m.polling {
			return m, nil
		}
		return m, tea.Batch(m.pollState(), tick())
	case stateMsg:
		if msg.err != nil {
			m.message = "State error: " + msg.err.Error()
			m.messageErr = true
		} else {
			m.state = msg.state
			m = m.refreshQueuePage()
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
	if m.queuePage {
		switch msg.String() {
		case "enter":
			if len(m.items) > 0 {
				m.queueIntent, m.queueTarget, m.busy = "jump", m.selected, true
				return m, m.queueCommand("jump")
			}
			return m, nil
		case "x":
			if len(m.items) > 0 {
				m.queueIntent, m.queueTarget, m.busy = "remove", m.selected, true
				return m, m.queueCommand("remove")
			}
			return m, nil
		case "J":
			if m.selected+1 < len(m.items) {
				m.queueIntent, m.queueTarget, m.busy = "movedown", m.selected, true
				return m, m.queueCommand("movedown")
			}
			return m, nil
		case "K":
			if m.selected > 0 {
				m.queueIntent, m.queueTarget, m.busy = "moveup", m.selected, true
				return m, m.queueCommand("moveup")
			}
			return m, nil
		case "c":
			m.queueIntent, m.busy = "clear", true
			return m, m.queueClear()
		}
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "shift+tab":
		return m.switchSource(otherSource(m.source))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.selectView(int(msg.String()[0] - '1'))
	case "0":
		if len(m.state.Queue) == 0 {
			return m.withToast("Nothing is queued", true)
		}
		m.queuePage = true
		m.detailKind, m.detailID = "", ""
		return m.pushLocal("Now Playing", queueItems(m.state), m.state.QueueIndex), nil
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
	case "p", "x":
		m.busy = true
		m.polling = true
		return m, tea.Batch(m.playSelected(), tick())
	case " ", "c":
		m.busy = true
		m.polling = true
		if m.state.Status == "playing" {
			return m, tea.Batch(m.control("pause"), tick())
		}
		return m, tea.Batch(m.control("resume"), tick())
	case "n":
		if m.state.IsLive {
			return m, nil
		}
		m.busy = true
		m.polling = true
		return m, tea.Batch(m.control("next"), tick())
	case "b":
		if m.state.IsLive {
			return m, nil
		}
		m.busy = true
		m.polling = true
		return m, tea.Batch(m.control("previous"), tick())
	case "v":
		m.busy = true
		return m, m.stopPlayback()
	case "s":
		if m.state.IsLive {
			return m.withToast("Shuffle applies to Apple Music only", true)
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
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil
	case "F":
		m.inputMode = "filter"
		m.input.Prompt = "Filter: "
		m.input.SetValue(m.filter)
		m.input.Focus()
		return m, textinput.Blink
	case "/":
		m.inputMode = "search"
		m.input.Prompt = "Search: "
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
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	mode := m.inputMode
	value := strings.TrimSpace(m.input.Value())
	m.logEvent("submit", map[string]any{"mode": mode, "value": value})
	m.input.Blur()
	m.inputMode = ""
	switch mode {
	case "search":
		if value == "" {
			return m, nil
		}
		m.input.SetValue("")
		if m.source == "radio" {
			return m.push("Search: "+value, m.searchRadio(value))
		}
		return m.push("Search: "+value, m.searchAM(value))
	case "filter":
		m.filter = value
		m.selected = 0
		return m, nil
	case "url":
		if value == "" {
			return m, nil
		}
		item := core.Item{Kind: "stream", URL: value, Title: value}
		if m.store.IsFavorite("radio", state.ItemID("radio", item)) {
			return m.withToast("Already in favorites: "+value, false)
		}
		m.store.ToggleFavorite("radio", item)
		_ = m.store.Save()
		model, cmd := m.withToast("Added to favorites: "+value, false)
		if model.source == "radio" && model.view == "Favorites" {
			model.cache = map[string][]core.Item{}
			return model, tea.Batch(cmd, model.loadView())
		}
		return model, cmd
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
	added := m.store.ToggleFavorite(source, item)
	_ = m.store.Save()
	m.logEvent("favorite", map[string]any{"title": item.Title, "on": added})
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
		m.store.Theme = m.themeName
		_ = m.store.Save()
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

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	if m.overlay != "" {
		return m.overlayView(width, height)
	}
	trailer := 0
	if m.message != "" {
		trailer = 1
	}
	header := []string{m.sourceLine(width), m.viewLine(width)}
	if m.input.Focused() {
		header = append(header, m.input.View())
	}
	bodyHeight := height - len(header) - 1 - trailer
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	nowHeight := 8
	if bodyHeight < 14 {
		nowHeight = bodyHeight / 2
	}
	if m.state.Track == nil && !m.busy && nowHeight > 3 {
		nowHeight = 3
	}
	if nowHeight < 3 {
		nowHeight = 3
	}
	listHeight := bodyHeight - nowHeight
	if listHeight < 5 {
		listHeight = 5
		nowHeight = bodyHeight - listHeight
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		renderBox(m.listTitle(), m.listLines(width-2, listHeight-2), width, listHeight, true),
		renderBox(m.nowTitle(), m.nowLines(width-2, nowHeight-2), width, nowHeight, false),
	)
	lines := append([]string{}, header...)
	lines = append(lines, body)
	if m.message != "" {
		style := accentStyle
		if m.messageErr {
			style = errorStyle
		}
		lines = append(lines, style.Render(fit(m.message, width)))
	}
	lines = append(lines, m.footerLine(width))
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
		title += " filter:" + m.filter
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

func (m Model) listLines(width, rows int) []string {
	items := m.visibleItems()
	if m.loading && len(items) == 0 {
		return []string{loadingStyle.Width(width).MaxWidth(width).Render("loading…")}
	}
	if len(items) == 0 {
		text := "(empty)"
		if m.filter != "" {
			text = "(no match for " + m.filter + ")"
		}
		return []string{tabStyle.Width(width).MaxWidth(width).Render(text)}
	}
	start, end := window(m.selected, len(items), rows)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		item := items[i]
		if item.Kind == "header" {
			lines = append(lines, accentStyle.Width(width).MaxWidth(width).Render("── "+item.Title+" ──"))
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
		if i == m.selected {
			lines = append(lines, selStyle.Width(width).MaxWidth(width).Render("> "+label))
		} else {
			lines = append(lines, rowStyle.Width(width).MaxWidth(width).Render("  "+label))
		}
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
	return "Now Playing"
}

func (m Model) nowLines(width, height int) []string {
	row := rowStyle.Width(width).MaxWidth(width)
	if m.state.Track == nil {
		if m.busy {
			return []string{loadingStyle.Width(width).MaxWidth(width).Render("Starting playback…")}
		}
		return []string{tabStyle.Width(width).MaxWidth(width).Render("Nothing playing")}
	}
	title := m.state.Track.Title
	if m.state.Track.Artist != "" {
		title += " — " + m.state.Track.Artist
	}
	lines := []string{trackStyle.Width(width).MaxWidth(width).Render(title)}
	if m.state.IsLive {
		status := m.state.Status
		if status == "" {
			status = "stopped"
		}
		lines = append(lines, row.Render(fmt.Sprintf("LIVE · %s · %s", status, emptyDash(m.state.Format))))
	} else {
		barWidth := width - 18
		if barWidth < 8 {
			barWidth = 8
		}
		if barWidth > 40 {
			barWidth = 40
		}
		lines = append(lines, row.Render(progressBar(m.state.Position, m.state.Duration, barWidth)))
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
		lines = append(lines, row.Render(stateLine))
		if len(m.state.Available) > 0 {
			lines = append(lines, row.Render("Available: "+strings.Join(m.state.Available, ", ")))
		}
	}
	rows := height - len(lines)
	if len(m.state.Queue) > 0 && rows > 0 {
		start, end := window(m.state.QueueIndex, len(m.state.Queue), rows)
		for i := start; i < end; i++ {
			entry := m.state.Queue[i]
			label := entry.Title
			if entry.Artist != "" {
				label += " — " + entry.Artist
			}
			if i == m.state.QueueIndex {
				lines = append(lines, selStyle.Width(width).MaxWidth(width).Render("▶ "+label))
			} else {
				lines = append(lines, rowStyle.Width(width).MaxWidth(width).Render("  "+label))
			}
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

func (m Model) footerLine(width int) string {
	keys := "? help · Tab source · 1-9 view · 0 queue · enter play · space pause · f favorite · t theme · / search · q quit"
	if m.queuePage {
		keys = "enter jump · x remove · J/K move · c clear · esc back · q quit"
	}
	return tabStyle.Render(fit(keys, width))
}

func (m Model) overlayView(width, height int) string {
	if m.overlay == "theme" {
		boxWidth := 40
		inner := boxWidth - 2
		rows := make([]string, 0, len(m.themeNames))
		for i, name := range m.themeNames {
			style := rowStyle
			if i == m.themeIndex {
				style = selStyle
			}
			rows = append(rows, style.Width(inner).MaxWidth(inner).Render("  "+name))
		}
		boxHeight := len(rows) + 2
		if boxHeight > height-2 {
			boxHeight = height - 2
		}
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox("Theme", rows, boxWidth, boxHeight, true))
	}
	boxWidth := 74
	if width-4 < boxWidth {
		boxWidth = width - 4
	}
	if boxWidth < 24 {
		boxWidth = 24
	}
	inner := boxWidth - 2
	title := "Help"
	rows := m.helpLines(inner)
	if m.overlay == "info" {
		title = "Track Info"
		rows = m.infoLines(inner)
	}
	boxHeight := len(rows) + 2
	if boxHeight > height-2 {
		boxHeight = height - 2
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, renderBox(title, rows, boxWidth, boxHeight, true))
}

func (m Model) helpLines(width int) []string {
	entries := [][2]string{
		{"tab", "switch source (Apple Music / Radio)"},
		{"1 - 9", "select sub-view"},
		{"0", "Up Next queue (enter jump, x remove, J/K move, c clear)"},
		{"[ / ]", "cycle sub-view"},
		{"j / k", "move selection"},
		{"g / G", "jump to top or bottom"},
		{"enter", "open playlist/station or play"},
		{"p / x", "play"},
		{"space / c", "pause or resume"},
		{"n / b", "next or previous (Apple Music)"},
		{"v", "stop"},
		{"s / R", "shuffle / repeat"},
		{"e / E", "queue next / append (Apple Music)"},
		{"f", "favorite / unfavorite"},
		{"a", "add radio stream URL"},
		{"/", "search Apple Music catalog or radio"},
		{"F", "filter current list"},
		{"t", "theme picker"},
		{"i", "track info"},
		{"esc / backspace", "back / clear filter"},
		{"q", "quit"},
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, rowStyle.Width(width).MaxWidth(width).Render(fmt.Sprintf("%-16s %s", entry[0], entry[1])))
	}
	return lines
}

func (m Model) infoLines(width int) []string {
	lines := []string{}
	add := func(key, value string) {
		lines = append(lines, rowStyle.Width(width).MaxWidth(width).Render(fmt.Sprintf("%-10s %s", key, emptyDash(value))))
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
	add("Live", fmt.Sprintf("%v", m.state.IsLive))
	add("Format", m.state.Format)
	add("Shuffle", fmt.Sprintf("%v", m.state.Shuffle))
	add("Repeat", m.state.Repeat)
	add("Position", fmt.Sprintf("%.0f / %.0f s", m.state.Position, m.state.Duration))
	add("Queue", fmt.Sprintf("%d entries, index %d", len(m.state.Queue), m.state.QueueIndex))
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
			text = lines[i]
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
	p := tea.NewProgram(m, tea.WithAltScreen())
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
