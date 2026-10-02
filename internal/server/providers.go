package server

import (
	"context"
	"errors"
	"html"
	"net/url"
	"strings"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/jamendo"
)

// ContentProvider is the compiled-in discovery contract. Playback preparation
// is intentionally a separate optional interface for later phases. Routing and
// capability checks read Descriptor: it is the single source of truth for which
// search kinds a source declares.
type ContentProvider interface {
	Source() api.SourceID
	Descriptor(context.Context) api.SourceDescriptor
	Search(context.Context, string, string, int) ([]api.Item, *api.Error)
}
type PlaylistProvider interface {
	PlaylistTracks(context.Context, string) (api.Item, []api.Item, *api.Error)
}

// AlbumProvider is an optional extension for sources that can resolve an
// album's track listing (Apple Music via the helper).
type AlbumProvider interface {
	AlbumTracks(context.Context, string) (api.Item, []api.Item, *api.Error)
}

// LibraryProvider is an optional extension for sources whose account library
// can be read (Apple Music, Audius when an account is linked). The handler
// routes by this assertion instead of a source-name switch.
type LibraryProvider interface {
	LibraryPlaylists(context.Context) ([]api.Item, *api.Error)
}

// LibraryAlbumsProvider is an optional extension for sources whose account
// library exposes albums. Sources without an album concept (Audius) do not
// implement it, which is what library.albums reports as unsupported_command.
type LibraryAlbumsProvider interface {
	LibraryAlbums(context.Context) ([]api.Item, *api.Error)
}

// TrendingProvider is an optional discovery extension for sources with a
// trending surface. The handler routes by this interface; a source that does not
// implement it returns unsupported_command.
type TrendingProvider interface {
	Trending(context.Context, string, int) ([]api.Item, *api.Error)
}

// RecommendationsProvider is an optional discovery extension for sources with a
// personalized recommendation surface. The handler routes by this interface and
// the descriptor's recommendations capability; a source that implements
// neither returns unsupported_command.
type RecommendationsProvider interface {
	Recommendations(ctx context.Context, limit int) ([]api.Item, *api.Error)
}

// ItemResolver is the optional source-specific metadata lookup used by
// favorites.add when the activity store has not seen a ref yet.
type ItemResolver interface {
	ResolveItem(context.Context, string, string) (api.Item, *api.Error)
}

type appleProvider struct{ server *Server }

func (p appleProvider) Source() api.SourceID { return api.SourceAppleMusic }
func (p appleProvider) Descriptor(ctx context.Context) api.SourceDescriptor {
	return p.server.appleDescriptor(ctx)
}
func (p appleProvider) resource(ctx context.Context) (AppleResourceClient, *api.Error) {
	return p.server.appleResourceClient(ctx)
}
func (p appleProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return nil, resourceErr
	}
	var items []core.Item
	var err error
	switch kind {
	case api.KindSong:
		items, err = resource.Search(ctx, term, limit)
	case api.KindAlbum:
		items, err = resource.SearchAlbums(ctx, term, limit)
	case api.KindPlaylist:
		items, err = resource.SearchPlaylists(ctx, term, limit)
	case api.KindStation:
		items, err = resource.Stations(ctx, term, limit)
	default:
		return nil, api.Errorf(api.CodeInvalidReference, "unsupported Apple Music search kind")
	}
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return nil, api.Errorf(api.CodeSearchFailed, "Apple Music search failed")
	}
	return p.server.projectItems(items, api.SourceAppleMusic), nil
}
func (p appleProvider) AlbumTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return api.Item{}, nil, resourceErr
	}
	album, tracks, err := resource.AlbumTracks(ctx, id)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return api.Item{}, nil, api.Errorf(api.CodeSearchFailed, "Apple Music album lookup failed")
	}
	return ProjectItem(album, api.SourceAppleMusic), p.server.projectItems(tracks, api.SourceAppleMusic), nil
}

