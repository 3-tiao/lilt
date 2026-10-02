package api

import (
	"encoding/json"
	"time"
)

// catalog returns the authoritative command list. `api.describe` serializes
// exactly this, and the server dispatches through it.
func catalog() []*Definition {
	defs := []*Definition{
		cmd("api.describe", "lilt api --json", time.Second, nil, "ApiDescription"),
		cmd("sources.list", "lilt sources --json", 5*time.Second, nil, "[SourceDescriptor]"),

		cmd("playback.play", "lilt play <ref> [--name T] [--shuffle] [--repeat MODE] --json", 60*time.Second,
			params(map[string]schemaProp{
				"ref":          {Type: "string"},
				"name":         {Type: "string"},
				"shuffle":      {Type: "boolean"},
				"repeat":       {Type: "string", Enum: []string{"off", "all", "one"}},
				"startAt":      {Type: "integer"},
				"startTrackID": {Type: "string"},
				"reverse":      {Type: "boolean"},
				"fromHere":     {Type: "boolean"},
			}, "ref"), "PlaybackState",
			CodeInvalidRequest, CodeInvalidReference, CodeSourceUnavailable, CodeSourceMismatch, CodeUnsupportedCommand,
			CodeAuthorizationRequired, CodeEngineRestarting, CodePreviewUnavailable,
			CodePartialFailure, CodePlaybackError, CodeOperationOutcomeUnknown),

		cmd("playback.playSongs", "lilt play-songs <ref,..> [--start N] [--shuffle] [--repeat MODE] --json", 60*time.Second,
			params(map[string]schemaProp{
				"refs":       {Type: "array", Items: "Reference"},
				"startIndex": {Type: "integer"},
				"shuffle":    {Type: "boolean"},
				"repeat":     {Type: "string", Enum: []string{"off", "all", "one"}},
			}, "refs"), "PlaybackState",
			CodeInvalidRequest, CodeInvalidReference, CodeSourceUnavailable, CodeSourceMismatch, CodeUnsupportedCommand,
			CodeAuthorizationRequired, CodeEngineRestarting,
			CodePartialFailure, CodePlaybackError, CodeOperationOutcomeUnknown),

		cmd("playback.pause", "lilt pause --json", 8*time.Second, nil, "PlaybackState", CodeInvalidState, CodePlaybackError),
		cmd("playback.toggle", "lilt toggle --json", 15*time.Second, nil, "PlaybackState", CodeInvalidState, CodePlaybackError),
		cmd("playback.resume", "lilt resume --json", 15*time.Second, nil, "PlaybackState", CodeInvalidState, CodePlaybackError),
		cmd("playback.next", "lilt next --json", 15*time.Second, nil, "PlaybackState", CodeUnsupportedCommand, CodeInvalidState, CodePlaybackError),
		cmd("playback.previous", "lilt previous --json", 15*time.Second, nil, "PlaybackState", CodeUnsupportedCommand, CodeInvalidState, CodePlaybackError),
		cmd("playback.stop", "lilt stop --json", 5*time.Second, nil, "PlaybackState",
			CodePlaybackError, CodeSourceUnavailable, CodeOperationOutcomeUnknown, CodeEngineRestarting),
		cmd("playback.setShuffle", "lilt shuffle on|off --json", 5*time.Second,
			params(map[string]schemaProp{"on": {Type: "boolean"}}, "on"), "PlaybackState", CodeUnsupportedCommand, CodeInvalidState),
		cmd("playback.setRepeat", "lilt repeat off|all|one --json", 5*time.Second,
			params(map[string]schemaProp{"mode": {Type: "string", Enum: []string{"off", "all", "one"}}}, "mode"),
			"PlaybackState", CodeUnsupportedCommand, CodeInvalidState),

		cmd("queue.list", "lilt queue [list] --json", 5*time.Second, nil, "QueueState",
			CodeQueueUnavailable, CodeEngineRestarting),
		cmd("queue.add", "lilt queue add <ref> --next|--append --json", 30*time.Second,
			params(map[string]schemaProp{
				"ref":             {Type: "string"},
				"position":        {Type: "string", Enum: []string{"next", "append"}},
				"ifQueueRevision": {Type: "integer"},
			}, "ref", "position"), "PlaybackState",
			CodeInvalidReference, CodeSourceMismatch, CodeQueueUnavailable, CodeEngineRestarting,
			CodeConflict),
		cmd("queue.jump", "lilt queue jump <index> --json", 20*time.Second,
			params(map[string]schemaProp{"index": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "index"),
			"PlaybackState", CodeQueueUnavailable, CodeQueueNotJumpable, CodeConflict, CodeInvalidRequest,
			CodePartialFailure, CodeOperationOutcomeUnknown, CodePlaybackError, CodeEngineRestarting),
		cmd("queue.remove", "lilt queue remove <index> --json", 5*time.Second,
			params(map[string]schemaProp{"index": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "index"),
			"QueueRemoveResult", CodeQueueUnavailable, CodeConflict, CodeInvalidRequest, CodeOperationOutcomeUnknown,
			CodeEngineRestarting),
		cmd("queue.undoRemove", "", 5*time.Second,
			params(map[string]schemaProp{"token": {Type: "string"}, "ifQueueRevision": {Type: "integer"}}, "token", "ifQueueRevision"),
			"PlaybackState", CodeUndoUnavailable, CodeConflict, CodeOperationOutcomeUnknown),
		cmd("queue.move", "lilt queue move <from> <to> --json", 5*time.Second,
			params(map[string]schemaProp{"from": {Type: "integer"}, "to": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "from", "to"),
			"PlaybackState", CodeQueueUnavailable, CodeConflict, CodeInvalidRequest, CodeEngineRestarting),
		cmd("queue.clear", "lilt queue clear --json", 5*time.Second,
			params(map[string]schemaProp{"ifQueueRevision": {Type: "integer"}}), "PlaybackState",
			CodeQueueUnavailable, CodeEngineRestarting, CodeConflict),

		cmd("discovery.search", "lilt search <term> --source S [--type T] [--limit N] --json", 45*time.Second,
			params(map[string]schemaProp{
				"source": {Type: "string"},
				"term":   {Type: "string"},
				"type":   {Type: "string", Enum: []string{"song", "album", "playlist", "station", "all"}},
				"limit":  {Type: "integer"},
			}, "source", "term", "type"), "SearchResult", CodeSearchFailed, CodeSourceUnavailable, CodeUnsupportedCommand),
		cmd("discovery.trending", "lilt trending --source S [--type song|playlist|all] [--limit N] --json", 45*time.Second,
			params(map[string]schemaProp{
				"source": {Type: "string"},
				"type":   {Type: "string", Enum: []string{"song", "playlist", "all"}},
				"limit":  {Type: "integer"},
			}, "source"), "SearchResult", CodeSearchFailed, CodeSourceUnavailable, CodeUnsupportedCommand),
		cmd("playlist.tracks", "lilt playlist <ref> --json", 45*time.Second,
			params(map[string]schemaProp{"ref": {Type: "string"}}, "ref"), "PlaylistTracksResult", CodeSearchFailed, CodeInvalidReference),
		cmd("album.tracks", "lilt album <ref> --json", 45*time.Second,
			params(map[string]schemaProp{"ref": {Type: "string"}}, "ref"), "AlbumTracksResult", CodeSearchFailed, CodeInvalidReference, CodeUnsupportedCommand),
		cmd("library.albums", "lilt albums [--source S] --json", 45*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}, "source"), "[Item]", CodeSearchFailed, CodeSourceUnavailable, CodeUnsupportedCommand),
		cmd("library.playlists", "lilt library [--source S] --json", 45*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}, "source"), "[Item]", CodeSearchFailed, CodeSourceUnavailable),
		cmd("recent.list", "lilt recent [N] --json", 5*time.Second,
			params(map[string]schemaProp{"limit": {Type: "integer"}}), "[Item]", CodeStorageUnavailable),
		cmd("recommendations.list", "", 45*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}, "limit": {Type: "integer"}}, "source"), "[Item]",
			CodeSearchFailed, CodeSourceUnavailable, CodeUnsupportedCommand, CodeAuthorizationRequired),

		cmd("radio.search", "lilt radio search [...] --json", 15*time.Second,
			params(map[string]schemaProp{
				"name":        {Type: "string"},
				"tag":         {Type: "string"},
				"language":    {Type: "string"},
				"countryCode": {Type: "string"},
				"limit":       {Type: "integer"},
				"offset":      {Type: "integer"},
				"origin":      {Type: "string", Enum: []string{"builtin", "directory", "all"}},
			}), "RadioSearchResult", CodeSearchFailed),
		cmd("radio.options", "", 15*time.Second,
			params(map[string]schemaProp{
				"facet":  {Type: "string", Enum: []string{"tag", "language", "country"}},
				"origin": {Type: "string", Enum: []string{"builtin", "directory", "all"}},
			}, "facet"), "RadioOptionsResult", CodeSearchFailed),
		cmd("radio.probe", "", 15*time.Second,
			params(map[string]schemaProp{"url": {Type: "string"}}, "url"), "RadioProbeResult"),
		cmd("radio.cache", "lilt radio cache --json", 5*time.Second, nil, "RadioCache"),

		cmd("state.get", "", 5*time.Second, nil, "AppState"),
		cmd("favorites.list", "lilt favorites --json", 5*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}), "[Item]", CodeStorageUnavailable),
		cmd("favorites.set", "", 5*time.Second,
			params(map[string]schemaProp{"item": {Ref: "Item"}, "favorited": {Type: "boolean"}}, "item", "favorited"),
			"FavoriteResult", CodeStorageUnavailable),
		cmd("favorites.add", "lilt favorite add <ref> --json", 45*time.Second,
			params(map[string]schemaProp{"ref": {Type: "string"}}, "ref"),
			"FavoriteResult", CodeInvalidReference, CodeUnsupportedCommand, CodeSearchFailed, CodeStorageUnavailable),
		cmd("favorites.remove", "lilt favorite remove <ref> --json", 5*time.Second,
			params(map[string]schemaProp{"ref": {Type: "string"}}, "ref"),
			"FavoriteResult", CodeInvalidReference, CodeStorageUnavailable),
		cmd("history.list", "lilt history --json", 5*time.Second,
			params(map[string]schemaProp{
				"source": {Type: "string"},
				"before": {Type: "string"},
				"limit":  {Type: "integer"},
			}), "HistoryPageResult", CodeStorageUnavailable, CodeInvalidRequest),
		cmd("history.stats", "lilt history stats <ref,..> --json", 5*time.Second,
			params(map[string]schemaProp{"refs": {Type: "array", Items: "string"}}, "refs"),
			"[HistoryStats]", CodeStorageUnavailable, CodeInvalidRequest),
		cmd("history.clear", "lilt history clear --confirm --json", 5*time.Second,
			params(map[string]schemaProp{"confirm": {Type: "boolean"}}),
			"HistoryClearResult", CodeStorageUnavailable, CodeInvalidRequest),
		cmd("activity.reset", "lilt data reset --confirm --json", 10*time.Second,
			params(map[string]schemaProp{"confirm": {Type: "boolean"}}),
			"ActivityResetResult", CodeInvalidRequest, CodeStorageUnavailable),
		cmd("ui.set", "", 5*time.Second,
			params(map[string]schemaProp{"theme": {Type: "string"}, "lastSource": {Type: "string"}}),
			"AppState", CodeStateSaveFailed),

		cmd("session.status", "lilt status [--queue] --json", 5*time.Second,
			params(map[string]schemaProp{"includeQueue": {Type: "boolean"}}), "PlaybackStatus|PlaybackState"),
		cmd("session.watch", "", 0,
			params(map[string]schemaProp{
				"includeState": {Type: "boolean"},
				"topics":       {Type: "array", Items: "string"},
			}), "WatchSnapshot", CodeInvalidRequest),
		cmd("session.shutdown", "lilt quit --json", 5*time.Second, params(nil), "{}", CodeInvalidRequest),

		cmd("authorization.list", "lilt auth status --json", 5*time.Second, nil, "[SourceAuthorization]"),
		cmd("authorization.status", "lilt auth status <SOURCE> --json", 5*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}, "source"), "SourceAuthorization", CodeInvalidRequest),
		cmd("authorization.begin", "lilt auth <SOURCE> --json", 10*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}, "interactive": {Type: "boolean"}}, "source", "interactive"),
			"AuthorizationFlow", CodeInvalidRequest, CodeAuthorizationInProgress, CodeAuthorizationFailed, CodeUnsupportedCommand),
		cmd("authorization.flowStatus", "", 5*time.Second,
			params(map[string]schemaProp{"flowId": {Type: "string"}}, "flowId"), "AuthorizationFlow", CodeAuthorizationFlowNotFound),
		cmd("authorization.cancel", "lilt auth cancel <FLOW_ID> --json", 5*time.Second,
			params(map[string]schemaProp{"flowId": {Type: "string"}}, "flowId"), "AuthorizationFlow", CodeAuthorizationFlowNotFound),
		cmd("authorization.disconnect", "lilt auth disconnect <SOURCE> --json", 10*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}, "source"), "SourceAuthorization",
			CodeInvalidRequest, CodeAuthorizationFailed, CodeUnsupportedCommand),
	}
	// Descriptions are non-normative guidance for agents. They never change
	// command behavior and clients must not branch on them.
	descriptions := map[string]string{
		"sources.list":         "List sources with per-capability availability. Read capabilities before issuing a source-scoped command; providers only support the capabilities they declare.",
		"discovery.search":     "Provider-scoped content search. `source` is required (for example apple-music or audius). Radio discovery uses radio.search, not this command. `type:\"all\"` returns only the search groups the source declares; an explicitly requested type the source does not declare returns unsupported_command.",
		"discovery.trending":   "Provider-scoped trending. `type` defaults to all and returns exactly the kinds the source declares; an explicitly requested undeclared kind returns unsupported_command. Optional extension: a source that does not implement trending returns unsupported_command.",
		"playlist.tracks":      "Fetch one playlist and its tracks by canonical playlist ref (source:playlist:id).",
		"library.playlists":    "List the user's library playlists for a source that declares the library capability.",
		"library.albums":       "List the user's library albums. Only sources whose library implementation exposes albums support this; others return unsupported_command.",
		"album.tracks":         "Fetch one album and its track listing by canonical album ref (source:album:id). Apple Music only.",
		"recommendations.list": "List source-provided recommendations for a source that declares the recommendations capability.",
		"radio.search":         "Search the radio source (vendored builtin snapshot plus the Radio Browser directory). Radio is not a discovery.search provider.",
	}
	for _, def := range defs {
		if description, ok := descriptions[def.Name]; ok {
			def.Description = description
		}
	}
	// queryCommands is the authoritative set of side-effect-free commands. It is
	// declared here, next to the catalog, so a new command cannot be added with
	// a side effect and silently become epoch-optional: everything not listed
	// MUST carry ifServerInstanceId (docs/internals/concurrency.md §5.2).
	// commandRegistryTest asserts this set exists in the catalog.
	for _, def := range defs {
		def.Query = queryCommands[def.Name]
		def.Concurrent = concurrentCommands[def.Name]
		if !def.Query {
			// Every command with side effects can be rejected before it runs
			// (a full request ledger), so server_busy belongs to each one's
			// declared error set rather than to a hand-maintained subset.
			def.Errors = append(def.Errors, CodeServerBusy)
		}
		// Concurrent commands run outside the serialized mutation slot: upstream
		// discovery and the radio cache must not wait behind playback control, and
		// playback control must not wait behind a slow provider. They therefore
		// have no admission budget at all, which api.describe reports as
		// `concurrent:true`.
		if def.Concurrent || def.Query {
			continue
		}
		if def.Name == "session.shutdown" {
			// Draining must outlast the longest command it waits for. The budget
			// is derived from the finished catalog (see shutdownAdmissionWait),
			// so a `lilt quit` behind a long play drains instead of giving up
			// after the ordinary 5s with server_busy.
			def.Admission = shutdownAdmissionWait(defs)
			continue
		}
		def.Admission = defaultAdmissionWait
	}

	return defs
}

