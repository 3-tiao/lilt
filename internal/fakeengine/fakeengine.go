package fakeengine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
)

// FakeEngine is used by tests and LILT_FAKE_PLAYER=1 development sessions.
type FakeEngine struct {
	mu       sync.Mutex
	state    core.PlaybackState
	started  time.Time
	elapsed  float64
	duration float64
}

func NewFakeEngine() *FakeEngine {
	return &FakeEngine{state: core.PlaybackState{Status: "paused", Mode: "preview", Authorization: "denied"}}
}
func (f *FakeEngine) Play(_ context.Context, r core.PlaybackRequest) error {
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
func (f *FakeEngine) Search(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "fake:1", URL: "https://music.apple.com/us/song/fake/1", Title: term + " (fake)", Artist: "lilt", PreviewURL: "https://example.invalid/preview.m4a"}}, nil
}
func (f *FakeEngine) SearchPlaylists(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "fake:playlist", Title: term + " (fake)", Artist: "lilt"}}, nil
}
func (f *FakeEngine) LibraryPlaylists(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "playlist", ID: "fake:playlist", Title: "Fake Library Playlist", Artist: "lilt"}}, nil
}
func (f *FakeEngine) LibraryAlbums(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "album", ID: "fake:album", Title: "Fake Library Album", Artist: "lilt"}}, nil
}
func (f *FakeEngine) SearchAlbums(context.Context, string, int) ([]core.Item, error) {
	return []core.Item{{Kind: "album", ID: "fake:album", Title: "Fake Catalog Album", Artist: "lilt"}}, nil
}
func (f *FakeEngine) AlbumTracks(_ context.Context, id string) (core.Item, []core.Item, error) {
	album := core.Item{Kind: "album", ID: id, Title: "Fake Library Album", Artist: "lilt"}
	return album, []core.Item{
		{Kind: "song", ID: "fake:track:1", Title: "Fake Track One", Artist: "lilt"},
		{Kind: "song", ID: "fake:track:2", Title: "Fake Track Two", Artist: "lilt"},
	}, nil
}
func (f *FakeEngine) PlaylistTracks(_ context.Context, id string) (core.Item, []core.Item, error) {
	return core.Item{Kind: "playlist", ID: id, Title: "Fake Library Playlist", Artist: "lilt"}, []core.Item{
		{Kind: "song", ID: "fake:track:1", Title: "Fake Track One", Artist: "lilt"},
		{Kind: "song", ID: "fake:track:2", Title: "Fake Track Two", Artist: "lilt"},
	}, nil
}
func (f *FakeEngine) RecentPlayed(context.Context, int) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "fake:recent", URL: "https://music.apple.com/us/song/fake/1", Title: "Fake Recent Song", Artist: "lilt"}}, nil
}
func (f *FakeEngine) Stations(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "station", ID: "fake:station", Title: term + " (fake)", Artist: "lilt"}}, nil
}
func (f *FakeEngine) Authorization(context.Context) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "denied"}, nil
}

func (f *FakeEngine) RequestAuthorization(_ context.Context, _ bool) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "denied"}, nil
}

func (f *FakeEngine) ResolveURL(_ context.Context, raw string) (core.Item, error) {
	return core.Item{Kind: "song", ID: "fake:url", URL: raw, Title: "Fake URL Track", Artist: "lilt"}, nil
}

// TrackInfo resolves the fake catalog: any song/playlist id gets a stable
// display name so favorites.add is testable without the helper.
func (f *FakeEngine) TrackInfo(_ context.Context, kind, id string) (core.Item, error) {
	switch kind {
	case "song", "playlist":
		return core.Item{Kind: kind, ID: id, Title: "Fake " + kind + " " + id, Artist: "lilt"}, nil
	default:
		return core.Item{}, fmt.Errorf("trackInfo does not support kind %q", kind)
	}
}

func (f *FakeEngine) Recommendations(context.Context) ([]core.Item, error) {
	return []core.Item{{Kind: "song", ID: "fake:rec", Title: "Fake Recommendation", Artist: "lilt"}}, nil
}

func (f *FakeEngine) SubscribeState(context.Context) (core.StateSubscription, error) {
	updates := make(chan core.PlaybackStateUpdate)
	return core.StateSubscription{
		Initial: core.PlaybackStateUpdate{State: f.state},
		Updates: updates,
	}, nil
}

