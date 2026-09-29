//go:build darwin

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/appleweb"
	"github.com/3-tiao/lilt/internal/player"
	"github.com/3-tiao/lilt/internal/playrouter"
	"github.com/3-tiao/lilt/internal/server"
)

const (
	appleEngineHelper  = "helper"
	appleEngineBrowser = "browser"
)

// configurePlatform keeps the signed MusicKit helper as the macOS default. The
// browser engine is deliberately opt-in: it replaces only Apple catalog and
// playback while lilt-audio continues to own radio and direct-URL queues.
func configurePlatform(options *server.Options) error {
	mode, err := appleEngineMode()
	if err != nil {
		return err
	}
	if mode == appleEngineHelper {
		options.EngineFactory = playerEngineFactory()
		options.AppleResourceFactory = appleResourceFactory()
		options.AudioEngineFactory = audioEngineFactory()
		return nil
	}

	fmt.Fprintln(os.Stderr, "Apple Music engine: browser (LILT_APPLE_ENGINE=browser); MusicKit helper and library APIs are disabled.")
	apple := appleweb.NewEngine(appleweb.Options{
		ProfileDir: appleweb.DefaultProfileDir(),
		Headless:   true,
		Log:        logger.Log,
	})
	options.AudioEngineFactory = func() (server.AudioEngine, error) {
		streams, err := startAudioPlayback()
		if err != nil {
			return nil, err
		}
		return playrouter.New(streams, apple), nil
	}
	options.Providers = append(options.Providers, server.NewAppleWebProvider(apple, appleweb.Available))
	options.AuthProviders = append(options.AuthProviders, server.NewAppleWebAuthProvider(apple, apple.Started))
	return nil
}

func appleEngineMode() (string, error) {
	mode := os.Getenv("LILT_APPLE_ENGINE")
	if mode == "" {
		mode = appleEngineHelper
	}
	if mode != appleEngineHelper && mode != appleEngineBrowser {
		return "", fmt.Errorf("invalid LILT_APPLE_ENGINE=%q; valid values are helper and browser", mode)
	}
	return mode, nil
}

func audioEngineFactory() func() (server.AudioEngine, error) {
	return func() (server.AudioEngine, error) {
		return startAudioPlayback()
	}
}

// startAudioPlayback is a variable so the Darwin composition test can replace
// LaunchServices with a hermetic stream backend while exercising the production
// composition and router.
var startAudioPlayback = func() (playrouter.Streams, error) {
	engine, err := player.Start(audioAppPath())
	if err != nil {
		return nil, err
	}
	engine.Trace = rpcTrace
	engine.TraceDebug = rpcStartDebug
	go func() {
		scanner := bufio.NewScanner(engine.Stderr())
		for scanner.Scan() {
			logger.Log("audio-helper", map[string]any{"line": scanner.Text()})
		}
	}()
	return engine, nil
}

// appleResourceFactory builds the independent, read-only Apple Music runtime.
// It intentionally remains alive when lilt-audio owns active playback, so
// catalog/library calls stay available without starting Apple audio.
func appleResourceFactory() func() (server.AppleResourceClient, error) {
	return func() (server.AppleResourceClient, error) {
		resource, err := player.Start(playerAppPath())
		if err != nil {
			return nil, err
		}
		resource.Trace = rpcTrace
		resource.TraceDebug = rpcStartDebug
		go func() {
			scanner := bufio.NewScanner(resource.Stderr())
			for scanner.Scan() {
				logger.Log("apple-resource", map[string]any{"line": scanner.Text()})
			}
		}()
		return resource, nil
	}
}

// playerEngineFactory builds a fresh signed playback helper. The server calls
// it at startup and again after a helper transport failure. Discovery and
// library access use appleResourceFactory instead.
func playerEngineFactory() func() (server.Engine, error) {
	return func() (server.Engine, error) {
		engine, err := player.Start(playerAppPath())
		if err != nil {
			return nil, err
		}
		engine.Trace = rpcTrace
		engine.TraceDebug = rpcStartDebug
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		status, authErr := engine.Authorization(ctx)
		cancel()
		if authErr != nil {
			_ = engine.Close()
			return nil, fmt.Errorf("read MusicKit authorization status: %w", authErr)
		}
		if status.Status == "not_determined" {
			fmt.Fprintln(os.Stderr, "Apple Music access is not determined; run `lilt auth apple-music` or open `lilt tui` once to authorize. Continuing (preview playback where available).")
		}
		go func() {
			scanner := bufio.NewScanner(engine.Stderr())
			for scanner.Scan() {
				logger.Log("helper", map[string]any{"line": scanner.Text()})
			}
		}()
		return engine, nil
	}
}

// runDoctor starts the signed helper once and reports MusicKit token
// diagnostics without starting a server. It is a local troubleshooting tool,
// not part of the Client API.
func runDoctor(jsonOutput bool) int {
	mode, err := appleEngineMode()
	if err != nil {
		return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, "%v", err)), jsonOutput)
	}
	if mode == appleEngineBrowser {
		return output(api.Failure("", api.Errorf(api.CodeUnsupportedCommand,
			"`lilt doctor` inspects the MusicKit helper; LILT_APPLE_ENGINE is browser")), jsonOutput)
	}
	helper, err := player.Start(playerAppPath())
	if err != nil {
		return output(api.Failure("", api.Errorf("player_unavailable", "%v", err)), jsonOutput)
	}
	defer helper.Close()
	helper.Trace = rpcTrace
	helper.TraceDebug = rpcStartDebug
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	diagnostics, err := helper.Diagnose(ctx)
	if err != nil {
		return output(api.Failure("", api.Errorf("diagnostics_failed", "%v", err)), jsonOutput)
	}
	return output(api.Success("", diagnostics), jsonOutput)
}

// helperAppPath resolves a signed helper bundle. The environment variable wins;
// otherwise the bundle is looked up next to the running binary, which is where
// the release archive puts it (lilt and lilt-*.app side by side) and where a
// repo build leaves it (repo root plus player/Build/Products/Release). A
// cwd-relative guess is deliberately not used: agents and scripts run the CLI
// from arbitrary directories, and a path that only works from the repo root
// reads as "the helper is missing".
func helperAppPath(env, name string) string {
	if path := os.Getenv(env); path != "" {
		return path
	}
	dir := ""
	if executable, err := os.Executable(); err == nil {
		dir = filepath.Dir(executable)
	}
	candidates := []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "player", "Build", "Products", "Release", name),
		filepath.Join(dir, "..", "player", "Build", "Products", "Release", name),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return filepath.Clean(candidates[0])
}

func playerAppPath() string {
	return helperAppPath("LILT_PLAYER_PATH", "lilt-player.app")
}

func audioAppPath() string {
	return helperAppPath("LILT_AUDIO_PATH", "lilt-audio.app")
}

// openBrowser launches the platform browser. It is a convenience everywhere it
// is used; callers treat failure as non-fatal and print the URL instead.
func openBrowser(target string) error {
	return exec.Command("open", target).Start()
}
