package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/player"
	"github.com/caiguo/lilt/internal/presets"
	"github.com/caiguo/lilt/internal/protocol"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/session"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/tui"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	jsonOutput := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	args = filtered
	if len(args) == 0 {
		return output(protocol.Failure("usage", "usage: lilt tui|focus|search <term> --json|search <term> --play|play <url|kind:id>|library|recent [limit] --json|status|pause|resume|next|previous|doctor"), jsonOutput)
	}
	command := args[0]
	if command == "tui" {
		return startTUI("tui", args[1:], "", false)
	}
	if command == "focus" {
		return startTUI("focus", args[1:], "", false)
	}
	if command == "doctor" {
		return runDoctor(jsonOutput)
	}
	if command == "library" {
		return runLibrary(jsonOutput)
	}
	if command == "recent" {
		if len(args) > 2 {
			return output(protocol.Failure("usage", "usage: lilt recent [limit] --json"), jsonOutput)
		}
		limit := 25
		if len(args) == 2 {
			var err error
			limit, err = strconv.Atoi(args[1])
			if err != nil {
				return output(protocol.Failure("usage", "usage: lilt recent [limit] --json"), jsonOutput)
			}
		}
		return runRecent(jsonOutput, limit)
	}
	if command == "play" {
		if len(args) < 2 {
			return output(protocol.Failure("usage", "usage: lilt play <Apple-Music-URL|kind:id>"), jsonOutput)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		response, err := session.CallRequest(ctx, session.SocketPath(), protocol.SessionRequest{Command: "play", Reference: args[1]})
		if err != nil {
			return output(protocol.Failure("session_unavailable", err.Error()), jsonOutput)
		}
		return output(response, jsonOutput)
	}
	if command == "search" && len(args) == 3 && args[2] == "--play" {
		return startTUI("search", nil, args[1], true)
	}
	if command == "search" && len(args) == 2 {
		return runSearch(jsonOutput, args[1])
	}
	switch command {
	case "status", "pause", "resume", "next", "previous":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := session.Call(ctx, session.SocketPath(), command)
		if err != nil {
			return output(protocol.Failure("session_unavailable", err.Error()), jsonOutput)
		}
		return output(response, jsonOutput)
	default:
		return output(protocol.Failure("usage", "usage: lilt tui|focus|search <term> --json|search <term> --play|play <url|kind:id>|library|recent [limit] --json|status|pause|resume|next|previous|doctor"), jsonOutput)
	}
}

func playerAppPath() string {
	if path := os.Getenv("LILT_PLAYER_PATH"); path != "" {
		return path
	}
	return filepath.Join("player", "Build", "Products", "Release", "lilt-player.app")
}

