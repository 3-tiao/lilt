package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caiguo/lilt/core"
)

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

// The same recording can reach lilt under different provider ids (a
// queue-local id from MusicKit vs the catalog id from search); one
// title/artist pair must still be one history entry.
func TestRecentDedupesSameTrackAcrossProviderIDs(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	catalog := core.Item{Kind: "song", ID: "163707331690918816", Title: "写一条歌,写你我尔尔 (feat. 黄奇斌)", Artist: "萧煌奇"}
	queueLocal := core.Item{Kind: "song", ID: "i.WmYRDYgcDE6lAz", Title: "写一条歌,写你我尔尔 (feat. 黄奇斌)", Artist: "萧煌奇"}
	store.AddRecent("apple-music", catalog)
	store.AddRecent("apple-music", queueLocal)
	if len(store.Recent) != 1 || store.Recent[0].ID != "am:i.WmYRDYgcDE6lAz" {
		t.Fatalf("recent = %#v, want one entry with the newest id", store.Recent)
	}
	// Whitespace and case variants of the same recording still merge.
	other := core.Item{Kind: "song", ID: "c", Title: "  写一条歌,写你我尔尔 (feat. 黄奇斌) ", Artist: " 萧煌奇 "}
	store.AddRecent("apple-music", other)
	if len(store.Recent) != 1 || store.Recent[0].ID != "am:c" {
		t.Fatalf("whitespace variant created a duplicate: %#v", store.Recent)
	}
	// Different recordings with the same artist stay separate.
	store.AddRecent("apple-music", core.Item{Kind: "song", ID: "d", Title: "Another Song", Artist: "萧煌奇"})
	if len(store.Recent) != 2 {
		t.Fatalf("distinct recordings merged: %#v", store.Recent)
	}
	// Matching is scoped to one source: the same title on another source is a
	// separate history entry.
	store.AddRecent("audius", core.Item{Kind: "song", ID: "x", Title: "Another Song", Artist: "萧煌奇"})
	if len(store.Recent) != 3 {
		t.Fatalf("cross-source rows merged: %#v", store.Recent)
	}
}

func TestRecentContainersDedupAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	store.AddRecentContainerFor("apple-music", core.Item{Kind: "playlist", ID: "p1", Title: "Morning"})
	store.AddRecentContainerFor("apple-music", core.Item{Kind: "unknown", ID: "l1", Title: "Road"})
	store.AddRecentContainerFor("apple-music", core.Item{Kind: "playlist", ID: "p1", Title: "Morning Mix"})
	if len(store.RecentContainers) != 1 || store.RecentContainers[0].ID != "am:p1" || store.RecentContainers[0].Title != "Morning Mix" {
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
	if store == nil || len(store.Recent) != 0 || len(store.Favorites["apple-music"]) != 0 {
		t.Fatalf("store = %#v", store)
	}
}

