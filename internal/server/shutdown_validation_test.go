package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
)

// shutdownProbeEngine models a helper whose Close stops playback and records
// calls. Both roles may share the fake, so repeated closes remain harmless.
type shutdownProbeEngine struct {
	*fakeengine.FakeEngine
	closes atomic.Int32
}

func (e *shutdownProbeEngine) Close() error {
	e.closes.Add(1)
	_, _ = e.FakeEngine.Stop(context.Background())
	return nil
}

// shutdownWireRequest uses a raw request so missing/colliding request IDs can
// reach the real socket handler. Waiting for EOF also waits for its post-reply
// finishShutdown branch, without sleeps or assumptions about goroutine timing.
func shutdownWireRequest(socket string, request api.Request) (api.Response, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return api.Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return api.Response{}, err
	}
	var response api.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return api.Response{}, err
	}
	_, err = io.Copy(io.Discard, conn)
	return response, err
}

func drainShutdownEvents(watch *watchClient) int {
	count := 0
	for {
		select {
		case event := <-watch.events:
			if event.Event == "server.shuttingDown" {
				count++
			}
		default:
			return count
		}
	}
}

func TestAcceptedShutdownKeepsOrderingAndDeduplicates(t *testing.T) {
	for _, params := range []json.RawMessage{nil, json.RawMessage(`{}`)} {
		name := "omitted_params"
		if params != nil {
			name = "empty_object"
		}
		t.Run(name, func(t *testing.T) {
			engine := &shutdownProbeEngine{FakeEngine: fakeengine.NewFakeEngine()}
			server, socket := startTestServerWithEngine(t, engine)
			if played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:one"}); !played.OK {
				t.Fatalf("fixture play: %+v", played.Error)
			}
			closesBefore := engine.closes.Load()
			watch := server.watchers.register(map[string]bool{})
			t.Cleanup(func() { server.watchers.unregister(watch) })
			request := api.Request{RequestID: "accepted-shutdown", Command: "session.shutdown", Params: params}
			response, err := shutdownWireRequest(socket, request)
			if err != nil || !response.OK || string(response.Data) != `{}` {
				t.Fatalf("accepted shutdown reply: response=%+v err=%v", response, err)
			}
			server.mu.Lock()
			draining, stopped, published, attached := server.draining, server.engineStopped, server.shutdownPublished, server.engine != nil
			server.mu.Unlock()
			if !draining || !stopped || !published || attached || engine.closes.Load() <= closesBefore {
				t.Fatalf("accepted shutdown did not release playback: draining=%v engineStopped=%v published=%v attached=%v closes=%d->%d", draining, stopped, published, attached, closesBefore, engine.closes.Load())
			}
			if private, err := engine.State(context.Background()); err != nil || private.Status != "stopped" {
				t.Fatalf("accepted shutdown did not stop fake audio: status=%s err=%v", private.Status, err)
			}
			select {
			case <-server.ShutdownRequested():
			default:
				t.Fatal("accepted shutdown did not signal the serve loop after its reply")
			}
			if count := drainShutdownEvents(watch); count != 1 {
				t.Fatalf("shutdown events = %d, want one", count)
			}
			closesAfter := engine.closes.Load()
			// Omission and {} mean the same valid no-argument request. A retry
			// must reuse its result without closing/publishing a second time.
			request.Params = json.RawMessage(`{}`)
			if params != nil {
				request.Params = nil
			}
			repeated, err := shutdownWireRequest(socket, request)
			if err != nil || !repeated.OK || string(repeated.Data) != string(response.Data) || repeated.RequestID != response.RequestID {
				t.Fatalf("shutdown retry: response=%+v err=%v", repeated, err)
			}
			if engine.closes.Load() != closesAfter || drainShutdownEvents(watch) != 0 {
				t.Fatal("shutdown retry repeated playback teardown or its event")
			}
		})
	}
}

