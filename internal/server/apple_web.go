package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	SearchPlaylists(ctx context.Context, term string, limit int) ([]appleweb.CatalogPlaylist, error)
	AlbumTracks(ctx context.Context, albumID string) (appleweb.CatalogAlbum, []appleweb.CatalogSong, error)
	PlaylistTracks(ctx context.Context, playlistID string) (appleweb.CatalogPlaylist, []appleweb.CatalogSong, error)
	TrendingSongs(ctx context.Context, limit int) ([]appleweb.CatalogSong, error)
	Recommendations(ctx context.Context, limit int) ([]appleweb.Recommendation, error)
	Song(ctx context.Context, songID string) (appleweb.CatalogSong, error)
}

// appleWebProvider serves Apple Music on platforms without MusicKit: catalog and
// playback both come from a Chromium running Apple's own web player, so the
// developer token, the user session, and the DRM path are Apple's.
type appleWebProvider struct {
	catalog PageCatalog
	// available reports whether a browser binary exists. It is the only thing
	// the descriptor may know without paying a browser cold start.
	available func() error
	// widevine reads the engine's cached EME probe answer when the catalog is
	// the real engine. Absent (hermetic fakes), no probe has run and the
	// descriptor keeps its declared precondition.
	widevine func() appleweb.WidevineProbe
}

// widevineSource is the PageCatalog extension the real engine implements: the
// EME probe answer, refreshed on every browser start. Full playback is DRM
// content, so the probe — not the binary's existence — is the capability fact.
type widevineSource interface {
	Widevine() appleweb.WidevineProbe
}