// concurrentCommands are the commands that run outside the serialized mutation
// slot. This is the single declaration: the server's dispatch branches and
// api.describe both read it, so a command cannot be marked concurrent in one
// place and admitted in another.
var concurrentCommands = map[string]bool{
	"api.describe":         true,
	"discovery.search":     true,
	"discovery.trending":   true,
	"album.tracks":         true,
	"playlist.tracks":      true,
	"library.playlists":    true,
	"library.albums":       true,
	"recommendations.list": true,
	"radio.search":         true,
	"radio.options":        true,
	"radio.cache":          true,
}

// queryCommands are the commands with no side effects. session.watch is here
// because a subscription changes nothing; session.shutdown is deliberately not,
// since it stops a specific server process. radio.cache is a pure snapshot read
// of the disposable probe cache; the commands that write that cache
// (radio.search, radio.probe) are deliberately not listed.
var queryCommands = map[string]bool{
	"api.describe":             true,
	"session.status":           true,
	"session.watch":            true,
	"sources.list":             true,
	"authorization.list":       true,
	"authorization.status":     true,
	"authorization.flowStatus": true,
	"discovery.search":         true,
	"discovery.trending":       true,
	"playlist.tracks":          true,
	"album.tracks":             true,
	"library.albums":           true,
	"library.playlists":        true,
	"recent.list":              true,
	"recommendations.list":     true,
	"radio.options":            true,
	"radio.cache":              true,
	"queue.list":               true,
	"state.get":                true,
	"favorites.list":           true,
	"history.list":             true,
	"history.stats":            true,
}

