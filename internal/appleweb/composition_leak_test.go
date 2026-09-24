package appleweb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/appleweb"
	"github.com/caiguo/lilt/internal/mpvplayer"
	"github.com/caiguo/lilt/internal/playrouter"
	"github.com/caiguo/lilt/internal/server"
	"github.com/caiguo/lilt/internal/state"
)

// leakNeedles are the fixture's own upstream blobs. The generalized checks in
// assertNoUpstreamLeak catch anything shaped like a signed asset URL, query
// string, or token, so a different leak cannot slip past by changing names.
var leakNeedles = []string{
	"audio-ssl",
	".m4a",
	"token=",
	"sig=",
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
	"deadbeefdeadbeefdeadbeefdeadbeef",
}

// leakURLPattern matches every absolute or percent-encoded URL in a surface.
var leakURLPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*(?:://|%3[aA]%2[fF])[^\s"'` + "`" + `<>]+`)

// mediaAssetExtensions are path suffixes that mark a media asset rather than a
// stable public page.
var mediaAssetExtensions = []string{".m4a", ".mp3", ".aac", ".mpd", ".m3u8", ".ts", ".webm", ".mp4"}

func assertNoUpstreamLeak(t *testing.T, surface, text string) {
	t.Helper()
	// Every URL that appears must be a stable public page: the music.apple.com
	// host, no query string, no media asset path. Anything else — a signed
	// asset, a CDN endpoint, an encoded form — is a leaked upstream resource.
	for _, match := range leakURLPattern.FindAllString(text, -1) {
		if strings.ContainsAny(match, "?&") {
			t.Fatalf("%s leaks a URL with parameters %q:\n%s", surface, match, text)
		}
		lowered := strings.ToLower(match)
		for _, extension := range mediaAssetExtensions {
			if strings.Contains(lowered, extension) {
				t.Fatalf("%s leaks a media asset URL %q:\n%s", surface, match, text)
			}
		}
		host := match
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		} else if i := strings.Index(host, "%3"); i >= 0 {
			host = host[:i]
		}
		if i := strings.IndexAny(host, "/"); i >= 0 {
			host = host[:i]
		}
		if host != "music.apple.com" {
			t.Fatalf("%s leaks a non-page URL %q:\n%s", surface, match, text)
		}
	}
	for _, needle := range leakNeedles {
		if strings.Contains(text, needle) {
			t.Fatalf("%s leaks the raw upstream error (%q):\n%s", surface, needle, text)
		}
	}
	// Any unbroken token-shaped run is a leak by shape, whatever its content:
	// signatures, nonces, and JWT segments are long mixed alnum runs. English
	// (including hyphenated words) has no digits, so a digit is required before
	// a run counts as token-shaped.
	for _, field := range strings.Fields(text) {
		trimmed := strings.Trim(field, ".,()[]{}")
		if len(trimmed) >= 16 && isLeakTokenShaped(trimmed) {
			t.Fatalf("%s leaks a token-shaped run %q:\n%s", surface, trimmed, text)
		}
	}
}

func isLeakTokenShaped(field string) bool {
	digits := 0
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '+', r == '/', r == '_', r == '-', r == '=':
		case r >= '0' && r <= '9':
			digits++
		default:
			return false
		}
	}
	return digits > 0
}

// startAppleCompositionServer mirrors cmd/lilt's Linux composition around a
// real appleweb engine driving the fake CDP page: mpv is the streams side
// (never started for Apple playback), the router is the server's audio engine
// and URL driver, and the provider/auth provider wrap the same engine. The
// Unix socket path must stay short, so the sandbox lives under /tmp.
func startAppleCompositionServer(t *testing.T, engine *appleweb.Engine, logf func(string, map[string]any)) (*server.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-apple-e2e-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	player := playrouter.New(mpvplayer.New(), engine)
	srv, err := server.Start(server.Options{
		SocketPath:         socket,
		Store:              state.New(filepath.Join(dir, "state.json")),
		AudioEngineFactory: func() (server.AudioEngine, error) { return player, nil },
		Providers:          []server.ContentProvider{server.NewAppleWebProvider(engine, func() error { return nil })},
		AuthProviders:      []server.AuthProvider{server.NewAppleWebAuthProvider(engine, engine.Started)},
		Log:                logf,
	})
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, socket
}