// NewAppleWebProvider builds the browser-backed Apple provider. The platform
// composition registers it with Options.Providers.
func NewAppleWebProvider(catalog PageCatalog, available func() error) ContentProvider {
	if available == nil {
		available = appleweb.Available
	}
	provider := appleWebProvider{catalog: catalog, available: available}
	if source, ok := catalog.(widevineSource); ok {
		provider.widevine = source.Widevine
	}
	return provider
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
	// Full playback is DRM content. Until a browser has run the EME probe the
	// descriptor cannot know, and it keeps the sign-in precondition rather
	// than paying a cold start to answer; once the probe answered, its verdict
	// is the truth — a browser without Widevine must not keep advertising
	// full only to fail at DRM start time.
	full := ready("Play full tracks; requires a profile signed in to Apple Music.")
	if probe := p.probeWidevine(); probe.Answered && !probe.Supported {
		reason := probe.Reason
		if reason == "" {
			reason = "the browser cannot play DRM content"
		}
		full = unavailable(fmt.Sprintf("this browser cannot play DRM content (%s); install a Widevine-capable chromium (nixpkgs: `chromium.override { enableWideVine = true; }`) or set LILT_CHROMIUM_PATH", reason))
	}
	descriptor := api.SourceDescriptor{
		ID:       api.SourceAppleMusic,
		Label:    "Apple Music",
		Priority: 100,
		Description: "Apple Music catalog and playback through Apple's own web player in a browser lilt manages. " +
			"Full playback needs a profile signed in once; previews work signed out.",
		Capabilities: map[string]api.Capability{
			api.CapSearchSongs:         ready("Search the Apple Music catalog in the account's own storefront."),
			api.CapSearchAlbums:        ready("Search the Apple Music catalog for albums."),
			api.CapSearchPlaylists:     ready("Search the Apple Music catalog for playlists."),
			api.CapSearchTrendingSongs: ready("Browse the storefront's song chart; no sign-in needed."),
			api.CapPlaybackPreview:     ready("Play a 30-second preview."),
			api.CapPlaybackFull:        full,
			// The queue is owned by the server for every URL-style source, so it
			// behaves exactly like Audius and Jamendo here.
			api.CapQueue: ready("Finite queue controls."),

			api.CapSearchStations: unavailable(linuxAppleReason),
			api.CapLibrary:        unavailable("the account library is not exposed by the web player's catalog API"),
			// Sign-in determines whether the request has content, not whether
			// recommendations exist as a provider capability. This matches the
			// MusicKit helper's descriptor semantics.
			api.CapRecommendations: ready("Read Apple Music recommendations; requires a profile signed in to Apple Music."),
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
		api.CapSearchTrendingSongs, api.CapLibrary, api.CapRecommendations, api.CapPlaybackFull, api.CapPlaybackPreview,
		api.CapQueue, api.CapShuffle, api.CapRepeat,
	}
}

// probeWidevine reads the engine's cached EME answer; a catalog that has none
// (the hermetic fakes) simply has not run one.
func (p appleWebProvider) probeWidevine() appleweb.WidevineProbe {
	if p.widevine == nil {
		return appleweb.WidevineProbe{}
	}
	return p.widevine()
}

// AvailabilitySignature reports the facts the descriptor derives from: whether
// a browser binary exists and what the last EME probe said. The server polls it
// and republishes sources.changed when it moves, because the answer settles
// inside ordinary provider calls — warm-up, search, playback — with no engine
// notification to mark the moment.
func (p appleWebProvider) AvailabilitySignature() string {
	probe := p.probeWidevine()
	return fmt.Sprintf("available=%v|answered=%v|supported=%v", p.available(), probe.Answered, probe.Supported)
}

func (p appleWebProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	if strings.TrimSpace(term) == "" {
		return nil, api.Errorf(api.CodeInvalidRequest, "Apple Music search needs a term")
	}
	switch kind {
	case api.KindSong:
		songs, err := p.catalog.SearchSongs(ctx, term, limit)
		if err != nil {
			return nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music search failed")
		}
		return appleWebSongs(songs), nil
	case api.KindAlbum:
		albums, err := p.catalog.SearchAlbums(ctx, term, limit)
		if err != nil {
			return nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music search failed")
		}
		return appleWebAlbums(albums), nil
	case api.KindPlaylist:
		playlists, err := p.catalog.SearchPlaylists(ctx, term, limit)
		if err != nil {
			return nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music search failed")
		}
		return appleWebPlaylists(playlists), nil
	default:
		// The descriptor does not declare this kind, so the server refuses it
		// before reaching here; this keeps a direct caller honest too.
		return nil, api.Errorf(api.CodeUnsupportedCommand, "Apple Music on this platform can search songs, albums, and playlists only")
	}
}

func (p appleWebProvider) PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	playlist, tracks, err := p.catalog.PlaylistTracks(ctx, id)
	if err != nil {
		return api.Item{}, nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music playlist lookup failed")
	}
	return appleWebPlaylist(playlist), appleWebSongs(tracks), nil
}

func (p appleWebProvider) Trending(ctx context.Context, kind string, limit int) ([]api.Item, *api.Error) {
	if kind != api.KindSong {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "Apple Music browser trending supports songs only")
	}
	songs, err := p.catalog.TrendingSongs(ctx, limit)
	if err != nil {
		return nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music trending failed")
	}
	return appleWebSongs(songs), nil
}

func (p appleWebProvider) Recommendations(ctx context.Context, limit int) ([]api.Item, *api.Error) {
	recommendations, err := p.catalog.Recommendations(ctx, limit)
	if err != nil {
		return nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music recommendations failed")
	}
	items := make([]api.Item, 0, len(recommendations))
	for _, recommendation := range recommendations {
		switch recommendation.Kind {
		case api.KindPlaylist:
			items = append(items, appleWebPlaylist(appleweb.CatalogPlaylist{
				ID: recommendation.ID, Title: recommendation.Title, Artist: recommendation.Artist, URL: recommendation.URL,
			}))
		case api.KindAlbum:
			items = append(items, appleWebAlbum(appleweb.CatalogAlbum{
				ID: recommendation.ID, Title: recommendation.Title, Artist: recommendation.Artist, URL: recommendation.URL,
			}))
		}
	}
	return items, nil
}

