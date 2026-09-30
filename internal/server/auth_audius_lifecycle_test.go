package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/securestore"
)

func TestAudiusLateRefreshCannotRestoreDisconnectedAccount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reconnect   bool
		failRefresh bool
	}{
		{"disconnect", false, false},
		{"disconnect_then_reconnect", true, false},
		{"disconnect_late_failure", false, true},
		{"reconnect_late_failure", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/token":
					var payload map[string]string
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if payload["grant_type"] == "refresh_token" {
						entered <- struct{}{}
						<-release
						if tc.failRefresh {
							http.Error(w, "fixture refresh refused", http.StatusUnauthorized)
							return
						}
						_, _ = w.Write([]byte(`{"access_token":"late-access","refresh_token":"late-refresh"}`))
					} else {
						_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh"}`))
					}
				case "/me":
					_, _ = w.Write([]byte(`{"data":{"id":"new-user","name":"New account"}}`))
				case "/oauth/revoke":
					w.WriteHeader(http.StatusOK)
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			defer unblock()
			store := securestore.NewMemory()
			raw, _ := json.Marshal(audiusCredentials{
				AccessToken: "old-access", RefreshToken: "old-refresh", UserID: "old-user",
				AccountLabel: "Old account", ExpiresAt: time.Now().Add(-time.Minute),
			})
			if err := store.Set(audiusSecureService, audiusSecureAccount, string(raw)); err != nil {
				t.Fatal(err)
			}
			p := newAudiusAuthProvider(audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}, store, "fixture-key", freeLoopbackRedirect(t), "read")
			p.openURL = func(string) error { return nil }
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			described := make(chan api.SourceAuthorization, 1)
			go func() { described <- p.Describe(ctx) }()
			waitURLTransition(t, entered)
			if err := p.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(audiusSecureService, audiusSecureAccount); !errors.Is(err, securestore.ErrNotFound) {
				t.Fatalf("disconnect did not remove credentials: %v", err)
			}
			if tc.reconnect {
				flows := make(chan api.AuthorizationFlow, 2)
				if err := p.Begin(ctx, "new-flow", func(flow api.AuthorizationFlow) { flows <- flow }, func(flow api.AuthorizationFlow) { flows <- flow }); err != nil {
					t.Fatal(err)
				}
				pending := waitURLTransition(t, flows)
				parsed, _ := url.Parse(pending.Interaction.URL)
				response, err := upstream.Client().Get(p.redirectURI + "?code=fixture-code&state=" + url.QueryEscape(parsed.Query().Get("state")))
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if terminal := waitURLTransition(t, flows); terminal.Status != api.FlowAuthorized {
					t.Fatalf("new account authorization = %+v", terminal)
				}
			}
			unblock()
			auth := waitURLTransition(t, described)
			stored, err := p.load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.reconnect {
				if stored.AccessToken != "new-access" || stored.UserID != "new-user" || auth.Status != api.AuthAuthorized || auth.AccountLabel != "New account" {
					t.Fatalf("old refresh replaced the new account: user=%q, label=%q, auth=%+v", stored.UserID, stored.AccountLabel, auth)
				}
			} else if stored.RefreshToken != "" || auth.Status != api.AuthNotDetermined {
				t.Fatalf("old refresh restored the disconnected account: credentialPresent=%t, auth=%+v", stored.RefreshToken != "", auth)
			}
		})
	}
}

func TestAudiusDisconnectFencesPendingAuthorization(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"fixture-access","refresh_token":"fixture-refresh"}`))
		case "/me":
			entered <- struct{}{}
			<-release
			_, _ = w.Write([]byte(`{"data":{"id":"fixture-user","name":"Fixture account"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	defer unblock()
	store := securestore.NewMemory()
	p := newAudiusAuthProvider(audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}, store, "fixture-key", freeLoopbackRedirect(t), "read")
	p.openURL = func(string) error { return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	flows := make(chan api.AuthorizationFlow, 2)
	if err := p.Begin(ctx, "pending-flow", func(flow api.AuthorizationFlow) { flows <- flow }, func(flow api.AuthorizationFlow) { flows <- flow }); err != nil {
		t.Fatal(err)
	}
	pending := waitURLTransition(t, flows)
	parsed, _ := url.Parse(pending.Interaction.URL)
	response, err := upstream.Client().Get(p.redirectURI + "?code=fixture-code&state=" + url.QueryEscape(parsed.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	waitURLTransition(t, entered)
	if err := p.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	unblock()
	if terminal := waitURLTransition(t, flows); terminal.Status != api.FlowCancelled {
		t.Fatalf("late flow = %+v, want cancelled", terminal)
	}
	if _, err := store.Get(audiusSecureService, audiusSecureAccount); !errors.Is(err, securestore.ErrNotFound) {
		t.Fatalf("late flow restored credentials: %v", err)
	}
}
