package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

// Check the driver as well as the projection: reporting paused while the
// playback backend keeps playing does not preserve the user's intent.
func TestURLQueueTransitionsPreservePlaybackIntent(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "playing"
		if paused {
			name = "paused"
		}
		t.Run(name, func(t *testing.T) {
			for _, operation := range []struct {
				name      string
				prepare   func(*URLQueueTransport) error
				run       func(*URLQueueTransport) (core.PlaybackState, error)
				wantID    string
				wantIndex int
				wantErr   error
			}{
				{"retry", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.RetryCurrent(context.Background())
				}, "2", 1, nil},
				{"next", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.Next(context.Background())
				}, "3", 2, nil},
				{"previous", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.Previous(context.Background())
				}, "1", 0, nil},
				{"jump", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.Jump(context.Background(), 0)
				}, "1", 0, nil},
				{"remove_current", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					outcome, _, err := tr.Remove(context.Background(), 1)
					return outcome.State, err
				}, "3", 1, nil},
				{"natural_end", nil, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.AdvanceEnded(context.Background())
				}, "3", 2, nil},
				{"dead_item_skip", func(tr *URLQueueTransport) error {
					_, err := tr.RetryCurrent(context.Background())
					return err
				}, func(tr *URLQueueTransport) (core.PlaybackState, error) {
					return tr.RetryCurrent(context.Background())
				}, "3", 2, errDeadItemSkipped},
			} {
				t.Run(operation.name, func(t *testing.T) {
					driver := &recordingURLDriver{}
					tr := NewURLQueueTransport(driver)
					var resolved []string
					if _, err := tr.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2", "3"), 1, &resolved), 7, "session"); err != nil {
						t.Fatal(err)
					}
					if operation.prepare != nil {
						if err := operation.prepare(tr); err != nil {
							t.Fatal(err)
						}
					}
					if paused {
						if _, err := tr.Pause(context.Background()); err != nil {
							t.Fatal(err)
						}
					}
					state, err := operation.run(tr)
					if !errors.Is(err, operation.wantErr) {
						t.Fatalf("transition error = %v, want %v", err, operation.wantErr)
					}
					backend, err := driver.StateURL(context.Background(), 7, "session")
					if err != nil {
						t.Fatal(err)
					}
					if state.Status != name || backend.Status != name {
						t.Errorf("transition status = %q, backend = %q, want both %q", state.Status, backend.Status, name)
					}
					if state.Track == nil || state.Track.ID != operation.wantID || state.QueueIndex != operation.wantIndex {
						t.Fatalf("transition state = %+v, want item %s at %d", state, operation.wantID, operation.wantIndex)
					}
					target := driver.last()
					if target.PlaybackGeneration != 7 || target.TransportSessionID != "session" || target.Item.ID != operation.wantID {
						t.Fatalf("target = %+v", target)
					}
					polled, err := tr.State(context.Background())
					if err != nil || polled.Status != name {
						t.Errorf("polled state = %+v, err = %v, want %s", polled, err, name)
					}
					assertNoSignedURL(t, state)
				})
			}
		})
	}
}

