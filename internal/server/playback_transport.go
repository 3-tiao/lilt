package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// errQueueNoSession reports a control that requires an active URL session.
var errQueueNoSession = errors.New("no active URL session")

// errQueueIndexOutOfRange reports a control request with no such queue item.
// It is a state error, not a source failure, and must not end the session.
var errQueueIndexOutOfRange = errors.New("queue index out of range")

// TransportID identifies a playback mechanism, not a public content source.
type TransportID string

const transportURLQueue TransportID = "url-queue"

// transportStream is live AVPlayer playback owned by lilt-audio.
const transportStream TransportID = "stream"

// transportEngine is MusicKit playback owned by lilt-player.
const transportEngine TransportID = "engine"

// PreparedPlayback is the private, compile-time provider/transport handoff.
// Its public queue is stable data and must never contain short-lived media URLs.
type PreparedPlayback interface {
	Source() api.SourceID
	Transport() TransportID
	PublicQueue() []api.Item
	StartIndex() int
}

// PlaybackTransport is the finite queue/control boundary required by v1.
// Implementations own their queue while a helper/driver owns audio output.
type PlaybackTransport interface {
	ID() TransportID
	Start(context.Context, PreparedPlayback, uint64, string) (core.PlaybackState, error)
	Pause(context.Context) (core.PlaybackState, error)
	Resume(context.Context) (core.PlaybackState, error)
	Next(context.Context) (core.PlaybackState, error)
	Previous(context.Context) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	Jump(context.Context, int) (core.PlaybackState, error)
	State(context.Context) (core.PlaybackState, error)
	List() api.QueueState
}

// PlaybackRequest contains stable references only. Providers may prepare one
// ref or an explicit same-source song list without exposing transport payloads.
type PlaybackRequest struct {
	References []api.Reference
	StartIndex int
	// FromHere drops entries before StartIndex so "play from here" builds a
	// forward-only queue instead of keeping earlier tracks as history.
	FromHere bool
}

// PlaybackPreparer is an optional provider extension. Capability declaration,
// not implementation of this interface alone, remains the routing truth.
type PlaybackPreparer interface {
	PreparePlayback(context.Context, PlaybackRequest) (PreparedPlayback, *api.Error)
}

type urlResolution struct {
	URL        string
	ArtworkURL string
	Duration   int
}

type urlResolver func(context.Context, api.Item) (urlResolution, error)

// URLQueuePlan carries only a stable public queue plus a lazy resolver. The
// resolver result is never written back into this plan.
type URLQueuePlan struct {
	source     api.SourceID
	queue      []api.Item
	startIndex int
	resolve    urlResolver
}

func NewURLQueuePlan(source api.SourceID, queue []api.Item, startIndex int, resolve urlResolver) URLQueuePlan {
	return URLQueuePlan{source: source, queue: cloneAPIItems(queue), startIndex: startIndex, resolve: resolve}
}

func (p URLQueuePlan) Source() api.SourceID    { return p.source }
func (p URLQueuePlan) Transport() TransportID  { return transportURLQueue }
func (p URLQueuePlan) PublicQueue() []api.Item { return cloneAPIItems(p.queue) }
func (p URLQueuePlan) StartIndex() int         { return p.startIndex }
func (p URLQueuePlan) resolveURL(ctx context.Context, item api.Item) (urlResolution, error) {
	if p.resolve == nil {
		return urlResolution{}, fmt.Errorf("URL queue plan has no resolver")
	}
	return p.resolve(ctx, item)
}

type urlQueuePrepared interface {
	PreparedPlayback
	resolveURL(context.Context, api.Item) (urlResolution, error)
}

// URLPlaybackTarget is ephemeral input to the helper boundary. URL is the only
// field here that may contain a short-lived media URL.
type URLPlaybackTarget = core.URLPlaybackTarget

// URLPlaybackDriver is injectable so the Go transport can be tested without
// the signed Swift helper. Every state-changing operation is session-bound.
type URLPlaybackDriver interface {
	PlayURL(context.Context, URLPlaybackTarget) (core.PlaybackState, error)
	PauseURL(context.Context, uint64, string) (core.PlaybackState, error)
	ResumeURL(context.Context, uint64, string) (core.PlaybackState, error)
	StopURL(context.Context, uint64, string) (core.PlaybackState, error)
	StateURL(context.Context, uint64, string) (core.PlaybackState, error)
}

