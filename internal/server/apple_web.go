package server

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/appleweb"
)

// PageCatalog is the page-backed Apple Music surface this provider consumes.
// *appleweb.Engine satisfies it; tests inject a fake so no browser is needed.
//
// Discovery and playback deliberately share one source: asking the account's own
// storefront is the only way the results and the player agree, and two catalogs
// (a storefront-agnostic API plus the player) drift apart at exactly the point
// that matters — a search hit that cannot be played.
type PageCatalog interface {
	Authorized(ctx context.Context) (bool, error)
	SearchSongs(ctx context.Context, term string, limit int) ([]appleweb.CatalogSong, error)
	SearchAlbums(ctx context.Context, term string, limit int) ([]appleweb.CatalogAlbum, error)
	AlbumTracks(ctx context.Context, albumID string) (appleweb.CatalogAlbum, []appleweb.CatalogSong, error)
	Song(ctx context.Context, songID string) (appleweb.CatalogSong, error)
}

// appleWebProvider serves Apple Music on platforms without MusicKit: catalog and
// playback both come from a Chromium running Apple's own web player, so the
// developer token, the user session, and the DRM path are Apple's.
type appleWebProvider struct {
	catalog PageCatalog
	// available reports whether a Widevine-capable browser exists. It is the only
	// thing the descriptor may know without paying a browser cold start.
	available func() error
}

// NewAppleWebProvider builds the browser-backed Apple provider. The platform
// composition registers it with Options.Providers.
func NewAppleWebProvider(catalog PageCatalog, available func() error) ContentProvider {
	if available == nil {
		available = appleweb.Available
	}
	return appleWebProvider{catalog: catalog, available: available}
}

func (p appleWebProvider) Source() api.SourceID { return api.SourceAppleMusic }

// linuxAppleReason explains the platform requirement. It is shown in every
// capability that needs the browser.
const linuxAppleReason = "Apple Music on this platform plays through a Chromium running Apple's web player; install a Widevine-capable chromium or set LILT_CHROMIUM_PATH"

func (p appleWebProvider) Descriptor(context.Context) api.SourceDescriptor {
	ready := func(description string) api.Capability {
		return api.Capability{Available: true, Description: description}
	}
	unavailable := func(reason string) api.Capability {
		return api.Capability{Available: false, Reason: reason}
	}
	descriptor := api.SourceDescriptor{
		ID:       api.SourceAppleMusic,
		Label:    "Apple Music",
		Priority: 100,
		Description: "Apple Music catalog and playback through Apple's own web player in a browser lilt manages. " +
			"Full playback needs a profile signed in once; previews work signed out.",
		Capabilities: map[string]api.Capability{
			api.CapSearchSongs:     ready("Search the Apple Music catalog in the account's own storefront."),
			api.CapSearchAlbums:    ready("Search the Apple Music catalog for albums."),
			api.CapPlaybackPreview: ready("Play a 30-second preview."),
			// Full playback needs a signed-in profile. That precondition is stated
			// here rather than hidden behind a descriptor that would have to start
			// a browser to answer; a play without a session fails with the
			// documented authorization_required.
			api.CapPlaybackFull: ready("Play full tracks; requires a profile signed in to Apple Music."),
			// The queue is owned by the server for every URL-style source, so it
			// behaves exactly like Audius and Jamendo here.
			api.CapQueue: ready("Finite queue controls."),

			api.CapSearchPlaylists: unavailable(linuxAppleReason + " (playlists are not exposed by the web player's catalog API)"),
			api.CapSearchStations:  unavailable(linuxAppleReason),
			api.CapLibrary:         unavailable("the account library is not exposed by the web player's catalog API"),
			api.CapRecommendations: unavailable(linuxAppleReason),
			// Shuffle and repeat belong to the MusicKit transport; the server's
			// queue has neither.
			api.CapShuffle: unavailable("shuffle is not available for the server-owned queue"),
			api.CapRepeat:  unavailable("repeat is not available for the server-owned queue"),
		},
	}
	if err := p.available(); err != nil {
		descriptor.Availability = api.AvailabilityUnavailable
		descriptor.Reason = err.Error()
		descriptor.Capabilities = map[string]api.Capability{}
		for _, name := range appleCapabilityNames() {
			descriptor.Capabilities[name] = unavailable(err.Error())
		}
		descriptor.Available = false
		return descriptor
	}
	descriptor.Availability = api.AvailabilityReady
	descriptor.Available = anyAvailable(descriptor.Capabilities)
	return descriptor
}

func appleCapabilityNames() []string {
	return []string{
		api.CapSearchSongs, api.CapSearchAlbums, api.CapSearchPlaylists, api.CapSearchStations,
		api.CapLibrary, api.CapRecommendations, api.CapPlaybackFull, api.CapPlaybackPreview,
		api.CapQueue, api.CapShuffle, api.CapRepeat,
	}
}

