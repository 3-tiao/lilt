package api

import (
	"encoding/json"
	"time"
)

// catalog returns the authoritative v2 command list. `api.describe` serializes
// exactly this, and the server dispatches through it.
func catalog() []*Definition {
	return []*Definition{
		cmd("api.describe", "lilt api --json", time.Second, nil, "ApiDescription"),
		cmd("sources.list", "lilt sources --json", 5*time.Second, nil, "[SourceDescriptor]"),

		cmd("playback.play", "lilt play <ref> [--name T] [--shuffle] [--repeat MODE] --json", 60*time.Second,
			params(map[string]schemaProp{
				"ref":     {Type: "string"},
				"name":    {Type: "string"},
				"shuffle": {Type: "boolean"},
				"repeat":  {Type: "string", Enum: []string{"off", "all", "one"}},
			}, "ref"), "PlaybackState",
			CodeInvalidReference, CodeSourceUnavailable, CodeSourceMismatch, CodePartialFailure, CodePlaybackError),

		cmd("playback.playSongs", "lilt play-songs <ref,..> [--start N] [--shuffle] [--repeat MODE] --json", 60*time.Second,
			params(map[string]schemaProp{
				"refs":       {Type: "array", Items: "Reference"},
				"startIndex": {Type: "integer"},
				"shuffle":    {Type: "boolean"},
				"repeat":     {Type: "string", Enum: []string{"off", "all", "one"}},
			}, "refs"), "PlaybackState",
			CodeInvalidReference, CodeSourceMismatch, CodePartialFailure, CodePlaybackError),

		cmd("playback.pause", "lilt pause --json", 5*time.Second, nil, "PlaybackState", CodeInvalidState),
		cmd("playback.toggle", "lilt toggle --json", 5*time.Second, nil, "PlaybackState", CodeInvalidState),
		cmd("playback.resume", "lilt resume --json", 5*time.Second, nil, "PlaybackState", CodeInvalidState),
		cmd("playback.next", "lilt next --json", 5*time.Second, nil, "PlaybackState", CodeFiniteQueueRequired),
		cmd("playback.previous", "lilt previous --json", 5*time.Second, nil, "PlaybackState", CodeFiniteQueueRequired),
		cmd("playback.stop", "lilt stop --json", 5*time.Second, nil, "PlaybackState"),
		cmd("playback.setShuffle", "lilt shuffle on|off --json", 5*time.Second,
			params(map[string]schemaProp{"on": {Type: "boolean"}}, "on"), "PlaybackState", CodeFiniteQueueRequired),
		cmd("playback.setRepeat", "lilt repeat off|all|one --json", 5*time.Second,
			params(map[string]schemaProp{"mode": {Type: "string", Enum: []string{"off", "all", "one"}}}, "mode"),
			"PlaybackState", CodeFiniteQueueRequired),

		cmd("queue.list", "lilt queue [list] --json", 5*time.Second, nil, "QueueState"),
		cmd("queue.add", "lilt queue add <ref> --next|--append --json", 30*time.Second,
			params(map[string]schemaProp{
				"ref":             {Type: "string"},
				"position":        {Type: "string", Enum: []string{"next", "append"}},
				"ifQueueRevision": {Type: "integer"},
			}, "ref", "position"), "PlaybackState",
			CodeInvalidReference, CodeSourceMismatch, CodeQueueUnavailable, CodeConflict),
		cmd("queue.jump", "", 5*time.Second,
			params(map[string]schemaProp{"index": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "index"),
			"PlaybackState", CodeQueueUnavailable, CodeConflict),
		cmd("queue.remove", "", 5*time.Second,
			params(map[string]schemaProp{"index": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "index"),
			"PlaybackState", CodeQueueUnavailable, CodeConflict),
		cmd("queue.move", "", 5*time.Second,
			params(map[string]schemaProp{"from": {Type: "integer"}, "to": {Type: "integer"}, "ifQueueRevision": {Type: "integer"}}, "from", "to"),
			"PlaybackState", CodeQueueUnavailable, CodeConflict),
		cmd("queue.clear", "lilt queue clear --json", 5*time.Second,
			params(map[string]schemaProp{"ifQueueRevision": {Type: "integer"}}), "PlaybackState", CodeConflict),

		cmd("discovery.search", "lilt search <term> [--source S] [--type T] [--limit N] --json", 45*time.Second,
			params(map[string]schemaProp{
				"source": {Type: "string"},
				"term":   {Type: "string"},
				"type":   {Type: "string", Enum: []string{"song", "playlist", "station", "all"}},
				"limit":  {Type: "integer"},
			}, "source", "term", "type"), "SearchResult", CodeSearchFailed, CodeSourceUnavailable),
		cmd("playlist.tracks", "lilt playlist <ref> --json", 45*time.Second,
			params(map[string]schemaProp{"ref": {Type: "string"}}, "ref"), "PlaylistTracksResult", CodeSearchFailed, CodeInvalidReference),
		cmd("library.playlists", "lilt library [--source S] --json", 45*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}}, "source"), "[Item]", CodeSearchFailed, CodeSourceUnavailable),
		cmd("recent.list", "lilt recent [N] --json", 5*time.Second,
			params(map[string]schemaProp{"limit": {Type: "integer"}}), "[Item]"),
		cmd("recommendations.list", "", 45*time.Second,
			params(map[string]schemaProp{"source": {Type: "string"}, "limit": {Type: "integer"}}, "source"), "[Item]", CodeSearchFailed),

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
			params(map[string]schemaProp{"source": {Type: "string"}}), "[Item]"),
		cmd("favorites.set", "", 5*time.Second,
			params(map[string]schemaProp{"item": {Ref: "Item"}, "favorited": {Type: "boolean"}}, "item", "favorited"),
			"FavoriteResult", CodeStateSaveFailed),
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
		cmd("session.shutdown", "lilt quit --json", 5*time.Second, nil, "{}"),

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
			CodeAuthorizationFailed, CodeUnsupportedCommand),
	}
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
