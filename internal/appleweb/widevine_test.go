package appleweb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The probe runs on every browser start and the engine caches the answer; the
// three scripted outcomes (supported / refused / the probe itself failing) must
// land in the cached tri-state, with the reason text preserved for the
// capability the descriptor derives from it.
func TestEngineCachesTheWidevineProbeAnswer(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")

	t.Setenv("LILT_TEST_FAKE_CDP_IME", "denied")
	engine := NewEngine(fakeOptions(t, profile))
	if _, err := engine.Authorized(context.Background()); err != nil {
		t.Fatalf("first session: %v", err)
	}
	probe := engine.Widevine()
	if !probe.Answered || probe.Supported || !strings.Contains(probe.Reason, "NotSupportedError") {
		t.Fatalf("denied probe = %+v, want answered and unsupported with the page's reason", probe)
	}

	// A fresh start re-probes: the same engine picks up a new answer from a
	// new browser, so a different binary can change the verdict.
	if err := engine.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	t.Setenv("LILT_TEST_FAKE_CDP_IME", "ok")
	if _, err := engine.Authorized(context.Background()); err != nil {
		t.Fatalf("second session: %v", err)
	}
	if probe := engine.Widevine(); !probe.Answered || !probe.Supported {
		t.Fatalf("supported probe = %+v, want answered and supported", probe)
	}

	// A probe that cannot run is a negative answer, never a silent "probably
	// fine": full playback stays undeclared until something answers for it.
	if err := engine.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	t.Setenv("LILT_TEST_FAKE_CDP_IME", "error")
	if _, err := engine.Authorized(context.Background()); err != nil {
		t.Fatalf("third session: %v", err)
	}
	probe = engine.Widevine()
	if !probe.Answered || probe.Supported {
		t.Fatalf("failed probe = %+v, want answered and unsupported", probe)
	}
	if !strings.Contains(probe.Reason, "did not answer") {
		t.Fatalf("failed probe reason = %q, want the probe failure to be named", probe.Reason)
	}
}

// The probe is one of the evaluations the page depends on for user activation;
// the fake refuses it without userGesture like every other evaluation.
func TestWidevineProbeEvaluationCarriesUserGesture(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_IME", "ok")
	browser, log := startFakeBrowser(t, true)
	if probe := browser.probeWidevine(context.Background()); !probe.Answered || !probe.Supported {
		t.Fatalf("probe = %+v, want the scripted ok answer", probe)
	}
	for _, line := range strings.Split(strings.TrimSpace(log()), "\n") {
		if strings.Contains(line, "requestMediaKeySystemAccess") && !strings.Contains(line, "userGesture=true present=true") {
			t.Fatalf("the EME probe went out without userGesture: %s", line)
		}
	}
}

// A browser that never started has not answered: the zero probe keeps the
// descriptor's declared precondition instead of guessing.
func TestWidevineProbeUnansweredBeforeFirstStart(t *testing.T) {
	engine := NewEngine(fakeOptions(t, filepath.Join(t.TempDir(), "absent-profile")))
	if probe := engine.Widevine(); probe.Answered {
		t.Fatalf("probe = %+v, want unanswered before any browser start", probe)
	}
}
