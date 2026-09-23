package appleweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// PlaybackStates as MusicKit JS defines them.
const (
	statusNone      = 0
	statusLoading   = 1
	statusPlaying   = 2
	statusPaused    = 3
	statusStopped   = 4
	statusEnded     = 5
	statusSeeking   = 6
	statusWaiting   = 8
	statusStalled   = 9
	statusCompleted = 10
)

// State is the playback snapshot read back from the page. Duration comes from
// the page, so a 30-second preview and a full track are told apart by the media
// itself rather than by a guess here.
type State struct {
	Ready       bool
	Authorized  bool
	Status      string
	IsPlaying   bool
	Position    float64
	Duration    float64
	ItemID      string
	ItemTitle   string
	QueueLength int
	// Error carries MusicKit's playbackError or a rejected play() call. Both
	// would otherwise be silent.
	Error string
}

// Live reports whether audio is progressing.
func (s State) Live() bool {
	return s.Status == "playing" || s.Status == "loading" || s.Status == "waiting" || s.Status == "stalled" || s.Status == "seeking"
}

// stateProbe reads everything the server needs in one round trip.
const stateProbe = `(() => {
  try {
    const mk = window.MusicKit && window.MusicKit.getInstance();
    if (!mk) return JSON.stringify({ready: false});
    if (typeof (mk.api && mk.api.music) !== 'function') return JSON.stringify({ready: false});
    const item = mk.nowPlayingItem;
    return JSON.stringify({
      ready: true,
      authorized: mk.isAuthorized === true,
      state: typeof mk.playbackState === 'number' ? mk.playbackState : 0,
      isPlaying: mk.isPlaying === true,
      position: Number(mk.currentPlaybackTime) || 0,
      duration: Number(mk.currentPlaybackDuration) || 0,
      itemID: item ? String(item.id) : '',
      itemTitle: item && item.attributes && item.attributes.name ? String(item.attributes.name) : '',
      queueLength: mk.queue ? mk.queue.length : 0,
      error: (window.__liltPlayError || (mk.playbackError ? String(mk.playbackError) : '')) || '',
    });
  } catch (e) { return JSON.stringify({ready: false, failure: String(e && e.message)}); }
})()`

type probePayload struct {
	Ready       bool    `json:"ready"`
	Authorized  bool    `json:"authorized"`
	State       int     `json:"state"`
	IsPlaying   bool    `json:"isPlaying"`
	Position    float64 `json:"position"`
	Duration    float64 `json:"duration"`
	ItemID      string  `json:"itemID"`
	ItemTitle   string  `json:"itemTitle"`
	QueueLength int     `json:"queueLength"`
	Error       string  `json:"error"`
	Failure     string  `json:"failure"`
}

// WaitMusicKit blocks until the page has a configured MusicKit instance. The web
// player creates and configures it after the app boots, which takes seconds.
func (b *Browser) WaitMusicKit(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := b.State(ctx)
		if err == nil && state.Ready {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if last != nil {
		return fmt.Errorf("appleweb: MusicKit never became ready: %w", last)
	}
	return fmt.Errorf("appleweb: MusicKit never became ready")
}

// State reads the current playback snapshot.
func (b *Browser) State(ctx context.Context) (State, error) {
	raw, err := b.Evaluate(ctx, stateProbe)
	if err != nil {
		return State{}, err
	}
	var payload probePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return State{}, fmt.Errorf("appleweb: unreadable page state %q", sanitizeUpstreamMessage(raw))
	}
	if !payload.Ready {
		return State{Error: sanitizeUpstreamMessage(payload.Failure)}, nil
	}
	return State{
		Ready:       true,
		Authorized:  payload.Authorized,
		Status:      statusName(payload.State),
		IsPlaying:   payload.IsPlaying,
		Position:    payload.Position,
		Duration:    payload.Duration,
		ItemID:      payload.ItemID,
		ItemTitle:   payload.ItemTitle,
		QueueLength: payload.QueueLength,
		// The page's playbackError is upstream text: signed media URLs and
		// diagnostics may be embedded in it, so it is sanitized here — the one
		// place it crosses into lilt. Everything downstream (public state,
		// watch events, journal lines) then carries only the safe remainder.
		Error: sanitizeUpstreamMessage(payload.Error),
	}, nil
}

// Authorized reports whether the profile carries a usable Apple Music session.
func (b *Browser) Authorized(ctx context.Context) (bool, error) {
	state, err := b.State(ctx)
	if err != nil {
		return false, err
	}
	return state.Authorized, nil
}

// PlayCatalogSong assigns one catalog song as the queue and starts it.
//
// play() is deliberately not awaited: it stays pending until the media pipeline
// is up, and a player command must not block on that. A rejection is recorded in
// the page instead, where State picks it up.
func (b *Browser) PlayCatalogSong(ctx context.Context, songID string) error {
	if songID == "" {
		return fmt.Errorf("appleweb: catalog song id is empty")
	}
	return b.runCommand(ctx, fmt.Sprintf(`(async () => {
  const mk = window.MusicKit.getInstance();
  window.__liltPlayError = '';
  await mk.setQueue({ song: %s });
  mk.play().then(() => {}, (e) => { window.__liltPlayError = String((e && e.message) || e); });
  return 'queued';
})()`, strconv.Quote(songID)))
}

// Pause suspends playback.
func (b *Browser) Pause(ctx context.Context) error {
	return b.runCommand(ctx, `(async () => { await window.MusicKit.getInstance().pause(); return 'paused'; })()`)
}

// Resume continues playback.
func (b *Browser) Resume(ctx context.Context) error {
	return b.runCommand(ctx, `(async () => { const mk = window.MusicKit.getInstance(); window.__liltPlayError = ''; mk.play().then(() => {}, (e) => { window.__liltPlayError = String((e && e.message) || e); }); return 'playing'; })()`)
}

// Stop halts playback and leaves the queue in place.
func (b *Browser) Stop(ctx context.Context) error {
	return b.runCommand(ctx, `(async () => { await window.MusicKit.getInstance().stop(); return 'stopped'; })()`)
}

// runCommand evaluates a command expression and rejects anything that does not
// look like the command's own return value, so a silent no-op cannot pass as a
// success.
func (b *Browser) runCommand(ctx context.Context, expression string) error {
	value, err := b.Evaluate(ctx, expression)
	if err != nil {
		return err
	}
	if value == "" || value == "null" || value == "undefined" {
		return fmt.Errorf("appleweb: the page did not confirm the command")
	}
	return nil
}

func statusName(code int) string {
	switch code {
	case statusNone:
		return "none"
	case statusLoading:
		return "loading"
	case statusPlaying:
		return "playing"
	case statusPaused:
		return "paused"
	case statusStopped:
		return "stopped"
	case statusEnded:
		return "ended"
	case statusSeeking:
		return "seeking"
	case statusWaiting:
		return "waiting"
	case statusStalled:
		return "stalled"
	case statusCompleted:
		return "completed"
	default:
		return "none"
	}
}
