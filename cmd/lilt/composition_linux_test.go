//go:build linux

package main

import (
	"testing"

	"github.com/caiguo/lilt/internal/server"
)

// The Linux composition must never fall back to the macOS helpers: MusicKit is
// Apple-only and the mpv driver is not implemented yet, so every factory stays
// unset. The server then reports the gap through source descriptors instead of
// trying to launch a signed .app bundle that does not exist here.
func TestConfigureEnginesLeavesLinuxWithoutPlaybackBackends(t *testing.T) {
	options := server.Options{}
	configureEngines(&options)
	if options.EngineFactory != nil || options.AppleResourceFactory != nil || options.AudioEngineFactory != nil {
		t.Fatalf("linux composition wired macOS helpers: %+v", options)
	}
}
