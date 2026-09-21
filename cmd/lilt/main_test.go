package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/server"
	"github.com/caiguo/lilt/internal/state"
)

func TestInitialSourceUsesDescriptorCatalog(t *testing.T) {
	descriptors := []api.SourceDescriptor{{ID: "custom"}, {ID: api.SourceAppleMusic}}
	if got := initialSource("custom", descriptors); got != "custom" {
		t.Fatalf("initialSource(custom) = %q", got)
	}
	if got := initialSource("missing", descriptors); got != string(api.SourceAppleMusic) {
		t.Fatalf("initialSource(missing) = %q", got)
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
