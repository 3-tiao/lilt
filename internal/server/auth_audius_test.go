package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/securestore"
)

// audiusOAuthUpstream serves the confirmed OAuth endpoints. It records calls so
// tests can assert refresh/revoke behaviour without real network.
type audiusOAuthUpstream struct {
	mu          sync.Mutex
	revokes     int
	tokens      int
	overflow    bool
	failProfile bool
	failRevoke  bool
}

func (u *audiusOAuthUpstream) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/token":
		u.mu.Lock()
		u.tokens++
		u.mu.Unlock()
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if u.overflow {
			http.Error(w, `{"error":"invalid_grant","error_description":"secret"}`, http.StatusBadRequest)
			return
		}
		switch payload["grant_type"] {
		case "authorization_code":
			if payload["code"] != "code-1" || payload["code_verifier"] == "" {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt"}`))
		case "refresh_token":
			_, _ = w.Write([]byte(`{"access_token":"at2","refresh_token":"rt2"}`))
		default:
			http.Error(w, "bad grant", http.StatusBadRequest)
		}
	case "/me":
		if u.failProfile {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Authorization") != "Bearer at" && r.Header.Get("Authorization") != "Bearer at2" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"abc","user_id":7,"name":"Guo","handle":"guo","is_verified":true}}`))
	case "/oauth/revoke":
		u.mu.Lock()
		u.revokes++
		u.mu.Unlock()
		if u.failRevoke {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (u *audiusOAuthUpstream) revokeCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.revokes
}

func freeLoopbackRedirect(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return fmt.Sprintf("http://127.0.0.1:%d/callback", port)
}

func TestAudiusOAuthBeginCallbackAndDisconnect(t *testing.T) {
	upstream := &audiusOAuthUpstream{}
	server := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer server.Close()
	client := audius.Client{BaseURL: server.URL, HTTP: server.Client()}
	store := securestore.NewMemory()
	provider := newAudiusAuthProvider(client, store, "KEY", freeLoopbackRedirect(t), "read")
	provider.timeout = 5 * time.Second

	var openedURL string
	provider.openURL = func(u string) error { openedURL = u; return nil }

	flows := make(chan api.AuthorizationFlow, 4)
	err := provider.Begin(context.Background(), "flow-1", func(flow api.AuthorizationFlow) {
		flows <- flow
	}, func(flow api.AuthorizationFlow) {
		flows <- flow
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	pending := <-flows
	if pending.Status != api.FlowPending || pending.Interaction.Type != api.InteractionBrowser || pending.Interaction.URL == "" {
		t.Fatalf("pending flow=%+v", pending)
	}
	if !strings.Contains(openedURL, "api_key=KEY") || !strings.Contains(openedURL, "code_challenge_method=S256") {
		t.Fatalf("opened url=%q", openedURL)
	}
	parsed, err := url.Parse(pending.Interaction.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")

	// Simulate the browser redirect to the loopback callback.
	resp, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	terminal := <-flows
	if terminal.Status != api.FlowAuthorized {
		t.Fatalf("terminal=%+v", terminal)
	}

	auth := provider.Describe(context.Background())
	if auth.Status != api.AuthAuthorized || auth.AccountLabel != "Guo" {
		t.Fatalf("describe=%+v", auth)
	}
	if _, err := store.Get(audiusSecureService, audiusSecureAccount); err != nil {
		t.Fatalf("credentials not stored: %v", err)
	}

	if apiErr := provider.Disconnect(context.Background()); apiErr != nil {
		t.Fatalf("disconnect: %v", apiErr)
	}
	if _, err := store.Get(audiusSecureService, audiusSecureAccount); err == nil {
		t.Fatal("credentials survived disconnect")
	}
	if upstream.revokeCount() != 1 {
		t.Fatalf("revoke count=%d", upstream.revokeCount())
	}
}

func TestAudiusOAuthUnconfiguredBeginFailsClearly(t *testing.T) {
	provider := newAudiusAuthProvider(audius.Client{}, securestore.NewMemory(), "", freeLoopbackRedirect(t), "read")
	err := provider.Begin(context.Background(), "flow-1", func(api.AuthorizationFlow) {}, func(api.AuthorizationFlow) {})
	if err == nil {
		t.Fatal("expected unconfigured begin to fail")
	}
	apiErr, ok := err.(*api.Error)
	if !ok || apiErr.Code != api.CodeAuthorizationFailed {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(apiErr.Message, "LILT_AUDIUS_API_KEY") {
		t.Fatalf("message not actionable: %q", apiErr.Message)
	}
	if provider.Describe(context.Background()).Status != api.AuthNotDetermined {
		t.Fatal("anonymous status changed")
	}
}

func TestAudiusOAuthUnconfiguredBeginOverSocket(t *testing.T) {
	provider := newAudiusAuthProvider(audius.Client{}, securestore.NewMemory(), "", freeLoopbackRedirect(t), "read")
	socket, _ := startAuthServer(t, provider)
	response := call(t, socket, "authorization.begin", map[string]any{"source": "audius", "interactive": true})
	if response.OK || response.Error == nil || response.Error.Code != api.CodeAuthorizationFailed {
		t.Fatalf("begin = %+v, want authorization_failed", response)
	}
	if !strings.Contains(response.Error.Message, "LILT_AUDIUS_API_KEY") {
		t.Fatalf("message not actionable: %q", response.Error.Message)
	}
	if response := call(t, socket, "authorization.list", nil); !response.OK {
		t.Fatalf("authorization.list: %+v", response.Error)
	}
}

func TestAudiusOAuthRejectsNonLoopbackRedirect(t *testing.T) {
	provider := newAudiusAuthProvider(audius.Client{}, securestore.NewMemory(), "KEY", "http://example.com/callback", "read")
	if err := provider.Begin(context.Background(), "flow-1", func(api.AuthorizationFlow) {}, func(api.AuthorizationFlow) {}); err == nil {
		t.Fatal("non-loopback redirect accepted")
	}
}

func TestAudiusOAuthProfileFailureFailsFlow(t *testing.T) {
	upstream := &audiusOAuthUpstream{failProfile: true}
	server := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer server.Close()
	store := securestore.NewMemory()
	provider := newAudiusAuthProvider(audius.Client{BaseURL: server.URL, HTTP: server.Client()}, store, "KEY", freeLoopbackRedirect(t), "read")
	provider.openURL = func(string) error { return nil }
	provider.timeout = 5 * time.Second
	flows := make(chan api.AuthorizationFlow, 4)
	if err := provider.Begin(context.Background(), "flow-1", func(f api.AuthorizationFlow) { flows <- f }, func(f api.AuthorizationFlow) { flows <- f }); err != nil {
		t.Fatal(err)
	}
	pending := <-flows
	parsed, _ := url.Parse(pending.Interaction.URL)
	resp, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(parsed.Query().Get("state")))
	if err == nil {
		_ = resp.Body.Close()
	}
	terminal := <-flows
	if terminal.Status != api.FlowError {
		t.Fatalf("terminal=%+v", terminal)
	}
	if _, err := store.Get(audiusSecureService, audiusSecureAccount); err == nil {
		t.Fatal("credentials stored despite profile failure")
	}
}

type failingStore struct{ err error }

func (s failingStore) Get(string, string) (string, error) { return "", s.err }
func (s failingStore) Set(string, string, string) error   { return s.err }
func (s failingStore) Delete(string, string) error        { return s.err }

func TestAudiusOAuthSecureStoreFailureIsReported(t *testing.T) {
	store := failingStore{err: errors.New("keychain unavailable")}
	provider := newAudiusAuthProvider(audius.Client{}, store, "KEY", freeLoopbackRedirect(t), "read")
	if auth := provider.Describe(context.Background()); auth.Status != api.AuthError {
		t.Fatalf("describe=%+v", auth)
	}
	if apiErr := provider.Disconnect(context.Background()); apiErr == nil || apiErr.Code != api.CodeAuthorizationFailed {
		t.Fatalf("disconnect=%v", apiErr)
	}
}

func TestAudiusOAuthRevokeFailureWarnsButDeletesLocal(t *testing.T) {
	upstream := &audiusOAuthUpstream{failRevoke: true}
	server := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer server.Close()
	store := securestore.NewMemory()
	raw, _ := json.Marshal(audiusCredentials{AccessToken: "at", RefreshToken: "rt", AccountLabel: "Guo"})
	_ = store.Set(audiusSecureService, audiusSecureAccount, string(raw))
	provider := newAudiusAuthProvider(audius.Client{BaseURL: server.URL, HTTP: server.Client()}, store, "KEY", freeLoopbackRedirect(t), "read")
	if apiErr := provider.Disconnect(context.Background()); apiErr != nil {
		t.Fatalf("disconnect: %v", apiErr)
	}
	if _, err := store.Get(audiusSecureService, audiusSecureAccount); err == nil {
		t.Fatal("local credentials survived disconnect")
	}
	if provider.TakeWarning() == nil {
		t.Fatal("expected a revoke warning")
	}
}

func TestAudiusDisconnectRevokeFailurePublishesWarning(t *testing.T) {
	upstream := &audiusOAuthUpstream{failRevoke: true}
	httptestServer := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer httptestServer.Close()
	store := securestore.NewMemory()
	raw, _ := json.Marshal(audiusCredentials{AccessToken: "at", RefreshToken: "rt", AccountLabel: "Guo"})
	_ = store.Set(audiusSecureService, audiusSecureAccount, string(raw))
	provider := newAudiusAuthProvider(audius.Client{BaseURL: httptestServer.URL, HTTP: httptestServer.Client()}, store, "KEY", freeLoopbackRedirect(t), "read")
	socket, _ := startAuthServer(t, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, watcher, err := api.Watch(ctx, socket, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if response := call(t, socket, "authorization.disconnect", map[string]any{"source": "audius"}); !response.OK {
		t.Fatalf("disconnect: %+v", response.Error)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				t.Fatal("watch closed before a warning")
			}
			if event.Event != "server.warning" {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["code"] == api.CodeAuthorizationFailed {
				return
			}
		case <-deadline:
			t.Fatal("no server.warning observed for revoke failure")
		}
	}
}

func TestFlowManagerStaleCompleteDoesNotReleaseActiveSlot(t *testing.T) {
	manager := newFlowManager()
	first, _ := manager.begin(api.SourceAudius)
	if !manager.isPending(first.FlowID) {
		t.Fatal("first flow not pending")
	}
	manager.cancel(first.FlowID)
	second, _ := manager.begin(api.SourceAudius)
	manager.complete(first.FlowID, api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowAuthorized})
	if flow, err := manager.get(first.FlowID); err != nil || flow.Status != api.FlowCancelled {
		t.Fatalf("stale completion overwrote terminal state: %+v err=%v", flow, err)
	}
	if !manager.isPending(second.FlowID) {
		t.Fatal("stale completion released the new flow")
	}
	if _, err := manager.begin(api.SourceAudius); err == nil || err.Code != api.CodeAuthorizationInProgress {
		t.Fatalf("duplicate begin = %v", err)
	}
}

func TestAudiusOAuthRejectsStateMismatch(t *testing.T) {
	upstream := &audiusOAuthUpstream{}
	server := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer server.Close()
	provider := newAudiusAuthProvider(audius.Client{BaseURL: server.URL, HTTP: server.Client()}, securestore.NewMemory(), "KEY", freeLoopbackRedirect(t), "read")
	provider.openURL = func(string) error { return nil }
	provider.timeout = 5 * time.Second
	flows := make(chan api.AuthorizationFlow, 4)
	if err := provider.Begin(context.Background(), "flow-1", func(f api.AuthorizationFlow) { flows <- f }, func(f api.AuthorizationFlow) { flows <- f }); err != nil {
		t.Fatal(err)
	}
	pending := <-flows
	parsed, _ := url.Parse(pending.Interaction.URL)
	resp, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=wrong")
	if err == nil {
		_ = resp.Body.Close()
	}
	terminal := <-flows
	if terminal.Status != api.FlowDenied {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestAudiusOAuthRefreshOnExpiryAndErrorNoLeak(t *testing.T) {
	upstream := &audiusOAuthUpstream{}
	server := httptest.NewServer(http.HandlerFunc(upstream.handler))
	defer server.Close()
	store := securestore.NewMemory()
	raw, _ := json.Marshal(audiusCredentials{AccessToken: "old", RefreshToken: "rt", AccountLabel: "Guo", ExpiresAt: time.Now().Add(-time.Minute)})
	_ = store.Set(audiusSecureService, audiusSecureAccount, string(raw))
	provider := newAudiusAuthProvider(audius.Client{BaseURL: server.URL, HTTP: server.Client()}, store, "KEY", freeLoopbackRedirect(t), "read")

	auth := provider.Describe(context.Background())
	if auth.Status != api.AuthAuthorized {
		t.Fatalf("refresh describe=%+v", auth)
	}
	loaded, _ := store.Get(audiusSecureService, audiusSecureAccount)
	if !strings.Contains(loaded, "at2") {
		t.Fatalf("refresh not persisted: %s", loaded)
	}

	upstream.overflow = true
	if _, err := provider.refresh(context.Background(), audiusCredentials{RefreshToken: "rt"}); err == nil {
		t.Fatal("expected refresh failure")
	}
}
