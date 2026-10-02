package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/state"
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
		case r.URL.Path == "/tracks":
			// The batch endpoint: the expanded container play prepares song
			// refs through it. Echo back the two fixture tracks in order.
			ids := r.URL.Query()["id"]
			payload := make([]string, 0, len(ids))
			for _, id := range ids {
				switch id {
				case "t1":
					payload = append(payload, `{"id":"t1","title":"One","permalink":"/u/one","is_streamable":true,"duration":120,"user":{"name":"A"}}`)
				case "t2":
					payload = append(payload, `{"id":"t2","title":"Two","permalink":"/u/two","is_streamable":true,"duration":90,"user":{"name":"A"}}`)
				}
			}
			_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(payload, ","))
		case r.URL.Path == "/playlists/p1/tracks":
			_, _ = w.Write([]byte(`{"data":[{"id":"t1","title":"One","permalink":"/u/one","is_streamable":true,"duration":120,"user":{"name":"A"}},{"id":"t2","title":"Two","permalink":"/u/two","is_streamable":true,"duration":90,"user":{"name":"A"}}]}`))
		case r.URL.Path == "/playlists/p1":
			// The play path expands container refs through the provider
			// registry, so the expansion needs the object endpoint too.
			_, _ = w.Write([]byte(`{"data":{"id":"p1","playlist_name":"Mix","user":{"name":"A"}}}`))
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

func startAudiusPlaybackServer(t *testing.T, upstream *httptest.Server, driver URLPlaybackDriver, logf ...func(string, map[string]any)) (*Server, string, *publishableEngine) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-aud-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	engine := newPublishableEngine()
	client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	options := Options{
		SocketPath:        socket,
		Engine:            engine,
		Store:             state.New(filepath.Join(dir, "state.json")),
		AudiusClient:      &client,
		URLPlaybackDriver: driver,
	}
	if len(logf) > 0 {
		options.Log = logf[0]
	}
	server, err := Start(options)
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

