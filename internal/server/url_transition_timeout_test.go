package server

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/player"
)

type failingStopURLDriver struct {
	recordingURLDriver
	err error
}

func (d *failingStopURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{}, d.err
}

func TestStopCatalogDeclaresActualBackendFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"media", errors.New("fixture stop failure"), api.CodePlaybackError},
		{"source", api.Errorf(api.CodeSourceUnavailable, "fixture source unavailable"), api.CodeSourceUnavailable},
		{"transport", &player.TransportError{Err: context.DeadlineExceeded}, api.CodeOperationOutcomeUnknown},
		{"restarting", nil, api.CodeEngineRestarting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newURLTransitionServer(t, &failingStopURLDriver{err: tc.err}, func(context.Context, api.Item) (urlResolution, error) {
				return urlResolution{URL: "https://media.invalid/one"}, nil
			})
			if tc.name == "restarting" {
				s.activeTransport = transportEngine
				s.engineRestarting = true
			}
			response := s.dispatch(api.Request{RequestID: "stop", Command: "playback.stop"})
			if response.Error == nil || response.Error.Code != tc.want {
				t.Fatalf("stop error = %+v, want %s", response.Error, tc.want)
			}
			for _, command := range s.registry.Describe().Commands {
				if command.Name == "playback.stop" && !slices.Contains(command.Errors, response.Error.Code) {
					t.Fatalf("stop returned %s, but api.describe declares %v", response.Error.Code, command.Errors)
				}
			}
		})
	}
}

// newURLTransitionServer exercises the real supervisor, transport and command
// dispatch without a socket, network, account or playback helper.
func newURLTransitionServer(t *testing.T, driver URLPlaybackDriver, resolve urlResolver) *Server {
	t.Helper()
	items := []api.Item{
		{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "one", Ref: "audius:song:one", Title: "One"},
		{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "two", Ref: "audius:song:two", Title: "Two"},
	}
	transport := NewURLQueueTransport(driver)
	if _, err := transport.Start(context.Background(), NewURLQueuePlan(api.SourceAudius, items, 0, resolve), 1, "session"); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		registry: api.NewRegistry(), dedup: newDedupCache(0, 0, 0), admission: newAdmissionGate(), closed: make(chan struct{}),
		urlTransport: transport, activeSource: api.SourceAudius, activeTransport: transportURLQueue,
		playbackGeneration: 1, transportSessionID: "session", logf: func(string, map[string]any) {},
	}
	s.registry.Bind("playback.stop", s.handleStop)
	s.registry.Bind("session.status", s.handleStatus)
	return s
}

func runURLTransition(s *Server, ended bool) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		update := core.PlaybackState{PlaybackGeneration: 1, TransportSessionID: "session", Ended: ended}
		if !ended {
			update.Error = "fixture media failure"
		}
		s.applyEngineUpdate(core.PlaybackStateUpdate{State: update}, nil, nil)
		close(done)
	}()
	return done
}

func waitURLTransition[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("automatic URL transition did not release the control channel")
		var zero T
		return zero
	}
}

func TestAutomaticURLTransitionsHaveExecutionDeadline(t *testing.T) {
	for _, ended := range []bool{true, false} {
		name := "media_failure"
		if ended {
			name = "natural_end"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			calls := 0
			s := newURLTransitionServer(t, &recordingURLDriver{}, func(ctx context.Context, item api.Item) (urlResolution, error) {
				calls++
				if calls == 1 {
					return urlResolution{URL: "https://media.invalid/" + item.ProviderID}, nil
				}
				entered <- ctx
				<-release
				return urlResolution{}, errors.New("fixture resolver unavailable")
			})
			done := runURLTransition(s, ended)
			t.Cleanup(func() { close(release); waitURLTransition(t, done) })
			ctx := waitURLTransition(t, entered)
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > s.registry.Timeout("playback.next") {
				t.Errorf("automatic resolver deadline = %v, present = %v; want the bounded playback.next budget", deadline, ok)
			}
			// The resolver runs inside the same mutex required by stop/status.
			// Without a deadline it can keep both commands waiting indefinitely.
			if s.mu.TryLock() {
				s.mu.Unlock()
				t.Fatal("fixture did not hold the server's serialized control slot")
			}
		})
	}
}

type timeoutURLDriver struct {
	recordingURLDriver
	blockStart  bool
	lateStart   bool
	failCleanup bool
	entered     chan context.Context
	release     <-chan struct{}
	cleanup     chan context.Context
}

