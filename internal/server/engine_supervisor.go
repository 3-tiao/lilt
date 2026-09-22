package server

import (
	"context"
	"errors"
	"fmt"
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
	// The authorization handshake may settle inside an update that a session
	// or generation filter would otherwise drop; availability must not.
	if music != nil {
		s.publishAppleAvailabilityLocked(update.State.Authorization, update.State.AccountStatus)
	}
	urlActive := s.usingURLTransportLocked()
	if update.State.PlaybackGeneration != 0 && update.State.PlaybackGeneration != s.playbackGeneration {
		return
	}
	if update.State.TransportSessionID != "" && update.State.TransportSessionID != s.transportSessionID {
		return
	}
	if urlActive && update.State.Error != "" {
		s.logURLStallLocked("media failed")
		s.retryURLSessionLocked()
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
			s.logf("server.warning", map[string]any{"code": api.CodeSourceUnavailable, "message": advanceErr.Error()})
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

// publishAppleAvailabilityLocked republishes sources.changed when the MusicKit
// helper reports a new authorization snapshot on its state stream. The helper
// settles its handshake after launch in two steps: MusicAuthorization flips to
// "authorized" first, and the async subscription read fills accountStatus /
// canPlayCatalogContent a beat later — both flip Apple Music capabilities
// (full playback, queue, shuffle, repeat). Gating on the status string alone
// missed the second step, so watch clients kept the degraded snapshot (OQ31).
// Callers hold s.mu.
func (s *Server) publishAppleAvailabilityLocked(authorization string, accountStatus string) {
	signature := fmt.Sprintf("%s|%s", authorization, accountStatus)
	if signature == s.appleAuthSignature {
		return
	}
	s.appleAuthSignature = signature
	s.sequence++
	s.publishLocked("sources.changed", map[string]any{"sources": s.sourceDescriptors()})
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
		s.logf("server.warning", map[string]any{"code": "engine_restarting", "message": "the playback helper is unavailable; rebuilding it"})
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

// defaultURLStallBudget is how long a URL session may make no progress before the
// server treats it as a media failure. A healthy Audius start spends a few
// seconds resolving and buffering, so the budget leaves headroom while still
// turning a permanent stall into an error instead of endless buffering.
const defaultURLStallBudget = 20 * time.Second

// runURLStallWatchdog converts a silent stall into the retry path the helper's
// own error would take. URL playback used to depend entirely on the helper
// reporting a failure, so a dead URL presented as permanent buffering with
// playbackError null, no retry and no queue advance. Progress is position-based:
// buffering or playing with a frozen position
// for the whole budget is a stall, while paused and stopped states reset it.
func (s *Server) runURLStallWatchdog() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var stalledSince time.Time
	var lastPosition float64
	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		budget := s.urlStallBudget
		if budget <= 0 {
			budget = defaultURLStallBudget
		}
		if s.urlTransport == nil || !s.usingURLTransportLocked() {
			s.mu.Unlock()
			stalledSince, lastPosition = time.Time{}, 0
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		state, err := s.urlTransport.State(ctx)
		cancel()
		if err != nil {
			s.mu.Unlock()
			continue
		}
		// A session the client did not pause is never reported as "paused": the
		// helper reports a stream that stopped progressing as "buffering" precisely
		// so this watchdog can act on it (a Jamendo track whose CDN never delivered
		// audio used to freeze at position 0 forever, because AVPlayer reports a dead
		// stream as paused and the budget was reset as if the user had rested).
		//
		// The other half of that contract: "paused" means a pause someone asked for,
		// including one made with a system media key, which the server never sees as
		// a command. Restarting that would override the user.
		switch {
		case state.Status != "buffering" && state.Status != "playing":
			stalledSince, lastPosition = time.Time{}, state.Position
		case state.Position > lastPosition+0.05:
			lastPosition, stalledSince = state.Position, time.Time{}
		case stalledSince.IsZero():
			stalledSince = time.Now()
		case time.Since(stalledSince) >= budget:
			stalledSince = time.Time{}
			s.logURLStallLocked(fmt.Sprintf("media stream stalled for %s", budget))
			s.retryURLSessionLocked()
		}
		s.mu.Unlock()
	}
}

// logURLStallLocked journals why the URL session is about to be retried. The
// stall notice is diagnostic evidence for `lilt log` only: a retry that
// recovers needs no user-facing warning, and publishing here would announce a
// problem that just fixed itself. Callers hold s.mu.
func (s *Server) logURLStallLocked(cause string) {
	message := cause + "; re-resolving the current item once"
	if title := s.urlCurrentTitleLocked(); title != "" {
		message = fmt.Sprintf("%s on %q; re-resolving the current item once", cause, title)
	}
	s.logf("server.warning", map[string]any{"code": api.CodePlaybackStalled, "message": message})
}

// urlCurrentTitleLocked names the playing queue item for diagnostics. Callers
// hold s.mu; it returns an empty string outside a live session.
func (s *Server) urlCurrentTitleLocked() string {
	if s.urlTransport == nil {
		return ""
	}
	queue := s.urlTransport.List()
	if queue.Index < 0 || queue.Index >= len(queue.Items) {
		return ""
	}
	return queue.Items[queue.Index].Title
}

// retryURLSessionLocked re-resolves and replays the current URL item once. A
// dead item is skipped (the transport bounds consecutive skips) and playback
// continues; only a real session end warns with the terminal codes. Callers
// hold s.mu; it is shared by the helper's own error path and the stall
// watchdog.
func (s *Server) retryURLSessionLocked() {
	next, retryErr := s.urlTransport.RetryCurrent(context.Background())
	if retryErr != nil {
		if errors.Is(retryErr, errDeadItemSkipped) {
			// The queue moved on: commit the new state as ordinary playback
			// and warn (journal + watch) so the jump explains itself.
			s.commitPlaybackLocked(next, false)
			s.logf("server.warning", map[string]any{"code": api.CodePlaybackSkipped, "message": retryErr.Error()})
			s.publishLocked("server.warning", map[string]any{"code": api.CodePlaybackSkipped, "message": retryErr.Error()})
			return
		}
		s.commitPlaybackLocked(core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, true)
		s.sequence++
		// A spent retry budget means the media stream stalled through both
		// attempts: the source itself may still be available, so this is a
		// playback error. Any other failure came from the upstream resolve
		// during playback, which the error table maps to source_unavailable.
		code := api.CodeSourceUnavailable
		if errors.Is(retryErr, errURLRetryExhausted) {
			code = api.CodePlaybackError
		}
		// Warnings are the only evidence of why a session died, and they
		// previously existed only on the watch feed: journal them so `lilt log`
		// can answer "why did playback stop" without a live client attached.
		s.logf("server.warning", map[string]any{"code": code, "message": retryErr.Error()})
		s.publishLocked("server.warning", map[string]any{"code": code, "message": retryErr.Error()})
		return
	}
	s.commitPlaybackLocked(next, false)
}
