package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/client"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/icy"
	"github.com/caiguo/lilt/internal/journal"
	"github.com/caiguo/lilt/internal/player"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/server"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/tui"
)

var logger *journal.Logger

const usage = "usage: lilt serve [--detach] [--fake] | tui [--fake] | quit | api | sources | status [--queue] | play <ref> [--name T] [--shuffle] [--repeat MODE] | play-songs <ref,..> [--start N] | pause | toggle | resume | next | previous | stop | shuffle on|off | repeat off|all|one | queue [list] | queue add <ref> --next|--append | queue remove <index> | queue move <from> <to> | queue clear | search <term> [--source S] [--type T] [--limit N] | trending [--source S] [--type song|playlist] [--limit N] | playlist <ref> | library [--source S] | recent [N] | favorites [--source S] | radio search [...] | radio options --facet F | radio probe --url URL | radio cache | auth status [SOURCE] | auth <SOURCE> | auth cancel <FLOW_ID> | auth disconnect <SOURCE> | log [N]"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) (code int) {
	logger = journal.Open()
	defer logger.Close()
	cwd, _ := os.Getwd()
	logger.Log("cli", map[string]any{"args": args, "cwd": cwd})
	defer func() { logger.Log("cli.exit", map[string]any{"code": code}) }()

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
		return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, usage)), jsonOutput)
	}
	command := args[0]
	if command == "search" && containsArg(args[1:], "--play") {
		term := firstPositional(args[1:])
		if term == "" {
			return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, "usage: lilt search <term> --play")), jsonOutput)
		}
		return startTUI("search", nil, term, true)
	}
	switch command {
	case "serve":
		return startServe(jsonOutput, args[1:])
	case "tui":
		return startTUI("tui", args[1:], "", false)
	case "api":
		description := api.NewRegistry().Describe()
		return output(api.Success(api.NewRequestID(), description), jsonOutput)
	case "sources", "status", "favorites", "library", "recent", "search", "playlist",
		"radio", "trending", "play", "play-songs", "queue", "pause", "toggle", "resume", "next",
		"previous", "stop", "shuffle", "repeat", "auth", "quit":
		return runRemote(command, args[1:], jsonOutput)
	case "log":
		return runLog(args[1:])
	case "doctor":
		return runDoctor(jsonOutput)
	default:
		return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, usage)), jsonOutput)
	}
}

// runRemote dispatches a command that requires the server, auto-starting it
// once when absent. A local usage error is a client error, not a transport one.
func runRemote(command string, args []string, jsonOutput bool) int {
	response, err := remoteCommand(command, args)
	if errors.Is(err, api.ErrNoActiveSession) || (err == nil && response.Error != nil && response.Error.Code == api.CodeNoActiveSession) {
		if command == "quit" {
			// Stopping a server that is not running is a successful no-op; do
			// not start a server just to shut it down.
			return output(api.Success("", map[string]any{"stopped": false, "reason": "no_active_session"}), jsonOutput)
		}
		if startErr := autoStartServer(); startErr != nil {
			return output(api.Failure("", api.Errorf(api.CodeSessionUnavailable, "%v", startErr)), jsonOutput)
		}
		response, err = remoteCommand(command, args)
	}
	if err != nil {
		return output(errorResponse(response, err), jsonOutput)
	}
	return output(response, jsonOutput)
}

// errorResponse maps a client-side failure to an envelope. A stable server
// error (an *api.Error already carried by response) is passed through
// unchanged so callers can branch on error.code and read details.
func errorResponse(response api.Response, err error) api.Response {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && response.Error != nil {
		return response
	}
	if errors.Is(err, api.ErrNoActiveSession) {
		return api.Failure("", api.Errorf(api.CodeNoActiveSession, "%v", err))
	}
	if errors.Is(err, api.ErrTransport) {
		return api.Failure("", api.Errorf(api.CodeSessionUnavailable, "%v", err))
	}
	// Local parse/usage failures never reach the server.
	return api.Failure("", api.Errorf(api.CodeInvalidRequest, "%v", err))
}