// URLQueueTransport owns a deterministic finite queue. It resolves exactly one
// item when that item starts; signed URLs are never retained in the transport.
type URLQueueTransport struct {
	mu         sync.Mutex
	driver     URLPlaybackDriver
	source     api.SourceID
	items      []api.Item
	index      int
	revision   uint64
	resolver   urlResolver
	generation uint64
	sessionID  string
	paused     bool
	retried    bool
	last       core.PlaybackState
}

func NewURLQueueTransport(driver URLPlaybackDriver) *URLQueueTransport {
	return &URLQueueTransport{driver: driver, index: -1}
}

// SetDriver points the transport at a fresh helper client after a rebuild.
func (t *URLQueueTransport) SetDriver(driver URLPlaybackDriver) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.driver = driver
}

func (*URLQueueTransport) ID() TransportID { return transportURLQueue }

func (t *URLQueueTransport) Start(ctx context.Context, prepared PreparedPlayback, generation uint64, sessionID string) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	plan, ok := prepared.(urlQueuePrepared)
	if !ok || prepared.Transport() != transportURLQueue {
		return core.PlaybackState{}, fmt.Errorf("url queue transport received incompatible plan")
	}
	items := prepared.PublicQueue()
	index := prepared.StartIndex()
	if len(items) == 0 || index < 0 || index >= len(items) || sessionID == "" {
		return core.PlaybackState{}, fmt.Errorf("url queue plan is invalid")
	}
	if t.driver == nil {
		return core.PlaybackState{}, fmt.Errorf("url queue transport has no driver")
	}
	for _, item := range items {
		if item.Source != prepared.Source() || item.Kind != api.KindSong {
			return core.PlaybackState{}, fmt.Errorf("url queue plan contains an invalid item")
		}
	}
	t.clearLocked()
	t.revision++
	t.source = prepared.Source()
	t.items = cloneAPIItems(items)
	t.index = index
	t.resolver = plan.resolveURL
	t.generation = generation
	t.sessionID = sessionID
	t.paused = false
	t.retried = false
	state, err := t.playCurrentLocked(ctx)
	if err != nil {
		t.clearLocked()
		return core.PlaybackState{}, err
	}
	return state, nil
}

func (t *URLQueueTransport) Pause(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	state, err := t.driver.PauseURL(ctx, t.generation, t.sessionID)
	if err != nil {
		return core.PlaybackState{}, err
	}
	t.paused = true
	t.last = t.sanitizeStateLocked(state)
	return t.last, nil
}

func (t *URLQueueTransport) Resume(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	state, err := t.driver.ResumeURL(ctx, t.generation, t.sessionID)
	if err != nil {
		return core.PlaybackState{}, err
	}
	t.paused = false
	t.last = t.sanitizeStateLocked(state)
	return t.last, nil
}

func (t *URLQueueTransport) Next(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playIndexLocked(ctx, t.index+1)
}

// AdvanceEnded consumes a natural EOF. The final item clears the finite queue
// and becomes stopped; an EOF never leaks into the public state. A mid-queue
// resolution failure ends the session.
func (t *URLQueueTransport) AdvanceEnded(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if t.index+1 < len(t.items) {
		state, err := t.playIndexLocked(ctx, t.index+1)
		if err != nil {
			_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
			t.clearLocked()
			t.revision++
			return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, err
		}
		return state, nil
	}
	state, err := t.driver.StopURL(ctx, t.generation, t.sessionID)
	if err != nil {
		return core.PlaybackState{}, err
	}
	t.clearLocked()
	t.revision++
	state.Track = nil
	state.Queue = nil
	state.QueueIndex = -1
	state.Status = "stopped"
	state.Mode = "none"
	state.Ended = false
	return state, nil
}

func (t *URLQueueTransport) Previous(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playIndexLocked(ctx, t.index-1)
}

func (t *URLQueueTransport) Jump(ctx context.Context, index int) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playIndexLocked(ctx, index)
}

