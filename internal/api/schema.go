package api

import "encoding/json"

// modelSchemas returns the public model schemas, keyed by model name. Command
// params and results reference them with JSON Pointers such as #/models/Item.
func modelSchemas() map[string]json.RawMessage {
	sourceID := schemaProp{Type: "string", Enum: []string{
		string(SourceAppleMusic), string(SourceAudius), string(SourceRadio),
	}}
	itemRef := schemaProp{Ref: "Item"}
	str := schemaProp{Type: "string"}
	integer := schemaProp{Type: "integer"}
	number := schemaProp{Type: "number"}
	boolean := schemaProp{Type: "boolean"}

	models := map[string]json.RawMessage{
		"Reference": toRaw(map[string]any{
			"type":    "string",
			"pattern": `^[a-z-]+:(song|playlist|station):.+$`,
		}),
		"RadioMetadata": model(map[string]schemaProp{
			"origin":      {Type: "string", Enum: []string{OriginBuiltin, OriginDirectory, OriginUser}},
			"stationUUID": str,
			"tags":        {Type: "array"},
			"languages":   {Type: "array"},
			"country":     str,
			"countryCode": str,
			"codec":       str,
			"bitrate":     integer,
			"hls":         boolean,
			"votes":       integer,
			"clickCount":  integer,
			// Keep in sync with api.RadioMetadata: Browse rows carry the full
			// metadata, so favorites.set rejects any field missing here.
			"clickTrend":    integer,
			"lastCheckOK":   boolean,
			"lastCheckTime": str,
		}, nil),
		"Item": model(map[string]schemaProp{
			"source":     sourceID,
			"kind":       {Type: "string", Enum: []string{KindSong, KindPlaylist, KindStation, KindStream}},
			"id":         str,
			"providerId": str,
			"ref":        str,
			"url":        str,
			"title":      str,
			"artist":     str,
			"previewURL": str,
			"radio":      {Ref: "RadioMetadata"},
		}, []string{"source", "kind", "id", "ref", "title"}),
		"Capability": model(map[string]schemaProp{
			"available":   boolean,
			"reason":      str,
			"description": str,
		}, []string{"available"}),
		"SourceDescriptor": model(map[string]schemaProp{
			"id":           sourceID,
			"label":        str,
			"priority":     integer,
			"available":    boolean,
			"availability": {Type: "string", Enum: []string{AvailabilityReady, AvailabilityAuthorizationRequired, AvailabilitySubscriptionRequired, AvailabilityUnavailable, AvailabilityDegraded}},
			"reason":       str,
			"description":  str,
			"capabilities": {Type: "object"},
		}, []string{"id", "label", "priority", "available", "availability", "capabilities"}),
		"PlaybackStatus": model(map[string]schemaProp{
			"sequence":         integer,
			"source":           sourceID,
			"track":            itemRef,
			"position":         number,
			"duration":         number,
			"status":           {Type: "string", Enum: []string{"stopped", "playing", "paused", "buffering", "error"}},
			"audioVariant":     str,
			"format":           str,
			"availableFormats": {Type: "array"},
			"shuffle":          boolean,
			"repeatMode":       {Type: "string", Enum: []string{"off", "all", "one"}},
			"isLive":           boolean,
			"mode":             {Type: "string", Enum: []string{"none", "preview", "full", "stream"}},
			"playbackError":    str,
			"streamTitle":      str,
			"streamArtist":     str,
		}, []string{"sequence", "source", "position", "duration", "status", "shuffle", "repeatMode", "isLive", "mode"}),
		"PlaybackState": model(map[string]schemaProp{
			"sequence":      integer,
			"source":        sourceID,
			"track":         itemRef,
			"position":      number,
			"duration":      number,
			"status":        str,
			"shuffle":       boolean,
			"repeatMode":    str,
			"isLive":        boolean,
			"mode":          str,
			"queueRevision": integer,
			"queueSource":   sourceID,
			"queue":         {Type: "array", Items: "Item"},
			"queueIndex":    integer,
		}, []string{"sequence", "source", "position", "duration", "status", "queueRevision", "queueIndex"}),
		"QueueState": model(map[string]schemaProp{
			"source":        sourceID,
			"items":         {Type: "array", Items: "Item"},
			"index":         integer,
			"queueRevision": integer,
		}, []string{"items", "index", "queueRevision"}),
		"RecentEntry": model(map[string]schemaProp{
			"item":     itemRef,
			"playedAt": {Type: "string", Format: "date-time"},
		}, []string{"item", "playedAt"}),
		"AppState": model(map[string]schemaProp{
			"revision":   integer,
			"theme":      str,
			"lastSource": sourceID,
			"favorites":  {Type: "array", Items: "Item"},
			"recent":     {Type: "array", Items: "RecentEntry"},
		}, []string{"revision", "theme", "lastSource", "favorites", "recent"}),
		"SourceAuthorization": model(map[string]schemaProp{
			"source":       sourceID,
			"status":       {Type: "string", Enum: []string{AuthNotRequired, AuthNotDetermined, AuthPending, AuthAuthorized, AuthDenied, AuthExpired, AuthError}},
			"accountLabel": str,
			"expiresAt":    {Type: "string", Format: "date-time"},
			"details":      {Type: "object"},
		}, []string{"source", "status"}),
		"Interaction": model(map[string]schemaProp{
			"type":      {Type: "string", Enum: []string{InteractionNone, InteractionSystemDialog, InteractionBrowser, InteractionDeviceCode}},
			"url":       str,
			"userCode":  str,
			"expiresAt": {Type: "string", Format: "date-time"},
		}, []string{"type"}),
		"AuthorizationFlow": model(map[string]schemaProp{
			"flowId":      str,
			"source":      sourceID,
			"status":      {Type: "string", Enum: []string{FlowPending, FlowAuthorized, FlowDenied, FlowExpired, FlowCancelled, FlowError}},
			"interaction": {Ref: "Interaction"},
			"error":       {Type: "object"},
		}, []string{"flowId", "source", "status", "interaction"}),
		"RadioProbeResult": model(map[string]schemaProp{
			"status":    {Type: "string", Enum: []string{"healthy", "failed"}},
			"latencyMs": integer,
			"errorCode": str,
			"message":   str,
		}, []string{"status"}),
		"RadioCache": model(map[string]schemaProp{
			"version":  integer,
			"stations": {Type: "object"},
			"health":   {Type: "object"},
		}, nil),
		"SearchResult": model(map[string]schemaProp{
			"source": sourceID,
			"term":   str,
			"groups": {Type: "object"},
		}, []string{"source", "term", "groups"}),
		"PlaylistTracksResult": model(map[string]schemaProp{
			"playlist": itemRef,
			"items":    {Type: "array", Items: "Item"},
		}, []string{"playlist", "items"}),
		"FavoriteResult": model(map[string]schemaProp{
			"favorited": boolean,
			"item":      itemRef,
		}, []string{"favorited", "item"}),
		"HistoryEntry": model(map[string]schemaProp{
			"item":     itemRef,
			"playedAt": {Type: "string", Format: "date-time"},
		}, []string{"item", "playedAt"}),
		"HistoryPageResult": model(map[string]schemaProp{
			"entries":    {Type: "array", Items: "HistoryEntry"},
			"nextCursor": str,
		}, []string{"entries"}),
		"HistoryStats": model(map[string]schemaProp{
			"ref":           str,
			"playCount":     integer,
			"firstPlayedAt": {Type: "string", Format: "date-time"},
			"lastPlayedAt":  {Type: "string", Format: "date-time"},
		}, []string{"ref", "playCount"}),
		"HistoryClearResult": model(map[string]schemaProp{
			"cleared": integer,
		}, []string{"cleared"}),
		"ActivityResetResult": model(map[string]schemaProp{
			"archived":    boolean,
			"archivePath": str,
		}, []string{"archived"}),
	}
	return models
}

func model(props map[string]schemaProp, required []string) json.RawMessage {
	return buildObject(props, required, true)
}
