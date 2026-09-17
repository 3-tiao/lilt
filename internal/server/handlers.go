package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
)

func (s *Server) bindHandlers() {
	r := s.registry
	r.Bind("api.describe", func(context.Context, json.RawMessage) (any, *api.Error) {
		return r.Describe(), nil
	})
	r.Bind("sources.list", func(context.Context, json.RawMessage) (any, *api.Error) {
		return s.sourceDescriptors(), nil
	})

	r.Bind("playback.play", s.handlePlay)
	r.Bind("playback.playSongs", s.handlePlaySongs)
	r.Bind("playback.pause", s.handleControl(func(ctx context.Context) (core.PlaybackState, error) {
		return s.engine.PauseState(ctx)
	}))
	r.Bind("playback.resume", s.handleControl(func(ctx context.Context) (core.PlaybackState, error) {
		return s.engine.ResumeState(ctx)
	}))
	r.Bind("playback.next", s.handleControl(func(ctx context.Context) (core.PlaybackState, error) {
		return s.engine.NextState(ctx)
	}))
	r.Bind("playback.previous", s.handleControl(func(ctx context.Context) (core.PlaybackState, error) {
		return s.engine.PreviousState(ctx)
	}))
	r.Bind("playback.stop", s.handleStop)
	r.Bind("playback.toggle", s.handleToggle)
	r.Bind("playback.setShuffle", s.handleSetShuffle)
	r.Bind("playback.setRepeat", s.handleSetRepeat)

	r.Bind("queue.list", s.handleQueueList)
	r.Bind("queue.add", s.handleQueueAdd)
	r.Bind("queue.jump", s.handleQueueJump)
	r.Bind("queue.remove", s.handleQueueRemove)
	r.Bind("queue.move", s.handleQueueMove)
	r.Bind("queue.clear", s.handleQueueClear)

	r.Bind("discovery.search", s.handleDiscoverySearch)
	r.Bind("playlist.tracks", s.handlePlaylistTracks)
	r.Bind("library.playlists", s.handleLibraryPlaylists)
	r.Bind("recent.list", s.handleRecentList)
	r.Bind("recommendations.list", s.handleRecommendations)

	r.Bind("radio.search", s.handleRadioSearch)
	r.Bind("radio.options", s.handleRadioOptions)
	r.Bind("radio.probe", s.handleRadioProbe)
	r.Bind("radio.cache", s.handleRadioCache)

	r.Bind("state.get", s.handleStateGet)
	r.Bind("favorites.list", s.handleFavoritesList)
	r.Bind("favorites.set", s.handleFavoritesSet)
	r.Bind("ui.set", s.handleUISet)

	r.Bind("session.status", s.handleStatus)
	r.Bind("session.shutdown", func(context.Context, json.RawMessage) (any, *api.Error) {
		return map[string]any{}, nil
	})

	r.Bind("authorization.list", s.handleAuthorizationList)
	r.Bind("authorization.status", s.handleAuthorizationStatus)
	r.Bind("authorization.begin", s.handleAuthorizationBegin)
	r.Bind("authorization.flowStatus", s.handleAuthorizationFlowStatus)
	r.Bind("authorization.cancel", s.handleAuthorizationCancel)
	r.Bind("authorization.disconnect", s.handleAuthorizationDisconnect)
}

// mapEngineError converts an engine failure into a stable public error. A
// transport failure additionally schedules a helper rebuild and is reported as
// operation_outcome_unknown because the command may have had side effects.
func (s *Server) mapEngineError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var rpcErr *player.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code != "" {
		return api.Errorf(mapHelperCode(rpcErr.Code), "%s", err.Error()).
			WithDetails(map[string]any{"providerCode": rpcErr.Code})
	}
	if player.IsTransportError(err) {
		s.noteEngineFailure(err)
		return api.Errorf(api.CodeOperationOutcomeUnknown, "%s", err.Error())
	}
	return api.Errorf(api.CodePlaybackError, "%v", err)
}

// mapHelperCode maps the helper's private error codes onto the stable public
// catalog. The original code is preserved in details.providerCode.
func mapHelperCode(code string) string {
	switch code {
	case api.CodeInvalidReference, api.CodeAuthorizationRequired, api.CodeQueueUnavailable,
		api.CodePreviewUnavailable, api.CodePreviewUnsupported, api.CodeSearchFailed:
		return code
	case "music_error", "diagnostics_failed", "playback_error":
		return api.CodePlaybackError
	case "invalid_search":
		return api.CodeSearchFailed
	case "player_unavailable":
		return api.CodeSourceUnavailable
	case "unknown_method":
		return api.CodeUnsupportedCommand
	case "preview_search_unavailable":
		return api.CodePreviewUnavailable
	default:
		return api.CodePlaybackError
	}
}

// commitPlaybackLocked assigns a sequence (and queue revision when composition
// changed), publishes playback.changed, and returns the public projection.
// Callers hold s.mu.
func (s *Server) commitPlaybackLocked(state core.PlaybackState, queueChanged bool) api.PlaybackState {
	s.sequence++
	if queueChanged {
		s.queueRevision++
	}
	projected := s.projectState(state, sourceForState(state), s.sequence, s.queueRevision)
	s.publishLocked("playback.changed", map[string]any{"state": projected})
	return projected
}

// checkQueueRevision enforces an optional optimistic-concurrency precondition.
func (s *Server) checkQueueRevision(expected *uint64) *api.Error {
	if expected == nil || *expected == s.queueRevision {
		return nil
	}
	details := map[string]any{"queueRevision": s.queueRevision}
	if s.engine != nil && !s.engineRestarting {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if state, err := s.engine.State(ctx); err == nil {
			details["queue"] = s.queueState(state)
		}
		cancel()
	}
	return api.Errorf(api.CodeConflict, "queue revision is %d, not %d", s.queueRevision, *expected).WithDetails(details)
}

func (s *Server) queueState(state core.PlaybackState) api.QueueState {
	queue := api.QueueState{Index: state.QueueIndex, QueueRevision: s.queueRevision}
	source := sourceForState(state)
	if len(state.Queue) > 0 {
		queue.Source = &source
		queue.Items = make([]api.Item, 0, len(state.Queue))
		for _, item := range state.Queue {
			queue.Items = append(queue.Items, ProjectItem(item, source))
		}
	} else {
		queue.Index = -1
	}
	return queue
}