func (t *URLQueueTransport) playIndexLocked(ctx context.Context, index int) (core.PlaybackState, error) {
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if index < 0 || index >= len(t.items) {
		return core.PlaybackState{}, errQueueIndexOutOfRange
	}
	old := t.index
	t.index = index
	t.retried = false
	state, err := t.playCurrentLocked(ctx)
	if err != nil {
		t.index = old
		return core.PlaybackState{}, err
	}
	// Switching tracks preserves the paused state.
	if t.paused {
		pausedState, pauseErr := t.driver.PauseURL(ctx, t.generation, t.sessionID)
		if pauseErr != nil {
			return core.PlaybackState{}, pauseErr
		}
		t.last = t.sanitizeStateLocked(pausedState)
		return t.last, nil
	}
	return state, nil
}

func (t *URLQueueTransport) playCurrentLocked(ctx context.Context) (core.PlaybackState, error) {
	item := t.items[t.index]
	resolved, err := t.resolver(ctx, item)
	if err != nil {
		return core.PlaybackState{}, err
	}
	if resolved.URL == "" {
		return core.PlaybackState{}, fmt.Errorf("URL resolver returned an empty URL")
	}
	state, err := t.driver.PlayURL(ctx, URLPlaybackTarget{Item: publicCoreItem(item), URL: resolved.URL, ArtworkURL: resolved.ArtworkURL, Duration: resolved.Duration, PlaybackGeneration: t.generation, TransportSessionID: t.sessionID})
	if err != nil {
		return core.PlaybackState{}, err
	}
	t.last = t.sanitizeStateLocked(state)
	return t.last, nil
}

// Add inserts same-source items after the current one ("next") or at the tail,
// preserving their order. A playlist ref therefore adds every prepared track.
func (t *URLQueueTransport) Add(ctx context.Context, items []api.Item, position string) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if len(items) == 0 {
		return core.PlaybackState{}, fmt.Errorf("no items to add")
	}
	for _, item := range items {
		if item.Source != t.source || item.Kind != api.KindSong {
			return core.PlaybackState{}, fmt.Errorf("queue item does not belong to %s", t.source)
		}
	}
	insertAt := len(t.items)
	if position == "next" {
		insertAt = t.index + 1
	}
	next := make([]api.Item, 0, len(t.items)+len(items))
	next = append(next, t.items[:insertAt]...)
	next = append(next, items...)
	next = append(next, t.items[insertAt:]...)
	t.items = next
	if insertAt <= t.index {
		t.index += len(items)
	}
	t.revision++
	return t.sanitizeStateLocked(t.last), nil
}

// Remove drops the item at index. Removing the current item advances to the
// following one, or stops when the queue becomes empty.
func (t *URLQueueTransport) Remove(ctx context.Context, index int) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if index < 0 || index >= len(t.items) {
		return core.PlaybackState{}, errQueueIndexOutOfRange
	}
	removedCurrent := index == t.index
	t.items = append(append([]api.Item{}, t.items[:index]...), t.items[index+1:]...)
	if len(t.items) == 0 {
		if _, stopErr := t.driver.StopURL(ctx, t.generation, t.sessionID); stopErr != nil {
			return core.PlaybackState{}, stopErr
		}
		t.clearLocked()
		t.revision++
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, nil
	}
	if removedCurrent {
		if t.index >= len(t.items) {
			t.index = len(t.items) - 1
		}
		t.retried = false
		state, err := t.playCurrentLocked(ctx)
		if err != nil {
			_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
			t.clearLocked()
			t.revision++
			return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, err
		}
		if t.paused {
			pausedState, pauseErr := t.driver.PauseURL(ctx, t.generation, t.sessionID)
			if pauseErr != nil {
				return core.PlaybackState{}, pauseErr
			}
			t.last = t.sanitizeStateLocked(pausedState)
			state = t.last
		}
		t.revision++
		return state, nil
	}
	if index < t.index {
		t.index--
	}
	t.revision++
	return t.sanitizeStateLocked(t.last), nil
}

