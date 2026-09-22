package mpvplayer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
)

// startFakeClient wires a Client to this test binary re-entered as a fake mpv.
func startFakeClient(t *testing.T) (*Client, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	argvPath := filepath.Join(t.TempDir(), "argv")
	t.Setenv("LILT_TEST_FAKE_MPV", "1")
	t.Setenv("LILT_TEST_FAKE_MPV_ARGV", argvPath)
	t.Setenv("LILT_MPV_PATH", executable)
	client := New()
	client.LoadWait = 3 * time.Second
	t.Cleanup(func() { _ = client.Close() })
	return client, argvPath
}

func waitForState(t *testing.T, updates <-chan core.PlaybackStateUpdate, want func(core.PlaybackState) bool) core.PlaybackState {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				t.Fatal("update stream closed while waiting for a state")
			}
			if want(update.State) {
				return update.State
			}
		case <-deadline:
			t.Fatal("timed out waiting for a state")
		}
	}
}

func TestRadioPlayReportsLiveStream(t *testing.T) {
	client, _ := startFakeClient(t)
	state, err := client.RadioPlay(context.Background(), "http://example.test/live", "Lofi")
	if err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	if state.Status != "playing" {
		t.Fatalf("status = %q, want playing", state.Status)
	}
	if !state.IsLive || state.Mode != "stream" {
		t.Fatalf("live=%v mode=%q, want a live stream", state.IsLive, state.Mode)
	}
	if state.Track == nil || state.Track.URL != "http://example.test/live" || state.Track.Title != "Lofi" {
		t.Fatalf("track = %+v", state.Track)
	}
	if state.Position != 3.5 {
		t.Fatalf("position = %v, want the queried time-pos", state.Position)
	}
}

func TestMPVFlagsAreDeterministic(t *testing.T) {
	client, argvPath := startFakeClient(t)
	if _, err := client.RadioPlay(context.Background(), "http://example.test/live", "Lofi"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("read mpv argv: %v", err)
	}
	argv := string(raw)
	// The user's mpv.conf and any windowing must never change what lilt asks for.
	for _, want := range []string{
		"--no-config",
		"--idle=yes",
		"--no-terminal",
		"--force-window=no",
		"--audio-display=no",
		"--no-video",
		"--input-ipc-server=",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("mpv argv is missing %q:\n%s", want, argv)
		}
	}
}

func TestPauseAndResume(t *testing.T) {
	client, _ := startFakeClient(t)
	ctx := context.Background()
	if _, err := client.RadioPlay(ctx, "http://example.test/live", "Lofi"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	paused, err := client.PauseState(ctx)
	if err != nil {
		t.Fatalf("PauseState: %v", err)
	}
	if paused.Status != "paused" {
		t.Fatalf("status = %q, want paused", paused.Status)
	}
	resumed, err := client.ResumeState(ctx)
	if err != nil {
		t.Fatalf("ResumeState: %v", err)
	}
	if resumed.Status != "playing" {
		t.Fatalf("status = %q, want playing", resumed.Status)
	}
}

func TestStopClearsPlayback(t *testing.T) {
	client, _ := startFakeClient(t)
	ctx := context.Background()
	if _, err := client.RadioPlay(ctx, "http://example.test/live", "Lofi"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	stopped, err := client.RadioStop(ctx)
	if err != nil {
		t.Fatalf("RadioStop: %v", err)
	}
	if stopped.Status != "stopped" || stopped.Mode != "none" || stopped.Track != nil {
		t.Fatalf("stopped state = %+v", stopped)
	}
	// mpv reports time-pos unavailable with nothing loaded; a status read after a
	// stop must still answer instead of failing with a playback error.
	after, err := client.State(ctx)
	if err != nil {
		t.Fatalf("State after stop: %v", err)
	}
	if after.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", after.Status)
	}
}

func TestFailedStreamIsReported(t *testing.T) {
	client, _ := startFakeClient(t)
	state, err := client.RadioPlay(context.Background(), "http://example.test/fail", "Dead")
	if err == nil {
		t.Fatal("a refused stream must surface an error, not only a state")
	}
	if !strings.Contains(err.Error(), "Failed to open") {
		t.Fatalf("error = %v, want mpv's reason", err)
	}
	if state.Status != "error" || !strings.Contains(state.Error, "Failed to open") {
		t.Fatalf("state = %+v, want a playbackError", state)
	}
}

func TestSlowStreamBuffersInsteadOfFailing(t *testing.T) {
	client, _ := startFakeClient(t)
	client.LoadWait = 200 * time.Millisecond
	state, err := client.RadioPlay(context.Background(), "http://example.test/hang", "Slow")
	if err != nil {
		t.Fatalf("a slow station is not a failure: %v", err)
	}
	if state.Status != "buffering" {
		t.Fatalf("status = %q, want buffering", state.Status)
	}
}

func TestPlaybackEndReportsEnded(t *testing.T) {
	client, _ := startFakeClient(t)
	subscription, err := client.SubscribeState(context.Background())
	if err != nil {
		t.Fatalf("SubscribeState: %v", err)
	}
	if _, err := client.RadioPlay(context.Background(), "http://example.test/track.eof", "Finite"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	state := waitForState(t, subscription.Updates, func(state core.PlaybackState) bool { return state.Ended })
	if state.Status != "ended" {
		t.Fatalf("status = %q, want ended", state.Status)
	}
}

func TestTransportDeathClosesUpdates(t *testing.T) {
	client, _ := startFakeClient(t)
	subscription, err := client.SubscribeState(context.Background())
	if err != nil {
		t.Fatalf("SubscribeState: %v", err)
	}
	if _, err := client.RadioPlay(context.Background(), "http://example.test/live", "Lofi"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}

	client.mu.Lock()
	process := client.cmd.Process
	client.mu.Unlock()
	if process == nil {
		t.Fatal("mpv was never started")
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill mpv: %v", err)
	}

	// Closing the update stream is what tells the server to rebuild its engine.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-subscription.Updates:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("mpv death did not close the update stream")
		}
	}
}

