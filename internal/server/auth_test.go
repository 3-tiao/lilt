package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/appleweb"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/state"
)

// fixtureAuthProvider is a scriptable provider used to verify the generic
// server-owned flow without a real external provider (no Audius/network).
type fixtureAuthProvider struct {
	source api.SourceID

	mu             sync.Mutex
	status         string
	budget         time.Duration
	canDisconnect  bool
	disconnectErr  *api.Error
	beginFunc      func(ctx context.Context, flowID string, update func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error
	beginCalls     int
	cancelCalls    []string
	disconnectCall int
}

func newFixtureProvider(source api.SourceID) *fixtureAuthProvider {
	return &fixtureAuthProvider{source: source, status: api.AuthNotDetermined}
}

func (p *fixtureAuthProvider) Source() api.SourceID { return p.source }

func (p *fixtureAuthProvider) setStatus(status string) {
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
}

func (p *fixtureAuthProvider) Describe(context.Context) api.SourceAuthorization {
	p.mu.Lock()
	defer p.mu.Unlock()
	return api.SourceAuthorization{Source: p.source, Status: p.status}
}

func (p *fixtureAuthProvider) Begin(ctx context.Context, flowID string, update func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
	p.mu.Lock()
	p.beginCalls++
	begin := p.beginFunc
	p.mu.Unlock()
	if begin == nil {
		complete(api.AuthorizationFlow{Source: p.source, Status: api.FlowAuthorized, Interaction: api.Interaction{Type: api.InteractionNone}})
		return nil
	}
	return begin(ctx, flowID, update, complete)
}

func (p *fixtureAuthProvider) Cancel(flowID string) {
	p.mu.Lock()
	p.cancelCalls = append(p.cancelCalls, flowID)
	p.mu.Unlock()
}

// AuthFlowBudget reports the scripted declaration; zero means the fixture
// does not declare one and must keep the server default.
func (p *fixtureAuthProvider) AuthFlowBudget() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.budget
}

// DisconnectSupported is opt-in: the fixture declares support only when a test
// asks for it, matching providers that omit the interface.
func (p *fixtureAuthProvider) DisconnectSupported() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.canDisconnect
}

func (p *fixtureAuthProvider) Disconnect(context.Context) *api.Error {
	p.mu.Lock()
	p.disconnectCall++
	err := p.disconnectErr
	p.mu.Unlock()
	return err
}

