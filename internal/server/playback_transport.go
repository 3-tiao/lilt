package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

// errQueueNoSession reports a control that requires an active URL session.
var errQueueNoSession = errors.New("no active URL session")

// errQueueIndexOutOfRange reports a control request with no such queue item.
// It is a state error, not a source failure, and must not end the session.
var errQueueIndexOutOfRange = errors.New("queue index out of range")

// errQueueUndoUnavailable means a restore was rejected before changing the
// queue because its exact removal context no longer exists.
var errQueueUndoUnavailable = core.ErrQueueUndoUnavailable

// errURLRetryExhausted marks the session's single retry budget as spent: the
// media stream stalled and the re-resolved URL did not recover either. It
// carries no upstream failure — the source itself may still be fine, so
// callers map it to playback_error, not source_unavailable.
var errURLRetryExhausted = errors.New("media URL failed again after re-resolution")

// errDeadItemSkipped is returned when a dead item was skipped instead of
// ending the session: the stream stayed dead through its retry budget, a
// bounded number of further items remain, and playback now continues on the
// next queue entry. The caller commits the returned state (playback goes on)
// and warns with playback_skipped instead of treating this as a session end.
var errDeadItemSkipped = errors.New("skipped a dead item after retry; playing the next one")

// maxConsecutiveDeadSkips bounds how many dead items are skipped in a row
// before the session gives up: one dead track keeps the queue alive, while a
// whole dead run (dead CDN album, dead network) must not burn the queue while
// the UI sits frozen for a stall budget per item. Any non-skip item transition
// (natural end, user next/jump, removal) restarts the count.
const maxConsecutiveDeadSkips = 2

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

// PlaybackRequest contains stable references only. Providers may prepare one
// ref or an explicit same-source song list without exposing transport payloads.
type PlaybackRequest struct {
	References []api.Reference
	StartIndex int
	// FromHere drops entries before StartIndex so "play from here" builds a
	// forward-only queue instead of keeping earlier tracks as history.
	FromHere bool
	// ResolvedItems carries the full item data for References when the caller
	// already resolved it — the container expansion (album/playlist) fetched
	// complete songs, and re-resolving each ref through the provider would
	// repeat one page/API round trip per track. Same length and order as
	// References when set. Server-internal runtime data: never persisted,
	// never crosses the wire.
	ResolvedItems []api.Item
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
	// Mode is the public playback mode the resolution itself determined. A
	// resolver that can know the truth at start time (Apple re-reads its live
	// session per item) sets it, and the transport adopts it for the item that
	// is starting; resolvers that cannot leave it empty and the plan's mode
	// stands. A queue must never keep reporting the mode frozen at plan time —
	// sign-ins and session expiries change it mid-queue.
	Mode URLQueueMode
}

type urlResolver func(context.Context, api.Item) (urlResolution, error)

// URLQueueMode is the public PlaybackStatus.mode a URL queue reports. A finite
// full-length queue reports "full"; a queue of limited previews reports
// "preview", so a client never presents a 30-second excerpt as full playback.
type URLQueueMode string

const (
	URLQueueFull       URLQueueMode = "full"
	URLQueuePreview    URLQueueMode = "preview"
	URLQueueUnverified URLQueueMode = "unverified"
)

// URLQueuePlan carries only a stable public queue plus a lazy resolver. The
// resolver result is never written back into this plan.
type URLQueuePlan struct {
	source     api.SourceID
	queue      []api.Item
	startIndex int
	mode       URLQueueMode
	resolve    urlResolver
}

// NewURLQueuePlan builds a finite full-length queue.
func NewURLQueuePlan(source api.SourceID, queue []api.Item, startIndex int, resolve urlResolver) URLQueuePlan {
	return NewURLQueuePlanWithMode(source, queue, startIndex, URLQueueFull, resolve)
}

// NewURLQueuePlanWithMode builds a queue whose public mode the caller decided.
// The mode is part of what the plan means, not an option: a preview queue that
// reported "full" would lie to the client about what is playing. Callers that
// cannot know the mode up front (a signed-in Apple session turns previews into
// full tracks) pass it explicitly.
func NewURLQueuePlanWithMode(source api.SourceID, queue []api.Item, startIndex int, mode URLQueueMode, resolve urlResolver) URLQueuePlan {
	return URLQueuePlan{source: source, queue: cloneAPIItems(queue), startIndex: startIndex, mode: mode, resolve: resolve}
}

