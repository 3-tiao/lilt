package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

type cleanupPlaybackProvider struct {
	audiusProvider
	resolve    urlResolver
	prepareErr *api.Error
}

func (p cleanupPlaybackProvider) PreparePlayback(context.Context, PlaybackRequest) (PreparedPlayback, *api.Error) {
	if p.prepareErr != nil {
		return nil, p.prepareErr
	}
	return NewURLQueuePlan(api.SourceAudius, urlSkipItems("replacement"), 0, p.resolve), nil
}

type cleanupURLDriver struct {
	recordingURLDriver
	lateStart   bool
	failCleanup bool
	cleanupLive bool
	cleanupTime time.Duration
	cleanupID   string
	cleanups    int
}

func (d *cleanupURLDriver) PlayURL(ctx context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	if d.lateStart && d.count() > 0 {
		<-ctx.Done()
	}
	return d.recordingURLDriver.PlayURL(ctx, target)
}

func (d *cleanupURLDriver) StopURL(ctx context.Context, generation uint64, session string) (core.PlaybackState, error) {
	d.cleanups++
	d.cleanupLive = ctx.Err() == nil
	if deadline, ok := ctx.Deadline(); ok {
		d.cleanupTime = time.Until(deadline)
	}
	d.cleanupID = session
	if err := ctx.Err(); err != nil {
		return core.PlaybackState{}, err
	}
	// A session-bound driver rejects stopping a different target. Clearing
	// the queue must not erase the identity of the audio that needs cleanup.
	last := d.last()
	if generation != last.PlaybackGeneration || session != last.TransportSessionID {
		return core.PlaybackState{}, errors.New("fixture cleanup identity mismatch")
	}
	if d.failCleanup {
		return core.PlaybackState{}, errors.New("fixture cleanup refused")
	}
	return d.recordingURLDriver.StopURL(ctx, generation, session)
}

