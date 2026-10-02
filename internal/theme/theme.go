// Package theme loads lilt color themes using the cliamp TOML schema.
package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/BurntSushi/toml"
)

// Theme is the cliamp palette schema: eight keys, decoded straight from the
// user's TOML file. Field names do not match the snake_case keys, so every key
// carries an explicit tag.
type Theme struct {
	Name      string `toml:"-"`
	BG        string `toml:"bg"`
	Selection string `toml:"selection"`
	Accent    string `toml:"accent"`
	BrightFG  string `toml:"bright_fg"`
	FG        string `toml:"fg"`
	Green     string `toml:"green"`
	Yellow    string `toml:"yellow"`
	Red       string `toml:"red"`
}

func themeDir() string {
	if path := os.Getenv("LILT_CONFIG"); path != "" {
		return filepath.Join(path, "themes")
	}
	return defaultDir()
}

func defaultDir() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "lilt", "themes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "lilt", "themes")
	}
	return filepath.Join(home, ".config", "lilt", "themes")
}

var builtins = map[string]Theme{
	// Every named palette below is the upstream theme's own values. Where an
	// upstream colour for long-form secondary text (comments, gutter text) sits
	// under 4:1 against its canvas, it is blended toward that theme's own
	// foreground until it clears the floor; the value stays recognisably the
	// theme's. TestBuiltinPalettesStayReadable keeps the floor honest.
	//
	// gruvbox is also the fallback palette: Load("") returns it, and partial
	// custom themes fill missing keys from it.
	"gruvbox":     {Name: "gruvbox", BG: "#282828", Selection: "#3c3836", Accent: "#8ec07c", BrightFG: "#ebdbb2", FG: "#a89984", Green: "#b8bb26", Yellow: "#fabd2f", Red: "#fb4934"},
	"tokyo-night": {Name: "tokyo-night", BG: "#1a1b26", Selection: "#292e42", Accent: "#7aa2f7", BrightFG: "#c0caf5", FG: "#a9b1d6", Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e"},
	"catppuccin":  {Name: "catppuccin", BG: "#1e1e2e", Selection: "#313244", Accent: "#89b4fa", BrightFG: "#cdd6f4", FG: "#9399b2", Green: "#a6e3a1", Yellow: "#f9e2af", Red: "#f38ba8"},
	"nord":        {Name: "nord", BG: "#2e3440", Selection: "#3b4252", Accent: "#88c0d0", BrightFG: "#eceff4", FG: "#929aaa", Green: "#a3be8c", Yellow: "#ebcb8b", Red: "#c5808a"},
	"dracula":     {Name: "dracula", BG: "#282a36", Selection: "#44475a", Accent: "#bd93f9", BrightFG: "#f8f8f2", FG: "#8894b8", Green: "#50fa7b", Yellow: "#f1fa8c", Red: "#ff5555"},
	"ayu-mirage":  {Name: "ayu-mirage", BG: "#1f2430", Selection: "#343f4c", Accent: "#73d0ff", BrightFG: "#cbccc6", FG: "#878f9b", Green: "#bae67e", Yellow: "#ffd580", Red: "#f28779"},
	"rose-pine":   {Name: "rose-pine", BG: "#191724", Selection: "#26233a", Accent: "#c4a7e7", BrightFG: "#e0def4", FG: "#908caa", Green: "#9ccfd8", Yellow: "#f6c177", Red: "#eb6f92"},
	"everforest":  {Name: "everforest", BG: "#2d353b", Selection: "#3d484d", Accent: "#7fbbb3", BrightFG: "#d3c6aa", FG: "#9da9a0", Green: "#a7c080", Yellow: "#dbbc7f", Red: "#e67e80"},
	// Broadcast themes borrow the editorial contrast and restrained signal-red
	// palette of modern radio consoles; they are original lilt palettes. Their
	// green is a console "on air" green rather than a second red, because green
	// now paints the playing text itself.
	"broadcast-night": {Name: "broadcast-night", BG: "#161412", Selection: "#2a2420", Accent: "#f4f0e6", BrightFG: "#ece6dc", FG: "#9b948a", Green: "#8aa96b", Yellow: "#d9a441", Red: "#e05252"},
	"print-room":      {Name: "print-room", BG: "#ece6dc", Selection: "#d9d2c4", Accent: "#7a2218", BrightFG: "#161412", FG: "#5e5951", Green: "#3d6b4a", Yellow: "#805714", Red: "#a72f28"},
	"signal-red":      {Name: "signal-red", BG: "#0c0a09", Selection: "#22201d", Accent: "#f4f0e6", BrightFG: "#ece6dc", FG: "#8a8478", Green: "#8aa96b", Yellow: "#e0a33c", Red: "#ee625b"},
}

func Names() []string {
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	if dir := themeDir(); dir != "" {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				if name, ok := themeName(entry.Name()); ok {
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	return dedupe(names)
}

// Load returns the named theme from the user theme directory, then built-ins.
// An empty name selects gruvbox, the built-in default palette.
func Load(name string) Theme {
	if path := filepath.Join(themeDir(), name+".toml"); name != "" {
		var loaded Theme
		if _, err := toml.DecodeFile(path, &loaded); err == nil && validSuppliedColors(loaded) {
			loaded.Name = name
			return filled(loaded)
		}
	}
	if name == "" || name == removedANSIDefault {
		return filled(builtins[defaultName])
	}
	if builtin, ok := builtins[name]; ok {
		return filled(builtin)
	}
	return filled(builtins[defaultName])
}

const (
	// defaultName is the palette every state resolves to when no theme is set.
	defaultName = "gruvbox"
	// removedANSIDefault was the old terminal-following palette; persisted
	// states that still name it resolve to defaultName through state migration.
	removedANSIDefault = "default"
)

func validSuppliedColors(value Theme) bool {
	for _, color := range []string{value.BG, value.Selection, value.Accent, value.BrightFG, value.FG, value.Green, value.Yellow, value.Red} {
		if color != "" && !validColor(color) {
			return false
		}
	}
	return true
}

func filled(value Theme) Theme {
	base := builtins[defaultName]
	if !validColor(value.Accent) {
		value.Accent = base.Accent
	}
	if !validColor(value.BrightFG) {
		value.BrightFG = base.BrightFG
	}
	if !validColor(value.FG) {
		value.FG = base.FG
	}
	if !validColor(value.Green) {
		value.Green = base.Green
	}
	if !validColor(value.Yellow) {
		value.Yellow = base.Yellow
	}
	if !validColor(value.Red) {
		value.Red = base.Red
	}
	if value.Selection != "" && !validColor(value.Selection) {
		value.Selection = ""
	}
	if value.BG != "" && !validColor(value.BG) {
		value.BG = ""
	}
	if value.Selection == "" {
		value.Selection = derivedSelection(value.BrightFG, value.BG)
	}
	return value
}

// derivedSelection lifts the canvas by one step for themes that ship a bg but no
// selection colour. ANSI palettes own no canvas to lift off, so they keep
// reverse video as their only cursor highlight.
func derivedSelection(brightFG, bg string) string {
	if !isHex(brightFG) || !isHex(bg) {
		return ""
	}
	return mix(bg, brightFG, selectionElevation)
}

var colorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|[0-9]{1,3})$`)

const (
	// selectionElevation is how far the focus row lifts off the canvas.
	selectionElevation = 12
	// borderMix is how far panel frames recede toward the canvas.
	borderMix = 50
)

func validColor(value string) bool {
	if !colorPattern.MatchString(value) {
		return false
	}
	if value[0] == '#' {
		return true
	}
	n, err := strconv.Atoi(value)
	return err == nil && n <= 255
}

func isHex(value string) bool { return len(value) == 7 && value[0] == '#' }

// mix blends two #RRGGBB colours, percent counting toward `to` (0 keeps `from`,
// 100 returns `to`). ANSI-index or malformed input returns `from` unchanged:
// those colours belong to the terminal emulator, so lilt cannot blend them.
func mix(from, to string, percent int) string {
	if !isHex(from) || !isHex(to) {
		return from
	}
	blended := []byte{'#'}
	for i := 1; i < 7; i += 2 {
		a, err := strconv.ParseInt(from[i:i+2], 16, 32)
		if err != nil {
			return from
		}
		b, err := strconv.ParseInt(to[i:i+2], 16, 32)
		if err != nil {
			return from
		}
		channel := (int(a)*(100-percent) + int(b)*percent + 50) / 100
		blended = append(blended, fmt.Sprintf("%02x", min(max(channel, 0), 255))...)
	}
	return string(blended)
}

// Border is the panel frame colour: secondary text blended toward the canvas so
// structure stays quieter than the rows it contains. The cliamp schema has no
// border key, so it is derived rather than configured.
func Border(value Theme) string { return mix(value.FG, value.BG, borderMix) }

func themeName(filename string) (string, bool) {
	if filepath.Ext(filename) != ".toml" {
		return "", false
	}
	name := filename[:len(filename)-len(".toml")]
	return name, name != ""
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}
	return kept
}