func (p appleProvider) PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return api.Item{}, nil, resourceErr
	}
	playlist, tracks, err := resource.PlaylistTracks(ctx, id)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return api.Item{}, nil, api.Errorf(api.CodeSearchFailed, "Apple Music playlist lookup failed")
	}
	return ProjectItem(playlist, api.SourceAppleMusic), p.server.projectItems(tracks, api.SourceAppleMusic), nil
}

// LibraryPlaylists lists the account's cloud playlists through MusicKit. The
// handler routes by the LibraryProvider assertion; the projection and error
// mapping match the resource runtime's own semantics.
func (p appleProvider) LibraryPlaylists(ctx context.Context) ([]api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return nil, resourceErr
	}
	items, err := resource.LibraryPlaylists(ctx)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return p.server.projectItems(items, api.SourceAppleMusic), nil
}

// LibraryAlbums lists the account's cloud albums through MusicKit.
func (p appleProvider) LibraryAlbums(ctx context.Context) ([]api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return nil, resourceErr
	}
	items, err := resource.LibraryAlbums(ctx)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return p.server.projectItems(items, api.SourceAppleMusic), nil
}

// Recommendations lists the account's recommendation groups through MusicKit.
// The helper owns the flattening (its projection dedups playlists and stations);
// the limit is applied here because the helper RPC has no limit parameter.
func (p appleProvider) Recommendations(ctx context.Context, limit int) ([]api.Item, *api.Error) {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return nil, resourceErr
	}
	items, err := resource.Recommendations(ctx)
	if err != nil {
		// mapAppleResourceError keeps the helper's own codes meaningful: an
		// unauthorized account is authorization_required, a transport failure
		// invalidates the runtime, everything else is the search failure.
		return nil, p.server.mapAppleResourceError(resource, err)
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return p.server.projectItems(items, api.SourceAppleMusic), nil
}

type audiusProvider struct {
	client audius.Client
	// credentials reports the connected account (token, user id) when present.
	// It gates the account-only library capability.
	credentials func() (string, string, bool)
}

func (p audiusProvider) Source() api.SourceID { return api.SourceAudius }

// ResolveItem resolves Audius metadata for favorites.add when the activity
// store has not seen the ref yet.
func (p audiusProvider) ResolveItem(ctx context.Context, kind, providerID string) (api.Item, *api.Error) {
	switch kind {
	case api.KindSong:
		track, apiErr := p.client.Track(ctx, providerID)
		if apiErr != nil {
			return api.Item{}, apiErr
		}
		return ProjectItem(core.Item{
			Kind: api.KindSong, ID: track.ID, Title: track.Title, Artist: track.User.Name,
		}, api.SourceAudius), nil
	case api.KindPlaylist:
		playlist, apiErr := p.client.Playlist(ctx, providerID)
		if apiErr != nil {
			return api.Item{}, apiErr
		}
		return audiusPlaylist(playlist), nil
	default:
		return api.Item{}, api.Errorf(api.CodeUnsupportedCommand, "Audius can resolve songs and playlists")
	}
}

