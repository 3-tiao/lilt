package api

import "encoding/json"

// SourceID identifies a public content domain.
type SourceID string

const (
	SourceAppleMusic SourceID = "apple-music"
	SourceRadio      SourceID = "radio"
	// SourceAudius is the Audius discovery source; playback arrives in Phase 2.
	SourceAudius SourceID = "audius"
	// SourceJamendo is the Jamendo source: public discovery plus finite queue
	// playback. Reads need a user-supplied client_id (an application-level
	// credential), never a user authorization.
	SourceJamendo SourceID = "jamendo"
)

// Item kinds. This is a closed public enum; providers map their native types
// onto it.
const (
	KindSong     = "song"
	KindPlaylist = "playlist"
	KindStation  = "station"
	KindAlbum    = "album"
	KindStream   = "stream"
)

// Availability values for a source or capability.
const (
	AvailabilityReady                 = "ready"
	AvailabilityAuthorizationRequired = "authorization_required"
	AvailabilitySubscriptionRequired  = "subscription_required"
	AvailabilityUnavailable           = "unavailable"
	AvailabilityDegraded              = "degraded"
)

// Stable capability names.
const (
	CapSearchSongs     = "search.songs"
	CapSearchAlbums    = "search.albums"
	CapSearchPlaylists = "search.playlists"
	CapSearchStations  = "search.stations"
	CapSearchRadio     = "search.radio"
	CapSearchTrending  = "search.trending"
	// CapSearchTrendingSongs is the kind-specific form for sources whose
	// upstream only orders songs (Jamendo has no playlist popularity). It
	// serves the song kind of discovery.trending; with the default type=all
	// such a source returns only the songs group.
	CapSearchTrendingSongs = "search.trending.songs"
	CapLibrary             = "library"
	CapRecommendations     = "recommendations"
	CapPlaybackFull        = "playback.full"
	CapPlaybackPreview     = "playback.preview"
	CapPlaybackStream      = "playback.stream"
	CapQueue               = "queue"
	CapShuffle             = "shuffle"
	CapRepeat              = "repeat"
)

// Capability reports whether one capability of a source is usable and why not.
// Description is optional, non-normative guidance for agents; clients MUST NOT
// branch on it.
type Capability struct {
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
	Description string `json:"description,omitempty"`
}

// SourceDescriptor describes a source and each capability's availability.
// Description is optional, non-normative guidance for agents (what the source
// is, what it currently supports); clients MUST NOT branch on it.
type SourceDescriptor struct {
	ID           SourceID              `json:"id"`
	Label        string                `json:"label"`
	Priority     int                   `json:"priority"`
	Available    bool                  `json:"available"`
	Availability string                `json:"availability"`
	Reason       string                `json:"reason"`
	Description  string                `json:"description,omitempty"`
	Capabilities map[string]Capability `json:"capabilities"`
}

