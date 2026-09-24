package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/client"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/server"
	"github.com/caiguo/lilt/internal/state"
)

func TestInitialSourceUsesDescriptorCatalog(t *testing.T) {
	descriptors := []api.SourceDescriptor{
		{ID: "custom", Available: true},
		{ID: api.SourceAppleMusic, Available: true},
	}
	if got := initialSource("custom", descriptors); got != "custom" {
		t.Fatalf("initialSource(custom) = %q", got)
	}
	if got := initialSource("missing", descriptors); got != string(api.SourceAppleMusic) {
		t.Fatalf("initialSource(missing) = %q", got)
	}
}

// An unavailable first screen must not be what a session opens on: Linux has no
// MusicKit, so Apple Music is permanently unavailable there — and `state.get`
// reports it as lastSource on a fresh state.
func TestInitialSourceSkipsAnUnavailableRememberedSource(t *testing.T) {
	descriptors := []api.SourceDescriptor{
		{ID: api.SourceAppleMusic, Available: false, Availability: api.AvailabilityUnavailable},
		{ID: api.SourceAudius, Available: true, Availability: api.AvailabilityReady},
		{ID: api.SourceRadio, Available: true, Availability: api.AvailabilityReady},
	}
	if got := initialSource(api.SourceAppleMusic, descriptors); got != string(api.SourceAudius) {
		t.Fatalf("initialSource(remembered apple-music) = %q, want the highest-priority available source", got)
	}
	if got := initialSource("", descriptors); got != string(api.SourceAudius) {
		t.Fatalf("initialSource = %q, want the highest-priority available source", got)
	}
	// A remembered source that is usable still wins.
	if got := initialSource(api.SourceRadio, descriptors); got != string(api.SourceRadio) {
		t.Fatalf("initialSource(remembered radio) = %q", got)
	}
	// With nothing available, precedence decides rather than an empty screen.
	none := []api.SourceDescriptor{
		{ID: api.SourceAppleMusic, Available: false},
		{ID: api.SourceRadio, Available: false},
	}
	if got := initialSource("", none); got != string(api.SourceAppleMusic) {
		t.Fatalf("initialSource(all unavailable) = %q", got)
	}
	if got := initialSource(api.SourceRadio, none); got != string(api.SourceRadio) {
		t.Fatalf("initialSource(all unavailable, remembered radio) = %q", got)
	}
}

func TestAwaitServerReadyWaitsForAPIResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var probes atomic.Int32
	err := awaitServerReady(ctx, func(context.Context) bool {
		return probes.Add(1) >= 3
	}, make(chan error))
	if err != nil {
		t.Fatalf("awaitServerReady: %v", err)
	}
	if got := probes.Load(); got != 3 {
		t.Fatalf("probes = %d, want 3", got)
	}
}

func TestAwaitServerReadyReportsEarlyExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exited := make(chan error, 1)
	exited <- errors.New("startup failed")
	err := awaitServerReady(ctx, func(context.Context) bool { return false }, exited)
	if err == nil || !strings.Contains(err.Error(), "startup failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestErrorResponsePassesServerErrorsThrough(t *testing.T) {
	serverErr := api.Errorf(api.CodeInvalidReference, "bad ref")
	response := api.Failure("r1", serverErr)
	got := errorResponse(response, serverErr)
	if got.Error == nil || got.Error.Code != api.CodeInvalidReference || got.RequestID != "r1" {
		t.Fatalf("server error = %+v", got)
	}
}

func TestFakeModeRejectsAccountSetup(t *testing.T) {
	t.Setenv("LILT_FAKE_PLAYER", "1")
	if code := runJamendo([]string{"setup", "--client-id", "test"}, true); code == 0 {
		t.Fatal("fake mode must reject account setup before accessing the Keychain or network")
	}
}

func TestJamendoSetupValidatesBeforeWriting(t *testing.T) {
	mode := "valid"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantID := "client-1"
		if mode != "valid" {
			wantID = "client-2"
		}
		if r.URL.Query().Get("client_id") != wantID || r.URL.Query().Get("limit") != "1" {
			t.Fatalf("query=%v", r.URL.Query())
		}
		if mode == "valid" {
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":5},"results":[]}`))
	}))
	defer upstream.Close()
	store := securestore.NewMemory()
	client := jamendo.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}

	if apiErr := setupJamendo(context.Background(), store, client, "client-1"); apiErr != nil {
		t.Fatalf("setup: %v", apiErr)
	}
	if got, err := jamendo.LoadClientID(store); err != nil || got != "client-1" {
		t.Fatalf("stored client_id=%q err=%v", got, err)
	}

	mode = "invalid"
	if apiErr := setupJamendo(context.Background(), store, client, "client-2"); apiErr == nil || apiErr.Code != api.CodeAuthorizationFailed {
		t.Fatalf("invalid setup error=%v", apiErr)
	}
	if got, err := jamendo.LoadClientID(store); err != nil || got != "client-1" {
		t.Fatalf("failed validation changed the stored client_id: %q err=%v", got, err)
	}
}

