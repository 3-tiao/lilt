package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

type playParams struct {
	Ref          string `json:"ref"`
	Name         string `json:"name"`
	Shuffle      *bool  `json:"shuffle"`
	Repeat       string `json:"repeat"`
	StartAt      int    `json:"startAt"`
	StartTrackID string `json:"startTrackID"`
	Reverse      bool   `json:"reverse"`
}

func (s *Server) handlePlay(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params playParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	// Capability declaration is the routing truth: a source that does not
	// declare playback is unsupported even if an engine is available.
	descriptor, ok := s.descriptorFor(ctx, reference.Source)
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "playback is not available for %s", reference.Source)
	}
	radioStream := reference.Source == api.SourceRadio && reference.Kind == api.KindStream
	if radioStream {
		if !declaresCapability(descriptor, api.CapPlaybackStream) {
			return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support stream playback", reference.Source)
		}
	} else if !declaresCapability(descriptor, api.CapPlaybackFull) && !declaresCapability(descriptor, api.CapPlaybackPreview) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support playback", reference.Source)
	}
	preparer, urlPlayback := s.providers[reference.Source].(PlaybackPreparer)
	if urlPlayback && s.urlTransport == nil {
		return nil, api.Errorf(api.CodeSourceUnavailable, "direct URL playback is unavailable")
	}
	if !urlPlayback {
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
	}
	transport := transportEngine
	if urlPlayback {
		transport = transportURLQueue
	}
	s.beginPlaybackStartLocked(reference.Source, transport)
	if urlPlayback {
		// Switching to the URL transport stops the old engine first, per the
		// stop-before-start source-switch invariant.
		if s.engine != nil {
			_, _ = s.engine.Stop(ctx)
		}
		s.stopICY()
	} else {
		s.stopURLTransportLocked(ctx)
	}
	var state core.PlaybackState
	var err error
	queueChanged := true
	switch {
	case radioStream:
		state, err = s.engine.RadioPlay(ctx, reference.URL, params.Name)
		queueChanged = false
	case urlPlayback:
		plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: []api.Reference{reference}, StartIndex: params.StartAt})
		if prepareErr != nil {
			return nil, s.failPlaybackStartLocked(ctx, prepareErr)
		}
		state, err = s.urlTransport.Start(ctx, plan, s.playbackGeneration, s.transportSessionID)
	default:
		state, err = s.engine.PlayState(ctx, core.PlaybackRequest{
			Kind: reference.Kind, ID: reference.ID, URL: reference.URL,
			StartAt: params.StartAt, StartTrackID: params.StartTrackID, Reverse: params.Reverse,
		})
	}
	if err != nil {
		return nil, s.failPlaybackStartLocked(ctx, err)
	}
	if radioStream {
		s.startICY(reference.URL)
	} else {
		s.stopICY()
	}
	applied, optionsErr := s.applyFormLocked(ctx, params.Shuffle, params.Repeat)
	state = appliedState(applied, state)
	persistErr := s.persistPlaybackSourceLocked(reference.Source)
	projected := s.commitPlaybackLocked(state, queueChanged)
	s.recordAfterPlayLocked(reference, state, params.Name)
	if optionsErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	if persistErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but its source was not saved").
			WithDetails(map[string]any{"state": projected})
	}
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
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "playback is not available for %s", source)
	}
	if !declaresCapability(descriptor, api.CapPlaybackFull) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support finite playback", source)
	}
	preparer, urlPlayback := s.providers[source].(PlaybackPreparer)
	if urlPlayback && s.urlTransport == nil {
		return nil, api.Errorf(api.CodeSourceUnavailable, "direct URL playback is unavailable")
	}
	if !urlPlayback {
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
	}
	transport := transportEngine
	if urlPlayback {
		transport = transportURLQueue
	}
	s.beginPlaybackStartLocked(source, transport)
	if urlPlayback {
		if s.engine != nil {
			_, _ = s.engine.Stop(ctx)
		}
		s.stopICY()
	} else {
		s.stopURLTransportLocked(ctx)
	}
	var state core.PlaybackState
	var err error
	if urlPlayback {
		refs := make([]api.Reference, 0, len(params.Refs))
		for _, raw := range params.Refs {
			ref, _ := api.ParseReference(raw)
			refs = append(refs, ref)
		}
		plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: refs, StartIndex: params.StartIndex})
		if prepareErr != nil {
			return nil, s.failPlaybackStartLocked(ctx, prepareErr)
		}
		state, err = s.urlTransport.Start(ctx, plan, s.playbackGeneration, s.transportSessionID)
	} else {
		state, err = s.engine.PlaySongs(ctx, ids, params.StartIndex)
	}
	if err != nil {
		return nil, s.failPlaybackStartLocked(ctx, err)
	}
	s.stopICY()
	applied, optionsErr := s.applyFormLocked(ctx, params.Shuffle, params.Repeat)
	state = appliedState(applied, state)
	persistErr := s.persistPlaybackSourceLocked(source)
	projected := s.commitPlaybackLocked(state, true)
	if optionsErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	if persistErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but its source was not saved").
			WithDetails(map[string]any{"state": projected})
	}
	return projected, nil
}