func (p audiusProvider) Descriptor(context.Context) api.SourceDescriptor {
	return api.SourceDescriptor{
		ID:           api.SourceAudius,
		Label:        "Audius",
		Priority:     80,
		Available:    true,
		Availability: api.AvailabilityReady,
		Description:  "Anonymous Audius discovery and finite full playback over the official api.audius.co REST API.",
		Capabilities: map[string]api.Capability{
			api.CapSearchSongs:     {Available: true, Description: "Search Audius tracks by text query."},
			api.CapSearchPlaylists: {Available: true, Description: "Search Audius playlists by text query."},
			api.CapSearchTrending:  {Available: true, Description: "Browse official Audius trending tracks and playlists."},
			api.CapPlaybackFull:    {Available: true, Description: "Play Audius tracks and playlists."},
			api.CapQueue:           {Available: true, Description: "Finite queue controls."},
			api.CapLibrary:         {Available: p.authorized(), Reason: libraryReason(p.authorized()), Description: "Read the connected Audius account's playlists."},
		},
	}
}
func (p audiusProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	switch kind {
	case api.KindSong:
		tracks, err := p.client.SearchTracks(ctx, term, limit)
		if err != nil {
			return nil, err
		}
		return audiusTracks(tracks), nil
	case api.KindPlaylist:
		lists, err := p.client.SearchPlaylists(ctx, term, limit)
		if err != nil {
			return nil, err
		}
		return audiusPlaylists(lists), nil
	default:
		return nil, api.Errorf(api.CodeInvalidReference, "unsupported Audius search kind")
	}
}
func (p audiusProvider) PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	playlist, err := p.client.Playlist(ctx, id)
	if err != nil {
		return api.Item{}, nil, err
	}
	tracks, err := p.client.PlaylistTracks(ctx, id)
	if err != nil {
		return api.Item{}, nil, err
	}
	return audiusPlaylist(playlist), audiusTracks(tracks), nil
}

// Trending returns official trending tracks or playlists.
func (p audiusProvider) Trending(ctx context.Context, kind string, limit int) ([]api.Item, *api.Error) {
	switch kind {
	case api.KindSong:
		tracks, err := p.client.TrendingTracks(ctx, limit)
		if err != nil {
			return nil, err
		}
		return audiusTracks(tracks), nil
	case api.KindPlaylist:
		playlists, err := p.client.TrendingPlaylists(ctx, limit)
		if err != nil {
			return nil, err
		}
		return audiusPlaylists(playlists), nil
	default:
		return nil, api.Errorf(api.CodeInvalidReference, "Audius trending supports songs and playlists")
	}
}

// PreparePlayback builds a stable public queue and a lazy per-track resolver.
func (p audiusProvider) PreparePlayback(ctx context.Context, request PlaybackRequest) (PreparedPlayback, *api.Error) {
	if len(request.References) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius playback requires a track or playlist ref")
	}
	for _, reference := range request.References {
		if reference.Source != api.SourceAudius {
			return nil, api.Errorf(api.CodeInvalidReference, "playback ref does not belong to Audius")
		}
	}
	reference := request.References[0]
	if reference.Source != api.SourceAudius {
		return nil, api.Errorf(api.CodeInvalidReference, "playback ref does not belong to Audius")
	}
	var tracks []audius.Track
	if len(request.References) > 1 {
		ids := make([]string, 0, len(request.References))
		for _, ref := range request.References {
			if ref.Kind != api.KindSong {
				return nil, api.Errorf(api.CodeInvalidReference, "Audius queue refs must be tracks")
			}
			ids = append(ids, ref.ID)
		}
		loaded, err := p.client.Tracks(ctx, ids)
		if err != nil {
			return nil, err
		}
		// Audius returns the batch in its own order; restore the caller's
		// order so an unshuffled queue matches the submitted refs and StartIndex
		// selects the intended track.
		byID := make(map[string]audius.Track, len(loaded))
		for _, track := range loaded {
			byID[track.ID] = track
		}
		tracks = make([]audius.Track, 0, len(ids))
		for _, id := range ids {
			track, ok := byID[id]
			// Explicit refs are an exact queue, not a discovery result. Dropping
			// missing or unplayable tracks would shift StartIndex to another song.
			if !ok || !track.IsStreamable {
				return nil, api.Errorf(api.CodeInvalidReference, "Audius queue contains a missing or unplayable track")
			}
			tracks = append(tracks, track)
		}
	} else {
		switch reference.Kind {
		case api.KindSong:
			track, err := p.client.Track(ctx, reference.ID)
			if err != nil {
				return nil, err
			}
			tracks = []audius.Track{track}
		case api.KindPlaylist:
			loaded, err := p.client.PlaylistTracks(ctx, reference.ID)
			if err != nil {
				return nil, err
			}
			tracks = loaded
		default:
			return nil, api.Errorf(api.CodeInvalidReference, "Audius playback supports tracks and playlists")
		}
	}
	queue := audiusTracks(tracks)
	if len(queue) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius resource has no playable tracks")
	}
	durations := make(map[string]int, len(tracks))
	artwork := make(map[string]string, len(tracks))
	for _, track := range tracks {
		durations[track.ID] = track.Duration
		artwork[track.ID] = track.ArtworkURL()
	}
	startIndex := request.StartIndex
	if startIndex < 0 || startIndex >= len(queue) {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius start index is out of range")
	}
	if request.FromHere {
		// Forward-only "play from here": drop earlier tracks rather than keeping
		// them as a dimmed history ahead of the current item.
		queue = queue[startIndex:]
		startIndex = 0
	}
	plan := NewURLQueuePlan(api.SourceAudius, queue, startIndex, func(resolveCtx context.Context, item api.Item) (urlResolution, error) {
		if item.Source != api.SourceAudius || item.Kind != api.KindSong || item.ProviderID == "" {
			return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Audius queue item is invalid")
		}
		mediaURL, resolveErr := p.client.StreamURL(resolveCtx, item.ProviderID)
		if resolveErr != nil {
			return urlResolution{}, resolveErr
		}
		duration := durations[item.ProviderID]
		if duration == 0 || artwork[item.ProviderID] == "" {
			// Playlist lists may omit artwork (and items added after preparation
			// may omit duration); resolve both from the single-track lookup.
			if track, trackErr := p.client.Track(resolveCtx, item.ProviderID); trackErr == nil {
				if duration == 0 {
					duration = track.Duration
				}
				if artwork[item.ProviderID] == "" {
					artwork[item.ProviderID] = track.ArtworkURL()
				}
			}
		}
		return urlResolution{URL: mediaURL, ArtworkURL: artwork[item.ProviderID], Duration: duration}, nil
	})
	return plan, nil
}

