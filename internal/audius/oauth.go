package audius

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/3-tiao/lilt/internal/api"
)

// Tokens is the OAuth token pair. It is secret material and must only ever be
// written to secure storage.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Profile is the authenticated user's public identity.
type Profile struct {
	ID         string `json:"id"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Handle     string `json:"handle"`
	IsVerified bool   `json:"is_verified"`
}

// GeneratePKCE returns a 43-character URL-safe verifier and its S256 challenge.
func GeneratePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return verifier, challenge, nil
}

// GenerateState returns a random CSRF state value.
func GenerateState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// AuthorizeURL builds the consent-screen URL. scope is "read" or "write".
func (c Client) AuthorizeURL(apiKey, redirectURI, state, challenge, scope string) string {
	query := url.Values{
		"response_type":         {"code"},
		"scope":                 {scope},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"response_mode":         {"query"},
		"display":               {"fullScreen"},
	}
	if apiKey != "" {
		query.Set("api_key", apiKey)
	} else {
		// Read-only flows may use the app name instead of a developer key.
		query.Set("app_name", "lilt")
	}
	return c.baseURL() + "/oauth/authorize?" + query.Encode()
}

// ExchangeCode trades an authorization code (with its PKCE verifier) for tokens.
func (c Client) ExchangeCode(ctx context.Context, code, verifier, redirectURI, clientID string) (Tokens, *api.Error) {
	return c.tokenRequest(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": verifier,
		"client_id":     clientID,
		"redirect_uri":  redirectURI,
	})
}

// RefreshToken exchanges a refresh token for a new token pair.
func (c Client) RefreshToken(ctx context.Context, refreshToken, clientID string) (Tokens, *api.Error) {
	return c.tokenRequest(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	})
}

// Revoke invalidates a refresh token. Callers treat failure as non-fatal.
func (c Client) Revoke(ctx context.Context, token, clientID string) *api.Error {
	return c.post(ctx, "/oauth/revoke", map[string]string{"token": token, "client_id": clientID}, nil)
}

// Profile fetches the authenticated user's identity.
func (c Client) Profile(ctx context.Context, accessToken string) (Profile, *api.Error) {
	if strings.TrimSpace(accessToken) == "" {
		return Profile{}, api.Errorf(api.CodeAuthorizationRequired, "no Audius access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/me", nil)
	if err != nil {
		return Profile{}, api.Errorf(api.CodeAuthorizationFailed, "Audius profile request failed")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Profile{}, api.Errorf(api.CodeAuthorizationFailed, "Audius profile request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Profile{}, api.Errorf(api.CodeAuthorizationFailed, "Audius profile request failed").
			WithDetails(map[string]any{"providerCode": fmt.Sprint(resp.StatusCode)})
	}
	var envelope struct {
		Data Profile `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || envelope.Data.ID == "" {
		return Profile{}, api.Errorf(api.CodeAuthorizationFailed, "Audius returned an invalid profile")
	}
	return envelope.Data, nil
}

// UserPlaylists lists the authenticated account's playlists. Audius exposes
// them under the public user-playlists route with the account's bearer token.
func (c Client) UserPlaylists(ctx context.Context, userID, accessToken string, limit int) ([]Playlist, *api.Error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(accessToken) == "" {
		return nil, api.Errorf(api.CodeAuthorizationRequired, "no Audius account is connected")
	}
	if limit <= 0 {
		limit = 20
	}
	query := url.Values{"limit": {fmt.Sprint(limit)}}
	endpoint := c.baseURL() + "/users/" + url.PathEscape(userID) + "/playlists?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Audius library request failed")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Audius library request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, api.Errorf(api.CodeSearchFailed, "Audius library request failed").
			WithDetails(map[string]any{"providerCode": fmt.Sprint(resp.StatusCode)})
	}
	var envelope struct {
		Data []Playlist `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, api.Errorf(api.CodeSearchFailed, "Audius returned an invalid library response")
	}
	return envelope.Data, nil
}

func (c Client) tokenRequest(ctx context.Context, payload map[string]string) (Tokens, *api.Error) {
	var tokens Tokens
	if err := c.post(ctx, "/oauth/token", payload, &tokens); err != nil {
		return Tokens{}, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return Tokens{}, api.Errorf(api.CodeAuthorizationFailed, "Audius returned an incomplete token response")
	}
	return tokens, nil
}

func (c Client) post(ctx context.Context, path string, payload map[string]string, out any) *api.Error {
	body, err := json.Marshal(payload)
	if err != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "Audius request could not be built")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+path, bytes.NewReader(body))
	if err != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "Audius request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return api.Errorf(api.CodeAuthorizationFailed, "Audius request timed out")
		}
		return api.Errorf(api.CodeAuthorizationFailed, "Audius request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The upstream body may echo credentials; never surface it.
		return api.Errorf(api.CodeAuthorizationFailed, "Audius rejected the request").
			WithDetails(map[string]any{"providerCode": fmt.Sprint(resp.StatusCode)})
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "Audius returned an invalid response")
	}
	return nil
}
