package theme

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadDefaultsToGruvbox(t *testing.T) {
	loaded := Load("")
	if loaded.Name != "gruvbox" || loaded.BG != "#282828" || loaded.Selection != "#3c3836" {
		t.Fatalf("default theme = %#v, want gruvbox", loaded)
	}
	// The removed terminal-following palette resolves to the default too.
	if Load("default").Name != "gruvbox" {
		t.Fatalf("removed default theme did not resolve to gruvbox")
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
	// Every documented snake_case key must reach its field: bright_fg used to be
	// dropped silently because no field name matches it.
	if loaded.BrightFG != "#eeeeee" || loaded.FG != "#888888" || loaded.Green != "#00ff00" || loaded.Yellow != "#ffff00" || loaded.Red != "#ff0000" {
		t.Fatalf("documented keys were dropped: %#v", loaded)
	}
}

func TestNamesIncludeBuiltins(t *testing.T) {
	names := Names()
	for _, want := range []string{"gruvbox", "tokyo-night", "broadcast-night", "print-room", "signal-red"} {
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

func TestBroadcastThemesHaveCompleteValidPalettes(t *testing.T) {
	for _, name := range []string{"broadcast-night", "print-room", "signal-red"} {
		loaded := Load(name)
		if loaded.Name != name || !validSuppliedColors(loaded) || loaded.BG == "" || loaded.Selection == "" {
			t.Fatalf("%s = %#v", name, loaded)
		}
	}
}

func TestInvalidCustomColorsFallBack(t *testing.T) {
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
}

// Border and selection are derived from the palette because the cliamp schema
// has no key for either (docs/ui/theme.md).
func TestDerivedBorderAndSelection(t *testing.T) {
	if got := mix("#ffffff", "#000000", 50); got != "#808080" {
		t.Fatalf("midpoint mix = %q", got)
	}
	if got := mix("#ffffff", "#000000", 0); got != "#ffffff" {
		t.Fatalf("mix at 0 = %q", got)
	}
	if got := mix("#ffffff", "#000000", 100); got != "#000000" {
		t.Fatalf("mix at 100 = %q", got)
	}
	// ANSI indices belong to the terminal emulator and cannot be blended.
	if got := mix("7", "#000000", 50); got != "7" {
		t.Fatalf("mix of ANSI colour = %q", got)
	}

	loaded := Load("gruvbox")
	border := Border(loaded)
	if border == loaded.FG || border == loaded.BG || !validColor(border) {
		t.Fatalf("border = %q, want a value between fg %q and bg %q", border, loaded.FG, loaded.BG)
	}
	if border != mix(loaded.FG, loaded.BG, borderMix) {
		t.Fatalf("border %q is not the documented derivation", border)
	}
}

// A theme that ships a bg but no selection must still show a cursor: the focus
// row has to lift off the canvas instead of vanishing into it.
func TestSelectionIsDerivedWhenMissing(t *testing.T) {
	t.Setenv("LILT_CONFIG", t.TempDir())
	for _, name := range Names() {
		loaded := Load(name)
		if loaded.BG == "" {
			if loaded.Selection != "" {
				t.Fatalf("%s has no bg but a selection: %#v", name, loaded)
			}
			continue
		}
		if loaded.Selection == "" || loaded.Selection == loaded.BG {
			t.Fatalf("%s selection %q is invisible against bg %q", name, loaded.Selection, loaded.BG)
		}
	}
}

// Built-in palettes must stay readable on their own canvas. Secondary text is
// the one that carries long strings (artists, hints, footer), so it clears the
// 4:1 floor together with every other text colour; a theme whose fg drops to its
// comment colour made artist names unreadable in practice.
func TestBuiltinPalettesStayReadable(t *testing.T) {
	t.Setenv("LILT_CONFIG", t.TempDir())
	for _, name := range Names() {
		loaded := Load(name)
		if loaded.BG == "" {
			continue // ANSI palettes own no canvas, so lilt cannot measure them.
		}
		for _, value := range []struct{ key, color string }{
			{"accent", loaded.Accent}, {"bright_fg", loaded.BrightFG}, {"fg", loaded.FG},
			{"green", loaded.Green}, {"yellow", loaded.Yellow}, {"red", loaded.Red},
		} {
			if ratio := contrast(value.color, loaded.BG); ratio < readableFloor {
				t.Fatalf("%s %s %s has %.2f:1 against bg %s, want >= %.1f:1", name, value.key, value.color, ratio, loaded.BG, readableFloor)
			}
		}
	}
}

// readableFloor is the contrast ratio every built-in palette text colour must
// clear against its own canvas (docs/ui/theme.md).
const readableFloor = 4.0

func contrast(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(value string) float64 {
	if !isHex(value) {
		return 0
	}
	channels := make([]float64, 3)
	for i := 0; i < 3; i++ {
		n, _ := strconv.ParseInt(value[1+i*2:3+i*2], 16, 32)
		channel := float64(n) / 255
		if channel <= 0.03928 {
			channels[i] = channel / 12.92
		} else {
			channels[i] = math.Pow((channel+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}

func TestCustomThemeWithoutSelectionLiftsOffItsCanvas(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LILT_CONFIG", dir)
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", "plain.toml"), []byte("bg = \"#101010\"\nbright_fg = \"#ffffff\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := Load("plain")
	// 12% of bright_fg #ffffff over bg #101010, i.e. a step that stays close to
	// the canvas instead of jumping to the text colour.
	if loaded.Selection != "#2d2d2d" {
		t.Fatalf("derived selection = %q, want #2d2d2d (bg %q)", loaded.Selection, loaded.BG)
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