// RadioMetadata carries typed radio signals. Origin is one of builtin,
// directory, user.
type RadioMetadata struct {
	Origin        string   `json:"origin,omitempty"`
	StationUUID   string   `json:"stationUUID,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	Country       string   `json:"country,omitempty"`
	CountryCode   string   `json:"countryCode,omitempty"`
	Codec         string   `json:"codec,omitempty"`
	Bitrate       int      `json:"bitrate,omitempty"`
	HLS           bool     `json:"hls,omitempty"`
	Votes         int      `json:"votes,omitempty"`
	ClickCount    int      `json:"clickCount,omitempty"`
	ClickTrend    int      `json:"clickTrend,omitempty"`
	LastCheckOK   bool     `json:"lastCheckOK,omitempty"`
	LastCheckTime string   `json:"lastCheckTime,omitempty"`
}

// Item is the single playable/browsable entity used by discovery, queues,
// favorites, and recent.
type Item struct {
	Source     SourceID       `json:"source"`
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	ProviderID string         `json:"providerId,omitempty"`
	Ref        string         `json:"ref"`
	URL        string         `json:"url,omitempty"`
	Title      string         `json:"title"`
	Artist     string         `json:"artist,omitempty"`
	Album      string         `json:"album,omitempty"`
	DurationMs int            `json:"durationMs,omitempty"`
	PreviewURL string         `json:"previewURL,omitempty"`
	Radio      *RadioMetadata `json:"radio,omitempty"`
}

// PlaybackStatus is PlaybackState without queue context: queue, queueIndex,
// and queueSource are omitted.
type PlaybackStatus struct {
	Sequence uint64   `json:"sequence"`
	Source   SourceID `json:"source"`
	Track    *Item    `json:"track"`
	Position float64  `json:"position"`
	Duration float64  `json:"duration"`
	Status   string   `json:"status"`
	// QueueFill is set only while a finite queue is still being filled, so a
	// client can show progress instead of an indefinite "working".
	QueueFill     *QueueFill `json:"queueFill,omitempty"`
	AudioVariant  *string    `json:"audioVariant"`
	Format        string     `json:"format"`
	Available     []string   `json:"availableFormats,omitempty"`
	Shuffle       bool       `json:"shuffle"`
	Repeat        string     `json:"repeatMode"`
	IsLive        bool       `json:"isLive"`
	Mode          string     `json:"mode"`
	PlaybackError *string    `json:"playbackError"`
	StreamTitle   *string    `json:"streamTitle"`
	StreamArtist  *string    `json:"streamArtist"`
}

// QueueFill reports a paced finite-queue fill in progress.
type QueueFill struct {
	Queued int `json:"queued"`
	Total  int `json:"total"`
}

// PlaybackState is PlaybackStatus plus the full queue context.
type PlaybackState struct {
	PlaybackStatus
	QueueRevision uint64    `json:"queueRevision"`
	QueueSource   *SourceID `json:"queueSource"`
	Queue         []Item    `json:"queue,omitempty"`
	QueueIndex    int       `json:"queueIndex"`
}

// QueueState is the queue.view result.
type QueueState struct {
	Source        *SourceID `json:"source"`
	Items         []Item    `json:"items"`
	Index         int       `json:"index"`
	QueueRevision uint64    `json:"queueRevision"`
}

// QueueUndoOffer is a short-lived capability returned only by queue.remove.
// Token is opaque; ExpiresAt is an RFC3339Nano server timestamp.
type QueueUndoOffer struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// QueueRemoveResult separates the committed playback state from the optional,
// transient undo capability. Undo is absent when the removed row was not a
// future song or the active transport cannot restore its exact object.
type QueueRemoveResult struct {
	State PlaybackState   `json:"state"`
	Undo  *QueueUndoOffer `json:"undo,omitempty"`
}

// RecentEntry pairs an item with when it was played.
type RecentEntry struct {
	Item     Item   `json:"item"`
	PlayedAt string `json:"playedAt"`
}

// AppState is the normalized public projection of the persisted state.
type AppState struct {
	Revision   uint64        `json:"revision"`
	Theme      string        `json:"theme"`
	LastSource SourceID      `json:"lastSource"`
	Favorites  []Item        `json:"favorites"`
	Recent     []RecentEntry `json:"recent"`
}

// HistoryEntry is one qualified playback in history.list.
type HistoryEntry struct {
	Item     Item   `json:"item"`
	PlayedAt string `json:"playedAt"`
}

// HistoryPageResult is history.list's result.
type HistoryPageResult struct {
	Entries    []HistoryEntry `json:"entries"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// HistoryStats summarizes qualified plays of one ref.
type HistoryStats struct {
	Ref           string  `json:"ref"`
	PlayCount     int64   `json:"playCount"`
	FirstPlayedAt *string `json:"firstPlayedAt,omitempty"`
	LastPlayedAt  *string `json:"lastPlayedAt,omitempty"`
}

// HistoryClearResult is history.clear's result.
type HistoryClearResult struct {
	Cleared int64 `json:"cleared"`
}

// ActivityResetResult is activity.reset's result.
type ActivityResetResult struct {
	Archived    bool   `json:"archived"`
	ArchivePath string `json:"archivePath,omitempty"`
}

// Authorization statuses.
const (
	AuthNotRequired   = "not_required"
	AuthNotDetermined = "not_determined"
	AuthPending       = "pending"
	AuthAuthorized    = "authorized"
	AuthDenied        = "denied"
	AuthExpired       = "expired"
	AuthError         = "error"
)

// SourceAuthorization is the per-source authorization state. Details is
// namespaced source-specific data; generic control flow MUST NOT depend on it.
type SourceAuthorization struct {
	Source SourceID `json:"source"`
	Status string   `json:"status"`
	// CanDisconnect reports whether authorization.disconnect is supported for
	// this source. When false the command returns unsupported_command and a
	// client MUST NOT offer the action; true still allows a runtime failure.
	CanDisconnect bool           `json:"canDisconnect"`
	AccountLabel  string         `json:"accountLabel,omitempty"`
	ExpiresAt     string         `json:"expiresAt,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

// Flow interaction kinds.
const (
	InteractionNone         = "none"
	InteractionSystemDialog = "system_dialog"
	InteractionBrowser      = "browser"
	InteractionDeviceCode   = "device_code"
)

// Flow statuses.
const (
	FlowPending    = "pending"
	FlowAuthorized = "authorized"
	FlowDenied     = "denied"
	FlowExpired    = "expired"
	FlowCancelled  = "cancelled"
	FlowError      = "error"
)

// Interaction describes how a user completes an authorization flow. It never
// carries tokens or secrets.
type Interaction struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	UserCode  string `json:"userCode,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// AuthorizationFlow is the server-owned async authorization flow.
type AuthorizationFlow struct {
	FlowID      string      `json:"flowId"`
	Source      SourceID    `json:"source"`
	Status      string      `json:"status"`
	Interaction Interaction `json:"interaction"`
	Error       *Error      `json:"error,omitempty"`
}

// WatchSnapshot is the first line of a session.watch stream. The epoch of the
// connection lives on the response that carries this snapshot (Response.
// ServerInstanceID), which is also where a one-shot client reads it, so the
// snapshot does not repeat it: sequence numbers, revisions and opaque tokens are
// only comparable inside one epoch, and one field is enough to say which.
type WatchSnapshot struct {
	Sequence       uint64                `json:"sequence"`
	Playback       PlaybackState         `json:"playback"`
	State          *AppState             `json:"state,omitempty"`
	Sources        []SourceDescriptor    `json:"sources,omitempty"`
	Authorizations []SourceAuthorization `json:"authorizations,omitempty"`
	Warning        *WatchWarning         `json:"warning,omitempty"`
}

// WatchWarning carries one persistent server condition (for example a degraded
// activity store) so late-joining clients see it without an extra event.
type WatchWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Client-side lifecycle updates share the WatchUpdate channel with wire events.
const (
	WatchKindDisconnected = "session.disconnected"
	WatchKindSnapshot     = "session.snapshot"
)

// WatchUpdate is the client-decoded projection of one session.watch event or
// reconnect lifecycle transition. Exactly the fields relevant to Kind are
// populated. Keeping decoding at the client boundary prevents renderers from
// depending on provider payloads or repeatedly interpreting raw JSON.
type WatchUpdate struct {
	Kind           string
	Sequence       uint64
	Snapshot       *WatchSnapshot
	Playback       *PlaybackState
	State          *AppState
	Sources        []SourceDescriptor
	Authorization  *SourceAuthorization
	WarningCode    string
	WarningMessage string
	EngineSource   SourceID
	Err            error
}

// RadioProbeResult reports whether a stream URL loaded.
type RadioProbeResult struct {
	Status    string `json:"status"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SearchResult is discovery.search's fixed-shape result.
type SearchResult struct {
	Source SourceID          `json:"source"`
	Term   string            `json:"term"`
	Groups map[string][]Item `json:"groups"`
}

// Search group keys.
const (
	GroupSongs     = "songs"
	GroupAlbums    = "albums"
	GroupPlaylists = "playlists"
	GroupStations  = "stations"
)

// DegradedOrigin records one origin that failed while another succeeded.
type DegradedOrigin struct {
	Origin  string `json:"origin"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RadioSearchResult is radio.search's result.
type RadioSearchResult struct {
	Items           []Item           `json:"items"`
	Query           map[string]any   `json:"query"`
	DegradedOrigins []DegradedOrigin `json:"degradedOrigins,omitempty"`
}

// RadioOption is one facet value with its occurrence count.
type RadioOption struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// RadioOptionsResult is radio.options' result.
type RadioOptionsResult struct {
	Options         []RadioOption    `json:"options"`
	DegradedOrigins []DegradedOrigin `json:"degradedOrigins,omitempty"`
}

// PlaylistTracksResult is playlist.tracks' result.
type PlaylistTracksResult struct {
	Playlist Item   `json:"playlist"`
	Items    []Item `json:"items"`
}

// AlbumTracksResult is album.tracks' result.
type AlbumTracksResult struct {
	Album Item   `json:"album"`
	Items []Item `json:"items"`
}

// FavoriteResult is favorites.set's result.
type FavoriteResult struct {
	Favorited bool `json:"favorited"`
	Item      Item `json:"item"`
}

// DecodeParams decodes raw params into dst.
func DecodeParams(raw json.RawMessage, dst any) *Error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return Errorf(CodeInvalidRequest, "invalid params: %v", err)
	}
	return nil
}
