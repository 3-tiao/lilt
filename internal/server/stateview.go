package server

import (
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/state"
)

// appState projects the activity store and the preference store into the public
// AppState model.
func (s *Server) appState() api.AppState {
	app := api.AppState{Revision: s.stateRevision}
	if s.store == nil {
		app.LastSource = api.SourceAppleMusic
		return app
	}
	app.Theme = s.store.Theme
	app.LastSource = api.SourceID(s.store.LastSource)
	if app.LastSource == "" {
		app.LastSource = api.SourceAppleMusic
	}
	if s.activity == nil {
		app.Favorites = []api.Item{}
		app.Recent = []api.RecentEntry{}
		return app
	}
	favorites, err := s.activity.ListFavorites()
	if err != nil {
		// The projection degrades to empty rows; the degraded store already
		// published a server.warning, so mutations surface the stable error.
		s.logf("activity.read_failed", map[string]any{"error": err.Error()})
	} else {
		app.Favorites = make([]api.Item, 0, len(favorites))
		for _, favorite := range favorites {
			app.Favorites = append(app.Favorites, activityItemToAPI(favorite))
		}
	}
	recent, err := s.activity.RecentEntries(recentProjectionLimit)
	if err != nil {
		s.logf("activity.read_failed", map[string]any{"error": err.Error()})
	} else {
		app.Recent = make([]api.RecentEntry, 0, len(recent))
		for _, entry := range recent {
			app.Recent = append(app.Recent, api.RecentEntry{
				Item:     activityItemToAPI(entry.Item),
				PlayedAt: rfc3339(entry.PlayedAt),
			})
		}
	}
	return app
}

// recentProjectionLimit bounds how much derived recent history AppState and
// watch events carry.
const recentProjectionLimit = 100

// persistAppStateMutation is the shared commit boundary for both durable
// stores. The store operation owns atomic persistence; only a confirmed commit
// advances the server-local aggregate revision. Callers hold s.mu.
func (s *Server) persistAppStateMutation(mutate func() error) error {
	if err := mutate(); err != nil {
		return err
	}
	s.stateRevision++
	return nil
}

// mutateState persists a preference mutation atomically and bumps the public
// revision. It never mutates authoritative memory when the save fails. Callers
// hold s.mu.
func (s *Server) mutateState(mutate func(*state.Store)) *api.Error {
	if s.store == nil {
		return api.Errorf(api.CodeStateSaveFailed, "state store is unavailable")
	}
	if err := s.persistAppStateMutation(func() error { return s.store.UpdateAndSave(mutate) }); err != nil {
		return api.Errorf(api.CodeStateSaveFailed, "state was not saved: %v", err)
	}
	return nil
}

// saveState persists a preference mutation and publishes a state.changed event.
// Callers hold s.mu.
func (s *Server) saveState(mutate func(*state.Store)) *api.Error {
	if apiErr := s.mutateState(mutate); apiErr != nil {
		return apiErr
	}
	s.publishAppStateChanged()
	return nil
}
