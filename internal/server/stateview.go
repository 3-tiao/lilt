package server

import (
	"strings"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

// appState projects the persisted store into the public AppState model.
func (s *Server) appState() api.AppState {
	app := api.AppState{}
	if s.store == nil {
		app.LastSource = api.SourceAppleMusic
		return app
	}
	app.Revision = s.stateRevision
	app.Theme = s.store.Theme
	app.LastSource = api.SourceID(s.store.LastSource)
	if app.LastSource == "" {
		app.LastSource = api.SourceAppleMusic
	}
	for source, favorites := range s.store.Favorites {
		for _, favorite := range favorites {
			app.Favorites = append(app.Favorites, storedItem(sourceFromStored(source), favorite.ID, favorite.Kind, favorite.Title, favorite.Artist, favorite.URL))
		}
	}
	for _, recent := range s.store.Recent {
		source := sourceFromStored(recent.Source)
		item := storedItem(source, recent.ID, recent.Kind, recent.Title, recent.Artist, recent.URL)
		app.Recent = append(app.Recent, api.RecentEntry{Item: item, PlayedAt: recent.PlayedAt.UTC().Format("2006-01-02T15:04:05Z07:00")})
	}
	for _, container := range s.store.RecentContainers {
		source := containerSource(container)
		item := storedItem(source, container.ID, container.Kind, container.Title, "", "")
		app.RecentContainers = append(app.RecentContainers, api.RecentEntry{Item: item, PlayedAt: container.PlayedAt.UTC().Format("2006-01-02T15:04:05Z07:00")})
	}
	return app
}

func sourceFromStored(source string) api.SourceID {
	switch source {
	case string(api.SourceRadio):
		return api.SourceRadio
	case string(api.SourceAudius):
		return api.SourceAudius
	default:
		return api.SourceAppleMusic
	}
}

func containerSource(container state.RecentContainer) api.SourceID {
	return sourceFromStored(container.Source)
}

// storedItem rebuilds a public Item from persisted fields, deriving the
// provider id, stable id, and ref without an online lookup.
func storedItem(source api.SourceID, storedID, kind, title, artist, url string) api.Item {
	providerID := storedID
	switch source {
	case api.SourceRadio:
		streamURL := url
		if streamURL == "" {
			streamURL = strings.TrimPrefix(storedID, "radio:")
		}
		return ProjectItem(core.Item{Kind: api.KindStream, URL: streamURL, Title: title, Artist: artist}, api.SourceRadio)
	case api.SourceAudius:
		providerID = strings.TrimPrefix(storedID, "audius:")
		providerID = strings.TrimPrefix(providerID, kind+":")
	default:
		providerID = strings.TrimPrefix(storedID, "am:")
	}
	return ProjectItem(core.Item{Kind: orKind(kind, api.KindSong), ID: providerID, Title: title, Artist: artist, URL: url}, source)
}

func orKind(kind, fallback string) string {
	if kind == "" {
		return fallback
	}
	return kind
}

// mutateState persists a mutation atomically and bumps the public revision. It
// never mutates authoritative memory when the save fails. Callers hold s.mu.
func (s *Server) mutateState(mutate func(*state.Store)) *api.Error {
	if s.store == nil {
		return api.Errorf(api.CodeStateSaveFailed, "state store is unavailable")
	}
	if err := s.store.UpdateAndSave(mutate); err != nil {
		return api.Errorf(api.CodeStateSaveFailed, "state was not saved: %v", err)
	}
	s.stateRevision++
	return nil
}

// saveState persists a mutation and publishes a state.changed event. Callers
// hold s.mu.
func (s *Server) saveState(mutate func(*state.Store)) *api.Error {
	if apiErr := s.mutateState(mutate); apiErr != nil {
		return apiErr
	}
	s.sequence++
	s.publishLocked("state.changed", map[string]any{"state": s.appState()})
	return nil
}