// applyFormLocked applies optional shuffle/repeat for the engine transport,
// returning what succeeded. The URL queue transport does not declare those
// capabilities, so nothing is applied for it (and the engine is never touched).
func (s *Server) applyFormLocked(ctx context.Context, shuffle *bool, repeat string) (map[string]any, *api.Error) {
	applied := map[string]any{}
	if s.activeTransport == transportURLQueue || s.engine == nil {
		return applied, nil
	}
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

func (s *Server) handleTransportControl(operation string) api.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
		if s.usingURLTransportLocked() {
			var state core.PlaybackState
			var err error
			switch operation {
			case "pause":
				state, err = s.urlTransport.Pause(ctx)
			case "resume":
				state, err = s.urlTransport.Resume(ctx)
			case "next":
				state, err = s.urlTransport.Next(ctx)
			case "previous":
				state, err = s.urlTransport.Previous(ctx)
			}
			if err != nil {
				if errors.Is(err, errQueueNoSession) {
					return nil, api.Errorf(api.CodeInvalidState, "nothing is playing")
				}
				if operation == "next" || operation == "previous" {
					if errors.Is(err, errQueueIndexOutOfRange) {
						return nil, api.Errorf(api.CodeInvalidState, "there is no %s track", operation)
					}
					return nil, s.failURLQueueLocked(ctx, err)
				}
				return nil, s.mapEngineError(err)
			}
			return s.commitPlaybackLocked(state, false), nil
		}
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
		var state core.PlaybackState
		var err error
		switch operation {
		case "pause":
			state, err = s.engine.PauseState(ctx)
		case "resume":
			state, err = s.engine.ResumeState(ctx)
		case "next":
			state, err = s.engine.NextState(ctx)
		case "previous":
			state, err = s.engine.PreviousState(ctx)
		}
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, false), nil
	}
}

func (s *Server) usingURLTransportLocked() bool {
	return s.urlTransport != nil && s.activeTransport == transportURLQueue
}

// urlQueueHasSessionLocked reports whether a live, non-empty URL queue exists.
func (s *Server) urlQueueHasSessionLocked() bool {
	return s.usingURLTransportLocked() && s.urlTransport.List().Source != nil
}

// stopURLTransportLocked best-effort stops any active URL session.
func (s *Server) stopURLTransportLocked(ctx context.Context) {
	if s.urlTransport != nil {
		_, _ = s.urlTransport.Stop(ctx)
	}
}

// failURLQueueLocked ends a URL session after a mid-queue failure: the source
// becomes unavailable, the queue is cleared, and the public state is stopped.
func (s *Server) failURLQueueLocked(ctx context.Context, err error) *api.Error {
	if s.urlTransport != nil {
		_, _ = s.urlTransport.Stop(ctx)
	}
	stopped := core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	projected := s.commitPlaybackLocked(stopped, true)
	return api.Errorf(api.CodeSourceUnavailable, "playback stopped: %v", err).
		WithDetails(map[string]any{"state": projected})
}

