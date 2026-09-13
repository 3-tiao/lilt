package player

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
)

func TestLivePlayback(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	items, err := client.Search(ctx, "Aruarian Dance Nujabes", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no search results")
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "song", ID: items[0].ID, URL: items[0].URL}); err != nil {
		t.Fatalf("play: %v", err)
	}
	time.Sleep(4 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstPosition := state.Position
	time.Sleep(3 * time.Second)
	state, err = client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s status=%s position=%.1f→%.1f variant=%v format=%q available=%v track=%v", state.Mode, state.Status, firstPosition, state.Position, state.AudioVariant, state.Format, state.Available, state.Track)
	if state.Mode != "full" {
		t.Errorf("expected full mode, got %q", state.Mode)
	}
	if state.Position <= firstPosition {
		t.Errorf("position did not advance: %.1f → %.1f", firstPosition, state.Position)
	}
}

func TestLivePresetContent(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	playlists, err := client.SearchPlaylists(ctx, "jazz classics", 3)
	if err != nil {
		t.Fatalf("searchPlaylists: %v", err)
	}
	stations, err := client.Stations(ctx, "lofi", 3)
	if err != nil {
		t.Fatalf("stations: %v", err)
	}
	t.Logf("playlists=%d stations=%d", len(playlists), len(stations))
	if len(playlists) == 0 {
		t.Error("expected at least one playlist")
	}
}

func TestLiveStationPlayback(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	stations, err := client.Stations(ctx, "lofi", 5)
	if err != nil {
		t.Fatalf("stations: %v", err)
	}
	if len(stations) == 0 {
		t.Skip("no stations returned")
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "station", ID: stations[0].ID, URL: stations[0].URL}); err != nil {
		t.Fatalf("play station: %v", err)
	}
	time.Sleep(12 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("station mode=%s status=%s track=%v", state.Mode, state.Status, state.Track)
	if state.Mode != "full" {
		t.Errorf("expected full mode, got %q", state.Mode)
	}
}

func TestLiveRecommendations(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	items, err := client.Recommendations(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "MusicTokenRequestError") {
			t.Skipf("personal recommendations need the cloud user token: %v", err)
		}
		t.Fatalf("recommendations: %v", err)
	}
	for i, item := range items {
		if i >= 5 {
			break
		}
		t.Logf("rec[%d] kind=%s title=%q artist=%q", i, item.Kind, item.Title, item.Artist)
	}
	t.Logf("total recommendations = %d", len(items))
}

func TestLivePlaylistTracks(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil || len(playlists) == 0 {
		t.Fatalf("libraryPlaylists: %v (%d)", err, len(playlists))
	}
	tracks, err := client.PlaylistTracks(ctx, playlists[0].ID)
	if err != nil {
		t.Fatalf("playlistTracks(%s): %v", playlists[0].ID, err)
	}
	t.Logf("playlist=%q tracks=%d first=%v", playlists[0].Title, len(tracks), tracks[0])
	if len(tracks) == 0 {
		t.Error("expected playlist tracks")
	}
}