func runDoctor(jsonOutput bool) int {
	client, err := player.Start(playerAppPath())
	if err != nil {
		return output(protocol.Failure("player_unavailable", err.Error()), jsonOutput)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	diagnostics, err := client.Diagnose(ctx)
	if err != nil {
		return output(protocol.Failure("diagnostics_failed", err.Error()), jsonOutput)
	}
	return output(protocol.Success(diagnostics), jsonOutput)
}

func runLibrary(jsonOutput bool) int {
	client, err := player.Start(playerAppPath())
	if err != nil {
		return output(protocol.Failure("player_unavailable", err.Error()), jsonOutput)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil {
		return output(protocol.Failure("library_failed", err.Error()), jsonOutput)
	}
	return output(protocol.Success(playlists), jsonOutput)
}

func runSearch(jsonOutput bool, term string) int {
	client, err := player.Start(playerAppPath())
	if err != nil {
		return output(protocol.Failure("search_failed", err.Error()), jsonOutput)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	items, err := client.Search(ctx, term, 20)
	if err != nil {
		return output(protocol.Failure("search_failed", err.Error()), jsonOutput)
	}
	return output(protocol.Success(items), jsonOutput)
}

func runRecent(jsonOutput bool, limit int) int {
	client, err := player.Start(playerAppPath())
	if err != nil {
		return output(protocol.Failure("recent_failed", err.Error()), jsonOutput)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	items, err := client.RecentPlayed(ctx, limit)
	if err != nil {
		return output(protocol.Failure("recent_failed", err.Error()), jsonOutput)
	}
	return output(protocol.Success(items), jsonOutput)
}

func output(envelope protocol.Envelope, jsonOutput bool) int {
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(envelope)
	} else if envelope.OK {
		fmt.Println("ok")
	} else {
		fmt.Fprintln(os.Stderr, envelope.Error.Code+": "+envelope.Error.Message)
	}
	if envelope.OK {
		return 0
	}
	return 1
}

func startTUI(mode string, args []string, initialTerm string, autoPlay bool) int {
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fake := fs.Bool("fake", os.Getenv("LILT_FAKE_PLAYER") == "1", "use fake player")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var target core.PlaybackTarget
	var provider tui.Provider
	authorization := core.AuthorizationStatus{Status: "unknown"}
	var closer interface{ Close() error }
	if *fake {
		fakeTarget := session.NewFakeTarget()
		target, provider = fakeTarget, fakeTarget
		authorization = core.AuthorizationStatus{Status: "denied"}
	} else {
		client, err := player.Start(playerAppPath())
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot start lilt-player:", err)
			return 1
		}
		authCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		status, err := client.Authorization(authCtx)
		cancel()
		if err != nil {
			_ = client.Close()
			fmt.Fprintln(os.Stderr, "cannot read MusicKit authorization status:", err)
			return 1
		}
		if status.Status == "not_determined" {
			fmt.Fprintln(os.Stderr, "Apple Music access is not determined; macOS will show its system authorization dialog. lilt never asks for an Apple ID or password.")
			authCtx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
			requested, requestErr := client.RequestAuthorization(authCtx, true)
			cancel()
			if requestErr != nil {
				if errors.Is(requestErr, context.DeadlineExceeded) || errors.Is(requestErr, context.Canceled) {
					_ = client.Close()
					fmt.Fprintln(os.Stderr, "Apple Music authorization timed out; close the system dialog and run lilt again")
					return 1
				}
				fmt.Fprintln(os.Stderr, "Apple Music authorization was not completed; continuing in preview mode:", requestErr)
			} else {
				status = requested
			}
		}
		authorization = status
		target, provider = client, client
		closer = client
		go func() {
			_, _ = io.Copy(os.Stderr, client.Stderr())
		}()
	}
	store, err := state.Load(state.Path())
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		fmt.Fprintln(os.Stderr, "state:", err)
		return 1
	}
	loaded, err := presets.Load(presets.Path())
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		fmt.Fprintln(os.Stderr, "presets:", err)
		return 1
	}
	byKey := make(map[string]presets.Preset, len(loaded))
	presetItems := make([]core.Item, 0, len(loaded))
	for _, preset := range loaded {
		byKey[preset.Key] = preset
		presetItems = append(presetItems, core.Item{Kind: "preset", ID: preset.Key, Title: preset.Label, Artist: preset.Kind + " · " + preset.Query})
	}
	resolve := func(ctx context.Context, key string) (core.Item, error) {
		preset, ok := byKey[key]
		if !ok {
			return core.Item{}, fmt.Errorf("unknown preset %q", key)
		}
		item, err := presets.Resolve(ctx, provider, store, preset)
		if err == nil {
			_ = store.Save()
		}
		return item, err
	}
	server, err := session.Start(session.SocketPath(), target)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		if errors.Is(err, session.ErrActive) {
			fmt.Fprintln(os.Stderr, "active_session: a lilt TUI is already running")
			return 1
		}
		fmt.Fprintln(os.Stderr, "session:", err)
		return 1
	}
	defer server.Close()
	if closer != nil {
		defer closer.Close()
	}
	source := store.LastSource
	if source != "radio" {
		source = "apple-music"
	}
	opts := tui.Options{
		Provider:      provider,
		Player:        target.(tui.Player),
		Radio:         radio.New(),
		Store:         store,
		Authorization: authorization,
		Presets:       presetItems,
		Resolve:       resolve,
		InitialTerm:   initialTerm,
		AutoPlay:      autoPlay,
		Focus:         mode == "focus",
		Source:        source,
	}
	if err := tui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "TUI:", err)
		return 1
	}
	return 0
}
