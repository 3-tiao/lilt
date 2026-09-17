package client

import (
	"context"
	"encoding/json"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// StateFeed opens a playback-focused watch and returns the initial snapshot and
// a stream of converted updates for the TUI. The watcher must be closed by the
// caller.
func (c *Client) StateFeed(ctx context.Context) (*core.PlaybackStateUpdate, <-chan core.PlaybackStateUpdate, *api.Watcher, error) {
	snapshot, watcher, err := c.Watch(ctx, false)
	if err != nil {
		return nil, nil, nil, err
	}
	initial := core.PlaybackStateUpdate{Sequence: snapshot.Sequence, State: toCoreState(snapshot.Playback)}
	updates := make(chan core.PlaybackStateUpdate, 64)
	go func() {
		defer close(updates)
		for event := range watcher.Events {
			if event.Event != "playback.changed" {
				continue
			}
			var payload struct {
				State api.PlaybackState `json:"state"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			update := core.PlaybackStateUpdate{Sequence: event.Sequence, State: toCoreState(payload.State)}
			select {
			case updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &initial, updates, watcher, nil
}
