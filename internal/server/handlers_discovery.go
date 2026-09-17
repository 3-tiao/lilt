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

func (s *Server) handleDiscoverySearch(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params discoveryParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Source == string(api.SourceRadio) {
		return s.searchRadio(ctx, radioSearchParams{Name: params.Term, Origin: api.OriginDirectory, Limit: params.Limit})
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	source := api.SourceAppleMusic
	result := api.SearchResult{Source: source, Term: params.Term, Groups: map[string][]api.Item{}}
	searchSongs := func() *api.Error {
		items, err := s.engine.Search(ctx, params.Term, limit)
		if err != nil {
			return api.Errorf(api.CodeSearchFailed, "%v", err)
		}
		if len(items) > 0 {
			result.Groups[api.GroupSongs] = s.projectItems(items, source)
		}
		return nil
	}
	searchPlaylists := func() *api.Error {
		items, err := s.engine.SearchPlaylists(ctx, params.Term, limit)
		if err != nil {
			return api.Errorf(api.CodeSearchFailed, "%v", err)
		}
		if len(items) > 0 {
			result.Groups[api.GroupPlaylists] = s.projectItems(items, source)
		}
		return nil
	}
	searchStations := func() *api.Error {
		items, err := s.engine.Stations(ctx, params.Term, limit)
		if err != nil {
			return api.Errorf(api.CodeSearchFailed, "%v", err)
		}
		if len(items) > 0 {
			result.Groups[api.GroupStations] = s.projectItems(items, source)
		}
		return nil
	}
	switch params.Type {
	case "song", "":
		if apiErr := searchSongs(); apiErr != nil {
			return nil, apiErr
		}
	case "playlist":
		if apiErr := searchPlaylists(); apiErr != nil {
			return nil, apiErr
		}
	case "station":
		if apiErr := searchStations(); apiErr != nil {
			return nil, apiErr
		}
	case "all":
		for _, search := range []func() *api.Error{searchSongs, searchPlaylists, searchStations} {
			if apiErr := search(); apiErr != nil {
				return nil, apiErr
			}
		}
	default:
		return nil, api.Errorf(api.CodeInvalidRequest, "type must be song, playlist, station, or all")
	}
	return result, nil
}

func (s *Server) handlePlaylistTracks(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	if reference.Kind != api.KindPlaylist {
		return nil, api.Errorf(api.CodeInvalidReference, "playlist.tracks needs a playlist ref")
	}
	tracks, err := s.engine.PlaylistTracks(ctx, reference.ID)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	playlist := ProjectItem(core.Item{Kind: api.KindPlaylist, ID: reference.ID, Title: params.Ref}, reference.Source)
	return api.PlaylistTracksResult{Playlist: playlist, Items: s.projectItems(tracks, reference.Source)}, nil
}

func (s *Server) handleLibraryPlaylists(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Source != "" && params.Source != string(api.SourceAppleMusic) {
		return nil, api.Errorf(api.CodeSourceUnavailable, "library is not available for %s", params.Source)
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	items, err := s.engine.LibraryPlaylists(ctx)
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
	if s.store == nil {
		return []api.Item{}, nil
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 25
	}
	items := make([]api.Item, 0, limit)
	for _, recent := range s.store.Recent {
		if len(items) >= limit {
			break
		}
		source := sourceFromStored(recent.Source)
		items = append(items, storedItem(source, recent.ID, recent.Kind, recent.Title, recent.Artist, recent.URL))
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
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	items, err := s.engine.Recommendations(ctx)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "%v", err)
	}
	return s.projectItems(items, api.SourceAppleMusic), nil
}
