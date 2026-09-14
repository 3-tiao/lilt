package presets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/state"
)

func TestLoadDefaultsAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.toml")
	content := `[jazz]
query = "jazz classics"
kind = "playlist"

[focus]
label = "Focus Lofi"
query = "lofi focus"

[empty]
label = "Ignored"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded = %#v, want 2", loaded)
	}
	if loaded[0].Key != "focus" || loaded[0].Label != "Focus Lofi" || loaded[0].Kind != "song" {
		t.Fatalf("focus = %#v", loaded[0])
	}
	if loaded[1].Key != "jazz" || loaded[1].Kind != "playlist" || loaded[1].Label != "jazz" {
		t.Fatalf("jazz = %#v", loaded[1])
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	loaded, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil || loaded != nil {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
}

func TestConfigRootAndExplicitPresetPath(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "lilt-config")
	t.Setenv("LILT_CONFIG", configDir)
	t.Setenv("LILT_PRESETS", "")
	if got := Path(); got != filepath.Join(configDir, "presets.toml") {
		t.Fatalf("Path = %q", got)
	}
	explicit := filepath.Join(dir, "custom.toml")
	t.Setenv("LILT_PRESETS", explicit)
	if got := Path(); got != explicit {
		t.Fatalf("explicit Path = %q", got)
	}
}

func TestExistingLILTConfigFileRetainsDeprecatedPresetSemantics(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy-presets.toml")
	if err := os.WriteFile(legacy, []byte("[focus]\nquery = \"focus\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LILT_CONFIG", legacy)
	t.Setenv("LILT_PRESETS", "")
	if got := Path(); got != legacy {
		t.Fatalf("Path = %q, want legacy file %q", got, legacy)
	}
	loaded, err := Load(Path())
	if err != nil || len(loaded) != 1 || loaded[0].Key != "focus" {
		t.Fatalf("legacy presets = %#v, %v", loaded, err)
	}
}

type fakeProvider struct {
	kind  string
	items []core.Item
}

func (f *fakeProvider) Search(context.Context, string, int) ([]core.Item, error) {
	f.kind = "song"
	return f.items, nil
}
func (f *fakeProvider) SearchPlaylists(context.Context, string, int) ([]core.Item, error) {
	f.kind = "playlist"
	return f.items, nil
}
func (f *fakeProvider) Stations(context.Context, string, int) ([]core.Item, error) {
	f.kind = "station"
	return f.items, nil
}

func TestResolveRoutesByKindAndRanks(t *testing.T) {
	first := core.Item{Kind: "playlist", ID: "first"}
	second := core.Item{Kind: "playlist", ID: "second"}
	provider := &fakeProvider{items: []core.Item{first, second}}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	store.Record("jazz", second)
	store.Record("jazz", second)

	resolved, err := Resolve(context.Background(), provider, store, Preset{Key: "jazz", Query: "jazz classics", Kind: "playlist"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.kind != "playlist" {
		t.Fatalf("provider kind = %q, want playlist", provider.kind)
	}
	if resolved.ID != "second" {
		t.Fatalf("resolved = %#v, want second", resolved)
	}
	if got := store.Presets["jazz"].Uses; got != 2 {
		t.Fatalf("Resolve mutated store uses to %d", got)
	}

	for kind, want := range map[string]string{"station": "station", "song": "song"} {
		provider := &fakeProvider{items: []core.Item{{Kind: "song", ID: "1"}}}
		if _, err := Resolve(context.Background(), provider, nil, Preset{Key: "k", Query: "q", Kind: kind}); err != nil {
			t.Fatal(err)
		}
		if provider.kind != want {
			t.Fatalf("kind %q routed to %q", kind, provider.kind)
		}
	}
}

func TestResolveNoCandidates(t *testing.T) {
	_, err := Resolve(context.Background(), &fakeProvider{}, nil, Preset{Key: "k", Query: "q", Kind: "song"})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}