func (p URLQueuePlan) Source() api.SourceID    { return p.source }
func (p URLQueuePlan) Transport() TransportID  { return transportURLQueue }
func (p URLQueuePlan) PublicQueue() []api.Item { return cloneAPIItems(p.queue) }
func (p URLQueuePlan) StartIndex() int         { return p.startIndex }
func (p URLQueuePlan) Mode() URLQueueMode      { return p.mode }
func (p URLQueuePlan) resolveURL(ctx context.Context, item api.Item) (urlResolution, error) {
	if p.resolve == nil {
		return urlResolution{}, fmt.Errorf("URL queue plan has no resolver")
	}
	return p.resolve(ctx, item)
}

type urlQueuePrepared interface {
	PreparedPlayback
	Mode() URLQueueMode
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
	mu               sync.Mutex
	driver           URLPlaybackDriver
	source           api.SourceID
	items            []api.Item
	index            int
	revision         uint64
	resolver         urlResolver
	mode             URLQueueMode
	generation       uint64
	sessionID        string
	paused           bool
	retried          bool
	deadSkips        int
	last             core.PlaybackState
	expectedDuration int  // catalog seconds for the current Apple browser item
	verifyMedia      bool // current item was authorized but its media length is not assumed
	mediaStarted     bool // the driver's initial state may still describe the previous item
}

// URLQueueUndo retains the exact stable item removed from a URL queue and the
// transport-local guards needed to restore it under the same lock. It is
// server-private and never contains a resolved media URL.
type URLQueueUndo struct {
	item         api.Item
	index        int
	currentIndex int
	revision     uint64
	source       api.SourceID
	generation   uint64
	sessionID    string
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
	// A plan that does not declare a supported mode fails loudly: defaulting to
	// "full" here is exactly how a preview queue would end up lying.
	if mode := plan.Mode(); mode != URLQueueFull && mode != URLQueuePreview && !(mode == URLQueueUnverified && prepared.Source() == api.SourceAppleMusic) {
		return core.PlaybackState{}, fmt.Errorf("url queue plan declares an unsupported mode %q", plan.Mode())
	}
	t.clearLocked()
	t.revision++
	t.source = prepared.Source()
	t.items = cloneAPIItems(items)
	t.index = index
	t.mode = plan.Mode()
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
	// Landing on an item by any path other than a dead-item skip restarts the
	// consecutive-skip budget: a natural end, a user jump, or a removal all
	// mean the queue is being driven, not burned.
	t.retried = false
	t.deadSkips = 0
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
	// A resolver may settle just as its budget expires. Never start media
	// from that late result, even if it returned a URL without an error.
	if err := ctx.Err(); err != nil {
		return core.PlaybackState{}, err
	}
	if resolved.URL == "" {
		return core.PlaybackState{}, fmt.Errorf("URL resolver returned an empty URL")
	}
	// A resolution that re-determined the mode at start time wins over the mode
	// frozen into the plan: the queue outlives sign-ins and session expiries,
	// and each item reports what it actually is.
	switch resolved.Mode {
	case "":
	case URLQueueFull, URLQueuePreview, URLQueueUnverified:
		t.mode = resolved.Mode
	default:
		return core.PlaybackState{}, fmt.Errorf("URL resolver declared an unsupported mode %q", resolved.Mode)
	}
	t.verifyMedia = resolved.Mode == URLQueueUnverified
	t.expectedDuration = 0
	if t.verifyMedia && resolved.Duration > 0 {
		t.expectedDuration = resolved.Duration
	}
	t.mediaStarted = false
	state, err := t.driver.PlayURL(ctx, URLPlaybackTarget{Item: publicCoreItem(item), URL: resolved.URL, ArtworkURL: resolved.ArtworkURL, Duration: resolved.Duration, PlaybackGeneration: t.generation, TransportSessionID: t.sessionID})
	if err != nil {
		return core.PlaybackState{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.PlaybackState{}, err
	}
	// Even a driver that reports playing immediately may still be exposing the
	// previous page item here. Only subsequent snapshots can verify its length.
	t.last = t.sanitizeStateLocked(state)
	t.mediaStarted = true
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
func (t *URLQueueTransport) Remove(ctx context.Context, index int) (core.QueueRemoveOutcome, *URLQueueUndo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.QueueRemoveOutcome{}, nil, err
	}
	if index < 0 || index >= len(t.items) {
		return core.QueueRemoveOutcome{}, nil, errQueueIndexOutOfRange
	}
	removed := t.items[index]
	beforeCurrent := t.index
	wasFuture := index > beforeCurrent && removed.Kind == api.KindSong
	removedCurrent := index == t.index
	t.items = append(append([]api.Item{}, t.items[:index]...), t.items[index+1:]...)
	if len(t.items) == 0 {
		if _, stopErr := t.driver.StopURL(ctx, t.generation, t.sessionID); stopErr != nil {
			return core.QueueRemoveOutcome{}, nil, stopErr
		}
		t.clearLocked()
		t.revision++
		return core.QueueRemoveOutcome{State: core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}}, nil, nil
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
			return core.QueueRemoveOutcome{State: core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}}, nil, err
		}
		if t.paused {
			pausedState, pauseErr := t.driver.PauseURL(ctx, t.generation, t.sessionID)
			if pauseErr != nil {
				return core.QueueRemoveOutcome{}, nil, pauseErr
			}
			t.last = t.sanitizeStateLocked(pausedState)
			state = t.last
		}
		t.revision++
		return core.QueueRemoveOutcome{State: state}, nil, nil
	}
	if index < t.index {
		t.index--
	}
	t.revision++
	var undo *URLQueueUndo
	if wasFuture {
		undo = &URLQueueUndo{item: removed, index: index, currentIndex: beforeCurrent, revision: t.revision, source: t.source, generation: t.generation, sessionID: t.sessionID}
	}
	return core.QueueRemoveOutcome{State: t.sanitizeStateLocked(t.last)}, undo, nil
}