func (p appleWebProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	if strings.TrimSpace(term) == "" {
		return nil, api.Errorf(api.CodeInvalidRequest, "Apple Music search needs a term")
	}
	switch kind {
	case api.KindSong:
		songs, err := p.catalog.SearchSongs(ctx, term, limit)
		if err != nil {
			return nil, mapAppleWebError(err, "Apple Music search failed")
		}
		return appleWebSongs(songs), nil
	case api.KindAlbum:
		albums, err := p.catalog.SearchAlbums(ctx, term, limit)
		if err != nil {
			return nil, mapAppleWebError(err, "Apple Music search failed")
		}
		return appleWebAlbums(albums), nil
	default:
		// The descriptor does not declare this kind, so the server refuses it
		// before reaching here; this keeps a direct caller honest too.
		return nil, api.Errorf(api.CodeUnsupportedCommand, "Apple Music on this platform can search songs and albums only")
	}
}

func (p appleWebProvider) AlbumTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	album, tracks, err := p.catalog.AlbumTracks(ctx, id)
	if err != nil {
		return api.Item{}, nil, mapAppleWebError(err, "Apple Music album lookup failed")
	}
	return appleWebAlbum(album), appleWebSongs(tracks), nil
}

// PreparePlayback turns song refs into a queue. The mode is decided by the live
// session: a signed-out profile can only produce previews, and saying "full"
// there would misreport a 30-second excerpt as a whole track.
func (p appleWebProvider) PreparePlayback(ctx context.Context, request PlaybackRequest) (PreparedPlayback, *api.Error) {
	if len(request.References) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Apple Music playback needs at least one song")
	}
	queue := make([]api.Item, 0, len(request.References))
	for _, reference := range request.References {
		if reference.Source != api.SourceAppleMusic || reference.Kind != api.KindSong || strings.TrimSpace(reference.ID) == "" {
			return nil, api.Errorf(api.CodeInvalidReference, "Apple Music playback needs song references")
		}
		song, err := p.catalog.Song(ctx, reference.ID)
		if err != nil {
			return nil, mapAppleWebError(err, "Apple Music could not resolve that song")
		}
		queue = append(queue, appleWebSong(song))
	}
	authorized, err := p.catalog.Authorized(ctx)
	if err != nil {
		return nil, mapAppleWebError(err, "Apple Music session is unavailable")
	}
	startIndex := request.StartIndex
	if startIndex < 0 || startIndex >= len(queue) {
		return nil, api.Errorf(api.CodeInvalidReference, "Apple Music start index is out of range")
	}
	if request.FromHere {
		queue = queue[startIndex:]
		startIndex = 0
	}
	mode := URLQueuePreview
	if authorized {
		mode = URLQueueFull
	}
	return NewURLQueuePlanWithMode(api.SourceAppleMusic, queue, startIndex, mode, resolveAppleWebTarget), nil
}

// resolveAppleWebTarget re-reads the item when it starts and hands the queue the
// item's stable public page. The browser plays the catalog id in
// Item.ProviderID; the URL is carried because a URL queue expects one, and it is
// the same public page the item already publishes.
func resolveAppleWebTarget(_ context.Context, item api.Item) (urlResolution, error) {
	if item.Source != api.SourceAppleMusic || item.Kind != api.KindSong || item.ProviderID == "" {
		return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Apple Music queue item is invalid")
	}
	if item.URL == "" {
		return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Apple Music queue item has no public URL")
	}
	return urlResolution{URL: item.URL}, nil
}

func appleWebSongs(songs []appleweb.CatalogSong) []api.Item {
	out := make([]api.Item, 0, len(songs))
	for _, song := range songs {
		if song.ID == "" || strings.TrimSpace(song.Title) == "" {
			continue
		}
		out = append(out, appleWebSong(song))
	}
	return out
}

func appleWebSong(song appleweb.CatalogSong) api.Item {
	return api.ProjectCoreItem(core.Item{
		Kind:   api.KindSong,
		ID:     song.ID,
		URL:    song.URL,
		Title:  song.Title,
		Artist: song.Artist,
	}, api.SourceAppleMusic)
}

func appleWebAlbums(albums []appleweb.CatalogAlbum) []api.Item {
	out := make([]api.Item, 0, len(albums))
	for _, album := range albums {
		if album.ID == "" || strings.TrimSpace(album.Title) == "" {
			continue
		}
		out = append(out, appleWebAlbum(album))
	}
	return out
}

func appleWebAlbum(album appleweb.CatalogAlbum) api.Item {
	return api.ProjectCoreItem(core.Item{
		Kind:   api.KindAlbum,
		ID:     album.ID,
		URL:    album.URL,
		Title:  album.Title,
		Artist: album.Artist,
	}, api.SourceAppleMusic)
}

// mapAppleWebError keeps the stable code meaningful: a missing browser is a
// source problem, an unknown id is a reference problem, and everything else is
// the discovery or the playback failure the caller already named.
func mapAppleWebError(err error, fallback string) *api.Error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, appleweb.ErrNoBrowser) {
		return api.Errorf(api.CodeSourceUnavailable, "%s", err.Error())
	}
	if strings.Contains(err.Error(), "was not found") {
		return api.Errorf(api.CodeInvalidReference, "%s", err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return api.Errorf(api.CodeSearchFailed, "%s: the Apple Music page did not answer in time", fallback)
	}
	return api.Errorf(api.CodeSearchFailed, "%s", fallback)
}

