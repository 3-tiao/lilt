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
	mu            sync.Mutex
	state         core.PlaybackState
	started       time.Time
	elapsed       float64
	duration      float64
	urlGeneration uint64
	urlSession    string
	// parkAfterEnqueue mimics the real MusicKit behaviour the finite-queue path
	// guards against: the paced appends leave the player parked on a paused
	// snapshot with the whole queue built.
	parkAfterEnqueue bool
	// resumeErr makes ResumeState fail, which is the recoverable failure a
	// complete fill can hit (docs/product/open-questions.md OQ17).
	resumeErr error
	// refuseEnqueue names the track ids the engine will not queue, which is how
	// a partial fill is reproduced in tests.
	refuseEnqueue map[string]bool
	// playSongsErr forces the one-shot start to fail, which is how the server's
	// append fallback is reproduced (see docs/product/limitations.md §7b).
	playSongsErr error
	undo         *fakeQueueUndo
	undoClock    uint64
	fullQueue    bool // isolated fake-only probe of finite-queue TUI interactions
}

type fakeQueueUndo struct {
	handle       string
	item         core.Item
	index        int
	currentIndex int
	postQueue    []core.Item
}

func NewFakeEngine() *FakeEngine {
	return &FakeEngine{state: core.PlaybackState{Status: "paused", Mode: "preview", Authorization: "denied"}}
}

// NewFullQueueFakeEngine makes the fake advertise a finite authorized queue.
// It is only wired behind the opt-in fake server's LILT_FAKE_FULL_QUEUE probe.
func NewFullQueueFakeEngine() *FakeEngine {
	f := NewFakeEngine()
	f.fullQueue = true
	f.state.Authorization = "authorized"
	f.state.Mode = "full"
	return f
}
func (f *FakeEngine) Play(_ context.Context, r core.PlaybackRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlSession = ""
	f.state.Status = "playing"
	f.state.Track = &core.Item{Kind: r.Kind, ID: r.ID, URL: r.URL, Title: "fake track"}
	f.state.Mode = "preview"
	if f.fullQueue {
		f.state.Mode = "full"
		f.state.Format = "fake finite queue"
	} else {
		f.state.Format = "AAC preview"
	}
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
func (f *FakeEngine) Stations(_ context.Context, term string, _ int) ([]core.Item, error) {
	return []core.Item{{Kind: "station", ID: "fake:station", Title: term + " (fake)", Artist: "lilt"}}, nil
}
func (f *FakeEngine) Authorization(context.Context) (core.AuthorizationStatus, error) {
	if f.fullQueue {
		return core.AuthorizationStatus{Status: "authorized", AccountStatus: "ready", CanPlayCatalogContent: true}, nil
	}
	return core.AuthorizationStatus{Status: "denied"}, nil
}

func (f *FakeEngine) RequestAuthorization(_ context.Context, _ bool) (core.AuthorizationStatus, error) {
	return core.AuthorizationStatus{Status: "denied"}, nil
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
	if f.resumeErr != nil {
		return f.resumeErr
	}
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
	f.urlSession = ""
	f.state.Status = "stopped"
	f.state.Mode = "none"
	f.elapsed = 0
	f.state.Position = 0
	f.state.Track = nil
	f.state.Queue = nil
	f.state.QueueIndex = 0
	return f.state, nil
}
func (f *FakeEngine) Enqueue(_ context.Context, request core.PlaybackRequest, _ string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuseEnqueue[request.ID] {
		return core.PlaybackState{}, fmt.Errorf("the engine refused %q", request.ID)
	}
	if f.parkAfterEnqueue {
		f.state.Status = "paused"
	}
	return f.state, nil
}

// SetQueue installs a specific queue with the index untouched, for tests
// that need canonical refs on the rows.
func (f *FakeEngine) SetQueue(items []core.Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Queue = items
}

// RefuseEnqueue makes every later append of that track id fail, which is the
// partial fill the queue path must report instead of silently shortening.
func (f *FakeEngine) RefuseEnqueue(trackID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuseEnqueue == nil {
		f.refuseEnqueue = map[string]bool{}
	}
	f.refuseEnqueue[trackID] = true
}

// PlaySongs is the one-shot finite-queue start: the whole queue is assigned at
// once and playback begins at StartAt, with no paced fill. FailPlaySongs forces
// the batch rejection the server answers with its append fallback.
func (f *FakeEngine) PlaySongs(_ context.Context, request core.PlaySongsRequest) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playSongsErr != nil {
		return core.PlaybackState{}, f.playSongsErr
	}
	f.urlSession = ""
	queue := make([]core.Item, 0, len(request.IDs))
	for _, id := range request.IDs {
		queue = append(queue, core.Item{Kind: "song", ID: id, Title: "fake " + id, Artist: "lilt"})
	}
	if len(queue) == 0 {
		return core.PlaybackState{}, fmt.Errorf("no songs to play")
	}
	start := request.StartAt
	if start < 0 || start >= len(queue) {
		start = 0
	}
	f.state.Status = "playing"
	f.state.Mode = "preview"
	if f.fullQueue {
		f.state.Mode = "full"
	}
	f.state.Format = "fake one-shot queue"
	f.state.Queue = queue
	f.state.QueueIndex = start
	current := queue[start]
	f.state.Track = &current
	f.started = time.Now()
	f.elapsed = 0
	return f.state, nil
}

