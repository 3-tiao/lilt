package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caiguo/lilt/core"
)

func TestRecordRankAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	a := core.Item{Kind: "song", ID: "a"}
	b := core.Item{Kind: "song", ID: "b"}
	store.Record("focus", b)
	store.Record("focus", b)
	ranked := store.Rank("focus", []core.Item{a, b})
	if len(ranked) != 2 || ranked[0].ID != "b" || ranked[1].ID != "a" {
		t.Fatalf("ranked = %#v, want b first", ranked)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	record := loaded.Presets["focus"]
	if record.Uses != 2 || record.Chosen["b"] != 2 || record.Last != "b" {
		t.Fatalf("record = %#v", record)
	}
}

func TestRadioIdentityCanonicalization(t *testing.T) {
	a := ItemID("radio", core.Item{URL: "HTTPS://Radio.Example/Live/?token=one#fragment"})
	b := ItemID("radio", core.Item{URL: "https://radio.example/Live?token=one"})
	if a != b || !strings.Contains(a, "?token=one") || strings.Contains(a, "#") {
		t.Fatalf("identities differ or lost query: %q %q", a, b)
	}
	if got := ItemID("radio", core.Item{URL: "https://RADIO.example/"}); got != "radio:https://radio.example/" {
		t.Fatalf("root slash changed: %q", got)
	}
}

