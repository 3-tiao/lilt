package server

import (
	"context"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

// AuthFlowBudget is implemented by authorization providers that bound their

// AuthProvider drives one source's authorization. Apple wraps the helper's
// system dialog; a future Audius provider wraps OAuth. Tests can register a
// scriptable provider, so the generic flow mechanics are verifiable without a
// real external provider.
// AuthWarmup is implemented by an authorization provider whose session must be up
// before anything asks for playback. Apple on Linux needs its browser for previews
// too, and `authorization.list` must not report "unverified" for as long as the
// session stays lazy: starting it at boot settles the status and moves the cold
// start off the user's first search. It complements the descriptor, which stays
// cheap on purpose (see docs/internals/apple-web-engine.md).
type AuthWarmup interface {
	// WarmUp starts whatever the provider needs and returns once the state is
	// settled. It runs in the background and must tolerate being called once.
	WarmUp(ctx context.Context)
}

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

// AuthFlowBudget is implemented by authorization providers that bound their
// own interactive flows. The budget has one owner — the provider, the only
// party that knows how long its interaction can legitimately take — and the
// server builds the flow's context from the declaration. A provider without
// the interface keeps the server default, which is what every existing
// provider (Audius, Jamendo) runs with.
type AuthFlowBudget interface {
	AuthFlowBudget() time.Duration
}

// SignInStopsPlayback is implemented by authorization providers whose
// interactive sign-in destroys the runtime their source plays on. The server
// stops that source's playback before the flow starts — through the same path
// playback.stop takes — so audio ends with a visible stopped transition
// instead of dying silently when the runtime is torn down underneath it. That
// stop is normal product behavior, not a fault: no warning is published.
type SignInStopsPlayback interface {
	SignInStopsPlayback() bool
}

// defaultAuthFlowBudget bounds flows of providers that declare none.
const defaultAuthFlowBudget = 2 * time.Minute

// authFlowBudget reads the provider's declared budget. Callers use it to build
// the flow context, so the provider's own claim is the single deadline the flow
// runs under.
func authFlowBudget(provider AuthProvider) time.Duration {
	if bounded, ok := provider.(AuthFlowBudget); ok {
		if budget := bounded.AuthFlowBudget(); budget > 0 {
			return budget
		}
	}
	return defaultAuthFlowBudget
}

// appleAuthProvider adapts the independent Apple resource runtime to the auth
// contract. Authorization must remain available while another source owns the
// exclusive playback backend.
type appleAuthProvider struct {
	server *Server
}

func newAppleAuthProvider(server *Server) *appleAuthProvider {
	return &appleAuthProvider{server: server}
}

func (p *appleAuthProvider) Source() api.SourceID { return api.SourceAppleMusic }

func (p *appleAuthProvider) resource(ctx context.Context) (AppleResourceClient, *api.Error) {
	return p.server.appleResourceClient(ctx)
}

func (p *appleAuthProvider) Describe(ctx context.Context) api.SourceAuthorization {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthError, Details: map[string]any{"message": resourceErr.Message}}
	}
	status, err := resource.Authorization(ctx)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
		return api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthError, Details: map[string]any{"message": err.Error()}}
	}
	details := map[string]any{}
	// The helper leaves accountStatus empty until its async subscription read
	// settles; authorized + empty means "still checking", not "no subscription".
	// Unknown facts are omitted rather than reported as false.
	if status.Status == "authorized" && status.AccountStatus == "" {
		details["accountStatus"] = "checking"
	} else if status.Status == "authorized" {
		details["accountStatus"] = status.AccountStatus
		details["canPlayCatalogContent"] = status.CanPlayCatalogContent
		details["hasCloudLibraryEnabled"] = status.HasCloudLibraryEnabled
	}
	return api.SourceAuthorization{
		Source:  api.SourceAppleMusic,
		Status:  mapAppleAuthStatus(status.Status),
		Details: details,
	}
}

func (p *appleAuthProvider) Begin(ctx context.Context, flowID string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
	resource, resourceErr := p.resource(ctx)
	if resourceErr != nil {
		return resourceErr
	}
	status, err := resource.RequestAuthorization(ctx, true)
	if err != nil {
		p.server.noteAppleResourceFailure(resource, err)
	}
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