func TestURLPlaybackStartFailureCleansOwnedAudioWithFreshBudget(t *testing.T) {
	for _, command := range []string{"playback.play", "playback.playSongs"} {
		for _, stage := range []string{"late_start", "late_resolve", "resolve_error", "prepare_error", "cleanup_error"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				driver := &cleanupURLDriver{lateStart: stage == "late_start" || stage == "cleanup_error", failCleanup: stage == "cleanup_error"}
				s := newURLTransitionServer(t, driver, func(_ context.Context, item api.Item) (urlResolution, error) {
					return urlResolution{URL: "https://media.invalid/" + item.ProviderID}, nil
				})
				provider := cleanupPlaybackProvider{resolve: func(ctx context.Context, _ api.Item) (urlResolution, error) {
					if stage == "late_resolve" {
						<-ctx.Done()
					}
					if stage == "resolve_error" {
						return urlResolution{}, errors.New("fixture resolution refused")
					}
					return urlResolution{URL: "https://media.invalid/replacement"}, nil
				}}
				if stage == "prepare_error" {
					provider.prepareErr = api.Errorf(api.CodeInvalidReference, "fixture prepare refused")
				}
				s.providers = map[api.SourceID]ContentProvider{api.SourceAudius: provider}
				s.externalURLDriver = true
				handler := s.handlePlay
				params := map[string]any{"ref": "audius:song:replacement"}
				if command == "playback.playSongs" {
					handler = s.handlePlaySongs
					params = map[string]any{"refs": []string{"audius:song:replacement"}}
				}
				// Exercise the real dispatch and handler with a short hermetic
				// execution budget instead of spending the catalog's 60 seconds.
				s.registry.Bind(command, func(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
					ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
					defer cancel()
					return handler(ctx, raw)
				})
				raw, _ := json.Marshal(params)
				response := s.dispatch(api.Request{RequestID: "start", Command: command, Params: raw})
				wantCode := api.CodePlaybackError
				if stage == "prepare_error" {
					wantCode = api.CodeInvalidReference
				}
				if response.Error == nil || response.Error.Code != wantCode {
					t.Fatalf("start response = %+v, want %s", response, wantCode)
				}
				if got := response.Error.Details["cleanupFailed"]; (got == true) != driver.failCleanup {
					t.Errorf("cleanupFailed=%v, want %t", got, driver.failCleanup)
				}
				beforeStarts := driver.count()
				duplicate := s.dispatch(api.Request{RequestID: "start", Command: command, Params: raw})
				if duplicate.Error == nil || duplicate.Error.Code != wantCode || driver.count() != beforeStarts {
					t.Fatalf("failed-request replay restarted media: response=%+v starts=%d", duplicate, driver.count())
				}
				if driver.cleanups != 1 || !driver.cleanupLive || driver.cleanupTime <= 0 || driver.cleanupTime > s.registry.Timeout("playback.stop") {
					t.Errorf("cleanup calls=%d live=%t budget=%s; want one independent bounded stop", driver.cleanups, driver.cleanupLive, driver.cleanupTime)
				}
				last := driver.last()
				if driver.cleanupID != last.TransportSessionID {
					t.Errorf("cleanup session=%q, last started session=%q", driver.cleanupID, last.TransportSessionID)
				}
				backend, _ := driver.StateURL(context.Background(), 0, "")
				wantBackend := "stopped"
				if driver.failCleanup {
					wantBackend = "playing"
				}
				if backend.Status != wantBackend {
					t.Errorf("backend=%q, want %s", backend.Status, wantBackend)
				}
				if queue := s.urlTransport.List(); queue.Source != nil || len(queue.Items) != 0 {
					t.Errorf("failed queue=%+v, want empty", queue)
				}
				status := s.dispatch(api.Request{RequestID: "status", Command: "session.status"})
				var projected api.PlaybackStatus
				if err := json.Unmarshal(status.Data, &projected); err != nil || !status.OK || projected.Status != "stopped" || projected.Track != nil {
					t.Fatalf("status=%s error=%+v decode=%v", status.Data, status.Error, err)
				}
			})
		}
	}
}

func TestManualURLTransitionFailureSharesIndependentCleanup(t *testing.T) {
	for _, command := range []string{"playback.next", "playback.previous", "queue.jump", "queue.remove"} {
		t.Run(command, func(t *testing.T) {
			driver := &cleanupURLDriver{}
			s := newURLTransitionServer(t, driver, func(_ context.Context, item api.Item) (urlResolution, error) {
				return urlResolution{URL: "https://media.invalid/" + item.ProviderID}, nil
			})
			if command == "playback.previous" {
				if _, err := s.urlTransport.Next(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			driver.lateStart = true
			var handler api.Handler
			var params json.RawMessage
			switch command {
			case "playback.next":
				handler = s.handleTransportControl("next")
			case "playback.previous":
				handler = s.handleTransportControl("previous")
			case "queue.jump":
				handler = s.handleQueueJump
				params = json.RawMessage(`{"index":1}`)
			case "queue.remove":
				handler = s.handleQueueRemove
				params = json.RawMessage(`{"index":0}`)
			}
			s.registry.Bind(command, func(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
				ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				return handler(ctx, raw)
			})
			response := s.dispatch(api.Request{RequestID: "control", Command: command, Params: params})
			if response.Error == nil || response.Error.Code != api.CodeSourceUnavailable {
				t.Fatalf("transition response=%+v, want source_unavailable", response)
			}
			backend, _ := driver.StateURL(context.Background(), 1, "session")
			if driver.cleanups != 1 || !driver.cleanupLive || driver.cleanupTime <= 0 || backend.Status != "stopped" || len(s.urlTransport.List().Items) != 0 {
				t.Fatalf("cleanup calls=%d live=%t budget=%s backend=%s queue=%+v", driver.cleanups, driver.cleanupLive, driver.cleanupTime, backend.Status, s.urlTransport.List())
			}
		})
	}
}
