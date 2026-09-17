// Package core defines the platform-independent lilt playback contracts.
package core

import "context"

// PlaybackRequest identifies a playable resource. Playlist starts prefer
// StartTrackID, then StartAt. Reverse applies to both queue order and start
// selection.
type PlaybackRequest struct {
	Ref          string `json:"ref,omitempty"`
	Kind         string `json:"kind"`
	ID           string `json:"id,omitempty"`
	Storefront   string `json:"storefront,omitempty"`
	URL          string `json:"url,omitempty"`
	StartAt      int    `json:"startAt,omitempty"`
	StartTrackID string `json:"startTrackID,omitempty"`
	Reverse      bool   `json:"reverse,omitempty"`
}

// RadioMetadata preserves Radio Browser's typed directory signals. Item stays
// the common playable projection while TUI ranking and future automation avoid
// parsing presentation text back into data.
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

type Item struct {
	Source     string         `json:"source,omitempty"`
	Kind       string         `json:"kind"`
	ID         string         `json:"id,omitempty"`
	Ref        string         `json:"ref,omitempty"`
	URL        string         `json:"url,omitempty"`
	Title      string         `json:"title"`
	Artist     string         `json:"artist,omitempty"`
	PreviewURL string         `json:"previewURL,omitempty"`
	Radio      *RadioMetadata `json:"radio,omitempty"`
}

type PlaybackState struct {
	Track         *Item    `json:"track,omitempty"`
	Position      float64  `json:"position"`
	Duration      float64  `json:"duration"`
	Status        string   `json:"status"`
	AudioVariant  *string  `json:"audioVariant"`
	Format        string   `json:"format"`
	Available     []string `json:"availableFormats,omitempty"`
	Shuffle       bool     `json:"shuffle"`
	Repeat        string   `json:"repeatMode"`
	IsLive        bool     `json:"isLive"`
	Mode          string   `json:"mode"`
	Authorization string   `json:"authorization"`
	AccountStatus string   `json:"accountStatus,omitempty"`
	AccountError  string   `json:"accountError,omitempty"`
	Error         string   `json:"playbackError,omitempty"`
	StreamTitle   string   `json:"streamTitle,omitempty"`
	StreamArtist  string   `json:"streamArtist,omitempty"`
	Queue         []Item   `json:"queue,omitempty"`
	QueueIndex    int      `json:"queueIndex"`
	QueueRevision uint64   `json:"queueRevision,omitempty"`
	// Ended is private helper-to-server transport data. It is deliberately not
	// included in api.PlaybackState projections.
	Ended              bool   `json:"ended,omitempty"`
	PlaybackGeneration uint64 `json:"playbackGeneration,omitempty"`
	TransportSessionID string `json:"transportSessionID,omitempty"`
}

// URLPlaybackTarget is private runtime-only input to the direct URL helper
// mode. URL must never be projected or persisted.
type URLPlaybackTarget struct {
	Item               Item
	URL                string
	Duration           int
	PlaybackGeneration uint64
	TransportSessionID string
}

// PlaybackStateUpdate is an authoritative helper snapshot. Sequence is local
// to one helper process and increases for every published state change.
type PlaybackStateUpdate struct {
	Sequence           uint64        `json:"sequence"`
	State              PlaybackState `json:"state"`
	PlaybackGeneration uint64        `json:"playbackGeneration,omitempty"`
	TransportSessionID string        `json:"transportSessionID,omitempty"`
}

// StateSubscription contains the snapshot returned by subscribeState and the
// ordered stream of subsequent stateChanged notifications.
type StateSubscription struct {
	Initial PlaybackStateUpdate
	Updates <-chan PlaybackStateUpdate
}

type PlaybackStateSubscriber interface {
	SubscribeState(context.Context) (StateSubscription, error)
	UnsubscribeState(context.Context) error
}

// AuthorizationStatus is the stable MusicKit status reported by the helper.
// It intentionally uses string values so it can cross the JSON-RPC boundary.
type AuthorizationStatus struct {
	Status                 string `json:"status"`
	AccountStatus          string `json:"accountStatus,omitempty"`
	AccountError           string `json:"accountError,omitempty"`
	CountryCode            string `json:"countryCode,omitempty"`
	CanPlayCatalogContent  bool   `json:"canPlayCatalogContent"`
	HasCloudLibraryEnabled bool   `json:"hasCloudLibraryEnabled"`
}

type TokenDiagnostics struct {
	Authorization          string `json:"authorization"`
	BundleID               string `json:"bundleID,omitempty"`
	DeveloperTokenReceived bool   `json:"developerTokenReceived"`
	DeveloperTokenError    string `json:"developerTokenError,omitempty"`
	KID                    string `json:"kid,omitempty"`
	Issuer                 string `json:"issuer,omitempty"`
	IssuedAt               int64  `json:"issuedAt,omitempty"`
	ExpiresAt              int64  `json:"expiresAt,omitempty"`
	UserTokenReceived      bool   `json:"userTokenReceived"`
	UserTokenError         string `json:"userTokenError,omitempty"`
	SubscriptionReceived   bool   `json:"subscriptionReceived"`
	SubscriptionError      string `json:"subscriptionError,omitempty"`
	CanPlayCatalogContent  bool   `json:"canPlayCatalogContent"`
	HasCloudLibraryEnabled bool   `json:"hasCloudLibraryEnabled"`
	CountryCode            string `json:"countryCode,omitempty"`
	CountryCodeError       string `json:"countryCodeError,omitempty"`
	LibraryPlaylistCount   int    `json:"libraryPlaylistCount,omitempty"`
	LibraryPlaylistError   string `json:"libraryPlaylistError,omitempty"`
	StorefrontUSStatus     int    `json:"storefrontUSStatus,omitempty"`
	StorefrontCNStatus     int    `json:"storefrontCNStatus,omitempty"`
}

type PlaybackTarget interface {
	Play(context.Context, PlaybackRequest) error
	Pause(context.Context) error
	Resume(context.Context) error
	Next(context.Context) error
	Previous(context.Context) error
	State(context.Context) (PlaybackState, error)
}

type Authorizer interface {
	Authorization(context.Context) (AuthorizationStatus, error)
}

// RadioProbeResult reports whether the helper's AVFoundation can load a stream.
// Status is healthy or failed; failure carries a stable error code.
type RadioProbeResult struct {
	Status    string `json:"status"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}
