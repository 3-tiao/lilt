package server

import (
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

var recentTestSource = api.SourceAppleMusic

func recordingTracker(min time.Duration) (*recentTracker, func() []core.Item) {
	var mu sync.Mutex
	var recorded []core.Item
	tracker := newRecentTracker(min, func(_ string, item core.Item) {
		mu.Lock()
		recorded = append(recorded, item)
		mu.Unlock()
	})
	return tracker, func() []core.Item {
		mu.Lock()
		defer mu.Unlock()
		return append([]core.Item(nil), recorded...)
	}
}

func TestRecentTrackerFiniteThresholdIsHalfDuration(t *testing.T) {
	tracker, recorded := recordingTracker(30 * time.Second)
	item := core.Item{Kind: "song", ID: "1", Title: "A"}
	tracker.begin("apple-music", item)
	start := time.Now()
	state := core.PlaybackState{Status: "playing", Track: &item, Duration: 10}

	tracker.sample(state, recentTestSource, start)
	for second := 1; second <= 6; second++ {
		state.Position = float64(second)
		tracker.sample(state, recentTestSource, start.Add(time.Duration(second)*time.Second))
	}
	if got := recorded(); len(got) != 1 {
		t.Fatalf("recorded %d items, want 1", len(got))
	}
	// Continued playing does not duplicate the entry.
	state.Position = 7
	tracker.sample(state, recentTestSource, start.Add(7*time.Second))
	if got := recorded(); len(got) != 1 {
		t.Fatalf("recorded %d items after more playing, want 1", len(got))
	}
}

func TestRecentTrackerIgnoresPausedTime(t *testing.T) {
	tracker, recorded := recordingTracker(3 * time.Second)
	item := core.Item{Kind: "song", ID: "2", Title: "B"}
	tracker.begin("apple-music", item)
	start := time.Now()

	state := core.PlaybackState{Status: "playing", Track: &item, Position: 0}
	tracker.sample(state, recentTestSource, start)
	paused := core.PlaybackState{Status: "paused", Track: &item, Position: 0}
	tracker.sample(paused, recentTestSource, start.Add(1*time.Second))
	tracker.sample(paused, recentTestSource, start.Add(10*time.Second))
	if got := recorded(); len(got) != 0 {
		t.Fatalf("paused time recorded %d items", len(got))
	}
	state.Position = 0
	tracker.sample(state, recentTestSource, start.Add(10*time.Second))
	for second := 11; second <= 13; second++ {
		state.Position = float64(second - 10)
		tracker.sample(state, recentTestSource, start.Add(time.Duration(second)*time.Second))
	}
	if got := recorded(); len(got) != 1 {
		t.Fatalf("recorded %d items, want 1 after 3s playing", len(got))
	}
}

func TestRecentTrackerIgnoresSeeks(t *testing.T) {
	tracker, recorded := recordingTracker(3 * time.Second)
	item := core.Item{Kind: "song", ID: "3", Title: "C"}
	tracker.begin("apple-music", item)
	start := time.Now()

	state := core.PlaybackState{Status: "playing", Track: &item, Position: 0}
	tracker.sample(state, recentTestSource, start)
	// A large forward jump is a seek and must not credit listening time.
	state.Position = 500
	tracker.sample(state, recentTestSource, start.Add(1*time.Second))
	if got := recorded(); len(got) != 0 {
		t.Fatalf("seek recorded %d items", len(got))
	}
	for second := 2; second <= 4; second++ {
		state.Position = 500 + float64(second-1)
		tracker.sample(state, recentTestSource, start.Add(time.Duration(second)*time.Second))
	}
	if got := recorded(); len(got) != 1 {
		t.Fatalf("recorded %d items, want 1", len(got))
	}
}

func TestRecentTrackerWrapStartsNewOccurrence(t *testing.T) {
	tracker, recorded := recordingTracker(2 * time.Second)
	item := core.Item{Kind: "song", ID: "4", Title: "D"}
	tracker.begin("apple-music", item)
	start := time.Now()

	state := core.PlaybackState{Status: "playing", Track: &item, Position: 0}
	tracker.sample(state, recentTestSource, start)
	for second := 1; second <= 2; second++ {
		state.Position = float64(second)
		tracker.sample(state, recentTestSource, start.Add(time.Duration(second)*time.Second))
	}
	if got := recorded(); len(got) != 1 {
		t.Fatalf("recorded %d, want 1", len(got))
	}
	// Repeat wrap: position resets, then qualifies again and refreshes history.
	state.Position = 0
	tracker.sample(state, recentTestSource, start.Add(3*time.Second))
	for second := 4; second <= 5; second++ {
		state.Position = float64(second - 3)
		tracker.sample(state, recentTestSource, start.Add(time.Duration(second)*time.Second))
	}
	if got := recorded(); len(got) != 2 {
		t.Fatalf("recorded %d after wrap, want 2", len(got))
	}
}

func TestRecentTrackerTrackChangeStartsNewOccurrence(t *testing.T) {
	tracker, recorded := recordingTracker(2 * time.Second)
	first := core.Item{Kind: "song", ID: "5", Title: "E"}
	second := core.Item{Kind: "song", ID: "6", Title: "F"}
	start := time.Now()

	tracker.sample(core.PlaybackState{Status: "playing", Track: &first, Position: 0}, recentTestSource, start)
	for i := 1; i <= 2; i++ {
		tracker.sample(core.PlaybackState{Status: "playing", Track: &first, Position: float64(i)}, recentTestSource, start.Add(time.Duration(i)*time.Second))
	}
	tracker.sample(core.PlaybackState{Status: "playing", Track: &second, Position: 0}, recentTestSource, start.Add(3*time.Second))
	for i := 4; i <= 5; i++ {
		tracker.sample(core.PlaybackState{Status: "playing", Track: &second, Position: float64(i - 3)}, recentTestSource, start.Add(time.Duration(i)*time.Second))
	}
	got := recorded()
	if len(got) != 2 || got[0].ID != "5" || got[1].ID != "6" {
		t.Fatalf("recorded = %+v", got)
	}
}