func TestCloseLeavesNoProcess(t *testing.T) {
	client, _ := startFakeClient(t)
	if _, err := client.RadioPlay(context.Background(), "http://example.test/live", "Lofi"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	client.mu.Lock()
	process := client.cmd.Process
	dir := client.dir
	client.mu.Unlock()
	if process == nil {
		t.Fatal("mpv was never started")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("mpv is still running after Close")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("private runtime directory %s survived Close", dir)
	}
}

func TestReplacingStreamStaysPlaying(t *testing.T) {
	client, _ := startFakeClient(t)
	ctx := context.Background()
	if _, err := client.RadioPlay(ctx, "http://example.test/one", "One"); err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	// loadfile replace ends the previous stream first; that end must not be read
	// as the new stream having stopped.
	replaced, err := client.RadioPlay(ctx, "http://example.test/two", "Two")
	if err != nil {
		t.Fatalf("RadioPlay replacement: %v", err)
	}
	if replaced.Status != "playing" {
		t.Fatalf("status = %q, want playing", replaced.Status)
	}
	if replaced.Track == nil || replaced.Track.Title != "Two" {
		t.Fatalf("track = %+v, want the replacement", replaced.Track)
	}
}

func TestStateWithoutPlaybackDoesNotStartMPV(t *testing.T) {
	client, _ := startFakeClient(t)
	state, err := client.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", state.Status)
	}
	client.mu.Lock()
	started := client.ipc != nil || client.cmd != nil
	client.mu.Unlock()
	if started {
		t.Fatal("answering a status question must not spawn a player")
	}
}

func TestURLSessionGuardRejectsReplacedSessions(t *testing.T) {
	client, _ := startFakeClient(t)
	ctx := context.Background()
	target := core.URLPlaybackTarget{
		Item:               core.Item{Kind: "song", ID: "audius:song:1", Title: "Track"},
		URL:                "http://example.test/track.mp3",
		Duration:           180,
		PlaybackGeneration: 4,
		TransportSessionID: "session-a",
	}
	if _, err := client.PlayURL(ctx, target); err != nil {
		t.Fatalf("PlayURL: %v", err)
	}
	state, err := client.StateURL(ctx, 4, "session-a")
	if err != nil {
		t.Fatalf("StateURL: %v", err)
	}
	if state.PlaybackGeneration != 4 || state.TransportSessionID != "session-a" {
		t.Fatalf("url state lost its session identity: %+v", state)
	}
	if state.IsLive || state.Mode != "full" {
		t.Fatalf("url playback must not look live: live=%v mode=%q", state.IsLive, state.Mode)
	}
	if _, err := client.StateURL(ctx, 5, "session-b"); err == nil {
		t.Fatal("a superseded session must be rejected")
	}
	if _, err := client.PauseURL(ctx, 4, "session-b"); err == nil {
		t.Fatal("a pause aimed at a replaced session must be rejected")
	}
}

func TestMissingMPVReportsAnInstallHint(t *testing.T) {
	t.Setenv("LILT_MPV_PATH", filepath.Join(t.TempDir(), "absent-mpv"))
	client := New()
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.RadioPlay(context.Background(), "http://example.test/live", "Lofi")
	if err == nil {
		t.Fatal("a missing mpv binary must fail the first playback")
	}
	if !strings.Contains(err.Error(), "mpv is not installed") {
		t.Fatalf("error = %v, want an install hint", err)
	}
}
