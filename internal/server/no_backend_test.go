package server

import (
	"strings"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/radio"
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

func TestPlaybackErrorsNameNoBackend(t *testing.T) {
	_, socket := startTestServerWithEngine(t, nil)

	response := call(t, socket, "session.status", nil)
	if response.OK || response.Error == nil {
		t.Fatalf("session.status = %+v, want an error", response)
	}
	if response.Error.Code != api.CodeSourceUnavailable {
		t.Fatalf("session.status code = %q, want %s", response.Error.Code, api.CodeSourceUnavailable)
	}
	for _, backend := range []string{"AVPlayer", "MusicKit"} {
		if strings.Contains(response.Error.Message, backend) {
			t.Fatalf("session.status message names %s: %q", backend, response.Error.Message)
		}
	}
}
