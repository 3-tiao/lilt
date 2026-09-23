// Package playrouter composes two playback backends behind the two interfaces
// the server already has.
//
// It exists because one source needs a different playback mechanism: Apple Music
// only plays inside a browser running Apple's own web player, while radio,
// Audius, and Jamendo keep using mpv. The server's model is "one AudioEngine plus
// one URLPlaybackDriver, one of them actually making sound". This package fills
// that shape and owns the routing,
// so nothing above it has to know there are two backends at all.
package playrouter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/appleweb"
	"github.com/caiguo/lilt/internal/server"
)

// Streams is the direct-stream side: live radio and the URL queues of every
// source that plays URLs (mpv on Linux, lilt-audio on macOS).
type Streams interface {
	server.AudioEngine
	server.URLPlaybackDriver
	Close() error
}

// Apple is the browser side.
type Apple interface {
	PlayCatalogSong(ctx context.Context, songID string) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error
	State(ctx context.Context) (appleweb.State, error)
	// Close reaps the browser process. Exiting lilt must not leave one behind.
	Close() error
}

// Backend identifies which side owns the current playback.
type Backend string

const (
	backendNone   Backend = ""
	backendStream Backend = "streams"
	backendApple  Backend = "apple"
)

// sampleInterval matches the samplers on both sides, so watchers see the same
// cadence whichever backend is playing.
const sampleInterval = time.Second

// Player routes playback and publishes one state stream to the server.
type Player struct {
	streams Streams
	apple   Apple

	mu              sync.Mutex
	owner           Backend
	lastEndedItem   string
	appleGeneration uint64
	appleSession    string
	appleEpoch      uint64
	appleStarts     bool
	sequence        uint64
	queue           []core.PlaybackStateUpdate
	closed          bool

	updates chan core.PlaybackStateUpdate
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// New builds the router. Neither backend is owned by the caller afterwards:
// Close releases the streams side, and the Apple session is shared with the
// provider that reads the catalog from the same page.
func New(streams Streams, apple Apple) *Player {
	p := &Player{
		streams: streams,
		apple:   apple,
		updates: make(chan core.PlaybackStateUpdate),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go p.dispatch()
	go p.forwardStreamUpdates()
	go p.sampleApple()
	return p
}

// --- AudioEngine (radio streams) ---------------------------------------------

func (p *Player) State(ctx context.Context) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		return p.appleState(ctx)
	}
	return p.streams.State(ctx)
}

func (p *Player) PauseState(ctx context.Context) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Pause(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		return p.appleState(ctx)
	}
	return p.streams.PauseState(ctx)
}

func (p *Player) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Resume(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		return p.appleState(ctx)
	}
	return p.streams.ResumeState(ctx)
}

// Stop halts whichever backend is playing. The server calls it both when a
// stream stops and when a transport is replaced, so it must be safe on either.
func (p *Player) Stop(ctx context.Context) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Stop(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		p.release()
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, nil
	}
	return p.streams.Stop(ctx)
}

// RadioPlay hands a live stream to the streams backend, stopping the browser first: only one
// backend may make sound.
func (p *Player) RadioPlay(ctx context.Context, url, name string) (core.PlaybackState, error) {
	if err := p.handTo(ctx, backendStream); err != nil {
		return core.PlaybackState{}, err
	}
	return p.streams.RadioPlay(ctx, url, name)
}

func (p *Player) RadioStop(ctx context.Context) (core.PlaybackState, error) {
	return p.Stop(ctx)
}

func (p *Player) Probe(ctx context.Context, url string, timeoutMs int) (core.RadioProbeResult, error) {
	return p.streams.Probe(ctx, url, timeoutMs)
}

// --- URLPlaybackDriver (finite queues) ---------------------------------------

