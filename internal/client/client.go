// Package client is the Go client for the lilt Client API v2. It implements the
// interfaces the TUI consumes (content provider, player, radio provider) and
// the Remote used for server-owned state mutations, so no client holds the
// helper, state store, or radio directory directly.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// Client talks to one lilt server over its Unix socket.
type Client struct {
	Path string
}

func New(path string) *Client { return &Client{Path: path} }

// Call sends a raw command and decodes the response, returning a stable error
// when the server reports one.
func (c *Client) Call(ctx context.Context, command string, params any) (api.Response, error) {
	response, err := api.Command(ctx, c.Path, command, params)
	if err != nil {
		return api.Response{}, err
	}
	if !response.OK {
		return response, response.Error
	}
	return response, nil
}

func decode[T any](response api.Response, dst *T) error {
	if len(response.Data) == 0 {
		return nil
	}
	return json.Unmarshal(response.Data, dst)
}

// --- Provider ---------------------------------------------------------------

func (c *Client) Search(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.search(ctx, term, "song", limit)
}

func (c *Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.search(ctx, term, "playlist", limit)
}

func (c *Client) Stations(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.search(ctx, term, "station", limit)
}

func (c *Client) search(ctx context.Context, term, kind string, limit int) ([]core.Item, error) {
	response, err := c.Call(ctx, "discovery.search", map[string]any{
		"source": string(api.SourceAppleMusic), "term": term, "type": kind, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	var result api.SearchResult
	if err := decode(response, &result); err != nil {
		return nil, err
	}
	group := map[string]string{"song": api.GroupSongs, "playlist": api.GroupPlaylists, "station": api.GroupStations}[kind]
	return toCoreItems(result.Groups[group]), nil
}

func (c *Client) LibraryPlaylists(ctx context.Context) ([]core.Item, error) {
	response, err := c.Call(ctx, "library.playlists", map[string]any{"source": string(api.SourceAppleMusic)})
	if err != nil {
		return nil, err
	}
	var items []api.Item
	if err := decode(response, &items); err != nil {
		return nil, err
	}
	return toCoreItems(items), nil
}

func (c *Client) PlaylistTracks(ctx context.Context, id string) ([]core.Item, error) {
	response, err := c.Call(ctx, "playlist.tracks", map[string]any{"ref": api.AppleMusicRef(api.KindPlaylist, id)})
	if err != nil {
		return nil, err
	}
	var result api.PlaylistTracksResult
	if err := decode(response, &result); err != nil {
		return nil, err
	}
	return toCoreItems(result.Items), nil
}

func (c *Client) RecentPlayed(ctx context.Context, limit int) ([]core.Item, error) {
	response, err := c.Call(ctx, "recent.list", map[string]any{"limit": limit})
	if err != nil {
		return nil, err
	}
	var items []api.Item
	if err := decode(response, &items); err != nil {
		return nil, err
	}
	return toCoreItems(items), nil
}

// --- Player -----------------------------------------------------------------

func (c *Client) Play(ctx context.Context, request core.PlaybackRequest) error {
	_, err := c.PlayState(ctx, request)
	return err
}

func (c *Client) Pause(ctx context.Context) error    { _, err := c.PauseState(ctx); return err }
func (c *Client) Resume(ctx context.Context) error   { _, err := c.ResumeState(ctx); return err }
func (c *Client) Next(ctx context.Context) error     { _, err := c.NextState(ctx); return err }
func (c *Client) Previous(ctx context.Context) error { _, err := c.PreviousState(ctx); return err }

func (c *Client) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	ref := refFromRequest(request)
	response, err := c.Call(ctx, "playback.play", map[string]any{"ref": ref})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) PauseState(ctx context.Context) (core.PlaybackState, error) {
	return c.simpleControl(ctx, "playback.pause")
}

func (c *Client) ResumeState(ctx context.Context) (core.PlaybackState, error) {
	return c.simpleControl(ctx, "playback.resume")
}

func (c *Client) NextState(ctx context.Context) (core.PlaybackState, error) {
	return c.simpleControl(ctx, "playback.next")
}

func (c *Client) PreviousState(ctx context.Context) (core.PlaybackState, error) {
	return c.simpleControl(ctx, "playback.previous")
}

func (c *Client) simpleControl(ctx context.Context, command string) (core.PlaybackState, error) {
	response, err := c.Call(ctx, command, nil)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) State(ctx context.Context) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "session.status", map[string]any{"includeQueue": true})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) SetShuffle(ctx context.Context, on bool) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "playback.setShuffle", map[string]any{"on": on})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) SetRepeat(ctx context.Context, mode string) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "playback.setRepeat", map[string]any{"mode": mode})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) Stop(ctx context.Context) (core.PlaybackState, error) {
	return c.simpleControl(ctx, "playback.stop")
}

