package audius

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestAudiusLiveDiscoveryAndStream is the opt-in real E2E for the official
// Audius REST API. It is skipped unless LILT_AUDIUS_E2E=1 so CI stays hermetic.
func TestAudiusLiveDiscoveryAndStream(t *testing.T) {
	if os.Getenv("LILT_AUDIUS_E2E") != "1" {
		t.Skip("set LILT_AUDIUS_E2E=1 to run the real Audius discovery/stream E2E")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := Client{}

	tracks, apiErr := client.SearchTracks(ctx, "lofi", 3)
	if apiErr != nil {
		t.Fatalf("SearchTracks: %v", apiErr)
	}
	if len(tracks) == 0 || tracks[0].ID == "" || tracks[0].Title == "" {
		t.Fatalf("unexpected search result: %+v", tracks)
	}

	playlists, apiErr := client.SearchPlaylists(ctx, "lofi", 3)
	if apiErr != nil {
		t.Fatalf("SearchPlaylists: %v", apiErr)
	}
	if len(playlists) == 0 || playlists[0].ID == "" {
		t.Fatalf("unexpected playlist result: %+v", playlists)
	}

	playable := -1
	for i, track := range tracks {
		if track.IsStreamable && track.ID != "" {
			playable = i
			break
		}
	}
	if playable < 0 {
		t.Skip("live search returned no streamable track")
	}

	mediaURL, apiErr := client.StreamURL(ctx, tracks[playable].ID)
	if apiErr != nil {
		t.Fatalf("StreamURL: %v", apiErr)
	}
	if !strings.HasPrefix(mediaURL, "http://") && !strings.HasPrefix(mediaURL, "https://") {
		t.Fatalf("stream URL is not a media URL: %q", mediaURL)
	}
	// The signed URL is runtime-only; never print it.
	t.Logf("resolved stream URL for track %s (redacted)", tracks[playable].ID)
}
