package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

// publishableEngine wraps the deterministic fake so a test can emit a private
// ended update exactly like the Swift helper would.
type publishableEngine struct {
	*fakeengine.FakeEngine
	updates chan core.PlaybackStateUpdate
}

func newPublishableEngine() *publishableEngine {
	return &publishableEngine{FakeEngine: fakeengine.NewFakeEngine(), updates: make(chan core.PlaybackStateUpdate, 16)}
}

func (e *publishableEngine) SubscribeState(context.Context) (core.StateSubscription, error) {
	return core.StateSubscription{Updates: e.updates}, nil
}

func (e *publishableEngine) publish(update core.PlaybackStateUpdate) { e.updates <- update }

type recordingURLDriver struct {
	mu      sync.Mutex
	targets []core.URLPlaybackTarget
	status  string
	stops   int
}

func (d *recordingURLDriver) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = append(d.targets, target)
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url", Track: &core.Item{URL: target.URL}, Position: 1, Duration: 120}, nil
}
func (d *recordingURLDriver) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = "paused"
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (d *recordingURLDriver) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (d *recordingURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = "stopped"
	d.stops++
	return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
}
func (d *recordingURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status == "" {
		d.status = "stopped"
	}
	return core.PlaybackState{Status: d.status, Mode: "url"}, nil
}
func (d *recordingURLDriver) last() core.URLPlaybackTarget {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.targets) == 0 {
		return core.URLPlaybackTarget{}
	}
	return d.targets[len(d.targets)-1]
}
func (d *recordingURLDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.targets)
}
func (d *recordingURLDriver) stopCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stops
}

