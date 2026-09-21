package server

import (
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

// ProjectItem converts a core item into its public shape for a source. The
// identity and the field mapping come from the shared api projection, so the
// server and the client can never name the same item differently.
func ProjectItem(item core.Item, source api.SourceID) api.Item {
	return api.ProjectCoreItem(item, source)
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
	if state.QueueFill != nil {
		status.QueueFill = &api.QueueFill{Queued: state.QueueFill.Queued, Total: state.QueueFill.Total}
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
