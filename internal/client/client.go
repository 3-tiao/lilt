// Package client is the Go client for the lilt Client API v0.1. It implements the
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
		return response, staleServerHint(response.Error)
	}
	return response, nil
}

// staleServerHint explains the common cause of unknown_command: a long-lived
// server started from an older build. The server owns playback and outlives the
// TUI/CLI, so an upgrade needs an explicit restart rather than a retry.
func staleServerHint(err *api.Error) *api.Error {
	if err == nil || err.Code != api.CodeUnknownCommand {
		return err
	}
	clone := *err
	clone.Message = err.Message + " (the running server may be out of date; run `lilt quit` and retry)"
	return &clone
}

func decode[T any](response api.Response, dst *T) error {
	if len(response.Data) == 0 {
		return nil
	}
	return json.Unmarshal(response.Data, dst)
}

// --- Provider ---------------------------------------------------------------

// Sources lists the registered sources and their capability availability.
func (c *Client) Sources(ctx context.Context) ([]api.SourceDescriptor, error) {
	response, err := c.Call(ctx, "sources.list", nil)
	if err != nil {
		return nil, err
	}
	var descriptors []api.SourceDescriptor
	if err := decode(response, &descriptors); err != nil {
		return nil, err
	}
	return descriptors, nil
}

func (c *Client) Search(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.SearchSource(ctx, string(api.SourceAppleMusic), term, "song", limit)
}

func (c *Client) SearchPlaylists(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.SearchSource(ctx, string(api.SourceAppleMusic), term, "playlist", limit)
}

func (c *Client) Stations(ctx context.Context, term string, limit int) ([]core.Item, error) {
	return c.SearchSource(ctx, string(api.SourceAppleMusic), term, "station", limit)
}

