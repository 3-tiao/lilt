package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
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
	var state core.PlaybackState
	if s.usingURLTransportLocked() {
		urlState, err := s.urlTransport.State(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		state = urlState
	} else {
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
		engineState, err := s.engine.State(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		state = engineState
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

func (s *Server) handleAuthorizationBegin(_ context.Context, raw json.RawMessage) (any, *api.Error) {
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
	source := api.SourceID(params.Source)
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
	go s.runAuthFlow(provider, flow.FlowID)
	return flow, nil
}

// runAuthFlow drives a provider flow and records its terminal state. It never
// blocks an RPC.
func (s *Server) runAuthFlow(provider AuthProvider, flowID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

// publishAuthorizationChangeLocked is the s.mu-held form, for command handlers
// that already run under the command lock.
func (s *Server) publishAuthorizationChangeLocked(source api.SourceID, flowID string) {
	descriptor := api.SourceAuthorization{Source: source, Status: api.AuthNotRequired}
	if provider, ok := s.authProviders[source]; ok {
		descriptor = provider.Describe(context.Background())
	}
	data := map[string]any{"authorization": descriptor}
	if flowID != "" {
		if flow, err := s.authFlows.get(flowID); err == nil {
			data["flow"] = map[string]any{"flowId": flow.FlowID, "source": flow.Source, "status": flow.Status}
		}
	}
	s.sequence++
	s.publishLocked("authorization.changed", data)
	s.sequence++
	s.publishLocked("sources.changed", map[string]any{"sources": s.sourceDescriptors()})
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
			s.publishLocked("server.warning", map[string]any{"code": warning.Code, "message": warning.Message})
		}
	}
	s.publishAuthorizationChangeLocked(source, "")
	return provider.Describe(context.Background()), nil
}
