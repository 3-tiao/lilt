package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/activity"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

// openActivity opens the activity store. A failure degrades the server: playback
// and discovery keep working, favorites/history turn read-only-empty, and the
// database file is never replaced automatically.
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
		"the activity store is unavailable; favorites and history are read-only")
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
		return items, nil
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
	if apiErr := s.activityMutation(func() error {
		return s.activity.SetFavorite(activityItemFromAPI(params.Item), params.Favorited, time.Now())
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: params.Favorited, Item: params.Item}, nil
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
	if apiErr := s.activityMutation(func() error {
		return s.activity.SetFavorite(activityItemFromAPI(item), true, time.Now())
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: true, Item: item}, nil
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
	source, stableID, apiErr := identityFromRef(params.Ref)
	if apiErr != nil {
		return nil, apiErr
	}
	if s.activity == nil {
		return nil, s.activityRequired()
	}
	if apiErr := s.activityMutation(func() error {
		return s.activity.RemoveFavorite(source, stableID)
	}); apiErr != nil {
		return nil, apiErr
	}
	return api.FavoriteResult{Favorited: false, Item: api.Item{Source: api.SourceID(source), Ref: params.Ref}}, nil
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
		return api.HistoryPageResult{Entries: []api.HistoryEntry{}}, nil
	}
	var cursor *activity.Cursor
	if params.Before != "" {
		parsed, err := parseHistoryCursor(params.Before)
		if err != nil {
			return nil, api.Errorf(api.CodeInvalidRequest, "invalid before cursor: %v", err)
		}
		cursor = parsed
	}
	page, err := s.activity.HistoryPage(cursor, params.Limit)
	if err != nil {
		return nil, s.activityRequired()
	}
	result := api.HistoryPageResult{Entries: []api.HistoryEntry{}}
	for _, entry := range page.Entries {
		item := activityItemToAPI(entry.Item)
		if params.Source != "" && string(item.Source) != params.Source {
			continue
		}
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
		return stats, nil
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
	s.activity = nil
	result, err := resetActivityDB(path)
	if err != nil {
		s.logf("activity.reset_failed", map[string]any{"path": path, "error": err.Error()})
		return nil, s.activityRequired()
	}
	s.activity = openActivity(path, s.logf)
	s.publishActivityChanged()
	return api.ActivityResetResult{Archived: result.Archived, ArchivePath: result.ArchivePath}, nil
}

// activityArchiveResult reports what reset did with the previous database.
type activityArchiveResult struct {
	Archived    bool
	ArchivePath string
}

// resetActivityDB closes nothing (the caller already detached the handle) and
// moves the database plus its WAL/SHM files to a timestamped archive before a
// fresh database is created. It never deletes the archive.
func resetActivityDB(path string) (activityArchiveResult, error) {
	// Checkpoint and drop any WAL content into the main file before archiving
	// by touching the files only if present: the store is already detached, so
	// leftover -wal/-shm files are archived alongside the database.
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	archived := false
	archivePath := ""
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := path + suffix
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := fmt.Sprintf("%s.archive-%s%s", path, stamp, suffix)
		if err := os.Rename(source, target); err != nil {
			return activityArchiveResult{}, err
		}
		if suffix == "" {
			archived = true
			archivePath = target
		}
	}
	return activityArchiveResult{Archived: archived, ArchivePath: archivePath}, nil
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

// activityItemFromAPI converts a public item into the store's persistent shape.
// Stable identity is canonical and derived from (source, kind, provider id);
// client ID spellings ("apple-music:123" vs "am:123") must not create two rows
// for the same item. Audius never persists its short-lived URL.
func activityItemFromAPI(item api.Item) activity.Item {
	kind := item.Kind
	if kind == "" {
		kind = api.KindSong
	}
	stored := activity.Item{
		Source:     string(item.Source),
		Kind:       kind,
		ProviderID: item.ProviderID,
		Ref:        item.Ref,
		Title:      item.Title,
		Artist:     item.Artist,
		PublicURL:  item.URL,
	}
	switch item.Source {
	case api.SourceAudius:
		stored.PublicURL = ""
		providerID := item.ProviderID
		if providerID == "" {
			parts := strings.SplitN(strings.TrimPrefix(item.ID, "audius:"), ":", 2)
			providerID = item.ID
			if len(parts) == 2 {
				providerID = parts[1]
			}
		}
		stored.StableID = api.AudiusRef(kind, providerID)
		stored.ProviderID = providerID
		if stored.Ref == "" {
			stored.Ref = stored.StableID
		}
	case api.SourceRadio:
		url := item.URL
		if url == "" {
			url = strings.TrimPrefix(item.ID, "radio:")
		}
		stored.StableID = api.RadioRef(normalizeStreamURL(url))
		stored.PublicURL = url
		if stored.Ref == "" {
			stored.Ref = url
		}
	default:
		providerID := item.ProviderID
		if providerID == "" {
			id := strings.TrimPrefix(item.ID, "am:")
			id = strings.TrimPrefix(id, string(api.SourceAppleMusic)+":")
			// "apple-music:song:<id>" leaves "song:<id>"; drop a kind segment.
			if i := strings.Index(id, ":"); i >= 0 && strings.HasPrefix(item.ID, string(api.SourceAppleMusic)+":") {
				id = id[i+1:]
			}
			providerID = id
		}
		stored.StableID = "am:" + providerID
		stored.ProviderID = providerID
		if stored.Ref == "" {
			stored.Ref = api.AppleMusicRef(kind, providerID)
		}
	}
	return stored
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
	reference, parseErr := parseResolveRef(ref)
	if parseErr != nil {
		return api.Item{}, parseErr
	}
	switch reference.source {
	case api.SourceRadio:
		url := reference.url
		if url == "" {
			url = strings.TrimPrefix(ref, "radio:")
		}
		item := ProjectItem(core.Item{Kind: api.KindStream, URL: url, Title: normalizeStreamURL(url)}, api.SourceRadio)
		return item, nil
	case api.SourceAudius:
		provider, ok := s.providers[api.SourceAudius].(audiusProvider)
		if !ok {
			return api.Item{}, api.Errorf(api.CodeUnsupportedCommand, "audius discovery is unavailable")
		}
		return provider.Track(ctx, reference.id)
	default:
		if err := s.requireEngine(); err != nil {
			return api.Item{}, err
		}
		kind := reference.kind
		id := reference.id
		if kind != api.KindSong && kind != api.KindPlaylist {
			return api.Item{}, api.Errorf(api.CodeUnsupportedCommand,
				"resolving %s refs is not supported; favorite them from search or history", kind)
		}
		item, err := s.currentEngine().TrackInfo(ctx, kind, id)
		if err != nil {
			return api.Item{}, s.mapEngineError(err)
		}
		if strings.TrimSpace(item.Title) == "" {
			return api.Item{}, api.Errorf(api.CodeInvalidReference,
				"%s could not be resolved; favorite it from search or history", ref)
		}
		return ProjectItem(item, api.SourceAppleMusic), nil
	}
}

// resolveRef is the parsed shape of a favorite ref input.
type resolveRef struct {
	source api.SourceID
	kind   string
	id     string
	url    string
}

// parseResolveRef accepts canonical refs, Apple Music URLs, raw stream URLs,
// and the radio:<url> state identity.
func parseResolveRef(raw string) (resolveRef, *api.Error) {
	if strings.HasPrefix(raw, string(api.SourceRadio)+":") && strings.Contains(raw[6:], "/") {
		return resolveRef{source: api.SourceRadio, kind: api.KindStream, url: strings.TrimPrefix(raw, string(api.SourceRadio)+":")}, nil
	}
	reference, err := api.ParseReference(raw)
	if err != nil {
		return resolveRef{}, err
	}
	return resolveRef{
		source: reference.Source,
		kind:   reference.Kind,
		id:     reference.ID,
		url:    reference.URL,
	}, nil
}

// identityFromRef derives (source, stable id) from a favorite ref input without
// touching a provider.
func identityFromRef(ref string) (string, string, *api.Error) {
	reference, apiErr := parseResolveRef(ref)
	if apiErr != nil {
		return "", "", apiErr
	}
	switch reference.source {
	case api.SourceRadio:
		url := reference.url
		if url == "" {
			url = strings.TrimPrefix(ref, "radio:")
		}
		return string(api.SourceRadio), api.RadioRef(normalizeStreamURL(url)), nil
	case api.SourceAudius:
		kind := reference.kind
		if kind == "" {
			kind = api.KindSong
		}
		return string(api.SourceAudius), api.AudiusRef(kind, reference.id), nil
	default:
		return string(api.SourceAppleMusic), "am:" + reference.id, nil
	}
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
