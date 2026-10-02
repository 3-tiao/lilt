package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/presentation"
)

func (m Model) views() []string {
	views := []string{"Home"}
	if source := m.source; source == "radio" && m.declares(source, api.CapSearchRadio) {
		views = append(views, "Browse")
	} else if m.declares(source, api.CapSearchTrending) || m.declares(source, api.CapSearchTrendingSongs) {
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
			return "Account: not signed in — previews only (:auth)"
		case "denied":
			return "Account: access denied — previews only (:auth)"
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

// accountWarning is the account row of the Now Playing box: the live per-source
// authorization snapshot, falling back to the playback state's own authorization
// (the macOS helper settles that field asynchronously). Reading m.account here
// instead left the row showing whatever the first watch snapshot carried, so the
// server's later authorization.changed — an Apple session warming up at boot, or
// a sign-in — never reached it.
func (m Model) accountWarning() string {
	// A live snapshot is authoritative even when it says "nothing to warn
	// about"; falling back to the stale field there is exactly how the row kept
	// claiming the account was missing after the session settled.
	if m.sourceAuth.Status != "" {
		return sourceAccountSummary(m.source, m.sourceAuth)
	}
	return m.account
}

// setAuthorizations replaces the watch projection atomically on startup and
// reconnect; switching sources then selects from the same authoritative set.
func (m *Model) setAuthorizations(values []api.SourceAuthorization) {
	m.authorizations = make(map[api.SourceID]core.AuthorizationStatus, len(values))
	for _, value := range values {
		m.authorizations[value.Source] = api.ProjectAuthorization(value)
	}
	m.selectSourceAuthorization()
}

func (m *Model) selectSourceAuthorization() {
	m.sourceAuth = m.authorizations[api.SourceID(m.source)]
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
	case "radio", "jamendo":
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
	if value.QueueFill != nil {
		state.QueueFill = &core.QueueFill{Queued: value.QueueFill.Queued, Total: value.QueueFill.Total}
	}
	if value.StreamTitle != nil {
		state.StreamTitle = *value.StreamTitle
	}
	if value.StreamArtist != nil {
		state.StreamArtist = *value.StreamArtist
	}
	return state
}

func watchWarningText(warning api.WatchWarning) string {
	message := warning.Message
	if warning.Code != "" {
		message = warning.Code + ": " + message
	}
	return "Warning: " + presentation.Text(message)
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
	for key := range m.cache {
		if strings.HasSuffix(key, "/Favorites") || strings.HasSuffix(key, "/Recent") {
			delete(m.cache, key)
		}
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

func (m Model) applyWatchUpdate(update api.WatchUpdate) (tea.Model, tea.Cmd) {
	rearm := waitForWatchUpdate(m.watchUpdates)
	if update.Kind == api.WatchKindDisconnected {
		m.connected = false
		m.snapshotAt = time.Time{}
		if m.state.Status == "playing" || m.state.Status == "buffering" {
			m.state.Status = "disconnected"
		}
		m = m.clearProbesOnDisconnect()
		m.message, m.messageErr = "Server watch disconnected — reconnecting…", true
		return m, rearm
	}
	if update.Kind == api.WatchKindSnapshot {
		if update.Snapshot == nil {
			return m, rearm
		}
		snapshot := *update.Snapshot
		m.sequence, m.connected = snapshot.Sequence, true
		m.state = presentation.Playback(apiPlaybackToCore(snapshot.Playback))
		m.queueUndo = nil
		m.snapshotAt = m.renderTime
		m.descriptors = append([]api.SourceDescriptor(nil), snapshot.Sources...)
		m.appRevision = 0
		if snapshot.State != nil {
			m.applyAppState(*snapshot.State)
		}
		m.setAuthorizations(snapshot.Authorizations)
		if m.overlay == "auth" && len(snapshot.Authorizations) > 0 {
			// A reconnect snapshot is atomic and already carries every
			// source's authorization; adopt it instead of issuing an
			// unversioned fallback read (docs/ui/async-state.md §6).
			m.authList = append([]api.SourceAuthorization(nil), snapshot.Authorizations...)
			m.authListLoaded, m.authListErr, m.authListLoading = true, "", false
		}
		m.persistentWarning = ""
		if snapshot.Warning != nil {
			m.persistentWarning = watchWarningText(*snapshot.Warning)
		}
		m.message, m.messageErr = "Reconnected to lilt server", false
		m.loading, m.generation = true, m.generation+1
		return m, tea.Batch(rearm, m.loadView())
	}
	if update.Err != nil || update.Sequence <= m.sequence {
		return m, rearm
	}
	m.sequence, m.connected = update.Sequence, true
	var follow tea.Cmd
	switch update.Kind {
	case "playback.changed":
		if update.Playback != nil {
			m = m.setState(apiPlaybackToCore(*update.Playback)).refreshQueueCursor()
			if m.queueUndo != nil && (m.state.QueueRevision != m.queueUndo.revision || m.state.QueueIndex != m.queueUndo.queueIndex) {
				var notice tea.Cmd
				m, notice = m.dropStaleQueueUndo(m.state.QueueRevision != m.queueUndo.revision)
				follow = tea.Batch(follow, notice)
			}
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
		if update.Authorization != nil {
			if m.authorizations == nil {
				m.authorizations = make(map[api.SourceID]core.AuthorizationStatus)
			}
			m.authorizations[update.Authorization.Source] = api.ProjectAuthorization(*update.Authorization)
			if string(update.Authorization.Source) == m.source {
				m.selectSourceAuthorization()
			}
			// The Account overlay shows every source's live row, so one
			// event re-reads the whole list instead of patching one row.
			if m.overlay == "auth" {
				follow = m.fetchAuthList()
			}
		}
	case "server.warning":
		m.message = watchWarningText(api.WatchWarning{Code: update.WarningCode, Message: update.WarningMessage})
		m.messageErr = true
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

func (m Model) destination() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d", m.source, m.view, m.detailKind, m.detailID, m.pageClass, len(m.history))
}

func (m Model) accepts(generation uint64, destination string) bool {
	return generation == m.generation && (destination == "" || destination == m.destination())
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

func activeAppleQueue(playback core.PlaybackState) bool {
	return !playback.IsLive && playback.Status != "" && playback.Status != "stopped" && playback.Status != "none" && len(playback.Queue) > 0
}

func homeItems(source string, playback core.PlaybackState, queueSource string, recommended, trending, recent, playlists, favorites []core.Item, supportsTrending bool) []core.Item {
	const sectionLimit = 5
	// The current fixed source catalog is the availability boundary for these
	// optional previews. Callers may supply cached slices, but a slice from a
	// different capability must never make an unavailable Home section appear.
	items := make([]core.Item, 0, 16)
	if activeAppleQueue(playback) {
		// The section header already says "Continue Playing", so the row is just
		// the current track; the footer's `2 Up Next` covers the queue hint.
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
	if len(recommended) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Recommended"})
		items = append(items, recommended[:min(sectionLimit, len(recommended))]...)
	}
	if len(trending) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Trending"})
		items = append(items, trending[:min(sectionLimit, len(trending))]...)
	}
	if len(recent) > sectionLimit {
		recent = recent[:sectionLimit]
	}
	if len(recent) > 0 {
		items = append(items, core.Item{Kind: "header", Title: "Recently Played"})
		items = append(items, recent...)
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
	if supportsTrending {
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
	allowTrending := m.declares(m.source, api.CapSearchTrending) || m.declares(m.source, api.CapSearchTrendingSongs)
	allowRecommendations := m.declares(m.source, api.CapRecommendations)
	allowLibrary := m.declares(m.source, api.CapLibrary)
	allowQueue := m.declares(m.source, api.CapQueue)
	result := make([]core.Item, 0, len(items))
	skipSection := false
	for _, item := range items {
		if item.Kind == "header" {
			skipSection = (item.Title == "Recommended" && !allowRecommendations) || (item.Title == "Trending" && !allowTrending) || (item.Title == "Your Playlists" && !allowLibrary) || (item.Title == "Continue Playing" && !allowQueue)
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
// withForm carries the user's current shuffle/repeat into a play request. The
// server starts every playback from a known form (an omitted parameter means
// "off"), so without this a play would silently clear the shuffle the user just
// turned on.
// form is the user's current shuffle/repeat, for the commands that take it
// separately from a request (playback.playSongs).
func (m Model) form() core.PlaybackForm {
	form := core.PlaybackForm{}
	if m.state.Shuffle && m.declares(m.source, api.CapShuffle) {
		on := true
		form.Shuffle = &on
	}
	if m.state.Repeat != "" && m.state.Repeat != "off" && m.declares(m.source, api.CapRepeat) {
		form.Repeat = m.state.Repeat
	}
	return form
}

func (m Model) withForm(request core.PlaybackRequest) core.PlaybackRequest {
	if m.state.Shuffle && m.declares(m.source, api.CapShuffle) {
		on := true
		request.Shuffle = &on
	}
	if m.state.Repeat != "" && m.state.Repeat != "off" && m.declares(m.source, api.CapRepeat) {
		request.Repeat = m.state.Repeat
	}
	return request
}

func playbackRequestFor(item core.Item, source string) core.PlaybackRequest {
	request := core.PlaybackRequest{Ref: item.Ref, Kind: item.Kind, ID: item.ID, URL: item.URL}
	if request.Ref == "" && source != "radio" && item.Kind != "" {
		id := strings.TrimPrefix(item.ID, source+":"+item.Kind+":")
		request.Ref = source + ":" + item.Kind + ":" + id
	}
	return request
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
	case api.KindPlaylist:
		if m.source == "apple-music" || m.source == "audius" || m.source == "jamendo" {
			return m.pushContainer("playlist", item.ID, item.Title, m.openPlaylist(item))
		}
	case "browse":
		if item.ID == "Recent" {
			return m.pushRecent()
		}
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
		return m.openAuthOverlay()
	case "entry-favorites":
		next, cmd := m.pushFavorites()
		return next, cmd
	case "entry-playlists":
		next, cmd := m.push("Playlists", m.openLibraryPlaylists())
		return next, cmd
	case "entry-albums":
		next, cmd := m.push("Albums", m.openLibraryAlbums())
		return next, cmd
	case api.KindAlbum:
		return m.pushContainer("album", item.ID, item.Title, m.openAlbum(item))
	case api.KindSong:
		if m.detailKind == "playlist" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playPlaylistFrom(item) })
		}
		if m.detailKind == "album" && m.detailID != "" {
			return m.startMutation(func(next *Model) tea.Cmd { return next.playAlbumFrom(item) })
		}
		// A query-result page is not a container the user assembled, so Enter
		// plays only the pointed row (batch 2026-09-23-postaudit recheck: a
		// search hit reads as a song to play, not as a queue to start).
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

// focusHome enters the fixed left work area at its Home root.
func (m Model) focusHome() (tea.Model, tea.Cmd) {
	m.queueFocus = false
	return m.selectView(indexOf(m.views(), "Home"))
}

// focusQueue enters the fixed Up Next work area. It remains focusable without a
// finite queue so `2` always reaches the visible area; its empty state explains
// why editing actions are unavailable.
func (m Model) focusQueue() Model {
	m.queueFocus = true
	if len(m.state.Queue) > 0 {
		m.queueCursor = m.state.QueueIndex
		m = m.centerQueueWindow()
	}
	return m
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
// and Enter on a song plays from that row to the end of its section.
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

// isPlayingItem reports whether a list row is the item currently playing, so it
// can be highlighted. The distinction is style-only: adding a text prefix would
// duplicate what the highlight already says and add noise to every row.
func (m Model) isPlayingItem(item core.Item) bool {
	if m.state.Track == nil {
		return false
	}
	if m.detailKind == "album" &&
		m.queueSource.Kind == api.KindAlbum && m.queueSource.ID == m.detailID {
		return item.ID != "" && item.ID == m.state.Track.ID
	}
	if m.detailKind == "playlist" &&
		m.queueSource.Kind == api.KindPlaylist && m.queueSource.ID == m.detailID {
		return item.ID != "" && item.ID == m.state.Track.ID
	}
	if item.Kind == api.KindStream || item.Kind == api.KindStation {
		return samePlayingTrack(*m.state.Track, item)
	}
	return false
}

// samePlayingTrack reports whether a selected item is the item currently
// playing, so p can act as an intuitive pause/resume toggle on it.
func samePlayingTrack(current, selected core.Item) bool {
	if current.Kind == api.KindStream || selected.Kind == api.KindStream {
		return current.URL != "" && current.URL == selected.URL
	}
	return current.ID != "" && current.ID == selected.ID
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
	m.selectSourceAuthorization()
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
	m.selectSourceAuthorization()
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
		// The switcher is where a user first meets an unconfigured Jamendo; the
		// setup modal is the actionable version of its unavailable reason.
		if source == string(api.SourceJamendo) && m.jamendoSetup != nil {
			return m.openJamendoSetup()
		}
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

// resultGroupContext describes the pushed page's header groups: which group
// the cursor sits in, how many there are, and the jump key. Empty when the
// page has fewer than two groups.
func (m Model) resultGroupContext() string {
	type group struct {
		name  string
		index int
	}
	groups := make([]group, 0, 4)
	for i, item := range m.items {
		if item.Kind == "header" {
			groups = append(groups, group{name: item.Title, index: i})
		}
	}
	if len(groups) < 2 {
		return ""
	}
	current := m.selectedOriginalIndex()
	active := 0
	for i, g := range groups {
		if g.index <= current {
			active = i
		}
	}
	return fmt.Sprintf("%s %d/%d · [/] group", groups[active].name, active+1, len(groups))
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

// replaceItems keeps the cursor attached to its playable item's server-defined
// identity while an asynchronous refresh inserts or rebuilds display groups.
// Both refresh callers clear the local filter, so the cursor is re-anchored in
// the unfiltered list before the viewport is recomputed (otherwise a filtered
// index would be clamped against the wrong list). Position is only a fallback
// after the item disappeared, and duplicates of the same identity prefer the
// occurrence nearest the previous row so the cursor stays in its section.
func (m Model) replaceItems(items []core.Item) Model {
	previous, hadPrevious := m.selectedItem()
	previousIndex := m.selected
	hadFilter := m.filter != ""
	previousID := ""
	if hadPrevious {
		previousID = stableItemID(m.source, previous)
	}
	m.items = items
	m.filter = ""
	if previousID != "" {
		matches := make([]int, 0, 1)
		for index, item := range items {
			if stableItemID(m.source, item) == previousID {
				matches = append(matches, index)
			}
		}
		if len(matches) > 0 {
			best := matches[0]
			if !hadFilter {
				for _, index := range matches {
					if abs(index-previousIndex) < abs(best-previousIndex) {
						best = index
					}
				}
			}
			m.selected = best
			return m.keepMainSelectionVisible()
		}
	}
	if len(m.items) == 0 {
		m.selected = 0
		return m
	}
	if hadPrevious {
		for index := clamp(previousIndex, 0, len(m.items)-1); index < len(m.items); index++ {
			if selectable(m.items[index]) {
				m.selected = index
				return m.keepMainSelectionVisible()
			}
		}
		for index := clamp(previousIndex-1, 0, len(m.items)-1); index >= 0; index-- {
			if selectable(m.items[index]) {
				m.selected = index
				return m.keepMainSelectionVisible()
			}
		}
	}
	m.selected = firstSelectableIndex(m.items)
	return m.keepMainSelectionVisible()
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
