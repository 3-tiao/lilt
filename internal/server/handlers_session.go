package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
)

// engineStateLocked reads the current playback state for a watch snapshot.
// Callers hold s.mu.
func (s *Server) engineStateLocked() (*core.PlaybackState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.usingURLTransportLocked() {
		state, err := s.urlTransport.State(ctx)
		if err != nil {
			return nil, err
		}
		return &state, nil
	}
	if s.activeTransport == transportStream {
		if s.audioEngine == nil || s.audioEngineRestarting {
			return nil, nil
		}
		state, err := s.audioEngine.State(ctx)
		if err != nil {
			return nil, err
		}
		return &state, nil
	}
	if s.engine == nil || s.engineRestarting {
		return nil, nil
	}
	state, err := s.engine.State(ctx)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

type statusParams struct {
	IncludeQueue bool `json:"includeQueue"`
}

func (s *Server) handleStatus(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params statusParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	// A session with no playback backend attached is stopped, not broken: Linux
	// has no MusicKit engine at all, and `session.status` is how scripts and
	// agents read the session. Only a backend that reports a failure is an error.
	state := core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	attached, err := s.engineStateLocked()
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	if attached != nil {
		state = *attached
	}
	source := s.publicActiveSourceLocked()
	if params.IncludeQueue {
		return s.projectState(state, source, s.sequence, s.queueRevision), nil
	}
	return s.projectStatus(state, source, s.sequence), nil
}

func (s *Server) handleAuthorizationList(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	return s.authorizationsCtx(ctx), nil
}

func (s *Server) handleAuthorizationStatus(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	for _, authorization := range s.authorizationsCtx(ctx) {
		if string(authorization.Source) == params.Source {
			return authorization, nil
		}
	}
	return nil, api.Errorf(api.CodeInvalidRequest, "unknown source %q", params.Source)
}

func (s *Server) handleAuthorizationBegin(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source      string `json:"source"`
		Interactive bool   `json:"interactive"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if !params.Interactive {
		return nil, api.Errorf(api.CodeInvalidRequest, "authorization.begin requires interactive:true")
	}
	source, known := api.KnownSourceID(params.Source)
	if !known {
		return nil, api.Errorf(api.CodeInvalidRequest, "unknown source %q", params.Source)
	}
	provider, ok := s.authProviders[source]
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not implement authorization", params.Source)
	}
	// Fail synchronously when the provider cannot start a flow at all.
	if ready, ok := provider.(interface{ Ready() *api.Error }); ok {
		if apiErr := ready.Ready(); apiErr != nil {
			return nil, apiErr
		}
	}
	descriptor := provider.Describe(context.Background())
	if descriptor.Status == api.AuthNotRequired {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not require authorization", params.Source)
	}
	if descriptor.Status == api.AuthAuthorized {
		return api.AuthorizationFlow{
			FlowID:      newFlowID(),
			Source:      source,
			Status:      api.FlowAuthorized,
			Interaction: api.Interaction{Type: api.InteractionNone},
		}, nil
	}
	flow, apiErr := s.authFlows.begin(source)
	if apiErr != nil {
		if apiErr.Details == nil {
			apiErr.Details = map[string]any{}
		}
		apiErr.Details["flowId"] = flow.FlowID
		return nil, apiErr
	}
	// A sign-in that destroys its own runtime stops this source's playback
	// first: the queue-clearing stop command path publishes the stopped
	// transition, so watch clients see why the audio ended instead of silence.
	if stopper, ok := provider.(SignInStopsPlayback); ok && stopper.SignInStopsPlayback() {
		if stopErr := s.stopSourceForSignInLocked(ctx, source); stopErr != nil {
			_, _ = s.authFlows.cancel(flow.FlowID)
			return nil, stopErr
		}
	}
	go s.runAuthFlow(provider, flow.FlowID)
	return flow, nil
}

// stopSourceForSignInLocked stops the signing-in source's playback through the
// same path playback.stop takes. Sign-in tears down the browser the audio runs
// on; stopping first makes that a visible transition instead of a silent
// death. It is normal product behavior, not a fault: no warning is published,
// and the user can explicitly start playback again afterwards. Callers hold
// s.mu.
func (s *Server) stopSourceForSignInLocked(ctx context.Context, source api.SourceID) *api.Error {
	if s.publicActiveSourceLocked() != source || !s.urlQueueHasSessionLocked() {
		return nil
	}
	hadQueue := s.urlQueueHasSessionLocked()
	state, err := s.urlTransport.Stop(ctx)
	if err != nil {
		// Disconnect must clear this source's playback or fail; the same holds
		// for a sign-in that is about to kill the browser underneath it.
		return s.mapEngineError(err)
	}
	s.commitPlaybackLocked(state, hadQueue)
	return nil
}

// runAuthFlow drives a provider flow and records its terminal state. It never
// blocks an RPC.
func (s *Server) runAuthFlow(provider AuthProvider, flowID string) {
	// The provider's declared budget is the single deadline the flow runs
	// under; the provider's own error mapping turns its expiry into the
	// flow's terminal status.
	ctx, cancel := context.WithTimeout(context.Background(), authFlowBudget(provider))
	s.authFlows.setCancel(flowID, cancel)
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(cancel) }
	if !s.authFlows.isPending(flowID) {
		// The flow was cancelled before provider work started.
		stop()
		return
	}
	finish := func(flow api.AuthorizationFlow) {
		flow.FlowID = flowID
		if flow.Source == "" {
			flow.Source = provider.Source()
		}
		if flow.Interaction.Type == "" {
			flow.Interaction = api.Interaction{Type: api.InteractionNone}
		}
		if flow.Status == api.FlowPending {
			flow.Status = api.FlowError
		}
		s.authFlows.complete(flowID, flow)
		s.publishAuthorizationChange(provider.Source(), flowID)
		stop()
	}
	err := provider.Begin(ctx, flowID, func(flow api.AuthorizationFlow) {
		flow.FlowID = flowID
		if flow.Source == "" {
			flow.Source = provider.Source()
		}
		if flow.Interaction.Type == "" {
			flow.Interaction = api.Interaction{Type: api.InteractionNone}
		}
		s.authFlows.pending(flowID, flow)
		s.publishAuthorizationChange(provider.Source(), flowID)
	}, finish)
	if err != nil {
		terminal := api.AuthorizationFlow{
			FlowID:      flowID,
			Source:      provider.Source(),
			Status:      api.FlowError,
			Interaction: api.Interaction{Type: api.InteractionNone},
			Error:       api.Errorf(api.CodeAuthorizationFailed, "%v", err),
		}
		s.authFlows.complete(flowID, terminal)
		s.publishAuthorizationChange(provider.Source(), flowID)
		stop()
	}
}

// publishAuthorizationChange emits authorization.changed plus a full
// sources.changed snapshot, since capability availability may have moved.
func (s *Server) publishAuthorizationChange(source api.SourceID, flowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishAuthorizationChangeLocked(source, flowID)
}

// publishAuthorizationChangeLocked publishes authorization.changed plus a
// full sources.changed snapshot — both through the shared content gate, so an
// event whose wire data matches the last published snapshot is suppressed
// without consuming a sequence number (a new flowId or a settled authorization
// state changes the payload, so those always go out).
func (s *Server) publishAuthorizationChangeLocked(source api.SourceID, flowID string) {
	descriptor := api.SourceAuthorization{Source: source, Status: api.AuthNotRequired}
	if provider, ok := s.authProviders[source]; ok {
		descriptor = describeAuthorization(context.Background(), provider)
	}
	data := authorizationChangedData{Authorization: descriptor}
	if flowID != "" {
		if flow, err := s.authFlows.get(flowID); err == nil {
			data.Flow = &authorizationFlowSummary{FlowID: flow.FlowID, Source: flow.Source, Status: flow.Status}
		}
	}
	s.publishSnapshotLocked("authorization.changed", data)
	s.publishSourcesChangedLocked()
}

func (s *Server) handleAuthorizationFlowStatus(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		FlowID string `json:"flowId"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	flow, apiErr := s.authFlows.get(params.FlowID)
	if apiErr != nil {
		return nil, apiErr
	}
	return flow, nil
}

func (s *Server) handleAuthorizationCancel(_ context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		FlowID string `json:"flowId"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	flow, apiErr := s.authFlows.cancel(params.FlowID)
	if apiErr != nil {
		return nil, apiErr
	}
	if provider, ok := s.authProviders[flow.Source]; ok {
		provider.Cancel(params.FlowID)
	}
	return flow, nil
}

func (s *Server) handleAuthorizationDisconnect(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Source string `json:"source"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	source := api.SourceID(params.Source)
	provider, ok := s.authProviders[source]
	if !ok {
		return nil, api.Errorf(api.CodeInvalidRequest, "unknown source %q", params.Source)
	}
	// A source that does not declare disconnect support MUST NOT be offered the
	// action: answer unsupported_command so the field and the command agree
	// (OQ40). Clients hide `d` for the same value.
	if !disconnectSupported(provider) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "disconnect is not supported for %s", params.Source)
	}
	// Disconnect stops this source's playback and cancels any pending flow
	// before removing local credentials.
	if s.publicActiveSourceLocked() == source && s.usingURLTransportLocked() {
		hadQueue := s.urlQueueHasSessionLocked()
		state, err := s.urlTransport.Stop(ctx)
		if err != nil {
			// Disconnect must clear this source's playback or fail; never report
			// success while audio could continue.
			return nil, s.mapEngineError(err)
		}
		s.commitPlaybackLocked(state, hadQueue)
	}
	s.authFlows.cancelActive(source)
	if apiErr := provider.Disconnect(ctx); apiErr != nil {
		return nil, apiErr
	}
	if reporter, ok := provider.(interface{ TakeWarning() *api.Error }); ok {
		if warning := reporter.TakeWarning(); warning != nil {
			s.sequence++
			s.logf("server.warning", map[string]any{"code": warning.Code, "message": warning.Message})
			s.publishLocked("server.warning", map[string]any{"code": warning.Code, "message": warning.Message})
		}
	}
	s.publishAuthorizationChangeLocked(source, "")
	return describeAuthorization(ctx, provider), nil
}
