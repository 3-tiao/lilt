package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		next.LastSource = "must-not-leak"
	}); err == nil {
		t.Fatal("blocked save unexpectedly succeeded")
	}
	if store.LastSource == "must-not-leak" {
		t.Fatalf("failed mutation leaked into live store: %#v", store)
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
	if loaded.LastSource != "radio" {
		t.Fatalf("later save lost the mutation: %#v", loaded)
	}
}

func TestDurablePathsFollowStateRoot(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "nested", "state.json")
	t.Setenv("LILT_STATE", statePath)
	t.Setenv("LILT_ACTIVITY_DB", "")

	if got, want := ActivityPath(), filepath.Join(root, "nested", "activity.sqlite3"); got != want {
		t.Fatalf("ActivityPath = %q, want %q", got, want)
	}
	if got, want := LockPath(), filepath.Join(root, "nested", "server.lock"); got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}

	override := filepath.Join(root, "activity-override.sqlite3")
	t.Setenv("LILT_ACTIVITY_DB", override)
	if got := ActivityPath(); got != override {
		t.Fatalf("ActivityPath override = %q, want %q", got, override)
	}
}

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if store == nil || store.Theme != "gruvbox" {
		t.Fatalf("store = %#v", store)
	}
}

// Favorites, recent, and recentContainers moved to the Activity SQLite store.
// A pre-v3 state file loads cleanly: those fields are ignored on read and gone
// after the next save; preferences survive.
func TestV2ActivityFieldsAreDroppedOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	legacy := `{"version":2,"theme":"tokyo-night","lastSource":"radio","lastPlaybackSource":"radio","favorites":{"apple-music":[{"id":"am:1","kind":"song","title":"Old"}],"radio":[{"id":"radio:https://radio.example/x","kind":"stream","title":"Old Station"}]},"recent":[{"id":"am:2","source":"apple-music","kind":"song","title":"Old Play"}],"recentContainers":[{"id":"am:p1","source":"apple-music","kind":"playlist","title":"Old Mix"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Theme != "tokyo-night" || store.LastSource != "radio" || store.LastPlaybackSource != "radio" {
		t.Fatalf("preferences lost: %#v", store)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{"favorites", "recent", "recentContainers", "am:1", "Old Station", "Old Mix"} {
		if strings.Contains(text, field) {
			t.Fatalf("upgraded state still contains %q: %s", field, text)
		}
	}
	if !strings.Contains(text, `"version": 3`) {
		t.Fatalf("upgraded state version: %s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme != "tokyo-night" || loaded.LastPlaybackSource != "radio" {
		t.Fatalf("preference round trip: %#v", loaded)
	}
}

// v1 (git 95b67db..f9f5558) keyed favorites by camelCase source names, carried
// source-less recent containers, and had no lastPlaybackSource. It upgrades
// through the same drop-on-read path as v2: preferences survive, the account
// fields vanish, and the file becomes the current version on the next save.
func TestV1StateMigratesToCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	legacy := `{"version":1,"theme":"tokyo-night","lastSource":"radio","favorites":{"appleMusic":[{"id":"am:1","source":"apple-music","kind":"song","title":"Old","addedAt":"2026-01-02T03:04:05Z"}],"radio":[{"id":"radio:https://radio.example/x","source":"radio","kind":"stream","title":"Old Station","addedAt":"2026-01-02T03:04:05Z"}]},"recent":[{"id":"am:2","source":"apple-music","kind":"song","title":"Old Play","playedAt":"2026-01-02T03:04:05Z"}],"recentContainers":[{"id":"p1","kind":"playlist","title":"Old Mix","playedAt":"2026-01-02T03:04:05Z"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Theme != "tokyo-night" || store.LastSource != "radio" {
		t.Fatalf("v1 preferences lost: %#v", store)
	}
	if store.LastPlaybackSource != "" {
		t.Fatalf("v1 has no lastPlaybackSource to restore: %#v", store)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{"favorites", "recent", "recentContainers", "appleMusic", "am:1", "Old Station", "Old Mix"} {
		if strings.Contains(text, field) {
			t.Fatalf("upgraded v1 state still contains %q: %s", field, text)
		}
	}
	if !strings.Contains(text, `"version": 3`) {
		t.Fatalf("upgraded v1 state version: %s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme != "tokyo-night" || loaded.LastSource != "radio" {
		t.Fatalf("v1 preference round trip: %#v", loaded)
	}
}

// Every historical version the spec has declared must upgrade to the current
// one, and the upgrade must be idempotent: reloading and re-saving the upgraded
// file produces identical bytes, so a restarted server never rewrites state it
// already rewrote.
func TestHistoricalStateUpgradeIsIdempotentOnReload(t *testing.T) {
	v1 := `{"version":1,"theme":"tokyo-night","lastSource":"radio","favorites":{"appleMusic":[{"id":"am:1","source":"apple-music","kind":"song","title":"Old","addedAt":"2026-01-02T03:04:05Z"}],"radio":[{"id":"radio:https://radio.example/x","source":"radio","kind":"stream","title":"Old Station","addedAt":"2026-01-02T03:04:05Z"}]},"recent":[{"id":"am:2","source":"apple-music","kind":"song","title":"Old Play","playedAt":"2026-01-02T03:04:05Z"}],"recentContainers":[{"id":"p1","kind":"playlist","title":"Old Mix","playedAt":"2026-01-02T03:04:05Z"}]}`
	v2 := `{"version":2,"theme":"gruvbox","lastSource":"apple-music","lastPlaybackSource":"apple-music","favorites":{"apple-music":[{"id":"am:1","kind":"song","title":"Old"}],"radio":[{"id":"radio:https://radio.example/x","kind":"stream","title":"Old Station"}]},"recent":[{"id":"am:2","source":"apple-music","kind":"song","title":"Old Play"}],"recentContainers":[{"id":"am:p1","source":"apple-music","kind":"playlist","title":"Old Mix"}]}`
	for name, legacy := range map[string]string{"v1": v1, "v2": v2} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
				t.Fatal(err)
			}
			save := func() string {
				store, err := Load(path)
				if err != nil {
					t.Fatalf("reload upgraded %s state: %v", name, err)
				}
				if store.Version != version {
					t.Fatalf("upgraded %s version = %d, want %d", name, store.Version, version)
				}
				if err := store.Save(); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			first := save()
			for _, field := range []string{"favorites", "recent", "recentContainers", "appleMusic"} {
				if strings.Contains(first, field) {
					t.Fatalf("upgraded %s state still contains %q: %s", name, field, first)
				}
			}
			if second := save(); second != first {
				t.Fatalf("reloading the upgraded %s state rewrote it:\nfirst:  %s\nsecond: %s", name, first, second)
			}
		})
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

// The terminal-following ANSI palette was removed; persisted names resolve to
// the built-in gruvbox palette on load.
func TestThemeResolvesToGruvbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for _, theme := range []string{"", "default"} {
		raw := `{"version":3,"theme":"` + theme + `"}`
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
	if err := os.WriteFile(path, []byte(`{"version":3,"theme":"tokyo-night"}`), 0600); err != nil {
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
