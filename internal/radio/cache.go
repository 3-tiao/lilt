package radio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

const (
	cacheVersion         = 1
	maxStationRecords    = 1000
	maxHealthRecords     = 500
	stationTTL           = 6 * time.Hour
	healthyHealthTTL     = 24 * time.Hour
	transientFailureTTL  = 10 * time.Minute
	structuralFailureTTL = 24 * time.Hour
)

// StationRecord is a disposable snapshot of Radio Browser metadata. The
// station UUID is directory identity; EndpointKey ties it to a separately
// measured local endpoint without confusing those two lifecycles.
type StationRecord struct {
	StationUUID   string    `json:"stationUUID"`
	Name          string    `json:"name"`
	URLResolved   string    `json:"urlResolved"`
	EndpointKey   string    `json:"endpointKey"`
	Tags          []string  `json:"tags,omitempty"`
	Languages     []string  `json:"languages,omitempty"`
	Country       string    `json:"country,omitempty"`
	CountryCode   string    `json:"countryCode,omitempty"`
	Codec         string    `json:"codec,omitempty"`
	Bitrate       int       `json:"bitrate,omitempty"`
	HLS           bool      `json:"hls,omitempty"`
	Votes         int       `json:"votes,omitempty"`
	ClickCount    int       `json:"clickCount,omitempty"`
	ClickTrend    int       `json:"clickTrend,omitempty"`
	LastCheckOK   bool      `json:"lastCheckOK,omitempty"`
	LastCheckTime string    `json:"lastCheckTime,omitempty"`
	FetchedAt     time.Time `json:"fetchedAt"`
}