func TestV1MigrationIsIdempotentAndStripsAudiusURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// A legacy radio favorite without its own source must stay radio (the map
	// key is authoritative), not be rewritten as Apple Music. Containers carry
	// a legacy "playlist:<id>" identity that canonicalizes to "am:<id>".
	legacy := `{"version":1,"lastSource":"radio","unknownField":42,"favorites":{"appleMusic":[{"id":"am:1","kind":"song"}],"radio":[{"id":"radio:https://RADIO.example/x/","url":"https://RADIO.example/x/"}],"audius":[{"id":"audius:song:t1","source":"audius","kind":"song","url":"https://signed.example/token"}]},"recent":[{"id":"audius:playlist:p1","source":"audius","kind":"playlist","url":"https://signed.example/token"}],"recentContainers":[{"id":"playlist:p1","kind":"playlist"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Version != 2 || store.LastPlaybackSource != "radio" {
		t.Fatalf("migration=%#v", store)
	}
	if radio := store.Favorites["radio"][0]; radio.Source != "radio" || radio.Kind != "stream" || radio.ID != "radio:https://radio.example/x" {
		t.Fatalf("radio favorite=%#v", radio)
	}
	if container := store.RecentContainers[0]; container.Source != "apple-music" || container.ID != "am:p1" {
		t.Fatalf("container=%#v", container)
	}
	if audius := store.Favorites["audius"][0]; audius.Source != "audius" || audius.Kind != "song" || ProviderID(audius.Source, audius.ID) != "t1" || audius.URL != "" {
		t.Fatalf("audius favorite=%#v", audius)
	}
	if recent := store.Recent[0]; recent.Source != "audius" || recent.Kind != "playlist" || ProviderID(recent.Source, recent.ID) != "p1" || recent.URL != "" {
		t.Fatalf("recent=%#v", recent)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 || len(loaded.Favorites["apple-music"]) != 1 {
		t.Fatalf("round trip=%#v", loaded)
	}
	if audius := loaded.Favorites["audius"][0]; audius.Source != "audius" || audius.Kind != "song" || ProviderID(audius.Source, audius.ID) != "t1" || audius.URL != "" {
		t.Fatalf("audius round trip=%#v", audius)
	}
	if radio := loaded.Favorites["radio"][0]; radio.Source != "radio" || radio.Kind != "stream" {
		t.Fatalf("radio round trip=%#v", radio)
	}
	// Unknown fields are tolerated on read and dropped on the next save.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "unknownField") {
		t.Fatalf("unknown field persisted: %s", data)
	}
}

func TestV1AppleURLFormIDKeepsItsScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{"version":1,"favorites":{"appleMusic":[{"id":"am:https://music.apple.com/us/song/x/1","kind":"song"}]},"recent":[{"id":"https://music.apple.com/us/song/y/2","source":"apple-music"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Favorites["apple-music"][0].ID; got != "am:https://music.apple.com/us/song/x/1" {
		t.Fatalf("favorite id = %q, want scheme preserved", got)
	}
	if got := store.Favorites["apple-music"][0].Kind; got != "song" {
		t.Fatalf("favorite kind = %q, want song", got)
	}
	if got := store.Recent[0].ID; got != "am:https://music.apple.com/us/song/y/2" {
		t.Fatalf("recent id = %q, want scheme preserved", got)
	}
}

func TestV2ReloadIsIdempotentForAudiusContainerAndSourceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	store.RecentContainers = []RecentContainer{
		{ID: "audius:playlist:p1", Source: "audius", Kind: "playlist", Title: "Mix"},
		{ID: "playlist:p2", Source: "apple-music", Kind: "playlist", Title: "Apple"},
	}
	store.Favorites = Favorites{
		"audius": {{ID: "audius:song:t1", Source: "apple-music", Kind: "song"}},
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		loaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.RecentContainers[0].ID; got != "audius:playlist:p1" {
			t.Fatalf("audius container id = %q, want unchanged", got)
		}
		if got := loaded.RecentContainers[1].ID; got != "am:p2" {
			t.Fatalf("apple container id = %q, want am:p2", got)
		}
		if got := loaded.Favorites["audius"][0]; got.Source != "audius" || got.ID != "audius:song:t1" {
			t.Fatalf("favorites key is not authoritative: %#v", got)
		}
		if err := loaded.Save(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAddRecentContainerForPreservesSource(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	store.AddRecentContainerFor("audius", core.Item{Kind: "playlist", ID: "audius:playlist:p1", Title: "Mix"})
	store.AddRecentContainerFor("apple-music", core.Item{Kind: "playlist", ID: "p2", Title: "Apple"})
	byID := map[string]RecentContainer{}
	for _, container := range store.RecentContainers {
		byID[container.ID] = container
	}
	if got := byID["audius:playlist:p1"]; got.Source != "audius" {
		t.Fatalf("audius container = %#v", got)
	}
	if got := byID["am:p2"]; got.Source != "apple-music" {
		t.Fatalf("apple container = %#v", got)
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

func TestRecentForFiltersBySource(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	store.AddRecent("radio", core.Item{Kind: "stream", URL: "https://radio.example/a", Title: "A"})
	store.AddRecent("apple-music", core.Item{Kind: "song", ID: "1", Title: "S"})
	items := store.RecentFor("radio")
	if len(items) != 1 || items[0].Title != "A" || items[0].Kind != "stream" {
		t.Fatalf("recent = %#v", items)
	}
}

func TestAddRecentRejectsEmptyTitles(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	store.AddRecent("radio", core.Item{Kind: "stream"})
	if len(store.Recent) != 0 {
		t.Fatalf("recent = %#v", store.Recent)
	}
	store.AddRecentContainerFor("apple-music", core.Item{Kind: "playlist"})
	if len(store.RecentContainers) != 0 {
		t.Fatalf("containers = %#v", store.RecentContainers)
	}
	store.AddRecent("apple-music", core.Item{Kind: "song", ID: "am:1", Title: "Keep"})
	if len(store.Recent) != 1 || store.Recent[0].Title != "Keep" {
		t.Fatalf("recent = %#v", store.Recent)
	}
}

// The terminal-following ANSI palette was removed; persisted names resolve to
// the built-in gruvbox palette on load.
func TestThemeResolvesToGruvbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for _, theme := range []string{"", "default"} {
		raw := `{"version":2,"theme":"` + theme + `"}`
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		store, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if store.Theme != "gruvbox" {
			t.Fatalf("theme %q resolved to %q, want gruvbox", theme, store.Theme)
		}
	}
	// A real name survives.
	if err := os.WriteFile(path, []byte(`{"version":2,"theme":"tokyo-night"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Theme != "tokyo-night" {
		t.Fatalf("real theme overwritten: %q", store.Theme)
	}
}
