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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/client"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/icy"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/journal"
	"github.com/caiguo/lilt/internal/presentation"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/server"
	"github.com/caiguo/lilt/internal/state"
	"github.com/caiguo/lilt/internal/tui"
)

var logger *journal.Logger

// version is the released build; override with -ldflags "-X main.version=...".
var version = "0.1.0"

const usage = "usage: lilt serve [--detach] [--fake] | tui [--fake] | quit | api | sources | status [--queue] | play <ref> [--name T] [--shuffle] [--repeat MODE] | play-songs <ref,..> [--start N] [--shuffle] [--repeat MODE] | pause | toggle | resume | next | previous | stop | shuffle on|off | repeat off|all|one | queue [list] | queue add <ref> --next|--append | queue remove <index> | queue move <from> <to> | queue jump <index> | queue clear | search <term> [--source S] [--type T] [--limit N] | trending [--source S] [--type song|playlist|all] [--limit N] | playlist <ref> | album <ref> | albums [--source S] | library [--source S] | recent [N] | favorites [--source S] | favorite add|remove <ref> | history [--limit N] [--before CURSOR] [--source S] | history stats <ref,..> | history clear --confirm | data reset --confirm | radio search [...] | radio options --facet F | radio probe --url URL | radio cache | jamendo setup [--client-id ID] | auth status [SOURCE] | auth <SOURCE> | auth cancel <FLOW_ID> | auth disconnect <SOURCE> | log [N] | version | help"

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
	case "help", "--help", "-h":
		fmt.Println(usage)
		fmt.Println("\nmachine-readable command catalog: lilt api --json")
		return 0
	case "version", "--version", "-v":
		value := strings.TrimPrefix(version, "v")
		if jsonOutput {
			return output(api.Success(api.NewRequestID(), map[string]string{"version": value}), true)
		}
		fmt.Println("lilt " + value)
		return 0
	case "serve":
		return startServe(jsonOutput, args[1:])
	case "tui":
		return startTUI("tui", args[1:], "", false)
	case "api":
		description := api.NewRegistry().Describe()
		return output(api.Success(api.NewRequestID(), description), jsonOutput)
	case "jamendo":
		return runJamendo(args[1:], jsonOutput)
	case "sources", "status", "favorites", "library", "albums", "album", "recent", "search", "playlist",
		"radio", "trending", "play", "play-songs", "queue", "pause", "toggle", "resume", "next",
		"previous", "stop", "shuffle", "repeat", "auth", "quit", "favorite", "history", "data":
		return runRemote(command, args[1:], jsonOutput)
	case "log":
		return runLog(args[1:])
	case "doctor":
		return runDoctor(jsonOutput)
	default:
		return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, usage)), jsonOutput)
	}
}

const jamendoDeveloperURL = jamendo.DeveloperPortalURL

func runJamendo(args []string, jsonOutput bool) int {
	if os.Getenv("LILT_FAKE_PLAYER") == "1" {
		return output(api.Failure("", api.Errorf(api.CodeUnsupportedCommand, "account setup is unavailable in fake mode")), jsonOutput)
	}
	clientID, parseErr := parseJamendoSetupArgs(args)
	if parseErr != nil {
		return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, "%v", parseErr)), jsonOutput)
	}
	if clientID == "" {
		if jsonOutput {
			return output(api.Failure("", api.Errorf(api.CodeInvalidRequest,
				"JSON mode requires `lilt jamendo setup --client-id <ID> --json`")), true)
		}
		fmt.Fprintln(os.Stderr, "Create a free read-only Jamendo app, then paste its client_id.")
		fmt.Fprintln(os.Stderr, jamendoDeveloperURL)
		// Browser launch is a convenience; a headless shell can use the printed
		// URL without turning setup into a failure.
		_ = openBrowser(jamendoDeveloperURL)
		fmt.Fprint(os.Stderr, "Jamendo client_id: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return output(api.Failure("", api.Errorf(api.CodeInvalidRequest, "could not read Jamendo client_id")), false)
		}
		clientID = strings.TrimSpace(line)
	}
	store := securestore.Default()
	if store == nil {
		return output(api.Failure("", api.Errorf(api.CodeStorageUnavailable,
			"secure storage is unavailable on this platform")), jsonOutput)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	setupClient := jamendo.Client{Credentials: func() (string, error) { return clientID, nil }}
	if apiErr := setupJamendo(ctx, store, setupClient, clientID); apiErr != nil {
		return output(api.Failure("", apiErr), jsonOutput)
	}
	prefix := clientID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return output(api.Success(api.NewRequestID(), map[string]any{
		"source":         api.SourceJamendo,
		"configured":     true,
		"clientIdPrefix": prefix,
	}), jsonOutput)
}

