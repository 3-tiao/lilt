package server

import (
	"context"

	"github.com/caiguo/lilt/internal/api"
)

// AuthProvider drives one source's authorization. Apple wraps the helper's
// system dialog; a future Audius provider wraps OAuth. Tests can register a
// scriptable provider, so the generic flow mechanics are verifiable without a
// real external provider.
type AuthProvider interface {
	Source() api.SourceID
	// Describe returns the current authorization state for the source.
	Describe(ctx context.Context) api.SourceAuthorization
	// Begin starts an interactive flow. update, when non-nil, publishes a
	// pending interaction (for example the browser URL) without completing the
	// flow. It must call complete exactly once with a terminal flow after the
	// flow id is returned to the caller. It returns an error only when it cannot
	// start at all.
	Begin(ctx context.Context, flowID string, update func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error
	// Cancel stops any provider-side work for a pending flow.
	Cancel(flowID string)
	// Disconnect is a desired-state, idempotent credential removal. It returns
	// a stable api error (for example unsupported_command) or nil.
	Disconnect(ctx context.Context) *api.Error
}

// appleAuthProvider adapts the MusicKit helper to the auth contract. It reads
// the current engine through the server so a rebuilt helper is picked up.
type appleAuthProvider struct {
	server *Server
}

func newAppleAuthProvider(server *Server) *appleAuthProvider {
	return &appleAuthProvider{server: server}
}

func (p *appleAuthProvider) Source() api.SourceID { return api.SourceAppleMusic }

func (p *appleAuthProvider) engine() Engine {
	return p.server.currentEngine()
}

func (p *appleAuthProvider) Describe(ctx context.Context) api.SourceAuthorization {
	engine := p.engine()
	if engine == nil {
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthNotRequired}
	}
	status, err := engine.Authorization(ctx)
	if err != nil {
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthError, Details: map[string]any{"message": err.Error()}}
	}
	return api.SourceAuthorization{
		Source:  api.SourceAppleMusic,
		Status:  mapAppleAuthStatus(status.Status),
		Details: map[string]any{"canPlayCatalogContent": status.CanPlayCatalogContent, "hasCloudLibraryEnabled": status.HasCloudLibraryEnabled},
	}
}

func (p *appleAuthProvider) Begin(ctx context.Context, flowID string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
	engine := p.engine()
	if engine == nil {
		return errNoEngine
	}
	status, err := engine.RequestAuthorization(ctx, true)
	flow := api.AuthorizationFlow{
		FlowID:      flowID,
		Source:      api.SourceAppleMusic,
		Interaction: api.Interaction{Type: api.InteractionNone},
	}
	switch {
	case err != nil:
		flow.Status = api.FlowError
		flow.Error = api.Errorf(api.CodeAuthorizationFailed, "%v", err)
	case status.Status == "authorized":
		flow.Status = api.FlowAuthorized
	default:
		flow.Status = api.FlowDenied
	}
	complete(flow)
	return nil
}

func (p *appleAuthProvider) Cancel(string) {}

func (p *appleAuthProvider) Disconnect(context.Context) *api.Error {
	return api.Errorf(api.CodeUnsupportedCommand, "macOS does not allow programmatic Apple Music authorization revocation; use System Settings")
}

// radioAuthProvider reports that radio needs no authorization.
type radioAuthProvider struct{}

func (radioAuthProvider) Source() api.SourceID { return api.SourceRadio }

func (radioAuthProvider) Describe(context.Context) api.SourceAuthorization {
	return api.SourceAuthorization{Source: api.SourceRadio, Status: api.AuthNotRequired}
}

func (radioAuthProvider) Begin(context.Context, string, func(api.AuthorizationFlow), func(api.AuthorizationFlow)) error {
	return api.Errorf(api.CodeUnsupportedCommand, "radio does not require authorization")
}

func (radioAuthProvider) Cancel(string) {}

func (radioAuthProvider) Disconnect(context.Context) *api.Error { return nil }

var errNoEngine = api.Errorf(api.CodeAuthorizationFailed, "the playback engine is unavailable")
