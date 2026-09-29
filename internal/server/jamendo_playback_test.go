package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/state"
)

func jamendoPlaybackUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlists":
			// The play path expands container refs through the provider
			// registry, so the expansion needs the playlist object endpoint too.
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"p1","name":"Mix","user_name":"A"}]}`))
		case "/playlists/tracks":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[` +
				`{"id":"t1","name":"One","duration":120,"artist_name":"A","audio":"https://default.invalid/t1","shareurl":"https://www.jamendo.com/track/t1"},` +
				`{"id":"t2","name":"Two","duration":90,"artist_name":"A","audio":"https://default.invalid/t2","shareurl":"https://www.jamendo.com/track/t2"}]}`))
		case "/tracks":
			id := r.URL.Query().Get("id")
			if id == "" {
				http.Error(w, "missing id", http.StatusBadRequest)
				return
			}
			format := r.URL.Query().Get("audioformat")
			media := "https://default.invalid/" + id
			if format == "mp32" {
				media = "https://media.invalid/" + id + ".mp3"
			}
			_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[{"id":%q,"name":%q,"duration":120,"artist_name":"A","audio":%q,"shareurl":%q}]}`,
				id, strings.ToUpper(id), media, "https://www.jamendo.com/track/"+id)
		default:
			http.NotFound(w, r)
		}
	}))
}

func startJamendoPlaybackServer(t *testing.T, upstream *httptest.Server, driver URLPlaybackDriver) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-jamendo-play-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := securestore.NewMemory()
	if err := jamendo.SaveClientID(store, "client-1"); err != nil {
		t.Fatal(err)
	}
	client := jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	server, err := Start(Options{
		SocketPath:        filepath.Join(dir, "s.sock"),
		Engine:            fakeengine.NewFakeEngine(),
		Store:             state.New(filepath.Join(dir, "state.json")),
		SecureStore:       store,
		JamendoClient:     &client,
		URLPlaybackDriver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, filepath.Join(dir, "s.sock")
}

func TestJamendoPlaybackRateLimitIsSourceUnavailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlists":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"p1","name":"Mix","user_name":"A"}]}`))
		case "/playlists/tracks":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"t1","name":"One","duration":120,"artist_name":"A","audio":"https://default.invalid/t1"}]}`))
		case "/tracks":
			// The play path resolves track discovery (no audioformat) during
			// PreparePlayback, and the media URL (mp32) at start time: the
			// rate limit is a media-side failure, so only the media call fails.
			if r.URL.Query().Get("audioformat") == "mp32" {
				_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":6},"results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"t1","name":"One","duration":120,"artist_name":"A","audio":"https://default.invalid/t1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket := startJamendoPlaybackServer(t, upstream, driver)

	response := call(t, socket, "playback.play", map[string]any{"ref": "jamendo:playlist:p1"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("play=%+v, want source_unavailable", response)
	}
	if got := fmt.Sprint(response.Error.Details["providerCode"]); got != "6" {
		t.Fatalf("providerCode=%q", got)
	}
	if driver.count() != 0 {
		t.Fatalf("driver received %d targets after resolution failure", driver.count())
	}
}

func TestJamendoURLQueuePlaybackOverServer(t *testing.T) {
	upstream := jamendoPlaybackUpstream(t)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket := startJamendoPlaybackServer(t, upstream, driver)

	response := call(t, socket, "playback.play", map[string]any{"ref": "jamendo:playlist:p1"})
	if !response.OK {
		t.Fatalf("play=%+v", response.Error)
	}
	if strings.Contains(string(response.Data), "media.invalid") || strings.Contains(string(response.Data), "default.invalid") {
		t.Fatalf("media URL leaked into playback response: %s", response.Data)
	}
	if got := driver.last().URL; got != "https://media.invalid/t1.mp3" {
		t.Fatalf("first target=%q", got)
	}
	var playback api.PlaybackState
	if err := json.Unmarshal(response.Data, &playback); err != nil {
		t.Fatal(err)
	}
	if playback.Source != api.SourceJamendo || playback.Mode != "full" || playback.IsLive || len(playback.Queue) != 2 {
		t.Fatalf("playback=%#v", playback)
	}
	for _, item := range playback.Queue {
		if item.Source != api.SourceJamendo || !strings.HasPrefix(item.Ref, "jamendo:song:") || strings.Contains(item.URL, "media.invalid") {
			t.Fatalf("public queue item=%#v", item)
		}
	}

	response = call(t, socket, "playback.next", nil)
	if !response.OK {
		t.Fatalf("next=%+v", response.Error)
	}
	if got := driver.last().URL; got != "https://media.invalid/t2.mp3" {
		t.Fatalf("second target=%q", got)
	}

	response = call(t, socket, "playback.playSongs", map[string]any{
		"refs":       []string{"jamendo:song:t1", "jamendo:song:t2"},
		"startIndex": 1,
	})
	if !response.OK {
		t.Fatalf("playSongs=%+v", response.Error)
	}
	if got := driver.last().URL; got != "https://media.invalid/t2.mp3" {
		t.Fatalf("playSongs target=%q", got)
	}
}

func TestJamendoQueueUsesInjectedFakeEngineWithoutMedia(t *testing.T) {
	upstream := jamendoPlaybackUpstream(t)
	defer upstream.Close()
	_, socket := startJamendoPlaybackServer(t, upstream, nil)
	response := call(t, socket, "playback.play", map[string]any{"ref": "jamendo:playlist:p1"})
	if !response.OK {
		t.Fatalf("fake Jamendo play: %+v", response.Error)
	}
	var playback api.PlaybackState
	if err := json.Unmarshal(response.Data, &playback); err != nil {
		t.Fatal(err)
	}
	if playback.Mode != "full" || len(playback.Queue) != 2 || playback.Track == nil || playback.Track.Ref != "jamendo:song:t1" {
		t.Fatalf("fake Jamendo state = %+v", playback)
	}
	if strings.Contains(string(response.Data), "media.invalid") || strings.Contains(string(response.Data), "default.invalid") {
		t.Fatalf("media URL leaked: %s", response.Data)
	}
	if response := call(t, socket, "playback.next", nil); !response.OK {
		t.Fatalf("fake Jamendo next: %+v", response.Error)
	}
	state := waitForStatus(t, socket, func(s api.PlaybackState) bool { return s.Track != nil && s.Track.Ref == "jamendo:song:t2" })
	if state.QueueIndex != 1 {
		t.Fatalf("next index = %d", state.QueueIndex)
	}
}

// queue.add routes by the ITEM's source: a Jamendo ref with no active URL
// queue session answers queue_unavailable instead of falling through to the
// MusicKit engine, whose fake acceptance turned the misroute into a success
// toast over an empty queue (batch 2026-09-22-recheck2 r4-recheck).
func TestQueueAddRoutesJamendoToTheURLQueuePath(t *testing.T) {
	upstream := jamendoPlaybackUpstream(t)
	_, socket := startJamendoPlaybackServer(t, upstream, &recordingURLDriver{})

	response := call(t, socket, "queue.add", map[string]any{
		"ref":      "jamendo:song:1157358",
		"position": "next",
	})
	if response.OK || response.Error.Code != api.CodeQueueUnavailable {
		t.Fatalf("jamendo queue.add without a session = %+v, want queue_unavailable", response)
	}
}
