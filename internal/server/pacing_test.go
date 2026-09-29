package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/state"
)

// The append gap is a probe knob: an unset or non-positive value must keep the
// conservative default, and a configured value must win.
func TestQueuePacingFallsBackToTheDefault(t *testing.T) {
	if got := queuePacing(0); got != defaultQueuePacing {
		t.Fatalf("queuePacing(0) = %v, want %v", got, defaultQueuePacing)
	}
	if got := queuePacing(-time.Second); got != defaultQueuePacing {
		t.Fatalf("queuePacing(-1s) = %v, want %v", got, defaultQueuePacing)
	}
	if got := queuePacing(300 * time.Millisecond); got != 300*time.Millisecond {
		t.Fatalf("queuePacing(300ms) = %v, want 300ms", got)
	}
}

// A configured pacing reaches the server used by the finite-queue path.
func TestOptionsQueuePacingIsApplied(t *testing.T) {
	dir := t.TempDir()
	server, err := Start(Options{
		SocketPath:   filepath.Join(dir, "s.sock"),
		Engine:       fakeengine.NewFakeEngine(),
		Store:        state.New(filepath.Join(dir, "state.json")),
		AudiusClient: startFakeAudius(t),
		QueuePacing:  250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if server.queuePacing != 250*time.Millisecond {
		t.Fatalf("server pacing = %v, want 250ms", server.queuePacing)
	}
}
