// Package state stores lilt's local-first UI preferences (state.json).
// Favorites, playback history, and derived recent data live in the Activity
// SQLite store owned by the server; see docs/internals/local-activity.md.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const version = 3

type Store struct {
	path               string
	memory             bool
	saveBlocked        error
	Version            int    `json:"version"`
	Theme              string `json:"theme,omitempty"`
	LastSource         string `json:"lastSource,omitempty"`
	LastPlaybackSource string `json:"lastPlaybackSource,omitempty"`
}

// NewMemory returns an in-process mirror that never writes to disk. Clients use
// it for display state; the server owns persistence.
func NewMemory() *Store {
	store := New("")
	store.memory = true
	return store
}

func Path() string {
	if path := os.Getenv("LILT_STATE"); path != "" {
		return path
	}
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "lilt", "state.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state", "lilt", "state.json")
	}
	return filepath.Join(home, ".local", "state", "lilt", "state.json")
}

// ActivityPath returns the durable Activity database path. It follows the
// state root, not the disposable socket/cache root.
func ActivityPath() string {
	if path := os.Getenv("LILT_ACTIVITY_DB"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(Path()), "activity.sqlite3")
}

// LockPath returns the per-user lifecycle lock path. Deriving it from the
// state root preserves the single-writer invariant even when LILT_SOCKET is
// overridden.
func LockPath() string {
	return filepath.Join(filepath.Dir(Path()), "server.lock")
}

func New(path string) *Store {
	// Fresh states carry the built-in default palette by name, so the persisted
	// theme field is always a real theme.
	return &Store{path: path, Version: version, Theme: "gruvbox"}
}

// Snapshot returns an immutable copy suitable for ranking inside a Tea command
// while Model.Update remains the sole owner of the live Store.
func (s *Store) Snapshot() *Store {
	if s == nil {
		return nil
	}
	copyStore := *s
	return &copyStore
}

func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(path), nil
	}
	if err != nil {
		return nil, err
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return quarantineCorrupt(path, data, err)
	}
	if header.Version > version {
		blocked := fmt.Errorf("state version %d is newer than supported version %d", header.Version, version)
		store := New(path)
		store.saveBlocked = blocked
		return store, blocked
	}
	decoded := New(path)
	if err := json.Unmarshal(data, decoded); err != nil {
		return quarantineCorrupt(path, data, err)
	}
	decoded.migrateAndNormalize()
	decoded.Version = version
	return decoded, nil
}

func (s *Store) migrateAndNormalize() {
	// Decoding a pre-v3 state ignores favorites/recent fields (unknown JSON
	// fields are dropped); the Activity store owns that data now. The
	// terminal-following ANSI palette was removed; its name (and an unset
	// theme) resolve to the built-in gruvbox palette.
	if s.Theme == "" || s.Theme == "default" {
		s.Theme = "gruvbox"
	}
}

func (s *Store) Save() error {
	if s.memory {
		return nil
	}
	if s.saveBlocked != nil {
		return s.saveBlocked
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".state-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

// UpdateAndSave mutates a deep snapshot and only publishes it to the live Store
// after the snapshot has been successfully replaced on disk.
func (s *Store) UpdateAndSave(mutate func(*Store)) error {
	if s == nil {
		return errors.New("state store is nil")
	}
	next := s.Snapshot()
	mutate(next)
	if err := next.Save(); err != nil {
		return err
	}
	*s = *next
	return nil
}

func quarantineCorrupt(path string, data []byte, decodeErr error) (*Store, error) {
	store := New(path)
	prefix := filepath.Base(path) + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z") + "-"
	backup, err := os.CreateTemp(filepath.Dir(path), prefix)
	if err != nil {
		store.saveBlocked = fmt.Errorf("state is corrupt and could not be quarantined; saves are blocked: %w", err)
		return store, fmt.Errorf("invalid state at %s; original retained and saves blocked: %w", path, decodeErr)
	}
	quarantine := backup.Name()
	cleanup := func(failure error) (*Store, error) {
		_ = backup.Close()
		_ = os.Remove(quarantine)
		store.saveBlocked = fmt.Errorf("state is corrupt and could not be quarantined; saves are blocked: %w", failure)
		return store, fmt.Errorf("invalid state at %s; original retained and saves blocked: %w", path, decodeErr)
	}
	if err := backup.Chmod(0600); err != nil {
		return cleanup(err)
	}
	if _, err := backup.Write(data); err != nil {
		return cleanup(err)
	}
	if err := backup.Sync(); err != nil {
		return cleanup(err)
	}
	if err := backup.Close(); err != nil {
		_ = os.Remove(quarantine)
		store.saveBlocked = fmt.Errorf("state is corrupt and could not be quarantined; saves are blocked: %w", err)
		return store, fmt.Errorf("invalid state at %s; original retained and saves blocked: %w", path, decodeErr)
	}
	if err := os.Remove(path); err != nil {
		store.saveBlocked = fmt.Errorf("state backup exists but original could not be removed; saves are blocked: %w", err)
		return store, fmt.Errorf("invalid state at %s; backup is at %s and saves are blocked: %w", path, quarantine, decodeErr)
	}
	return store, fmt.Errorf("invalid state quarantined at %s: %w", quarantine, decodeErr)
}