func remoteCommand(command string, args []string) (api.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cli := client.New(api.SocketPath())
	switch command {
	case "sources":
		return cli.Call(ctx, "sources.list", nil)
	case "status":
		includeQueue := false
		for _, arg := range args {
			if arg == "--queue" {
				includeQueue = true
			}
		}
		return cli.Call(ctx, "session.status", map[string]any{"includeQueue": includeQueue})
	case "quit":
		return cli.Call(ctx, "session.shutdown", nil)
	case "stop":
		return cli.Call(ctx, "playback.stop", nil)
	case "pause", "toggle", "resume", "next", "previous":
		return cli.Call(ctx, "playback."+command, nil)
	case "play":
		ref, name, shuffle, repeat, parseErr := parsePlayArgs(args)
		if parseErr != nil {
			return api.Response{}, parseErr
		}
		params := map[string]any{"ref": ref}
		if name != "" {
			params["name"] = name
		}
		if shuffle != nil {
			params["shuffle"] = *shuffle
		}
		if repeat != "" {
			params["repeat"] = repeat
		}
		return cli.Call(ctx, "playback.play", params)
	case "play-songs":
		refs, start, shuffle, repeat := parsePlaySongsArgs(args)
		if len(refs) == 0 {
			return api.Response{}, errors.New("usage: lilt play-songs <ref,..> [--start N] [--shuffle] [--repeat off|all|one]")
		}
		params := map[string]any{"refs": refs, "startIndex": start}
		if shuffle != nil {
			params["shuffle"] = *shuffle
		}
		if repeat != "" {
			params["repeat"] = repeat
		}
		return cli.Call(ctx, "playback.playSongs", params)
	case "shuffle":
		if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
			return api.Response{}, errors.New("usage: lilt shuffle on|off")
		}
		return cli.Call(ctx, "playback.setShuffle", map[string]any{"on": args[0] == "on"})
	case "repeat":
		if len(args) != 1 || (args[0] != "off" && args[0] != "all" && args[0] != "one") {
			return api.Response{}, errors.New("usage: lilt repeat off|all|one")
		}
		return cli.Call(ctx, "playback.setRepeat", map[string]any{"mode": args[0]})
	case "queue":
		return queueCommand(ctx, cli, args)
	case "search":
		return searchCommand(ctx, cli, args)
	case "trending":
		return trendingCommand(ctx, cli, args)
	case "playlist":
		if len(args) != 1 {
			return api.Response{}, errors.New("usage: lilt playlist <ref>")
		}
		return cli.Call(ctx, "playlist.tracks", map[string]any{"ref": args[0]})
	case "library":
		source := flagValue(args, "--source", string(api.SourceAppleMusic))
		return cli.Call(ctx, "library.playlists", map[string]any{"source": source})
	case "recent":
		limit := 25
		if len(args) >= 1 {
			if parsed, err := strconv.Atoi(args[0]); err == nil {
				limit = parsed
			}
		}
		return cli.Call(ctx, "recent.list", map[string]any{"limit": limit})
	case "favorites":
		source := flagValue(args, "--source", "")
		params := map[string]any{}
		if source != "" {
			params["source"] = source
		}
		return cli.Call(ctx, "favorites.list", params)
	case "radio":
		return radioCommand(ctx, cli, args)
	case "auth":
		return authCommand(ctx, cli, args)
	default:
		return api.Response{}, errors.New(usage)
	}
}

func queueCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) == 0 || args[0] == "list" {
		return cli.Call(ctx, "queue.list", nil)
	}
	switch args[0] {
	case "add":
		position := "append"
		ref := ""
		for _, arg := range args[1:] {
			switch arg {
			case "--next":
				position = "next"
			case "--append":
				position = "append"
			default:
				ref = arg
			}
		}
		if ref == "" {
			return api.Response{}, errors.New("usage: lilt queue add <ref> --next|--append")
		}
		return cli.Call(ctx, "queue.add", map[string]any{"ref": ref, "position": position})
	case "remove":
		if len(args) != 2 {
			return api.Response{}, errors.New("usage: lilt queue remove <index>")
		}
		index, err := strconv.Atoi(args[1])
		if err != nil {
			return api.Response{}, errors.New("usage: lilt queue remove <index>")
		}
		return cli.Call(ctx, "queue.remove", map[string]any{"index": index})
	case "move":
		if len(args) != 3 {
			return api.Response{}, errors.New("usage: lilt queue move <from> <to>")
		}
		from, fromErr := strconv.Atoi(args[1])
		to, toErr := strconv.Atoi(args[2])
		if fromErr != nil || toErr != nil {
			return api.Response{}, errors.New("usage: lilt queue move <from> <to>")
		}
		return cli.Call(ctx, "queue.move", map[string]any{"from": from, "to": to})
	case "clear":
		return cli.Call(ctx, "queue.clear", nil)
	default:
		return api.Response{}, errors.New("usage: lilt queue [list] | queue add <ref> --next|--append | queue remove <index> | queue move <from> <to> | queue clear")
	}
}

func searchCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	source := fs.String("source", string(api.SourceAppleMusic), "source")
	kind := fs.String("type", "song", "song|playlist|station|all")
	limit := fs.Int("limit", 20, "maximum results")
	play := fs.Bool("play", false, "open the TUI and play the first result")
	if err := fs.Parse(flagsFirst(args, map[string]bool{"--source": true, "--type": true, "--limit": true})); err != nil {
		return api.Response{}, err
	}
	if fs.NArg() != 1 {
		return api.Response{}, errors.New("usage: lilt search <term> [--source S] [--type T] [--limit N] [--play]")
	}
	if *play {
		return api.Response{}, errors.New("--play must be handled by the TUI")
	}
	if *source == string(api.SourceRadio) {
		return api.Response{}, errors.New("radio discovery uses: lilt radio search --name <term> [--tag T]")
	}
	return cli.Call(ctx, "discovery.search", map[string]any{
		"source": *source, "term": fs.Arg(0), "type": *kind, "limit": *limit,
	})
}

// trendingCommand prints a source's trending tracks or playlists.
func trendingCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	fs := flag.NewFlagSet("trending", flag.ContinueOnError)
	source := fs.String("source", "audius", "source")
	kind := fs.String("type", "song", "song|playlist")
	limit := fs.Int("limit", 20, "maximum results")
	if err := fs.Parse(flagsFirst(args, map[string]bool{"--source": true, "--type": true, "--limit": true})); err != nil {
		return api.Response{}, err
	}
	if fs.NArg() != 0 {
		return api.Response{}, errors.New("usage: lilt trending [--source S] [--type song|playlist] [--limit N]")
	}
	return cli.Call(ctx, "discovery.trending", map[string]any{
		"source": *source, "type": *kind, "limit": *limit,
	})
}

func radioCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) == 0 {
		return api.Response{}, errors.New("usage: lilt radio search|options|probe ...")
	}
	switch args[0] {
	case "cache":
		return cli.Call(ctx, "radio.cache", nil)
	case "search":
		fs := flag.NewFlagSet("radio-search", flag.ContinueOnError)
		name := fs.String("name", "", "station name text")
		tag := fs.String("tag", "", "genre tag")
		language := fs.String("language", "", "language")
		country := fs.String("country", "", "country code")
		limit := fs.Int("limit", 10, "maximum stations")
		offset := fs.Int("offset", 0, "pagination offset")
		origin := fs.String("origin", "all", "builtin|directory|all")
		valueFlags := map[string]bool{"--name": true, "--tag": true, "--language": true, "--country": true, "--limit": true, "--offset": true, "--origin": true}
		if err := fs.Parse(flagsFirst(args[1:], valueFlags)); err != nil {
			return api.Response{}, err
		}
		return cli.Call(ctx, "radio.search", map[string]any{
			"name": *name, "tag": *tag, "language": *language, "countryCode": *country,
			"limit": *limit, "offset": *offset, "origin": *origin,
		})
	case "options":
		fs := flag.NewFlagSet("radio-options", flag.ContinueOnError)
		facet := fs.String("facet", "tag", "tag|language|country")
		origin := fs.String("origin", api.OriginDirectory, "builtin|directory|all")
		if err := fs.Parse(flagsFirst(args[1:], map[string]bool{"--facet": true, "--origin": true})); err != nil {
			return api.Response{}, err
		}
		return cli.Call(ctx, "radio.options", map[string]any{"facet": *facet, "origin": *origin})
	case "probe":
		fs := flag.NewFlagSet("radio-probe", flag.ContinueOnError)
		url := fs.String("url", "", "stream URL")
		if err := fs.Parse(flagsFirst(args[1:], map[string]bool{"--url": true})); err != nil {
			return api.Response{}, err
		}
		if *url == "" {
			return api.Response{}, errors.New("usage: lilt radio probe --url URL")
		}
		return cli.Call(ctx, "radio.probe", map[string]any{"url": *url})
	default:
		return api.Response{}, errors.New("usage: lilt radio search|options|probe ...")
	}
}

func authCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) == 0 {
		return api.Response{}, errors.New("usage: lilt auth status [SOURCE] | auth <SOURCE> | auth cancel <FLOW_ID> | auth disconnect <SOURCE>")
	}
	switch args[0] {
	case "status":
		if len(args) >= 2 {
			return cli.Call(ctx, "authorization.status", map[string]any{"source": args[1]})
		}
		return cli.Call(ctx, "authorization.list", nil)
	case "cancel":
		if len(args) != 2 {
			return api.Response{}, errors.New("usage: lilt auth cancel <FLOW_ID>")
		}
		return cli.Call(ctx, "authorization.cancel", map[string]any{"flowId": args[1]})
	case "disconnect":
		if len(args) != 2 {
			return api.Response{}, errors.New("usage: lilt auth disconnect <SOURCE>")
		}
		return cli.Call(ctx, "authorization.disconnect", map[string]any{"source": args[1]})
	default:
		return beginAuth(ctx, cli, args[0])
	}
}

func beginAuth(ctx context.Context, cli *client.Client, source string) (api.Response, error) {
	response, err := cli.Call(ctx, "authorization.begin", map[string]any{"source": source, "interactive": true})
	if err != nil {
		return response, err
	}
	var flow api.AuthorizationFlow
	if err := json.Unmarshal(response.Data, &flow); err != nil {
		return response, err
	}
	printedURL := false
	if flow.Interaction.URL != "" {
		fmt.Fprintln(os.Stderr, "open:", flow.Interaction.URL)
		printedURL = true
	} else if flow.Interaction.Type == api.InteractionSystemDialog {
		fmt.Fprintln(os.Stderr, "complete the system authorization dialog…")
	}
	for flow.Status == api.FlowPending {
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case <-time.After(time.Second):
		}
		poll, pollErr := cli.Call(ctx, "authorization.flowStatus", map[string]any{"flowId": flow.FlowID})
		if pollErr != nil {
			return poll, pollErr
		}
		if err := json.Unmarshal(poll.Data, &flow); err != nil {
			return poll, err
		}
		// A browser flow publishes its URL shortly after the begin response.
		if !printedURL && flow.Interaction.URL != "" {
			fmt.Fprintln(os.Stderr, "open:", flow.Interaction.URL)
			printedURL = true
		}
	}
	data, _ := json.Marshal(flow)
	return api.Response{OK: true, Data: data}, nil
}

// --- server lifecycle -------------------------------------------------------

