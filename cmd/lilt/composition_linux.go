//go:build linux

package main

import (
	"os/exec"
	"runtime"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/mpvplayer"
	"github.com/caiguo/lilt/internal/server"
)

// configureEngines wires the Linux playback backend: one in-process mpv driver
// owns both live radio and finite URL queues (Audius, Jamendo), the roles the
// signed lilt-audio helper holds on macOS. MusicKit is Apple-only, so the
// MusicKit factories stay unset and the Apple source reports itself unavailable.
//
// The mpv process starts lazily on the first playback, so a server that only
// browses radio never spawns a player, and a missing mpv binary fails one
// playback with an install hint instead of failing server startup.
func configureEngines(options *server.Options) {
	options.AudioEngineFactory = func() (server.AudioEngine, error) {
		return mpvplayer.New(), nil
	}
}

func runDoctor(jsonOutput bool) int {
	return output(api.Failure("", api.Errorf(api.CodeUnsupportedCommand,
		"`lilt doctor` inspects the macOS MusicKit helper and is not available on %s", runtime.GOOS)), jsonOutput)
}

// openBrowser launches the platform browser. It is a convenience everywhere it
// is used; callers treat failure as non-fatal and print the URL instead.
func openBrowser(target string) error {
	return exec.Command("xdg-open", target).Start()
}
