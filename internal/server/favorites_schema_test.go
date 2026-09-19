package server

import (
	"strings"
	"testing"
)

// Regression for batch 2026-09-19-watch-sync-recheck NEW-H4: the wire schema's
// RadioMetadata was missing fields that api.RadioMetadata carries, so the
// strict params validation rejected favorites.set for any Browse row that
// carries full radio metadata.
func TestFavoritesSetAcceptsFullRadioMetadata(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "favorites.set", map[string]any{
		"item": map[string]any{
			"source": "radio",
			"kind":   "stream",
			"id":     "radio:http://example.test/stream",
			"ref":    "http://example.test/stream",
			"url":    "http://example.test/stream",
			"title":  "Probe Stream",
			"radio": map[string]any{
				"origin":        "directory",
				"stationUUID":   "uuid-1",
				"tags":          []string{"jazz"},
				"languages":     []string{"en"},
				"country":       "United States",
				"countryCode":   "US",
				"codec":         "MP3",
				"bitrate":       192,
				"hls":           false,
				"votes":         10,
				"clickCount":    5,
				"clickTrend":    3,
				"lastCheckOK":   true,
				"lastCheckTime": "2026-09-19T00:00:00Z",
			},
		},
		"favorited": true,
	})
	if !response.OK {
		t.Fatalf("favorites.set with full radio metadata = %+v, want ok", response.Error)
	}
	listed := call(t, socket, "favorites.list", map[string]any{"source": "radio"})
	if !listed.OK {
		t.Fatalf("favorites.list = %+v", listed.Error)
	}
	if !strings.Contains(string(listed.Data), `"title":"Probe Stream"`) {
		t.Fatalf("radio favorite not persisted: %s", listed.Data)
	}
}
