package fakeengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/3-tiao/lilt/core"
)

func TestFullQueueFakeOnlyChangesOptInConstructor(t *testing.T) {
	ctx := context.Background()
	defaultFake := NewFakeEngine()
	fullFake := NewFullQueueFakeEngine()
	for _, check := range []struct {
		name       string
		engine     *FakeEngine
		mode, auth string
	}{
		{"default", defaultFake, "preview", "denied"},
		{"full", fullFake, "full", "authorized"},
	} {
		state, err := check.engine.PlaySongs(ctx, core.PlaySongsRequest{IDs: []string{"a", "b"}})
		if err != nil || state.Mode != check.mode {
			t.Fatalf("%s play mode = %q err=%v", check.name, state.Mode, err)
		}
		auth, err := check.engine.Authorization(ctx)
		if err != nil || auth.Status != check.auth {
			t.Fatalf("%s authorization = %+v err=%v", check.name, auth, err)
		}
	}
}

// Removing the current entry advances to the next one and clamps the index, the
// same cursor rules the helper applies: without them a remove left queueIndex
// out of range ("2/1") after deleting the current row (batch 2026-09-23-polish,
// rp3 replay).
func TestQueueRemoveKeepsIndexInRange(t *testing.T) {
	engine := NewFakeEngine()
	state, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	if state.QueueIndex != 0 {
		t.Fatalf("start index = %d", state.QueueIndex)
	}
	if _, err := engine.QueueJump(context.Background(), 1); err != nil {
		t.Fatalf("QueueJump: %v", err)
	}
	// Remove the current (last) entry: the index must land on the remaining row.
	removed, err := engine.QueueRemove(context.Background(), 1)
	if err != nil {
		t.Fatalf("QueueRemove: %v", err)
	}
	state = removed.State
	if len(state.Queue) != 1 || state.QueueIndex != 0 {
		t.Fatalf("after removing the current row: queue=%d index=%d, want 1/0", len(state.Queue), state.QueueIndex)
	}

	// Remove an entry before the cursor: the cursor shifts down with it.
	if _, err := engine.PlaySongs(context.Background(), core.PlaySongsRequest{IDs: []string{"a", "b", "c"}}); err != nil {
		t.Fatalf("PlaySongs: %v", err)
	}
	if _, err := engine.QueueJump(context.Background(), 2); err != nil {
		t.Fatalf("QueueJump: %v", err)
	}
	removed, err = engine.QueueRemove(context.Background(), 0)
	if err != nil {
		t.Fatalf("QueueRemove: %v", err)
	}
	state = removed.State
	if len(state.Queue) != 2 || state.QueueIndex != 1 {
		t.Fatalf("after removing a row before the cursor: queue=%d index=%d, want 2/1", len(state.Queue), state.QueueIndex)
	}
}

func TestURLPlaybackIsSilentSessionBoundAndDoesNotRetainMediaURL(t *testing.T) {
	var requests atomic.Int32
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer media.Close()
	ctx := context.Background()
	f := NewFakeEngine()
	item := core.Item{Kind: "song", ID: "track-1", Title: "Offline fixture"}
	state, err := f.PlayURL(ctx, core.URLPlaybackTarget{Item: item, URL: media.URL + "/signed", Duration: 73, PlaybackGeneration: 2, TransportSessionID: "session-1"})
	if err != nil || state.Status != "playing" || state.Mode != "url" || state.Track == nil || state.Track.ID != item.ID || state.Duration != 73 {
		t.Fatalf("fake URL play: state=%+v err=%v", state, err)
	}
	if state.Track.URL != "" || requests.Load() != 0 {
		t.Fatalf("media URL leaked or fetched: track=%+v requests=%d", state.Track, requests.Load())
	}
	state, err = f.PauseURL(ctx, 2, "session-1")
	if err != nil || state.Status != "paused" {
		t.Fatalf("pause: state=%+v err=%v", state, err)
	}
	if _, err := f.ResumeURL(ctx, 1, "session-1"); err == nil {
		t.Fatal("stale generation resumed current session")
	}
	if _, err := f.StopURL(ctx, 2, "different-session"); err == nil {
		t.Fatal("another session stopped current playback")
	}
	state, err = f.StateURL(ctx, 2, "session-1")
	if err != nil || state.Status != "paused" {
		t.Fatalf("stale actions mutated paused state: state=%+v err=%v", state, err)
	}
	state, err = f.ResumeURL(ctx, 2, "session-1")
	if err != nil || state.Status != "playing" {
		t.Fatalf("resume: state=%+v err=%v", state, err)
	}
	state, err = f.PlayURL(ctx, core.URLPlaybackTarget{Item: core.Item{Kind: "song", ID: "track-2"}, URL: media.URL + "/next", PlaybackGeneration: 3, TransportSessionID: "session-2"})
	if err != nil || state.Track == nil || state.Track.ID != "track-2" {
		t.Fatalf("new session: state=%+v err=%v", state, err)
	}
	if _, err := f.PlayURL(ctx, core.URLPlaybackTarget{Item: item, URL: media.URL, PlaybackGeneration: 2, TransportSessionID: "session-1"}); err == nil {
		t.Fatal("stale play replaced the newer session")
	}
	if _, err := f.PauseURL(ctx, 2, "session-1"); err == nil {
		t.Fatal("previous session paused the newer one")
	}
	state, err = f.StopURL(ctx, 3, "session-2")
	if err != nil || state.Status != "stopped" || state.Track != nil || len(state.Queue) != 0 {
		t.Fatalf("stop: state=%+v err=%v", state, err)
	}
	if _, err := f.StateURL(ctx, 3, "session-2"); err == nil {
		t.Fatal("stopped session remained active")
	}
	if _, err := f.PlayURL(ctx, core.URLPlaybackTarget{Item: item, URL: media.URL, PlaybackGeneration: 3, TransportSessionID: "session-2"}); err == nil {
		t.Fatal("stopped session restarted without a new generation")
	}
	if _, err := f.PlayURL(ctx, core.URLPlaybackTarget{Item: item, URL: media.URL, PlaybackGeneration: 4, TransportSessionID: "session-3"}); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	if _, err := f.RadioPlay(ctx, "https://radio.example.invalid/stream", "Radio"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PauseURL(ctx, 4, "session-3"); err == nil {
		t.Fatal("previous URL session paused radio")
	}
	if requests.Load() != 0 {
		t.Fatalf("fake playback requested media %d times", requests.Load())
	}
}