func (p appleWebProvider) AlbumTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	album, tracks, err := p.catalog.AlbumTracks(ctx, id)
	if err != nil {
		return api.Item{}, nil, mapAppleWebError(err, api.CodeSearchFailed, "Apple Music album lookup failed")
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
	catalogDurations := make(map[string]int, len(request.References))
	// The container expansion already fetched complete song data; carrying it
	// here skips one page round trip per track (a 25-track playlist used to
	// spend ~18s re-resolving what the expansion already had). Anything missing
	// or mismatched falls back to the per-ref resolve so the fast path can
	// never substitute data the preparer would not have produced itself.
	resolved := request.ResolvedItems
	if len(resolved) != len(request.References) {
		resolved = nil
	}
	for i, reference := range request.References {
		if reference.Source != api.SourceAppleMusic || reference.Kind != api.KindSong || strings.TrimSpace(reference.ID) == "" {
			return nil, api.Errorf(api.CodeInvalidReference, "Apple Music playback needs song references")
		}
		var song appleweb.CatalogSong
		if resolved != nil {
			// The wire item never carries duration (the page reports it at
			// play time), so the resolved path loses nothing the fetched path
			// had.
			song = appleweb.CatalogSong{
				ID:     resolved[i].ProviderID,
				Title:  resolved[i].Title,
				Artist: resolved[i].Artist,
				Album:  resolved[i].Album,
				URL:    resolved[i].URL,
			}
		} else {
			fetched, err := p.catalog.Song(ctx, reference.ID)
			if err != nil {
				return nil, mapAppleWebError(err, api.CodePlaybackError, "Apple Music could not resolve that song")
			}
			song = fetched
		}
		queue = append(queue, appleWebSong(song))
		if song.DurationMs > 0 {
			catalogDurations[song.ID] = (song.DurationMs + 999) / 1000
		}
	}
	authorized, err := p.catalog.Authorized(ctx)
	if err != nil {
		return nil, mapAppleWebError(err, api.CodePlaybackError, "Apple Music session is unavailable")
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
		mode = URLQueueUnverified
	}
	// Catalog metadata obtained while preparing is private to this plan; items
	// on the wire stay stable and need not gain a duration field. Container
	// expansion lost that metadata, so resolve only the starting item on demand.
	return NewURLQueuePlanWithMode(api.SourceAppleMusic, queue, startIndex, mode,
		func(ctx context.Context, item api.Item) (urlResolution, error) {
			return p.resolveTarget(ctx, item, catalogDurations[item.ProviderID])
		}), nil
}

