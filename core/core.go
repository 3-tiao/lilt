// Package core defines the platform-independent lilt playback contracts.
package core

import "context"

// PlaybackRequest identifies an Apple Music resource. Reference is always an
// Apple Music URL or a kind:id value; bare IDs are intentionally invalid.
type PlaybackRequest struct {
	Kind         string `json:"kind"`
	ID           string `json:"id,omitempty"`
	Storefront   string `json:"storefront,omitempty"`
	URL          string `json:"url,omitempty"`
	StartAt      int    `json:"startAt,omitempty"`
	StartTrackID string `json:"startTrackID,omitempty"`
	StartTitle   string `json:"startTitle,omitempty"`
	Reverse      bool   `json:"reverse,omitempty"`
}

type Item struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	PreviewURL string `json:"previewURL,omitempty"`
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
	Queue         []Item   `json:"queue,omitempty"`
	QueueIndex    int      `json:"queueIndex"`
}

// PlaybackStateUpdate is an authoritative helper snapshot. Sequence is local
// to one helper process and increases for every published state change.
type PlaybackStateUpdate struct {
	Sequence uint64        `json:"sequence"`
	State    PlaybackState `json:"state"`
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

type ContentProvider interface {
	Search(context.Context, string, int) ([]Item, error)
	LibraryPlaylists(context.Context) ([]Item, error)
	RecentPlayed(context.Context, int) ([]Item, error)
	ResolveURL(context.Context, string) (Item, error)
	Stations(context.Context, string, int) ([]Item, error)
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