func parseJamendoSetupArgs(args []string) (string, error) {
	if len(args) == 0 || args[0] != "setup" {
		return "", errors.New("usage: lilt jamendo setup [--client-id ID]")
	}
	clientID := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--client-id":
			if i+1 >= len(args) {
				return "", errors.New("--client-id requires a value")
			}
			i++
			clientID = args[i]
		case strings.HasPrefix(arg, "--client-id="):
			clientID = strings.TrimPrefix(arg, "--client-id=")
		default:
			return "", fmt.Errorf("unknown Jamendo setup argument %q", arg)
		}
	}
	clientID = strings.TrimSpace(clientID)
	return clientID, nil
}

func setupJamendo(ctx context.Context, store securestore.Store, client jamendo.Client, clientID string) *api.Error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return api.Errorf(api.CodeInvalidRequest, "Jamendo client_id is required")
	}
	client.Credentials = func() (string, error) { return clientID, nil }
	if apiErr := client.Validate(ctx); apiErr != nil {
		return apiErr
	}
	if err := jamendo.SaveClientID(store, clientID); err != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "Jamendo client_id could not be stored")
	}
	return nil
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
	if command == "quit" && err == nil && response.OK {
		// The server answers quit and then drains: the browser engine closes
		// Chromium, which takes seconds, and the socket keeps answering while
		// it does. An explicit engine switch must not attach to the draining
		// server and then hit EOF instead of starting the requested engine.
		// Wait for the socket to actually stop answering before returning.
		deadline := time.Now().Add(30 * time.Second)
		waitForServerGone(deadline, func() bool {
			probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
			defer probeCancel()
			return client.New(api.SocketPath()).ServerResponds(probeCtx)
		})
	}
	if err != nil {
		return output(errorResponse(response, err), jsonOutput)
	}
	return outputCommand(command, firstSubcommand(command, args), response, jsonOutput)
}

// firstSubcommand returns the radio/auth/queue subcommand so renderers can
// distinguish "radio probe" from "radio search".
func firstSubcommand(command string, args []string) string {
	if command == "radio" && len(args) > 0 {
		return args[0]
	}
	return ""
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
	case "album":
		if len(args) != 1 {
			return api.Response{}, errors.New("usage: lilt album <ref>")
		}
		return cli.Call(ctx, "album.tracks", map[string]any{"ref": args[0]})
	case "albums":
		source := flagValue(args, "--source", string(api.SourceAppleMusic))
		return cli.Call(ctx, "library.albums", map[string]any{"source": source})
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
	case "favorite":
		return favoriteCommand(ctx, cli, args)
	case "history":
		return historyCommand(ctx, cli, args)
	case "data":
		return dataCommand(ctx, cli, args)
	case "radio":
		return radioCommand(ctx, cli, args)
	case "auth":
		return authCommand(ctx, cli, args)
	default:
		return api.Response{}, errors.New(usage)
	}
}

// favoriteCommand implements `lilt favorite add|remove <ref>`. Both are
// idempotent; add resolves a complete item before writing.
func favoriteCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) != 2 {
		return api.Response{}, errors.New("usage: lilt favorite add|remove <ref>")
	}
	switch args[0] {
	case "add":
		return cli.Call(ctx, "favorites.add", map[string]any{"ref": args[1]})
	case "remove":
		return cli.Call(ctx, "favorites.remove", map[string]any{"ref": args[1]})
	default:
		return api.Response{}, errors.New("usage: lilt favorite add|remove <ref>")
	}
}

