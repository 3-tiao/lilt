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

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
	"github.com/caiguo/lilt/internal/server"
)

// configurePlatform wires every macOS-specific piece of the server: two signed
// Swift helpers, lilt-player (MusicKit) and lilt-audio (AVPlayer). The server
// owns their lifecycle; nothing above the server knows which platform is
// playing. Apple discovery and authorization come from the helpers too, so no
// provider or auth override is registered here.
func configurePlatform(options *server.Options) {
	options.EngineFactory = playerEngineFactory()
	options.AppleResourceFactory = appleResourceFactory()
	options.AudioEngineFactory = audioEngineFactory()
}

func audioEngineFactory() func() (server.AudioEngine, error) {
	return func() (server.AudioEngine, error) {
		engine, err := player.Start(audioAppPath())
		if err != nil {
			return nil, err
		}
		engine.Trace = rpcTrace
		go func() {
			scanner := bufio.NewScanner(engine.Stderr())
			for scanner.Scan() {
				logger.Log("audio-helper", map[string]any{"line": scanner.Text()})
			}
		}()
		return engine, nil
	}
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
	helper, err := player.Start(playerAppPath())
	if err != nil {
		return output(api.Failure("", api.Errorf("player_unavailable", "%v", err)), jsonOutput)
	}
	defer helper.Close()
	helper.Trace = rpcTrace
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