// AppleSession is the browser session the sign-in flow drives.
type AppleSession interface {
	Authorized(ctx context.Context) (bool, error)
	// SignIn opens a visible browser and blocks until the profile is authorized.
	SignIn(ctx context.Context) error
	// Disconnect closes the session and forgets the profile.
	Disconnect() error
	// WarmUp starts the session if a profile exists, so the authorization state
	// settles without a user action.
	WarmUp(ctx context.Context)
}

// appleWebAuthProvider reports the browser session's authorization and drives
// Apple's own sign-in in a browser lilt owns. Credentials never reach lilt: the
// page renders Apple's UI and the session lands in the browser profile.
type appleWebAuthProvider struct {
	session AppleSession
	// started reports whether a session already exists. Describe must not start a
	// browser just to answer an authorization question.
	started func() bool

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewAppleWebAuthProvider builds the browser-backed Apple authorization
// provider for the platform composition.
func NewAppleWebAuthProvider(session AppleSession, started func() bool) AuthProvider {
	return &appleWebAuthProvider{session: session, started: started, cancels: map[string]context.CancelFunc{}}
}

func (*appleWebAuthProvider) Source() api.SourceID { return api.SourceAppleMusic }

func (p *appleWebAuthProvider) Describe(ctx context.Context) api.SourceAuthorization {
	// not_required would read as "full playback needs nothing", and the server
	// refuses to start a flow for a source in that state — which made `lilt auth
	// apple-music` unreachable. Authorization genuinely has not been requested
	// until the profile says otherwise, and previews keep working meanwhile.
	if p.started == nil || !p.started() {
		return api.SourceAuthorization{
			Source: api.SourceAppleMusic,
			Status: api.AuthNotDetermined,
			Details: map[string]any{
				"platform": "linux",
				"message":  "previews need no account; run `lilt auth apple-music` for full tracks",
			},
		}
	}
	authorized, err := p.session.Authorized(ctx)
	if err != nil {
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthError, Details: map[string]any{"message": err.Error()}}
	}
	if authorized {
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthAuthorized, Details: map[string]any{"platform": "linux"}}
	}
	return api.SourceAuthorization{
		Source: api.SourceAppleMusic,
		Status: api.AuthNotDetermined,
		Details: map[string]any{
			"platform": "linux",
			"message":  "the browser profile is signed out; run `lilt auth apple-music` (only previews play until then)",
		},
	}
}

// WarmUp settles the authorization state at boot, without starting a browser for
// a profile that does not exist yet.
func (p *appleWebAuthProvider) WarmUp(ctx context.Context) { p.session.WarmUp(ctx) }

// Begin opens the sign-in window and completes the flow when the profile becomes
// authorized. It returns as soon as the flow is running, like every other
// provider: the flow outlives the client that started it.
func (p *appleWebAuthProvider) Begin(ctx context.Context, flowID string, update func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
	flowCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancels[flowID] = cancel
	p.mu.Unlock()

	if update != nil {
		update(api.AuthorizationFlow{
			Source:      api.SourceAppleMusic,
			Status:      api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser, URL: appleweb.SignInURL},
		})
	}
	go func() {
		defer p.Cancel(flowID)
		err := p.session.SignIn(flowCtx)
		switch {
		case err == nil:
			complete(api.AuthorizationFlow{Source: api.SourceAppleMusic, Status: api.FlowAuthorized, Interaction: api.Interaction{Type: api.InteractionNone}})
		case errors.Is(err, context.Canceled):
			complete(api.AuthorizationFlow{Source: api.SourceAppleMusic, Status: api.FlowCancelled, Interaction: api.Interaction{Type: api.InteractionNone}})
		case errors.Is(err, appleweb.ErrSignInTimeout):
			complete(api.AuthorizationFlow{Source: api.SourceAppleMusic, Status: api.FlowExpired, Interaction: api.Interaction{Type: api.InteractionNone}})
		default:
			complete(api.AuthorizationFlow{Source: api.SourceAppleMusic, Status: api.FlowError, Interaction: api.Interaction{Type: api.InteractionNone},
				Error: api.Errorf(api.CodeAuthorizationFailed, "%s", err.Error())})
		}
	}()
	return nil
}

func (p *appleWebAuthProvider) Cancel(flowID string) {
	p.mu.Lock()
	cancel := p.cancels[flowID]
	delete(p.cancels, flowID)
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Disconnect forgets the Apple session by removing the browser profile it lives
// in. A directory lilt did not create is refused rather than deleted.
func (p *appleWebAuthProvider) Disconnect(context.Context) *api.Error {
	switch err := p.session.Disconnect(); {
	case err == nil:
		return nil
	case errors.Is(err, appleweb.ErrForeignProfile):
		return api.Errorf(api.CodeUnsupportedCommand,
			"%s is not a lilt browser profile; sign out there or remove it yourself", err.Error())
	default:
		return api.Errorf(api.CodeAuthorizationFailed, "could not remove the Apple browser profile: %s", err.Error())
	}
}
