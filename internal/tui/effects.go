package tui

import (
	"context"
	"time"

	"charm.land/bubbletea/v2"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
)

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

// boundedContext gives generic mutations their operation budget.
func boundedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}

func boundedStartContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), playbackStartTimeout)
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
