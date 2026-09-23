package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/activity"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

// openActivity opens the activity store. A failure degrades the server:
// playback and discovery keep working, Activity reads and mutations return
// storage_unavailable, and the database file is never replaced automatically.
func openActivity(path string, logf func(kind string, fields map[string]any)) *activity.DB {
	db, err := activity.Open(path)
	if err != nil {
		logf("activity.degraded", map[string]any{"path": path, "error": err.Error()})
		return nil
	}
	return db
}

// activityRequired is the stable error for mutations the degraded store cannot
// serve.
func (s *Server) activityRequired() *api.Error {
	return api.Errorf(api.CodeStorageUnavailable,
		"the activity store is unavailable; favorites and history cannot be read or changed")
}

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
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	favorites, err := s.activity.ListFavorites()
	if err != nil {
		return nil, s.activityRequired()
	}
	for _, favorite := range favorites {
		item := activityItemToAPI(favorite)
		if params.Source != "" && string(item.Source) != params.Source {
			continue
		}
		items = append(items, item)
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
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	// Persist and echo the canonical stored item: the response must never
	// repeat caller-supplied short-lived media URLs or non-canonical ids.
	stored, identityErr := activityItemFromAPI(params.Item)
	if identityErr != nil {
		return nil, identityErr
	}
	if apiErr := s.activityMutation(func() error {
		return s.activity.SetFavorite(stored, params.Favorited, time.Now())
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: params.Favorited, Item: activityItemToAPI(stored)}, nil
}

// handleFavoritesAdd resolves a ref to a complete item, then favorites it.
// Idempotent: adding an existing favorite keeps its addedAt.
func (s *Server) handleFavoritesAdd(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	item, apiErr := s.resolveItem(ctx, params.Ref)
	if apiErr != nil {
		return nil, apiErr
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	stored, identityErr := activityItemFromAPI(item)
	if identityErr != nil {
		return nil, identityErr
	}
	if apiErr := s.activityMutation(func() error {
		return s.activity.SetFavorite(stored, true, time.Now())
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: true, Item: activityItemToAPI(stored)}, nil
}

// handleFavoritesRemove unfavorites a ref by identity. Idempotent: removing an
// unknown favorite succeeds without touching the provider.
func (s *Server) handleFavoritesRemove(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	identity, apiErr := api.ParseIdentity(params.Ref)
	if apiErr != nil {
		return nil, apiErr
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	if apiErr := s.activityMutation(func() error {
		return s.activity.RemoveFavorite(string(identity.Source), identity.StableID)
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: false, Item: api.Item{Source: identity.Source, Ref: identity.Ref}}, nil
}

func (s *Server) handleHistoryList(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
		Before string `json:"before"`
		Limit  int    `json:"limit"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	var cursor *activity.Cursor
	if params.Before != "" {
		parsed, err := parseHistoryCursor(params.Before)
		if err != nil {
			return nil, api.Errorf(api.CodeInvalidRequest, "invalid before cursor: %v", err)
		}
		cursor = parsed
	}
	page, err := s.activity.HistoryPage(activity.HistoryQuery{
		Source: params.Source,
		Before: cursor,
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, s.activityRequired()
	}
	result := api.HistoryPageResult{Entries: []api.HistoryEntry{}}
	for _, entry := range page.Entries {
		item := activityItemToAPI(entry.Item)
		result.Entries = append(result.Entries, api.HistoryEntry{Item: item, PlayedAt: rfc3339(entry.PlayedAt)})
	}
	if page.NextCursor != nil {
		result.NextCursor = formatHistoryCursor(*page.NextCursor)
	}
	return result, nil
}

func (s *Server) handleHistoryStats(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Refs []string `json:"refs"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if len(params.Refs) == 0 {
		return nil, api.Errorf(api.CodeInvalidRequest, "history.stats needs refs")
	}
	if len(params.Refs) > 500 {
		return nil, api.Errorf(api.CodeInvalidRequest, "history.stats accepts at most 500 refs per request")
	}
	stats := make([]api.HistoryStats, len(params.Refs))
	for i, ref := range params.Refs {
		stats[i].Ref = ref
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	resolved, err := s.activity.StatsForRefs(params.Refs)
	if err != nil {
		return nil, s.activityRequired()
	}
	for i, stat := range resolved {
		stats[i].PlayCount = stat.PlayCount
		if !stat.FirstPlayed.IsZero() {
			first := rfc3339(stat.FirstPlayed)
			stats[i].FirstPlayedAt = &first
		}
		if !stat.LastPlayed.IsZero() {
			last := rfc3339(stat.LastPlayed)
			stats[i].LastPlayedAt = &last
		}
	}
	return stats, nil
}

func (s *Server) handleHistoryClear(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Confirm bool `json:"confirm"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if !params.Confirm {
		return nil, api.Errorf(api.CodeInvalidRequest, "history.clear needs confirm:true")
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	cleared, err := s.activity.ClearHistory()
	if err != nil {
		return nil, s.activityRequired()
	}
	if s.recent != nil {
		s.recent.forget()
	}
	s.publishActivityChanged()
	return api.HistoryClearResult{Cleared: cleared}, nil
}

// handleActivityReset archives an unhealthy (or user-condemned) activity
// database with its WAL/SHM files and creates an empty one. Never automatic.
func (s *Server) handleActivityReset(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Confirm bool `json:"confirm"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if !params.Confirm {
		return nil, api.Errorf(api.CodeInvalidRequest, "activity.reset needs confirm:true")
	}
	path := s.activityPath
	if path == "" {
		return nil, s.activityRequired()
	}
	// Detach first: Reset closes the live handle, and the server must not use
	// the old handle while the files are being renamed. A failed reset leaves
	// the server degraded (files restored, service requires a restart).
	previous := s.activity
	s.activity = nil
	fresh, result, err := activity.Reset(previous, path, time.Now())
	if err != nil {
		s.logf("activity.reset_failed", map[string]any{"path": path, "error": err.Error()})
		return nil, s.activityRequired()
	}
	s.activity = fresh
	if s.recent != nil {
		s.recent.forget()
	}
	s.publishActivityChanged()
	return api.ActivityResetResult{Archived: result.Archived, ArchivePath: result.ArchivePath}, nil
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

// activityMutation wraps a store mutation and publishes state.changed after a
// successful write. Callers hold s.mu via the command dispatch.
func (s *Server) activityMutation(mutate func() error) *api.Error {
	if err := mutate(); err != nil {
		s.logf("activity.mutation_failed", map[string]any{"error": err.Error()})
		return s.activityRequired()
	}
	s.publishActivityChanged()
	return nil
}

// publishActivityChanged publishes the AppState projection after an activity
// mutation. Callers hold s.mu.
func (s *Server) publishActivityChanged() {
	s.sequence++
	s.publishLocked("state.changed", map[string]any{"state": s.appState()})
}

// activityItemFromAPI converts a public item into the store's persistent shape
// through the shared identity: client spellings ("apple-music:1" vs "am:1"),
// missing kinds, and non-canonical radio URLs all collapse onto one row. An
// item without a usable identity is rejected instead of written with an empty
// key. Audius never persists its short-lived media URL; radio persists its
// stable normalized stream URL.
func activityItemFromAPI(item api.Item) (activity.Item, *api.Error) {
	identity := api.IdentityFromComponents(item.Source, item.Kind, item.ProviderID, item.ID, item.Ref, item.URL)
	if identity.StableID == "" || identity.Ref == "" {
		return activity.Item{}, api.Errorf(api.CodeInvalidReference,
			"item %q (source %q) has no stable identity", item.Ref, item.Source)
	}
	stored := activity.Item{
		Source:     string(identity.Source),
		Kind:       identity.Kind,
		StableID:   identity.StableID,
		ProviderID: identity.ProviderID,
		Ref:        identity.Ref,
		Title:      item.Title,
		Artist:     item.Artist,
	}
	if identity.Source == api.SourceRadio {
		stored.PublicURL = identity.StreamURL
	}
	return stored, nil
}

// activityItemToAPI rebuilds the public item from persisted fields without an
// online lookup.
func activityItemToAPI(stored activity.Item) api.Item {
	return api.Item{
		Source:     api.SourceID(stored.Source),
		Kind:       stored.Kind,
		ID:         stored.StableID,
		ProviderID: stored.ProviderID,
		Ref:        stored.Ref,
		URL:        stored.PublicURL,
		Title:      stored.Title,
		Artist:     stored.Artist,
		Radio:      nil,
	}
}

// resolveItem resolves a ref to a complete item for favorites.add. The activity
// store wins (it already carries display metadata); otherwise the source's
// provider resolves it. A ref that yields no title is rejected: favorites never
// store bare identities.
func (s *Server) resolveItem(ctx context.Context, ref string) (api.Item, *api.Error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return api.Item{}, api.Errorf(api.CodeInvalidReference, "ref is required")
	}
	if s.activity != nil {
		if stored, ok, err := s.activity.FindItemByRef(ref); err != nil {
			return api.Item{}, s.activityRequired()
		} else if ok {
			item := activityItemToAPI(stored)
			if strings.TrimSpace(item.Title) != "" {
				return item, nil
			}
		}
	}
	identity, apiErr := api.ParseIdentity(ref)
	if apiErr != nil {
		return api.Item{}, apiErr
	}
	if identity.Source == api.SourceRadio {
		item := ProjectItem(core.Item{Kind: api.KindStream, URL: identity.StreamURL, Title: identity.StreamURL}, api.SourceRadio)
		return item, nil
	}
	if provider, ok := s.providers[identity.Source]; ok {
		if resolver, ok := provider.(ItemResolver); ok {
			return resolver.ResolveItem(ctx, identity.Kind, identity.ProviderID)
		}
	}
	if identity.Source != api.SourceAppleMusic {
		return api.Item{}, api.Errorf(api.CodeUnsupportedCommand,
			"resolving %s refs is not supported for %s; favorite them from search or history", identity.Kind, identity.Source)
	}
	if identity.Kind != api.KindSong && identity.Kind != api.KindPlaylist {
		return api.Item{}, api.Errorf(api.CodeUnsupportedCommand,
			"resolving %s refs is not supported; favorite them from search or history", identity.Kind)
	}
	resource, resourceErr := s.appleResourceClient(ctx)
	if resourceErr != nil {
		return api.Item{}, resourceErr
	}
	item, err := resource.TrackInfo(ctx, identity.Kind, identity.ProviderID)
	if err != nil {
		return api.Item{}, s.mapAppleResourceError(resource, err)
	}
	if strings.TrimSpace(item.Title) == "" {
		return api.Item{}, api.Errorf(api.CodeInvalidReference,
			"%s could not be resolved; favorite it from search or history", ref)
	}
	return ProjectItem(item, api.SourceAppleMusic), nil
}

// parseHistoryCursor decodes the opaque "<playedAtMS>-<id>" keyset cursor.
func parseHistoryCursor(raw string) (*activity.Cursor, error) {
	var ms, id int64
	if _, err := fmt.Sscanf(raw, "%d-%d", &ms, &id); err != nil {
		return nil, fmt.Errorf("cursor %q is not valid", raw)
	}
	return &activity.Cursor{PlayedAtMS: ms, ID: id}, nil
}

func formatHistoryCursor(cursor activity.Cursor) string {
	return fmt.Sprintf("%d-%d", cursor.PlayedAtMS, cursor.ID)
}

func rfc3339(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z07:00")
}
