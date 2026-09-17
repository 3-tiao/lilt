package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/state"
)

// The server exposes its disposable probe cache so the TUI can display cached
// station health and an offline fallback without its own persistence.
func TestRadioCacheCommandExposesServerCache(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-rcache-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	cache := radio.NewCache(filepath.Join(dir, "radio-cache.json"))
	cache.RememberItems([]core.Item{{
		Kind: "stream", URL: "https://radio.example/live", Title: "Example",
		Radio: &core.RadioMetadata{StationUUID: "station-1", Tags: []string{"lofi"}},
	}}, time.Now())
	cache.RecordHealth("https://radio.example/live", "healthy", 120, "", time.Now())
	if err := cache.Save(); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	srv, err := Start(Options{
		SocketPath: socket,
		Engine:     newSupervisedEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		RadioCache: cache,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	response := call(t, socket, "radio.cache", nil)
	if !response.OK {
		t.Fatalf("radio.cache failed: %+v", response.Error)
	}
	var payload struct {
		Version  int                        `json:"version"`
		Stations map[string]json.RawMessage `json:"stations"`
		Health   map[string]json.RawMessage `json:"health"`
	}
	if err := json.Unmarshal(response.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Stations) == 0 || len(payload.Health) == 0 {
		t.Fatalf("cache payload = %+v", payload)
	}
}
