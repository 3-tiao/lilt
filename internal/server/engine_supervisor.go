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
	if s.engineFactory != nil {
		return s.ensureMusicEngineLocked()
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
			s.applyEngineUpdate(update, engine, nil)
		}
		s.onEngineStreamClosed(engine)
	}()
}

func (s *Server) watchAudioEngine(engine AudioEngine) {
	subscription, err := engine.SubscribeState(context.Background())
	if err != nil {
		return
	}
	go func() {
		for update := range subscription.Updates {
			s.applyEngineUpdate(update, nil, engine)
		}
		s.onAudioEngineStreamClosed(engine)
	}()
}

func (s *Server) applyEngineUpdate(update core.PlaybackStateUpdate, music Engine, audio AudioEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (music != nil && s.engine != music) || (audio != nil && s.audioEngine != audio) {
		return
	}
	urlActive := s.usingURLTransportLocked()
	if update.State.PlaybackGeneration != 0 && update.State.PlaybackGeneration != s.playbackGeneration {
		return
	}
	if update.State.TransportSessionID != "" && update.State.TransportSessionID != s.transportSessionID {
		return
	}
	if urlActive && update.State.Error != "" {
		next, retryErr := s.urlTransport.RetryCurrent(context.Background())
		if retryErr != nil {
			s.commitPlaybackLocked(core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, true)
			s.sequence++
			s.publishLocked("server.warning", map[string]any{"code": api.CodeSourceUnavailable, "message": retryErr.Error()})
			return
		}
		s.commitPlaybackLocked(next, false)
		return
	}
	if !urlActive && time.Now().Before(s.switchSettleUntil) && sourceFromState(update.State) != s.activeSource {
		return
	}
	if update.State.Ended && urlActive {
		next, advanceErr := s.urlTransport.AdvanceEnded(context.Background())
		if advanceErr != nil {
			_, _ = s.urlTransport.Stop(context.Background())
			s.commitPlaybackLocked(core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, true)
			s.sequence++
			s.publishLocked("server.warning", map[string]any{"code": api.CodeSourceUnavailable, "message": advanceErr.Error()})
			return
		}
		s.commitPlaybackLocked(next, next.Status == "stopped")
		return
	}
	projected := update.State
	if urlActive {
		projected = s.urlTransport.Snapshot(projected)
	}
	s.sequence++
	s.publishLocked("playback.changed", map[string]any{"state": s.projectState(projected, s.publicActiveSourceLocked(), s.sequence, s.queueRevision)})
}

func (s *Server) onAudioEngineStreamClosed(engine AudioEngine) {
	s.mu.Lock()
	if s.audioEngine == engine && s.audioCanRestart && !s.audioEngineRestarting && !s.engineStopped {
		s.audioEngineRestarting = true
		s.playbackGeneration++
		s.transportSessionID = ""
		s.resetURLTransportLocked()
		s.mu.Unlock()
		go s.rebuildAudioEngine()
		return
	}
	s.mu.Unlock()
}

func (s *Server) rebuildAudioEngine() {
	backoff := 250 * time.Millisecond
	for {
		s.mu.Lock()
		if s.engineStopped {
			s.audioEngineRestarting = false
			s.mu.Unlock()
			return
		}
		old := s.audioEngine
		s.audioEngine = nil
		s.sequence++
		s.publishLocked("server.warning", map[string]any{"code": "engine_restarting", "message": "the audio helper is unavailable; rebuilding it"})
		s.mu.Unlock()
		if old != nil {
			_ = old.UnsubscribeState(context.Background())
			if closer, ok := old.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		engine, err := s.audioEngineFactory()
		if err == nil {
			s.mu.Lock()
			s.audioEngine = engine
			if driver, ok := engine.(URLPlaybackDriver); ok {
				if s.urlTransport == nil {
					s.urlTransport = NewURLQueueTransport(driver)
				} else {
					s.urlTransport.SetDriver(driver)
				}
			}
			s.audioEngineRestarting = false
			s.sequence++
			s.publishLocked("engine.restarted", map[string]any{"source": string(s.publicActiveSourceLocked())})
			s.commitPlaybackLocked(core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, true)
			s.mu.Unlock()
			s.watchAudioEngine(engine)
			return
		}
		select {
		case <-s.engineStop:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// onEngineStreamClosed starts a rebuild when the current engine's stream ends.
func (s *Server) onEngineStreamClosed(engine Engine) {
	s.mu.Lock()
	if s.engine == engine && s.canRestart && !s.engineRestarting && !s.engineStopped {
		s.engineRestarting = true
		s.playbackGeneration++
		s.transportSessionID = ""
		s.resetURLTransportLocked()
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
	s.playbackGeneration++
	s.transportSessionID = ""
	s.resetURLTransportLocked()
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
			if s.urlTransport != nil {
				if driver, ok := engine.(URLPlaybackDriver); ok {
					s.urlTransport.SetDriver(driver)
				}
			}
			s.engineRestarting = false
			s.sequence++
			s.publishLocked("engine.restarted", map[string]any{"source": string(s.publicActiveSourceLocked())})
			s.sequence++
			s.publishLocked("sources.changed", map[string]any{"sources": s.sourceDescriptors()})
			// A rebuilt helper has no playback: publish the stopped reset.
			stopped := core.PlaybackState{Status: "stopped", Mode: "none"}
			s.sequence++
			s.publishLocked("playback.changed", map[string]any{
				"state": s.projectState(stopped, s.publicActiveSourceLocked(), s.sequence, s.queueRevision),
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var state core.PlaybackState
	var err error
	if s.activeTransport == transportStream && s.audioEngine != nil && !s.audioEngineRestarting {
		state, err = s.audioEngine.State(ctx)
	} else if s.engine != nil && !s.engineRestarting {
		state, err = s.engine.State(ctx)
	} else {
		return
	}
	if err != nil {
		return
	}
	s.sequence++
	s.publishLocked("playback.changed", map[string]any{
		"state": s.projectState(state, s.publicActiveSourceLocked(), s.sequence, s.queueRevision),
	})
}
