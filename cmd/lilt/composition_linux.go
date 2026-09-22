//go:build linux

package main

import (
	"os/exec"
	"runtime"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/server"
)

// configureEngines wires the Linux playback backend.
//
// MusicKit is Apple-only, so the macOS helper factories stay unset and the
// Apple Music source reports itself unavailable; radio, Audius, and Jamendo are
// meant to play through an in-process mpv driver
// (docs/internals/linux-mpv-engine.md), which is not implemented yet. Until it
// lands the stream engine is unset as well: discovery, favorites, and history
// work, and playback answers with the server's "stream playback is
// unavailable" reason instead of silently failing.
func configureEngines(options *server.Options) {
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