func TestParseJamendoSetupArgs(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
		err  bool
	}{
		{args: []string{"setup"}, want: ""},
		{args: []string{"setup", "--client-id", " abc "}, want: "abc"},
		{args: []string{"setup", "--client-id=abc"}, want: "abc"},
		{args: []string{"status"}, err: true},
		{args: []string{"setup", "--unknown"}, err: true},
	} {
		got, err := parseJamendoSetupArgs(test.args)
		if (err != nil) != test.err || got != test.want {
			t.Fatalf("args=%v got=%q err=%v", test.args, got, err)
		}
	}
}

func TestErrorResponseMapsClientFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"transport", api.ErrTransport, api.CodeSessionUnavailable},
		{"no session", api.ErrNoActiveSession, api.CodeNoActiveSession},
		{"usage", errors.New("usage"), api.CodeInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := errorResponse(api.Response{}, test.err)
			if got.Error == nil || got.Error.Code != test.want {
				t.Fatalf("code = %+v, want %s", got.Error, test.want)
			}
		})
	}
}

// serverRespondsSoon is the TUI's bounded startup wait (batch
// 2026-09-21-r12: one-shot probe raced server bind and killed the TUI).
func TestServerRespondsSoonWithoutServerFailsFast(t *testing.T) {
	t.Setenv("LILT_SOCKET", "/tmp/lilt-no-such-server.sock")
	start := time.Now()
	if serverRespondsSoon(200 * time.Millisecond) {
		t.Fatal("no server answered but the wait reported success")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("wait overshot: %v", elapsed)
	}
}