// LibraryPlaylists lists the connected account's playlists. It is only
// available when an account is linked (capability gated by authorization).
func (p audiusProvider) LibraryPlaylists(ctx context.Context) ([]api.Item, *api.Error) {
	if p.credentials == nil {
		return nil, api.Errorf(api.CodeAuthorizationRequired, "Audius account linking is not configured")
	}
	token, userID, ok := p.credentials()
	if !ok {
		return nil, api.Errorf(api.CodeAuthorizationRequired, "connect an Audius account to read your playlists")
	}
	lists, err := p.client.UserPlaylists(ctx, userID, token, 50)
	if err != nil {
		return nil, err
	}
	return audiusPlaylists(lists), nil
}

func (p audiusProvider) authorized() bool {
	if p.credentials == nil {
		return false
	}
	_, _, ok := p.credentials()
	return ok
}

func libraryReason(authorized bool) string {
	if authorized {
		return ""
	}
	return "Connect an Audius account to read your playlists."
}

func audiusTracks(tracks []audius.Track) []api.Item {
	out := make([]api.Item, 0, len(tracks))
	for _, t := range tracks {
		// Audius returns non-streamable tracks too; discovery only offers the
		// ones the source can actually play.
		if t.ID == "" || !t.IsStreamable {
			continue
		}
		out = append(out, api.Item{Source: api.SourceAudius, Kind: api.KindSong, ID: api.AudiusRef(api.KindSong, t.ID), ProviderID: t.ID, Ref: api.AudiusRef(api.KindSong, t.ID), URL: audiusPublicURL(t.Permalink), Title: t.Title, Artist: t.User.Name})
	}
	return out
}
func audiusPlaylist(p audius.Playlist) api.Item {
	return api.Item{Source: api.SourceAudius, Kind: api.KindPlaylist, ID: api.AudiusRef(api.KindPlaylist, p.ID), ProviderID: p.ID, Ref: api.AudiusRef(api.KindPlaylist, p.ID), URL: audiusPublicURL(p.Permalink), Title: p.PlaylistName, Artist: p.User.Name}
}

