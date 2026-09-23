package appleweb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startAlignedEngine wires an Engine to the fake CDP page and captures both the
// fake's request log and the journal events the engine emits.
func startAlignedEngine(t *testing.T) (*Engine, func() string, func() []map[string]any) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "cdp.log")
	t.Setenv("LILT_TEST_FAKE_CDP", "1")
	t.Setenv("LILT_TEST_FAKE_CDP_LOG", logPath)

	var mu sync.Mutex
	var events []map[string]any
	engine := NewEngine(Options{
		ProfileDir:   filepath.Join(dir, "profile"),
		ChromiumPath: executable,
		Headless:     true,
		Log: func(kind string, fields map[string]any) {
			entry := map[string]any{"kind": kind}
			for key, value := range fields {
				entry[key] = value
			}
			mu.Lock()
			defer mu.Unlock()
			events = append(events, entry)
		},
	})
	t.Cleanup(func() { _ = engine.Close() })
	return engine,
		func() string {
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read cdp log: %v", err)
			}
			return string(raw)
		},
		func() []map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any(nil), events...)
		}
}

// A signed-in account whose storefront differs from the page region must move
// the page exactly once: the navigation lands the page on the account region,
// the catalog then runs there, and the journal records the move. The
// re-read-after-navigation must observe the match, because the two-navigation
// give-up test below proves a mismatch would navigate again.
func TestStorefrontAlignsThePageToTheAccount(t *testing.T) {
	// The fake page starts on the US region (the default) and the account
	// lives in the CN storefront.
	t.Setenv("LILT_TEST_FAKE_CDP_STOREFRONT_ACCOUNT", "cn")
	engine, log, events := startAlignedEngine(t)

	authorized, err := engine.Authorized(context.Background())
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !authorized {
		t.Fatal("the fixture session must be authorized for alignment to run")
	}

	raw := log()
	if navs := strings.Count(raw, "window.location.href"); navs != 1 {
		t.Fatalf("alignment navigated %d times, want exactly 1:\n%s", navs, raw)
	}
	if !strings.Contains(raw, "music.apple.com/cn/listen-now") {
		t.Fatalf("the navigation did not target the account storefront:\n%s", raw)
	}
	// Alignment settles before the Widevine probe, so the capability answer and
	// the catalog see the final region.
	if strings.Index(raw, "window.location.href") > strings.Index(raw, "isSecureContext") {
		t.Fatalf("the Widevine probe ran before the page was aligned:\n%s", raw)
	}
	// The account is asked, then confirmed after the navigation.
	if probes := strings.Count(raw, "/v1/me/storefront"); probes < 2 {
		t.Fatalf("the account storefront was read %d times, want a read and a confirmation", probes)
	}

	aligned := events()
	if len(aligned) != 1 {
		t.Fatalf("journal events = %v, want exactly the alignment entry", aligned)
	}
	if aligned[0]["kind"] != "apple.storefront_aligned" || aligned[0]["from"] != "us" || aligned[0]["to"] != "cn" {
		t.Fatalf("alignment event = %v, want us -> cn", aligned[0])
	}

	// The catalog expression reads mk.storefrontId, which after alignment is
	// the account region; the fake notes the region it served.
	if _, err := engine.SearchSongs(context.Background(), "fixture", 5); err != nil {
		t.Fatalf("SearchSongs after alignment: %v", err)
	}
	raw = log()
	if !strings.Contains(raw, "note catalog storefront=cn") {
		t.Fatalf("the catalog did not run in the account storefront:\n%s", raw)
	}
	if strings.Contains(raw, "note catalog storefront=us") {
		t.Fatalf("the catalog ran in the page's old storefront:\n%s", raw)
	}
	// Reading the storefronts back must agree — a consistent answer is what
	// stops the alignment loop from navigating again.
	browser, err := engine.session(context.Background())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	probe, err := browser.storefronts(context.Background())
	if err != nil {
		t.Fatalf("storefronts: %v", err)
	}
	if probe.Account != "cn" || probe.Page != "cn" {
		t.Fatalf("storefronts after alignment = account %q page %q, want cn on both", probe.Account, probe.Page)
	}
}

// A session that cannot answer /v1/me/storefront (signed out, or authorized
// once and expired since) has no region to align to: the page stays where it
// is and the session keeps serving the catalog.
func TestStorefrontTreatsAnUnanswerableAccountAsSignedOut(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_STOREFRONT_ERROR", "1")
	engine, log, _ := startAlignedEngine(t)

	authorized, err := engine.Authorized(context.Background())
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !authorized {
		t.Fatal("the fixture session must still report authorized; the me failure is the point")
	}
	raw := log()
	if navs := strings.Count(raw, "window.location.href"); navs != 0 {
		t.Fatalf("an unanswerable account still navigated %d times:\n%s", navs, raw)
	}
	if probes := strings.Count(raw, "/v1/me/storefront"); probes < 1 {
		t.Fatalf("the account storefront was never asked; the fixture is not exercising the me failure:\n%s", raw)
	}
	if _, err := engine.SearchSongs(context.Background(), "fixture", 5); err != nil {
		t.Fatalf("SearchSongs without alignment: %v", err)
	}
}

// A signed-out session never asks about the storefront at all: previews are
// previews anywhere, so the page keeps the region it landed on.
func TestStorefrontSkipsSignedOutSessions(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":true,"authorized":false,"state":0}`)
	engine, log, _ := startAlignedEngine(t)

	authorized, err := engine.Authorized(context.Background())
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if authorized {
		t.Fatal("the fixture session must report signed out")
	}
	raw := log()
	if probes := strings.Count(raw, "/v1/me/storefront"); probes != 0 {
		t.Fatalf("a signed-out session asked about the storefront %d times:\n%s", probes, raw)
	}
	if navs := strings.Count(raw, "window.location.href"); navs != 0 {
		t.Fatalf("a signed-out session navigated %d times:\n%s", navs, raw)
	}
}

// A page that never lands on the account storefront must not loop: alignment
// gives up after its limit, journals each attempt, and the session continues
// on the region the page is on — the catalog still answers there and playback
// degrades to preview instead of failing the browser start.
func TestStorefrontGivesUpAfterTwoNavigationsAndContinues(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_STOREFRONT_ACCOUNT", "cn")
	// Navigations never move the canned page, so every alignment attempt
	// re-reads the same mismatch.
	t.Setenv("LILT_TEST_FAKE_CDP_STOREFRONT_NAV_FAIL", "1")
	// The settle wait is what makes a failed attempt slow; shorten it.
	original := storefrontSettle
	storefrontSettle = 100 * time.Millisecond
	defer func() { storefrontSettle = original }()

	engine, log, events := startAlignedEngine(t)

	authorized, err := engine.Authorized(context.Background())
	if err != nil {
		t.Fatalf("Authorized after a failed alignment: %v", err)
	}
	if !authorized {
		t.Fatal("a failed alignment must not break the session")
	}
	raw := log()
	if navs := strings.Count(raw, "window.location.href"); navs != 2 {
		t.Fatalf("alignment navigated %d times, want the limit of 2:\n%s", navs, raw)
	}
	if aligned := events(); len(aligned) != 2 {
		t.Fatalf("journal events = %v, want one per attempt", aligned)
	}
	// The session continues on the unaligned region: the catalog answers there.
	if _, err := engine.SearchSongs(context.Background(), "fixture", 5); err != nil {
		t.Fatalf("SearchSongs after a failed alignment: %v", err)
	}
	if !strings.Contains(log(), "note catalog storefront=us") {
		t.Fatalf("the catalog did not keep serving the page's own storefront:\n%s", log())
	}
}
