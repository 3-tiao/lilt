package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// recordRecentLocked adds one qualified play to the activity store and publishes
// the state change. Callers hold s.mu.
func (s *Server) recordRecentLocked(source string, item core.Item) {
	if s.activity == nil || s.store == nil {
		return
	}
	// Radio rows before the ICY name arrives have no title; the tracker retries
	// once the stream announces one (a new occurrence begins).
	if strings.TrimSpace(item.Title) == "" {
		return
	}
	projected := ProjectItem(item, api.SourceID(source))
	stored := activityItemFromAPI(projected)
	if apiErr := s.activityMutation(func() error {
		return s.activity.RecordQualifiedPlay(stored, time.Now())
	}); apiErr != nil {
		// A failed automatic mutation is dropped, never retried; the degraded
		// store already published server.warning.
		s.logf("recent.record_failed", map[string]any{"error": apiErr.Message})
	}
}

// recentOccurrence is one playback occurrence of a track. History is written
// only after enough monotonic playing time has accumulated.
type recentOccurrence struct {
	source      string
	item        core.Item
	accumulated time.Duration
	recorded    bool
	emitted     bool
}

// recentTracker decides when a playback occurrence qualifies for history:
// cumulative monotonic status=playing time must reach min(minThreshold, 50% of
// a known finite duration). Seek/position jumps and non-playing states do not
// count.
type recentTracker struct {
	mu           sync.Mutex
	minThreshold time.Duration
	record       func(source string, item core.Item)
	active       *recentOccurrence
	lastPosition float64
	lastAt       time.Time
}

func newRecentTracker(minThreshold time.Duration, record func(string, core.Item)) *recentTracker {
	if minThreshold <= 0 {
		minThreshold = 30 * time.Second
	}
	return &recentTracker{minThreshold: minThreshold, record: record}
}

// begin starts a new occurrence for an explicitly started track.
func (t *recentTracker) begin(source string, item core.Item) {
	t.mu.Lock()
	t.active = &recentOccurrence{source: source, item: item}
	t.lastPosition = 0
	t.lastAt = time.Time{}
	t.mu.Unlock()
}

// sample advances the accumulator from one engine state snapshot. It may invoke
// the record callback once the threshold is reached.
func (t *recentTracker) sample(state core.PlaybackState, source api.SourceID, at time.Time) {
	t.mu.Lock()
	// A track change starts a new occurrence even without an explicit play
	// command (queue next/previous, repeat wrap, external media keys).
	if state.Track != nil && trackIdentity(*state.Track) != "" {
		if t.active == nil || trackIdentity(t.active.item) != trackIdentity(*state.Track) {
			t.active = &recentOccurrence{source: string(source), item: *state.Track}
			t.lastPosition = 0
			t.lastAt = time.Time{}
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
				occurrence.accumulated = 0
				occurrence.recorded = false
				occurrence.emitted = false
			} else if state.Status == "playing" && !occurrence.recorded {
				if advanced <= elapsed.Seconds()+2.0 {
					occurrence.accumulated += elapsed
				}
			}
		}
		if state.Status == "playing" && !occurrence.recorded &&
			occurrence.accumulated >= t.threshold(state.Duration) {
			occurrence.recorded = true
		}
	}
	t.lastPosition = state.Position
	t.lastAt = at

	var ready *recentOccurrence
	if occurrence != nil && occurrence.recorded && !occurrence.emitted {
		occurrence.emitted = true
		copied := *occurrence
		ready = &copied
	}
	t.mu.Unlock()

	if ready != nil {
		t.record(ready.source, ready.item)
	}
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
		return "url:" + normalizeStreamURL(item.URL)
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
		// Sample under the command lock: the helper executes requests serially,
		// so polling its state while a play is rebuilding a queue would time out
		// the in-flight call and tear down the transport.
		s.mu.Lock()
		engine := s.engine
		if engine == nil {
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		state, err := engine.State(ctx)
		cancel()
		source := s.publicActiveSourceLocked()
		s.mu.Unlock()
		if err != nil {
			continue
		}
		s.recent.sample(state, source, time.Now())
	}
}

func (s *Server) recordRecent(source string, item core.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordRecentLocked(source, item)
}