// PlayURL routes one queue item. Apple items carry a catalog id rather than a
// media URL: the page plays Item.ProviderID, and URL only holds the item's
// stable public page because a URL queue expects one.
func (p *Player) PlayURL(ctx context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	if target.Item.Source == string(api.SourceAppleMusic) {
		if err := p.handTo(ctx, backendApple); err != nil {
			return core.PlaybackState{}, err
		}
		// core.Item.ID is the provider id, which for Apple is the catalog id the
		// page's setQueue needs.
		if target.Item.ID == "" {
			p.invalidateApple()
			return core.PlaybackState{}, fmt.Errorf("apple music queue item has no catalog id")
		}
		epoch := p.bindApple(target.PlaybackGeneration, target.TransportSessionID)
		if err := p.apple.PlayCatalogSong(ctx, target.Item.ID); err != nil {
			p.invalidateAppleEpoch(epoch)
			return core.PlaybackState{}, err
		}
		// play() is not awaited, so the page is mid-transition here: a sample
		// right now reads the old track's position against the new title, or a
		// half-reset snapshot. The queue knows the item and that nothing has
		// started yet — report buffering honestly and let the sampler publish
		// the page's real states as they settle.
		track := target.Item
		return core.PlaybackState{
			Status:             "buffering",
			Track:              &track,
			PlaybackGeneration: target.PlaybackGeneration,
			TransportSessionID: target.TransportSessionID,
		}, nil
	}
	if err := p.handTo(ctx, backendStream); err != nil {
		return core.PlaybackState{}, err
	}
	return p.streams.PlayURL(ctx, target)
}

func (p *Player) PauseURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Pause(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		return p.appleState(ctx)
	}
	return p.streams.PauseURL(ctx, generation, session)
}

func (p *Player) ResumeURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Resume(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		return p.appleState(ctx)
	}
	return p.streams.ResumeURL(ctx, generation, session)
}

func (p *Player) StopURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		if err := p.apple.Stop(ctx); err != nil {
			return core.PlaybackState{}, err
		}
		p.release()
		return core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, nil
	}
	return p.streams.StopURL(ctx, generation, session)
}

func (p *Player) StateURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if p.owns() == backendApple {
		return p.appleState(ctx)
	}
	return p.streams.StateURL(ctx, generation, session)
}

// --- ownership and state -----------------------------------------------------

