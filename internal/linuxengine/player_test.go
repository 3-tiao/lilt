package linuxengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/appleweb"
)

// fakeStreams stands in for the mpv backend.
type fakeStreams struct {
	mu        sync.Mutex
	calls     []string
	plays     []core.URLPlaybackTarget
	state     core.PlaybackState
	updates   chan core.PlaybackStateUpdate
	closed    bool
	closeErr  error
	stopError error
}

func newFakeStreams() *fakeStreams {
	return &fakeStreams{state: core.PlaybackState{Status: "playing", Mode: "stream"}, updates: make(chan core.PlaybackStateUpdate, 8)}
}

func (f *fakeStreams) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeStreams) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call == name {
			return true
		}
	}
	return false
}

func (f *fakeStreams) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeStreams) State(context.Context) (core.PlaybackState, error) {
	f.record("State")
	return f.state, nil
}

func (f *fakeStreams) PauseState(context.Context) (core.PlaybackState, error) {
	f.record("PauseState")
	return core.PlaybackState{Status: "paused"}, nil
}

func (f *fakeStreams) ResumeState(context.Context) (core.PlaybackState, error) {
	f.record("ResumeState")
	return core.PlaybackState{Status: "playing"}, nil
}

func (f *fakeStreams) Stop(context.Context) (core.PlaybackState, error) {
	f.record("Stop")
	if f.stopError != nil {
		return core.PlaybackState{}, f.stopError
	}
	return core.PlaybackState{Status: "stopped"}, nil
}

func (f *fakeStreams) RadioPlay(context.Context, string, string) (core.PlaybackState, error) {
	f.record("RadioPlay")
	return core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true}, nil
}

func (f *fakeStreams) RadioStop(context.Context) (core.PlaybackState, error) {
	f.record("RadioStop")
	return core.PlaybackState{Status: "stopped"}, nil
}

func (f *fakeStreams) Probe(context.Context, string, int) (core.RadioProbeResult, error) {
	f.record("Probe")
	return core.RadioProbeResult{Status: "healthy"}, nil
}

func (f *fakeStreams) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	f.record("PlayURL")
	f.mu.Lock()
	f.plays = append(f.plays, target)
	f.mu.Unlock()
	return core.PlaybackState{Status: "playing", Mode: "full"}, nil
}

func (f *fakeStreams) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	f.record("PauseURL")
	return core.PlaybackState{Status: "paused"}, nil
}

func (f *fakeStreams) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	f.record("ResumeURL")
	return core.PlaybackState{Status: "playing"}, nil
}

func (f *fakeStreams) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	f.record("StopURL")
	return core.PlaybackState{Status: "stopped"}, nil
}

func (f *fakeStreams) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	f.record("StateURL")
	return f.state, nil
}

func (f *fakeStreams) SubscribeState(context.Context) (core.StateSubscription, error) {
	return core.StateSubscription{Updates: f.updates}, nil
}

func (f *fakeStreams) UnsubscribeState(context.Context) error { return nil }

func (f *fakeStreams) Close() error {
	f.record("Close")
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.closeErr
}

// fakeApple stands in for the browser backend.
type fakeApple struct {
	mu      sync.Mutex
	calls   []string
	played  []string
	state   appleweb.State
	closed  bool
	stopErr error
}

func newFakeApple() *fakeApple {
	return &fakeApple{state: appleweb.State{Ready: true, Authorized: true, Status: "playing", Duration: 204, Position: 1, ItemID: "111"}}
}

func (f *fakeApple) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeApple) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call == name {
			return true
		}
	}
	return false
}

func (f *fakeApple) setState(state appleweb.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *fakeApple) PlayCatalogSong(_ context.Context, songID string) error {
	f.record("PlayCatalogSong")
	f.mu.Lock()
	f.played = append(f.played, songID)
	f.mu.Unlock()
	return nil
}

