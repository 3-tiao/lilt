package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/securestore"
)

const (
	audiusSecureService = "lilt"
	audiusSecureAccount = "audius"
)

// audiusAuthProvider implements the optional Audius account OAuth 2
// Authorization Code + PKCE flow. Anonymous discovery/playback never needs it;
// tokens live only in secure storage and never cross the Client API.
type audiusAuthProvider struct {
	client      audius.Client
	store       securestore.Store
	apiKey      string
	redirectURI string
	scope       string
	configured  bool
	openURL     func(string) error
	timeout     time.Duration

	mu                 sync.Mutex
	credentialRevision uint64
	cancels            map[string]func()
	cancelled          map[string]bool
	warning            *api.Error
}

type audiusCredentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountLabel string    `json:"account_label,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func newAudiusAuthProvider(client audius.Client, store securestore.Store, apiKey, redirectURI, scope string) *audiusAuthProvider {
	if store == nil {
		store = securestore.NewMemory()
	}
	if redirectURI == "" {
		redirectURI = "http://localhost:8765/callback"
	}
	if scope == "" {
		scope = "read"
	}
	return &audiusAuthProvider{
		client:      client,
		store:       store,
		apiKey:      apiKey,
		redirectURI: redirectURI,
		scope:       scope,
		// Account linking requires a developer app (API key) whose redirect URI
		// is registered; without it the consent screen would reject the flow.
		configured: strings.TrimSpace(apiKey) != "",
		openURL:    openBrowser,
		timeout:    2 * time.Minute,
		cancels:    map[string]func(){},
		cancelled:  map[string]bool{},
	}
}

func (*audiusAuthProvider) Source() api.SourceID { return api.SourceAudius }

// Ready reports whether account linking can start. The command layer calls it
// before creating a flow so an unconfigured deployment fails synchronously.
func (p *audiusAuthProvider) Ready() *api.Error {
	if !p.configured {
		return api.Errorf(api.CodeAuthorizationFailed,
			"Audius account linking is not configured; create a developer app and set LILT_AUDIUS_API_KEY with a registered LILT_AUDIUS_REDIRECT_URI")
	}
	return nil
}

func (p *audiusAuthProvider) Describe(ctx context.Context) api.SourceAuthorization {
	p.mu.Lock()
	creds, err := p.loadLocked()
	revision := p.credentialRevision
	p.mu.Unlock()
	if err != nil {
		return api.SourceAuthorization{Source: api.SourceAudius, Status: api.AuthError}
	}
	if creds.RefreshToken != "" && !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		// Network work never owns the credential lock. Every writer commits
		// against the revision it started from, so disconnect/reconnect wins
		// over both a late refresh result and a late refresh failure.
		refreshed, refreshErr := p.refresh(ctx, creds)
		p.mu.Lock()
		defer p.mu.Unlock()
		if revision != p.credentialRevision {
			current, loadErr := p.loadLocked()
			if loadErr != nil {
				return api.SourceAuthorization{Source: api.SourceAudius, Status: api.AuthError}
			}
			return audiusAuthorization(current)
		}
		if refreshErr != nil {
			return audiusAuthorization(creds)
		}
		if _, saveErr := p.commitCredentialsLocked(revision, refreshed); saveErr != nil {
			return audiusAuthorization(creds)
		}
		creds = refreshed
	}
	return audiusAuthorization(creds)
}

func audiusAuthorization(creds audiusCredentials) api.SourceAuthorization {
	status := api.AuthAuthorized
	if creds.RefreshToken == "" {
		status = api.AuthNotDetermined
	} else if !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		status = api.AuthExpired
	}
	return api.SourceAuthorization{Source: api.SourceAudius, Status: status, AccountLabel: creds.AccountLabel}
}

type callbackResult struct {
	code    string
	state   string
	errCode string
}

func (p *audiusAuthProvider) Begin(ctx context.Context, flowID string, update func(api.AuthorizationFlow), complete func(api.AuthorizationFlow)) error {
	if readyErr := p.Ready(); readyErr != nil {
		return readyErr
	}
	verifier, challenge, err := audius.GeneratePKCE()
	if err != nil {
		return err
	}
	state, err := audius.GenerateState()
	if err != nil {
		return err
	}
	parsed, err := url.Parse(p.redirectURI)
	if err != nil {
		return fmt.Errorf("invalid Audius redirect URI")
	}
	if err := validateLoopbackRedirect(parsed); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		return fmt.Errorf("Audius callback listener: %w", err)
	}
	results := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(parsed.Path, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		select {
		case results <- callbackResult{code: query.Get("code"), state: query.Get("state"), errCode: query.Get("error")}:
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("lilt: you can close this window and return to the terminal.\n"))
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	p.mu.Lock()
	p.cancels[flowID] = func() { _ = server.Close() }
	revision := p.credentialRevision
	p.mu.Unlock()

	authorizeURL := p.client.AuthorizeURL(p.apiKey, p.redirectURI, state, challenge, p.scope)
	if update != nil {
		update(api.AuthorizationFlow{
			Source:      api.SourceAudius,
			Status:      api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser, URL: authorizeURL},
		})
	}
	if err := p.openURL(authorizeURL); err != nil {
		p.Cancel(flowID)
		return fmt.Errorf("open Audius authorization page: %w", err)
	}
	go func() {
		defer p.Cancel(flowID)
		timer := time.NewTimer(p.timeout)
		defer timer.Stop()
		select {
		case result := <-results:
			if result.errCode != "" || result.state != state || result.code == "" {
				complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowDenied, Interaction: api.Interaction{Type: api.InteractionNone}})
				return
			}
			tokens, apiErr := p.client.ExchangeCode(ctx, result.code, verifier, p.redirectURI, p.apiKey)
			if apiErr != nil {
				complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowError, Interaction: api.Interaction{Type: api.InteractionNone}, Error: apiErr})
				return
			}
			profile, profileErr := p.client.Profile(ctx, tokens.AccessToken)
			if profileErr != nil {
				// The account label is part of the connected authorization
				// contract, so a failed profile lookup fails the flow.
				complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowError, Interaction: api.Interaction{Type: api.InteractionNone},
					Error: api.Errorf(api.CodeAuthorizationFailed, "could not read the Audius account profile")})
				return
			}
			creds := audiusCredentials{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, AccountLabel: audiusAccountLabel(profile), UserID: profile.ID, ExpiresAt: time.Now().Add(time.Hour)}
			// Flow cancellation and the shared credential revision both fence
			// completion. A late flow cannot overwrite a newer account either.
			p.mu.Lock()
			cancelled := p.cancelled[flowID]
			var saveErr error
			if !cancelled {
				var committed bool
				committed, saveErr = p.commitCredentialsLocked(revision, creds)
				cancelled = !committed && saveErr == nil
			}
			p.mu.Unlock()
			if cancelled {
				complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowCancelled, Interaction: api.Interaction{Type: api.InteractionNone}})
				return
			}
			if saveErr != nil {
				complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowError, Interaction: api.Interaction{Type: api.InteractionNone},
					Error: api.Errorf(api.CodeAuthorizationFailed, "could not store Audius credentials")})
				return
			}
			complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowAuthorized, Interaction: api.Interaction{Type: api.InteractionNone}})
		case <-ctx.Done():
			complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowCancelled, Interaction: api.Interaction{Type: api.InteractionNone}})
		case <-timer.C:
			complete(api.AuthorizationFlow{Source: api.SourceAudius, Status: api.FlowExpired, Interaction: api.Interaction{Type: api.InteractionNone}})
		}
	}()
	return nil
}

func (p *audiusAuthProvider) Cancel(flowID string) {
	p.mu.Lock()
	p.cancelled[flowID] = true
	cancel := p.cancels[flowID]
	delete(p.cancels, flowID)
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// TakeWarning returns and clears a deferred warning (for example a failed
// remote revoke), so the command layer can publish server.warning.
func (p *audiusAuthProvider) TakeWarning() *api.Error {
	p.mu.Lock()
	defer p.mu.Unlock()
	warning := p.warning
	p.warning = nil
	return warning
}

// DisconnectSupported reports that stored Audius credentials can be removed.
func (*audiusAuthProvider) DisconnectSupported() bool { return true }

func (p *audiusAuthProvider) Disconnect(ctx context.Context) *api.Error {
	p.mu.Lock()
	creds, err := p.loadLocked()
	if err != nil {
		p.mu.Unlock()
		return api.Errorf(api.CodeAuthorizationFailed, "could not read Audius credentials")
	}
	// Read/delete and revision invalidation share the writers' lock.
	for flowID := range p.cancels {
		p.cancelled[flowID] = true
	}
	deleteErr := p.store.Delete(audiusSecureService, audiusSecureAccount)
	if deleteErr == nil {
		p.credentialRevision++
	}
	p.mu.Unlock()
	if deleteErr != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "could not delete Audius credentials")
	}
	if creds.RefreshToken == "" {
		return nil
	}
	// Best effort only; failure must not restore the local credentials, but it
	// is surfaced as a warning for the command layer.
	if apiErr := p.client.Revoke(ctx, creds.RefreshToken, p.apiKey); apiErr != nil {
		p.mu.Lock()
		p.warning = api.Errorf(api.CodeAuthorizationFailed, "Audius credentials were removed locally, but remote revoke failed")
		p.mu.Unlock()
	}
	return nil
}

func (p *audiusAuthProvider) refresh(ctx context.Context, creds audiusCredentials) (audiusCredentials, error) {
	if creds.RefreshToken == "" {
		return creds, fmt.Errorf("no refresh token")
	}
	tokens, apiErr := p.client.RefreshToken(ctx, creds.RefreshToken, p.apiKey)
	if apiErr != nil {
		return creds, apiErr
	}
	refreshed := audiusCredentials{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, AccountLabel: creds.AccountLabel, UserID: creds.UserID, ExpiresAt: time.Now().Add(time.Hour)}
	return refreshed, nil
}

// authorizationCredentials exposes the connected account for the content
// provider. It never refreshes; an expired link is reported as disconnected
// until the authorization status path refreshes it.
func (p *audiusAuthProvider) authorizationCredentials() (string, string, bool) {
	creds, err := p.load()
	if err != nil || creds.AccessToken == "" || creds.UserID == "" {
		return "", "", false
	}
	if !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		return "", "", false
	}
	return creds.AccessToken, creds.UserID, true
}

func (p *audiusAuthProvider) load() (audiusCredentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadLocked()
}

// loadLocked returns stored credentials, or a zero value when none exist.
// A read/decode failure is an operational error, not "not connected".
func (p *audiusAuthProvider) loadLocked() (audiusCredentials, error) {
	raw, err := p.store.Get(audiusSecureService, audiusSecureAccount)
	if errors.Is(err, securestore.ErrNotFound) {
		return audiusCredentials{}, nil
	}
	if err != nil {
		return audiusCredentials{}, err
	}
	if raw == "" {
		return audiusCredentials{}, nil
	}
	var creds audiusCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return audiusCredentials{}, err
	}
	return creds, nil
}

// validateLoopbackRedirect enforces the OAuth loopback callback requirement:
// http(s) on localhost with a concrete path.
func validateLoopbackRedirect(parsed *url.URL) error {
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("Audius redirect URI must be http or https")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("Audius redirect URI must be a loopback address")
		}
	}
	if parsed.Path == "" {
		return fmt.Errorf("Audius redirect URI must include a callback path")
	}
	return nil
}

// commitCredentialsLocked is the only credential write path. Its caller owns
// p.mu; the network result's starting revision must still be current.
func (p *audiusAuthProvider) commitCredentialsLocked(revision uint64, creds audiusCredentials) (bool, error) {
	if revision != p.credentialRevision {
		return false, nil
	}
	raw, err := json.Marshal(creds)
	if err != nil {
		return false, err
	}
	if err := p.store.Set(audiusSecureService, audiusSecureAccount, string(raw)); err != nil {
		return false, err
	}
	p.credentialRevision++
	return true, nil
}

func audiusAccountLabel(profile audius.Profile) string {
	if profile.Name != "" {
		return profile.Name
	}
	return profile.Handle
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "linux":
		return exec.Command("xdg-open", target).Start()
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// audiusOAuthConfig reads the optional account-linking configuration from the
// environment. Without an API key, read-scope linking still uses app_name.
func audiusOAuthConfig() (apiKey, redirectURI, scope string) {
	return strings.TrimSpace(envOr("LILT_AUDIUS_API_KEY", "")),
		strings.TrimSpace(envOr("LILT_AUDIUS_REDIRECT_URI", "http://localhost:8765/callback")),
		strings.TrimSpace(envOr("LILT_AUDIUS_OAUTH_SCOPE", "read"))
}