func TestURLQueueResumeAndFreshStartReplacePauseIntent(t *testing.T) {
	driver := &recordingURLDriver{}
	tr := NewURLQueueTransport(driver)
	var resolved []string
	plan := urlSkipPlan(urlSkipItems("1", "2"), 0, &resolved)
	if _, err := tr.Start(context.Background(), plan, 7, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := tr.RetryCurrent(context.Background())
	if err != nil || state.Status != "playing" {
		t.Fatalf("retry after resume = %+v, %v, want playing", state, err)
	}
	if _, err := tr.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = tr.Start(context.Background(), plan, 8, "replacement")
	backend, _ := driver.StateURL(context.Background(), 8, "replacement")
	if err != nil || state.Status != "playing" || backend.Status != "playing" {
		t.Fatalf("fresh start = %+v, %v, backend = %q, must not inherit pause", state, err, backend.Status)
	}
}

func TestAutomaticURLRetryPublishesPauseOutcome(t *testing.T) {
	for _, failPause := range []bool{false, true} {
		name, wantStatus := "restored", "paused"
		if failPause {
			name, wantStatus = "refused", "stopped"
		}
		t.Run(name, func(t *testing.T) {
			driver := &rejectedPauseURLDriver{}
			s := newURLTransitionServer(t, driver, func(_ context.Context, item api.Item) (urlResolution, error) {
				return urlResolution{URL: "https://media.invalid/" + item.ProviderID}, nil
			})
			s.registry.Bind("playback.pause", s.handleTransportControl("pause"))
			if response := s.dispatch(api.Request{RequestID: "pause", Command: "playback.pause"}); !response.OK {
				t.Fatalf("pause = %+v", response.Error)
			}
			if failPause {
				driver.pauseErr = errors.New("fixture pause refused")
			}
			s.watchers = newWatchHub()
			watcher := s.watchers.register(nil)
			defer s.watchers.unregister(watcher)
			s.applyEngineUpdate(core.PlaybackStateUpdate{State: core.PlaybackState{
				PlaybackGeneration: 1, TransportSessionID: "session", Error: "fixture media failure",
			}}, nil, nil)
			event := waitURLTransition(t, watcher.events)
			var payload struct {
				State api.PlaybackState `json:"state"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if event.Event != "playback.changed" || payload.State.Status != wantStatus {
				t.Fatalf("watch event = %+v, state = %+v, want %s", event, payload.State, wantStatus)
			}
			if failPause {
				warning := waitURLTransition(t, watcher.events)
				var data struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(warning.Data, &data); err != nil || warning.Event != "server.warning" || data.Code != api.CodeSourceUnavailable {
					t.Fatalf("warning = %+v, code = %q, err = %v", warning, data.Code, err)
				}
				if payload.State.Track != nil || len(payload.State.Queue) != 0 || driver.stopCount() != 1 {
					t.Fatalf("failed pause state = %+v, stops = %d", payload.State, driver.stopCount())
				}
			} else if payload.State.Track == nil || payload.State.Track.ProviderID != "one" || len(payload.State.Queue) != 2 {
				t.Fatalf("restored pause changed the queue: %+v", payload.State)
			}
			response := s.dispatch(api.Request{RequestID: "status", Command: "session.status"})
			var status api.PlaybackStatus
			if err := json.Unmarshal(response.Data, &status); err != nil || !response.OK || status.Status != wantStatus {
				t.Fatalf("status = %s, error = %+v, decode error = %v", response.Data, response.Error, err)
			}
			backend, _ := driver.StateURL(context.Background(), 1, "session")
			if backend.Status != wantStatus || driver.count() != 2 {
				t.Fatalf("backend status = %q, starts = %d, want %s with one retry", backend.Status, driver.count(), wantStatus)
			}
			select {
			case extra := <-watcher.events:
				t.Fatalf("unexpected intermediate event: %+v", extra)
			default:
			}
		})
	}
}

type rejectedPauseURLDriver struct {
	recordingURLDriver
	pauseErr error
}

func (d *rejectedPauseURLDriver) PauseURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	if d.pauseErr != nil {
		return core.PlaybackState{}, d.pauseErr
	}
	return d.recordingURLDriver.PauseURL(ctx, generation, session)
}

func TestURLQueueRecoveryPauseFailureEndsSession(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*URLQueueTransport) (core.PlaybackState, error)
	}{
		{"retry", func(tr *URLQueueTransport) (core.PlaybackState, error) {
			return tr.RetryCurrent(context.Background())
		}},
		{"remove_current", func(tr *URLQueueTransport) (core.PlaybackState, error) {
			outcome, _, err := tr.Remove(context.Background(), 0)
			return outcome.State, err
		}},
		{"natural_end", func(tr *URLQueueTransport) (core.PlaybackState, error) {
			return tr.AdvanceEnded(context.Background())
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			driver := &rejectedPauseURLDriver{}
			tr := NewURLQueueTransport(driver)
			var resolved []string
			if _, err := tr.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2"), 0, &resolved), 7, "session"); err != nil {
				t.Fatal(err)
			}
			if _, err := tr.Pause(context.Background()); err != nil {
				t.Fatal(err)
			}
			driver.pauseErr = errors.New("fixture pause refused")
			state, err := operation.run(tr)
			if !errors.Is(err, driver.pauseErr) {
				t.Errorf("transition error = %v, want the pause failure", err)
			}
			if state.Status != "stopped" || driver.stopCount() != 0 {
				t.Errorf("state = %+v, stops = %d; transport must invalidate without spending execution context on cleanup", state, driver.stopCount())
			}
			if err := tr.cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			backend, _ := driver.StateURL(context.Background(), 7, "session")
			if backend.Status != "stopped" || driver.stopCount() != 1 {
				t.Errorf("backend = %q, stops = %d; cleanup must stop the owned session", backend.Status, driver.stopCount())
			}
			if queue := tr.List(); queue.Source != nil || len(queue.Items) != 0 || queue.Index != -1 {
				t.Errorf("queue after pause failure = %+v, want empty", queue)
			}
		})
	}
}