// audiusPublicURL turns the API's relative permalink into the canonical public
// web URL. It never carries a signed media URL.
func audiusPublicURL(permalink string) string {
	permalink = strings.TrimSpace(permalink)
	if permalink == "" {
		return ""
	}
	if strings.HasPrefix(permalink, "http://") || strings.HasPrefix(permalink, "https://") {
		return permalink
	}
	return "https://audius.co" + permalink
}
func audiusPlaylists(lists []audius.Playlist) []api.Item {
	out := make([]api.Item, 0, len(lists))
	for _, p := range lists {
		if p.ID != "" {
			out = append(out, audiusPlaylist(p))
		}
	}
	return out
}

type jamendoProvider struct {
	client      jamendo.Client
	credentials jamendo.Credentials
}

func (p jamendoProvider) Source() api.SourceID { return api.SourceJamendo }

func (p jamendoProvider) Descriptor(context.Context) api.SourceDescriptor {
	descriptor := api.SourceDescriptor{
		ID:           api.SourceJamendo,
		Label:        "Jamendo",
		Priority:     70,
		Availability: api.AvailabilityReady,
		Description:  "Non-commercial Jamendo discovery over the official api.jamendo.com v3.0 API. Run `lilt jamendo setup` with your own client_id.",
		Capabilities: map[string]api.Capability{
			api.CapSearchSongs:         {Available: true, Description: "Search Jamendo tracks by free text."},
			api.CapSearchPlaylists:     {Available: true, Description: "Search Jamendo playlists by name."},
			api.CapSearchTrendingSongs: {Available: true, Description: "Browse Jamendo's featured tracks by monthly popularity."},
			api.CapPlaybackFull:        {Available: true, Description: "Play Jamendo tracks and playlists."},
			api.CapQueue:               {Available: true, Description: "Finite queue controls."},
		},
	}
	if _, err := p.clientID(); err != nil {
		reason := "Jamendo is not configured; run `lilt jamendo setup`"
		if !errors.Is(err, jamendo.ErrNotConfigured) {
			reason = "Jamendo credentials could not be read"
		}
		descriptor.Availability = api.AvailabilityUnavailable
		descriptor.Reason = reason
		for name, capability := range descriptor.Capabilities {
			capability.Available = false
			capability.Reason = reason
			descriptor.Capabilities[name] = capability
		}
	}
	descriptor.Available = anyAvailable(descriptor.Capabilities)
	return descriptor
}

func (p jamendoProvider) clientID() (string, error) {
	if p.credentials == nil {
		return "", jamendo.ErrNotConfigured
	}
	return p.credentials()
}

func (p jamendoProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	switch kind {
	case api.KindSong:
		tracks, apiErr := p.client.SearchTracks(ctx, term, limit)
		if apiErr != nil {
			return nil, apiErr
		}
		return jamendoTracks(tracks), nil
	case api.KindPlaylist:
		playlists, apiErr := p.client.SearchPlaylists(ctx, term, limit)
		if apiErr != nil {
			return nil, apiErr
		}
		return jamendoPlaylists(playlists), nil
	default:
		return nil, api.Errorf(api.CodeInvalidReference, "unsupported Jamendo search kind")
	}
}

func (p jamendoProvider) Trending(ctx context.Context, kind string, limit int) ([]api.Item, *api.Error) {
	if kind != api.KindSong {
		// The songs-only capability is the routing truth: Jamendo has no
		// playlist popularity ordering.
		return nil, api.Errorf(api.CodeUnsupportedCommand, "Jamendo trending supports songs only")
	}
	tracks, apiErr := p.client.TrendingTracks(ctx, limit)
	if apiErr != nil {
		return nil, apiErr
	}
	return jamendoTracks(tracks), nil
}

