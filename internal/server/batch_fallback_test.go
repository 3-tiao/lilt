package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/player"
)

type failedBatchEngine struct {
	*albumSpyEngine
	batchError  error
	singleError error
	assigned    bool
}

func (e *failedBatchEngine) PlaySongs(ctx context.Context, request core.PlaySongsRequest) (core.PlaybackState, error) {
	e.mu.Lock()
	e.playSongsRequests = append(e.playSongsRequests, request)
	e.mu.Unlock()
	if e.assigned {
		// Model a lost response after assignment: the caller cannot know
		// whether the helper already applied the requested queue.
		_, _ = e.FakeEngine.PlaySongs(ctx, request)
	}
	return core.PlaybackState{}, e.batchError
}

func (e *failedBatchEngine) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	if e.singleError == nil {
		return e.albumSpyEngine.PlayState(ctx, request)
	}
	e.mu.Lock()
	e.plays = append(e.plays, request)
	e.mu.Unlock()
	return core.PlaybackState{}, e.singleError
}

func TestCancelledPrepareRefusalNeverStartsFallback(t *testing.T) {
	engine := newAlbumEngine(3)
	refusal := &player.RPCError{Code: player.CodeQueuePrepareRejected, Message: "fixture batch prepare refused"}
	engine.FailPlaySongs(refusal)
	server := &Server{engine: engine}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := server.startFiniteQueueLocked(ctx,
		[]string{"apple-music:song:t1", "apple-music:song:t2", "apple-music:song:t3"}, []string{"t1", "t2", "t3"}, 1)
	if !errors.Is(err, refusal) {
		t.Fatalf("cancelled prepare error = %v, want the original refusal", err)
	}
	if plays, enqueues := engine.calls(); len(plays) != 0 || len(enqueues) != 0 {
		t.Fatalf("cancelled request started fallback: plays=%+v enqueues=%+v", plays, enqueues)
	}
}

func TestFiniteQueueFallbackOnlyForExplicitPrepareRefusal(t *testing.T) {
	for _, command := range []string{"playback.play", "playback.playSongs"} {
		for _, tc := range []struct {
			name        string
			err         error
			code        string
			assigned    bool
			singleError error
		}{
			{"prepare_refusal", &player.RPCError{Code: player.CodeQueuePrepareRejected, Message: "fixture batch prepare refused"}, "", false, nil},
			{"transport_timeout", &player.TransportError{Err: context.DeadlineExceeded}, api.CodeOperationOutcomeUnknown, false, nil},
			{"wrapped_transport", fmt.Errorf("fixture batch RPC: %w", &player.TransportError{Err: io.EOF}), api.CodeOperationOutcomeUnknown, false, nil},
			{"response_lost_after_assignment", &player.TransportError{Err: io.EOF}, api.CodeOperationOutcomeUnknown, true, nil},
			{"dead_client", &player.TransportError{Err: io.EOF}, api.CodeOperationOutcomeUnknown, false, &player.RPCError{Code: api.CodePlaybackError, Message: "fixture client already invalid"}},
			{"start_not_confirmed", &player.RPCError{Code: api.CodePlaybackError, Message: "fixture start not confirmed"}, api.CodePlaybackError, false, nil},
			{"authorization", &player.RPCError{Code: api.CodeAuthorizationRequired, Message: "fixture authorization denied"}, api.CodeAuthorizationRequired, false, nil},
			{"invalid_reference", &player.RPCError{Code: api.CodeInvalidReference, Message: "fixture song unresolved"}, api.CodeInvalidReference, false, nil},
			{"generic_music_error", &player.RPCError{Code: "music_error", Message: "fixture unrelated MusicKit failure"}, api.CodePlaybackError, false, nil},
			{"untyped_error", errors.New("fixture unclassified failure"), api.CodePlaybackError, false, nil},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				engine := &failedBatchEngine{albumSpyEngine: newAlbumEngine(3), batchError: tc.err, assigned: tc.assigned, singleError: tc.singleError}
				server, socket := startTestServerWithEngine(t, engine)
				server.mu.Lock()
				server.queuePacing = time.Millisecond
				server.mu.Unlock()
				params := map[string]any{"ref": "apple-music:album:al1", "startAt": 1}
				if command == "playback.playSongs" {
					params = map[string]any{"refs": []string{"apple-music:song:t1", "apple-music:song:t2", "apple-music:song:t3"}, "startIndex": 1}
				}
				response := call(t, socket, command, params)
				batches := engine.playSongs()
				if len(batches) != 1 || fmt.Sprint(batches[0].IDs) != "[t1 t2 t3]" || batches[0].StartAt != 1 {
					t.Fatalf("one-shot request = %+v", batches)
				}
				plays, enqueues := engine.calls()
				if tc.code == "" {
					if !response.OK || len(plays) != 1 || plays[0].ID != "t2" || len(enqueues) != 2 || enqueues[0].ID != "t1" || enqueues[1].ID != "t3" {
						t.Fatalf("explicit refusal fallback: response=%+v plays=%+v enqueues=%+v", response.Error, plays, enqueues)
					}
					return
				}
				if response.OK || response.Error == nil || response.Error.Code != tc.code {
					t.Errorf("failed batch response: ok=%v error=%+v, want %s", response.OK, response.Error, tc.code)
				}
				if len(plays) != 0 || len(enqueues) != 0 {
					t.Errorf("failed/unknown batch triggered hidden starts: plays=%+v enqueues=%+v", plays, enqueues)
				}
				for _, described := range server.registry.Describe().Commands {
					if described.Name == command && !slices.Contains(described.Errors, tc.code) {
						t.Errorf("api.describe %s errors=%v, missing actual %s", command, described.Errors, tc.code)
					}
				}
				if response.Error != nil {
					var details struct {
						State api.PlaybackState `json:"state"`
					}
					raw, err := json.Marshal(response.Error.Details)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(raw, &details); err != nil || details.State.Status != "stopped" || details.State.Track != nil {
						t.Errorf("failure state = %s, err = %v; want stopped/no track", raw, err)
					}
				}
			})
		}
	}
}
