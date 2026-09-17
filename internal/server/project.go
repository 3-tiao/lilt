package server

import (
	"net/url"
	"strings"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// sourceFromState classifies a helper state by source shape. It is used only as
// a safety check during a source switch; projection always uses the committed
// activeSource, never this inference.
func sourceFromState(state core.PlaybackState) api.SourceID {
	if state.IsLive || state.Mode == "stream" {
		return api.SourceRadio
	}
	if state.Track != nil && state.Track.Kind == api.KindStream {
		return api.SourceRadio
	}
	return api.SourceAppleMusic
}

// ProjectItem converts a core item into its public shape for a source.
func ProjectItem(item core.Item, source api.SourceID) api.Item {
	kind := item.Kind
	if kind == "" {
		if source == api.SourceRadio {
			kind = api.KindStream
		} else {
			kind = api.KindSong
		}
	}
	providerID := item.ID
	projected := api.Item{
		Source:     source,
		Kind:       kind,
		ProviderID: providerID,
		URL:        item.URL,
		Title:      item.Title,
		Artist:     item.Artist,
		PreviewURL: item.PreviewURL,
	}
	switch source {
	case api.SourceRadio:
		normalized := normalizeStreamURL(item.URL)
		projected.ID = api.RadioRef(normalized)
		projected.Ref = item.URL
		projected.Radio = radioMetadata(item)
	case api.SourceAppleMusic:
		projected.ID = "am:" + providerID
		projected.Ref = api.AppleMusicRef(kind, providerID)
	case api.SourceAudius:
		projected.ID = api.AudiusRef(kind, providerID)
		projected.Ref = projected.ID
	default:
		projected.ID = string(source) + ":" + providerID
		projected.Ref = string(source) + ":" + kind + ":" + providerID
	}
	return projected
}

func radioMetadata(item core.Item) *api.RadioMetadata {
	if item.Radio == nil {
		return nil
	}
	return &api.RadioMetadata{
		Origin:        item.Radio.Origin,
		StationUUID:   item.Radio.StationUUID,
		Tags:          item.Radio.Tags,
		Languages:     item.Radio.Languages,
		Country:       item.Radio.Country,
		CountryCode:   item.Radio.CountryCode,
		Codec:         item.Radio.Codec,
		Bitrate:       item.Radio.Bitrate,
		HLS:           item.Radio.HLS,
		Votes:         item.Radio.Votes,
		ClickCount:    item.Radio.ClickCount,
		ClickTrend:    item.Radio.ClickTrend,
		LastCheckOK:   item.Radio.LastCheckOK,
		LastCheckTime: item.Radio.LastCheckTime,
	}
}

// ProjectStatus converts engine state into the queue-free public projection.
func ProjectStatus(state core.PlaybackState, source api.SourceID, sequence uint64) api.PlaybackStatus {
	status := api.PlaybackStatus{
		Sequence:     sequence,
		Source:       source,
		Position:     state.Position,
		Duration:     state.Duration,
		Status:       state.Status,
		AudioVariant: state.AudioVariant,
		Format:       formatOr(state.Format),
		Available:    state.Available,
		Shuffle:      state.Shuffle,
		Repeat:       repeatOr(state.Repeat),
		IsLive:       state.IsLive,
		Mode:         modeOr(state.Mode),
	}
	if state.Track != nil {
		item := ProjectItem(*state.Track, source)
		status.Track = &item
	}
	if state.Error != "" {
		message := state.Error
		status.PlaybackError = &message
	}
	// streamTitle/streamArtist are attached by the server's ICY overlay
	// (engine_supervisor.go) and stay null when the stream announces nothing.
	return status
}

// ProjectState converts engine state into the full public projection including
// queue context. queueRevision tracks composition changes only.
func ProjectState(state core.PlaybackState, source api.SourceID, sequence, queueRevision uint64) api.PlaybackState {
	status := ProjectStatus(state, source, sequence)
	full := api.PlaybackState{
		PlaybackStatus: status,
		QueueRevision:  queueRevision,
		QueueIndex:     state.QueueIndex,
	}
	if len(state.Queue) > 0 {
		queueSource := source
		full.QueueSource = &queueSource
		full.Queue = make([]api.Item, 0, len(state.Queue))
		for _, item := range state.Queue {
			full.Queue = append(full.Queue, ProjectItem(item, source))
		}
	}
	return full
}

func formatOr(value string) string {
	if strings.TrimSpace(value) == "" {
		return "System-selected"
	}
	return value
}

func repeatOr(value string) string {
	switch value {
	case "off", "all", "one":
		return value
	}
	return "off"
}

func modeOr(value string) string {
	switch value {
	case "none", "preview", "full", "stream":
		return value
	}
	return "none"
}

// normalizeStreamURL produces the stable radio endpoint identity: lowercase
// scheme and host, no default port, no fragment. Query is preserved because it
// can select a distinct stream.
func normalizeStreamURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Host = strings.TrimSuffix(parsed.Host, ":80")
	parsed.Host = strings.TrimSuffix(parsed.Host, ":443")
	parsed.Fragment = ""
	return parsed.String()
}