// defaultAdmissionWait is the single server-side budget for waiting on the
// mutation slot. It is declared once here so the server and the CLI catalog
// cannot drift (docs/internals/concurrency.md §5.3); a client's own deadline is
// what shortens the wait, not a second server field.
const defaultAdmissionWait = 5 * time.Second

// shutdownAdmissionWait derives session.shutdown's queue budget from the
// finished catalog: the longest execution budget any other command may hold
// the mutation slot (playback.play / playSongs, 60s), plus one ordinary
// admission wait, plus session.shutdown's own execution budget — the 5s the
// handler itself may spend accepting the shutdown. Deriving it here keeps
// `lilt quit` draining whenever any execution budget grows, instead of
// silently returning server_busy after a stale hand-kept constant.
func shutdownAdmissionWait(defs []*Definition) time.Duration {
	longest := time.Duration(0)
	own := 5 * time.Second
	for _, def := range defs {
		if def.Name == "session.shutdown" {
			own = def.Timeout
			continue
		}
		if def.Timeout > longest {
			longest = def.Timeout
		}
	}
	return longest + defaultAdmissionWait + own
}

func cmd(name, cli string, timeout time.Duration, paramsSchema json.RawMessage, result string, errors ...string) *Definition {
	return &Definition{
		Name:         name,
		CLI:          cli,
		Timeout:      timeout,
		ParamsSchema: paramsSchema,
		ResultSchema: result,
		Errors:       errors,
	}
}
