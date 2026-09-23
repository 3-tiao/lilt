package appleweb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The fake CDP answers in the mapped shape, so these tests drive the Go half of
// each new surface: decode, flattening, dedup, the station filter, the limit,
// and the signed-out guard. The JS mappings themselves are exercised by the
// opt-in real-browser E2E.

// startCatalogEngine drops the storefront-alignment journal the catalog tests
// do not read.
func startCatalogEngine(t *testing.T) (*Engine, func() string) {
	t.Helper()
	engine, log, _ := startAlignedEngine(t)
	return engine, log
}

func TestTrendingSongsReadsTheChart(t *testing.T) {
	engine, _ := startCatalogEngine(t)
	songs, err := engine.TrendingSongs(context.Background(), 5)
	if err != nil {
		t.Fatalf("TrendingSongs: %v", err)
	}
	if len(songs) != 1 || songs[0].ID != "1111111111" || songs[0].Title != "Fixture" {
		t.Fatalf("songs = %+v, want the chart fixture", songs)
	}
}

// The fixture carries one duplicate playlist row and one station row: the
// serving list must keep each catalog object once and drop the kind this
// engine has no playback path for.
func TestRecommendationsFlattenGroupsAndDropStations(t *testing.T) {
	engine, _ := startCatalogEngine(t)
	rows, err := engine.Recommendations(context.Background(), 20)
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want the deduped playlist rows plus the album", rows)
	}
	if rows[0].Kind != "playlist" || rows[0].ID != "pl.1" || rows[0].Title != "Fixture Mix" || rows[0].Artist != "Made for You" {
		t.Fatalf("first row = %+v, want the group-titled playlist", rows[0])
	}
	if rows[1].Kind != "playlist" || rows[1].ID != "pl.2" {
		t.Fatalf("second row = %+v, want the other playlist", rows[1])
	}
	if rows[2].Kind != "album" || rows[2].ID != "3333333333" || rows[2].Artist != "Fixture Artist" {
		t.Fatalf("album row = %+v, want the album's own artist", rows[2])
	}
}

func TestRecommendationsApplyTheLimit(t *testing.T) {
	engine, _ := startCatalogEngine(t)
	rows, err := engine.Recommendations(context.Background(), 1)
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the limit applied after flattening", rows)
	}
}

// A signed-out profile is refused up front: asking the me endpoint would only
// produce a 401, and the error must say what is missing rather than hide it.
func TestRecommendationsRefuseSignedOutProfiles(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":true,"authorized":false,"state":0}`)
	engine, log := startCatalogEngine(t)
	if _, err := engine.Recommendations(context.Background(), 20); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Recommendations = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(log(), "/v1/me/recommendations") {
		t.Fatalf("a signed-out profile still asked the endpoint:\n%s", log())
	}
}

func TestRecommendationsMapPage401ToUnauthorized(t *testing.T) {
	t.Setenv("LILT_TEST_FAKE_CDP_RECOMMENDATIONS_401", "1")
	engine, log := startCatalogEngine(t)
	if _, err := engine.Recommendations(context.Background(), 20); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Recommendations = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(log(), "/v1/me/recommendations") {
		t.Fatalf("the fixture did not exercise the me endpoint:\n%s", log())
	}
}

func TestSearchPlaylistsFindsCatalogPlaylists(t *testing.T) {
	engine, _ := startCatalogEngine(t)
	playlists, err := engine.SearchPlaylists(context.Background(), "fixture", 5)
	if err != nil {
		t.Fatalf("SearchPlaylists: %v", err)
	}
	if len(playlists) != 1 || playlists[0].ID != "pl.1" || playlists[0].Artist != "Fixture Curator" {
		t.Fatalf("playlists = %+v, want the fixture row with its curator", playlists)
	}
}

func TestPlaylistTracksResolvesCatalogPlaylists(t *testing.T) {
	engine, log := startCatalogEngine(t)
	playlist, tracks, err := engine.PlaylistTracks(context.Background(), "pl.1")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if playlist.ID != "pl.1" || playlist.Title != "Fixture Mix" {
		t.Fatalf("playlist = %+v", playlist)
	}
	if len(tracks) != 1 || tracks[0].ID != "1111111111" {
		t.Fatalf("tracks = %+v", tracks)
	}
	if !strings.Contains(log(), "/tracks") {
		t.Fatalf("playlist lookup did not use the catalog tracks endpoint:\n%s", log())
	}
}