func startServe(jsonOutput bool, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	detach := fs.Bool("detach", false, "run the server in the background")
	fake := fs.Bool("fake", os.Getenv("LILT_FAKE_PLAYER") == "1", "use the fake engine")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *detach && os.Getenv("LILT_SERVE_CHILD") != "1" {
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		responds := client.New(api.SocketPath()).ServerResponds(probeCtx)
		cancel()
		if responds {
			return output(api.Failure("", api.Errorf(api.CodeActiveSession, "a lilt server is already running")), jsonOutput)
		}
		exe, err := os.Executable()
		if err != nil {
			return output(api.Failure("", api.Errorf(api.CodeSessionUnavailable, "%v", err)), jsonOutput)
		}
		childArgs := []string{"serve"}
		if *fake {
			childArgs = append(childArgs, "--fake")
		}
		child := exec.Command(exe, childArgs...)
		child.Env = append(os.Environ(), "LILT_SERVE_CHILD=1")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := child.Start(); err != nil {
			return output(api.Failure("", api.Errorf(api.CodeSessionUnavailable, "%v", err)), jsonOutput)
		}
		return output(api.Success("", map[string]any{"detached": true, "pid": child.Process.Pid}), jsonOutput)
	}

	logger.Log("serve.start", map[string]any{"fake": *fake})
	store, err := state.Load(state.Path())
	if err != nil {
		if store == nil {
			store = state.New(state.Path())
		}
		fmt.Fprintln(os.Stderr, "State warning:", err)
	}
	radioCache, cacheErr := radio.LoadCache(radio.CachePath())
	if cacheErr != nil {
		fmt.Fprintln(os.Stderr, "Radio cache warning:", cacheErr)
	}

	options := server.Options{
		SocketPath:  api.SocketPath(),
		Store:       store,
		Radio:       radio.New(),
		RadioCache:  radioCache,
		ICY:         icy.New(),
		SecureStore: securestore.Default(),
		Log:         logger.Log,
	}
	if *fake {
		options.Engine = fakeengine.NewFakeEngine()
	} else {
		options.EngineFactory = playerEngineFactory()
	}
	srv, err := server.Start(options)
	if err != nil {
		if errors.Is(err, server.ErrActive) {
			fmt.Fprintln(os.Stderr, "active_session: a lilt server is already running")
			return 1
		}
		fmt.Fprintln(os.Stderr, "server:", err)
		return 1
	}
	defer srv.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-srv.ShutdownRequested():
	case <-signals:
	}
	logger.Log("serve.quit", nil)
	return 0
}

// playerEngineFactory builds a fresh signed helper. The server calls it at
// startup and again after a helper transport failure, so each call wires its
// own stderr logging and authorization check.
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

func autoStartServer() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(exe, "serve")
	child.Env = append(os.Environ(), "LILT_SERVE_CHILD=1")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		return err
	}
	cli := client.New(api.SocketPath())
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		responds := cli.ServerResponds(ctx)
		cancel()
		if responds {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("server did not become ready in 10s")
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

func runLog(args []string) int {
	n := 50
	if len(args) >= 1 {
		if value, err := strconv.Atoi(args[0]); err == nil {
			n = value
		}
	}
	entries, err := journal.Tail(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "log:", err)
		return 1
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, entry := range entries {
		_ = encoder.Encode(entry)
	}
	return 0
}

// --- TUI --------------------------------------------------------------------

func startTUI(mode string, args []string, initialTerm string, autoPlay bool) int {
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fake := fs.Bool("fake", os.Getenv("LILT_FAKE_PLAYER") == "1", "use the fake engine")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	logger.Log("tui.start", map[string]any{"mode": mode, "fake": *fake, "autoPlay": autoPlay})

	if !*fake {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		responds := client.New(api.SocketPath()).ServerResponds(ctx)
		cancel()
		if !responds {
			if err := autoStartServer(); err != nil {
				fmt.Fprintln(os.Stderr, "cannot start lilt server:", err)
				return 1
			}
		}
	}

	cli := client.New(api.SocketPath())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial, updates, watcher, err := cli.StateFeed(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot watch server state:", err)
		return 1
	}
	defer watcher.Close()

	appState, stateErr := cli.AppState(ctx)
	store := storeFromAppState(appState)
	authorization, authErr := cli.Authorization(ctx)
	if authErr != nil {
		authorization = core.AuthorizationStatus{Status: "unknown"}
	}

	startupWarning := ""
	if stateErr != nil {
		startupWarning = "State warning: " + stateErr.Error()
	}

	source := string(appState.LastSource)
	if source != "radio" && source != "audius" {
		source = "apple-music"
	}
	// Hydrate the TUI's in-memory probe cache from the server-owned persistent
	// cache; the TUI never writes it back to disk.
	radioCache, radioCacheErr := cli.RadioCache(ctx)
	if radioCacheErr != nil || radioCache == nil {
		radioCache = radio.NewCache("")
	}
	opts := tui.Options{
		Provider:       cli,
		Player:         cli,
		Radio:          cli,
		Remote:         cli,
		RadioCache:     radioCache,
		Store:          store,
		Authorization:  authorization,
		InitialTerm:    initialTerm,
		AutoPlay:       autoPlay,
		Source:         source,
		Log:            logger.Log,
		InitialState:   initial,
		StateUpdates:   updates,
		StartupWarning: startupWarning,
	}
	if err := tui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "TUI:", err)
		return 1
	}
	return 0
}

