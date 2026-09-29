package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
)

var recentTestSource = api.SourceAppleMusic

type blockedRecentState struct {
	*fakeengine.FakeEngine
	started chan struct{}
	release chan struct{}
	state   core.PlaybackState
}

func (e *blockedRecentState) State(context.Context) (core.PlaybackState, error) {
	close(e.started)
	<-e.release
	return e.state, nil
}

func TestRecentSampleCannotRestoreOldTrackAfterNewPlay(t *testing.T) {
	old := core.Item{Kind: api.KindSong, ID: "old", Title: "Old"}
	newTrack := core.Item{Kind: api.KindSong, ID: "new", Title: "New"}
	engine := &blockedRecentState{
		FakeEngine: fakeengine.NewFakeEngine(),
		started:    make(chan struct{}), release: make(chan struct{}),
		state: core.PlaybackState{Status: "playing", Track: &old},
	}
	tracker, _ := recordingTracker(time.Second)
	tracker.begin(string(recentTestSource), old)
	s := &Server{engine: engine, activeSource: recentTestSource, recent: tracker}
	sampled := make(chan struct{})
	go func() {
		s.sampleRecentOnce(time.Now())
		close(sampled)
	}()
	<-engine.started
	playStarted := make(chan struct{})
	go func() {
		s.mu.Lock()
		tracker.begin(string(recentTestSource), newTrack)
		s.mu.Unlock()
		close(playStarted)
	}()
	close(engine.release)
	<-sampled
	<-playStarted
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.active == nil || tracker.active.item.ID != newTrack.ID {
		t.Fatalf("old sample replaced the new play: %+v", tracker.active)
	}
}

func recordingTracker(min time.Duration) (*recentTracker, func() []core.Item) {
	var mu sync.Mutex
	var recorded []core.Item
	tracker := newRecentTracker(min, func(ready *recentOccurrence) bool {
		mu.Lock()
		recorded = append(recorded, ready.item)
		mu.Unlock()
		return true
	})
	return tracker, func() []core.Item {
		mu.Lock()
		defer mu.Unlock()
		return append([]core.Item(nil), recorded...)
	}
}

func TestRecentTrackerRetriesFailedWriteOncePerOccurrence(t *testing.T) {
	attempts := 0
	ids := []string{}
	tracker := newRecentTracker(time.Second, func(ready *recentOccurrence) bool {
		attempts++
		ids = append(ids, ready.id)
		return attempts > 1
	})
	item := core.Item{Kind: api.KindSong, ID: "retry", Title: "Retry"}
	tracker.begin(string(recentTestSource), item)
	at := time.Now()
	for i := 0; i < 4; i++ {
		tracker.sample(core.PlaybackState{Status: "playing", Track: &item, Position: float64(i)}, recentTestSource, at.Add(time.Duration(i)*time.Second))
	}
	if attempts != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("retry attempts = %d, ids = %v; want same occurrence exactly twice", attempts, ids)
	}
}

func TestRecentTrackerWaitsForStreamTitleWithoutSuppressingFailureWarning(t *testing.T) {
	attempts := 0
	tracker := newRecentTracker(time.Second, func(ready *recentOccurrence) bool {
		attempts++
		if attempts == 1 {
			if ready.item.Title != "" {
				t.Fatalf("first title = %q", ready.item.Title)
			}
			return false // metadata not ready, no write failed
		}
		if ready.warned || ready.item.Title != "On air" {
			t.Fatalf("second attempt = %+v; need title and an unsuppressed warning", ready)
		}
		return true
	})
	item := core.Item{Kind: api.KindStream, URL: "https://radio.invalid/live"}
	tracker.begin(string(api.SourceRadio), item)
	at := time.Now()
	tracker.sample(core.PlaybackState{Status: "playing", Track: &item}, api.SourceRadio, at)
	tracker.sample(core.PlaybackState{Status: "playing", Track: &item}, api.SourceRadio, at.Add(time.Second))
	item.Title = "On air"
	tracker.sample(core.PlaybackState{Status: "playing", Track: &item}, api.SourceRadio, at.Add(2*time.Second))
	if attempts != 2 {
		t.Fatalf("record attempts = %d, want 2", attempts)
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
