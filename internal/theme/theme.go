// Package theme loads lilt color themes using the cliamp TOML schema.
package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/BurntSushi/toml"
)

type Theme struct {
	Name      string
	BG        string
	Selection string
	Accent    string
	BrightFG  string
	FG        string
	Green     string
	Yellow    string
	Red       string
}

func Dir() string {
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
	// ANSI 0–15 are owned by the terminal emulator, so this palette follows
	// the active Terminal/iTerm/Kitty theme instead of imposing lilt colors.
	"default":     {Name: "default", Accent: "14", BrightFG: "15", FG: "7", Green: "10", Yellow: "11", Red: "9"},
	"gruvbox":     {Name: "gruvbox", BG: "#282828", Selection: "#3c3836", Accent: "#7daea3", BrightFG: "#d4be98", FG: "#a89984", Green: "#a9b665", Yellow: "#d8a657", Red: "#ea6962"},
	"tokyo-night": {Name: "tokyo-night", BG: "#1a1b26", Accent: "#7aa2f7", BrightFG: "#c0caf5", FG: "#565f89", Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e"},
	"catppuccin":  {Name: "catppuccin", BG: "#1e1e2e", Accent: "#89b4fa", BrightFG: "#cdd6f4", FG: "#7f849c", Green: "#a6e3a1", Yellow: "#f9e2af", Red: "#f38ba8"},
	"nord":        {Name: "nord", BG: "#2e3440", Accent: "#88c0d0", BrightFG: "#eceff4", FG: "#7b88a1", Green: "#a3be8c", Yellow: "#ebcb8b", Red: "#bf616a"},
	"dracula":     {Name: "dracula", BG: "#282a36", Accent: "#bd93f9", BrightFG: "#f8f8f2", FG: "#6272a4", Green: "#50fa7b", Yellow: "#f1fa8c", Red: "#ff5555"},
	"ayu-mirage":  {Name: "ayu-mirage", BG: "#1f2430", Accent: "#73d0ff", BrightFG: "#cbccc6", FG: "#707a8c", Green: "#bae67e", Yellow: "#ffd580", Red: "#f28779"},
	"rose-pine":   {Name: "rose-pine", BG: "#191724", Accent: "#c4a7e7", BrightFG: "#e0def4", FG: "#6e6a86", Green: "#9ccfd8", Yellow: "#f6c177", Red: "#eb6f92"},
	"everforest":  {Name: "everforest", BG: "#2d353b", Accent: "#7fbbb3", BrightFG: "#d3c6aa", FG: "#859289", Green: "#a7c080", Yellow: "#dbbc7f", Red: "#e67e80"},
	// Broadcast themes borrow the editorial contrast and restrained signal-red
	// palette of modern radio consoles; they are original lilt palettes.
	"broadcast-night": {Name: "broadcast-night", BG: "#161412", Selection: "#2a2420", Accent: "#f4f0e6", BrightFG: "#ece6dc", FG: "#9b948a", Green: "#7a2218", Yellow: "#d9a441", Red: "#e05252"},
	"print-room":      {Name: "print-room", BG: "#ece6dc", Selection: "#d9d2c4", Accent: "#7a2218", BrightFG: "#161412", FG: "#5e5951", Green: "#7a2218", Yellow: "#a36d15", Red: "#a72f28"},
	"signal-red":      {Name: "signal-red", BG: "#0c0a09", Selection: "#22201d", Accent: "#f4f0e6", BrightFG: "#ece6dc", FG: "#8a8478", Green: "#7a2218", Yellow: "#e0a33c", Red: "#ee625b"},
}

func Names() []string {
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	if dir := Dir(); dir != "" {
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
// An empty name selects the terminal-following built-in default theme.
func Load(name string) Theme {
	if path := filepath.Join(Dir(), name+".toml"); name != "" {
		var loaded Theme
		if _, err := toml.DecodeFile(path, &loaded); err == nil && validSuppliedColors(loaded) {
			loaded.Name = name
			return filled(loaded)
		}
	}
	if name == "" {
		return filled(builtins["default"])
	}
	if builtin, ok := builtins[name]; ok {
		return filled(builtin)
	}
	return filled(builtins["default"])
}

func validSuppliedColors(value Theme) bool {
	for _, color := range []string{value.BG, value.Selection, value.Accent, value.BrightFG, value.FG, value.Green, value.Yellow, value.Red} {
		if color != "" && !validColor(color) {
			return false
		}
	}
	return true
}

func filled(value Theme) Theme {
	base := builtins["default"]
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
		value.Selection = value.BG
	}
	return value
}

var colorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|[0-9]{1,3})$`)

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

// ActiveForeground returns black or white with sufficient contrast for a
// #RRGGBB active-tab background. ANSI colors use the configured fallback.
func ActiveForeground(background, fallback string) string {
	if len(background) != 7 || background[0] != '#' {
		if validColor(fallback) {
			return fallback
		}
		return "0"
	}
	r, _ := strconv.ParseInt(background[1:3], 16, 64)
	g, _ := strconv.ParseInt(background[3:5], 16, 64)
	b, _ := strconv.ParseInt(background[5:7], 16, 64)
	// WCAG relative-luminance threshold commonly used for black/white text.
	if 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) > 145 {
		return "#000000"
	}
	return "#ffffff"
}

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
