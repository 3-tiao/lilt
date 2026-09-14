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

func TestInvalidCustomColorsFallBackAndForegroundIsReadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LILT_CONFIG", dir)
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", "bad.toml"), []byte("accent = \"red;escape\"\ngreen = \"#ffffff\"\nfg = \"999\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := Load("bad")
	if loaded.Name == "bad" || loaded.Accent == "red;escape" || loaded.FG == "999" {
		t.Fatalf("malformed custom theme was not rejected: %#v", loaded)
	}
	if got := ActiveForeground("#ffffff", "#111111"); got != "#000000" {
		t.Fatalf("white foreground = %q", got)
	}
	if got := ActiveForeground("#000000", "#eeeeee"); got != "#ffffff" {
		t.Fatalf("black foreground = %q", got)
	}
}

func TestValidPartialThemeWithoutAccentIsFilled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LILT_CONFIG", dir)
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", "partial.toml"), []byte("green = \"#123456\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := Load("partial")
	if loaded.Name != "partial" || loaded.Green != "#123456" || loaded.Accent == "" || loaded.FG == "" {
		t.Fatalf("partial theme = %#v", loaded)
	}
}

func TestLegacyLILTConfigFileDoesNotBecomeThemeDirectory(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "presets.toml")
	if err := os.WriteFile(legacy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(root, "xdg")
	t.Setenv("LILT_CONFIG", legacy)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got := Dir(); got != filepath.Join(xdg, "lilt", "themes") {
		t.Fatalf("Dir = %q", got)
	}
}
