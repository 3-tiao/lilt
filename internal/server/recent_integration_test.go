package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/state"
)

// Recent history is written only after the monotonic playing threshold, driven
// by the sampler rather than at play time.
func TestRecentRecordedAfterPlayingThreshold(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-recent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := Start(Options{
		SocketPath: socket,
		Engine:     newSupervisedEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		RecentMin:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if played := call(t, socket, "playback.play", map[string]any{"ref": "song:1"}); !played.OK {
		t.Fatalf("play failed: %+v", played.Error)
	}
	// Immediately after play the entry is not yet in history.
	immediate := call(t, socket, "recent.list", map[string]any{"limit": 5})
	var items []api.Item
	_ = json.Unmarshal(immediate.Data, &items)
	if len(items) != 0 {
		t.Fatalf("recent recorded before threshold: %+v", items)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		response := call(t, socket, "recent.list", map[string]any{"limit": 5})
		var recorded []api.Item
		if json.Unmarshal(response.Data, &recorded) == nil && len(recorded) > 0 {
			if recorded[0].Ref != "apple-music:song:1" {
				t.Fatalf("recent item = %+v", recorded[0])
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("recent entry never recorded after threshold")
}