// historyCommand implements `lilt history [list]`, `history stats <refs>` and
// `history clear --confirm`.
func historyCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) == 0 || args[0] == "list" {
		rest := args
		if len(args) > 0 && args[0] == "list" {
			rest = args[1:]
		}
		params := map[string]any{}
		if source := flagValue(rest, "--source", ""); source != "" {
			params["source"] = source
		}
		if before := flagValue(rest, "--before", ""); before != "" {
			params["before"] = before
		}
		if limit := flagValue(rest, "--limit", ""); limit != "" {
			n, err := strconv.Atoi(limit)
			if err != nil {
				return api.Response{}, errors.New("usage: lilt history [--limit N]")
			}
			params["limit"] = n
		}
		return cli.Call(ctx, "history.list", params)
	}
	switch args[0] {
	case "stats":
		refs := []string{}
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--") {
				continue
			}
			refs = append(refs, strings.Split(arg, ",")...)
		}
		if len(refs) == 0 {
			return api.Response{}, errors.New("usage: lilt history stats <ref>... [--json]")
		}
		return cli.Call(ctx, "history.stats", map[string]any{"refs": refs})
	case "clear":
		if !containsArg(args, "--confirm") {
			return api.Response{}, errors.New("refusing to clear history without --confirm")
		}
		return cli.Call(ctx, "history.clear", map[string]any{"confirm": true})
	default:
		return api.Response{}, errors.New("usage: lilt history [list|stats|clear]")
	}
}

// dataCommand implements `lilt data reset --confirm`, the explicit recovery
// path for an unhealthy activity database.
func dataCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	if len(args) == 0 || args[0] != "reset" {
		return api.Response{}, errors.New("usage: lilt data reset --confirm")
	}
	if !containsArg(args, "--confirm") {
		return api.Response{}, errors.New("refusing to reset the activity database without --confirm")
	}
	return cli.Call(ctx, "activity.reset", map[string]any{"confirm": true})
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
	case "jump":
		if len(args) != 2 {
			return api.Response{}, errors.New("usage: lilt queue jump <index>")
		}
		index, err := strconv.Atoi(args[1])
		if err != nil {
			return api.Response{}, errors.New("usage: lilt queue jump <index>")
		}
		return cli.Call(ctx, "queue.jump", map[string]any{"index": index})
	case "clear":
		return cli.Call(ctx, "queue.clear", nil)
	default:
		return api.Response{}, errors.New("usage: lilt queue [list] | queue add <ref> --next|--append | queue remove <index> | queue move <from> <to> | queue jump <index> | queue clear")
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