func (p jamendoProvider) PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	playlist, apiErr := p.client.Playlist(ctx, id)
	if apiErr != nil {
		return api.Item{}, nil, apiErr
	}
	tracks, apiErr := p.client.PlaylistTracks(ctx, id)
	if apiErr != nil {
		return api.Item{}, nil, apiErr
	}
	return jamendoPlaylist(playlist), jamendoTracks(tracks), nil
}

// PreparePlayback builds a stable public queue and resolves a fresh mp32 media
// URL only when each track starts. Media URLs never enter Item or persisted state.
func (p jamendoProvider) PreparePlayback(ctx context.Context, request PlaybackRequest) (PreparedPlayback, *api.Error) {
	if len(request.References) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Jamendo playback requires a track or playlist ref")
	}
	var tracks []jamendo.Track
	if len(request.References) > 1 {
		tracks = make([]jamendo.Track, 0, len(request.References))
		for _, reference := range request.References {
			if reference.Source != api.SourceJamendo || reference.Kind != api.KindSong {
				return nil, api.Errorf(api.CodeInvalidReference, "Jamendo queue refs must be Jamendo tracks")
			}
			track, apiErr := p.client.Track(ctx, reference.ID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			tracks = append(tracks, track)
		}
	} else {
		reference := request.References[0]
		if reference.Source != api.SourceJamendo {
			return nil, api.Errorf(api.CodeInvalidReference, "playback ref does not belong to Jamendo")
		}
		switch reference.Kind {
		case api.KindSong:
			track, apiErr := p.client.Track(ctx, reference.ID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			tracks = []jamendo.Track{track}
		case api.KindPlaylist:
			loaded, apiErr := p.client.PlaylistTracks(ctx, reference.ID)
			if apiErr != nil {
				return nil, apiErr
			}
			tracks = loaded
		default:
			return nil, api.Errorf(api.CodeInvalidReference, "Jamendo playback supports tracks and playlists")
		}
	}
	queue := jamendoTracks(tracks)
	if len(queue) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "Jamendo resource has no playable tracks")
	}
	if len(request.References) > 1 && len(queue) != len(request.References) {
		return nil, api.Errorf(api.CodeInvalidReference, "Jamendo queue contains an unplayable track")
	}
	startIndex := request.StartIndex
	if startIndex < 0 || startIndex >= len(queue) {
		return nil, api.Errorf(api.CodeInvalidReference, "Jamendo start index is out of range")
	}
	if request.FromHere {
		queue = queue[startIndex:]
		startIndex = 0
	}
	plan := NewURLQueuePlan(api.SourceJamendo, queue, startIndex, func(resolveCtx context.Context, item api.Item) (urlResolution, error) {
		if item.Source != api.SourceJamendo || item.Kind != api.KindSong || item.ProviderID == "" {
			return urlResolution{}, api.Errorf(api.CodeInvalidReference, "Jamendo queue item is invalid")
		}
		track, resolveErr := p.client.Track(resolveCtx, item.ProviderID, "mp32")
		if resolveErr != nil {
			return urlResolution{}, jamendoPlaybackError(resolveErr)
		}
		mediaURL := strings.TrimSpace(track.Audio)
		parsed, parseErr := url.Parse(mediaURL)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return urlResolution{}, api.Errorf(api.CodeSourceUnavailable, "Jamendo returned an invalid media URL")
		}
		return urlResolution{URL: mediaURL, ArtworkURL: strings.TrimSpace(track.ArtworkURL()), Duration: track.DurationSeconds()}, nil
	})
	return plan, nil
}

func jamendoPlaybackError(err *api.Error) *api.Error {
	if err != nil && err.Details["providerCode"] == "6" {
		return api.Errorf(api.CodeSourceUnavailable, "Jamendo rate limit exceeded").WithDetails(err.Details)
	}
	return err
}