func (s *Server) handleStop(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if s.usingURLTransportLocked() {
		hadQueue := s.urlQueueHasSessionLocked()
		state, err := s.urlTransport.Stop(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, hadQueue), nil
	}
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
	if s.usingURLTransportLocked() {
		current, err := s.urlTransport.State(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		var next core.PlaybackState
		if current.Status == "paused" {
			next, err = s.urlTransport.Resume(ctx)
		} else if current.Status == "playing" || current.Status == "buffering" {
			next, err = s.urlTransport.Pause(ctx)
		} else {
			return nil, api.Errorf(api.CodeInvalidState, "nothing is playing to toggle")
		}
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(next, false), nil
	}
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
	source := s.publicActiveSourceLocked()
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapShuffle) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support shuffle", source)
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
	source := s.publicActiveSourceLocked()
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapRepeat) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support repeat", source)
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
	if s.activeTransport == transportURLQueue && s.urlTransport != nil {
		// Public concurrency uses the server-global queue revision, so present
		// that instead of the transport-local counter.
		queue := s.urlTransport.List()
		queue.QueueRevision = s.queueRevision
		return queue, nil
	}
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
	if params.Position != "next" && params.Position != "append" {
		return nil, api.Errorf(api.CodeInvalidRequest, "position must be next or append")
	}
	if s.activeTransport == transportURLQueue {
		return s.addURLQueueItem(ctx, params)
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
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
		activeSource := s.publicActiveSourceLocked()
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

// addURLQueueItem prepares one same-source ref and appends it to the URL queue.
func (s *Server) addURLQueueItem(ctx context.Context, params queueAddParams) (any, *api.Error) {
	if !s.urlQueueHasSessionLocked() {
		return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	source := s.publicActiveSourceLocked()
	if reference.Source != source {
		return nil, api.Errorf(api.CodeSourceMismatch, "the active queue belongs to %s", source)
	}
	preparer, ok := s.providers[source].(PlaybackPreparer)
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support an editable queue", source)
	}
	plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: []api.Reference{reference}})
	if prepareErr != nil {
		return nil, prepareErr
	}
	queue := plan.PublicQueue()
	if len(queue) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "ref has no playable item")
	}
	state, err := s.urlTransport.Add(ctx, queue, params.Position)
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
	if s.usingURLTransportLocked() {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		var p queueIndexParams
		if err := api.DecodeParams(raw, &p); err != nil {
			return nil, err
		}
		if err := s.checkQueueRevision(p.IfQueueRevision); err != nil {
			return nil, err
		}
		state, err := s.urlTransport.Jump(ctx, p.Index)
		if err != nil {
			if errors.Is(err, errQueueNoSession) {
				return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
			}
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.failURLQueueLocked(ctx, err)
		}
		return s.commitPlaybackLocked(state, false), nil
	}
	return s.queueIndexOp(ctx, raw, false, func(index int) (core.PlaybackState, error) {
		return s.engine.QueueJump(ctx, index)
	})
}

func (s *Server) handleQueueRemove(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	if s.activeTransport == transportURLQueue {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		var params queueIndexParams
		if err := api.DecodeParams(raw, &params); err != nil {
			return nil, err
		}
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		state, err := s.urlTransport.Remove(ctx, params.Index)
		if err != nil {
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.failURLQueueLocked(ctx, err)
		}
		return s.commitPlaybackLocked(state, true), nil
	}
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
	if s.activeTransport == transportURLQueue {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		state, err := s.urlTransport.Move(ctx, params.From, params.To)
		if err != nil {
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, params.From != params.To), nil
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
	return s.commitPlaybackLocked(state, params.From != params.To), nil
}

func (s *Server) handleQueueClear(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		IfQueueRevision *uint64 `json:"ifQueueRevision"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if s.activeTransport == transportURLQueue {
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		hadQueue := s.urlQueueHasSessionLocked()
		state, err := s.urlTransport.Stop(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, hadQueue), nil
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
