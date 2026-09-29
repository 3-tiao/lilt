package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

// recordRecentLocked adds one qualified play to the activity store and publishes
// the state change. Callers hold s.mu, including the sampler.
func (s *Server) recordRecentLocked(ready *recentOccurrence) bool {
	// Radio rows before the ICY name arrives have no title; keep the pending
	// occurrence so the next snapshot can supply it.
	if strings.TrimSpace(ready.item.Title) == "" {
		return false
	}
	projected := ProjectItem(ready.item, api.SourceID(ready.source))
	stored, identityErr := activityItemFromAPI(projected)
	if identityErr != nil {
		s.logf("recent.identity_failed", map[string]any{"error": identityErr.Message})
		return false
	}
	var writeErr error
	if s.activity == nil || s.store == nil {
		writeErr = s.activityRequired()
	} else {
		writeErr = s.activity.RecordQualifiedPlayOnce(stored, ready.qualifiedAt, ready.id)
		if writeErr == nil {
			s.publishActivityChanged()
			return true
		}
	}
	if !ready.warned {
		s.logf("recent.record_failed", map[string]any{"error": writeErr.Error()})
		message := "a qualified play could not be confirmed in history; retrying while this track remains active"
		s.logf("server.warning", map[string]any{"code": api.CodeStorageUnavailable, "message": message})
		s.nextSequenceLocked()
		s.publishLocked("server.warning", map[string]any{"code": api.CodeStorageUnavailable, "message": message})
		ready.warned = true
	}
	return false
}

// recentOccurrence is one playback occurrence of a track. History is written
// only after enough monotonic playing time has accumulated.
type recentOccurrence struct {
	id          string
	source      string
	item        core.Item
	qualifiedAt time.Time
	accumulated time.Duration
	recorded    bool
	emitted     bool
	warned      bool
}

// recentTracker decides when a playback occurrence qualifies for history:
// cumulative monotonic status=playing time must reach min(minThreshold, 50% of
// a known finite duration). Seek/position jumps and non-playing states do not
// count.
type recentTracker struct {
	mu           sync.Mutex
	minThreshold time.Duration
	record       func(*recentOccurrence) bool
	active       *recentOccurrence
	lastPosition float64
	lastAt       time.Time
}

func newRecentTracker(minThreshold time.Duration, record func(*recentOccurrence) bool) *recentTracker {
	if minThreshold <= 0 {
		minThreshold = 30 * time.Second
	}
	return &recentTracker{minThreshold: minThreshold, record: record}
}

// begin starts a new occurrence for an explicitly started track.
func (t *recentTracker) begin(source string, item core.Item) {
	t.mu.Lock()
	t.active = &recentOccurrence{id: api.NewRequestID(), source: source, item: item}
	t.lastPosition = 0
	t.lastAt = time.Time{}
	t.mu.Unlock()
}

// forget suppresses the current occurrence after history is explicitly cleared.
// A new track or repeat wrap still starts a fresh occurrence.
func (t *recentTracker) forget() {
	t.mu.Lock()
	if t.active != nil {
		t.active.recorded = true
		t.active.emitted = true
	}
	t.mu.Unlock()
}

// sample advances the accumulator from one engine state snapshot. It may invoke
// the record callback once the threshold is reached.
func (t *recentTracker) sample(state core.PlaybackState, source api.SourceID, at time.Time) {
	if ready := t.advance(state, source, at); ready != nil {
		t.persist(ready)
	}
}

