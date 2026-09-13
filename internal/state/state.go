// Package state stores lilt's local-first cross-platform state.
package state

import (
	"encoding/json"
	"fmt"
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

type PresetRecord struct {
	Uses   int            `json:"uses"`
	Last   string         `json:"last,omitempty"`
	Chosen map[string]int `json:"chosen,omitempty"`
}

type Favorites struct {
	AppleMusic []Favorite `json:"appleMusic,omitempty"`
	Radio      []Favorite `json:"radio,omitempty"`
}

type LocalTrack struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	URL    string `json:"url,omitempty"`
}

type LocalPlaylist struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Items     []LocalTrack `json:"items"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

type Store struct {
	path       string
	Version    int                     `json:"version"`
	Theme      string                  `json:"theme,omitempty"`
	LastSource string                  `json:"lastSource,omitempty"`
	Favorites  Favorites               `json:"favorites"`
	Recent     []Recent                `json:"recent,omitempty"`
	Presets    map[string]PresetRecord `json:"presets,omitempty"`
	Playlists  []LocalPlaylist         `json:"playlists,omitempty"`
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

func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(path), nil
	}
	if err != nil {
		return nil, err
	}
	store := New(path)
	if err := json.Unmarshal(data, store); err != nil {
		return nil, err
	}
	if store.Presets == nil {
		store.Presets = make(map[string]PresetRecord)
	}
	store.Version = version
	return store, nil
}

func (s *Store) Save() error {
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
	value = strings.TrimSuffix(value, "/")
	return value
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

func newListID() string {
	return fmt.Sprintf("list-%d", time.Now().UnixNano())
}

func (s *Store) LocalPlaylists() []LocalPlaylist {
	return s.Playlists
}

func (s *Store) LocalPlaylist(id string) (LocalPlaylist, bool) {
	for _, list := range s.Playlists {
		if list.ID == id {
			return list, true
		}
	}
	return LocalPlaylist{}, false
}

func (s *Store) SaveQueue(name string, items []LocalTrack) LocalPlaylist {
	now := time.Now().UTC()
	list := LocalPlaylist{ID: newListID(), Name: strings.TrimSpace(name), Items: items, CreatedAt: now, UpdatedAt: now}
	s.Playlists = append(s.Playlists, list)
	return list
}

func (s *Store) AddToLocalPlaylist(name string, item LocalTrack) LocalPlaylist {
	if item.ID == "" {
		return LocalPlaylist{}
	}
	name = strings.TrimSpace(name)
	for i := range s.Playlists {
		if strings.EqualFold(s.Playlists[i].Name, name) {
			for _, existing := range s.Playlists[i].Items {
				if existing.ID == item.ID {
					return s.Playlists[i]
				}
			}
			s.Playlists[i].Items = append(s.Playlists[i].Items, item)
			s.Playlists[i].UpdatedAt = time.Now().UTC()
			return s.Playlists[i]
		}
	}
	return s.SaveQueue(name, []LocalTrack{item})
}

func (s *Store) RemoveFromLocalPlaylist(id string, index int) {
	for i := range s.Playlists {
		if s.Playlists[i].ID == id && index >= 0 && index < len(s.Playlists[i].Items) {
			s.Playlists[i].Items = append(s.Playlists[i].Items[:index], s.Playlists[i].Items[index+1:]...)
			s.Playlists[i].UpdatedAt = time.Now().UTC()
			return
		}
	}
}

func (s *Store) MoveInLocalPlaylist(id string, from, to int) {
	for i := range s.Playlists {
		list := &s.Playlists[i]
		if list.ID != id || from < 0 || from >= len(list.Items) || to < 0 || to >= len(list.Items) || from == to {
			continue
		}
		item := list.Items[from]
		list.Items = append(list.Items[:from], list.Items[from+1:]...)
		list.Items = append(list.Items[:to], append([]LocalTrack{item}, list.Items[to:]...)...)
		list.UpdatedAt = time.Now().UTC()
		return
	}
}

func (s *Store) DeleteLocalPlaylist(id string) {
	kept := s.Playlists[:0]
	for _, list := range s.Playlists {
		if list.ID != id {
			kept = append(kept, list)
		}
	}
	s.Playlists = kept
}