// SearchSource searches one declared discovery source and preserves each item's
// canonical source/ref for callers that later play or queue it.
func (c *Client) SearchSource(ctx context.Context, source, term, kind string, limit int) ([]core.Item, error) {
	response, err := c.Call(ctx, "discovery.search", map[string]any{
		"source": source, "term": term, "type": kind, "limit": limit,
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

// TrendingSource returns a source's trending tracks or playlists. It errors with
// the server's stable unsupported_command when the source has no trending.
func (c *Client) TrendingSource(ctx context.Context, source, kind string, limit int) ([]core.Item, error) {
	response, err := c.Call(ctx, "discovery.trending", map[string]any{
		"source": source, "type": kind, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	var result api.SearchResult
	if err := decode(response, &result); err != nil {
		return nil, err
	}
	group := map[string]string{"song": api.GroupSongs, "playlist": api.GroupPlaylists}[kind]
	return toCoreItems(result.Groups[group]), nil
}

func (c *Client) LibraryPlaylists(ctx context.Context) ([]core.Item, error) {
	return c.LibraryPlaylistsSource(ctx, string(api.SourceAppleMusic))
}

// LibraryPlaylistsSource reads one source's account playlists. Apple uses the
// MusicKit helper; Audius requires a linked account.
func (c *Client) LibraryPlaylistsSource(ctx context.Context, source string) ([]core.Item, error) {
	response, err := c.Call(ctx, "library.playlists", map[string]any{"source": source})
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
	return c.PlaylistTracksSource(ctx, string(api.SourceAppleMusic), id)
}

// PlaylistTracksSource loads a playlist using its source-specific canonical ref.
func (c *Client) PlaylistTracksSource(ctx context.Context, source, ref string) ([]core.Item, error) {
	if !strings.Contains(ref, ":") || !strings.HasPrefix(ref, source+":") {
		ref = source + ":" + api.KindPlaylist + ":" + ref
	}
	response, err := c.Call(ctx, "playlist.tracks", map[string]any{"ref": ref})
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
	params := map[string]any{"ref": ref}
	if strings.TrimSpace(request.Name) != "" {
		params["name"] = request.Name
	}
	if request.StartAt != 0 {
		params["startAt"] = request.StartAt
	}
	if strings.TrimSpace(request.StartTrackID) != "" {
		params["startTrackID"] = request.StartTrackID
	}
	if request.Reverse {
		params["reverse"] = true
	}
	if request.FromHere {
		params["fromHere"] = true
	}
	if request.Shuffle != nil {
		params["shuffle"] = *request.Shuffle
	}
	if strings.TrimSpace(request.Repeat) != "" {
		params["repeat"] = request.Repeat
	}
	response, err := c.Call(ctx, "playback.play", params)
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

func (c *Client) Enqueue(ctx context.Context, request core.PlaybackRequest, position string, ifQueueRevision uint64) (core.PlaybackState, error) {
	params := map[string]any{"ref": refFromRequest(request), "position": position}
	if ifQueueRevision > 0 {
		params["ifQueueRevision"] = ifQueueRevision
	}
	response, err := c.Call(ctx, "queue.add", params)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) PlaySongs(ctx context.Context, ids []string, startIndex int) (core.PlaybackState, error) {
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		// Existing Apple callers pass provider IDs. Canonical refs from other
		// sources are already complete and must not be reconstructed as Apple.
		if reference, err := api.ParseReference(id); err == nil && reference.Source != "" {
			refs = append(refs, id)
			continue
		}
		refs = append(refs, api.AppleMusicRef(api.KindSong, id))
	}
	response, err := c.Call(ctx, "playback.playSongs", map[string]any{"refs": refs, "startIndex": startIndex})
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueJump(ctx context.Context, index int, ifQueueRevision uint64) (core.PlaybackState, error) {
	return c.queueIndexOp(ctx, "queue.jump", index, ifQueueRevision)
}

func (c *Client) QueueRemove(ctx context.Context, index int, ifQueueRevision uint64) (core.PlaybackState, error) {
	return c.queueIndexOp(ctx, "queue.remove", index, ifQueueRevision)
}

func (c *Client) queueIndexOp(ctx context.Context, command string, index int, ifQueueRevision uint64) (core.PlaybackState, error) {
	params := map[string]any{"index": index}
	if ifQueueRevision > 0 {
		params["ifQueueRevision"] = ifQueueRevision
	}
	response, err := c.Call(ctx, command, params)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueMove(ctx context.Context, from, to int, ifQueueRevision uint64) (core.PlaybackState, error) {
	params := map[string]any{"from": from, "to": to}
	if ifQueueRevision > 0 {
		params["ifQueueRevision"] = ifQueueRevision
	}
	response, err := c.Call(ctx, "queue.move", params)
	if err != nil {
		return core.PlaybackState{}, err
	}
	return c.decodeState(response)
}

func (c *Client) QueueClear(ctx context.Context, ifQueueRevision uint64) (core.PlaybackState, error) {
	params := map[string]any{}
	if ifQueueRevision > 0 {
		params["ifQueueRevision"] = ifQueueRevision
	}
	response, err := c.Call(ctx, "queue.clear", params)
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
	return c.AuthorizationStatus(ctx, string(api.SourceAppleMusic))
}

// AuthorizationStatus reads one source's authorization for the TUI account
// summary.
func (c *Client) AuthorizationStatus(ctx context.Context, source string) (core.AuthorizationStatus, error) {
	response, err := c.Call(ctx, "authorization.status", map[string]any{"source": source})
	if err != nil {
		return core.AuthorizationStatus{}, err
	}
	var authorization api.SourceAuthorization
	if err := decode(response, &authorization); err != nil {
		return core.AuthorizationStatus{}, err
	}
	return core.AuthorizationStatus{Status: authorization.Status, AccountLabel: authorization.AccountLabel}, nil
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

// refFromRequest builds a canonical ref from a playback request.
func refFromRequest(request core.PlaybackRequest) string {
	if request.Ref != "" {
		return request.Ref
	}
	if request.URL != "" {
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
