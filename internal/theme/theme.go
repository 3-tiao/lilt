// Package theme loads lilt color themes using the cliamp TOML schema.
package theme

import (
	"os"
	"path/filepath"
	"sort"

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
	"default":     {Name: "default", Accent: "81", BrightFG: "255", FG: "245", Green: "82", Yellow: "214", Red: "203"},
	"gruvbox":     {Name: "gruvbox", BG: "#282828", Selection: "#3c3836", Accent: "#7daea3", BrightFG: "#d4be98", FG: "#a89984", Green: "#a9b665", Yellow: "#d8a657", Red: "#ea6962"},
	"tokyo-night": {Name: "tokyo-night", BG: "#1a1b26", Accent: "#7aa2f7", BrightFG: "#c0caf5", FG: "#565f89", Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e"},
	"catppuccin":  {Name: "catppuccin", BG: "#1e1e2e", Accent: "#89b4fa", BrightFG: "#cdd6f4", FG: "#7f849c", Green: "#a6e3a1", Yellow: "#f9e2af", Red: "#f38ba8"},
	"nord":        {Name: "nord", BG: "#2e3440", Accent: "#88c0d0", BrightFG: "#eceff4", FG: "#7b88a1", Green: "#a3be8c", Yellow: "#ebcb8b", Red: "#bf616a"},
	"dracula":     {Name: "dracula", BG: "#282a36", Accent: "#bd93f9", BrightFG: "#f8f8f2", FG: "#6272a4", Green: "#50fa7b", Yellow: "#f1fa8c", Red: "#ff5555"},
	"ayu-mirage":  {Name: "ayu-mirage", BG: "#1f2430", Accent: "#73d0ff", BrightFG: "#cbccc6", FG: "#707a8c", Green: "#bae67e", Yellow: "#ffd580", Red: "#f28779"},
	"rose-pine":   {Name: "rose-pine", BG: "#191724", Accent: "#c4a7e7", BrightFG: "#e0def4", FG: "#6e6a86", Green: "#9ccfd8", Yellow: "#f6c177", Red: "#eb6f92"},
	"everforest":  {Name: "everforest", BG: "#2d353b", Accent: "#7fbbb3", BrightFG: "#d3c6aa", FG: "#859289", Green: "#a7c080", Yellow: "#dbbc7f", Red: "#e67e80"},
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
// An empty name selects the built-in gruvbox theme.
func Load(name string) Theme {
	if path := filepath.Join(Dir(), name+".toml"); name != "" {
		var loaded Theme
		if _, err := toml.DecodeFile(path, &loaded); err == nil && loaded.Accent != "" {
			loaded.Name = name
			return filled(loaded)
		}
	}
	if name == "" {
		return filled(builtins["gruvbox"])
	}
	if builtin, ok := builtins[name]; ok {
		return filled(builtin)
	}
	return filled(builtins["default"])
}

func filled(value Theme) Theme {
	base := builtins["default"]
	if value.Accent == "" {
		value.Accent = base.Accent
	}
	if value.BrightFG == "" {
		value.BrightFG = base.BrightFG
	}
	if value.FG == "" {
		value.FG = base.FG
	}
	if value.Green == "" {
		value.Green = base.Green
	}
	if value.Yellow == "" {
		value.Yellow = base.Yellow
	}
	if value.Red == "" {
		value.Red = base.Red
	}
	if value.Selection == "" {
		value.Selection = value.BG
	}
	return value
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
