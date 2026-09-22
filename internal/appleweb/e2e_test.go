package appleweb

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestRealAppleMusicFullPlaybackE2E is the only test that can prove the point of
// this package: a real Chromium, a real Apple session, and a full-length DRM
// track that actually advances. Everything else is hermetic and drives a fake
// peer.
//
// Opt-in because it needs a browser with Widevine and a profile the user has
// signed in once (see docs/internals/apple-web-engine.md).
func TestRealAppleMusicFullPlaybackE2E(t *testing.T) {
	if os.Getenv("LILT_APPLE_E2E") != "1" {
		t.Skip("set LILT_APPLE_E2E=1 to run against a signed-in Apple Music profile (requires LILT_CHROMIUM_PATH and LILT_APPLE_PROFILE)")
	}
	if os.Getenv("LILT_CHROMIUM_PATH") == "" {
		t.Skip("set LILT_CHROMIUM_PATH to a chromium built with Widevine")
	}
	profile := os.Getenv("LILT_APPLE_PROFILE")
	if profile == "" {
		t.Skip("set LILT_APPLE_PROFILE to a profile that has signed in to Apple Music once")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	browser, err := Start(ctx, Options{
		ProfileDir:   profile,
		ChromiumPath: os.Getenv("LILT_CHROMIUM_PATH"),
		Headless:     true,
		Stderr:       os.Stderr,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = browser.Close() }()

	if err := browser.WaitMusicKit(ctx); err != nil {
		t.Fatalf("WaitMusicKit: %v", err)
	}
	authorized, err := browser.Authorized(ctx)
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !authorized {
		t.Fatalf("the profile has no Apple Music session; sign in once with --restore-last-session first")
	}

	// Ask the page's own storefront, so the id exists where the player is.
	songID := searchFirstSong(t, ctx, browser, "Bill Evans")
	if songID == "" {
		t.Skip("the account's storefront returned no catalog result for the probe term")
	}
	if err := browser.PlayCatalogSong(ctx, songID); err != nil {
		t.Fatalf("PlayCatalogSong: %v", err)
	}

	started := waitForPlaying(t, ctx, browser)
	// A full track, not a 30-second preview: this is the whole difference between
	// "signed in" and "not signed in", and the number comes from the media itself.
	if started.Duration <= 60 {
		t.Fatalf("duration = %.0fs, want a full-length track (a preview is ~30s)", started.Duration)
	}
	if started.ItemID != songID {
		t.Fatalf("playing item = %q, want the queued song %q", started.ItemID, songID)
	}

	// The position must advance, which is what a decoded DRM stream proves.
	before := started.Position
	time.Sleep(6 * time.Second)
	after, err := browser.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if after.Position <= before {
		t.Fatalf("position did not advance: %.1f -> %.1f (status %q, error %q)", before, after.Position, after.Status, after.Error)
	}

	if err := browser.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused := waitForStatus(t, ctx, browser, "paused")
	if paused.IsPlaying {
		t.Fatalf("a paused player still reports IsPlaying: %+v", paused)
	}
}

func searchFirstSong(t *testing.T, ctx context.Context, browser *Browser, term string) string {
	t.Helper()
	expression := `(async () => {
	  const mk = window.MusicKit.getInstance();
	  const sf = mk.storefrontId || 'us';
	  const r = await mk.api.music('/v1/catalog/' + sf + '/search', { term: ` + strconv.Quote(term) + `, types: 'songs', limit: 1 });
	  const songs = r && r.data && r.data.results && r.data.results.songs;
	  if (!songs || !songs.data || !songs.data.length) return '';
	  return String(songs.data[0].id);
	})()`
	id, err := browser.Evaluate(ctx, expression)
	if err != nil {
		t.Fatalf("catalog search: %v", err)
	}
	return id
}

func waitForPlaying(t *testing.T, ctx context.Context, browser *Browser) State {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last State
	for time.Now().Before(deadline) {
		state, err := browser.State(ctx)
		if err == nil {
			last = state
			if state.Error != "" {
				t.Fatalf("playback reported an error: %s", state.Error)
			}
			if state.Status == "playing" && state.Position > 0 {
				return state
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("never reached playing: %+v", last)
	return State{}
}

func waitForStatus(t *testing.T, ctx context.Context, browser *Browser, want string) State {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last State
	for time.Now().Before(deadline) {
		state, err := browser.State(ctx)
		if err == nil {
			last = state
			if state.Status == want {
				return state
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("never reached %q: %+v", want, last)
	return State{}
}
