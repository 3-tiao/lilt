package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/radio"
)

// The shared server layer must describe a missing playback backend in terms of
// capability, not in terms of the macOS engine that happens to implement it, so
// the same response reads correctly on every platform.
func TestStreamCapabilityReasonNamesNoBackend(t *testing.T) {
	descriptor := (&Server{radio: radio.New()}).radioDescriptor()

	stream := descriptor.Capabilities[api.CapPlaybackStream]
	if stream.Available {
		t.Fatalf("playback.stream available without an audio backend: %+v", stream)
	}
	if stream.Reason != "stream playback is unavailable" {
		t.Fatalf("playback.stream reason = %q", stream.Reason)
	}
	if !descriptor.Capabilities[api.CapSearchRadio].Available {
		t.Fatal("radio discovery must survive a missing playback backend")
	}
}

// A session with no playback backend is stopped, not broken. Linux has no
// MusicKit engine at all, and `session.status` is how scripts and agents read
// the session, so it must answer instead of failing.
func TestStatusWithoutBackendReportsStopped(t *testing.T) {
	_, socket := startTestServerWithEngine(t, nil)

	response := call(t, socket, "session.status", nil)
	if !response.OK {
		t.Fatalf("session.status = %+v, want a stopped session", response.Error)
	}
	var status api.PlaybackStatus
	if err := json.Unmarshal(response.Data, &status); err != nil {
		t.Fatalf("decode session.status: %v", err)
	}
	if status.Status != "stopped" || status.Mode != "none" {
		t.Fatalf("status = %+v, want stopped/none", status)
	}
}

func TestPlaybackErrorsNameNoBackend(t *testing.T) {
	_, socket := startTestServerWithEngine(t, nil)

	response := call(t, socket, "playback.play", map[string]any{"ref": "https://example.test/stream.mp3"})
	if response.OK || response.Error == nil {
		t.Fatalf("playback.play = %+v, want an error", response)
	}
	if response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("playback.play code = %q, want %s", response.Error.Code, api.CodeSourceUnavailable)
	}
	for _, backend := range []string{"AVPlayer", "MusicKit"} {
		if strings.Contains(response.Error.Message, backend) {
			t.Fatalf("playback.play message names %s: %q", backend, response.Error.Message)
		}
	}
}