func (d *timeoutURLDriver) PlayURL(ctx context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	if d.blockStart && d.count() > 0 {
		d.entered <- ctx
		select {
		case <-ctx.Done():
			if !d.lateStart {
				return core.PlaybackState{}, ctx.Err()
			}
		case <-d.release:
			return core.PlaybackState{}, errors.New("fixture released")
		}
	}
	return d.recordingURLDriver.PlayURL(ctx, target)
}

func (d *timeoutURLDriver) StopURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if err := ctx.Err(); err != nil {
		return core.PlaybackState{}, err
	}
	d.cleanup <- ctx
	if d.failCleanup {
		return core.PlaybackState{}, errors.New("fixture audio cleanup failed")
	}
	return d.recordingURLDriver.StopURL(ctx, generation, session)
}

func TestAutomaticURLTransitionTimeoutReleasesControls(t *testing.T) {
	for _, ended := range []bool{true, false} {
		name := "media_failure"
		if ended {
			name = "natural_end"
		}
		for _, stage := range []string{"resolve", "late_resolve", "start", "late_start", "cleanup_error"} {
			t.Run(name+"/"+stage, func(t *testing.T) {
				entered := make(chan context.Context, 1)
				release := make(chan struct{})
				driver := &timeoutURLDriver{
					blockStart: stage == "start" || stage == "late_start", lateStart: stage == "late_start",
					failCleanup: stage == "cleanup_error",
					entered:     entered, release: release, cleanup: make(chan context.Context, 4),
				}
				calls := 0
				s := newURLTransitionServer(t, driver, func(ctx context.Context, item api.Item) (urlResolution, error) {
					calls++
					if calls > 1 && !driver.blockStart {
						entered <- ctx
						select {
						case <-ctx.Done():
							if stage != "late_resolve" {
								return urlResolution{}, ctx.Err()
							}
						case <-release:
							return urlResolution{}, errors.New("fixture released")
						}
					}
					return urlResolution{URL: "https://media.invalid/" + item.ProviderID}, nil
				})
				s.urlTransitionBudget = 100 * time.Millisecond
				warnings := make(chan string, 4)
				s.logf = func(kind string, fields map[string]any) {
					if kind == "server.warning" {
						warnings <- fields["code"].(string)
					}
				}
				done := runURLTransition(s, ended)
				t.Cleanup(func() { close(release); waitURLTransition(t, done) })
				ctx := waitURLTransition(t, entered)
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("blocked automatic operation has no deadline")
				}
				// Queue both real handlers while the automatic operation owns
				// the control slot. Neither needs its own timeout to expire.
				responses := make(chan api.Response, 2)
				for _, command := range []string{"playback.stop", "session.status"} {
					go func(command string) {
						responses <- s.dispatch(api.Request{RequestID: command, Command: command})
					}(command)
				}
				waitURLTransition(t, done)
				for range 2 {
					response := waitURLTransition(t, responses)
					if !response.OK {
						t.Fatalf("queued control failed: %+v", response.Error)
					}
					var state api.PlaybackStatus
					if err := json.Unmarshal(response.Data, &state); err != nil || state.Status != "stopped" || state.Track != nil {
						t.Fatalf("queued control state = %s, err = %v; want stopped with no track", response.Data, err)
					}
				}
				cleanup := waitURLTransition(t, driver.cleanup)
				deadline, ok := cleanup.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > s.registry.Timeout("playback.stop") {
					t.Fatalf("audio cleanup did not have a fresh bounded context: %v", deadline)
				}
				wantStops := 1
				if stage == "cleanup_error" {
					wantStops = 0 // Public stopped is not proof that a failed backend is silent.
				}
				if driver.stopCount() != wantStops {
					t.Fatalf("audio stops = %d, want %d", driver.stopCount(), wantStops)
				}
				wantStarts := 1
				if stage == "late_start" {
					wantStarts = 2 // A late driver result is cleaned up, not committed.
				}
				if driver.count() != wantStarts || len(s.urlTransport.List().Items) != 0 {
					t.Fatalf("starts = %d, queue = %+v; late result must not survive", driver.count(), s.urlTransport.List())
				}
				if !ended {
					if code := waitURLTransition(t, warnings); code != api.CodePlaybackStalled {
						t.Fatalf("retry journal code = %s", code)
					}
				}
				if code := waitURLTransition(t, warnings); code != api.CodeSourceUnavailable {
					t.Fatalf("terminal warning code = %s", code)
				}
			})
		}
	}
}
