package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/caiguo/lilt/internal/api"
)

// SessionFeed preserves every watch event needed by an interactive client.
// It decodes only public, generic projections; provider-specific details never
// cross this boundary into the TUI's control flow.
func (c *Client) SessionFeed(ctx context.Context) (api.WatchSnapshot, <-chan api.WatchUpdate, *api.Watcher, error) {
	snapshot, watcher, err := c.Watch(ctx, true)
	if err != nil {
		return api.WatchSnapshot{}, nil, nil, err
	}
	updates := make(chan api.WatchUpdate, 64)
	go func() {
		defer close(updates)
		for event := range watcher.Events {
			update := decodeWatchUpdate(event)
			select {
			case updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	return snapshot, updates, watcher, nil
}

func decodeWatchUpdate(event api.Event) api.WatchUpdate {
	update := api.WatchUpdate{Kind: event.Event, Sequence: event.Sequence}
	var err error
	switch event.Event {
	case "playback.changed":
		var payload struct {
			State api.PlaybackState `json:"state"`
		}
		err = json.Unmarshal(event.Data, &payload)
		update.Playback = &payload.State
	case "state.changed":
		var payload struct {
			State api.AppState `json:"state"`
		}
		err = json.Unmarshal(event.Data, &payload)
		update.State = &payload.State
	case "sources.changed":
		var payload struct {
			Sources []api.SourceDescriptor `json:"sources"`
		}
		err = json.Unmarshal(event.Data, &payload)
		update.Sources = payload.Sources
	case "authorization.changed":
		var payload struct {
			Authorization api.SourceAuthorization `json:"authorization"`
		}
		err = json.Unmarshal(event.Data, &payload)
		update.Authorization = &payload.Authorization
	case "server.warning":
		var payload struct{ Code, Message string }
		err = json.Unmarshal(event.Data, &payload)
		update.WarningCode, update.WarningMessage = payload.Code, payload.Message
	case "engine.restarted":
		var payload struct {
			Source api.SourceID `json:"source"`
		}
		err = json.Unmarshal(event.Data, &payload)
		update.EngineSource = payload.Source
	case "server.shuttingDown":
		// No payload is required.
	default:
		update.Err = fmt.Errorf("unknown watch event %q", event.Event)
		return update
	}
	if err != nil {
		update.Err = fmt.Errorf("decode %s: %w", event.Event, err)
	}
	return update
}
