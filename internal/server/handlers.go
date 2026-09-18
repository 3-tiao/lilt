package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
	"github.com/caiguo/lilt/internal/state"
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
	r.Bind("playback.pause", s.handleTransportControl("pause"))
	r.Bind("playback.resume", s.handleTransportControl("resume"))
	r.Bind("playback.next", s.handleTransportControl("next"))
	r.Bind("playback.previous", s.handleTransportControl("previous"))
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
	r.Bind("discovery.trending", s.handleDiscoveryTrending)
	r.Bind("playlist.tracks", s.handlePlaylistTracks)
	r.Bind("library.playlists", s.handleLibraryPlaylists)
	r.Bind("library.albums", s.handleLibraryAlbums)
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
	projected := s.projectState(state, s.publicActiveSourceLocked(), s.sequence, s.queueRevision)
	s.publishLocked("playback.changed", map[string]any{"state": projected})
	return projected
}

// publicActiveSourceLocked keeps the historical public default without
// pretending an empty fresh server already owns an Apple Music session.
func (s *Server) publicActiveSourceLocked() api.SourceID {
	if s.activeSource == "" {
		return api.SourceAppleMusic
	}
	return s.activeSource
}

// beginPlaybackStartLocked commits ownership before invoking a transport. A
// failed start therefore cannot resurrect the previous source or session.
func (s *Server) beginPlaybackStartLocked(source api.SourceID, transport TransportID) {
	s.activeSource = source
	s.activeTransport = transport
	s.playbackGeneration++
	s.transportSessionID = newTransportSessionID()
	// A replaced provider can keep reporting for a few seconds (for example
	// MusicKit keeps playing for up to ~3s); suppress those stale shapes.
	s.switchSettleUntil = time.Now().Add(3 * time.Second)
}

// selectHelperLocked enforces process ownership of Now Playing: starting an
// AVPlayer transport terminates lilt-player, while starting MusicKit terminates
// lilt-audio. Stops and shutdowns are intentionally idempotent.
func (s *Server) selectHelperLocked(ctx context.Context, transport TransportID) *api.Error {
	if transport == transportEngine {
		if err := s.ensureMusicEngineLocked(); err != nil {
			return err
		}
		if s.audioEngine != nil {
			audio := s.audioEngine
			s.audioEngine = nil
			_, _ = audio.Stop(ctx)
			_ = audio.UnsubscribeState(context.Background())
			if closer, ok := audio.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		return nil
	}
	if transport == transportURLQueue && s.externalURLDriver {
		if s.engine != nil {
			_, _ = s.engine.Stop(ctx)
		}
		return nil
	}
	if err := s.ensureAudioEngineLocked(); err != nil {
		return err
	}
	if s.engine != nil {
		music := s.engine
		s.setEngine(nil)
		_, _ = music.Stop(ctx)
		_ = music.UnsubscribeState(context.Background())
		if closer, ok := music.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	return nil
}

func newTransportSessionID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", id[:])
}

// resetURLTransportLocked invalidates the URL-queue session, for example when
// the helper is rebuilt.
func (s *Server) resetURLTransportLocked() {
	s.activeTransport = ""
	if s.urlTransport != nil {
		s.urlTransport.Reset()
	}
}

// failPlaybackStartLocked clears the old finite queue in the public state. The
// best-effort Stop also prevents a failed replacement from restoring old audio.
// A provider preparation error keeps its stable code; a resolution/engine
// failure becomes playback_error.
func (s *Server) failPlaybackStartLocked(ctx context.Context, cause error) *api.Error {
	if s.urlTransport != nil {
		_, _ = s.urlTransport.Stop(ctx)
	}
	if s.engine != nil {
		_, _ = s.engine.Stop(ctx)
	}
	if s.audioEngine != nil {
		_, _ = s.audioEngine.Stop(ctx)
	}
	stopped := core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	projected := s.commitPlaybackLocked(stopped, true)
	var apiErr *api.Error
	if errors.As(cause, &apiErr) && (apiErr.Code == api.CodeInvalidReference || apiErr.Code == api.CodeSourceUnavailable) {
		return apiErr.WithDetails(map[string]any{"state": projected})
	}
	return s.mapEngineError(cause).WithDetails(map[string]any{"state": projected})
}

// persistPlaybackSourceLocked is an additional mutation after audio starts.
// Playback remains active if persistence fails.
func (s *Server) persistPlaybackSourceLocked(source api.SourceID) *api.Error {
	return s.mutateState(func(next *state.Store) {
		next.LastPlaybackSource = string(source)
	})
}

// checkQueueRevision enforces an optional optimistic-concurrency precondition.
func (s *Server) checkQueueRevision(expected *uint64) *api.Error {
	if expected == nil || *expected == s.queueRevision {
		return nil
	}
	details := map[string]any{"queueRevision": s.queueRevision}
	if s.usingURLTransportLocked() && s.urlTransport != nil {
		queue := s.urlTransport.List()
		queue.QueueRevision = s.queueRevision
		details["queue"] = queue
	} else if s.engine != nil && !s.engineRestarting {
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
	source := s.publicActiveSourceLocked()
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

// urlPlaybackAvailable reports whether a URL/AVPlayer transport can be built.
// The transport itself is created lazily when the audio helper starts, so this
// checks the factory rather than the (still nil) transport.
func (s *Server) urlPlaybackAvailable() bool {
	// The transport is built at Start for an injected driver/engine, or lazily
	// from the audio-helper factory. An engine set for deterministic tests is an
	// AudioEngine but not a URL driver, so it does not qualify on its own.
	return s.urlTransport != nil || s.audioEngineFactory != nil
}