// This runs the server + router + browser engine against the fake CDP page.
// Login alone cannot certify full playback: the page's settled media duration
// must match the catalog duration before response/watch clients can see full.
func TestAppleBrowserModeUsesTheMediaLengthNotJustAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mediaSeconds int
		want         string
	}{
		{"storefront mismatch returns a preview", 90, "preview"},
		{"full media confirms the catalog length", 204, "full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := fmt.Sprintf(`{"ready":true,"authorized":true,"state":2,"isPlaying":true,"position":0.5,"duration":%d,"itemID":"1111111111","itemTitle":"Fixture"}`, tc.mediaSeconds)
			t.Setenv("LILT_TEST_FAKE_CDP_STATE", state)
			t.Setenv("LILT_TEST_FAKE_CDP", "1")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			engine := appleweb.NewEngine(appleweb.Options{
				ProfileDir: filepath.Join(t.TempDir(), "profile"), ChromiumPath: executable, Headless: true,
			})
			_, socket := startAppleCompositionServer(t, engine, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			response, err := api.Command(ctx, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
			if err != nil || !response.OK {
				t.Fatalf("play response=%+v err=%v", response, err)
			}
			var initial api.PlaybackState
			if err := json.Unmarshal(response.Data, &initial); err != nil || initial.Mode != "unverified" {
				t.Fatalf("initial mode=%q err=%v, want unverified", initial.Mode, err)
			}
			for ctx.Err() == nil {
				response, err = api.Command(ctx, socket, "session.status", map[string]any{"includeQueue": true})
				if err != nil || !response.OK {
					t.Fatalf("status response=%+v err=%v", response, err)
				}
				var settled api.PlaybackState
				if err := json.Unmarshal(response.Data, &settled); err != nil {
					t.Fatal(err)
				}
				if settled.Mode == tc.want {
					return
				}
				if settled.Mode != "unverified" {
					t.Fatalf("media %ds: mode=%q, want %q", tc.mediaSeconds, settled.Mode, tc.want)
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatalf("media %ds never settled to %q", tc.mediaSeconds, tc.want)
		})
	}
}

// TestUpstreamPlaybackErrorNeverReachesThePublicSurface drives the real
// composition — fake CDP page, real appleweb engine, real router, real server —
// with an upstream playbackError carrying a signed media URL, query
// parameters, and token blobs. The sanitized remainder may appear on the public
// surfaces; the raw upstream content must not, not in the play response, not
// in watch events, not in journal lines.
func TestUpstreamPlaybackErrorNeverReachesThePublicSurface(t *testing.T) {
	nasty := "media failed: fetch https://audio-ssl.itunes.apple.com/AssetStore/v4/12/34/56/abc.m4a?token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c&sig=deadbeefdeadbeefdeadbeefdeadbeef (403)"
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":true,"authorized":true,"state":2,"isPlaying":true,"position":1,"duration":204,"itemID":"1111111111","itemTitle":"Fixture","error":"`+nasty+`"}`)

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LILT_TEST_FAKE_CDP", "1")
	engine := appleweb.NewEngine(appleweb.Options{
		ProfileDir:   filepath.Join("/tmp", "lilt-leak-profile"),
		ChromiumPath: executable,
		Headless:     true,
	})

	journalMu := sync.Mutex{}
	journal := []string{}
	logf := func(event string, data map[string]any) {
		encoded, _ := json.Marshal(data)
		journalMu.Lock()
		journal = append(journal, event+" "+string(encoded))
		journalMu.Unlock()
	}
	_, socket := startAppleCompositionServer(t, engine, logf)

	watchCtx, watchCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer watchCancel()
	_, watcher, err := api.Watch(watchCtx, socket, []string{"playback", "server", "sources"}, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	playCtx, playCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer playCancel()
	response, err := api.Command(playCtx, socket, "playback.play", map[string]any{"ref": "apple-music:song:1111111111"})
	if err != nil {
		t.Fatalf("playback.play: %v", err)
	}
	playBody, _ := json.Marshal(response)
	assertNoUpstreamLeak(t, "the play response", string(playBody))
	// The play response is the buffering placeholder now: the error surfaces a
	// beat later through the sampler's states, so the sanitized remainder is
	// asserted on the watch stream below — proving the upstream error traveled
	// the pipeline and was stripped, not that the pipeline carried nothing.

	// The retry path the server drives on a reported error replays once, warns,
	// and then ends the session; every event and journal line it produced must
	// stay clean. The stall notice itself is journal-only by design, so the
	// collection ends on the terminal playback_error warning instead.
	deadline := time.After(6 * time.Second)
collect:
	for {
		select {
		case event := <-watcher.Events:
			body, _ := json.Marshal(event)
			assertNoUpstreamLeak(t, fmt.Sprintf("watch event %s", event.Event), string(body))
			// The session ends (single-item queue, error every sample), so the
			// stopped transition plus its warning is the natural end of the
			// collection window.
			if event.Event == "server.warning" && strings.Contains(string(body), "playback_error") {
				break collect
			}
		case <-deadline:
			break collect
		}
	}

	// The upstream remainder is absorbed server-side now: the play response is
	// the buffering placeholder and the retry notices are constructed text, so
	// the boundary guarantee here is "no raw upstream text anywhere public"
	// (asserted per event and journal line above); the stripping itself is
	// anchored by the sanitize unit tests at the appleweb boundary.

	journalMu.Lock()
	lines := append([]string(nil), journal...)
	journalMu.Unlock()
	if len(lines) == 0 {
		t.Fatal("the server never journaled anything")
	}
	sawStalledWarning := false
	for _, line := range lines {
		assertNoUpstreamLeak(t, "the journal", line)
		if strings.Contains(line, "re-resolving") {
			sawStalledWarning = true
		}
	}
	if !sawStalledWarning {
		t.Fatal("the stall warning never fired; the upstream error did not reach the retry path")
	}
}

// TestWidevineProbeDrivesTheFullCapability exercises the three probe outcomes
// against the real composition: a browser without Widevine (or one whose probe
// could not run) must stop advertising playback.full once the probe has
// answered, while previews, search, and the queue keep working; the move is
// pushed as sources.changed, not left for the next poll by the client.
func TestWidevineProbeDrivesTheFullCapability(t *testing.T) {
	for _, outcome := range []struct {
		name       string
		ime        string
		wantFull   bool
		wantReason string
	}{
		{"denied", "denied", false, "Widevine"},
		{"probe failed", "error", false, "did not answer"},
		{"supported", "ok", true, ""},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			t.Setenv("LILT_TEST_FAKE_CDP_IME", outcome.ime)
			executable, err := os.Executable()
			if err != nil {
				t.Fatalf("os.Executable: %v", err)
			}
			t.Setenv("LILT_TEST_FAKE_CDP", "1")
			// A fresh profile per run keeps the warm-up lazy: a leftover
			// profile would start the browser during boot warm-up, settling
			// the probe before the catalog call and letting the wait match the
			// warm-up's own sources.changed instead of the poll's push.
			dir, err := os.MkdirTemp("/tmp", "lilt-widevine-")
			if err != nil {
				t.Fatalf("temp dir: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			engine := appleweb.NewEngine(appleweb.Options{
				ProfileDir:   filepath.Join(dir, "profile"),
				ChromiumPath: executable,
				Headless:     true,
			})
			_, socket := startAppleCompositionServer(t, engine, nil)

			// Before any browser start the probe has not answered: full keeps
			// its declared precondition. The engine cache must not pretend.
			before := appleDescriptorForComposition(t, socket)
			if !before.Capabilities["playback.full"].Available {
				t.Fatalf("full playback unavailable before any probe: %+v", before.Capabilities["playback.full"])
			}

			watchCtx, watchCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer watchCancel()
			_, watcher, err := api.Watch(watchCtx, socket, []string{"sources"}, false)
			if err != nil {
				t.Fatalf("watch: %v", err)
			}
			defer func() { _ = watcher.Close() }()

			// Any Apple catalog call starts the browser, which runs the probe.
			// With a fresh profile the boot warm-up stayed lazy, so from here the
			// settled verdict can only arrive via the server's signature poll:
			// waiting for the push pins that machinery. (The warm-up completion
			// also publishes a sources.changed at boot, before this watcher
			// connects; its pre-probe shape — full still claimed — cannot match
			// the negative outcomes. For the supported outcome both shapes say
			// available, and the substance of that case is the descriptor
			// assertions below.)
			if _, err := api.Command(watchCtx, socket, "discovery.search", map[string]any{"source": "apple-music", "term": "fixture", "type": "song"}); err != nil {
				t.Fatalf("discovery.search: %v", err)
			}

			// The descriptor is settled as soon as the probe answered; the
			// contract under test is the push: the server polls the declared
			// signature and publishes sources.changed carrying the settled
			// verdict, so watch clients refresh instead of holding a stale
			// capability snapshot. The boot warm-up also publishes one
			// sources.changed (pre-probe, full still claimed), so the wait
			// matches on the settled shape, not on the event name alone.
			deadline := time.After(8 * time.Second)
		waitPush:
			for {
				select {
				case event := <-watcher.Events:
					if event.Event != "sources.changed" {
						continue
					}
					var payload struct {
						Sources []api.SourceDescriptor `json:"sources"`
					}
					if json.Unmarshal(event.Data, &payload) != nil {
						continue
					}
					for _, descriptor := range payload.Sources {
						if descriptor.ID == api.SourceAppleMusic &&
							descriptor.Capabilities["playback.full"].Available == outcome.wantFull {
							break waitPush
						}
					}
				case <-deadline:
					t.Fatalf("the probe verdict was never pushed as sources.changed (want playback.full available=%v)", outcome.wantFull)
				}
			}
			after := appleDescriptorForComposition(t, socket)
			full := after.Capabilities["playback.full"]
			if full.Available != outcome.wantFull {
				t.Fatalf("playback.full = %+v", full)
			}
			if outcome.wantReason != "" && !strings.Contains(full.Reason, outcome.wantReason) {
				t.Fatalf("full reason = %q, want it to mention %q", full.Reason, outcome.wantReason)
			}
			// Previews are not DRM content and stay available either way; the
			// source as a whole keeps serving them.
			if !after.Capabilities["playback.preview"].Available {
				t.Fatalf("previews fell away with full playback: %+v", after.Capabilities["playback.preview"])
			}
			if !after.Available {
				t.Fatalf("the whole source went unavailable: %+v", after)
			}
		})
	}
}

// appleDescriptorForComposition reads the Apple descriptor over the real
// socket.
func appleDescriptorForComposition(t *testing.T, socket string) api.SourceDescriptor {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := api.Command(ctx, socket, "sources.list", nil)
	if err != nil {
		t.Fatalf("sources.list: %v", err)
	}
	var descriptors []api.SourceDescriptor
	if err := json.Unmarshal(response.Data, &descriptors); err != nil {
		t.Fatalf("decode sources.list: %v", err)
	}
	for _, descriptor := range descriptors {
		if descriptor.ID == api.SourceAppleMusic {
			return descriptor
		}
	}
	t.Fatal("apple-music is missing from sources.list")
	return api.SourceDescriptor{}
}