// Move reorders the queue and keeps the current track current.
func (t *URLQueueTransport) Move(_ context.Context, from, to int) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if from < 0 || from >= len(t.items) || to < 0 || to >= len(t.items) {
		return core.PlaybackState{}, errQueueIndexOutOfRange
	}
	if from == to {
		return t.sanitizeStateLocked(t.last), nil
	}
	isCurrent := from == t.index
	cur := t.index
	if !isCurrent && cur > from {
		cur--
	}
	item := t.items[from]
	rest := make([]api.Item, 0, len(t.items)-1)
	rest = append(rest, t.items[:from]...)
	rest = append(rest, t.items[from+1:]...)
	if to > len(rest) {
		to = len(rest)
	}
	t.items = append(append(append([]api.Item{}, rest[:to]...), item), rest[to:]...)
	if isCurrent {
		cur = to
	} else if to <= cur {
		cur++
	}
	t.index = cur
	t.revision++
	return t.sanitizeStateLocked(t.last), nil
}

// RetryCurrent re-resolves and replays the current item exactly once after a
// media failure (expired/403 signed URL). It ends the session on second failure.
func (t *URLQueueTransport) RetryCurrent(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if t.retried {
		_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
		t.clearLocked()
		t.revision++
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, fmt.Errorf("media URL failed again after re-resolution")
	}
	t.retried = true
	state, err := t.playCurrentLocked(ctx)
	if err != nil {
		_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
		t.clearLocked()
		t.revision++
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, err
	}
	return state, nil
}

func (t *URLQueueTransport) Stop(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		// Stop is idempotent: with no session it is already stopped.
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, nil
	}
	state, err := t.driver.StopURL(ctx, t.generation, t.sessionID)
	if err != nil {
		return core.PlaybackState{}, err
	}
	t.clearLocked()
	t.revision++
	state.Track = nil
	state.Queue = nil
	state.QueueIndex = -1
	state.Status = "stopped"
	state.Mode = "none"
	return state, nil
}

func (t *URLQueueTransport) State(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, nil
	}
	state, err := t.driver.StateURL(ctx, t.generation, t.sessionID)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return t.sanitizeStateLocked(state), nil
}

// Snapshot overlays the server-owned queue onto a helper state without calling
// the driver. The watcher uses it so watch events carry the finite queue too.
func (t *URLQueueTransport) Snapshot(state core.PlaybackState) core.PlaybackState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.items) == 0 {
		return state
	}
	return t.sanitizeStateLocked(state)
}

func (t *URLQueueTransport) List() api.QueueState {
	t.mu.Lock()
	defer t.mu.Unlock()
	queue := api.QueueState{Items: cloneAPIItems(t.items), Index: t.index, QueueRevision: t.revision}
	if len(t.items) > 0 {
		source := t.source
		queue.Source = &source
	} else {
		queue.Index = -1
	}
	return queue
}

func (t *URLQueueTransport) sanitizeStateLocked(state core.PlaybackState) core.PlaybackState {
	state.Mode = "full"
	state.IsLive = false
	state.Queue = make([]core.Item, 0, len(t.items))
	for _, item := range t.items {
		state.Queue = append(state.Queue, publicCoreItem(item))
	}
	state.QueueIndex = t.index
	if t.index >= 0 && t.index < len(t.items) {
		item := publicCoreItem(t.items[t.index])
		state.Track = &item
	}
	return state
}

func (t *URLQueueTransport) requireSessionLocked() error {
	if t.driver == nil || len(t.items) == 0 || t.index < 0 || t.sessionID == "" {
		return errQueueNoSession
	}
	return nil
}

func (t *URLQueueTransport) clearLocked() {
	t.source = ""
	t.items = nil
	t.index = -1
	t.resolver = nil
	t.generation = 0
	t.sessionID = ""
	t.paused = false
}

// Reset drops any session and queue without touching the driver. The server
// uses it when the helper is rebuilt, so a dead session cannot be mistaken for
// a live one.
func (t *URLQueueTransport) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clearLocked()
	t.revision++
}

func cloneAPIItems(items []api.Item) []api.Item {
	if items == nil {
		return nil
	}
	return append([]api.Item(nil), items...)
}

func publicCoreItem(item api.Item) core.Item {
	return core.Item{Kind: item.Kind, ID: item.ProviderID, URL: item.URL, Title: item.Title, Artist: item.Artist, PreviewURL: item.PreviewURL}
}