func TestItemIDIsIdempotentForStoredEntries(t *testing.T) {
	if got := ItemID("apple-music", core.Item{Kind: "song", ID: "am:123"}); got != "am:123" {
		t.Fatalf("prefixed Apple ID doubled: %q", got)
	}
	if got := ItemID("apple-music", core.Item{Kind: "song", ID: "123"}); got != "am:123" {
		t.Fatalf("raw Apple ID not prefixed: %q", got)
	}
	if got := ItemID("radio", core.Item{Kind: "stream", ID: "radio:https://radio.example/live"}); got != "radio:https://radio.example/live" {
		t.Fatalf("prefixed radio ID doubled: %q", got)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	song := core.Item{Kind: "song", ID: "s1", Title: "Song"}
	store.ToggleFavorite("apple-music", song)
	stored := store.FavoritesFor("apple-music")
	if len(stored) != 1 || !store.IsFavorite("apple-music", ItemID("apple-music", stored[0])) {
		t.Fatalf("stored favorite identity mismatch: %#v", stored)
	}
	if store.ToggleFavorite("apple-music", stored[0]) {
		t.Fatal("toggling a stored favorite should remove it, not add a duplicate")
	}
	if len(store.FavoritesFor("apple-music")) != 0 {
		t.Fatalf("duplicate favorites: %#v", store.FavoritesFor("apple-music"))
	}
}

func TestFutureVersionIsReadOnlyAndNotRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte(`{"version":99,"future":"keep"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err == nil || store == nil {
		t.Fatalf("Load = %#v, %v", store, err)
	}
	if saveErr := store.Save(); saveErr == nil {
		t.Fatal("future state save unexpectedly succeeded")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("future state changed: %q %v", got, readErr)
	}
}

func TestCorruptStateIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err == nil || store == nil {
		t.Fatalf("Load = %#v, %v", store, err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original remains: %v", statErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.Contains(entries[0].Name(), ".corrupt-") {
		t.Fatalf("quarantine entries = %v", entries)
	}
	if saveErr := store.Save(); saveErr != nil {
		t.Fatal(saveErr)
	}
}

func TestTypedCorruptStateIsQuarantinedBeforeLaterSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	original := []byte(`{"version":1,"theme":["not-a-string"],"favorites":{}}`)
	// Pre-create the first likely quarantine basename to exercise collision-safe
	// creation without relying on timestamp precision.
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err == nil {
		t.Fatal("typed corruption unexpectedly loaded")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("typed-corrupt original remains at writable path: %v", statErr)
	}
	store.LastSource = "radio"
	if err := store.Save(); err != nil {
		t.Fatalf("save after successful quarantine: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var preserved bool
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".corrupt-") {
			data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
			if readErr == nil && string(data) == string(original) {
				preserved = true
			}
		}
	}
	if !preserved {
		t.Fatalf("typed-corrupt contents were not preserved: %v", entries)
	}
}

func TestUpdateAndSaveFailureDoesNotLeakIntoLaterSave(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	store := New(filepath.Join(blocker, "state.json"))
	if err := store.UpdateAndSave(func(next *Store) {
		next.AddRecent("apple-music", core.Item{Kind: "song", ID: "failed", Title: "Must not leak"})
	}); err == nil {
		t.Fatal("blocked save unexpectedly succeeded")
	}
	if len(store.Recent) != 0 {
		t.Fatalf("failed mutation leaked into live store: %#v", store.Recent)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAndSave(func(next *Store) { next.LastSource = "radio" }); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(blocker, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastSource != "radio" || len(loaded.Recent) != 0 {
		t.Fatalf("later save included failed mutation: %#v", loaded)
	}
}

func TestFavoritesToggleAndSourceIDs(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	song := core.Item{Kind: "song", ID: "123", Title: "Song"}
	if store.ToggleFavorite("apple-music", song) != true {
		t.Fatal("expected favorited")
	}
	if !store.IsFavorite("apple-music", "am:123") {
		t.Fatal("favorite not stored with stable id")
	}
	if store.ToggleFavorite("apple-music", song) != false {
		t.Fatal("expected un-favorited")
	}
	station := core.Item{Kind: "stream", URL: "https://radio.example/lofi/", Title: "lofi"}
	store.ToggleFavorite("radio", station)
	favorites := store.FavoritesFor("radio")
	if len(favorites) != 1 || favorites[0].ID != "https://radio.example/lofi" || ItemID("radio", favorites[0]) != "radio:https://radio.example/lofi" {
		t.Fatalf("radio favorites = %#v", favorites)
	}
}

func TestRecentDedupAndOrder(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	a := core.Item{Kind: "song", ID: "a", Title: "A"}
	b := core.Item{Kind: "song", ID: "b", Title: "B"}
	store.AddRecent("apple-music", a)
	store.AddRecent("apple-music", b)
	store.AddRecent("apple-music", a)
	if len(store.Recent) != 2 || store.Recent[0].ID != "am:a" {
		t.Fatalf("recent = %#v", store.Recent)
	}
}

func TestRecentContainersDedupAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	store.AddRecentContainer(core.Item{Kind: "playlist", ID: "p1", Title: "Morning"})
	store.AddRecentContainer(core.Item{Kind: "unknown", ID: "l1", Title: "Road"})
	store.AddRecentContainer(core.Item{Kind: "playlist", ID: "p1", Title: "Morning Mix"})
	if len(store.RecentContainers) != 1 || store.RecentContainers[0].ID != "playlist:p1" || store.RecentContainers[0].Title != "Morning Mix" {
		t.Fatalf("recent containers = %#v", store.RecentContainers)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.RecentContainers) != 1 || loaded.RecentContainers[0].Kind != "playlist" {
		t.Fatalf("loaded recent containers = %#v", loaded.RecentContainers)
	}
}

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if store == nil || len(store.Presets) != 0 {
		t.Fatalf("store = %#v", store)
	}
}

func TestRadioDiscoveryQueriesAreNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "radioDiscoveryFilter") {
		t.Fatalf("state unexpectedly persists radio query: %s", data)
	}
}

func TestRankNilStoreAndStableOrder(t *testing.T) {
	var store *Store
	items := []core.Item{{Kind: "song", ID: "a"}, {Kind: "song", ID: "b"}}
	ranked := store.Rank("focus", items)
	if len(ranked) != 2 || ranked[0].ID != "a" || ranked[1].ID != "b" {
		t.Fatalf("ranked = %#v, want provider order", ranked)
	}
	ranked[0].ID = "changed"
	if items[0].ID != "a" {
		t.Fatal("Rank mutated the provider slice")
	}
}

func TestRecentForFiltersBySource(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	store.AddRecent("radio", core.Item{Kind: "stream", URL: "https://radio.example/a", Title: "A"})
	store.AddRecent("apple-music", core.Item{Kind: "song", ID: "1", Title: "S"})
	items := store.RecentFor("radio")
	if len(items) != 1 || items[0].Title != "A" || items[0].Kind != "stream" {
		t.Fatalf("recent = %#v", items)
	}
}