func startAuthServer(t *testing.T, provider AuthProvider) (string, *Server) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-auth-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := Start(Options{
		SocketPath:    socket,
		Engine:        newSupervisedEngine(),
		Store:         state.New(filepath.Join(dir, "state.json")),
		AuthProviders: []AuthProvider{provider},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return socket, srv
}

func waitFlow(t *testing.T, socket, flowID string, statuses ...string) api.AuthorizationFlow {
	t.Helper()
	want := map[string]bool{}
	for _, status := range statuses {
		want[status] = true
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := call(t, socket, "authorization.flowStatus", map[string]any{"flowId": flowID})
		if response.OK {
			var flow api.AuthorizationFlow
			if json.Unmarshal(response.Data, &flow) == nil && want[flow.Status] {
				return flow
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("flow %s never reached %v", flowID, statuses)
	return api.AuthorizationFlow{}
}

func TestAuthFlowBeginReachesAuthorized(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.beginFunc = func(_ context.Context, _ string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
		complete(api.AuthorizationFlow{
			Source:      api.SourceRadio,
			Status:      api.FlowAuthorized,
			Interaction: api.Interaction{Type: api.InteractionNone},
		})
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if !response.OK {
		t.Fatalf("begin failed: %+v", response.Error)
	}
	var flow api.AuthorizationFlow
	if err := json.Unmarshal(response.Data, &flow); err != nil {
		t.Fatal(err)
	}
	if flow.Status != api.FlowPending {
		t.Fatalf("begin flow = %+v, want pending", flow)
	}
	final := waitFlow(t, socket, flow.FlowID, api.FlowAuthorized)
	if final.Source != api.SourceRadio {
		t.Fatalf("final = %+v", final)
	}
}

func TestAuthFlowInProgress(t *testing.T) {
	block := make(chan struct{})
	provider := newFixtureProvider(api.SourceRadio)
	provider.beginFunc = func(ctx context.Context, _ string, _ func(api.AuthorizationFlow), _ func(api.AuthorizationFlow)) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if !response.OK {
		t.Fatalf("begin failed: %+v", response.Error)
	}
	var first api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &first)

	again := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if again.Error == nil || again.Error.Code != api.CodeAuthorizationInProgress {
		t.Fatalf("second begin = %+v, want authorization_in_progress", again.Error)
	}
	if again.Error.Details["flowId"] != first.FlowID {
		t.Fatalf("flowId detail = %v, want %s", again.Error.Details["flowId"], first.FlowID)
	}
	close(block)
}

func TestAuthFlowCancel(t *testing.T) {
	release := make(chan struct{})
	provider := newFixtureProvider(api.SourceRadio)
	provider.beginFunc = func(ctx context.Context, _ string, _ func(api.AuthorizationFlow), _ func(api.AuthorizationFlow)) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	var flow api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &flow)

	cancelled := call(t, socket, "authorization.cancel", map[string]any{"flowId": flow.FlowID})
	if !cancelled.OK {
		t.Fatalf("cancel failed: %+v", cancelled.Error)
	}
	var cancelFlow api.AuthorizationFlow
	_ = json.Unmarshal(cancelled.Data, &cancelFlow)
	if cancelFlow.Status != api.FlowCancelled {
		t.Fatalf("cancel flow = %+v", cancelFlow)
	}
	provider.mu.Lock()
	cancelCalls := len(provider.cancelCalls)
	provider.mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("provider cancel calls = %d", cancelCalls)
	}
	// Cancel is idempotent for a terminal flow.
	again := call(t, socket, "authorization.cancel", map[string]any{"flowId": flow.FlowID})
	if !again.OK {
		t.Fatalf("idempotent cancel failed: %+v", again.Error)
	}
	close(release)
}

func TestAuthFlowProviderError(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.beginFunc = func(context.Context, string, func(api.AuthorizationFlow), func(api.AuthorizationFlow)) error {
		return context.DeadlineExceeded
	}
	socket, _ := startAuthServer(t, provider)
	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	var flow api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &flow)

	final := waitFlow(t, socket, flow.FlowID, api.FlowError)
	if final.Error == nil || final.Error.Code != api.CodeAuthorizationFailed {
		t.Fatalf("final error = %+v", final.Error)
	}
}

// The provider's declared budget is the single deadline the flow runs under:
// the context runAuthFlow builds must carry it, and its expiry must land as a
// distinct expired terminal status — not error, not cancelled.
func TestAuthFlowExpiresWhenTheDeclaredBudgetRunsOut(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.budget = 150 * time.Millisecond
	provider.beginFunc = func(ctx context.Context, _ string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline {
			t.Error("the flow context carries no deadline")
		} else if budget := time.Until(deadline); budget > time.Second {
			t.Errorf("flow deadline is %v away, want the declared 150ms budget", budget)
		}
		<-ctx.Done()
		status := api.FlowError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = api.FlowExpired
		}
		complete(api.AuthorizationFlow{Source: api.SourceRadio, Status: status, Interaction: api.Interaction{Type: api.InteractionNone}})
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if !response.OK {
		t.Fatalf("begin failed: %+v", response.Error)
	}
	var flow api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &flow)
	final := waitFlow(t, socket, flow.FlowID, api.FlowExpired)
	if final.Status != api.FlowExpired {
		t.Fatalf("final = %+v, want expired", final)
	}
	if final.Error != nil {
		t.Fatalf("an expired flow carries an error: %+v", final.Error)
	}
}