func TestServerRespondsSoonDetectsRunningServer(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-cmd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Setenv("LILT_SOCKET", socket)
	srv, err := server.Start(server.Options{
		SocketPath: socket,
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if !serverRespondsSoon(2 * time.Second) {
		t.Fatal("running server was not detected")
	}
}

// A server with no playback engine answers `session.status` with an error. That
// is still a live server, and the readiness probe must not read it as "no
// server": Linux has no engine until the first play, so the whole TUI
// auto-start path depended on this.
func TestServerRespondsSoonDetectsServerWithoutPlaybackEngine(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-cmd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Setenv("LILT_SOCKET", socket)
	srv, err := server.Start(server.Options{
		SocketPath: socket,
		Store:      state.New(filepath.Join(dir, "state.json")),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if !serverRespondsSoon(2 * time.Second) {
		t.Fatal("a server without a playback engine was not detected")
	}
}

func TestRenderHuman(t *testing.T) {
	t.Run("search renders groups with refs", func(t *testing.T) {
		data := json.RawMessage(`{"source":"apple-music","term":"x","groups":{"songs":[{"kind":"song","title":"A","artist":"B","ref":"apple-music:song:1"},{"kind":"song","title":"","ref":""}]}}`)
		out := renderHuman("search", "", data)
		if !strings.Contains(out, "── Songs ──") || !strings.Contains(out, "A — B") || !strings.Contains(out, "apple-music:song:1") {
			t.Fatalf("search render = %q", out)
		}
	})
	t.Run("status renders fields", func(t *testing.T) {
		data := json.RawMessage(`{"status":"playing","source":"apple-music","track":{"kind":"song","title":"T","artist":"A","ref":"apple-music:song:9"},"position":61,"duration":121,"mode":"full"}`)
		out := renderHuman("status", "", data)
		if !strings.Contains(out, "status:  playing") || !strings.Contains(out, "1:01 / 2:01") || !strings.Contains(out, "T — A") {
			t.Fatalf("status render = %q", out)
		}
	})
	t.Run("queue marks current", func(t *testing.T) {
		data := json.RawMessage(`{"index":1,"items":[{"title":"One"},{"title":"Two","artist":"X"}]}`)
		out := renderHuman("queue", "", data)
		if !strings.Contains(out, "▶  1  Two — X") {
			t.Fatalf("queue render = %q", out)
		}
	})
	t.Run("empty list renders none", func(t *testing.T) {
		data := json.RawMessage(`[]`)
		if out := renderHuman("recent", "", data); out != "(none)\n" {
			t.Fatalf("recent render = %q", out)
		}
	})
	t.Run("auth list", func(t *testing.T) {
		data := json.RawMessage(`[{"source":"radio","status":"not_required"}]`)
		if out := renderHuman("auth", "status", data); out != "radio        not_required\n" {
			t.Fatalf("auth render = %q", out)
		}
	})
	t.Run("empty payload keeps ok behavior", func(t *testing.T) {
		data := json.RawMessage(`{"stopped":false}`)
		if out := renderHuman("quit", "", data); out != "" {
			t.Fatalf("quit render = %q, want empty", out)
		}
	})
}

// The auth flow's lifetime belongs to the server (its provider-declared
// budget); the CLI used to impose its own 90s deadline and die with a false
// timeout while the sign-in window was still valid. awaitAuthFlow must keep
// polling until a terminal state, and on interrupt must say where the flow
// still lives (usability probe 2026-09-23-apple-browser-preview, D-1).
func TestAwaitAuthFlowPollsUntilTerminal(t *testing.T) {
	flow := api.AuthorizationFlow{FlowID: "f1", Status: api.FlowPending}
	calls := 0
	got, err := awaitAuthFlow(context.Background(), "apple-music", flow, false,
		func(context.Context) (api.AuthorizationFlow, error) {
			calls++
			if calls >= 2 {
				return api.AuthorizationFlow{FlowID: "f1", Status: api.AuthAuthorized}, nil
			}
			return api.AuthorizationFlow{FlowID: "f1", Status: api.FlowPending}, nil
		},
		func(string) {},
	)
	if err != nil {
		t.Fatalf("awaitAuthFlow: %v", err)
	}
	if got.Status != api.AuthAuthorized || calls != 2 {
		t.Fatalf("status = %q after %d polls", got.Status, calls)
	}
}

func TestAwaitAuthFlowInterruptReportsTheLiveFlow(t *testing.T) {
	flow := api.AuthorizationFlow{FlowID: "f7", Status: api.FlowPending}
	ctx, cancel := context.WithCancel(context.Background())
	var lines []string
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := awaitAuthFlow(ctx, "apple-music", flow, false,
		func(context.Context) (api.AuthorizationFlow, error) {
			return flow, nil
		},
		func(line string) { lines = append(lines, line) },
	)
	if err == nil {
		t.Fatal("interrupted wait must surface an error")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "f7") || !strings.Contains(joined, "auth status apple-music") || !strings.Contains(joined, "auth cancel f7") {
		t.Fatalf("interrupt hint must name the live flow and both follow-ups:\n%s", joined)
	}
}

func TestAwaitAuthFlowLateURLStillPrinted(t *testing.T) {
	flow := api.AuthorizationFlow{FlowID: "f2", Status: api.FlowPending}
	calls := 0
	var lines []string
	got, err := awaitAuthFlow(context.Background(), "apple-music", flow, false,
		func(context.Context) (api.AuthorizationFlow, error) {
			calls++
			if calls == 2 {
				return api.AuthorizationFlow{FlowID: "f2", Status: api.FlowPending, Interaction: api.Interaction{URL: "https://music.apple.com/sign-in"}}, nil
			}
			if calls >= 3 {
				return api.AuthorizationFlow{FlowID: "f2", Status: api.AuthAuthorized}, nil
			}
			return flow, nil
		},
		func(line string) { lines = append(lines, line) },
	)
	if err != nil {
		t.Fatalf("awaitAuthFlow: %v", err)
	}
	if got.Status != api.AuthAuthorized {
		t.Fatalf("status = %q", got.Status)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "https://music.apple.com/sign-in") {
		t.Fatalf("late URL never printed:\n%s", joined)
	}
}

// quit must not return while the server is still answering. An explicit
// engine switch after shutdown must not probe a draining server and attach
// to its dead socket ("session transport failed: EOF"). With no server,
// quit remains a successful no-op that never starts one. The wait polls
// until nothing answers or its deadline passes.
func TestWaitForServerGoneStopsWhenTheSocketStopsAnswering(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	calls := 0
	waitForServerGone(deadline, func() bool {
		calls++
		return calls < 3
	})
	if calls != 3 {
		t.Fatalf("probes = %d, want the poll to stop at the first false", calls)
	}
}

func TestWaitForServerGoneRespectsTheDeadline(t *testing.T) {
	deadline := time.Now().Add(300 * time.Millisecond)
	waitForServerGone(deadline, func() bool { return true })
	if time.Now().Before(deadline.Add(-200 * time.Millisecond)) {
		t.Fatalf("returned before the deadline")
	}
}

// With no server at all, quit is a successful no-op that never starts one.
func TestQuitWithoutAServerStaysANoOp(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-quit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("LILT_SOCKET", filepath.Join(dir, "quit.sock"))
	t.Setenv("LILT_STATE", filepath.Join(dir, "state.json"))
	t.Setenv("LILT_CONFIG", filepath.Join(dir, "config"))
	t.Setenv("LILT_RADIO_CACHE", filepath.Join(dir, "radio"))

	if code := runRemote("quit", nil, true); code != 0 {
		t.Fatalf("quit with no server: exit %d", code)
	}
	if client.New(api.SocketPath()).ServerResponds(context.Background()) {
		t.Fatal("quit on no session started a server")
	}
}
