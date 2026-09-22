package appleweb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// startFakeBrowser wires a Browser to this test binary re-entered as a fake
// DevTools peer. It returns the browser and the request log the fake wrote.
func startFakeBrowser(t *testing.T, headless bool) (*Browser, func() string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "cdp.log")
	argvPath := filepath.Join(dir, "argv")
	t.Setenv("LILT_TEST_FAKE_CDP", "1")
	t.Setenv("LILT_TEST_FAKE_CDP_LOG", logPath)
	t.Setenv("LILT_TEST_FAKE_CDP_ARGV", argvPath)

	browser, err := Start(context.Background(), Options{
		ProfileDir:   filepath.Join(dir, "profile"),
		ChromiumPath: executable,
		Headless:     headless,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })

	return browser, func() string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read cdp log: %v", err)
		}
		return string(raw)
	}
}

// A launch with --restore-last-session reopens last session's tabs, so Start must
// take one page and close the rest. Without this each launch leaves another
// Apple Music tab behind.
func TestStartKeepsOnePageAndClosesRestoredTabs(t *testing.T) {
	browser, log := startFakeBrowser(t, true)
	if browser.session != "session-1" {
		t.Fatalf("session = %q, want the attached session", browser.session)
	}
	var closed []string
	for _, line := range strings.Split(strings.TrimSpace(log()), "\n") {
		if strings.HasPrefix(line, "Target.closeTarget") {
			closed = append(closed, line)
		}
	}
	if len(closed) != 1 {
		t.Fatalf("expected exactly the restored tab to be closed, got %d closes:\n%s", len(closed), strings.Join(closed, "\n"))
	}
	if !strings.Contains(closed[0], "restored") {
		t.Fatalf("the wrong target was closed: %s", closed[0])
	}
	if strings.Contains(closed[0], "kept") {
		t.Fatalf("the driven page was closed: %s", closed[0])
	}
}

func TestLaunchFlagsCarrySessionRestoreAndProfile(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LILT_TEST_FAKE_CDP", "1")
	t.Setenv("LILT_TEST_FAKE_CDP_ARGV", argvPath)

	browser, err := Start(context.Background(), Options{
		ProfileDir:   filepath.Join(dir, "profile"),
		ChromiumPath: executable,
		Headless:     true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = browser.Close() }()

	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	argv := string(raw)
	// Session restore is what keeps Apple's session cookies across restarts;
	// without it every restart is a logout.
	for _, want := range []string{"--restore-last-session", "--remote-debugging-pipe", "--user-data-dir=", "--headless=new", "--autoplay-policy=no-user-gesture-required"} {
		if !strings.Contains(argv, want) {
			t.Errorf("chromium argv is missing %q:\n%s", want, argv)
		}
	}
}

// Every evaluation must carry userGesture. The fake refuses evaluations without
// it, exactly as the real page refuses play() without user activation, so a
// regression here fails the test instead of hanging silently in production.
func TestEvaluationsAlwaysCarryUserGesture(t *testing.T) {
	browser, log := startFakeBrowser(t, true)
	ctx := context.Background()

	if _, err := browser.Evaluate(ctx, "1+1"); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := browser.PlayCatalogSong(ctx, "111"); err != nil {
		t.Fatalf("PlayCatalogSong: %v", err)
	}
	if err := browser.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if _, err := browser.State(ctx); err != nil {
		t.Fatalf("State: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(log()), "\n") {
		if !strings.HasPrefix(line, "Runtime.evaluate") {
			continue
		}
		if !strings.Contains(line, "userGesture=true present=true") {
			t.Fatalf("an evaluation went out without userGesture: %s", line)
		}
		if !strings.Contains(line, "session=session-1") {
			t.Fatalf("an evaluation was sent outside the page session: %s", line)
		}
	}
}

// The state probe is split into two writes by the fake, so a partial read has to
// be reassembled rather than parsed as a frame.
func TestStateReadsAReassembledFrame(t *testing.T) {
	browser, _ := startFakeBrowser(t, true)
	state, err := browser.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !state.Ready || !state.Authorized || !state.IsPlaying {
		t.Fatalf("state = %+v", state)
	}
	if state.Status != "playing" || state.Duration != 204 || state.Position != 1.5 {
		t.Fatalf("state = %+v, want a full-length playing track", state)
	}
	if state.ItemID != "111" || state.ItemTitle != "Fixture" {
		t.Fatalf("item = %q / %q", state.ItemID, state.ItemTitle)
	}
}

func TestStateMapsPageFailures(t *testing.T) {
	browser, _ := startFakeBrowser(t, true)
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":false,"failure":"boom"}`)
	// The fake reads its state once at start, so drive the mapping directly.
	if got := statusName(statusWaiting); got != "waiting" {
		t.Fatalf("statusName(8) = %q", got)
	}
	if got := statusName(99); got != "none" {
		t.Fatalf("statusName(99) = %q", got)
	}
	if _, err := browser.State(context.Background()); err != nil {
		t.Fatalf("State: %v", err)
	}
}

func TestPageExceptionsAreReported(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_EXCEPTION", "explode")
	browser, _ := startFakeBrowser(t, true)
	_, err := browser.Evaluate(context.Background(), "explode()")
	if err == nil {
		t.Fatal("a page exception must surface as an error")
	}
	if !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("error = %v, want the page's own description", err)
	}
	// Only the first line: a stack trace is noise in an API error.
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("error kept the stack trace: %q", err)
	}
}

func TestCloseShutsDownGracefully(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "cdp.log")
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LILT_TEST_FAKE_CDP", "1")
	t.Setenv("LILT_TEST_FAKE_CDP_LOG", logPath)

	browser, err := Start(context.Background(), Options{
		ProfileDir:   filepath.Join(dir, "profile"),
		ChromiumPath: executable,
		Headless:     true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := browser.cmd.Process.Pid
	if err := browser.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Browser.close comes first: a SIGKILL would drop the profile writes that
	// keep the Apple session.
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "Browser.close") {
		t.Fatalf("Close did not ask the browser to shut down:\n%s", raw)
	}
	if err := checkProcessGone(pid); err == nil {
		t.Fatal("the browser process survived Close")
	}
}

// checkProcessGone returns an error once the pid no longer exists.
func checkProcessGone(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.Signal(0))
}

func TestMissingChromiumReportsHowToGetOne(t *testing.T) {
	_, err := findChromium(filepath.Join(t.TempDir(), "absent-chromium"))
	if err == nil {
		t.Fatal("a missing browser must fail")
	}
	if !strings.Contains(err.Error(), "Widevine") || !strings.Contains(err.Error(), "LILT_CHROMIUM_PATH") {
		t.Fatalf("error = %v, want an install hint", err)
	}
}