func (p jamendoProvider) ResolveItem(ctx context.Context, kind, providerID string) (api.Item, *api.Error) {
	switch kind {
	case api.KindSong:
		track, apiErr := p.client.Track(ctx, providerID, "")
		if apiErr != nil {
			return api.Item{}, apiErr
		}
		item, ok := jamendoTrack(track)
		if !ok {
			return api.Item{}, api.Errorf(api.CodeInvalidReference, "Jamendo track is not playable")
		}
		return item, nil
	case api.KindPlaylist:
		playlist, apiErr := p.client.Playlist(ctx, providerID)
		if apiErr != nil {
			return api.Item{}, apiErr
		}
		item := jamendoPlaylist(playlist)
		if item.ID == "" || item.Title == "" {
			return api.Item{}, api.Errorf(api.CodeInvalidReference, "Jamendo playlist was not found")
		}
		return item, nil
	default:
		return api.Item{}, api.Errorf(api.CodeUnsupportedCommand, "Jamendo can resolve songs and playlists")
	}
}

func jamendoTracks(tracks []jamendo.Track) []api.Item {
	out := make([]api.Item, 0, len(tracks))
	for _, track := range tracks {
		if item, ok := jamendoTrack(track); ok {
			out = append(out, item)
		}
	}
	return out
}

// jamendoText normalizes Jamendo's user-facing text. The API embeds HTML
// entities in names (a track titled "… &amp; …" reached the TUI verbatim,
// batch 2026-09-22-recheck); the raw client stays faithful to upstream and the
// provider boundary decodes.
func jamendoText(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}

func jamendoTrack(track jamendo.Track) (api.Item, bool) {
	id := strings.TrimSpace(track.ID)
	title := jamendoText(track.Name)
	artist := jamendoText(track.ArtistName)
	// The audio field is never projected, but its presence is the only public
	// API signal that this track can actually be played.
	if id == "" || title == "" || artist == "" || strings.TrimSpace(track.Audio) == "" {
		return api.Item{}, false
	}
	ref := api.JamendoRef(api.KindSong, id)
	publicURL := strings.TrimSpace(track.ShareURL)
	if publicURL == "" {
		publicURL = "https://www.jamendo.com/track/" + id
	}
	return api.Item{Source: api.SourceJamendo, Kind: api.KindSong, ID: ref, ProviderID: id, Ref: ref, URL: publicURL, Title: title, Artist: artist}, true
}

func jamendoPlaylists(playlists []jamendo.Playlist) []api.Item {
	out := make([]api.Item, 0, len(playlists))
	for _, playlist := range playlists {
		item := jamendoPlaylist(playlist)
		if item.ID != "" && item.Title != "" {
			out = append(out, item)
		}
	}
	return out
}

func jamendoPlaylist(playlist jamendo.Playlist) api.Item {
	id := strings.TrimSpace(playlist.ID)
	if id == "" {
		return api.Item{}
	}
	ref := api.JamendoRef(api.KindPlaylist, id)
	publicURL := strings.TrimSpace(playlist.ShareURL)
	if publicURL == "" {
		publicURL = "https://www.jamendo.com/list/p" + id
	}
	return api.Item{Source: api.SourceJamendo, Kind: api.KindPlaylist, ID: ref, ProviderID: id, Ref: ref, URL: publicURL, Title: jamendoText(playlist.Name), Artist: jamendoText(playlist.UserName)}
}

// descriptorFor returns a source's public descriptor whether it is a registered
// discovery provider or the built-in radio source.
func (s *Server) descriptorFor(ctx context.Context, source api.SourceID) (api.SourceDescriptor, bool) {
	if provider, ok := s.providers[source]; ok {
		return provider.Descriptor(ctx), true
	}
	if source == api.SourceRadio {
		return s.radioDescriptor(), true
	}
	return api.SourceDescriptor{}, false
}

// declaresCapability reports whether a source declares a capability at all.
// Declaration (key presence) is the routing truth; availability is orthogonal
// and may depend on authorization, subscription, or network.
func declaresCapability(descriptor api.SourceDescriptor, name string) bool {
	_, ok := descriptor.Capabilities[name]
	return ok
}
