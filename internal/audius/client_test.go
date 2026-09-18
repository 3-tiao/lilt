package audius

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

func TestDiscoveryRequestsAndErrorsAreHermetic(t *testing.T) {
	mode := "tracks"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("app_name") != "lilt" {
			t.Errorf("app_name = %q", r.URL.Query().Get("app_name"))
		}
		switch r.URL.Path {
		case "/playlists/p1":
			// Audius returns a one-element array for single-playlist lookups.
			_, _ = w.Write([]byte(`{"data":[{"id":"p1","playlist_name":"List"}]}`))
		case "/playlists/p1/tracks":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"Track","is_streamable":true,"user":{"name":"Artist"}}]}`))
		default:
			switch mode {
			case "tracks":
				_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"Track","is_streamable":true,"user":{"name":"Artist"}}]}`))
			case "empty":
				_, _ = w.Write([]byte(`{"data":[]}`))
			case "bad":
				_, _ = w.Write([]byte(`{"data":`))
			default:
				http.Error(w, "private upstream body", http.StatusTooManyRequests)
			}
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	tracks, err := client.SearchTracks(context.Background(), "term", 3)
	if err != nil || len(tracks) != 1 || tracks[0].ID != "t1" {
		t.Fatalf("tracks=%#v err=%v", tracks, err)
	}
	mode = "empty"
	tracks, err = client.SearchTracks(context.Background(), "term", 3)
	if err != nil || len(tracks) != 0 {
		t.Fatalf("empty tracks=%#v err=%v", tracks, err)
	}
	playlist, err := client.Playlist(context.Background(), "p1")
	if err != nil || playlist.ID != "p1" {
		t.Fatalf("playlist=%#v err=%v", playlist, err)
	}
	playlistTracks, err := client.PlaylistTracks(context.Background(), "p1")
	if err != nil || len(playlistTracks) != 1 || playlistTracks[0].ID != "t1" {
		t.Fatalf("playlist tracks=%#v err=%v", playlistTracks, err)
	}
	mode = "bad"
	_, err = client.SearchTracks(context.Background(), "term", 3)
	if err == nil || err.Code != api.CodeSearchFailed {
		t.Fatalf("malformed err=%v", err)
	}
	mode = "status"
	_, err = client.SearchTracks(context.Background(), "term", 3)
	if err == nil || err.Code != api.CodeSearchFailed || err.Details["providerCode"] != "429" {
		t.Fatalf("status err=%#v", err)
	}
	if _, err := client.Track(context.Background(), ""); err == nil || err.Code != api.CodeInvalidReference {
		t.Fatalf("invalid ref err=%v", err)
	}
}

func TestDiscoveryTimeoutMapsToSearchFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: &http.Client{Timeout: time.Millisecond}}
	_, err := client.SearchTracks(context.Background(), "x", 1)
	if err == nil || err.Code != api.CodeSearchFailed {
		t.Fatalf("err=%v", err)
	}
}

func TestDiscoveryStatusAndCancellationMapToSearchFailed(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "private upstream body", code)
			}))
			defer server.Close()
			client := Client{BaseURL: server.URL, HTTP: server.Client()}
			_, err := client.SearchTracks(context.Background(), "x", 1)
			if err == nil || err.Code != api.CodeSearchFailed {
				t.Fatalf("status %d err=%#v", code, err)
			}
			if err.Details["providerCode"] != fmt.Sprint(code) {
				t.Fatalf("status %d providerCode=%v", code, err.Details["providerCode"])
			}
			if strings.Contains(err.Message, "private upstream body") {
				t.Fatalf("status %d leaked upstream body: %q", code, err.Message)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.SearchTracks(ctx, "x", 1); err == nil || err.Code != api.CodeSearchFailed {
		t.Fatalf("cancelled err=%#v", err)
	}
}

func TestStreamURLUsesNoRedirectEnvelopeAndStableErrors(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tracks/track%2Fid/stream" && r.URL.Path != "/tracks/track/id/stream" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.URL.Query().Get("no_redirect") != "true" || r.URL.Query().Get("app_name") != "lilt" {
			t.Errorf("query=%v", r.URL.Query())
		}
		switch mode {
		case "ok":
			_, _ = w.Write([]byte(`{"data":"https://media.invalid/signed?token=secret"}`))
		case "malformed":
			_, _ = w.Write([]byte(`{"data":{"url":"wrong-shape"}}`))
		default:
			http.Error(w, "private", http.StatusForbidden)
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	mediaURL, err := client.StreamURL(context.Background(), "track/id")
	if err != nil || mediaURL != "https://media.invalid/signed?token=secret" {
		t.Fatalf("url=%q err=%v", mediaURL, err)
	}
	mode = "malformed"
	if _, err := client.StreamURL(context.Background(), "track/id"); err == nil || err.Code != api.CodeSearchFailed {
		t.Fatalf("malformed err=%#v", err)
	}
	mode = "status"
	if _, err := client.StreamURL(context.Background(), "track/id"); err == nil || err.Code != api.CodeSearchFailed || err.Details["providerCode"] != "403" {
		t.Fatalf("status err=%#v", err)
	}
	if _, err := client.StreamURL(context.Background(), " "); err == nil || err.Code != api.CodeInvalidReference {
		t.Fatalf("empty id err=%#v", err)
	}
}

func TestTracksSendsRepeatedIDParams(t *testing.T) {
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tracks" {
			t.Errorf("path = %q", r.URL.Path)
		}
		ids = r.URL.Query()["id"]
		_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"A","is_streamable":true},{"id":"t2","title":"B","is_streamable":true}]}`))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	tracks, err := client.Tracks(context.Background(), []string{"t1", "t2"})
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks=%#v err=%v", tracks, err)
	}
	// Audius needs repeated `id=` params; a comma-joined value returns nothing.
	if len(ids) != 2 || ids[0] != "t1" || ids[1] != "t2" {
		t.Fatalf("id params = %#v (a comma-joined value would be one element)", ids)
	}
}