// RestoreRemoved inserts the exact stable item at its original canonical index.
// All validation and the insertion happen under the transport lock; no media is
// resolved and playback is not restarted.
func (t *URLQueueTransport) RestoreRemoved(_ context.Context, undo URLQueueUndo) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, errQueueUndoUnavailable
	}
	if t.source != undo.source || t.generation != undo.generation || t.sessionID != undo.sessionID ||
		t.revision != undo.revision || t.index != undo.currentIndex || undo.index <= t.index ||
		undo.index < 0 || undo.index > len(t.items) {
		return core.PlaybackState{}, errQueueUndoUnavailable
	}
	next := make([]api.Item, 0, len(t.items)+1)
	next = append(next, t.items[:undo.index]...)
	next = append(next, undo.item)
	next = append(next, t.items[undo.index:]...)
	t.items = next
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
// media failure (expired/403 signed URL, dead stream). If the re-resolved
// stream stays dead too, a bounded number of further items are skipped with
// errDeadItemSkipped so one dead track does not end the session; running past
// the skip budget, hitting the last item, or failing to start the next item
// all end the session as before.
func (t *URLQueueTransport) RetryCurrent(ctx context.Context) (core.PlaybackState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.requireSessionLocked(); err != nil {
		return core.PlaybackState{}, err
	}
	if t.retried {
		deadTitle := t.items[t.index].Title
		if next := t.index + 1; next < len(t.items) && t.deadSkips < maxConsecutiveDeadSkips {
			skipped := t.deadSkips + 1
			// playIndexLocked restarts the budget for the skipped-to item;
			// restore the running count so "consecutive" survives the move.
			state, err := t.playIndexLocked(ctx, next)
			if err != nil {
				// The skip target itself failed to start: a resolve or helper
				// failure is systemic, not one dead item. End the session and
				// surface the real error (the caller maps it to
				// source_unavailable).
				_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
				t.clearLocked()
				t.revision++
				return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, err
			}
			t.deadSkips = skipped
			if deadTitle != "" {
				return state, fmt.Errorf("%w: %q", errDeadItemSkipped, deadTitle)
			}
			return state, errDeadItemSkipped
		}
		_, _ = t.driver.StopURL(ctx, t.generation, t.sessionID)
		t.clearLocked()
		t.revision++
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, errURLRetryExhausted
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
	if t.verifyMedia && t.mediaStarted && t.expectedDuration > 0 &&
		state.Status == "playing" && state.Duration > 0 {
		// A 90s asset cannot be full against a 204s catalog track. Limit
		// tolerance to 5% (at most 10s) so short tracks cannot pass merely
		// because the absolute tolerance exceeds the entire track length.
		// Re-evaluate so intermediate durations cannot freeze a false verdict.
		tolerance := float64(t.expectedDuration) / 20
		if tolerance > 10 {
			tolerance = 10
		}
		if state.Duration >= float64(t.expectedDuration)-tolerance {
			t.mode = URLQueueFull
		} else {
			t.mode = URLQueuePreview
		}
	}
	state.Mode = string(t.mode)
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
	t.mode = ""
	t.resolver = nil
	t.generation = 0
	t.sessionID = ""
	t.paused = false
	t.deadSkips = 0
	t.expectedDuration = 0
	t.verifyMedia = false
	t.mediaStarted = false
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
	// Source travels with the item: the playback driver dispatches on it (Apple
	// plays in the browser, everything else in mpv), and dropping it here made
	// every queued item look source-less.
	return core.Item{Source: string(item.Source), Kind: item.Kind, ID: item.ProviderID, URL: item.URL, Title: item.Title, Artist: item.Artist, PreviewURL: item.PreviewURL}
}
