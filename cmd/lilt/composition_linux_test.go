//go:build linux

package main

import (
	"testing"

	"github.com/caiguo/lilt/internal/server"
)

// The Linux composition must never fall back to the macOS helpers: MusicKit is
// Apple-only, so only the mpv stream backend is wired, and it must serve the
// finite URL queues (Audius, Jamendo) that the macOS audio helper also holds.
func TestConfigureEnginesWiresOnlyTheMPVBackend(t *testing.T) {
	var options server.Options
	configureEngines(&options)

	if options.EngineFactory != nil || options.AppleResourceFactory != nil {
		t.Fatalf("linux composition wired a macOS MusicKit helper: %+v", options)
	}
	if options.AudioEngineFactory == nil {
		t.Fatal("linux composition wired no stream backend")
	}
	engine, err := options.AudioEngineFactory()
	if err != nil {
		t.Fatalf("AudioEngineFactory: %v", err)
	}
	if engine == nil {
		t.Fatal("AudioEngineFactory returned no engine")
	}
	closer, ok := engine.(interface{ Close() error })
	if !ok {
		t.Fatalf("stream backend %T cannot be closed; the server leaks it on rebuild", engine)
	}
	defer func() { _ = closer.Close() }()
	if _, ok := engine.(server.URLPlaybackDriver); !ok {
		t.Fatalf("stream backend %T does not serve URL queues; Audius and Jamendo playback would be advertised but unavailable", engine)
	}
}