func audiusPlaybackUpstream(failStream map[string]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/playlists/p1/tracks":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"One","permalink":"/u/one","is_streamable":true,"duration":120,"user":{"name":"A"}},{"id":"t2","title":"Two","permalink":"/u/two","is_streamable":true,"duration":90,"user":{"name":"A"}}]}`))
		case r.URL.Path == "/tracks/t1":
			_, _ = w.Write([]byte(`{"data":{"id":"t1","title":"One","permalink":"/u/one","is_streamable":true,"duration":120,"artwork":{"1000x1000":"https://images.invalid/t1.jpg"},"user":{"name":"A"}}}`))
		case r.URL.Path == "/tracks/t2":
			_, _ = w.Write([]byte(`{"data":{"id":"t2","title":"Two","permalink":"/u/two","is_streamable":true,"duration":90,"user":{"name":"A"}}}`))
		case strings.HasSuffix(r.URL.Path, "/stream"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tracks/"), "/stream")
			if code, ok := failStream[id]; ok {
				http.Error(w, "private upstream body", code)
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":"https://signed.invalid/%s"}`, id)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func startAudiusPlaybackServer(t *testing.T, upstream *httptest.Server, driver URLPlaybackDriver) (*Server, string, *publishableEngine) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-aud-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	engine := newPublishableEngine()
	client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	server, err := Start(Options{
		SocketPath:        socket,
		Engine:            engine,
		Store:             state.New(filepath.Join(dir, "state.json")),
		AudiusClient:      &client,
		URLPlaybackDriver: driver,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, socket, engine
}

func waitForStatus(t *testing.T, socket string, check func(api.PlaybackState) bool) api.PlaybackState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last api.PlaybackState
	for time.Now().Before(deadline) {
		response := call(t, socket, "session.status", map[string]any{"includeQueue": true})
		if response.OK {
			var state api.PlaybackState
			if err := json.Unmarshal(response.Data, &state); err == nil {
				last = state
				if check(state) {
					return state
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status never satisfied; last = %+v", last)
	return last
}

func TestAudiusURLQueuePlaybackOverServer(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)

	response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"})
	if !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	if strings.Contains(string(response.Data), "signed.invalid") {
		t.Fatalf("signed URL leaked: %s", response.Data)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Source != api.SourceAudius || state.Mode != "full" || state.IsLive {
		t.Fatalf("state = %+v", state)
	}
	if state.QueueSource == nil || *state.QueueSource != api.SourceAudius || len(state.Queue) != 2 {
		t.Fatalf("queue = %+v", state)
	}
	if state.Track == nil || state.Track.Ref != "audius:song:t1" {
		t.Fatalf("track = %+v", state.Track)
	}
	if target := driver.last(); target.URL != "https://signed.invalid/t1" || target.ArtworkURL != "https://images.invalid/t1.jpg" || target.Duration != 120 || target.PlaybackGeneration == 0 || target.TransportSessionID == "" {
		t.Fatalf("target = %+v", target)
	}

	// queue.list reflects the server-owned queue.
	listed := call(t, socket, "queue.list", nil)
	var queue api.QueueState
	if err := json.Unmarshal(listed.Data, &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Source == nil || *queue.Source != api.SourceAudius || len(queue.Items) != 2 || queue.Index != 0 {
		t.Fatalf("queue list = %+v", queue)
	}

	// Controls route to the URL transport.
	for _, command := range []string{"playback.pause", "playback.resume", "playback.next", "playback.previous"} {
		if response := call(t, socket, command, nil); !response.OK {
			t.Fatalf("%s: %+v", command, response.Error)
		}
	}
	waitForStatus(t, socket, func(s api.PlaybackState) bool { return s.QueueIndex == 0 })

	// Phase 2.5: the URL queue is editable.
	added := call(t, socket, "queue.add", map[string]any{"ref": "audius:song:t2", "position": "append"})
	if !added.OK {
		t.Fatalf("queue.add: %+v", added.Error)
	}
	var addState api.PlaybackState
	if err := json.Unmarshal(added.Data, &addState); err != nil {
		t.Fatal(err)
	}
	if len(addState.Queue) != 3 || addState.Queue[2].Ref != "audius:song:t2" {
		t.Fatalf("after add: %+v", addState.Queue)
	}
	revision := addState.QueueRevision

	moved := call(t, socket, "queue.move", map[string]any{"from": 2, "to": 0, "ifQueueRevision": revision})
	if !moved.OK {
		t.Fatalf("queue.move: %+v", moved.Error)
	}
	var moveState api.PlaybackState
	if err := json.Unmarshal(moved.Data, &moveState); err != nil {
		t.Fatal(err)
	}
	if moveState.Queue[0].Ref != "audius:song:t2" || moveState.Track == nil || moveState.Track.Ref != "audius:song:t1" {
		t.Fatalf("after move: %+v", moveState)
	}

	conflict := call(t, socket, "queue.remove", map[string]any{"index": 0, "ifQueueRevision": revision})
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != api.CodeConflict {
		t.Fatalf("stale revision = %+v, want conflict", conflict)
	}

	removed := call(t, socket, "queue.remove", map[string]any{"index": 0, "ifQueueRevision": moveState.QueueRevision})
	if !removed.OK {
		t.Fatalf("queue.remove: %+v", removed.Error)
	}
	cleared := call(t, socket, "queue.clear", nil)
	if !cleared.OK {
		t.Fatalf("queue.clear: %+v", cleared.Error)
	}
	var clearState api.PlaybackState
	if err := json.Unmarshal(cleared.Data, &clearState); err != nil {
		t.Fatal(err)
	}
	if clearState.Status != "stopped" || len(clearState.Queue) != 0 || clearState.QueueSource != nil {
		t.Fatalf("after clear: %+v", clearState)
	}
	if response := call(t, socket, "playback.setShuffle", map[string]any{"on": true}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("setShuffle = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "playback.setRepeat", map[string]any{"mode": "all"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("setRepeat = %+v, want unsupported_command", response)
	}
}

func TestAudiusURLQueueAutoAdvanceAndEnd(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket, engine := startAudiusPlaybackServer(t, upstream, driver)

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	first := driver.last()

	engine.publish(core.PlaybackStateUpdate{State: core.PlaybackState{
		Status:             "stopped",
		Ended:              true,
		PlaybackGeneration: first.PlaybackGeneration,
		TransportSessionID: first.TransportSessionID,
	}})
	waitForStatus(t, socket, func(s api.PlaybackState) bool { return s.QueueIndex == 1 })
	if driver.count() != 2 || driver.last().URL != "https://signed.invalid/t2" {
		t.Fatalf("advance targets=%d last=%+v", driver.count(), driver.last())
	}

	second := driver.last()
	engine.publish(core.PlaybackStateUpdate{State: core.PlaybackState{
		Status:             "stopped",
		Ended:              true,
		PlaybackGeneration: second.PlaybackGeneration,
		TransportSessionID: second.TransportSessionID,
	}})
	waitForStatus(t, socket, func(s api.PlaybackState) bool {
		return s.Status == "stopped" && len(s.Queue) == 0 && s.QueueSource == nil
	})
}

func TestAudiusNonStreamableTracksAreHiddenAndRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tracks/search":
			_, _ = w.Write([]byte(`{"data":[{"id":"ok","title":"Playable","is_streamable":true,"user":{"name":"A"}},{"id":"no","title":"Gated","is_streamable":false,"user":{"name":"A"}}]}`))
		case "/tracks/no":
			_, _ = w.Write([]byte(`{"data":{"id":"no","title":"Gated","is_streamable":false,"user":{"name":"A"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer upstream.Close()
	_, socket, _ := startAudiusPlaybackServer(t, upstream, &recordingURLDriver{})

	response := call(t, socket, "discovery.search", map[string]any{"source": "audius", "term": "x", "type": "song"})
	if !response.OK {
		t.Fatalf("search: %+v", response.Error)
	}
	var result api.SearchResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	songs := result.Groups[api.GroupSongs]
	if len(songs) != 1 || songs[0].ID != "audius:song:ok" {
		t.Fatalf("songs = %+v, want only the streamable track", songs)
	}

	response = call(t, socket, "playback.play", map[string]any{"ref": "audius:song:no"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeInvalidReference {
		t.Fatalf("play gated = %+v, want invalid_reference", response)
	}
}

func TestAudiusSwitchToEngineStopsURLTransport(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("audius play: %+v", response.Error)
	}
	if driver.stopCount() != 0 {
		t.Fatalf("unexpected stop before switch: %d", driver.stopCount())
	}
	if response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1"}); !response.OK {
		t.Fatalf("apple play: %+v", response.Error)
	}
	if driver.stopCount() == 0 {
		t.Fatal("switching to an engine source did not stop the URL transport")
	}
	listed := call(t, socket, "queue.list", nil)
	var queue api.QueueState
	if err := json.Unmarshal(listed.Data, &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Source == nil || *queue.Source != api.SourceAppleMusic {
		t.Fatalf("queue after switch = %+v", queue)
	}
}

func TestAudiusURLQueueAddPlaylistAddsAllTracks(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	_, socket, _ := startAudiusPlaybackServer(t, upstream, &recordingURLDriver{})
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	added := call(t, socket, "queue.add", map[string]any{"ref": "audius:playlist:p1", "position": "append"})
	if !added.OK {
		t.Fatalf("queue.add playlist: %+v", added.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(added.Data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 3 || state.Queue[1].Ref != "audius:song:t1" || state.Queue[2].Ref != "audius:song:t2" {
		t.Fatalf("playlist add = %+v", state.Queue)
	}
}

func TestAudiusDisconnectStopsPlayback(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	_, socket, _ := startAudiusPlaybackServer(t, upstream, &recordingURLDriver{})
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	if response := call(t, socket, "authorization.disconnect", map[string]any{"source": "audius"}); !response.OK {
		t.Fatalf("disconnect: %+v", response.Error)
	}
	listed := call(t, socket, "queue.list", nil)
	var queue api.QueueState
	if err := json.Unmarshal(listed.Data, &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after disconnect = %+v", queue)
	}
	response := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "stopped" {
		t.Fatalf("status after disconnect = %q", state.Status)
	}
}

func TestAudiusURLQueueBoundariesAreStateErrors(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	_, socket, _ := startAudiusPlaybackServer(t, upstream, &recordingURLDriver{})
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	if response := call(t, socket, "playback.next", nil); response.OK || response.Error.Code != api.CodeInvalidState {
		t.Fatalf("next = %+v, want invalid_state", response)
	}
	if response := call(t, socket, "queue.jump", map[string]any{"index": 5}); response.OK || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("jump = %+v, want invalid_request", response)
	}
	// A boundary error must not end the session.
	listed := call(t, socket, "queue.list", nil)
	var queue api.QueueState
	if err := json.Unmarshal(listed.Data, &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Source == nil || len(queue.Items) != 1 || queue.Index != 0 {
		t.Fatalf("queue after boundary errors = %+v", queue)
	}
}

func TestAudiusURLQueueErrorMapping(t *testing.T) {
	t.Run("initial resolution failure is playback_error", func(t *testing.T) {
		upstream := audiusPlaybackUpstream(map[string]int{"t1": http.StatusInternalServerError})
		defer upstream.Close()
		driver := &recordingURLDriver{}
		_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
		response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"})
		if response.OK || response.Error == nil || response.Error.Code != api.CodePlaybackError {
			t.Fatalf("play = %+v, want playback_error", response)
		}
		if _, ok := response.Error.Details["state"]; !ok {
			t.Fatalf("missing details.state: %+v", response.Error)
		}
	})

	t.Run("mid-queue failure is source_unavailable", func(t *testing.T) {
		upstream := audiusPlaybackUpstream(map[string]int{"t2": http.StatusInternalServerError})
		defer upstream.Close()
		driver := &recordingURLDriver{}
		_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
		if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !response.OK {
			t.Fatalf("playlist play: %+v", response.Error)
		}
		response := call(t, socket, "queue.jump", map[string]any{"index": 1})
		if response.OK || response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
			t.Fatalf("jump = %+v, want source_unavailable", response)
		}
		if _, ok := response.Error.Details["state"]; !ok {
			t.Fatalf("missing details.state: %+v", response.Error)
		}
		waitForStatus(t, socket, func(s api.PlaybackState) bool {
			return s.Status == "stopped" && len(s.Queue) == 0
		})
	})
}

func TestPlaySongsPreservesSubmittedOrder(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tracks/") && strings.HasSuffix(r.URL.Path, "/stream") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tracks/"), "/stream")
			_, _ = w.Write([]byte(`{"data":"https://media.example/` + id + `"}`))
			return
		}
		if r.URL.Path == "/tracks" {
			// Audius returns its own order, not the requested one.
			_, _ = w.Write([]byte(`{"data":[
				{"id":"c","title":"C","permalink":"/a/c","is_streamable":true,"duration":100,"user":{"name":"A"}},
				{"id":"a","title":"A","permalink":"/a/a","is_streamable":true,"duration":100,"user":{"name":"A"}},
				{"id":"b","title":"B","permalink":"/a/b","is_streamable":true,"duration":100,"user":{"name":"A"}}
			]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	driver := &recordingURLDriver{}
	_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
	response := call(t, socket, "playback.playSongs", map[string]any{
		"refs":       []string{"audius:song:a", "audius:song:b", "audius:song:c"},
		"startIndex": 0,
	})
	if !response.OK {
		t.Fatalf("playSongs: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(state.Queue))
	for _, item := range state.Queue {
		got = append(got, item.ProviderID)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("queue order = %v, want [a b c]", got)
	}
}

func TestAudiusPlayFromHereDropsEarlierTracks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tracks/") && strings.HasSuffix(r.URL.Path, "/stream") {
			_, _ = w.Write([]byte(`{"data":"https://media.example/x"}`))
			return
		}
		if r.URL.Path == "/playlists/p1/tracks" {
			_, _ = w.Write([]byte(`{"data":[
				{"id":"a","title":"A","permalink":"/a/a","is_streamable":true,"duration":100,"user":{"name":"A"}},
				{"id":"b","title":"B","permalink":"/a/b","is_streamable":true,"duration":100,"user":{"name":"A"}},
				{"id":"c","title":"C","permalink":"/a/c","is_streamable":true,"duration":100,"user":{"name":"A"}}
			]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	driver := &recordingURLDriver{}
	_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
	response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1", "startAt": 1, "fromHere": true})
	if !response.OK {
		t.Fatalf("play: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(state.Queue))
	for _, item := range state.Queue {
		got = append(got, item.ProviderID)
	}
	if strings.Join(got, ",") != "b,c" {
		t.Fatalf("queue = %v, want [b c] (history dropped)", got)
	}
	if state.QueueIndex != 0 {
		t.Fatalf("queueIndex = %d, want 0", state.QueueIndex)
	}
}

// A source that never declared shuffle/repeat must refuse the form before
// playback starts, instead of reporting success and dropping the parameter.
func TestUnsupportedFormIsRejectedBeforePlayback(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)

	response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1", "shuffle": true})
	if response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius shuffle = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1", "repeat": "all"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius repeat = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"audius:song:t1", "audius:song:t2"}, "shuffle": true}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius playSongs shuffle = %+v, want unsupported_command", response)
	}
	if response := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"audius:song:t1", "audius:song:t2"}, "repeat": "all"}); response.OK || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("audius playSongs repeat = %+v, want unsupported_command", response)
	}
	if got := driver.count(); got != 0 {
		t.Fatalf("refused forms still started playback: %d starts", got)
	}

	// The same request without the unsupported form still plays.
	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("audius play without form failed: %+v", response.Error)
	}
	if got := driver.count(); got != 1 {
		t.Fatalf("expected one playback start, got %d", got)
	}
}

// Apple Music declares shuffle and repeat, so the form is applied as before.
func TestSupportedFormStillApplies(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:s1", "shuffle": true, "repeat": "all"})
	if !response.OK {
		t.Fatalf("apple play with form failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Shuffle || state.Repeat != "all" {
		t.Fatalf("apple form not applied: shuffle=%v repeat=%q", state.Shuffle, state.Repeat)
	}
}

// stalledURLDriver models a helper that accepted the URL and then never made
// progress: state stays buffering at position 0 with no playbackError.
type stalledURLDriver struct {
	recordingURLDriver
}

func (d *stalledURLDriver) PlayURL(context.Context, core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = append(d.targets, core.URLPlaybackTarget{})
	d.status = "buffering"
	return core.PlaybackState{Status: "buffering", Mode: "url", Position: 0}, nil
}

func (d *stalledURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status == "stopped" {
		return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
	}
	return core.PlaybackState{Status: "buffering", Mode: "url", Position: 0}, nil
}

// A URL session that reports no progress forever must not stay buffering: the
// watchdog retries once through the media-failure path and then ends the
// session.
func TestURLStallWatchdogRetriesThenEndsTheSession(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &stalledURLDriver{}
	server, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
	server.mu.Lock()
	server.urlStallBudget = time.Millisecond
	server.mu.Unlock()

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("audius play failed: %+v", response.Error)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if driver.count() >= 2 && driver.stopCount() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stall was never handled: starts=%d stops=%d", driver.count(), driver.stopCount())
}

// A new playback must not inherit the previous one's shuffle/repeat: MusicKit
// keeps its form across plays, so an omitted parameter used to leak it
// (batch manual-20260920 OQ9).
func TestPlayWithoutFormResetsInheritedShuffle(t *testing.T) {
	engine := fakeengine.NewFakeEngine()
	_, socket := startTestServerWithEngine(t, engine)
	if response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:s1", "shuffle": true, "repeat": "all"}); !response.OK {
		t.Fatalf("seed play failed: %+v", response.Error)
	}
	response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:s2"})
	if !response.OK {
		t.Fatalf("plain play failed: %+v", response.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Shuffle || state.Repeat != "off" {
		t.Fatalf("plain play inherited the form: shuffle=%v repeat=%q", state.Shuffle, state.Repeat)
	}
	// The engine itself must be cleared, not only the projection.
	engineState, err := engine.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if engineState.Shuffle || engineState.Repeat != "off" {
		t.Fatalf("engine kept the form: shuffle=%v repeat=%q", engineState.Shuffle, engineState.Repeat)
	}

	// playSongs follows the same rule.
	if response := call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:s1"}, "repeat": "one"}); !response.OK {
		t.Fatalf("seed playSongs failed: %+v", response.Error)
	}
	response = call(t, socket, "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:s2"}})
	if !response.OK {
		t.Fatalf("plain playSongs failed: %+v", response.Error)
	}
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Shuffle || state.Repeat != "off" {
		t.Fatalf("plain playSongs inherited the form: shuffle=%v repeat=%q", state.Shuffle, state.Repeat)
	}
}

// formOrderEngine records the order of form changes and queue starts.
type formOrderEngine struct {
	*fakeengine.FakeEngine
	mu    sync.Mutex
	calls []string
}

func (e *formOrderEngine) record(call string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, call)
}

func (e *formOrderEngine) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = nil
}

func (e *formOrderEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	e.record("play")
	return e.FakeEngine.PlayState(ctx, request)
}

func (e *formOrderEngine) SetShuffle(ctx context.Context, on bool) (core.PlaybackState, error) {
	e.record("shuffle=" + map[bool]string{true: "true", false: "false"}[on])
	return e.FakeEngine.SetShuffle(ctx, on)
}

func (e *formOrderEngine) SetRepeat(ctx context.Context, mode string) (core.PlaybackState, error) {
	e.record("repeat=" + mode)
	return e.FakeEngine.SetRepeat(ctx, mode)
}

// The form must be settled before the queue is built: clearing an inherited
// shuffle afterwards rebuilt a freshly filled album queue down to one entry and
// left playback stopped (batch 2026-09-20-form-and-playlist-fixes r3).
func TestFormIsAppliedBeforeTheQueueIsBuilt(t *testing.T) {
	engine := &formOrderEngine{FakeEngine: fakeengine.NewFakeEngine()}
	_, socket := startTestServerWithEngine(t, engine)

	if response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:s1", "shuffle": true, "repeat": "all"}); !response.OK {
		t.Fatalf("seed play failed: %+v", response.Error)
	}
	engine.reset()
	if response := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:s2"}); !response.OK {
		t.Fatalf("plain play failed: %+v", response.Error)
	}
	engine.mu.Lock()
	calls := append([]string(nil), engine.calls...)
	engine.mu.Unlock()
	if len(calls) != 3 || calls[0] != "shuffle=false" || calls[1] != "repeat=off" || calls[2] != "play" {
		t.Fatalf("call order = %v, want the form before the start", calls)
	}
}
