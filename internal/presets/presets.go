package presets

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

type Preset struct {
	Key   string
	Label string
	Query string
	Kind  string
}

func Path() string {
	if path := os.Getenv("LILT_PRESETS"); path != "" {
		return path
	}
	if path := os.Getenv("LILT_CONFIG"); path != "" {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			// Deprecated compatibility: LILT_CONFIG originally named the preset
			// file. Existing regular files retain that meaning; new/non-file paths
			// are configuration directories.
			return path
		}
		return filepath.Join(path, "presets.toml")
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "lilt", "presets.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "lilt", "presets.toml")
	}
	return filepath.Join(home, ".config", "lilt", "presets.toml")
}

func Load(path string) ([]Preset, error) {
	var entries map[string]struct {
		Label string `toml:"label"`
		Query string `toml:"query"`
		Kind  string `toml:"kind"`
	}
	if _, err := toml.DecodeFile(path, &entries); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	presets := make([]Preset, 0, len(entries))
	for key, entry := range entries {
		if entry.Query == "" {
			continue
		}
		if entry.Label == "" {
			entry.Label = key
		}
		if entry.Kind == "" {
			entry.Kind = "song"
		}
		presets = append(presets, Preset{Key: key, Label: entry.Label, Query: entry.Query, Kind: entry.Kind})
	}
	sort.Slice(presets, func(i, j int) bool { return presets[i].Key < presets[j].Key })
	return presets, nil
}
