package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/icy"
	"github.com/caiguo/lilt/internal/state"
)

func TestICYOverlayOnlyAppliesToStreams(t *testing.T) {
	title, artist := "Around the World", "Daft Punk"
	server := &Server{icyTitle: title, icyArtist: artist}

	stream := server.projectStatus(core.PlaybackState{Status: "playing", IsLive: true, Mode: "stream"}, api.SourceRadio, 1)
	if stream.StreamTitle == nil || *stream.StreamTitle != title {
		t.Fatalf("stream title = %v", stream.StreamTitle)
	}
	if stream.StreamArtist == nil || *stream.StreamArtist != artist {
		t.Fatalf("stream artist = %v", stream.StreamArtist)
	}

	apple := server.projectStatus(core.PlaybackState{Status: "playing", Mode: "full"}, api.SourceAppleMusic, 1)
	if apple.StreamTitle != nil || apple.StreamArtist != nil {
		t.Fatalf("ICY leaked into Apple playback: %v %v", apple.StreamTitle, apple.StreamArtist)
	}
}

func TestStartICYUpdatesStreamMetadata(t *testing.T) {
	icyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-metaint", "4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("AAAA"))
		payload := []byte("StreamTitle='Daft Punk - Around the World';")
		padded := make([]byte, (len(payload)/16+1)*16)
		copy(padded, payload)
		_, _ = w.Write([]byte{byte(len(padded) / 16)})
		_, _ = w.Write(padded)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer icyServer.Close()

	server := &Server{
		engine:   newSupervisedEngine(),
		icy:      &icy.Client{},
		watchers: newWatchHub(),
	}
	server.startICY(icyServer.URL)
	defer server.stopICY()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		server.icyMu.Lock()
		got := server.icyTitle
		server.icyMu.Unlock()
		if got == "Around the World" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	status := server.projectStatus(core.PlaybackState{Status: "playing", IsLive: true, Mode: "stream"}, api.SourceRadio, 1)
	if status.StreamTitle == nil || *status.StreamTitle != "Around the World" {
		t.Fatalf("stream title = %v", status.StreamTitle)
	}
	if status.StreamArtist == nil || *status.StreamArtist != "Daft Punk" {
		t.Fatalf("stream artist = %v", status.StreamArtist)
	}
}

// Playing a radio stream wires ICY metadata into session.status over the wire.
func TestRadioPlayExposesICYMetadata(t *testing.T) {
	icyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-metaint", "4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("AAAA"))
		payload := []byte("StreamTitle='Daft Punk - Around the World';")
		padded := make([]byte, (len(payload)/16+1)*16)
		copy(padded, payload)
		_, _ = w.Write([]byte{byte(len(padded) / 16)})
		_, _ = w.Write(padded)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	// Registered before the server cleanup so the ICY handler is released by
	// server.Close (which cancels the watcher) before the HTTP server waits.
	t.Cleanup(icyServer.Close)

	dir, err := os.MkdirTemp("/tmp", "lilt-icy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server, err := Start(Options{
		SocketPath: socket,
		Engine:     newSupervisedEngine(),
		ICY:        &icy.Client{},
		Store:      state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	played := call(t, socket, "playback.play", map[string]any{"ref": icyServer.URL, "name": "Example FM"})
	if !played.OK {
		t.Fatalf("play failed: %+v", played.Error)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := call(t, socket, "session.status", nil)
		if response.OK {
			var status api.PlaybackStatus
			if json.Unmarshal(response.Data, &status) == nil && status.StreamTitle != nil {
				if *status.StreamTitle != "Around the World" {
					t.Fatalf("streamTitle = %q", *status.StreamTitle)
				}
				if status.StreamArtist == nil || *status.StreamArtist != "Daft Punk" {
					t.Fatalf("streamArtist = %v", status.StreamArtist)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stream metadata never appeared in session.status")
}

func TestStaleICYCallbackIsIgnored(t *testing.T) {
	server := &Server{watchers: newWatchHub()}
	server.icyGeneration = 5
	server.applyICY(4, icy.Update{Title: "Stale", Artist: "Old"})
	if server.icyTitle != "" || server.icyArtist != "" {
		t.Fatalf("stale callback applied: %q %q", server.icyTitle, server.icyArtist)
	}
}