// handTo moves ownership to one backend, stopping the other. Starting playback on
// a backend that does not own the session while the other is still playing would
// put two sources on the speakers at once.
func (p *Player) handTo(ctx context.Context, next Backend) error {
	p.mu.Lock()
	current := p.owner
	p.mu.Unlock()
	if current == next {
		return nil
	}
	// Ownership only moves after the previous backend actually stopped: a failed
	// stop means it is still playing, and pretending otherwise would leave the
	// router unable to control it.
	switch current {
	case backendApple:
		// Leaving the browser: stop playback, but leave the session (and with it
		// the signed-in profile) alone.
		if err := p.apple.Stop(ctx); err != nil {
			return err
		}
		p.invalidateApple()
	case backendStream:
		if _, err := p.streams.Stop(ctx); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.owner = next
	p.mu.Unlock()
	return nil
}

func (p *Player) owns() Backend {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.owner
}

func (p *Player) release() {
	p.mu.Lock()
	p.owner = backendNone
	p.appleGeneration = 0
	p.appleSession = ""
	p.appleEpoch++
	p.lastEndedItem = ""
	p.mu.Unlock()
}

// appleState reads the browser and maps it into the server's playback vocabulary.
func (p *Player) appleState(ctx context.Context) (core.PlaybackState, error) {
	p.mu.Lock()
	epoch := p.appleEpoch
	p.mu.Unlock()
	return p.appleStateFor(ctx, epoch)
}

func (p *Player) appleStateFor(ctx context.Context, epoch uint64) (core.PlaybackState, error) {
	state, err := p.apple.State(ctx)
	if err != nil {
		return core.PlaybackState{}, err
	}
	p.mu.Lock()
	if p.owner != backendApple || p.appleEpoch != epoch || p.appleGeneration == 0 || p.appleSession == "" {
		p.mu.Unlock()
		return core.PlaybackState{}, fmt.Errorf("stale Apple Music playback session")
	}
	generation, session := p.appleGeneration, p.appleSession
	out := p.mapAppleLocked(state, generation, session)
	p.mu.Unlock()
	return out, nil
}

// mapAppleLocked converts a page state into core.PlaybackState. It only fills what the
// transport does not own: status, position, duration, and the end-of-item signal
// the queue advances on.
// mapAppleLocked maps a snapshot and updates end de-duplication atomically with
// the session identity check. Callers hold p.mu.
func (p *Player) mapAppleLocked(state appleweb.State, generation uint64, session string) core.PlaybackState {
	out := core.PlaybackState{
		Status:             appleStatus(state.Status),
		Position:           state.Position,
		Duration:           state.Duration,
		Error:              state.Error,
		PlaybackGeneration: generation,
		TransportSessionID: session,
	}
	// A completed item advances the queue exactly once: MusicKit keeps reporting
	// the final state until the next item starts.
	if state.Status == "ended" || state.Status == "completed" {
		alreadyEnded := p.lastEndedItem == state.ItemID
		p.lastEndedItem = state.ItemID
		if !alreadyEnded {
			out.Ended = true
		}
	}
	if out.Status == "" {
		out.Status = "stopped"
	}
	return out
}

func (p *Player) bindApple(generation uint64, session string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appleEpoch++
	p.appleGeneration = generation
	p.appleSession = session
	p.lastEndedItem = ""
	// Every bind is a start: the page is about to switch tracks, so samples
	// that read like the old track are held back until it settles.
	p.appleStarts = true
	return p.appleEpoch
}

func (p *Player) invalidateApple() {
	p.mu.Lock()
	p.appleEpoch++
	p.appleGeneration = 0
	p.appleSession = ""
	p.lastEndedItem = ""
	if p.owner == backendApple {
		p.owner = backendNone
	}
	p.mu.Unlock()
}

func (p *Player) invalidateAppleEpoch(epoch uint64) {
	p.mu.Lock()
	if p.appleEpoch == epoch {
		p.appleEpoch++
		p.appleGeneration = 0
		p.appleSession = ""
		p.lastEndedItem = ""
		if p.owner == backendApple {
			p.owner = backendNone
		}
	}
	p.mu.Unlock()
}

// appleStatus maps MusicKit's states onto the public status vocabulary.
func appleStatus(status string) string {
	switch status {
	case "playing":
		return "playing"
	case "loading", "waiting", "stalled", "seeking":
		return "buffering"
	case "paused":
		return "paused"
	case "ended", "completed":
		return "ended"
	case "stopped", "none", "":
		return "stopped"
	default:
		return "buffering"
	}
}

// --- state stream ------------------------------------------------------------

// SubscribeState returns the router's merged stream. The server subscribes once
// per engine instance, whichever backend ends up playing.
func (p *Player) SubscribeState(context.Context) (core.StateSubscription, error) {
	return core.StateSubscription{
		Initial: core.PlaybackStateUpdate{},
		Updates: p.updates,
	}, nil
}

func (p *Player) UnsubscribeState(context.Context) error { return nil }

// forwardStreamUpdates republishes stream-backend updates while it owns the session.
func (p *Player) forwardStreamUpdates() {
	subscription, err := p.streams.SubscribeState(context.Background())
	if err != nil {
		return
	}
	for update := range subscription.Updates {
		if p.owns() != backendStream {
			continue
		}
		p.publish(update.State)
	}
}

// sampleApple publishes browser progress once a second, matching the cadence the
// streams side uses. Without it a watcher would see the first Apple state and then
// nothing until the track ended.
func (p *Player) sampleApple() {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		if p.owner != backendApple || p.appleGeneration == 0 || p.appleSession == "" {
			p.mu.Unlock()
			continue
		}
		epoch, generation, session := p.appleEpoch, p.appleGeneration, p.appleSession
		p.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		state, err := p.apple.State(ctx)
		cancel()
		if err != nil {
			if errors.Is(err, appleweb.ErrBrowserDead) {
				p.fatalApple(epoch, generation, session, err)
				return
			}
			continue
		}
		p.publishApple(state, epoch, generation, session)
	}
}

