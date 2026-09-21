package server

import (
	"context"
	"encoding/json"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

func (s *Server) projectItems(items []core.Item, source api.SourceID) []api.Item {
	projected := make([]api.Item, 0, len(items))
	for _, item := range items {
		projected = append(projected, ProjectItem(item, source))
	}
	return projected
}

type discoveryParams struct {
	Source string `json:"source"`
	Term   string `json:"term"`
	Type   string `json:"type"`
	Limit  int    `json:"limit"`
}

// searchCapability maps a public search kind to its standard capability name.
func searchCapability(kind string) (string, bool) {
	switch kind {
	case api.KindSong:
		return api.CapSearchSongs, true
	case api.KindAlbum:
		return api.CapSearchAlbums, true
	case api.KindPlaylist:
		return api.CapSearchPlaylists, true
	case api.KindStation:
		return api.CapSearchStations, true
	default:
		return "", false
	}
}

// handleDiscoveryTrending returns a source's trending tracks or playlists. It is
// an optional provider extension: sources that do not implement it return
// unsupported_command.
func (s *Server) handleDiscoveryTrending(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
		Type   string `json:"type"`
		Limit  int    `json:"limit"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Source == "" {
		return nil, api.Errorf(api.CodeInvalidRequest, "discovery.trending requires source")
	}
	if params.Source == string(api.SourceRadio) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "radio discovery uses radio.search")
	}
	source := api.SourceID(params.Source)
	provider, ok := s.providers[source]
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "trending is not available for %s", source)
	}
	if !declaresCapability(provider.Descriptor(ctx), api.CapSearchTrending) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not declare trending", source)
	}
	trending, ok := provider.(TrendingProvider)
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support trending", source)
	}
	kind := params.Type
	if kind == "" {
		kind = api.KindSong
	}
	group, ok := map[string]string{api.KindSong: api.GroupSongs, api.KindPlaylist: api.GroupPlaylists}[kind]
	if !ok {
		return nil, api.Errorf(api.CodeInvalidRequest, "type must be song or playlist")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	items, apiErr := trending.Trending(ctx, kind, limit)
	if apiErr != nil {
		return nil, apiErr
	}
	result := api.SearchResult{Source: source, Groups: map[string][]api.Item{}}
	if len(items) > 0 {
		result.Groups[group] = items
	}
	return result, nil
}

func (s *Server) handleDiscoverySearch(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params discoveryParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Source == "" {
		return nil, api.Errorf(api.CodeInvalidRequest, "discovery.search requires source")
	}
	if params.Source == string(api.SourceRadio) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "radio discovery uses radio.search")
	}
	source := api.SourceID(params.Source)
	provider, ok := s.providers[source]
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "discovery is not available for %s", source)
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	// The provider descriptor is the single source of truth for which search
	// kinds the source declares. A kind whose standard capability is absent is
	// unsupported; a declared but temporarily unavailable capability is left to
	// the provider so it can surface the dynamic error.
	descriptor := provider.Descriptor(ctx)
	supported := func(kind string) bool {
		capability, ok := searchCapability(kind)
		if !ok {
			return false
		}
		_, declared := descriptor.Capabilities[capability]
		return declared
	}
	result := api.SearchResult{Source: source, Term: params.Term, Groups: map[string][]api.Item{}}
	searchSongs := func() *api.Error {
		items, err := provider.Search(ctx, params.Term, api.KindSong, limit)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			result.Groups[api.GroupSongs] = items
		}
		return nil
	}
	searchAlbums := func() *api.Error {
		items, err := provider.Search(ctx, params.Term, api.KindAlbum, limit)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			result.Groups[api.GroupAlbums] = items
		}
		return nil
	}
	searchPlaylists := func() *api.Error {
		items, err := provider.Search(ctx, params.Term, api.KindPlaylist, limit)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			result.Groups[api.GroupPlaylists] = items
		}
		return nil
	}
	searchStations := func() *api.Error {
		items, err := provider.Search(ctx, params.Term, api.KindStation, limit)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			result.Groups[api.GroupStations] = items
		}
		return nil
	}
	run := func(kind string, search func() *api.Error) *api.Error {
		if !supported(kind) {
			return api.Errorf(api.CodeUnsupportedCommand, "%s search does not support %s", source, kind)
		}
		return search()
	}
	switch params.Type {
	case "song", "":
		if apiErr := run(api.KindSong, searchSongs); apiErr != nil {
			return nil, apiErr
		}
	case "album":
		if apiErr := run(api.KindAlbum, searchAlbums); apiErr != nil {
			return nil, apiErr
		}
	case "playlist":
		if apiErr := run(api.KindPlaylist, searchPlaylists); apiErr != nil {
			return nil, apiErr
		}
	case "station":
		if apiErr := run(api.KindStation, searchStations); apiErr != nil {
			return nil, apiErr
		}
	case "all":
		groups := []struct {
			kind   string
			search func() *api.Error
		}{
			{api.KindSong, searchSongs},
			{api.KindAlbum, searchAlbums},
			{api.KindPlaylist, searchPlaylists},
			{api.KindStation, searchStations},
		}
		for _, group := range groups {
			if !supported(group.kind) {
				continue
			}
			if apiErr := group.search(); apiErr != nil {
				return nil, apiErr
			}
		}
	default:
		return nil, api.Errorf(api.CodeInvalidRequest, "type must be song, album, playlist, station, or all")
	}
	return result, nil
}

// handleAlbumTracks resolves one album and its track listing by canonical
// album ref. Apple Music only; other sources return unsupported_command.
func (s *Server) handleAlbumTracks(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	if reference.Kind != api.KindAlbum {
		return nil, api.Errorf(api.CodeInvalidReference, "album.tracks needs an album ref")
	}
	provider, ok := s.providers[reference.Source]
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "album lookup is not available for %s", reference.Source)
	}
	albumProvider, ok := provider.(AlbumProvider)
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "album lookup is not supported")
	}
	album, tracks, providerErr := albumProvider.AlbumTracks(ctx, reference.ID)
	if providerErr != nil {
		return nil, providerErr
	}
	return api.AlbumTracksResult{Album: album, Items: tracks}, nil
}

func (s *Server) handlePlaylistTracks(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	if reference.Kind != api.KindPlaylist {
		return nil, api.Errorf(api.CodeInvalidReference, "playlist.tracks needs a playlist ref")
	}
	provider, ok := s.providers[reference.Source]
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "playlist lookup is not available for %s", reference.Source)
	}
	playlistProvider, ok := provider.(PlaylistProvider)
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "playlist lookup is not supported")
	}
	playlist, tracks, providerErr := playlistProvider.PlaylistTracks(ctx, reference.ID)
	if providerErr != nil {
		return nil, providerErr
	}
	return api.PlaylistTracksResult{Playlist: playlist, Items: tracks}, nil
}

func (s *Server) handleLibraryPlaylists(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	source := api.SourceID(params.Source)
	if source == "" {
		source = api.SourceAppleMusic
	}
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapLibrary) {
		return nil, api.Errorf(api.CodeSourceUnavailable, "library is not available for %s", source)
	}
	if source == api.SourceAudius {
		provider, ok := s.providers[source].(LibraryProvider)
		if !ok {
			return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support the library", source)
		}
		items, apiErr := provider.LibraryPlaylists(ctx)
		if apiErr != nil {
			return nil, apiErr
		}
		return items, nil
	}
	engine := s.currentEngine()
	if engine == nil {
		return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music playback engine is unavailable")
	}
	items, err := engine.LibraryPlaylists(ctx)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return s.projectItems(items, api.SourceAppleMusic), nil
}

// handleLibraryAlbums lists the connected account's albums. Only Apple's
// MusicKit library exposes albums today; Audius has no album concept.
func (s *Server) handleLibraryAlbums(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	source := api.SourceID(params.Source)
	if source == "" {
		source = api.SourceAppleMusic
	}
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapLibrary) {
		return nil, api.Errorf(api.CodeSourceUnavailable, "library is not available for %s", source)
	}
	if source != api.SourceAppleMusic {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support library albums", source)
	}
	engine := s.currentEngine()
	if engine == nil {
		return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music playback engine is unavailable")
	}
	items, err := engine.LibraryAlbums(ctx)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return s.projectItems(items, api.SourceAppleMusic), nil
}

func (s *Server) handleRecentList(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Limit int `json:"limit"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Limit <= 0 {
		params.Limit = 25
	}
	if params.Limit > 200 {
		params.Limit = 200
	}
	items := []api.Item{}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	entries, err := s.activity.RecentEntries(params.Limit)
	if err != nil {
		return nil, s.activityRequired()
	}
	for _, entry := range entries {
		items = append(items, activityItemToAPI(entry.Item))
	}
	return items, nil
}

func (s *Server) handleRecommendations(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	engine := s.currentEngine()
	if engine == nil {
		return nil, api.Errorf(api.CodeSourceUnavailable, "Apple Music playback engine is unavailable")
	}
	items, err := engine.Recommendations(ctx)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return s.projectItems(items, api.SourceAppleMusic), nil
}