// Providers that declare no budget keep the server default, which is the
// behavior every existing provider (Audius, Jamendo) was built on.
func TestAuthFlowDefaultBudgetForUndeclaredProviders(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.budget = 0 // the fixture's AuthFlowBudget then reports no budget
	provider.beginFunc = func(ctx context.Context, _ string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline {
			t.Fatal("the flow context carries no deadline")
		}
		if budget := time.Until(deadline); budget < defaultAuthFlowBudget-time.Second || budget > defaultAuthFlowBudget+time.Second {
			t.Fatalf("flow budget = %v, want the default %v", budget, defaultAuthFlowBudget)
		}
		complete(api.AuthorizationFlow{Source: api.SourceRadio, Status: api.FlowAuthorized, Interaction: api.Interaction{Type: api.InteractionNone}})
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if !response.OK {
		t.Fatalf("begin failed: %+v", response.Error)
	}
	var flow api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &flow)
	waitFlow(t, socket, flow.FlowID, api.FlowAuthorized)
}

// The Apple web provider owns a ten-minute budget (a human signs in, possibly
// with 2FA), and the existing providers keep the default by not declaring one.
func TestAuthFlowBudgetDeclarations(t *testing.T) {
	apple := NewAppleWebAuthProvider(&fakePageCatalog{}, func() bool { return false })
	if appleweb.SignInBudget != 10*time.Minute {
		t.Fatalf("SignInBudget = %v, want ten minutes", appleweb.SignInBudget)
	}
	if got := apple.(AuthFlowBudget).AuthFlowBudget(); got != appleweb.SignInBudget {
		t.Fatalf("apple budget = %v, want the declared SignInBudget", got)
	}
	audius := newAudiusAuthProvider(audius.Client{}, securestore.NewMemory(), "", "", "")
	if _, ok := any(audius).(AuthFlowBudget); ok {
		t.Fatal("the Audius provider must keep the default budget by not declaring one")
	}
}

func TestAuthBeginAlreadyAuthorizedTerminal(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.setStatus(api.AuthAuthorized)
	socket, _ := startAuthServer(t, provider)
	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if !response.OK {
		t.Fatalf("begin failed: %+v", response.Error)
	}
	var flow api.AuthorizationFlow
	_ = json.Unmarshal(response.Data, &flow)
	if flow.Status != api.FlowAuthorized || flow.Interaction.Type != api.InteractionNone {
		t.Fatalf("flow = %+v", flow)
	}
	provider.mu.Lock()
	calls := provider.beginCalls
	provider.mu.Unlock()
	if calls != 0 {
		t.Fatalf("provider begin called %d times for an already-authorized source", calls)
	}
}

func TestAuthDisconnect(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.canDisconnect = true
	socket, _ := startAuthServer(t, provider)
	response := call(t, socket, "authorization.disconnect", map[string]any{"source": string(api.SourceRadio)})
	if !response.OK {
		t.Fatalf("disconnect failed: %+v", response.Error)
	}
	provider.mu.Lock()
	calls := provider.disconnectCall
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("disconnect calls = %d", calls)
	}

	provider.mu.Lock()
	provider.disconnectErr = api.Errorf(api.CodeUnsupportedCommand, "provider cannot revoke")
	provider.mu.Unlock()
	unsupported := call(t, socket, "authorization.disconnect", map[string]any{"source": string(api.SourceRadio)})
	if unsupported.Error == nil || unsupported.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("unsupported disconnect = %+v", unsupported.Error)
	}
}

func TestAuthBeginUnsupportedWhenNotRequired(t *testing.T) {
	// No override: the default radio provider needs no authorization.
	socket, _ := startAuthServer(t, nil)
	response := call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	if response.Error == nil || response.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("response = %+v, want unsupported_command", response.Error)
	}
}

func TestAuthListIncludesProviders(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	socket, _ := startAuthServer(t, provider)
	response := call(t, socket, "authorization.list", nil)
	if !response.OK {
		t.Fatalf("list failed: %+v", response.Error)
	}
	var authorizations []api.SourceAuthorization
	if err := json.Unmarshal(response.Data, &authorizations); err != nil {
		t.Fatal(err)
	}
	if len(authorizations) < 2 {
		t.Fatalf("authorizations = %+v", authorizations)
	}
}

