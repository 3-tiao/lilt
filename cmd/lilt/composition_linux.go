//go:build linux

package main

import (
	"os/exec"
	"runtime"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/appleweb"
	"github.com/3-tiao/lilt/internal/mpvplayer"
	"github.com/3-tiao/lilt/internal/playrouter"
	"github.com/3-tiao/lilt/internal/server"
)

// configurePlatform wires the Linux playback backend and Apple source.
//
// mpv owns live radio and finite URL queues (Audius, Jamendo), the role the
// signed lilt-audio helper holds on macOS. It starts lazily on the first
// playback, so a server that only browses never spawns a player, and a missing
// mpv binary fails one playback with an install hint instead of failing server
// startup.
//
// Apple Music has no MusicKit here, so it plays through Apple's own web player in
// a Chromium lilt owns: one session, shared by the catalog provider (which reads
// discovery out of the same page, so results and playback agree on the
// storefront) and by the playback router. The MusicKit-shaped provider and auth
// provider are replaced accordingly, and no AppleResourceFactory is set, so
// nothing claims MusicKit-based capabilities.
//
// See docs/internals/playback/apple-web-engine.md.
func configurePlatform(options *server.Options) error {
	// The profile holds the Apple session: machine-level credential storage, not
	// session state, so it does not move with the state root.
	apple := appleweb.NewEngine(appleweb.Options{
		ProfileDir: appleweb.DefaultProfileDir(),
		Headless:   true,
		Log:        logger.Log,
	})

	options.AudioEngineFactory = func() (server.AudioEngine, error) {
		return playrouter.New(mpvplayer.New(), apple), nil
	}
	options.Providers = append(options.Providers, server.NewAppleWebProvider(apple, appleweb.Available))
	options.AuthProviders = append(options.AuthProviders, server.NewAppleWebAuthProvider(apple, apple.Started))
	return nil
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
