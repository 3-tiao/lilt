package fakeengine

import (
	"context"
	"fmt"
	"time"

	"github.com/caiguo/lilt/core"
)

// PlayURL simulates a URL session without opening, retaining, or probing the
// ephemeral media URL. The server-owned URLQueueTransport owns the finite queue.
func (f *FakeEngine) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if target.URL == "" || target.TransportSessionID == "" || target.PlaybackGeneration == 0 {
		return core.PlaybackState{}, fmt.Errorf("invalid fake URL playback target")
	}
	if target.PlaybackGeneration < f.urlGeneration ||
		(target.PlaybackGeneration == f.urlGeneration && f.urlSession != target.TransportSessionID) {
		return core.PlaybackState{}, fmt.Errorf("stale fake URL playback session")
	}
	f.urlGeneration = target.PlaybackGeneration
	f.urlSession = target.TransportSessionID
	item := target.Item
	f.state = core.PlaybackState{
		Status: "playing", Mode: "url", Format: "fake URL playback",
		Authorization: "denied", Track: &item, Duration: float64(target.Duration),
		PlaybackGeneration: target.PlaybackGeneration, TransportSessionID: target.TransportSessionID,
	}
	f.started = time.Now()
	f.elapsed = 0
	return f.state, nil
}

func (f *FakeEngine) requireURLSessionLocked(generation uint64, session string) error {
	if session == "" || session != f.urlSession || generation != f.urlGeneration {
		return fmt.Errorf("stale fake URL playback session")
	}
	return nil
}

func (f *FakeEngine) urlStateLocked() core.PlaybackState {
	state := f.state
	if state.Status == "playing" {
		state.Position = f.elapsed + time.Since(f.started).Seconds()
	} else {
		state.Position = f.elapsed
	}
	return state
}

func (f *FakeEngine) PauseURL(_ context.Context, generation uint64, session string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.requireURLSessionLocked(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	if f.state.Status == "playing" {
		f.elapsed += time.Since(f.started).Seconds()
	}
	f.state.Status = "paused"
	return f.urlStateLocked(), nil
}

func (f *FakeEngine) ResumeURL(_ context.Context, generation uint64, session string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.requireURLSessionLocked(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	if f.state.Status != "playing" {
		f.started = time.Now()
	}
	f.state.Status = "playing"
	return f.urlStateLocked(), nil
}

func (f *FakeEngine) StopURL(_ context.Context, generation uint64, session string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.requireURLSessionLocked(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	f.urlSession = ""
	f.state = core.PlaybackState{Status: "stopped", Mode: "none", Authorization: "denied", QueueIndex: -1}
	f.elapsed = 0
	return f.state, nil
}

func (f *FakeEngine) StateURL(_ context.Context, generation uint64, session string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.requireURLSessionLocked(generation, session); err != nil {
		return core.PlaybackState{}, err
	}
	return f.urlStateLocked(), nil
}
