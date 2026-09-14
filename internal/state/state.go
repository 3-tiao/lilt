// Package state stores lilt's local-first cross-platform state.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
)

const version = 1

type Favorite struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"`
	Kind    string    `json:"kind,omitempty"`
	Title   string    `json:"title,omitempty"`
	Artist  string    `json:"artist,omitempty"`
	URL     string    `json:"url,omitempty"`
	AddedAt time.Time `json:"addedAt"`
}

type Recent struct {
	ID       string    `json:"id"`
	Source   string    `json:"source"`
	Kind     string    `json:"kind,omitempty"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	URL      string    `json:"url,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

// RecentContainer records a playlist context explicitly started by lilt. Apple
// Music does not expose a reliable source-container for arbitrary playback.
type RecentContainer struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

type PresetRecord struct {
	Uses   int            `json:"uses"`
	Last   string         `json:"last,omitempty"`
	Chosen map[string]int `json:"chosen,omitempty"`
}

type Favorites struct {
	AppleMusic []Favorite `json:"appleMusic,omitempty"`
	Radio      []Favorite `json:"radio,omitempty"`
}

type Store struct {
	path             string
	saveBlocked      error
	Version          int                     `json:"version"`
	Theme            string                  `json:"theme,omitempty"`
	LastSource       string                  `json:"lastSource,omitempty"`
	Favorites        Favorites               `json:"favorites"`
	Recent           []Recent                `json:"recent,omitempty"`
	RecentContainers []RecentContainer       `json:"recentContainers,omitempty"`
	Presets          map[string]PresetRecord `json:"presets,omitempty"`
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

func New(path string) *Store {
	return &Store{path: path, Version: version, Presets: make(map[string]PresetRecord)}
}

// Snapshot returns an immutable deep copy suitable for ranking inside a Tea
// command while Model.Update remains the sole owner of the live Store.
func (s *Store) Snapshot() *Store {
	if s == nil {
		return nil
	}
	copyStore := *s
	copyStore.Favorites.AppleMusic = append([]Favorite(nil), s.Favorites.AppleMusic...)
	copyStore.Favorites.Radio = append([]Favorite(nil), s.Favorites.Radio...)
	copyStore.Recent = append([]Recent(nil), s.Recent...)
	copyStore.RecentContainers = append([]RecentContainer(nil), s.RecentContainers...)
	copyStore.Presets = make(map[string]PresetRecord, len(s.Presets))
	for key, record := range s.Presets {
		chosen := make(map[string]int, len(record.Chosen))
		for id, count := range record.Chosen {
			chosen[id] = count
		}
		record.Chosen = chosen
		copyStore.Presets[key] = record
	}
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
	if decoded.Presets == nil {
		decoded.Presets = make(map[string]PresetRecord)
	}
	decoded.normalizeRadioRecords()
	decoded.Version = version
	return decoded, nil
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

func (s *Store) normalizeRadioRecords() {
	seen := map[string]bool{}
	keptFavorites := s.Favorites.Radio[:0]
	for _, favorite := range s.Favorites.Radio {
		favorite.ID = "radio:" + normalizeURL(firstNonEmpty(favorite.URL, strings.TrimPrefix(favorite.ID, "radio:")))
		if seen[favorite.ID] {
			continue
		}
		seen[favorite.ID] = true
		keptFavorites = append(keptFavorites, favorite)
	}
	s.Favorites.Radio = keptFavorites
	seen = map[string]bool{}
	keptRecent := s.Recent[:0]
	for _, recent := range s.Recent {
		if recent.Source == "radio" {
			recent.ID = "radio:" + normalizeURL(firstNonEmpty(recent.URL, strings.TrimPrefix(recent.ID, "radio:")))
			if seen[recent.ID] {
				continue
			}
			seen[recent.ID] = true
		}
		keptRecent = append(keptRecent, recent)
	}
	s.Recent = keptRecent
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Store) Save() error {
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

// ItemID returns the stable cross-platform id for an item from a source.
func ItemID(source string, item core.Item) string {
	if source == "radio" {
		url := item.URL
		if url == "" {
			url = item.ID
		}
		return "radio:" + normalizeURL(url)
	}
	if item.ID != "" {
		return "am:" + item.ID
	}
	return "am:" + item.URL
}

func normalizeURL(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSuffix(value, "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

func (s *Store) favorites(source string) *[]Favorite {
	if source == "radio" {
		return &s.Favorites.Radio
	}
	return &s.Favorites.AppleMusic
}

func (s *Store) IsFavorite(source, id string) bool {
	for _, favorite := range *s.favorites(source) {
		if favorite.ID == id {
			return true
		}
	}
	return false
}

// ToggleFavorite flips the favorite state and reports whether it is now favorited.
func (s *Store) ToggleFavorite(source string, item core.Item) bool {
	id := ItemID(source, item)
	list := s.favorites(source)
	for i, favorite := range *list {
		if favorite.ID == id {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return false
		}
	}
	*list = append(*list, Favorite{ID: id, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: item.URL, AddedAt: time.Now().UTC()})
	return true
}

func (s *Store) FavoritesFor(source string) []core.Item {
	items := make([]core.Item, 0)
	for _, favorite := range *s.favorites(source) {
		items = append(items, core.Item{Kind: kindOr(favorite.Kind, "stream"), ID: favorite.ID, URL: favorite.URL, Title: favorite.Title, Artist: favorite.Artist})
	}
	return items
}

// RecentFor returns locally recorded plays for one source, newest first.
func (s *Store) RecentFor(source string) []core.Item {
	items := make([]core.Item, 0, len(s.Recent))
	for _, recent := range s.Recent {
		if recent.Source != source {
			continue
		}
		items = append(items, core.Item{Kind: kindOr(recent.Kind, "stream"), ID: recent.ID, URL: recent.URL, Title: recent.Title, Artist: recent.Artist})
	}
	return items
}

func (s *Store) AddRecent(source string, item core.Item) {
	id := ItemID(source, item)
	kept := s.Recent[:0]
	for _, recent := range s.Recent {
		if recent.ID != id {
			kept = append(kept, recent)
		}
	}
	s.Recent = append([]Recent{{ID: id, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: item.URL, PlayedAt: time.Now().UTC()}}, kept...)
	if len(s.Recent) > 100 {
		s.Recent = s.Recent[:100]
	}
}

// AddRecentContainer records an Apple playlist started by lilt.
// It is intentionally separate from song history because it restores a detail
// page, not an unavailable historical Apple Music queue.
func (s *Store) AddRecentContainer(item core.Item) {
	if item.Kind != "playlist" {
		return
	}
	id := item.Kind + ":" + item.ID
	kept := s.RecentContainers[:0]
	for _, recent := range s.RecentContainers {
		if recent.ID != id {
			kept = append(kept, recent)
		}
	}
	s.RecentContainers = append([]RecentContainer{{ID: id, Kind: item.Kind, Title: item.Title, PlayedAt: time.Now().UTC()}}, kept...)
	if len(s.RecentContainers) > 100 {
		s.RecentContainers = s.RecentContainers[:100]
	}
}

func (s *Store) Rank(key string, items []core.Item) []core.Item {
	ranked := append([]core.Item(nil), items...)
	if s == nil {
		return ranked
	}
	chosen := s.Presets[key].Chosen
	sort.SliceStable(ranked, func(i, j int) bool {
		return chosen[ranked[i].ID] > chosen[ranked[j].ID]
	})
	return ranked
}

func (s *Store) Record(key string, item core.Item) {
	if s == nil {
		return
	}
	if s.Presets == nil {
		s.Presets = make(map[string]PresetRecord)
	}
	record := s.Presets[key]
	if record.Chosen == nil {
		record.Chosen = make(map[string]int)
	}
	record.Uses++
	record.Chosen[item.ID]++
	record.Last = item.ID
	s.Presets[key] = record
}

func kindOr(kind, fallback string) string {
	if kind == "" {
		return fallback
	}
	return kind
}