func TestInjectedFakeEngineDrivesAudiusURLQueueWithoutMediaRequests(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	dir, err := os.MkdirTemp("/tmp", "lilt-fake-url-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	fake := fakeengine.NewFakeEngine()
	srv, err := Start(Options{SocketPath: socket, Engine: fake, AudiusClient: &client, Store: state.New(filepath.Join(dir, "state.json"))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"})
	if !response.OK {
		t.Fatalf("fake URL queue play: %+v", response.Error)
	}
	var playing api.PlaybackState
	if err := json.Unmarshal(response.Data, &playing); err != nil {
		t.Fatal(err)
	}
	if playing.Mode != "full" || playing.Track == nil || playing.Track.Ref != "audius:song:t1" || len(playing.Queue) != 2 {
		t.Fatalf("fake URL queue = %+v", playing)
	}
	if strings.Contains(string(response.Data), "signed.invalid") {
		t.Fatalf("signed media URL leaked: %s", response.Data)
	}
	if response := call(t, socket, "queue.add", map[string]any{"ref": "audius:song:t1", "position": "append"}); !response.OK {
		t.Fatalf("queue add: %+v", response.Error)
	}
	if response := call(t, socket, "queue.jump", map[string]any{"index": 1}); !response.OK {
		t.Fatalf("queue jump: %+v", response.Error)
	}
	if response := call(t, socket, "playback.pause", nil); !response.OK {
		t.Fatalf("pause: %+v", response.Error)
	}
	paused := waitForStatus(t, socket, func(s api.PlaybackState) bool {
		return s.Status == "paused" && s.Track != nil && s.Track.Ref == "audius:song:t2"
	})
	if len(paused.Queue) != 3 || paused.QueueIndex != 1 {
		t.Fatalf("paused queue = %+v", paused)
	}
	if response := call(t, socket, "playback.resume", nil); !response.OK {
		t.Fatalf("resume: %+v", response.Error)
	}
	if response := call(t, socket, "playback.stop", nil); !response.OK {
		t.Fatalf("stop: %+v", response.Error)
	}
	stopped := waitForStatus(t, socket, func(s api.PlaybackState) bool { return s.Status == "stopped" })
	if stopped.Track != nil || len(stopped.Queue) != 0 {
		t.Fatalf("fake URL queue survived stop: %+v", stopped)
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
	// Disconnect ends the URL session, so queue.list has no owner to answer
	// for; the emptied queue is observed through the session snapshot.
	if listed := call(t, socket, "queue.list", nil); listed.OK || listed.Error.Code != api.CodeQueueUnavailable {
		t.Fatalf("queue.list after disconnect = %+v, want queue_unavailable", listed.Error)
	}
	response := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	var state api.PlaybackState
	if err := json.Unmarshal(response.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "stopped" {
		t.Fatalf("status after disconnect = %q", state.Status)
	}
	if state.QueueSource != nil || len(state.Queue) != 0 {
		t.Fatalf("queue after disconnect = %+v", state)
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
		"startIndex": 1,
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
	if state.QueueIndex != 1 || state.Track == nil || state.Track.ProviderID != "b" || driver.last().URL != "https://media.example/b" {
		t.Fatalf("selected track = %+v index %d target %+v; want b at index 1", state.Track, state.QueueIndex, driver.last())
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
		if r.URL.Path == "/playlists/p1" {
			// The play path expands container refs through the provider
			// registry, so the expansion needs the object endpoint too.
			_, _ = w.Write([]byte(`{"data":{"id":"p1","playlist_name":"Mix","user":{"name":"A"}}}`))
			return
		}
		if r.URL.Path == "/tracks" {
			// The batch discovery endpoint the expanded play prepares through.
			ids := r.URL.Query()["id"]
			payload := make([]string, 0, len(ids))
			for _, id := range ids {
				payload = append(payload, `{"id":"`+id+`","title":"`+strings.ToUpper(id)+`","permalink":"/a/`+id+`","is_streamable":true,"duration":100,"user":{"name":"A"}}`)
			}
			_, _ = w.Write([]byte(`{"data":[` + strings.Join(payload, ",") + `]}`))
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
// session. Each stall detection journals a playback_stalled notice first (the
// only evidence a retry ever happened), and the terminal warning must say
// playback_error (the source itself is fine) and must reach the journal so
// `lilt log` can explain the stop.
func TestURLStallWatchdogRetriesThenEndsTheSession(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &stalledURLDriver{}
	warnings := make(chan map[string]any, 8)
	server, socket, _ := startAudiusPlaybackServer(t, upstream, driver, func(kind string, fields map[string]any) {
		if kind == "server.warning" {
			warnings <- fields
		}
	})
	server.mu.Lock()
	server.urlStallBudget = time.Millisecond
	server.mu.Unlock()

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("audius play failed: %+v", response.Error)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if driver.count() >= 2 && driver.stopCount() > 0 {
			// Two stall notices (one per watchdog fire), then the terminal
			// playback_error. A skip would change the last code, so this
			// also pins the single-item no-next behavior.
			codes := []string{}
			terminal := false
			for !terminal {
				select {
				case warning := <-warnings:
					code, _ := warning["code"].(string)
					codes = append(codes, code)
					if code == api.CodePlaybackError {
						terminal = true
					} else if code != api.CodePlaybackStalled {
						t.Fatalf("unexpected warning code %q in %v", code, codes)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("terminal stall warning never arrived, codes=%v", codes)
				}
			}
			if len(codes) != 3 {
				t.Fatalf("warning codes = %v, want two stalls then playback_error", codes)
			}
			select {
			case warning := <-warnings:
				t.Fatalf("unexpected extra warning after the terminal one: %+v", warning)
			default:
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stall was never handled: starts=%d stops=%d", driver.count(), driver.stopCount())
}

// The live incident shape (2026-09-22, jamendo trending): one queue item's
// stream is dead while the rest play fine. The watchdog must stall, retry,
// then skip the dead item and keep the queue — no playback_error for a queue
// that healed itself.
func TestURLStallWatchdogSkipsDeadItemAndKeepsQueue(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &deadThenLiveURLDriver{}
	warnings := make(chan map[string]any, 8)
	server, socket, _ := startAudiusPlaybackServer(t, upstream, driver, func(kind string, fields map[string]any) {
		if kind == "server.warning" {
			warnings <- fields
		}
	})
	server.mu.Lock()
	server.urlStallBudget = time.Millisecond
	server.mu.Unlock()
	watchCtx, watchCancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer watchCancel()
	_, watcher, err := api.Watch(watchCtx, socket, []string{"playback", "server"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !response.OK {
		t.Fatalf("audius play failed: %+v", response.Error)
	}

	deadline := time.Now().Add(12 * time.Second)
	var state api.PlaybackState
	for time.Now().Before(deadline) {
		response := call(t, socket, "session.status", map[string]any{"includeQueue": true})
		if response.OK {
			var current api.PlaybackState
			if err := json.Unmarshal(response.Data, &current); err == nil {
				state = current
				if state.QueueIndex == 1 && state.Status == "playing" {
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state.QueueIndex != 1 || state.Track == nil || state.Track.Title != "Two" {
		t.Fatalf("state after skip = %+v, want the queue on \"Two\"", state)
	}
	if len(state.Queue) != 2 {
		t.Fatalf("queue after skip = %+v, want both items kept", state.Queue)
	}
	// Two stall notices (one per watchdog fire) then the skip warning; the
	// session never stopped, so no playback_error may exist.
	assertWarningSequence(t, warnings, []string{api.CodePlaybackStalled, api.CodePlaybackStalled, api.CodePlaybackSkipped})
	var skipPlaybackSequence uint64
	for {
		select {
		case event := <-watcher.Events:
			switch event.Event {
			case "playback.changed":
				var payload struct {
					State api.PlaybackState `json:"state"`
				}
				if err := json.Unmarshal(event.Data, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.State.QueueIndex == 1 {
					skipPlaybackSequence = event.Sequence
				}
			case "server.warning":
				var payload struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(event.Data, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Code == api.CodePlaybackSkipped {
					if skipPlaybackSequence == 0 || event.Sequence <= skipPlaybackSequence {
						t.Fatalf("skip warning sequence %d must follow playback sequence %d", event.Sequence, skipPlaybackSequence)
					}
					goto checkedWatch
				}
			}
		case <-watchCtx.Done():
			t.Fatal("no playback_skipped watch event")
		}
	}
checkedWatch:
	select {
	case warning := <-warnings:
		t.Fatalf("unexpected warning after the skip: %+v", warning)
	default:
	}
	if driver.stopCount() != 0 {
		t.Fatalf("stops = %d, the skipped session must stay alive", driver.stopCount())
	}
}

// A helper-reported media failure takes the same path: the journal records
// the failed item, and a dead item is skipped instead of ending the queue.
func TestURLMediaFailureSkipsDeadItemAndKeepsQueue(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &recordingURLDriver{}
	warnings := make(chan map[string]any, 8)
	server, socket, engine := startAudiusPlaybackServer(t, upstream, driver, func(kind string, fields map[string]any) {
		if kind == "server.warning" {
			warnings <- fields
		}
	})

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"}); !response.OK {
		t.Fatalf("audius play failed: %+v", response.Error)
	}
	publishMediaFailure := func() {
		server.mu.Lock()
		generation, session := server.playbackGeneration, server.transportSessionID
		server.mu.Unlock()
		engine.publish(core.PlaybackStateUpdate{State: core.PlaybackState{
			Mode: "url", Status: "error", Error: "AVPlayer: the stream could not be loaded",
			PlaybackGeneration: generation, TransportSessionID: session,
		}})
	}
	// First failure: re-resolve and replay "One" in place.
	publishMediaFailure()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && driver.count() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if driver.count() != 2 {
		t.Fatalf("plays = %d, want the retry replay", driver.count())
	}
	// Second failure on the same item: skip to "Two" and keep playing.
	publishMediaFailure()
	state := waitForStatus(t, socket, func(s api.PlaybackState) bool {
		return s.QueueIndex == 1 && s.Status == "playing"
	})
	if state.Track == nil || state.Track.Title != "Two" || len(state.Queue) != 2 {
		t.Fatalf("state after skip = %+v", state)
	}
	assertWarningSequence(t, warnings, []string{api.CodePlaybackStalled, api.CodePlaybackStalled, api.CodePlaybackSkipped})
	if driver.stopCount() != 0 {
		t.Fatalf("stops = %d, the skipped session must stay alive", driver.stopCount())
	}
}

// assertWarningSequence drains the journal-warning channel and requires the
// codes to arrive in exactly the given order.
func assertWarningSequence(t *testing.T, warnings <-chan map[string]any, want []string) {
	t.Helper()
	for _, code := range want {
		select {
		case warning := <-warnings:
			got, _ := warning["code"].(string)
			if got != code {
				t.Fatalf("warning code = %q, want %q", got, code)
			}
			if code == api.CodePlaybackSkipped {
				if message, _ := warning["message"].(string); !strings.Contains(message, `"One"`) {
					t.Fatalf("skip warning does not name the dead item: %q", message)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("warning %q never arrived", code)
		}
	}
}

// A driver-reported pause is a real pause and must never be retried, whatever
// asked for it: the client, or a system media key the server never sees as a
// command. The helper contract is what keeps this safe — a stream that stopped
// progressing is reported as "buffering", not "paused" — so the watchdog may
// trust "paused" and rest (observed live 2026-09-22: a media key paused a URL
// session, and the previous position-0 heuristic restarted it).
type pausedURLDriver struct {
	recordingURLDriver
}

// deadThenLiveURLDriver freezes the first target at position 0 — a stream
// whose URL resolves but never delivers audio — and lets every later target
// advance, so the stall watchdog fires exactly once per dead item.
type deadThenLiveURLDriver struct {
	recordingURLDriver
	position float64
}

func (d *deadThenLiveURLDriver) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = append(d.targets, target)
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url", Position: 0}, nil
}

func (d *deadThenLiveURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status == "stopped" {
		return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
	}
	if target := d.targets[len(d.targets)-1]; target.URL == "https://signed.invalid/t1" {
		return core.PlaybackState{Status: "playing", Mode: "url", Position: 0}, nil
	}
	d.position++
	return core.PlaybackState{Status: "playing", Mode: "url", Position: d.position}, nil
}

func (d *pausedURLDriver) PlayURL(context.Context, core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = append(d.targets, core.URLPlaybackTarget{})
	d.status = "paused"
	return core.PlaybackState{Status: "paused", Mode: "url", Position: 0}, nil
}

func (d *pausedURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status == "stopped" {
		return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
	}
	return core.PlaybackState{Status: "paused", Mode: "url", Position: 0}, nil
}

func TestURLStallWatchdogLeavesAPauseAlone(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	driver := &pausedURLDriver{}
	server, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
	server.mu.Lock()
	server.urlStallBudget = time.Millisecond
	server.mu.Unlock()

	if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:t1"}); !response.OK {
		t.Fatalf("audius play failed: %+v", response.Error)
	}
	before := driver.count()
	time.Sleep(1500 * time.Millisecond)
	if got := driver.count(); got != before {
		t.Fatalf("a pause was retried as a stall: starts %d -> %d", before, got)
	}

	// A client pause is the same resting state.
	if response := call(t, socket, "playback.pause", nil); !response.OK {
		t.Fatalf("pause failed: %+v", response.Error)
	}
	before = driver.count()
	time.Sleep(1500 * time.Millisecond)
	if got := driver.count(); got != before {
		t.Fatalf("a client pause was retried as a stall: starts %d -> %d", before, got)
	}
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

// A plain play still asks the helper to clear its inherited form. If either
// setting fails, playback may have started, but success would claim an "off"
// form that the engine still has not applied.
type failingFormEngine struct {
	*fakeengine.FakeEngine
	fail string
}

func (e *failingFormEngine) SetShuffle(ctx context.Context, on bool) (core.PlaybackState, error) {
	if e.fail == "shuffle" && !on {
		return core.PlaybackState{}, errors.New("cannot clear shuffle")
	}
	return e.FakeEngine.SetShuffle(ctx, on)
}

func (e *failingFormEngine) SetRepeat(ctx context.Context, mode string) (core.PlaybackState, error) {
	if e.fail == "repeat" && mode == "off" {
		return core.PlaybackState{}, errors.New("cannot clear repeat")
	}
	return e.FakeEngine.SetRepeat(ctx, mode)
}

func TestPlayReportsFailedDefaultFormAsPartialFailure(t *testing.T) {
	for _, command := range []string{"playback.play", "playback.playSongs"} {
		for _, failedSetting := range []string{"shuffle", "repeat"} {
			t.Run(command+"/"+failedSetting, func(t *testing.T) {
				engine := &failingFormEngine{FakeEngine: fakeengine.NewFakeEngine()}
				_, socket := startTestServerWithEngine(t, engine)
				seed := map[string]any{"ref": "apple-music:song:s1", "shuffle": true, "repeat": "all"}
				if response := call(t, socket, "playback.play", seed); !response.OK {
					t.Fatalf("seed play failed: %+v", response.Error)
				}
				engine.fail = failedSetting
				watchCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				initial, watcher, err := api.Watch(watchCtx, socket, []string{"playback"}, false)
				if err != nil || !initial.OK {
					t.Fatalf("watch: response=%+v err=%v", initial, err)
				}
				defer watcher.Close()
				params := map[string]any{"ref": "apple-music:song:s2"}
				if command == "playback.playSongs" {
					params = map[string]any{"refs": []string{"apple-music:song:s2"}}
				}
				response := call(t, socket, command, params)
				if response.OK || response.Error.Code != api.CodePartialFailure {
					t.Fatalf("response = %+v, want partial_failure", response)
				}
				var projected api.PlaybackState
				body, err := json.Marshal(response.Error.Details["state"])
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &projected); err != nil {
					t.Fatal(err)
				}
				actual, err := engine.State(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if projected.Status != "playing" || projected.Shuffle != actual.Shuffle || projected.Repeat != actual.Repeat {
					t.Fatalf("reported state = %+v, engine = %+v", projected, actual)
				}
				if failedSetting == "shuffle" && !actual.Shuffle || failedSetting == "repeat" && actual.Repeat != "all" {
					t.Fatalf("failure not reflected in engine: %+v", actual)
				}
				select {
				case event := <-watcher.Events:
					var body struct {
						State api.PlaybackState `json:"state"`
					}
					if event.Event != "playback.changed" || json.Unmarshal(event.Data, &body) != nil ||
						body.State.Sequence != projected.Sequence || body.State.Shuffle != actual.Shuffle || body.State.Repeat != actual.Repeat {
						t.Fatalf("watch event = %+v, response state = %+v", event, projected)
					}
				case <-watchCtx.Done():
					t.Fatal("no playback.changed after partial_failure")
				}
			})
		}
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