func (f *FakeEngine) UnsubscribeState(context.Context) error { return nil }
func (f *FakeEngine) Pause(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state.Status == "playing" {
		f.elapsed += time.Since(f.started).Seconds()
	}
	f.state.Position = f.elapsed
	f.state.Status = "paused"
	return nil
}
func (f *FakeEngine) Resume(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = time.Now()
	f.state.Status = "playing"
	return nil
}
func (f *FakeEngine) Next(context.Context) error     { return nil }
func (f *FakeEngine) Previous(context.Context) error { return nil }
func (f *FakeEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	err := f.Play(ctx, request)
	state, stateErr := f.State(ctx)
	if err == nil {
		err = stateErr
	}
	return state, err
}

// SetStatus parks the fake engine in an arbitrary status so a test can exercise
// states the normal play/pause flow does not reach (for example a finished
// finite queue).
func (f *FakeEngine) SetStatus(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = status
}

func (f *FakeEngine) PauseState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Pause(ctx)
	state, stateErr := f.State(ctx)
	if err == nil {
		err = stateErr
	}
	return state, err
}
func (f *FakeEngine) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Resume(ctx)
	state, stateErr := f.State(ctx)
	if err == nil {
		err = stateErr
	}
	return state, err
}
func (f *FakeEngine) NextState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Next(ctx)
	state, stateErr := f.State(ctx)
	if err == nil {
		err = stateErr
	}
	return state, err
}
func (f *FakeEngine) PreviousState(ctx context.Context) (core.PlaybackState, error) {
	err := f.Previous(ctx)
	state, stateErr := f.State(ctx)
	if err == nil {
		err = stateErr
	}
	return state, err
}
func (f *FakeEngine) SetShuffle(_ context.Context, on bool) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Shuffle = on
	return f.state, nil
}
func (f *FakeEngine) SetRepeat(_ context.Context, mode string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Repeat = mode
	return f.state, nil
}
func (f *FakeEngine) Stop(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = "stopped"
	f.state.Mode = "none"
	f.elapsed = 0
	f.state.Position = 0
	f.state.Track = nil
	f.state.Queue = nil
	f.state.QueueIndex = 0
	return f.state, nil
}
func (f *FakeEngine) Enqueue(context.Context, core.PlaybackRequest, string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *FakeEngine) QueueJump(_ context.Context, index int) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= 0 && index < len(f.state.Queue) {
		f.state.QueueIndex = index
	}
	return f.state, nil
}
func (f *FakeEngine) QueueRemove(_ context.Context, index int) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= 0 && index < len(f.state.Queue) {
		f.state.Queue = append(f.state.Queue[:index], f.state.Queue[index+1:]...)
	}
	return f.state, nil
}
func (f *FakeEngine) QueueMove(_ context.Context, from, to int) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from >= 0 && from < len(f.state.Queue) && to >= 0 && to < len(f.state.Queue) {
		entry := f.state.Queue[from]
		f.state.Queue = append(f.state.Queue[:from], f.state.Queue[from+1:]...)
		f.state.Queue = append(f.state.Queue[:to], append([]core.Item{entry}, f.state.Queue[to:]...)...)
	}
	return f.state, nil
}
func (f *FakeEngine) QueueClear(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Queue = nil
	f.state.QueueIndex = 0
	return f.state, nil
}
func (f *FakeEngine) RadioPlay(_ context.Context, url, name string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Authorization: "denied", Track: &core.Item{Kind: "stream", URL: url, Title: name}}
	f.started = time.Now()
	f.elapsed = 0
	return f.state, nil
}
func (f *FakeEngine) RadioStop(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Status = "stopped"
	f.state.Mode = "none"
	f.state.IsLive = false
	f.state.Track = nil
	return f.state, nil
}

func (f *FakeEngine) Probe(context.Context, string, int) (core.RadioProbeResult, error) {
	return core.RadioProbeResult{Status: "healthy", LatencyMs: 1}, nil
}
func (f *FakeEngine) State(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state.Status == "playing" {
		f.state.Position = f.elapsed + time.Since(f.started).Seconds()
	} else {
		f.state.Position = f.elapsed
	}
	return f.state, nil
}
