package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsToGruvbox(t *testing.T) {
	loaded := Load("")
	if loaded.Name != "gruvbox" || loaded.Accent == "" || loaded.Green == "" || loaded.Selection == "" {
		t.Fatalf("default theme = %#v", loaded)
	}
}

func TestUserThemeOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LILT_CONFIG", dir)
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	content := "accent = \"#ffffff\"\nbright_fg = \"#eeeeee\"\nfg = \"#888888\"\ngreen = \"#00ff00\"\nyellow = \"#ffff00\"\nred = \"#ff0000\"\nselection = \"#333333\"\n"
	if err := os.WriteFile(filepath.Join(dir, "themes", "custom.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := Load("custom")
	if loaded.Name != "custom" || loaded.Accent != "#ffffff" || loaded.Selection != "#333333" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestNamesIncludeBuiltins(t *testing.T) {
	names := Names()
	for _, want := range []string{"gruvbox", "default"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s missing from %v", want, names)
		}
	}
}