// HealthRecord describes this machine's HTTP first-byte result for one actual
// stream endpoint. Endpoint keys are SHA-256 hashes; custom URLs are never
// written to the cache as station metadata.
type HealthRecord struct {
	Status    string    `json:"status"`
	LatencyMs int       `json:"latencyMs,omitempty"`
	Code      string    `json:"code,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	Samples   int       `json:"samples,omitempty"`
}

type Cache struct {
	path     string
	Version  int                      `json:"version"`
	Stations map[string]StationRecord `json:"stations,omitempty"`
	Health   map[string]HealthRecord  `json:"health,omitempty"`
}

func CachePath() string {
	if path := os.Getenv("LILT_RADIO_CACHE"); path != "" {
		return path
	}
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "lilt", "radio-cache.json")
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "lilt", "radio-cache.json")
	}
	return filepath.Join(".cache", "lilt", "radio-cache.json")
}

func NewCache(path string) *Cache {
	return &Cache{path: path, Version: cacheVersion, Stations: map[string]StationRecord{}, Health: map[string]HealthRecord{}}
}

// Snapshot returns an independent cache projection safe to encode after the
// server releases its cache lock.
func (c *Cache) Snapshot() *Cache {
	if c == nil {
		return nil
	}
	copyCache := NewCache("")
	copyCache.Version = c.Version
	for key, station := range c.Stations {
		station.Tags = append([]string(nil), station.Tags...)
		station.Languages = append([]string(nil), station.Languages...)
		copyCache.Stations[key] = station
	}
	for key, health := range c.Health {
		copyCache.Health[key] = health
	}
	return copyCache
}

func LoadCache(path string) (*Cache, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewCache(path), nil
	}
	if err != nil {
		return NewCache(path), err
	}
	cache := NewCache(path)
	if err := json.Unmarshal(data, cache); err != nil {
		return NewCache(path), err
	}
	if cache.Version > cacheVersion {
		return NewCache(path), errors.New("radio cache was written by a newer lilt version")
	}
	if cache.Stations == nil {
		cache.Stations = map[string]StationRecord{}
	}
	if cache.Health == nil {
		cache.Health = map[string]HealthRecord{}
	}
	cache.Version = cacheVersion
	cache.prune(time.Now())
	return cache, nil
}

func (c *Cache) Save() error {
	if c == nil || c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(c.path), ".radio-cache-")
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
	return os.Rename(name, c.path)
}

func EndpointKey(streamURL string) string {
	sum := sha256.Sum256([]byte(api.NormalizeStreamURL(streamURL)))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) RememberItems(items []core.Item, now time.Time) bool {
	if c == nil {
		return false
	}
	changed := false
	for _, item := range items {
		metadata := item.Radio
		if metadata == nil || metadata.StationUUID == "" || item.URL == "" {
			continue
		}
		c.Stations[metadata.StationUUID] = StationRecord{
			StationUUID: metadata.StationUUID, Name: item.Title, URLResolved: item.URL, EndpointKey: EndpointKey(item.URL),
			Tags: append([]string(nil), metadata.Tags...), Languages: append([]string(nil), metadata.Languages...),
			Country: metadata.Country, CountryCode: metadata.CountryCode, Codec: metadata.Codec, Bitrate: metadata.Bitrate,
			HLS: metadata.HLS, Votes: metadata.Votes, ClickCount: metadata.ClickCount, ClickTrend: metadata.ClickTrend,
			LastCheckOK: metadata.LastCheckOK, LastCheckTime: metadata.LastCheckTime, FetchedAt: now,
		}
		changed = true
	}
	c.prune(now)
	return changed
}

func (c *Cache) FreshHealth(streamURL string, now time.Time) (HealthRecord, bool) {
	if c == nil {
		return HealthRecord{}, false
	}
	record, ok := c.Health[EndpointKey(streamURL)]
	if !ok || record.CheckedAt.IsZero() || now.Sub(record.CheckedAt) > healthTTL(record) {
		return HealthRecord{}, false
	}
	return record, true
}

func (c *Cache) RecordHealth(streamURL, status string, latencyMs int, code string, checkedAt time.Time) {
	if c == nil {
		return
	}
	key := EndpointKey(streamURL)
	record := HealthRecord{Status: status, Code: code, CheckedAt: checkedAt}
	if status == "healthy" {
		previous, ok := c.Health[key]
		samples := 1
		if ok && previous.Status == "healthy" {
			samples = min(previous.Samples, 4) + 1
			latencyMs = (previous.LatencyMs*(samples-1) + latencyMs) / samples
		}
		record.LatencyMs, record.Samples = latencyMs, samples
	}
	c.Health[key] = record
	c.prune(checkedAt)
}

// StationItems rebuilds playable items from fresh cached directory profiles,
// ordered by click count. It is the offline fallback when the directory cannot
// be reached, so a slow or down mirror does not leave an empty Browse screen.
func (c *Cache) StationItems(now time.Time) []core.Item {
	if c == nil {
		return nil
	}
	stations := make([]Station, 0, len(c.Stations))
	for _, record := range c.Stations {
		if record.FetchedAt.IsZero() || now.Sub(record.FetchedAt) > stationTTL || record.URLResolved == "" {
			continue
		}
		stations = append(stations, Station{
			StationUUID: record.StationUUID, Name: record.Name, URL: record.URLResolved,
			Country: record.Country, CountryCode: record.CountryCode,
			Tags: append([]string(nil), record.Tags...), Languages: append([]string(nil), record.Languages...),
			Codec: record.Codec, Bitrate: record.Bitrate, HLS: record.HLS, Votes: record.Votes,
			ClickCount: record.ClickCount, ClickTrend: record.ClickTrend,
			LastCheckOK: record.LastCheckOK, LastCheckTime: record.LastCheckTime,
		})
	}
	sort.Slice(stations, func(i, j int) bool {
		if stations[i].ClickCount != stations[j].ClickCount {
			return stations[i].ClickCount > stations[j].ClickCount
		}
		return stations[i].Name < stations[j].Name
	})
	return ToItems(stations)
}

func (c *Cache) DeleteHealth(streamURL string) {
	if c != nil {
		delete(c.Health, EndpointKey(streamURL))
	}
}

func (c *Cache) prune(now time.Time) {
	for key, record := range c.Stations {
		if record.FetchedAt.IsZero() || now.Sub(record.FetchedAt) > stationTTL {
			delete(c.Stations, key)
		}
	}
	for key, record := range c.Health {
		if record.CheckedAt.IsZero() || now.Sub(record.CheckedAt) > healthTTL(record) {
			delete(c.Health, key)
		}
	}
	trimOldestStations(c.Stations, maxStationRecords)
	trimOldestHealth(c.Health, maxHealthRecords)
}

func healthTTL(record HealthRecord) time.Duration {
	if record.Status == "healthy" {
		return healthyHealthTTL
	}
	switch record.Code {
	case "network", "timeout", "transport":
		return transientFailureTTL
	default:
		return structuralFailureTTL
	}
}

func trimOldestStations(records map[string]StationRecord, limit int) {
	if len(records) <= limit {
		return
	}
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return records[keys[i]].FetchedAt.Before(records[keys[j]].FetchedAt) })
	for _, key := range keys[:len(keys)-limit] {
		delete(records, key)
	}
}

func trimOldestHealth(records map[string]HealthRecord, limit int) {
	if len(records) <= limit {
		return
	}
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return records[keys[i]].CheckedAt.Before(records[keys[j]].CheckedAt) })
	for _, key := range keys[:len(keys)-limit] {
		delete(records, key)
	}
}