// resolveTarget re-reads the item when it starts and hands the queue the item's
// stable public page. The browser plays the catalog id in Item.ProviderID; the
// URL is carried because a URL queue expects one, and it is the same public
// page the item already publishes. Re-sample authorization on every start:
// signed-out is preview; signed-in remains unverified until the transport
// compares the page media length with this item's catalog length.
func (p appleWebProvider) resolveTarget(ctx context.Context, item api.Item, catalogSeconds int) (urlResolution, error) {
	if item.Source != api.SourceAppleMusic || item.Kind != api.KindSong || item.ProviderID == "" {
		return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Apple Music queue item is invalid")
	}
	if item.URL == "" {
		return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Apple Music queue item has no public URL")
	}
	authorized, err := p.catalog.Authorized(ctx)
	if err != nil {
		// A session that cannot be read cannot be trusted to label playback:
		// fail the start rather than guess a mode.
		return urlResolution{}, mapAppleWebError(err, api.CodePlaybackError, "Apple Music session is unavailable")
	}
	mode := URLQueuePreview
	if authorized {
		mode = URLQueueUnverified
		if catalogSeconds <= 0 {
			// A catalog outage must not abort a valid page playback. Without a
			// trustworthy full length the public mode remains unverified.
			if song, lookupErr := p.catalog.Song(ctx, item.ProviderID); lookupErr == nil &&
				song.ID == item.ProviderID && song.DurationMs > 0 {
				catalogSeconds = (song.DurationMs + 999) / 1000
			}
		}
	}
	return urlResolution{URL: item.URL, Mode: mode, Duration: catalogSeconds}, nil
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
		Kind:       api.KindSong,
		ID:         song.ID,
		URL:        song.URL,
		Title:      song.Title,
		Artist:     song.Artist,
		Album:      song.Album,
		DurationMs: song.DurationMs,
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

func appleWebPlaylists(playlists []appleweb.CatalogPlaylist) []api.Item {
	out := make([]api.Item, 0, len(playlists))
	for _, playlist := range playlists {
		if playlist.ID == "" || strings.TrimSpace(playlist.Title) == "" {
			continue
		}
		out = append(out, appleWebPlaylist(playlist))
	}
	return out
}

func appleWebPlaylist(playlist appleweb.CatalogPlaylist) api.Item {
	return api.ProjectCoreItem(core.Item{
		Kind:   api.KindPlaylist,
		ID:     playlist.ID,
		URL:    playlist.URL,
		Title:  playlist.Title,
		Artist: playlist.Artist,
	}, api.SourceAppleMusic)
}

// mapAppleWebError keeps the stable code meaningful: a missing browser is a
// source problem, an unknown id is a reference problem, and everything else is
// the discovery or the playback failure the caller already named with its code
// (discovery paths report search_failed, playback paths report
// playback_error — errors.md, not one shared bucket).
func mapAppleWebError(err error, code string, fallback string) *api.Error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, appleweb.ErrNoBrowser) {
		return api.Errorf(api.CodeSourceUnavailable, "%s", err.Error())
	}
	if errors.Is(err, appleweb.ErrProfileInUse) {
		return api.Errorf(api.CodeSourceUnavailable, "%s", err.Error())
	}
	if errors.Is(err, appleweb.ErrSignInInProgress) {
		return api.Errorf(api.CodeInvalidState, "%s", err.Error())
	}
	if errors.Is(err, appleweb.ErrUnauthorized) {
		return api.Errorf(api.CodeAuthorizationRequired, "sign in to Apple Music to read recommendations")
	}
	if strings.Contains(err.Error(), "was not found") {
		return api.Errorf(api.CodeInvalidReference, "%s", err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return api.Errorf(code, "%s: the Apple Music page did not answer in time", fallback)
	}
	return api.Errorf(code, "%s", fallback)
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

// AuthFlowBudget declares the sign-in window: a human has to type credentials
// in Apple's own page, possibly ride a 2FA round trip, and the ten-minute
// figure is the budget the flow context is built from — there is no second
// deadline inside the engine's sign-in loop.
func (*appleWebAuthProvider) AuthFlowBudget() time.Duration { return appleweb.SignInBudget }

// SignInStopsPlayback: the sign-in window closes the headless browser that is
// playing, so the server must stop Apple playback before the flow opens it.
func (*appleWebAuthProvider) SignInStopsPlayback() bool { return true }

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
		case errors.Is(err, context.DeadlineExceeded):
			// The declared budget ran out while nobody finished the sign-in:
			// that is an expired interaction, not a failed one, and conflating
			// the two is what made expired unreachable.
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

// DisconnectSupported reports that the browser session can be forgotten locally.
func (*appleWebAuthProvider) DisconnectSupported() bool { return true }

// Disconnect forgets the Apple session by removing the browser profile it lives
// in. A directory lilt did not create is refused rather than deleted.
func (p *appleWebAuthProvider) Disconnect(context.Context) *api.Error {
	switch err := p.session.Disconnect(); {
	case err == nil:
		return nil
	case errors.Is(err, appleweb.ErrForeignProfile):
		return api.Errorf(api.CodeInvalidState,
			"%s is not a lilt browser profile; sign out there or remove it yourself", err.Error())
	default:
		return api.Errorf(api.CodeAuthorizationFailed, "could not remove the Apple browser profile: %s", err.Error())
	}
}