func TestLiveRadioPlayback(t *testing.T) {
	if os.Getenv("LILT_LIVE_RADIO") != "1" {
		t.Skip("set LILT_LIVE_RADIO=1 to run against a live radio stream")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	url := os.Getenv("LILT_RADIO_URL")
	if url == "" {
		url = "https://radio.cliamp.stream/lofi/stream"
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := client.RadioPlay(ctx, url, "lofi"); err != nil {
		t.Fatalf("radioPlay: %v", err)
	}
	time.Sleep(4 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s status=%s live=%v track=%v", state.Mode, state.Status, state.IsLive, state.Track)
	if state.Mode != "stream" || !state.IsLive {
		t.Errorf("expected live stream, got mode=%q live=%v", state.Mode, state.IsLive)
	}
	if state.Status != "playing" && state.Status != "buffering" {
		t.Errorf("unexpected stream status %q", state.Status)
	}
	items, err := client.Search(ctx, "Nujabes", 3)
	if err != nil || len(items) == 0 {
		t.Fatalf("search: %v (%d)", err, len(items))
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "song", ID: items[0].ID, URL: items[0].URL}); err != nil {
		t.Fatalf("play after radio: %v", err)
	}
	time.Sleep(2 * time.Second)
	after, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode == "stream" || after.IsLive {
		t.Errorf("radio did not stop when Apple Music started: mode=%q live=%v", after.Mode, after.IsLive)
	}
	if _, err := client.RadioStop(ctx); err != nil {
		t.Fatalf("radioStop: %v", err)
	}
}

func TestLiveRecentPlayedTwice(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	first, err := client.RecentPlayed(ctx, 25)
	if err != nil {
		t.Fatalf("recent 1: %v", err)
	}
	firstDur := time.Since(start)
	start = time.Now()
	second, err := client.RecentPlayed(ctx, 25)
	if err != nil {
		t.Fatalf("recent 2: %v", err)
	}
	secondDur := time.Since(start)
	t.Logf("recent first=%v second=%v items=%d/%d", firstDur.Round(time.Millisecond), secondDur.Round(time.Millisecond), len(first), len(second))
	if secondDur > firstDur {
		t.Errorf("second recent call was not faster: %v -> %v", firstDur, secondDur)
	}
}

func TestLiveQueueEditing(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil || len(playlists) == 0 {
		t.Fatalf("libraryPlaylists: %v (%d)", err, len(playlists))
	}
	tracks, err := client.PlaylistTracks(ctx, playlists[0].ID)
	if err != nil || len(tracks) < 4 {
		t.Fatalf("playlistTracks: %v (%d)", err, len(tracks))
	}
	startTrack := tracks[2]
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "playlist", ID: playlists[0].ID, StartTrackID: startTrack.ID, StartTitle: startTrack.Title}); err != nil {
		t.Fatalf("play from track: %v", err)
	}
	time.Sleep(4 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("start-at: queue=%d index=%d current=%q want=%q", len(state.Queue), state.QueueIndex, trackTitle(state), startTrack.Title)
	if state.Mode != "full" || len(state.Queue) == 0 {
		t.Fatalf("queue not started: mode=%q queue=%d", state.Mode, len(state.Queue))
	}
	jumped, err := client.QueueJump(ctx, 0)
	if err != nil {
		t.Fatalf("queueJump: %v", err)
	}
	time.Sleep(1 * time.Second)
	if jumped.QueueIndex != 0 {
		t.Errorf("after jump index=%d want 0", jumped.QueueIndex)
	}
	before := len(jumped.Queue)
	removed, err := client.QueueRemove(ctx, 1)
	if err != nil {
		t.Fatalf("queueRemove: %v", err)
	}
	if len(removed.Queue) != before-1 {
		t.Errorf("after remove queue=%d want %d", len(removed.Queue), before-1)
	}
	moved, err := client.QueueMove(ctx, 0, 2)
	if err != nil {
		t.Fatalf("queueMove: %v", err)
	}
	if len(moved.Queue) != before-1 {
		t.Errorf("move changed length: %d", len(moved.Queue))
	}
	cleared, err := client.QueueClear(ctx)
	if err != nil {
		t.Fatalf("queueClear: %v", err)
	}
	if len(cleared.Queue) != 0 {
		t.Errorf("after clear queue=%d want 0", len(cleared.Queue))
	}
}

func trackTitle(state core.PlaybackState) string {
	if state.Track == nil {
		return ""
	}
	return state.Track.Title
}

func TestLivePlaySongs(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	items, err := client.Search(ctx, "Nujabes", 4)
	if err != nil || len(items) < 3 {
		t.Fatalf("search: %v (%d)", err, len(items))
	}
	ids := []string{items[0].ID, items[1].ID, items[2].ID}
	if _, err := client.PlaySongs(ctx, ids, 1); err != nil {
		t.Fatalf("playSongs: %v", err)
	}
	time.Sleep(2 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("playSongs: mode=%s queue=%d index=%d", state.Mode, len(state.Queue), state.QueueIndex)
	if state.Mode != "full" || len(state.Queue) < 2 {
		t.Errorf("playSongs queue=%d mode=%q", len(state.Queue), state.Mode)
	}
}

func TestLivePlaylistQueue(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil {
		t.Fatalf("libraryPlaylists: %v", err)
	}
	if len(playlists) == 0 {
		t.Skip("no library playlists")
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "playlist", ID: playlists[0].ID, URL: playlists[0].URL}); err != nil {
		t.Fatalf("play playlist: %v", err)
	}
	time.Sleep(12 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("playlist=%q mode=%s status=%s queue=%d index=%d current=%v next=%v", playlists[0].Title, state.Mode, state.Status, len(state.Queue), state.QueueIndex, state.Track, nextTrack(state))
	if len(state.Queue) == 0 {
		t.Error("expected a populated queue")
	}
	if state.QueueIndex < 0 || state.QueueIndex >= len(state.Queue) {
		t.Errorf("queueIndex %d out of range for %d entries", state.QueueIndex, len(state.Queue))
	}
}

func nextTrack(state core.PlaybackState) *core.Item {
	if state.QueueIndex+1 >= len(state.Queue) {
		return nil
	}
	return &state.Queue[state.QueueIndex+1]
}

func TestLiveNextPrevious(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil || len(playlists) == 0 {
		t.Fatalf("libraryPlaylists: %v (%d)", err, len(playlists))
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "playlist", ID: playlists[0].ID, URL: playlists[0].URL}); err != nil {
		t.Fatalf("play: %v", err)
	}
	time.Sleep(4 * time.Second)
	first, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Next(ctx); err != nil {
		t.Fatalf("next: %v", err)
	}
	time.Sleep(2 * time.Second)
	second, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Previous(ctx); err != nil {
		t.Fatalf("previous: %v", err)
	}
	time.Sleep(2 * time.Second)
	third, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first index=%d %v", first.QueueIndex, first.Track)
	t.Logf("after next index=%d %v", second.QueueIndex, second.Track)
	t.Logf("after previous index=%d %v", third.QueueIndex, third.Track)
	if first.QueueIndex == second.QueueIndex {
		t.Errorf("next did not advance queue index (%d)", first.QueueIndex)
	}
}

func TestLivePlaybackModesAndQueue(t *testing.T) {
	if os.Getenv("LILT_LIVE_PLAYBACK") != "1" {
		t.Skip("set LILT_LIVE_PLAYBACK=1 to run against the signed helper")
	}
	path := os.Getenv("LILT_PLAYER_PATH")
	if path == "" {
		t.Fatal("LILT_PLAYER_PATH is required")
	}
	client, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	playlists, err := client.LibraryPlaylists(ctx)
	if err != nil || len(playlists) == 0 {
		t.Fatalf("libraryPlaylists: %v (%d)", err, len(playlists))
	}
	if err := client.Play(ctx, core.PlaybackRequest{Kind: "playlist", ID: playlists[0].ID, URL: playlists[0].URL}); err != nil {
		t.Fatalf("play: %v", err)
	}
	time.Sleep(4 * time.Second)
	state, err := client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shuffled, err := client.SetShuffle(ctx, true)
	if err != nil || !shuffled.Shuffle {
		t.Fatalf("setShuffle: %v %#v", err, shuffled.Shuffle)
	}
	repeated, err := client.SetRepeat(ctx, "all")
	if err != nil || repeated.Repeat != "all" {
		t.Fatalf("setRepeat all: %v %q", err, repeated.Repeat)
	}
	one, err := client.SetRepeat(ctx, "one")
	if err != nil || one.Repeat != "one" {
		t.Fatalf("setRepeat one: %v %q", err, one.Repeat)
	}
	if _, err := client.SetRepeat(ctx, "off"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetShuffle(ctx, false); err != nil {
		t.Fatal(err)
	}
	items, err := client.Search(ctx, "Nujabes", 5)
	if err != nil || len(items) == 0 {
		t.Fatalf("search: %v (%d)", err, len(items))
	}
	enqueued, err := client.Enqueue(ctx, core.PlaybackRequest{Kind: "song", ID: items[0].ID, URL: items[0].URL}, "next")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Logf("queue before=%d after=%d shuffled=%v repeat=%q", len(state.Queue), len(enqueued.Queue), shuffled.Shuffle, one.Repeat)
	if len(enqueued.Queue) <= len(state.Queue) {
		t.Error("enqueue did not grow the queue")
	}
	stopped, err := client.Stop(ctx)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Errorf("stop status = %q", stopped.Status)
	}
}
