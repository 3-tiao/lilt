package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/activity"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

// A qualified play lands in history once, stats summarize it, and clearing
// keeps favorites.
func TestHistoryRecordsQualifiedPlaysAndClear(t *testing.T) {
	server, socket := startTestServerWithEngine(t, fakeengine.NewFakeEngine())
	server.recent.minThreshold = 20 * time.Millisecond
	played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if !played.OK {
		t.Fatalf("play failed: %+v", played.Error)
	}
	deadline := time.Now().Add(4 * time.Second)
	var stats []api.HistoryStats
	for time.Now().Before(deadline) {
		response := call(t, socket, "history.stats", map[string]any{"refs": []string{"apple-music:song:1"}})
		if !response.OK {
			t.Fatalf("history.stats failed: %+v", response.Error)
		}
		stats = nil
		if err := json.Unmarshal(response.Data, &stats); err != nil {
			t.Fatal(err)
		}
		if len(stats) == 1 && stats[0].PlayCount > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(stats) != 1 || stats[0].PlayCount == 0 {
		t.Fatalf("stats never recorded: %+v", stats)
	}

	favorite := call(t, socket, "favorites.add", map[string]any{"ref": "apple-music:song:1"})
	if !favorite.OK {
		t.Fatalf("favorites.add failed: %+v", favorite.Error)
	}
	var result api.FavoriteResult
	if err := json.Unmarshal(favorite.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Favorited || result.Item.Title == "" {
		t.Fatalf("favorite result = %+v, want resolved item", result)
	}

	cleared := call(t, socket, "history.clear", map[string]any{"confirm": true})
	if !cleared.OK {
		t.Fatalf("history.clear failed: %+v", cleared.Error)
	}
	page := call(t, socket, "history.list", nil)
	if !page.OK {
		t.Fatalf("history.list failed: %+v", page.Error)
	}
	var history api.HistoryPageResult
	if err := json.Unmarshal(page.Data, &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 0 {
		t.Fatalf("history after clear = %+v", history)
	}
	list := call(t, socket, "favorites.list", nil)
	var favorites []api.Item
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 {
		t.Fatalf("favorites after history.clear = %+v", favorites)
	}
}

// Clearing history and resetting the database wait for an in-flight qualified
// play, then suppress that same playback occurrence in the cleared store.
func TestHistoryClearAndResetSerializeWithQualifiedPlay(t *testing.T) {
	for _, command := range []string{"history.clear", "activity.reset"} {
		t.Run(command, func(t *testing.T) {
			server, socket := startTestServer(t)
			played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
			if !played.OK {
				t.Fatalf("play: %+v", played.Error)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			server.mu.Lock()
			server.recent.minThreshold = time.Second
			server.recent.record = func(ready *recentOccurrence) bool {
				close(started)
				<-release
				return server.recordRecentLocked(ready)
			}
			server.mu.Unlock()
			at := time.Now()
			server.sampleRecentOnce(at)
			sampled := make(chan struct{})
			go func() {
				server.sampleRecentOnce(at.Add(2 * time.Second))
				close(sampled)
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("qualified play did not reach the store")
			}
			cleared := make(chan api.Response, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				response, _ := api.Command(ctx, socket, command, map[string]any{"confirm": true})
				cleared <- response
			}()
			select {
			case <-cleared:
				t.Fatal("clear overtook an in-flight history write")
			case <-time.After(40 * time.Millisecond):
			}
			close(release)
			<-sampled
			select {
			case result := <-cleared:
				if !result.OK {
					t.Fatalf("%s: %+v", command, result.Error)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("clear never completed")
			}
			server.sampleRecentOnce(at.Add(4 * time.Second))
			page := call(t, socket, "history.list", nil)
			if !page.OK {
				t.Fatalf("history.list: %+v", page.Error)
			}
			var history api.HistoryPageResult
			if err := json.Unmarshal(page.Data, &history); err != nil {
				t.Fatal(err)
			}
			if len(history.Entries) != 0 {
				t.Fatalf("history after %s = %+v", command, history.Entries)
			}
		})
	}
}

func TestAutomaticHistoryWriteFailureWarnsWatchClient(t *testing.T) {
	server, socket := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, watcher, err := api.Watch(ctx, socket, []string{"playback"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if !played.OK {
		t.Fatalf("play: %+v", played.Error)
	}
	server.mu.Lock()
	server.recent.minThreshold = time.Second
	if err := server.activity.Close(); err != nil {
		server.mu.Unlock()
		t.Fatal(err)
	}
	server.mu.Unlock()
	at := time.Now()
	server.sampleRecentOnce(at)
	server.sampleRecentOnce(at.Add(2 * time.Second))
	for {
		select {
		case event := <-watcher.Events:
			if event.Event != "server.warning" {
				continue
			}
			var warning api.WatchWarning
			if err := json.Unmarshal(event.Data, &warning); err != nil {
				t.Fatal(err)
			}
			if warning.Code != api.CodeStorageUnavailable {
				t.Fatalf("warning = %+v", warning)
			}
			fresh, err := activity.Open(server.activityPath)
			if err != nil {
				t.Fatal(err)
			}
			page, err := fresh.HistoryPage(activity.HistoryQuery{Limit: 10})
			if err != nil || len(page.Entries) != 0 {
				_ = fresh.Close()
				t.Fatalf("failed write created history: %v, %+v", err, page.Entries)
			}
			server.mu.Lock()
			server.activity = fresh
			server.mu.Unlock()
			server.sampleRecentOnce(at.Add(4 * time.Second))
			server.sampleRecentOnce(at.Add(5 * time.Second))
			page, err = fresh.HistoryPage(activity.HistoryQuery{Limit: 10})
			if err != nil || len(page.Entries) != 1 {
				t.Fatalf("recovered write = %+v, error %v; want one play", page.Entries, err)
			}
			return
		case <-ctx.Done():
			t.Fatal("automatic history write failed without a watch warning")
		}
	}
}

// favorites.add resolves the ref through the provider when the store does not
// know the item; favorites.remove is idempotent and never touches a provider.
func TestFavoritesAddResolvesAndRemoveIsIdempotent(t *testing.T) {
	server, socket := startTestServerWithEngine(t, fakeengine.NewFakeEngine())
	server.recent.minThreshold = 20 * time.Millisecond

	removed := call(t, socket, "favorites.remove", map[string]any{"ref": "apple-music:song:does-not-exist"})
	if !removed.OK {
		t.Fatalf("removing an unknown favorite must succeed: %+v", removed.Error)
	}
	added := call(t, socket, "favorites.add", map[string]any{"ref": "apple-music:song:1440845629"})
	if !added.OK {
		t.Fatalf("favorites.add failed: %+v", added.Error)
	}
	var result api.FavoriteResult
	if err := json.Unmarshal(added.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Item.Title != "Fake song 1440845629" {
		t.Fatalf("add did not resolve metadata: %+v", result.Item)
	}
	list := call(t, socket, "favorites.list", map[string]any{"source": "apple-music"})
	var favorites []api.Item
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 {
		t.Fatalf("favorites = %+v", favorites)
	}
}

// A store that cannot open degrades the server: playback keeps working while
// every Activity read and mutation reports the stable storage error.
func TestDegradedActivityStoreKeepsPlaybackAlive(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-degraded-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// A directory where the database file belongs blocks SQLite from creating
	// its schema files.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s.sock")
	server, startErr := Start(Options{
		SocketPath:   socket,
		Engine:       fakeengine.NewFakeEngine(),
		Store:        state.New(filepath.Join(dir, "state.json")),
		ActivityPath: filepath.Join(blocker, "activity.sqlite3"),
		AudiusClient: startFakeAudius(t),
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })

	played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if !played.OK {
		t.Fatalf("playback broken by degraded store: %+v", played.Error)
	}
	set := call(t, socket, "favorites.set", map[string]any{
		"item":      api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:1", Ref: "apple-music:song:1", Title: "Song"},
		"favorited": true,
	})
	if set.Error == nil || set.Error.Code != api.CodeStorageUnavailable {
		t.Fatalf("favorites.set error = %+v, want storage_unavailable", set.Error)
	}
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	response, watcher, watchErr := api.Watch(watchCtx, socket, nil, true)
	if watchErr != nil {
		t.Fatalf("watch: %v", watchErr)
	}
	defer func() {
		cancelWatch()
		_ = watcher.Close()
	}()
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(response.Data, &snapshot); err != nil {
		t.Fatalf("decode watch snapshot: %v", err)
	}
	if snapshot.Warning == nil || snapshot.Warning.Code != api.CodeStorageUnavailable {
		t.Fatalf("watch warning = %+v, want storage_unavailable", snapshot.Warning)
	}

	for _, request := range []struct {
		command string
		params  any
	}{
		{command: "favorites.list"},
		{command: "recent.list"},
		{command: "history.list"},
		{command: "history.stats", params: map[string]any{"refs": []string{"apple-music:song:1"}}},
	} {
		response := call(t, socket, request.command, request.params)
		if response.Error == nil || response.Error.Code != api.CodeStorageUnavailable {
			t.Fatalf("%s error = %+v, want storage_unavailable", request.command, response.Error)
		}
	}
}

// data reset archives a corrupt database (never deleting it) and returns the
// store to service.
func TestActivityResetArchivesAndRecreates(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-reset-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	activityPath := filepath.Join(dir, "activity.sqlite3")
	if err := os.WriteFile(activityPath, []byte("definitely not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	server, startErr := Start(Options{
		SocketPath:   socket,
		Engine:       fakeengine.NewFakeEngine(),
		Store:        state.New(filepath.Join(dir, "state.json")),
		ActivityPath: activityPath,
		AudiusClient: startFakeAudius(t),
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })

	// The garbage file is not valid SQLite: the server must be degraded, not
	// crashed, and must not have replaced the file.
	if server.activity != nil {
		t.Fatal("corrupt activity database unexpectedly opened")
	}
	if _, statErr := os.Stat(activityPath); statErr != nil {
		t.Fatalf("degraded open replaced the database file: %v", statErr)
	}
	set := call(t, socket, "favorites.set", map[string]any{
		"item":      api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:1", Ref: "apple-music:song:1", Title: "Song"},
		"favorited": true,
	})
	if set.Error == nil || set.Error.Code != api.CodeStorageUnavailable {
		t.Fatalf("degraded favorites.set = %+v", set.Error)
	}

	reset := call(t, socket, "activity.reset", map[string]any{"confirm": true})
	if !reset.OK {
		t.Fatalf("activity.reset failed: %+v", reset.Error)
	}
	var result api.ActivityResetResult
	if err := json.Unmarshal(reset.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Archived || !strings.Contains(result.ArchivePath, "archive-") {
		t.Fatalf("reset result = %+v", result)
	}
	if _, statErr := os.Stat(result.ArchivePath); statErr != nil {
		t.Fatalf("archive missing: %v", statErr)
	}
	// The store works again, and history survives... it starts empty.
	call(t, socket, "favorites.add", map[string]any{"ref": "apple-music:song:1"})
	list := call(t, socket, "favorites.list", nil)
	var favorites []api.Item
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 {
		t.Fatalf("favorites after reset = %+v", favorites)
	}
}

// Client spellings of the same item must collapse onto one canonical row.
func TestFavoritesSetCanonicalizesStableIdentity(t *testing.T) {
	_, socket := startTestServer(t)
	// TUI form: client builds ID "apple-music:1721843001".
	tuiForm := api.Item{
		Source: api.SourceAppleMusic, Kind: api.KindSong,
		ID: "apple-music:1721843001", ProviderID: "1721843001",
		Ref: "apple-music:song:1721843001", Title: "Aruarian Dance", Artist: "Nujabes",
	}
	if r := call(t, socket, "favorites.set", map[string]any{"item": tuiForm, "favorited": true}); !r.OK {
		t.Fatalf("favorites.set (TUI form) failed: %+v", r.Error)
	}
	// CLI resolve form: canonical am:-prefixed identity for the same song.
	cliForm := api.Item{
		Source: api.SourceAppleMusic, Kind: api.KindSong,
		ID: "am:1721843001", ProviderID: "1721843001",
		Ref: "apple-music:song:1721843001", Title: "Aruarian Dance", Artist: "Nujabes",
	}
	if r := call(t, socket, "favorites.set", map[string]any{"item": cliForm, "favorited": true}); !r.OK {
		t.Fatalf("favorites.set (canonical form) failed: %+v", r.Error)
	}
	list := call(t, socket, "favorites.list", nil)
	var favorites []api.Item
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 {
		t.Fatalf("favorite spellings created %d rows: %+v", len(favorites), favorites)
	}
	if favorites[0].ID != "am:1721843001" {
		t.Fatalf("stable id = %q, want am:1721843001", favorites[0].ID)
	}
	// Audius client spelling collapses to the kind-carrying identity.
	audius := api.Item{
		Source: api.SourceAudius, Kind: api.KindSong,
		ID: "audius:track-1", ProviderID: "track-1",
		Ref: "audius:song:track-1", Title: "Track", Artist: "Artist",
	}
	if r := call(t, socket, "favorites.set", map[string]any{"item": audius, "favorited": true}); !r.OK {
		t.Fatalf("favorites.set (audius) failed: %+v", r.Error)
	}
	list = call(t, socket, "favorites.list", map[string]any{"source": "audius"})
	if err := json.Unmarshal(list.Data, &favorites); err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 || favorites[0].ID != "audius:song:track-1" {
		t.Fatalf("audius favorite = %+v, want audius:song:track-1", favorites)
	}
}

// Short-lived URLs and authorization material must never reach the database or
// a public response: the store keeps only stable public URLs.
func TestActivityStoreNeverPersistsSignedURLsOrTokens(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-secrets-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	activityPath := filepath.Join(dir, "activity.sqlite3")
	server, startErr := Start(Options{
		SocketPath:   socket,
		Engine:       fakeengine.NewFakeEngine(),
		Store:        state.New(filepath.Join(dir, "state.json")),
		ActivityPath: activityPath,
		AudiusClient: startFakeAudius(t),
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })

	signed := api.Item{
		Source: api.SourceAudius, Kind: api.KindSong,
		ID: "audius:song:t1", ProviderID: "t1", Ref: "audius:song:t1",
		Title: "Signed", Artist: "Artist",
		URL: "https://api.audius.co/v1/tracks/t1/stream?signature=SECRETMARKER&expires=1700000000",
	}
	set := call(t, socket, "favorites.set", map[string]any{"item": signed, "favorited": true})
	if !set.OK {
		t.Fatalf("favorites.set failed: %+v", set.Error)
	}
	if strings.Contains(string(set.Data), "SECRETMARKER") {
		t.Fatalf("public response leaked a signed URL: %s", set.Data)
	}
	list := call(t, socket, "favorites.list", nil)
	if strings.Contains(string(list.Data), "SECRETMARKER") {
		t.Fatalf("favorites.list leaked a signed URL: %s", list.Data)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	contents, err := os.ReadFile(activityPath)
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	for _, forbidden := range []string{
		"SECRETMARKER", "signature=", "expires=", "Bearer ", "Music-User-Token", "access_token",
	} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("database contains %q", forbidden)
		}
	}
}
