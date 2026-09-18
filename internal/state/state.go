// Package state stores lilt's local-first cross-platform state.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
)

const version = 2

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
	Source   string    `json:"source"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

// Favorites is keyed by canonical public SourceID on disk.
type Favorites map[string][]Favorite

type Store struct {
	path               string
	memory             bool
	saveBlocked        error
	Version            int               `json:"version"`
	Theme              string            `json:"theme,omitempty"`
	LastSource         string            `json:"lastSource,omitempty"`
	LastPlaybackSource string            `json:"lastPlaybackSource,omitempty"`
	Favorites          Favorites         `json:"favorites"`
	Recent             []Recent          `json:"recent,omitempty"`
	RecentContainers   []RecentContainer `json:"recentContainers,omitempty"`
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

func New(path string) *Store {
	return &Store{path: path, Version: version, Favorites: Favorites{}}
}

// Snapshot returns an immutable deep copy suitable for ranking inside a Tea
// command while Model.Update remains the sole owner of the live Store.
func (s *Store) Snapshot() *Store {
	if s == nil {
		return nil
	}
	copyStore := *s
	copyStore.Favorites = make(Favorites, len(s.Favorites))
	for source, favorites := range s.Favorites {
		copyStore.Favorites[source] = append([]Favorite(nil), favorites...)
	}
	copyStore.Recent = append([]Recent(nil), s.Recent...)
	copyStore.RecentContainers = append([]RecentContainer(nil), s.RecentContainers...)
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
	if s.Favorites == nil {
		s.Favorites = Favorites{}
	}
	// Decoding a v1 object into the map yields its v1 key names.
	if favorites, ok := s.Favorites["appleMusic"]; ok {
		s.Favorites["apple-music"] = append(s.Favorites["apple-music"], favorites...)
		delete(s.Favorites, "appleMusic")
	}
	if s.LastPlaybackSource == "" {
		s.LastPlaybackSource = s.LastSource
	}
	// Containers carry an explicit source now; v1 entries without one are
	// Apple Music. Every source's container id is canonicalized to its own
	// stable identity: apple-music "am:<provider-id>" and audius
	// "audius:<kind>:<provider-id>".
	for i := range s.RecentContainers {
		container := &s.RecentContainers[i]
		if container.Source == "" {
			container.Source = "apple-music"
		}
		if container.Kind == "" {
			container.Kind = "playlist"
		}
		switch container.Source {
		case "apple-music":
			container.ID = "am:" + strings.TrimPrefix(strings.TrimPrefix(container.ID, "am:"), "playlist:")
		case "audius":
			parts := strings.SplitN(strings.TrimPrefix(container.ID, "audius:"), ":", 2)
			if len(parts) == 2 {
				container.ID = "audius:" + parts[0] + ":" + parts[1]
			}
		}
	}
	for i := range s.Recent {
		recent := &s.Recent[i]
		s.normalizeRecord("apple-music", &recent.ID, &recent.Source, &recent.Kind, &recent.URL)
	}
	// The favorites map key is the authoritative source: an entry's own source
	// is always replaced by the collection it lives in, then normalized.
	for source, favorites := range s.Favorites {
		for i := range favorites {
			favorite := &favorites[i]
			favorite.Source = source
			s.normalizeRecord(source, &favorite.ID, &favorite.Source, &favorite.Kind, &favorite.URL)
		}
		s.Favorites[source] = favorites
	}
	s.normalizeRadioRecords()
}

// normalizeRecord canonicalizes one persisted record's identity. defaultSource
// is the collection a record belongs to (favorites) or apple-music (recent and
// containers). rawURL is nil when the record has no persisted URL.
func (s *Store) normalizeRecord(defaultSource string, id, source, kind, rawURL *string) {
	if *source == "" {
		*source = defaultSource
	}
	if *source == "" {
		*source = "apple-music"
	}
	switch *source {
	case "radio":
		*id = "radio:" + normalizeURL(firstNonEmpty(recordURL(rawURL), strings.TrimPrefix(*id, "radio:")))
		if *kind == "" {
			*kind = "stream"
		}
	case "audius":
		if rawURL != nil {
			*rawURL = ""
		}
		parts := strings.SplitN(strings.TrimPrefix(*id, "audius:"), ":", 2)
		if len(parts) == 2 {
			if *kind == "" {
				*kind = parts[0]
			}
			*id = "audius:" + *kind + ":" + parts[1]
		} else if *kind == "" {
			*kind = "song"
		}
	case "apple-music":
		if *kind == "" {
			*kind = "song"
		}
		*id = "am:" + strings.TrimPrefix(*id, "am:")
	default:
		// Unknown source: keep the record as-is rather than corrupting it into
		// Apple Music. sourceFromStored still treats it as Apple for projection.
	}
}

func recordURL(rawURL *string) string {
	if rawURL == nil {
		return ""
	}
	return *rawURL
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
	radio := s.Favorites["radio"]
	keptFavorites := radio[:0]
	for _, favorite := range radio {
		favorite.ID = "radio:" + normalizeURL(firstNonEmpty(favorite.URL, strings.TrimPrefix(favorite.ID, "radio:")))
		if seen[favorite.ID] {
			continue
		}
		seen[favorite.ID] = true
		keptFavorites = append(keptFavorites, favorite)
	}
	s.Favorites["radio"] = keptFavorites
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

// ItemID returns the stable cross-platform id for an item from a source. It is
// idempotent: items already carrying their source prefix (for example entries
// read back from favorites or recents) keep the same id.
func ItemID(source string, item core.Item) string {
	if source == "radio" {
		url := item.URL
		if url == "" {
			url = strings.TrimPrefix(item.ID, "radio:")
		}
		return "radio:" + normalizeURL(url)
	}
	if source == "audius" {
		if strings.HasPrefix(item.ID, "audius:") {
			return item.ID
		}
		return "audius:" + item.Kind + ":" + item.ID
	}
	if item.ID != "" {
		if strings.HasPrefix(item.ID, "am:") {
			return item.ID
		}
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

func (s *Store) IsFavorite(source, id string) bool {
	for _, favorite := range s.Favorites[source] {
		if favorite.ID == id {
			return true
		}
	}
	return false
}

// ToggleFavorite flips the favorite state and reports whether it is now favorited.
func (s *Store) ToggleFavorite(source string, item core.Item) bool {
	if s.Favorites == nil {
		s.Favorites = Favorites{}
	}
	id := ItemID(source, item)
	list := s.Favorites[source]
	for i, favorite := range list {
		if favorite.ID == id {
			s.Favorites[source] = append(list[:i], list[i+1:]...)
			return false
		}
	}
	s.Favorites[source] = append(list, Favorite{ID: id, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: persistedURL(source, item.URL), AddedAt: time.Now().UTC()})
	return true
}

func (s *Store) FavoritesFor(source string) []core.Item {
	defaultKind := "stream"
	if source != "radio" {
		defaultKind = "song"
	}
	items := make([]core.Item, 0)
	for _, favorite := range s.Favorites[source] {
		items = append(items, core.Item{Kind: kindOr(favorite.Kind, defaultKind), ID: rawSourceID(source, favorite.ID), URL: favorite.URL, Title: favorite.Title, Artist: favorite.Artist})
	}
	return items
}

// RecentFor returns locally recorded plays for one source, newest first.
func (s *Store) RecentFor(source string) []core.Item {
	defaultKind := "stream"
	if source != "radio" {
		defaultKind = "song"
	}
	items := make([]core.Item, 0, len(s.Recent))
	for _, recent := range s.Recent {
		if recent.Source != source {
			continue
		}
		items = append(items, core.Item{Kind: kindOr(recent.Kind, defaultKind), ID: rawSourceID(source, recent.ID), URL: recent.URL, Title: recent.Title, Artist: recent.Artist})
	}
	return items
}

// rawSourceID strips a stored cross-source prefix so list items carry the same
// identifier the source provider uses; ItemID re-adds the prefix once.
func rawSourceID(source, id string) string {
	if source == "radio" {
		return strings.TrimPrefix(id, "radio:")
	}
	if source == "audius" {
		parts := strings.SplitN(strings.TrimPrefix(id, "audius:"), ":", 2)
		if len(parts) == 2 {
			return parts[1]
		}
		return id
	}
	return strings.TrimPrefix(id, "am:")
}

func (s *Store) AddRecent(source string, item core.Item) {
	if strings.TrimSpace(item.Title) == "" {
		return
	}
	id := ItemID(source, item)
	kept := s.Recent[:0]
	for _, recent := range s.Recent {
		if recent.ID != id {
			kept = append(kept, recent)
		}
	}
	s.Recent = append([]Recent{{ID: id, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: persistedURL(source, item.URL), PlayedAt: time.Now().UTC()}}, kept...)
	if len(s.Recent) > 100 {
		s.Recent = s.Recent[:100]
	}
}

// AddRecentContainerFor records a playlist context started by lilt for a source.
func (s *Store) AddRecentContainerFor(source string, item core.Item) {
	if item.Kind != "playlist" || strings.TrimSpace(item.Title) == "" {
		return
	}
	id := ItemID(source, item)
	kept := s.RecentContainers[:0]
	for _, recent := range s.RecentContainers {
		if recent.ID != id || recent.Source != source {
			kept = append(kept, recent)
		}
	}
	s.RecentContainers = append([]RecentContainer{{ID: id, Source: source, Kind: item.Kind, Title: item.Title, PlayedAt: time.Now().UTC()}}, kept...)
	if len(s.RecentContainers) > 100 {
		s.RecentContainers = s.RecentContainers[:100]
	}
}

func kindOr(kind, fallback string) string {
	if kind == "" {
		return fallback
	}
	return kind
}

// ProviderID returns the provider-native id from a persisted canonical id. It
// is empty for radio, whose identity is its normalized URL.
func ProviderID(source, id string) string {
	if source == "radio" {
		return ""
	}
	return rawSourceID(source, id)
}
func persistedURL(source, value string) string {
	if source == "audius" {
		return ""
	}
	return value
}