// trendingCommand prints a source's trending tracks or playlists; the
// default type "all" returns exactly the kinds the source declares.
func trendingCommand(ctx context.Context, cli *client.Client, args []string) (api.Response, error) {
	fs := flag.NewFlagSet("trending", flag.ContinueOnError)
	source := fs.String("source", "audius", "source")
	kind := fs.String("type", "all", "song|playlist|all")
	limit := fs.Int("limit", 20, "maximum results")
	if err := fs.Parse(flagsFirst(args, map[string]bool{"--source": true, "--type": true, "--limit": true})); err != nil {
		return api.Response{}, err
	}
	if fs.NArg() != 0 {
		return api.Response{}, errors.New("usage: lilt trending [--source S] [--type song|playlist|all] [--limit N]")
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
	notify := func(line string) {
		fmt.Fprintln(os.Stderr, line)
	}
	if flow.Interaction.URL != "" {
		notify("open: " + flow.Interaction.URL)
		printedURL = true
	} else if flow.Interaction.Type == api.InteractionSystemDialog {
		notify("complete the system authorization dialog…")
	}
	// The flow's lifetime belongs to the server: it ends on the provider's
	// declared budget (expired, cancelled, completed, error). The CLI must not
	// impose its own deadline on top — the remote command ctx caps every other
	// command at 90s, and a slow sign-in used to die with a false timeout while
	// the window was still valid. Ctrl-C leaves the flow pending on the server.
	authCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	flow, err = awaitAuthFlow(authCtx, source, flow, printedURL, func(pollCtx context.Context) (api.AuthorizationFlow, error) {
		poll, pollErr := cli.Call(pollCtx, "authorization.flowStatus", map[string]any{"flowId": flow.FlowID})
		if pollErr != nil {
			return api.AuthorizationFlow{}, pollErr
		}
		var polled api.AuthorizationFlow
		if err := json.Unmarshal(poll.Data, &polled); err != nil {
			return api.AuthorizationFlow{}, err
		}
		return polled, nil
	}, notify)
	if err != nil {
		return response, err
	}
	data, _ := json.Marshal(flow)
	return api.Response{OK: true, Data: data}, nil
}

// awaitAuthFlow polls a pending flow until it reaches a terminal state or ctx
// is done. poll must return the flow's current state; notify prints one stderr
// line. Interrupted waits report where the flow still lives instead of a bare
// deadline error, so a user who walks away from a slow sign-in can find it again.
func awaitAuthFlow(ctx context.Context, source string, flow api.AuthorizationFlow, printedURL bool, poll func(context.Context) (api.AuthorizationFlow, error), notify func(string)) (api.AuthorizationFlow, error) {
	for flow.Status == api.FlowPending {
		select {
		case <-ctx.Done():
			notify(fmt.Sprintf("interrupted — flow %s is still pending on the server; check `lilt auth status %s` or cancel with `lilt auth cancel %s`", flow.FlowID, source, flow.FlowID))
			return flow, ctx.Err()
		case <-time.After(time.Second):
		}
		polled, err := poll(ctx)
		if err != nil {
			return flow, err
		}
		flow = polled
		// A browser flow publishes its URL shortly after the begin response.
		if !printedURL && flow.Interaction.URL != "" {
			notify("open: " + flow.Interaction.URL)
			printedURL = true
		}
	}
	return flow, nil
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
		exited := make(chan error, 1)
		go func() { exited <- child.Wait() }()
		readyCtx, readyCancel := context.WithTimeout(context.Background(), 10*time.Second)
		readyErr := awaitServerReady(readyCtx, func(ctx context.Context) bool {
			return client.New(api.SocketPath()).ServerResponds(ctx)
		}, exited)
		readyCancel()
		if readyErr != nil {
			_ = child.Process.Kill()
			return output(api.Failure("", api.Errorf(api.CodeSessionUnavailable, "%v", readyErr)), jsonOutput)
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
		SocketPath:   api.SocketPath(),
		LockPath:     state.LockPath(),
		ActivityPath: state.ActivityPath(),
		QueuePacing:  queuePacingFromEnv(),
		Store:        store,
		Radio:        radio.New(),
		RadioCache:   radioCache,
		ICY:          icy.New(),
		SecureStore:  securestore.Default(),
		Log:          logger.Log,
	}
	if *fake {
		options.Engine = fakeengine.NewFakeEngine()
		options.SecureStore = securestore.NewMemory()
	} else {
		if err := configurePlatform(&options); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			return 1
		}
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

func awaitServerReady(ctx context.Context, responds func(context.Context) bool, exited <-chan error) error {
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		ready := responds(probeCtx)
		cancel()
		if ready {
			return nil
		}
		select {
		case err := <-exited:
			if err == nil {
				return errors.New("server exited before becoming ready")
			}
			return fmt.Errorf("server exited before becoming ready: %w", err)
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// initialSource picks the source a fresh session opens on: the remembered
// source when it is usable, then Apple Music, then the highest-priority
// available source.
//
// An unavailable source is skipped even when it was remembered. `state.get`
// defaults lastSource to apple-music, which Linux can never make available, so
// remembering it unconditionally pinned a fresh session to an empty screen and
// hid the sources that do work.
func initialSource(last api.SourceID, descriptors []api.SourceDescriptor) string {
	preferred := string(last)
	if preferred != "" {
		if descriptor, ok := descriptorFor(preferred, descriptors); ok && descriptor.Available {
			return preferred
		}
	}
	if descriptor, ok := descriptorFor(string(api.SourceAppleMusic), descriptors); ok && descriptor.Available {
		return string(api.SourceAppleMusic)
	}
	for _, descriptor := range descriptors {
		if descriptor.Available {
			return string(descriptor.ID)
		}
	}
	// Nothing is usable: keep the remembered source if it still exists, then the
	// highest-priority descriptor, so a fully unavailable snapshot still opens on
	// a stable screen.
	if preferred != "" {
		if _, ok := descriptorFor(preferred, descriptors); ok {
			return preferred
		}
	}
	if len(descriptors) > 0 {
		return string(descriptors[0].ID)
	}
	return string(api.SourceAppleMusic)
}

func descriptorFor(source string, descriptors []api.SourceDescriptor) (api.SourceDescriptor, bool) {
	for _, descriptor := range descriptors {
		if string(descriptor.ID) == source {
			return descriptor, true
		}
	}
	return api.SourceDescriptor{}, false
}

// serverRespondsSoon polls the socket until a server answers or the wait
// expires. Startup takes a few hundred milliseconds, so callers get a bounded
// window instead of racing the bind with one shot.
func serverRespondsSoon(wait time.Duration) bool {
	cli := client.New(api.SocketPath())
	deadline := time.Now().Add(wait)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		responds := cli.ServerResponds(ctx)
		cancel()
		if responds {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForServerGone polls responds until it reports false or the deadline
// passes. It never starts a server; the probe belongs to the caller.
func waitForServerGone(deadline time.Time, responds func() bool) {
	for time.Now().Before(deadline) {
		if !responds() {
			return
		}
		time.Sleep(200 * time.Millisecond)
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

	// A server started moments ago may not have bound the socket yet; one shot
	// probe raced startup and killed the TUI at first launch (usability batch
	// 2026-09-21-r12). Poll briefly, then auto-start once. Fake mode inherits
	// LILT_FAKE_PLAYER, so an auto-started server runs the fake engine too.
	if !serverRespondsSoon(time.Second) {
		if err := autoStartServer(); err != nil {
			fmt.Fprintln(os.Stderr, "cannot start lilt server:", err)
			return 1
		}
	}

	cli := client.New(api.SocketPath())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, updates, watcher, err := cli.SessionFeed(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot watch server state:", err)
		return 1
	}
	defer watcher.Close()

	appState := api.AppState{}
	if snapshot.State != nil {
		appState = *snapshot.State
	}
	store := storeFromAppState(appState)
	authorization := core.AuthorizationStatus{Status: "unknown"}
	for _, value := range snapshot.Authorizations {
		if value.Source == api.SourceAppleMusic {
			authorization = core.AuthorizationStatus{Status: value.Status, AccountLabel: value.AccountLabel}
			break
		}
	}

	source := initialSource(appState.LastSource, snapshot.Sources)
	// Hydrate the TUI's in-memory probe cache from the server-owned persistent
	// cache; the TUI never writes it back to disk.
	radioCache, radioCacheErr := cli.RadioCache(ctx)
	if radioCacheErr != nil || radioCache == nil {
		radioCache = radio.NewCache("")
	}
	opts := tui.Options{
		Provider:      cli,
		Player:        cli,
		Radio:         cli,
		Remote:        cli,
		RadioCache:    radioCache,
		Store:         store,
		Authorization: authorization,
		InitialTerm:   initialTerm,
		AutoPlay:      autoPlay,
		Source:        source,
		Log:           logger.Log,
		InitialWatch:  &snapshot,
		WatchUpdates:  updates,
		JamendoSetup: func(ctx context.Context, clientID string) error {
			if *fake {
				return errors.New("account setup is unavailable in fake mode")
			}
			// Same in-process validate-and-save path as `lilt jamendo setup`:
			// write the Keychain directly, leave the Client API untouched.
			store := securestore.Default()
			if store == nil {
				return errors.New("secure storage is unavailable on this platform")
			}
			if apiErr := setupJamendo(ctx, store, jamendo.Client{}, clientID); apiErr != nil {
				return errors.New(apiErr.Message)
			}
			return nil
		},
		OpenURL: func(target string) {
			if !*fake {
				// Browser launch is a convenience; failing to open must not break setup.
				_ = openBrowser(target)
			}
		},
	}
	if err := tui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "TUI:", err)
		return 1
	}
	return 0
}

// storeFromAppState hydrates an in-memory preference mirror from the
// authoritative state. Favorites and recent live in the server's activity
// store and reach the TUI through state.changed snapshots.
func storeFromAppState(appState api.AppState) *state.Store {
	store := state.NewMemory()
	store.Theme = appState.Theme
	store.LastSource = string(appState.LastSource)
	return store
}

func rpcTrace(method string, duration time.Duration, err error) {
	fields := map[string]any{"method": method, "ms": duration.Milliseconds(), "ok": err == nil}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.Log("rpc", fields)
}

// queuePacingFromEnv reads the finite-queue append gap. It exists so pacing can
// be probed without rebuilding; an unset or invalid value keeps the server
// default.
func queuePacingFromEnv() time.Duration {
	raw := os.Getenv("LILT_QUEUE_PACING_MS")
	if raw == "" {
		return 0
	}
	millis, err := strconv.Atoi(raw)
	if err != nil || millis <= 0 {
		return 0
	}
	return time.Duration(millis) * time.Millisecond
}

// --- helpers ----------------------------------------------------------------

func output(envelope api.Response, jsonOutput bool) int {
	return outputCommand("", "", envelope, jsonOutput)
}

// outputCommand is output with command context so human-readable output can
// render the payload instead of printing a bare "ok".
func outputCommand(command, subcommand string, envelope api.Response, jsonOutput bool) int {
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(envelope)
	} else if envelope.OK {
		if rendered := renderHuman(command, subcommand, envelope.Data); rendered != "" {
			fmt.Print(rendered)
		} else {
			fmt.Println("ok")
		}
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
