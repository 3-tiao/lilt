package server

import (
	"context"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/icy"
)

// requireEngine reports the engine status for a command. It runs under s.mu.
func (s *Server) requireEngine() *api.Error {
	if s.engine != nil && !s.engineRestarting {
		return nil
	}
	if s.engineRestarting {
		return api.Errorf(api.CodeEngineRestarting, "the playback engine is restarting; retry shortly")
	}
	return api.Errorf(api.CodeSourceUnavailable, "no playback engine is attached")
}

// watchEngine subscribes to one engine instance and republishes notifications.
// When the update stream closes (helper death), it schedules a rebuild.
func (s *Server) watchEngine(engine Engine) {
	subscription, err := engine.SubscribeState(context.Background())
	if err != nil {
		return
	}
	go func() {
		for update := range subscription.Updates {
			// Ignore updates from a superseded engine and publish under s.mu so
			// sequence allocation and enqueue stay ordered with commands.
			s.mu.Lock()
			if s.engine != engine {
				s.mu.Unlock()
				continue
			}
			s.sequence++
			sequence := s.sequence
			state := s.projectState(update.State, sourceForState(update.State), sequence, s.queueRevision)
			s.publishLocked("playback.changed", map[string]any{"state": state})
			s.mu.Unlock()
		}
		s.onEngineStreamClosed(engine)
	}()
}

// onEngineStreamClosed starts a rebuild when the current engine's stream ends.
func (s *Server) onEngineStreamClosed(engine Engine) {
	s.mu.Lock()
	if s.engine == engine && s.canRestart && !s.engineRestarting && !s.engineStopped {
		s.engineRestarting = true
		s.mu.Unlock()
		go s.rebuildEngine()
		return
	}
	s.mu.Unlock()
}

// noteEngineFailure marks the engine as restarting and schedules a rebuild.
// Callers hold s.mu.
func (s *Server) noteEngineFailure(err error) {
	if !s.canRestart || s.engineRestarting || s.engineStopped {
		return
	}
	s.engineRestarting = true
	s.logf("engine.failed", map[string]any{"error": err.Error()})
	go s.rebuildEngine()
}

// rebuildEngine closes the failed engine and builds a fresh one with bounded
// exponential backoff. It never replays the command that timed out. Callers
// must already have set engineRestarting.
func (s *Server) rebuildEngine() {
	s.stopICY()
	backoff := 250 * time.Millisecond
	const maxBackoff = 30 * time.Second
	for {
		s.mu.Lock()
		if s.engineStopped {
			s.engineRestarting = false
			s.mu.Unlock()
			return
		}
		old := s.engine
		s.setEngine(nil)
		s.sequence++
		s.publishLocked("server.warning", map[string]any{
			"code":    "engine_restarting",
			"message": "the playback helper is unavailable; rebuilding it",
		})
		s.mu.Unlock()

		if old != nil {
			_ = old.UnsubscribeState(context.Background())
			if closer, ok := old.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}

		engine, err := s.engineFactory()
		if err == nil {
			s.mu.Lock()
			s.setEngine(engine)
			s.engineRestarting = false
			s.sequence++
			s.publishLocked("engine.restarted", map[string]any{"source": string(api.SourceAppleMusic)})
			s.sequence++
			s.publishLocked("sources.changed", map[string]any{"sources": s.sourceDescriptors()})
			// A rebuilt helper has no playback: publish the stopped reset.
			stopped := core.PlaybackState{Status: "stopped", Mode: "none"}
			s.sequence++
			s.publishLocked("playback.changed", map[string]any{
				"state": s.projectState(stopped, api.SourceAppleMusic, s.sequence, s.queueRevision),
			})
			s.mu.Unlock()
			s.watchEngine(engine)
			s.logf("engine.restarted", map[string]any{})
			return
		}
		s.logf("engine.rebuild_failed", map[string]any{"error": err.Error()})
		select {
		case <-s.engineStop:
			s.mu.Lock()
			s.engineRestarting = false
			s.mu.Unlock()
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
}

// --- ICY stream metadata ----------------------------------------------------

func (s *Server) projectStatus(state core.PlaybackState, source api.SourceID, sequence uint64) api.PlaybackStatus {
	status := ProjectStatus(state, source, sequence)
	s.overlayICY(&status)
	return status
}

func (s *Server) projectState(state core.PlaybackState, source api.SourceID, sequence, queueRevision uint64) api.PlaybackState {
	full := ProjectState(state, source, sequence, queueRevision)
	s.overlayICY(&full.PlaybackStatus)
	return full
}

// overlayICY attaches the current stream metadata to live radio states.
func (s *Server) overlayICY(status *api.PlaybackStatus) {
	if !status.IsLive && status.Mode != "stream" {
		return
	}
	s.icyMu.Lock()
	title, artist := s.icyTitle, s.icyArtist
	s.icyMu.Unlock()
	if title != "" {
		value := title
		status.StreamTitle = &value
	}
	if artist != "" {
		value := artist
		status.StreamArtist = &value
	}
}

// startICY watches a live stream for inline metadata until the stream is
// replaced. It replaces any previous watcher.
func (s *Server) startICY(streamURL string) {
	s.stopICY()
	if streamURL == "" || s.icy == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.icyMu.Lock()
	s.icyCancel = cancel
	s.icyTitle, s.icyArtist = "", ""
	s.icyGeneration++
	generation := s.icyGeneration
	s.icyMu.Unlock()
	go func() {
		_ = s.icy.Watch(ctx, streamURL, func(update icy.Update) {
			s.applyICY(generation, update)
		})
	}()
}

// stopICY cancels the metadata watcher and clears the announced title.
func (s *Server) stopICY() {
	s.icyMu.Lock()
	cancel := s.icyCancel
	s.icyCancel = nil
	s.icyTitle, s.icyArtist = "", ""
	s.icyGeneration++
	s.icyMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// applyICY records a new announced title and republishes playback state. A
// callback from a replaced watcher is ignored via its generation.
func (s *Server) applyICY(generation uint64, update icy.Update) {
	s.icyMu.Lock()
	if generation != s.icyGeneration {
		s.icyMu.Unlock()
		return
	}
	s.icyTitle, s.icyArtist = update.Title, update.Artist
	s.icyMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.engine == nil || s.engineRestarting {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state, err := s.engine.State(ctx)
	if err != nil {
		return
	}
	s.sequence++
	s.publishLocked("playback.changed", map[string]any{
		"state": s.projectState(state, sourceForState(state), s.sequence, s.queueRevision),
	})
}