// FailPlaySongs makes the next one-shot start fail, which forces the server
// onto the paced-append fallback path.
func (f *FakeEngine) FailPlaySongs(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playSongsErr = err
}

// ParkAfterEnqueue makes the next appends leave the player paused with the queue
// built, which is what the finite-queue re-pin exists for.
func (f *FakeEngine) ParkAfterEnqueue() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parkAfterEnqueue = true
}

// FailResume makes every later resume fail.
func (f *FakeEngine) FailResume(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeErr = err
}
func (f *FakeEngine) QueueJump(_ context.Context, index int) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= 0 && index < len(f.state.Queue) {
		f.state.QueueIndex = index
	}
	return f.state, nil
}
func (f *FakeEngine) QueueRemove(_ context.Context, index int) (core.QueueRemoveOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undo = nil
	if index >= 0 && index < len(f.state.Queue) {
		item := f.state.Queue[index]
		current := f.state.QueueIndex
		f.state.Queue = append(f.state.Queue[:index], f.state.Queue[index+1:]...)
		// Mirror the helper's cursor rules: removing an entry before the
		// cursor shifts it down, removing the current entry advances to the
		// next (clamped to the last). Skipping this left queueIndex out of
		// range ("2/1") after removing the current row (batch
		// 2026-09-23-polish, rp3 replay).
		switch {
		case f.state.QueueIndex > index:
			f.state.QueueIndex--
		case f.state.QueueIndex == index:
			f.state.QueueIndex = min(index, max(0, len(f.state.Queue)-1))
		}
		if index > current {
			f.undoClock++
			handle := fmt.Sprintf("fake-undo-%d", f.undoClock)
			f.undo = &fakeQueueUndo{handle: handle, item: item, index: index, currentIndex: current, postQueue: append([]core.Item(nil), f.state.Queue...)}
			return core.QueueRemoveOutcome{State: f.state, UndoHandle: handle}, nil
		}
	}
	return core.QueueRemoveOutcome{State: f.state}, nil
}
func (f *FakeEngine) QueueRestore(_ context.Context, handle string) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	undo := f.undo
	if undo == nil || undo.handle != handle || f.state.QueueIndex != undo.currentIndex ||
		!sameFakeQueue(f.state.Queue, undo.postQueue) || undo.index <= f.state.QueueIndex || undo.index > len(f.state.Queue) {
		return core.PlaybackState{}, core.ErrQueueUndoUnavailable
	}
	next := make([]core.Item, 0, len(f.state.Queue)+1)
	next = append(next, f.state.Queue[:undo.index]...)
	next = append(next, undo.item)
	next = append(next, f.state.Queue[undo.index:]...)
	f.state.Queue = next
	f.undo = nil
	return f.state, nil
}

func sameFakeQueue(a, b []core.Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].ID != b[i].ID {
			return false
		}
	}
	return true
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
	f.urlSession = ""
	f.state = core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true, Authorization: "denied", Track: &core.Item{Kind: "stream", URL: url, Title: name}}
	f.started = time.Now()
	f.elapsed = 0
	return f.state, nil
}
func (f *FakeEngine) RadioStop(context.Context) (core.PlaybackState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlSession = ""
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