func (p *Player) publishApple(state appleweb.State, epoch, generation uint64, session string) {
	p.mu.Lock()
	if p.closed || p.owner != backendApple || p.appleEpoch != epoch || p.appleGeneration != generation || p.appleSession != session {
		p.mu.Unlock()
		return
	}
	// A track change puts the page in a transition for a beat: MusicKit has
	// swapped the title but the position/duration still read as the previous
	// song, so samples taken in that window report a track that has not
	// started at a position it never reached, or a half-reset stopped state
	// with no duration. Hold them back until the page shows a coherent start.
	// Error-bearing samples always pass: the server's stall/retry logic needs
	// them even mid-transition, or a failed start would hang in buffering.
	if p.appleStarts {
		if state.Error != "" || state.Status == "buffering" || state.Status == "paused" {
			p.appleStarts = false
		} else if state.Status == "playing" && state.Position <= 1 {
			p.appleStarts = false
		} else if state.Status == "stopped" && state.Duration == 0 {
			p.mu.Unlock()
			return
		} else if state.Status == "playing" && state.Position > 1 {
			p.mu.Unlock()
			return
		}
	}
	p.sequence++
	if len(p.queue) >= 8 {
		p.queue = p.queue[len(p.queue)-7:]
	}
	p.queue = append(p.queue, core.PlaybackStateUpdate{Sequence: p.sequence, State: p.mapAppleLocked(state, generation, session)})
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Player) fatalApple(epoch, generation uint64, session string, err error) {
	p.mu.Lock()
	if p.closed || p.owner != backendApple || p.appleEpoch != epoch {
		p.mu.Unlock()
		return
	}
	p.sequence++
	p.queue = append(p.queue, core.PlaybackStateUpdate{Sequence: p.sequence, State: core.PlaybackState{
		Status: "stopped", Mode: "none", QueueIndex: -1, Error: err.Error(), EngineFatal: true,
		PlaybackGeneration: generation, TransportSessionID: session,
	}})
	p.closed = true
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// publish enqueues a snapshot. The queue is small and coalescing: every update is
// a full snapshot, and a slow consumer must never block a backend's sampler.
func (p *Player) publish(state core.PlaybackState) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.sequence++
	if len(p.queue) >= 8 {
		p.queue = p.queue[len(p.queue)-7:]
	}
	p.queue = append(p.queue, core.PlaybackStateUpdate{Sequence: p.sequence, State: state})
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Player) dispatch() {
	defer close(p.done)
	defer close(p.updates)
	for {
		p.mu.Lock()
		if len(p.queue) > 0 {
			next := p.queue[0]
			p.queue[0] = core.PlaybackStateUpdate{}
			p.queue = p.queue[1:]
			p.mu.Unlock()
			select {
			case p.updates <- next:
				continue
			case <-p.stop:
				return
			}
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-p.wake:
		case <-p.stop:
			return
		}
	}
}

// Close releases both backends and stops publishing. The browser is reaped here
// because exiting lilt must not leave a Chromium behind; the shared session is
// restartable, so a later catalog call simply starts a new one.
func (p *Player) Close() error {
	var err error
	p.once.Do(func() {
		p.invalidateApple()
		_ = p.apple.Close()
		err = p.streams.Close()
		p.mu.Lock()
		p.closed = true
		p.queue = nil
		p.mu.Unlock()
		close(p.stop)
	})
	return err
}