func (f *fakeApple) Pause(context.Context) error  { f.record("Pause"); return nil }
func (f *fakeApple) Resume(context.Context) error { f.record("Resume"); return nil }
func (f *fakeApple) Stop(context.Context) error   { f.record("Stop"); return f.stopErr }
func (f *fakeApple) Close() error {
	f.record("Close")
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeApple) State(context.Context) (appleweb.State, error) {
	f.record("State")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func appleTarget(id string) core.URLPlaybackTarget {
	return core.URLPlaybackTarget{Item: core.Item{
		Source: string(api.SourceAppleMusic), Kind: api.KindSong, ID: id,
		URL: "https://music.apple.com/cn/song/fixture/" + id, Title: "Fixture",
	}}
}

func audiusTarget(id string) core.URLPlaybackTarget {
	return core.URLPlaybackTarget{Item: core.Item{
		Source: string(api.SourceAudius), Kind: api.KindSong, ID: id, URL: "https://audius.co/fixture",
	}}
}

// Only one backend may make sound: handing an Apple item to the browser has to
// stop mpv first.
func TestAppleTargetStopsTheStreamBackend(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()
	ctx := context.Background()

	// mpv plays first, so the hand-over has something to stop.
	if _, err := player.PlayURL(ctx, audiusTarget("a1")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	streams.mu.Lock()
	streams.calls = nil
	streams.mu.Unlock()

	if _, err := player.PlayURL(ctx, appleTarget("111")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if !streams.called("Stop") {
		t.Fatalf("mpv was not stopped before handing playback to the browser: %v", streams.callNames())
	}
	if len(apple.played) != 1 || apple.played[0] != "111" {
		t.Fatalf("played catalog ids = %v, want the queue item's id", apple.played)
	}
	if streams.called("PlayURL") {
		t.Fatal("an Apple item was also handed to mpv")
	}
}

// And the other way round: an mpv item stops the browser.
func TestDirectURLTargetStopsTheAppleBackend(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()

	// Prime the browser as the owner, then hand playback to mpv.
	if _, err := player.PlayURL(context.Background(), appleTarget("111")); err != nil {
		t.Fatalf("PrimeURL: %v", err)
	}
	apple.mu.Lock()
	apple.calls = nil
	apple.mu.Unlock()

	if _, err := player.PlayURL(context.Background(), audiusTarget("a1")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if !apple.called("Stop") {
		t.Fatal("the browser was not stopped before handing playback to mpv")
	}
	if !streams.called("PlayURL") {
		t.Fatal("the direct-URL item did not reach mpv")
	}
}

// Control goes to whichever backend owns the session.
func TestControlFollowsTheOwningBackend(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()
	ctx := context.Background()

	if _, err := player.PlayURL(ctx, appleTarget("111")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if _, err := player.PauseURL(ctx, 1, "session-1"); err != nil {
		t.Fatalf("PauseURL: %v", err)
	}
	if !apple.called("Pause") {
		t.Fatal("pause was not routed to the browser")
	}
	if streams.called("PauseURL") {
		t.Fatal("pause was also routed to mpv")
	}
	state, err := player.StateURL(ctx, 1, "session-1")
	if err != nil {
		t.Fatalf("StateURL: %v", err)
	}
	if state.Duration != 204 || state.Status != "playing" {
		t.Fatalf("state = %+v, want the browser's own state", state)
	}

	// Radio is the mpv side, and taking it back must stop the browser.
	if _, err := player.RadioPlay(ctx, "https://stream.invalid/live", "Station"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	if !apple.called("Stop") || !streams.called("RadioPlay") {
		t.Fatalf("radio did not take playback back from the browser: streams=%v apple=%v", streams.callNames(), apple.calls)
	}
	if _, err := player.PauseState(ctx); err != nil {
		t.Fatalf("PauseState: %v", err)
	}
	if !streams.called("PauseState") {
		t.Fatal("a stream pause did not reach mpv")
	}
}

// MusicKit's vocabulary maps onto the public one, and a finished item advances
// the queue exactly once.
func TestAppleStatesMapToPublicStatusAndEndOnce(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()
	ctx := context.Background()
	if _, err := player.PlayURL(ctx, appleTarget("111")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}

	cases := []struct {
		status string
		want   string
	}{
		{"playing", "playing"},
		{"loading", "buffering"},
		{"waiting", "buffering"},
		{"stalled", "buffering"},
		{"paused", "paused"},
		{"stopped", "stopped"},
		{"none", "stopped"},
	}
	for _, testCase := range cases {
		apple.setState(appleweb.State{Ready: true, Status: testCase.status, ItemID: "111"})
		state, err := player.StateURL(ctx, 1, "session-1")
		if err != nil {
			t.Fatalf("StateURL(%s): %v", testCase.status, err)
		}
		if state.Status != testCase.want {
			t.Fatalf("status %q mapped to %q, want %q", testCase.status, state.Status, testCase.want)
		}
	}

	apple.setState(appleweb.State{Ready: true, Status: "completed", ItemID: "111"})
	first, err := player.StateURL(ctx, 1, "session-1")
	if err != nil {
		t.Fatalf("StateURL: %v", err)
	}
	if !first.Ended || first.Status != "ended" {
		t.Fatalf("completed state = %+v, want ended with Ended set", first)
	}
	again, err := player.StateURL(ctx, 1, "session-1")
	if err != nil {
		t.Fatalf("StateURL: %v", err)
	}
	if again.Ended {
		t.Fatal("the same item reported Ended twice; the queue would skip a track")
	}
}

// mpv's own progress must keep flowing while mpv owns the session.
func TestStreamUpdatesAreForwardedOnlyWhileStreamsOwn(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()
	subscription, err := player.SubscribeState(context.Background())
	if err != nil {
		t.Fatalf("SubscribeState: %v", err)
	}

	streams.mu.Lock()
	streams.calls = nil
	streams.mu.Unlock()
	if _, err := player.PlayURL(context.Background(), audiusTarget("a1")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	streams.updates <- core.PlaybackStateUpdate{Sequence: 1, State: core.PlaybackState{Status: "playing", Position: 3}}
	select {
	case update := <-subscription.Updates:
		if update.State.Position != 3 {
			t.Fatalf("forwarded update = %+v", update.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mpv updates were not forwarded while mpv owned playback")
	}

	// While the browser owns playback, mpv's stream must not leak through.
	if _, err := player.PlayURL(context.Background(), appleTarget("111")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	streams.updates <- core.PlaybackStateUpdate{Sequence: 2, State: core.PlaybackState{Status: "playing", Position: 99}}
	select {
	case update := <-subscription.Updates:
		if update.State.Position == 99 {
			t.Fatal("a stale mpv update was published while the browser owned playback")
		}
	case <-time.After(500 * time.Millisecond):
	}
}

// Stop must not leave the browser believing it still owns playback.
func TestStopReleasesOwnership(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	defer func() { _ = player.Close() }()
	ctx := context.Background()
	if _, err := player.PlayURL(ctx, appleTarget("111")); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	if _, err := player.StopURL(ctx, 1, "session-1"); err != nil {
		t.Fatalf("StopURL: %v", err)
	}
	if player.owns() != backendNone {
		t.Fatalf("owner = %q after stop, want none", player.owns())
	}
	// A later state read goes to mpv, which is the default owner.
	streams.mu.Lock()
	streams.calls = nil
	streams.mu.Unlock()
	if _, err := player.State(ctx); err != nil {
		t.Fatalf("State: %v", err)
	}
	if !streams.called("State") {
		t.Fatal("state did not fall back to the stream backend")
	}
}

// Close releases both backends: exiting lilt must not leave a player behind.
func TestCloseReleasesBothBackends(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	player := New(streams, apple)
	if err := player.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !streams.closed {
		t.Fatal("the stream backend was not released")
	}
	if !apple.closed {
		t.Fatal("the browser was not released")
	}
}

// A failed hand-over must surface instead of silently leaving two backends.
func TestHandoverFailureIsReported(t *testing.T) {
	streams, apple := newFakeStreams(), newFakeApple()
	apple.stopErr = errors.New("the browser refused to stop")
	player := New(streams, apple)
	defer func() { _ = player.Close() }()

	if _, err := player.PlayURL(context.Background(), appleTarget("111")); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if _, err := player.PlayURL(context.Background(), audiusTarget("a1")); err == nil {
		t.Fatal("a failed hand-over from the browser to mpv must be reported")
	}
}