func (c *Client) Enqueue(ctx context.Context, request core.PlaybackRequest, position string) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "queue.add", map[string]any{"ref": refFromRequest(request), "position": position})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) PlaySongs(ctx context.Context, ids []string, startIndex int) (core.PlaybackState, error) {
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, api.AppleMusicRef(api.KindSong, id))
	}
	response, err := c.Call(ctx, "playback.playSongs", map[string]any{"refs": refs, "startIndex": startIndex})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueJump(ctx context.Context, index int) (core.PlaybackState, error) {
	return c.queueIndexOp(ctx, "queue.jump", index)
}

func (c *Client) QueueRemove(ctx context.Context, index int) (core.PlaybackState, error) {
	return c.queueIndexOp(ctx, "queue.remove", index)
}

func (c *Client) queueIndexOp(ctx context.Context, command string, index int) (core.PlaybackState, error) {
	response, err := c.Call(ctx, command, map[string]any{"index": index})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueMove(ctx context.Context, from, to int) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "queue.move", map[string]any{"from": from, "to": to})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueClear(ctx context.Context) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "queue.clear", nil)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) RadioPlay(ctx context.Context, url, name string) (core.PlaybackState, error) {
	response, err := c.Call(ctx, "playback.play", map[string]any{"ref": url, "name": name})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) RadioStop(ctx context.Context) (core.PlaybackState, error) {
	return c.Stop(ctx)
}

func (c *Client) Probe(ctx context.Context, url string, _ int) (core.RadioProbeResult, error) {
	response, err := c.Call(ctx, "radio.probe", map[string]any{"url": url})
	if err != nil {
		return core.RadioProbeResult{}, err
	}
	var result api.RadioProbeResult
	if err := decode(response, &result); err != nil {
		return core.RadioProbeResult{}, err
	}
	return core.RadioProbeResult{Status: result.Status, LatencyMs: result.LatencyMs, ErrorCode: result.ErrorCode, Message: result.Message}, nil
}

// Authorization satisfies core.Authorizer for the TUI's startup summary.
func (c *Client) Authorization(ctx context.Context) (core.AuthorizationStatus, error) {
	response, err := c.Call(ctx, "authorization.status", map[string]any{"source": string(api.SourceAppleMusic)})
	if err != nil {
		return core.AuthorizationStatus{}, err
	}
	var authorization api.SourceAuthorization
	if err := decode(response, &authorization); err != nil {
		return core.AuthorizationStatus{}, err
	}
	return core.AuthorizationStatus{Status: authorization.Status}, nil
}

// --- Remote state -----------------------------------------------------------

func (c *Client) SetFavorite(ctx context.Context, source string, item core.Item, favorited bool) error {
	_, err := c.Call(ctx, "favorites.set", map[string]any{
		"item":      toAPIItem(item, api.SourceID(source)),
		"favorited": favorited,
	})
	return err
}

func (c *Client) SetTheme(ctx context.Context, name string) error {
	_, err := c.Call(ctx, "ui.set", map[string]any{"theme": name})
	return err
}

func (c *Client) SetLastSource(ctx context.Context, source string) error {
	_, err := c.Call(ctx, "ui.set", map[string]any{"lastSource": source})
	return err
}

// AppState fetches the authoritative persisted state.
func (c *Client) AppState(ctx context.Context) (api.AppState, error) {
	response, err := c.Call(ctx, "state.get", nil)
	if err != nil {
		return api.AppState{}, err
	}
	var appState api.AppState
	if err := decode(response, &appState); err != nil {
		return api.AppState{}, err
	}
	return appState, nil
}

// Watch opens a session.watch stream.
func (c *Client) Watch(ctx context.Context, includeState bool) (api.WatchSnapshot, *api.Watcher, error) {
	response, watcher, err := api.Watch(ctx, c.Path, nil, includeState)
	if err != nil {
		return api.WatchSnapshot{}, nil, err
	}
	if !response.OK {
		return api.WatchSnapshot{}, nil, response.Error
	}
	var snapshot api.WatchSnapshot
	if err := decode(response, &snapshot); err != nil {
		_ = watcher.Close()
		return api.WatchSnapshot{}, nil, err
	}
	return snapshot, watcher, nil
}

func (c *Client) decodeState(response api.Response) (core.PlaybackState, error) {
	var state api.PlaybackState
	if err := decode(response, &state); err != nil {
		return core.PlaybackState{}, err
	}
	return toCoreState(state), nil
}

// refFromRequest builds a canonical ref from a legacy playback request.
func refFromRequest(request core.PlaybackRequest) string {
	if request.URL != "" {
		if reference, err := api.ParseReference(request.URL); err == nil {
			if reference.Source == api.SourceAppleMusic || strings.HasPrefix(request.URL, "http") {
				return request.URL
			}
		}
		return request.URL
	}
	if request.Kind == "" {
		return request.ID
	}
	return api.AppleMusicRef(request.Kind, request.ID)
}

// ServerResponds reports whether a server answers on the socket.
func (c *Client) ServerResponds(ctx context.Context) bool {
	response, err := c.Call(ctx, "session.status", nil)
	return err == nil && response.OK
}

var _ = errors.New
var _ = fmt.Sprintf
var _ = time.Second
