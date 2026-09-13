package session

import (
	"context"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
)

// FakeTarget is used by tests and LILT_FAKE_PLAYER=1 development sessions.
type FakeTarget struct {
	mu       sync.Mutex
	state    core.PlaybackState
	started  time.Time
	elapsed  float64
	duration float64
}

func NewFakeTarget() *FakeTarget {
	return &FakeTarget{state: core.PlaybackState{Status: "paused", Mode: "preview", Authorization: "denied"}}
}
func (f *FakeTarget) Play(_ context.Context, r core.PlaybackRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = "playing"
	f.state.Track = &core.Item{Kind: r.Kind, ID: r.ID, URL: r.URL, Title: "fake track"}
	f.state.Mode = "preview"
	f.state.Format = "AAC preview"
	f.state.Duration = 180
	f.state.Position = 0
	f.state.Queue = []core.Item{
		{Kind: "song", ID: "fake:1", Title: "Fake Track One", Artist: "lilt"},
		{Kind: "song", ID: "fake:2", Title: "Fake Track Two", Artist: "lilt"},
	}
	f.state.QueueIndex = 0
	f.started = time.Now()
	f.elapsed = 0
	return nil
}
func (f *FakeTarget) Search(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "fake:1", URL: "https://music.apple.com/us/song/fake/1", Title: term + " (fake)", Artist: "lilt", PreviewURL: "https://example.invalid/preview.m4a"}}, nil
}
func (f *FakeTarget) SearchPlaylists(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "fake:playlist", Title: term + " (fake)", Artist: "lilt"}}, nil
}
func (f *FakeTarget) LibraryPlaylists(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "fake:playlist", Title: "Fake Library Playlist", Artist: "lilt"}}, nil
}
func (f *FakeTarget) PlaylistTracks(context.Context, string) ([]core.Item, error) {
	return []core.Item{
		{Kind: "song", ID: "fake:track:1", Title: "Fake Track One", Artist: "lilt"},
		{Kind: "song", ID: "fake:track:2", Title: "Fake Track Two", Artist: "lilt"},
	}, nil
}
func (f *FakeTarget) RecentPlayed(context.Context, int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "fake:recent", URL: "https://music.apple.com/us/song/fake/1", Title: "Fake Recent Song", Artist: "lilt"}}, nil
}
func (f *FakeTarget) Stations(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "station", ID: "fake:station", Title: term + " (fake)", Artist: "lilt"}}, nil
}
func (f *FakeTarget) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "denied"}, nil
}
func (f *FakeTarget) Pause(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state.Status == "playing" {
		f.elapsed += time.Since(f.started).Seconds()
	}
	f.state.Position = f.elapsed
	f.state.Status = "paused"
	return nil
}
func (f *FakeTarget) Resume(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = time.Now()
	f.state.Status = "playing"
	return nil
}
func (f *FakeTarget) Next(context.Context) error     { return nil }
func (f *FakeTarget) Previous(context.Context) error { return nil }
func (f *FakeTarget) SetShuffle(_ context.Context, on bool) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Shuffle = on
	return f.state, nil
}
func (f *FakeTarget) SetRepeat(_ context.Context, mode string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Repeat = mode
	return f.state, nil
}
func (f *FakeTarget) Stop(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = "stopped"
	f.state.Mode = "none"
	f.elapsed = 0
	f.state.Position = 0
	return f.state, nil
}
func (f *FakeTarget) Enqueue(context.Context, core.PlaybackRequest, string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *FakeTarget) RadioPlay(_ context.Context, url, name string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Authorization: "denied", Track: &core.Item{Kind: "stream", URL: url, Title: name}}
	f.started = time.Now()
	f.elapsed = 0
	return f.state, nil
}
func (f *FakeTarget) RadioStop(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = "stopped"
	f.state.Mode = "none"
	f.state.IsLive = false
	return f.state, nil
}
func (f *FakeTarget) State(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state.Status == "playing" {
		f.state.Position = f.elapsed + time.Since(f.started).Seconds()
	} else {
		f.state.Position = f.elapsed
	}
	return f.state, nil
}
