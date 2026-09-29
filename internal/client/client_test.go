package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/server"
	"github.com/3-tiao/lilt/internal/state"
)

func startClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-cli-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := server.Start(server.Options{
		SocketPath: socket,
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return New(socket), ctx
}

func TestClientQueueRemoveAndUndoEnvelope(t *testing.T) {
	cli, ctx := startClient(t)
	started, err := cli.PlaySongs(ctx, []string{"a", "b", "c"}, 0, core.PlaybackForm{})
	if err != nil {
		t.Fatal(err)
	}
	removed, undo, err := cli.QueueRemove(ctx, 1, started.QueueRevision)
	if err != nil {
		t.Fatal(err)
	}
	if undo == nil || len(removed.Queue) != 2 || removed.Queue[1].ID != "c" {
		t.Fatalf("removed=%+v undo=%+v", removed, undo)
	}
	restored, err := cli.QueueUndoRemove(ctx, undo.Token, removed.QueueRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Queue) != 3 || restored.Queue[1].ID != "b" {
		t.Fatalf("restored=%+v", restored.Queue)
	}
}

// trendingStubProvider is the hermetic trending source for the client-side
// group flattening of discovery.trending (OQ21: "all" used to map to no group
// and silently returned an empty list).
type trendingStubProvider struct{}

func (trendingStubProvider) Source() api.SourceID { return api.SourceAudius }

func (trendingStubProvider) Descriptor(context.Context) api.SourceDescriptor {
	return api.SourceDescriptor{
		ID:           api.SourceAudius,
		Label:        "Audius",
		Available:    true,
		Capabilities: map[string]api.Capability{api.CapSearchTrending: {Available: true}},
	}
}

func (trendingStubProvider) Search(context.Context, string, string, int) ([]api.Item, *api.Error) {
	return nil, nil
}

func (trendingStubProvider) Trending(_ context.Context, kind string, _ int) ([]api.Item, *api.Error) {
	if kind == api.KindSong {
		return []api.Item{{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:t1", ProviderID: "t1", Ref: "audius:song:t1", Title: "Golden"}}, nil
	}
	return []api.Item{{Source: api.SourceAudius, Kind: api.KindPlaylist, ID: "audius:playlist:p1", ProviderID: "p1", Ref: "audius:playlist:p1", Title: "Golden Hour"}}, nil
}

func TestClientTrendingSourceFlattensDeclaredGroups(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-cli-trending-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := server.Start(server.Options{
		SocketPath: socket,
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		Providers:  []server.ContentProvider{trendingStubProvider{}},
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cli := New(socket)

	songs, err := cli.TrendingSource(ctx, string(api.SourceAudius), api.KindSong, 20)
	if err != nil {
		t.Fatalf("trending songs: %v", err)
	}
	if len(songs) != 1 || songs[0].Title != "Golden" {
		t.Fatalf("trending songs = %+v", songs)
	}
	playlists, err := cli.TrendingSource(ctx, string(api.SourceAudius), api.KindPlaylist, 20)
	if err != nil {
		t.Fatalf("trending playlists: %v", err)
	}
	if len(playlists) != 1 || playlists[0].Title != "Golden Hour" {
		t.Fatalf("trending playlists = %+v", playlists)
	}
	all, err := cli.TrendingSource(ctx, string(api.SourceAudius), "all", 20)
	if err != nil {
		t.Fatalf("trending all: %v", err)
	}
	if len(all) != 2 || all[0].Kind != api.KindSong || all[1].Kind != api.KindPlaylist {
		t.Fatalf("trending all must merge songs then playlists, got %+v", all)
	}
	if _, err := cli.TrendingSource(ctx, string(api.SourceAudius), "bogus", 20); err == nil {
		t.Fatal("an unknown trending type must error instead of returning an empty list")
	}
}

func TestClientRecommendationsSourcePreservesKindsAndRefs(t *testing.T) {
	cli, ctx := startClient(t)
	items, err := cli.RecommendationsSource(ctx, string(api.SourceAppleMusic), 1)
	if err != nil {
		t.Fatalf("RecommendationsSource: %v", err)
	}
	if len(items) != 1 || items[0].Kind != api.KindSong || items[0].Ref != "apple-music:song:fake:rec" {
		t.Fatalf("items = %+v", items)
	}
}

func TestClientPlaybackRoundTrip(t *testing.T) {
	cli, ctx := startClient(t)
	playback, err := cli.PlayState(ctx, core.PlaybackRequest{Kind: "song", ID: "1"})
	if err != nil {
		t.Fatalf("PlayState: %v", err)
	}
	if playback.Status != "playing" || playback.Track == nil {
		t.Fatalf("state = %+v", playback)
	}
	status, err := cli.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if status.QueueIndex != 0 || len(status.Queue) == 0 {
		t.Fatalf("queue = %+v", status.Queue)
	}
}

func TestClientFavoritesAndAppState(t *testing.T) {
	cli, ctx := startClient(t)
	item := toCoreItem(api.Item{
		Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:5",
		ProviderID: "5", Ref: "apple-music:song:5", Title: "Five",
	})
	if item.Album != "" {
		t.Fatalf("empty wire album projected as %q", item.Album)
	}
	withAlbum := toCoreItem(api.Item{
		Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:6",
		ProviderID: "6", Ref: "apple-music:song:6", Title: "Six",
		Artist: "Artist", Album: "After Hours", DurationMs: 201000,
	})
	if withAlbum.Album != "After Hours" || withAlbum.DurationMs != 201000 {
		t.Fatalf("wire catalog metadata dropped: %+v", withAlbum)
	}
	if err := cli.SetFavorite(ctx, "apple-music", item, true); err != nil {
		t.Fatalf("SetFavorite: %v", err)
	}
	appState, err := cli.AppState(ctx)
	if err != nil {
		t.Fatalf("AppState: %v", err)
	}
	if len(appState.Favorites) != 1 || appState.Favorites[0].Ref != "apple-music:song:5" {
		t.Fatalf("favorites = %+v", appState.Favorites)
	}
	if err := cli.SetFavorite(ctx, "apple-music", item, false); err != nil {
		t.Fatalf("SetFavorite(false): %v", err)
	}
	appState, _ = cli.AppState(ctx)
	if len(appState.Favorites) != 0 {
		t.Fatalf("favorites after removal = %+v", appState.Favorites)
	}
}

func TestClientRadioBuiltinSearch(t *testing.T) {
	cli, ctx := startClient(t)
	response, err := cli.Call(ctx, "radio.search", map[string]any{"origin": api.OriginBuiltin, "limit": 5})
	if err != nil {
		t.Fatalf("radio.search: %v", err)
	}
	var result api.RadioSearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) == 0 {
		t.Fatal("no builtin stations returned")
	}
}

func TestToCoreStateConversion(t *testing.T) {
	variant := "AAC 256 kbps"
	message := "boom"
	converted := toCoreState(api.PlaybackState{
		PlaybackStatus: api.PlaybackStatus{
			Status:        "playing",
			AudioVariant:  &variant,
			PlaybackError: &message,
			Format:        "AAC 256 kbps",
		},
		Queue:      []api.Item{{Source: api.SourceAppleMusic, Kind: "song", ID: "am:9", ProviderID: "9", Ref: "apple-music:song:9", Title: "Nine"}},
		QueueIndex: 0,
	})
	if converted.Error != "boom" || converted.Queue[0].ID != "9" {
		t.Fatalf("conversion = %+v", converted)
	}
}

func TestAudiusItemPreservesCanonicalRef(t *testing.T) {
	item := toCoreItem(api.Item{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:track-1", ProviderID: "track-1", Ref: "audius:song:track-1", Title: "Track"})
	if item.Source != "audius" || item.ID != "track-1" || item.Ref != "audius:song:track-1" {
		t.Fatalf("core item = %#v", item)
	}
	if got := refFromRequest(core.PlaybackRequest{Ref: item.Ref, Kind: item.Kind, ID: item.ID}); got != item.Ref {
		t.Fatalf("request ref = %q", got)
	}
	projected := toAPIItem(core.Item{Kind: "song", ID: "audius:song:track-1", Ref: "audius:song:track-1", Title: "Track"}, api.SourceAudius)
	if projected.ProviderID != "track-1" || projected.Ref != "audius:song:track-1" {
		t.Fatalf("projected Audius item = %#v", projected)
	}
}

func TestClientRadioCacheRoundTrip(t *testing.T) {
	cli, ctx := startClient(t)
	cache, err := cli.RadioCache(ctx)
	if err != nil {
		t.Fatalf("RadioCache: %v", err)
	}
	if cache == nil {
		t.Fatal("RadioCache returned nil")
	}
}

func TestSessionFeedReconnectsWithFreshSnapshot(t *testing.T) {
	cli, ctx := startClient(t)
	initial, updates, watcher, err := cli.SessionFeed(ctx)
	if err != nil {
		t.Fatalf("SessionFeed: %v", err)
	}
	if initial.State == nil {
		t.Fatal("initial snapshot has no AppState")
	}
	if err := watcher.Close(); err != nil {
		t.Fatalf("close initial watcher: %v", err)
	}

	seenDisconnected := false
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				t.Fatal("feed closed instead of reconnecting")
			}
			switch update.Kind {
			case api.WatchKindDisconnected:
				seenDisconnected = true
			case api.WatchKindSnapshot:
				if !seenDisconnected || update.Snapshot == nil || update.Snapshot.State == nil {
					t.Fatalf("reconnect update = %#v, disconnected=%v", update, seenDisconnected)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for reconnect snapshot")
		}
	}
}

func TestDecodeWatchUpdateCoversPublicEvents(t *testing.T) {
	cases := []struct {
		name, event, data string
		check             func(t *testing.T, update api.WatchUpdate)
	}{
		{"playback", "playback.changed", `{"state":{"status":"playing"}}`, func(t *testing.T, u api.WatchUpdate) {
			if u.Playback == nil || u.Playback.Status != "playing" {
				t.Fatalf("playback=%#v", u)
			}
		}},
		{"state", "state.changed", `{"state":{"theme":"nord"}}`, func(t *testing.T, u api.WatchUpdate) {
			if u.State == nil || u.State.Theme != "nord" {
				t.Fatalf("state=%#v", u)
			}
		}},
		{"sources", "sources.changed", `{"sources":[{"id":"radio","available":true,"capabilities":{}}]}`, func(t *testing.T, u api.WatchUpdate) {
			if len(u.Sources) != 1 || u.Sources[0].ID != api.SourceRadio {
				t.Fatalf("sources=%#v", u)
			}
		}},
		{"authorization", "authorization.changed", `{"authorization":{"source":"radio","status":"not_required"}}`, func(t *testing.T, u api.WatchUpdate) {
			if u.Authorization == nil || u.Authorization.Status != api.AuthNotRequired {
				t.Fatalf("authorization=%#v", u)
			}
		}},
		{"warning", "server.warning", `{"code":"engine","message":"restarting"}`, func(t *testing.T, u api.WatchUpdate) {
			if u.WarningCode != "engine" || u.WarningMessage != "restarting" {
				t.Fatalf("warning=%#v", u)
			}
		}},
		{"restart", "engine.restarted", `{"source":"apple-music"}`, func(t *testing.T, u api.WatchUpdate) {
			if u.EngineSource != api.SourceAppleMusic {
				t.Fatalf("restart=%#v", u)
			}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			update := decodeWatchUpdate(api.Event{Event: test.event, Sequence: 7, Data: json.RawMessage(test.data)})
			if update.Err != nil || update.Sequence != 7 {
				t.Fatalf("update=%#v", update)
			}
			test.check(t, update)
		})
	}
}
