package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

func (s *Server) handleStateGet(_ context.Context, _ json.RawMessage) (any, *api.Error) {
	return s.appState(), nil
}

func (s *Server) handleFavoritesList(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	items := []api.Item{}
	for _, favorite := range s.appState().Favorites {
		if params.Source != "" && string(favorite.Source) != params.Source {
			continue
		}
		items = append(items, favorite)
	}
	return items, nil
}

func (s *Server) handleFavoritesSet(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Item      api.Item `json:"item"`
		Favorited bool     `json:"favorited"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Item.Source == "" {
		return nil, api.Errorf(api.CodeInvalidRequest, "favorites.set needs item.source")
	}
	if apiErr := s.saveState(func(next *state.Store) {
		setFavorite(next, params.Item, params.Favorited)
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: params.Favorited, Item: params.Item}, nil
}

func (s *Server) handleUISet(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Theme      *string `json:"theme"`
		LastSource *string `json:"lastSource"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if apiErr := s.saveState(func(next *state.Store) {
		if params.Theme != nil {
			next.Theme = *params.Theme
		}
		if params.LastSource != nil {
			next.LastSource = *params.LastSource
		}
	}); apiErr != nil {
		return nil, apiErr
	}
	return s.appState(), nil
}

func setFavorite(store *state.Store, item api.Item, favorited bool) {
	source := string(item.Source)
	if store.Favorites == nil {
		store.Favorites = state.Favorites{}
	}
	list := store.Favorites[source]
	id := storedIDFor(item)
	index := -1
	for i := range list {
		if list[i].ID == id {
			index = i
			break
		}
	}
	if favorited && index < 0 {
		url := item.URL
		if item.Source == api.SourceAudius {
			url = ""
		}
		store.Favorites[source] = append(list, state.Favorite{ID: id, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: url})
		return
	}
	if !favorited && index >= 0 {
		store.Favorites[source] = append(list[:index], list[index+1:]...)
	}
}

// storedIDFor derives the persisted identity from a public item.
func storedIDFor(item api.Item) string {
	providerID := item.ProviderID
	if providerID == "" {
		providerID = strings.TrimPrefix(item.ID, "am:")
		providerID = strings.TrimPrefix(providerID, "audius:")
		if colon := strings.Index(providerID, ":"); colon >= 0 && !strings.HasPrefix(item.ID, "am:") {
			providerID = providerID[colon+1:]
		}
	}
	switch item.Source {
	case api.SourceRadio:
		url := item.URL
		if url == "" {
			url = strings.TrimPrefix(item.ID, "radio:")
		}
		return api.RadioRef(normalizeStreamURL(url))
	case api.SourceAudius:
		return "audius:" + item.Kind + ":" + providerID
	default:
		return "am:" + providerID
	}
}
