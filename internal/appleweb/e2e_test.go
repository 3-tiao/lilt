package appleweb

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRealAppleMusicFullPlaybackE2E is the only test that can prove the point of
// this package: a real Chromium, a real Apple session, a page aligned to the
// account's own storefront, and a full-length DRM track that actually
// advances. Everything else is hermetic and drives a fake peer.
//
// Opt-in because it needs a browser with Widevine and a profile the user has
// signed in once (see docs/internals/apple-web-engine.md).
func TestRealAppleMusicFullPlaybackE2E(t *testing.T) {
	if os.Getenv("LILT_APPLE_E2E") != "1" {
		t.Skip("set LILT_APPLE_E2E=1 to run against a signed-in Apple Music profile")
	}
	if err := Available(); err != nil {
		t.Skipf("no supported Chromium is discoverable: %v", err)
	}
	profile := DefaultProfileDir()
	if _, err := os.Stat(profile); err != nil {
		t.Skipf("no Apple Music browser profile at %s; run `LILT_APPLE_ENGINE=browser lilt auth apple-music` first", profile)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// The engine is the product path: starting the session settles MusicKit
	// readiness, storefront alignment, and the Widevine probe before any
	// catalog or playback call happens.
	engine := NewEngine(Options{
		ProfileDir: profile,
		Headless:   true,
		Stderr:     os.Stderr,
	})
	defer func() { _ = engine.Close() }()

	authorized, err := engine.Authorized(ctx)
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !authorized {
		t.Skip("the browser profile is not signed in to Apple Music; run `LILT_APPLE_ENGINE=browser lilt auth apple-music` first")
	}

	// The page must now sit on the account's own storefront — that is what
	// this E2E guards. A page left on the launch region finds the catalog in
	// a region the account cannot play whole tracks from, and the duration
	// assertion below would report exactly that as a preview.
	browser, err := engine.session(ctx)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	probe, err := browser.storefronts(ctx)
	if err != nil {
		t.Fatalf("storefronts: %v", err)
	}
	if probe.Page != probe.Account {
		t.Fatalf("page storefront = %q, account storefront = %q; the page did not follow the account", probe.Page, probe.Account)
	}
	t.Logf("storefront: account=%s page=%s", probe.Account, probe.Page)

	// Ask the page's own storefront, so the id exists where the player is.
	songs, err := engine.SearchSongs(ctx, "Bill Evans", 1)
	if err != nil {
		t.Fatalf("SearchSongs: %v", err)
	}
	if len(songs) == 0 {
		t.Skip("the account's storefront returned no catalog result for the probe term")
	}
	song := songs[0]
	if err := engine.PlayCatalogSong(ctx, song.ID); err != nil {
		t.Fatalf("PlayCatalogSong: %v", err)
	}

	started := waitForPlaying(t, ctx, engine)
	// Full playback means the media matches the catalog's own duration. A
	// web preview is 90 seconds, so a bare "longer than 60" cannot tell the
	// two apart — a signed-in account without full rights in this storefront
	// plays exactly such a 90s excerpt (e.g. a CN subscription on a US page).
	// The comparison number comes from the media itself.
	if song.DurationMs > 0 && started.Duration < float64(song.DurationMs)/1000-10 {
		t.Fatalf("duration = %.0fs, want the catalog's %.0fs — the page played a preview: the signed-in account has no full playback rights in this storefront (subscription region ≠ page storefront?)",
			started.Duration, float64(song.DurationMs)/1000)
	}
	if started.ItemID != song.ID {
		t.Fatalf("playing item = %q, want the queued song %q", started.ItemID, song.ID)
	}

	// The position must advance, which is what a decoded DRM stream proves.
	before := started.Position
	time.Sleep(6 * time.Second)
	after, err := engine.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if after.Position <= before {
		t.Fatalf("position did not advance: %.1f -> %.1f (status %q, error %q)", before, after.Position, after.Status, after.Error)
	}

	if err := engine.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused := waitForStatus(t, ctx, engine, "paused")
	if paused.IsPlaying {
		t.Fatalf("a paused player still reports IsPlaying: %+v", paused)
	}
}

func waitForPlaying(t *testing.T, ctx context.Context, engine *Engine) State {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last State
	for time.Now().Before(deadline) {
		state, err := engine.State(ctx)
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

func waitForStatus(t *testing.T, ctx context.Context, engine *Engine, want string) State {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last State
	for time.Now().Before(deadline) {
		state, err := engine.State(ctx)
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
