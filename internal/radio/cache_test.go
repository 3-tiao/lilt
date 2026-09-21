package radio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
)

func TestCacheSnapshotIsIndependent(t *testing.T) {
	cache := NewCache("")
	cache.Stations["one"] = StationRecord{StationUUID: "one", Tags: []string{"jazz"}}
	cache.Health["endpoint"] = HealthRecord{Status: "healthy"}

	snapshot := cache.Snapshot()
	station := snapshot.Stations["one"]
	station.StationUUID = "changed"
	station.Tags[0] = "rock"
	snapshot.Stations["one"] = station
	delete(snapshot.Health, "endpoint")
	if cache.Stations["one"].StationUUID != "one" || cache.Stations["one"].Tags[0] != "jazz" {
		t.Fatalf("snapshot changed source station: %#v", cache.Stations["one"])
	}
	if _, ok := cache.Health["endpoint"]; !ok {
		t.Fatal("snapshot changed source health")
	}
}

func TestCacheSeparatesStationIdentityFromEndpointHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio-cache.json")
	cache := NewCache(path)
	now := time.Now().UTC().Add(-time.Minute)
	item := core.Item{
		Kind: "stream", ID: "station-1", URL: "https://radio.example/live?public=one", Title: "City Pop FM",
		Radio: &core.RadioMetadata{StationUUID: "station-1", Tags: []string{"city pop", "pop"}, Languages: []string{"jpn"}, ClickCount: 42},
	}
	cache.RememberItems([]core.Item{item}, now)
	cache.RecordHealth(item.URL, "healthy", 100, "", now)
	cache.RecordHealth(item.URL, "healthy", 300, "", now.Add(time.Minute))
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	station := loaded.Stations["station-1"]
	if station.EndpointKey != EndpointKey(item.URL) || station.Tags[0] != "city pop" || station.ClickCount != 42 {
		t.Fatalf("station cache = %#v", station)
	}
	health, ok := loaded.FreshHealth(item.URL, now.Add(2*time.Hour))
	if !ok || health.LatencyMs != 200 || health.Samples != 2 {
		t.Fatalf("health cache = %#v, %v", health, ok)
	}

	changedURL := "https://radio.example/new-live"
	item.URL = changedURL
	cache.RememberItems([]core.Item{item}, now.Add(2*time.Hour))
	if cache.Stations["station-1"].EndpointKey != EndpointKey(changedURL) {
		t.Fatal("station endpoint did not change")
	}
	if _, ok := cache.FreshHealth(changedURL, now.Add(2*time.Hour)); ok {
		t.Fatal("new endpoint reused old endpoint health")
	}
}

func TestCacheDoesNotPersistCustomStreamURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio-cache.json")
	cache := NewCache(path)
	customURL := "https://private.example/live?token=secret"
	cache.RememberItems([]core.Item{{Kind: "stream", URL: customURL, Title: "Private"}}, time.Now())
	cache.RecordHealth(customURL, "healthy", 50, "", time.Now())
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private.example") || strings.Contains(string(data), "secret") {
		t.Fatalf("custom URL leaked into cache: %s", data)
	}
}

func TestHealthTTL(t *testing.T) {
	cache := NewCache("")
	now := time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)
	cache.RecordHealth("https://radio.example/healthy", "healthy", 80, "", now)
	if _, ok := cache.FreshHealth("https://radio.example/healthy", now.Add(25*time.Hour)); ok {
		t.Fatal("healthy result remained fresh after 24 hours")
	}
	cache.RecordHealth("https://radio.example/timeout", "failed", 0, "timeout", now)
	if _, ok := cache.FreshHealth("https://radio.example/timeout", now.Add(11*time.Minute)); ok {
		t.Fatal("transient failure remained fresh after 10 minutes")
	}
}