// advance updates the occurrence without invoking the persistence callback.
// The server holds its command lock through persistence as well, so clear
// and reset cannot overtake the sample.
func (t *recentTracker) advance(state core.PlaybackState, source api.SourceID, at time.Time) *recentOccurrence {
	t.mu.Lock()
	// A track change starts a new occurrence even without an explicit play
	// command (queue next/previous, repeat wrap, external media keys).
	if state.Track != nil && trackIdentity(*state.Track) != "" {
		if t.active == nil || trackIdentity(t.active.item) != trackIdentity(*state.Track) {
			t.active = &recentOccurrence{id: api.NewRequestID(), source: string(source), item: *state.Track}
			t.lastPosition = 0
			t.lastAt = time.Time{}
		} else if t.active.item.Title == "" && state.Track.Title != "" {
			t.active.item = *state.Track
		}
	}

	occurrence := t.active
	if occurrence != nil && state.Track != nil {
		if !t.lastAt.IsZero() {
			elapsed := at.Sub(t.lastAt)
			// Bound elapsed so a long gap (sleep, pause) is not credited.
			if elapsed > 5*time.Second {
				elapsed = 5 * time.Second
			}
			advanced := state.Position - t.lastPosition
			if advanced < -1.0 {
				// Repeat wrap or track restart: begin a fresh occurrence so a
				// later qualifying playback can refresh playedAt. Detected even
				// after the current occurrence has been recorded.
				occurrence.id = api.NewRequestID()
				occurrence.qualifiedAt = time.Time{}
				occurrence.accumulated = 0
				occurrence.recorded = false
				occurrence.emitted = false
				occurrence.warned = false
			} else if state.Status == "playing" && !occurrence.recorded {
				if advanced <= elapsed.Seconds()+2.0 {
					occurrence.accumulated += elapsed
				}
			}
		}
		if occurrence.qualifiedAt.IsZero() && state.Status == "playing" && !occurrence.recorded &&
			occurrence.accumulated >= t.threshold(state.Duration) {
			occurrence.qualifiedAt = at
		}
	}
	t.lastPosition = state.Position
	t.lastAt = at

	var ready *recentOccurrence
	if occurrence != nil && !occurrence.qualifiedAt.IsZero() && !occurrence.recorded && !occurrence.emitted {
		occurrence.emitted = true
		copied := *occurrence
		ready = &copied
	}
	t.mu.Unlock()
	return ready
}

func (t *recentTracker) persist(ready *recentOccurrence) {
	committed := t.record(ready)
	t.mu.Lock()
	if t.active != nil && t.active.id == ready.id {
		if committed {
			t.active.recorded = true
		} else {
			t.active.emitted = false
			t.active.warned = ready.warned
		}
	}
	t.mu.Unlock()
}

func (t *recentTracker) threshold(duration float64) time.Duration {
	limit := t.minThreshold
	if duration > 0 {
		half := time.Duration(duration / 2 * float64(time.Second))
		if half < limit {
			limit = half
		}
	}
	return limit
}

func trackIdentity(item core.Item) string {
	if item.ID == "" && item.URL == "" {
		return ""
	}
	if item.Kind == api.KindStream || item.ID == "" {
		return "url:" + api.NormalizeStreamURL(item.URL)
	}
	return "id:" + item.ID
}

// recordAfterPlayLocked resets the occurrence clock for a newly started track.
// Callers hold s.mu.
func (s *Server) recordAfterPlayLocked(reference api.Reference, state core.PlaybackState, name string) {
	if s.recent != nil && state.Track != nil {
		s.recent.begin(string(reference.Source), *state.Track)
	}
}

// runRecentSampler periodically samples playback to accumulate listening time.
func (s *Server) runRecentSampler() {
	if s.recent == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}
		s.sampleRecentOnce(time.Now())
	}
}

// sampleRecentOnce keeps attribution and persistence in the same command slot.
// A history clear/reset cannot overtake a qualified play between those steps.
func (s *Server) sampleRecentOnce(at time.Time) {
	s.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	state, source, err := s.activePlaybackStateLocked(ctx)
	cancel()
	var ready *recentOccurrence
	if err == nil {
		ready = s.recent.advance(state, source, at)
	}
	if ready != nil {
		s.recent.persist(ready)
	}
	s.mu.Unlock()
}

// activePlaybackStateLocked returns the authoritative state of the currently
// selected playback transport. Callers hold s.mu. Resource helpers are never
// considered here: they do not own audio or qualified-play timing.
func (s *Server) activePlaybackStateLocked(ctx context.Context) (core.PlaybackState, api.SourceID, error) {
	source := s.publicActiveSourceLocked()
	switch s.activeTransport {
	case transportURLQueue:
		if s.urlTransport == nil {
			return core.PlaybackState{}, source, errQueueNoSession
		}
		state, err := s.urlTransport.State(ctx)
		return state, source, err
	case transportStream:
		if s.audioEngine == nil {
			return core.PlaybackState{}, source, errQueueNoSession
		}
		state, err := s.audioEngine.State(ctx)
		return state, source, err
	default:
		if s.engine == nil {
			return core.PlaybackState{}, source, errQueueNoSession
		}
		state, err := s.engine.State(ctx)
		return state, source, err
	}
}