func TestRejectedShutdownLeavesPlaybackAndServerRunning(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{"missing_request_id", api.CodeInvalidRequest},
		{"unknown_params", api.CodeInvalidRequest},
		{"array_params", api.CodeInvalidRequest},
		{"null_params", api.CodeInvalidRequest},
		{"completed_id_conflict", api.CodeInvalidRequest},
		{"inflight_id_conflict", api.CodeInvalidRequest},
		{"handler_panic", api.CodeInternalError},
		{"cached_failure", api.CodeInternalError},
		{"evicted_failure", api.CodeDuplicateResultUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &shutdownProbeEngine{FakeEngine: fakeengine.NewFakeEngine()}
			server, socket := startTestServerWithEngine(t, engine)
			played := call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:one"})
			if !played.OK {
				t.Fatalf("fixture play: %+v", played.Error)
			}
			var before api.PlaybackState
			if err := json.Unmarshal(played.Data, &before); err != nil || before.Status != "playing" || before.Track == nil {
				t.Fatalf("fixture state = %s, err = %v", played.Data, err)
			}
			closesBefore := engine.closes.Load()
			flow, flowErr := server.authFlows.begin(api.SourceAppleMusic)
			if flowErr != nil {
				t.Fatal(flowErr)
			}
			flowCtx, flowCancel := context.WithCancel(context.Background())
			server.authFlows.setCancel(flow.FlowID, flowCancel)
			t.Cleanup(flowCancel)
			watch := server.watchers.register(map[string]bool{})
			t.Cleanup(func() { server.watchers.unregister(watch) })
			request := api.Request{RequestID: "shutdown-test", Command: "session.shutdown"}
			switch tc.name {
			case "missing_request_id":
				request.RequestID = ""
			case "unknown_params":
				request.Params = json.RawMessage(`{"unexpected":true}`)
			case "array_params":
				request.Params = json.RawMessage(`[]`)
			case "null_params":
				request.Params = json.RawMessage(`null`)
			case "completed_id_conflict":
				response, err := shutdownWireRequest(socket, api.Request{RequestID: request.RequestID, Command: "api.describe"})
				if err != nil || !response.OK {
					t.Fatalf("seed completed request: response=%+v err=%v", response.Error, err)
				}
			case "inflight_id_conflict":
				entered, release := make(chan struct{}), make(chan struct{})
				server.registry.Bind("api.describe", func(context.Context, json.RawMessage) (any, *api.Error) {
					close(entered)
					<-release
					return map[string]any{}, nil
				})
				done := make(chan error, 1)
				go func() {
					_, err := shutdownWireRequest(socket, api.Request{RequestID: request.RequestID, Command: "api.describe"})
					done <- err
				}()
				t.Cleanup(func() {
					close(release)
					select {
					case err := <-done:
						if err != nil {
							t.Errorf("inflight fixture: %v", err)
						}
					case <-time.After(3 * time.Second):
						t.Error("inflight fixture did not complete")
					}
				})
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("inflight fixture never entered its handler")
				}
			case "handler_panic":
				server.registry.Bind("session.shutdown", func(context.Context, json.RawMessage) (any, *api.Error) {
					panic("fixture shutdown panic")
				})
			case "cached_failure", "evicted_failure":
				if tc.name == "evicted_failure" {
					server.dedup = newDedupCache(1, 0)
				}
				original, handlerErr := server.registry.Handler("session.shutdown")
				if handlerErr != nil {
					t.Fatal(handlerErr)
				}
				server.registry.Bind("session.shutdown", func(context.Context, json.RawMessage) (any, *api.Error) {
					return nil, api.Errorf(api.CodeInternalError, "fixture shutdown rejected")
				})
				// Seed a failed outcome without entering the socket prologue.
				if response := server.guardedDispatch(request); response.Error == nil || response.Error.Code != api.CodeInternalError {
					t.Fatalf("seed failed request: %+v", response.Error)
				}
				server.registry.Bind("session.shutdown", original)
				if tc.name == "evicted_failure" {
					if response := call(t, socket, "api.describe", nil); !response.OK {
						t.Fatalf("evict failed result: %+v", response.Error)
					}
				}
			}

			response, err := shutdownWireRequest(socket, request)
			if err != nil {
				t.Fatalf("shutdown wire request: %v", err)
			}
			if response.OK || response.Error == nil || response.Error.Code != tc.code {
				t.Errorf("shutdown response = %+v, want %s", response, tc.code)
			}
			server.mu.Lock()
			draining, stopped, published, attached := server.draining, server.engineStopped, server.shutdownPublished, server.engine == engine
			server.mu.Unlock()
			if draining || stopped || published || !attached || engine.closes.Load() != closesBefore {
				t.Errorf("rejected shutdown changed lifecycle: draining=%v engineStopped=%v published=%v attached=%v closes=%d->%d", draining, stopped, published, attached, closesBefore, engine.closes.Load())
			}
			if private, err := engine.State(context.Background()); err != nil || private.Status != "playing" {
				t.Errorf("rejected shutdown stopped the fake helper itself: status=%s err=%v", private.Status, err)
			}
			select {
			case <-server.ShutdownRequested():
				t.Error("rejected shutdown signalled the serve loop")
			default:
			}
			if drainShutdownEvents(watch) != 0 {
				t.Error("rejected shutdown published server.shuttingDown")
			}
			if flowCtx.Err() != nil || !server.authFlows.isPending(flow.FlowID) {
				t.Error("rejected shutdown cancelled a pending authorization flow")
			}
			status := call(t, socket, "session.status", map[string]any{"includeQueue": true})
			var after api.PlaybackState
			if err := json.Unmarshal(status.Data, &after); err != nil || !status.OK || after.Status != "playing" || after.Track == nil || after.Track.Ref != before.Track.Ref {
				t.Errorf("rejected shutdown interrupted playback: response=%+v state=%s err=%v", status.Error, status.Data, err)
			}
			if paused := call(t, socket, "playback.pause", nil); !paused.OK {
				t.Errorf("rejected shutdown disabled playback control: %+v", paused.Error)
			}
		})
	}
}
