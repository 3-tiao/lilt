package server

import (
	"context"
	"strings"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
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

type appleProvider struct{ server *Server }

func (p appleProvider) Source() api.SourceID { return api.SourceAppleMusic }
func (p appleProvider) Descriptor(ctx context.Context) api.SourceDescriptor {
	return p.server.appleDescriptor(ctx)
}
func (p appleProvider) Search(ctx context.Context, term, kind string, limit int) ([]api.Item, *api.Error) {
	if err := p.server.requireEngine(); err != nil {
		return nil, err
	}
	var items []core.Item
	var err error
	switch kind {
	case api.KindSong:
		items, err = p.server.currentEngine().Search(ctx, term, limit)
	case api.KindPlaylist:
		items, err = p.server.currentEngine().SearchPlaylists(ctx, term, limit)
	case api.KindStation:
		items, err = p.server.currentEngine().Stations(ctx, term, limit)
	default:
		return nil, api.Errorf(api.CodeInvalidReference, "unsupported Apple Music search kind")
	}
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Apple Music search failed")
	}
	return p.server.projectItems(items, api.SourceAppleMusic), nil
}
func (p appleProvider) PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, *api.Error) {
	if err := p.server.requireEngine(); err != nil {
		return api.Item{}, nil, err
	}
	tracks, err := p.server.currentEngine().PlaylistTracks(ctx, id)
	if err != nil {
		return api.Item{}, nil, api.Errorf(api.CodeSearchFailed, "Apple Music playlist lookup failed")
	}
	return ProjectItem(core.Item{Kind: api.KindPlaylist, ID: id, Title: id}, api.SourceAppleMusic), p.server.projectItems(tracks, api.SourceAppleMusic), nil
}

type audiusProvider struct{ client audius.Client }

func (p audiusProvider) Source() api.SourceID { return api.SourceAudius }
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
			api.CapPlaybackFull:    {Available: true, Description: "Play Audius tracks and playlists."},
			api.CapQueue:           {Available: true, Description: "Finite queue controls."},
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
		tracks = loaded
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
	for _, track := range tracks {
		durations[track.ID] = track.Duration
	}
	startIndex := request.StartIndex
	if startIndex < 0 || startIndex >= len(queue) {
		return nil, api.Errorf(api.CodeInvalidReference, "Audius start index is out of range")
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
		if duration == 0 {
			// Items added to the queue after preparation resolve their duration
			// on demand.
			if track, trackErr := p.client.Track(resolveCtx, item.ProviderID); trackErr == nil {
				duration = track.Duration
			}
		}
		return urlResolution{URL: mediaURL, Duration: duration}, nil
	})
	return plan, nil
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
