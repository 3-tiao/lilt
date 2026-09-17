package audius

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caiguo/lilt/internal/api"
)

func TestGeneratePKCEAndState(t *testing.T) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 43 {
		t.Fatalf("verifier length = %d", len(verifier))
	}
	digest := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); challenge != want {
		t.Fatalf("challenge = %q, want %q", challenge, want)
	}
	state, err := GenerateState()
	if err != nil || len(state) < 16 {
		t.Fatalf("state=%q err=%v", state, err)
	}
}

func TestAuthorizeURLContainsPKCEParams(t *testing.T) {
	client := Client{BaseURL: "https://api.audius.co/v1"}
	raw := client.AuthorizeURL("KEY", "http://localhost:8765/callback", "STATE", "CHALLENGE", "read")
	if !strings.HasPrefix(raw, "https://api.audius.co/v1/oauth/authorize?") {
		t.Fatalf("url = %q", raw)
	}
	for _, want := range []string{"response_type=code", "scope=read", "state=STATE", "code_challenge=CHALLENGE", "code_challenge_method=S256", "api_key=KEY", "response_mode=query"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("url %q missing %q", raw, want)
		}
	}
	noKey := client.AuthorizeURL("", "http://localhost:8765/callback", "S", "C", "read")
	if !strings.Contains(noKey, "app_name=lilt") {
		t.Fatalf("read-only authorize url missing app_name: %q", noKey)
	}
}

func TestOAuthTokenProfileAndRevoke(t *testing.T) {
	var lastPath, lastAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/oauth/token":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["grant_type"] == "authorization_code" && payload["code_verifier"] == "verifier" {
				_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt"}`))
				return
			}
			if payload["grant_type"] == "refresh_token" && payload["refresh_token"] == "rt" {
				_, _ = w.Write([]byte(`{"access_token":"at2","refresh_token":"rt2"}`))
				return
			}
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		case "/oauth/revoke":
			w.WriteHeader(http.StatusOK)
		case "/me":
			if lastAuth != "Bearer at" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"abc","user_id":7,"name":"Guo","handle":"guo","is_verified":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	ctx := context.Background()

	tokens, err := client.ExchangeCode(ctx, "code", "verifier", "http://localhost:8765/callback", "KEY")
	if err != nil || tokens.AccessToken != "at" || tokens.RefreshToken != "rt" {
		t.Fatalf("exchange tokens=%+v err=%v", tokens, err)
	}
	refreshed, err := client.RefreshToken(ctx, "rt", "KEY")
	if err != nil || refreshed.AccessToken != "at2" {
		t.Fatalf("refresh tokens=%+v err=%v", refreshed, err)
	}
	if err := client.Revoke(ctx, "rt2", "KEY"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	profile, err := client.Profile(ctx, "at")
	if err != nil || profile.UserID != 7 || profile.Name != "Guo" || !profile.IsVerified {
		t.Fatalf("profile=%+v path=%s err=%v", profile, lastPath, err)
	}
}

func TestOAuthErrorsAreStableAndDoNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_grant","error_description":"secret-token-abc"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.ExchangeCode(context.Background(), "c", "v", "http://localhost/cb", "KEY")
	if err == nil || err.Code != api.CodeAuthorizationFailed {
		t.Fatalf("err=%v", err)
	}
	if err.Details["providerCode"] != "400" {
		t.Fatalf("providerCode=%v", err.Details["providerCode"])
	}
	if strings.Contains(err.Message, "secret-token-abc") {
		t.Fatalf("error leaked upstream body: %q", err.Message)
	}
}