// authorization.list declares per-source disconnect support so the Account
// overlay can hide `d` where the command would fail, and the command agrees
// with the field (OQ40).
func TestAuthorizationListDeclaresDisconnectSupport(t *testing.T) {
	socket, _ := startAuthServer(t, nil)
	response := call(t, socket, "authorization.list", nil)
	if !response.OK {
		t.Fatalf("list failed: %+v", response.Error)
	}
	var authorizations []api.SourceAuthorization
	if err := json.Unmarshal(response.Data, &authorizations); err != nil {
		t.Fatal(err)
	}
	want := map[api.SourceID]bool{
		api.SourceAppleMusic: false,
		api.SourceAudius:     true,
		api.SourceJamendo:    true,
		api.SourceRadio:      false,
	}
	seen := map[api.SourceID]bool{}
	for _, authorization := range authorizations {
		expected, ok := want[authorization.Source]
		if !ok {
			continue
		}
		seen[authorization.Source] = true
		if authorization.CanDisconnect != expected {
			t.Errorf("%s canDisconnect = %v, want %v", authorization.Source, authorization.CanDisconnect, expected)
		}
	}
	for source := range want {
		if !seen[source] {
			t.Errorf("authorization.list omitted %s", source)
		}
	}
	// The command agrees with the field: radio declares no support, so it is
	// unsupported rather than a silent success.
	radio := call(t, socket, "authorization.disconnect", map[string]any{"source": string(api.SourceRadio)})
	if radio.OK || radio.Error == nil || radio.Error.Code != api.CodeUnsupportedCommand {
		t.Fatalf("radio disconnect = %+v, want unsupported_command", radio)
	}
}

func TestAuthFlowRetentionExpires(t *testing.T) {
	manager := newFlowManager()
	manager.retention = 5 * time.Millisecond
	flow, apiErr := manager.begin(api.SourceRadio)
	if apiErr != nil {
		t.Fatalf("begin: %v", apiErr)
	}
	manager.complete(flow.FlowID, api.AuthorizationFlow{Source: api.SourceRadio, Status: api.FlowAuthorized})
	if _, err := manager.get(flow.FlowID); err != nil {
		t.Fatalf("get before expiry: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := manager.get(flow.FlowID); err == nil || err.Code != api.CodeAuthorizationFlowNotFound {
		t.Fatalf("expired get = %v, want authorization_flow_not_found", err)
	}
}

func TestAuthFlowPublishesWatchEvents(t *testing.T) {
	provider := newFixtureProvider(api.SourceRadio)
	provider.beginFunc = func(_ context.Context, _ string, _ func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
		complete(api.AuthorizationFlow{Source: api.SourceRadio, Status: api.FlowAuthorized, Interaction: api.Interaction{Type: api.InteractionNone}})
		return nil
	}
	socket, _ := startAuthServer(t, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, []string{"authorization", "sources"}, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer watcher.Close()
	if !response.OK {
		t.Fatalf("watch initial = %+v", response.Error)
	}

	call(t, socket, "authorization.begin", map[string]any{"source": string(api.SourceRadio), "interactive": true})
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !seen["authorization.changed"] || !seen["sources.changed"] {
		select {
		case event := <-watcher.Events:
			seen[event.Event] = true
			if event.Event == "authorization.changed" {
				// The flow summary must be limited to {flowId,source,status} and
				// never leak interaction URLs, device codes, or provider details.
				var payload map[string]any
				if json.Unmarshal(event.Data, &payload) == nil {
					if flow, ok := payload["flow"].(map[string]any); ok {
						if len(flow) != 3 || flow["url"] != nil {
							t.Fatalf("flow summary = %v", flow)
						}
					}
				}
			}
		case <-deadline:
			t.Fatalf("missing auth watch events; saw %v", seen)
		}
	}
}

// An unknown source is a caller error (invalid_request), distinct from a known
// source that does not implement authorization (unsupported_command).
func TestAuthBeginUnknownSourceIsInvalidRequest(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "authorization.begin", map[string]any{"source": "not-a-source", "interactive": true})
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("authorization.begin unknown source = %+v, want invalid_request", response.Error)
	}
}