// storeFromAppState hydrates an in-memory mirror from the authoritative state.
func storeFromAppState(appState api.AppState) *state.Store {
	store := state.NewMemory()
	store.Theme = appState.Theme
	store.LastSource = string(appState.LastSource)
	for _, item := range appState.Favorites {
		url := item.URL
		if item.Source == api.SourceAudius {
			url = ""
		}
		source := string(item.Source)
		store.Favorites[source] = append(store.Favorites[source], state.Favorite{ID: item.ID, Source: source, Kind: item.Kind, Title: item.Title, Artist: item.Artist, URL: url})
	}
	for _, entry := range appState.Recent {
		store.Recent = append(store.Recent, state.Recent{ID: entry.Item.ID, Source: string(entry.Item.Source), Kind: entry.Item.Kind, Title: entry.Item.Title, Artist: entry.Item.Artist, URL: entry.Item.URL})
	}
	for _, entry := range appState.RecentContainers {
		store.RecentContainers = append(store.RecentContainers, state.RecentContainer{ID: entry.Item.ID, Source: string(entry.Item.Source), Kind: entry.Item.Kind, Title: entry.Item.Title})
	}
	return store
}

func rpcTrace(method string, duration time.Duration, err error) {
	fields := map[string]any{"method": method, "ms": duration.Milliseconds(), "ok": err == nil}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.Log("rpc", fields)
}

func playerAppPath() string {
	if path := os.Getenv("LILT_PLAYER_PATH"); path != "" {
		return path
	}
	return filepath.Join("player", "Build", "Products", "Release", "lilt-player.app")
}

// --- helpers ----------------------------------------------------------------

func output(envelope api.Response, jsonOutput bool) int {
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(envelope)
	} else if envelope.OK {
		fmt.Println("ok")
	} else if envelope.Error != nil {
		fmt.Fprintln(os.Stderr, presentation.Text(envelope.Error.Code+": "+envelope.Error.Message))
	}
	if envelope.OK {
		return 0
	}
	return 1
}

func flagsFirst(args []string, valueFlags map[string]bool) []string {
	flags := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if valueFlags[arg] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, arg)
	}
	return append(flags, rest...)
}

func flagValue(args []string, name, fallback string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return fallback
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func firstPositional(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func parsePlayArgs(args []string) (ref, name string, shuffle *bool, repeat string, err error) {
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--name":
			if i+1 >= len(rest) {
				return "", "", nil, "", errors.New("--name needs a value")
			}
			name = rest[i+1]
			i++
		case "--shuffle":
			value := true
			shuffle = &value
		case "--repeat":
			if i+1 >= len(rest) {
				return "", "", nil, "", errors.New("--repeat needs a value")
			}
			repeat = rest[i+1]
			i++
		default:
			if ref != "" {
				return "", "", nil, "", errors.New("usage: lilt play <ref> [--name T] [--shuffle] [--repeat MODE]")
			}
			ref = rest[i]
		}
	}
	if ref == "" {
		return "", "", nil, "", errors.New("usage: lilt play <ref> [--name T] [--shuffle] [--repeat MODE]")
	}
	return ref, name, shuffle, repeat, nil
}

func parsePlaySongsArgs(args []string) ([]string, int, *bool, string) {
	start := 0
	refs := []string{}
	rest := args
	var shuffle *bool
	repeat := ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--start":
			if i+1 < len(rest) {
				if parsed, err := strconv.Atoi(rest[i+1]); err == nil {
					start = parsed
				}
				i++
			}
			continue
		case "--shuffle":
			value := true
			shuffle = &value
			continue
		case "--repeat":
			if i+1 < len(rest) {
				repeat = rest[i+1]
				i++
			}
			continue
		}
		for _, part := range strings.Split(rest[i], ",") {
			if strings.TrimSpace(part) != "" {
				refs = append(refs, strings.TrimSpace(part))
			}
		}
	}
	return refs, start, shuffle, repeat
}
