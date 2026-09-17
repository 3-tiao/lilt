package server

import (
	"context"
	"encoding/json"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

type playParams struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Shuffle *bool  `json:"shuffle"`
	Repeat  string `json:"repeat"`
}

func (s *Server) handlePlay(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params playParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	var state core.PlaybackState
	var err error
	queueChanged := true
	radioStream := false
	switch {
	case reference.Source == api.SourceRadio && reference.Kind == api.KindStream:
		state, err = s.engine.RadioPlay(ctx, reference.URL, params.Name)
		queueChanged = false
		radioStream = true
	default:
		state, err = s.engine.PlayState(ctx, core.PlaybackRequest{Kind: reference.Kind, ID: reference.ID, URL: reference.URL})
	}
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	if radioStream {
		s.startICY(reference.URL)
	} else {
		s.stopICY()
	}
	applied, apiErr := s.applyFormLocked(ctx, params.Shuffle, params.Repeat)
	if apiErr != nil {
		projected := s.commitPlaybackLocked(state, queueChanged)
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	state = appliedState(applied, state)
	projected := s.commitPlaybackLocked(state, queueChanged)
	s.recordAfterPlayLocked(reference, state)
	return projected, nil
}

type playSongsParams struct {
	Refs       []string `json:"refs"`
	StartIndex int      `json:"startIndex"`
	Shuffle    *bool    `json:"shuffle"`
	Repeat     string   `json:"repeat"`
}

func (s *Server) handlePlaySongs(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params playSongsParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if len(params.Refs) == 0 {
		return nil, api.Errorf(api.CodeInvalidRequest, "playSongs requires at least one ref")
	}
	var source api.SourceID
	ids := make([]string, 0, len(params.Refs))
	for _, raw := range params.Refs {
		reference, refErr := api.ParseReference(raw)
		if refErr != nil {
			return nil, refErr
		}
		if reference.Source == api.SourceRadio {
			return nil, api.Errorf(api.CodeSourceMismatch, "radio has no finite queue")
		}
		if source == "" {
			source = reference.Source
		} else if reference.Source != source {
			return nil, api.Errorf(api.CodeSourceMismatch, "refs span multiple sources")
		}
		ids = append(ids, reference.ID)
	}
	state, err := s.engine.PlaySongs(ctx, ids, params.StartIndex)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	s.stopICY()
	applied, apiErr := s.applyFormLocked(ctx, params.Shuffle, params.Repeat)
	if apiErr != nil {
		projected := s.commitPlaybackLocked(state, true)
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	state = appliedState(applied, state)
	projected := s.commitPlaybackLocked(state, true)
	return projected, nil
}

// applyFormLocked applies optional shuffle/repeat, returning what succeeded.
func (s *Server) applyFormLocked(ctx context.Context, shuffle *bool, repeat string) (map[string]any, *api.Error) {
	applied := map[string]any{}
	if shuffle != nil {
		if _, err := s.engine.SetShuffle(ctx, *shuffle); err != nil {
			return applied, s.mapEngineError(err)
		}
		applied["shuffle"] = *shuffle
	}
	if repeat != "" {
		if _, err := s.engine.SetRepeat(ctx, repeat); err != nil {
			return applied, s.mapEngineError(err)
		}
		applied["repeat"] = repeat
	}
	return applied, nil
}

func appliedState(applied map[string]any, fallback core.PlaybackState) core.PlaybackState {
	state := fallback
	if value, ok := applied["shuffle"].(bool); ok {
		state.Shuffle = value
	}
	if value, ok := applied["repeat"].(string); ok {
		state.Repeat = value
	}
	return state
}

func (s *Server) handleControl(call func(context.Context) (core.PlaybackState, error)) api.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
		state, err := call(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, false), nil
	}
}

func (s *Server) handleStop(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.Stop(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	s.stopICY()
	return s.commitPlaybackLocked(state, true), nil
}

func (s *Server) handleToggle(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	current, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	var next core.PlaybackState
	switch current.Status {
	case "playing", "buffering":
		next, err = s.engine.PauseState(ctx)
	case "paused":
		next, err = s.engine.ResumeState(ctx)
	default:
		return nil, api.Errorf(api.CodeInvalidState, "nothing is playing to toggle")
	}
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(next, false), nil
}

func (s *Server) handleSetShuffle(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		On bool `json:"on"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.SetShuffle(ctx, params.On)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, false), nil
}

func (s *Server) handleSetRepeat(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Mode string `json:"mode"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Mode != "off" && params.Mode != "all" && params.Mode != "one" {
		return nil, api.Errorf(api.CodeInvalidRequest, "repeat mode must be off, all, or one")
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.SetRepeat(ctx, params.Mode)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, false), nil
}

func (s *Server) handleQueueList(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.queueState(state), nil
}

type queueAddParams struct {
	Ref             string  `json:"ref"`
	Position        string  `json:"position"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueAdd(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params queueAddParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if params.Position != "next" && params.Position != "append" {
		return nil, api.Errorf(api.CodeInvalidRequest, "position must be next or append")
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	if reference.Source == api.SourceRadio {
		return nil, api.Errorf(api.CodeQueueUnavailable, "radio streams have no editable queue")
	}
	current, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	if len(current.Queue) > 0 {
		activeSource := sourceForState(current)
		if activeSource != reference.Source {
			return nil, api.Errorf(api.CodeSourceMismatch, "the active queue belongs to %s", activeSource)
		}
	}
	state, err := s.engine.Enqueue(ctx, core.PlaybackRequest{Kind: reference.Kind, ID: reference.ID, URL: reference.URL}, params.Position)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}

type queueIndexParams struct {
	Index           int     `json:"index"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueJump(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	return s.queueIndexOp(ctx, raw, false, func(index int) (core.PlaybackState, error) {
		return s.engine.QueueJump(ctx, index)
	})
}

func (s *Server) handleQueueRemove(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	return s.queueIndexOp(ctx, raw, true, func(index int) (core.PlaybackState, error) {
		return s.engine.QueueRemove(ctx, index)
	})
}

func (s *Server) queueIndexOp(ctx context.Context, raw json.RawMessage, queueChanged bool, call func(int) (core.PlaybackState, error)) (any, *api.Error) {
	var params queueIndexParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := call(params.Index)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, queueChanged), nil
}

type queueMoveParams struct {
	From            int     `json:"from"`
	To              int     `json:"to"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueMove(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params queueMoveParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := s.engine.QueueMove(ctx, params.From, params.To)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}

func (s *Server) handleQueueClear(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		IfQueueRevision *uint64 `json:"ifQueueRevision"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := s.engine.QueueClear(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}
