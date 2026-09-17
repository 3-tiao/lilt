package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-srv-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server, startErr := Start(Options{
		SocketPath: socket,
		Engine:     fakeengine.NewFakeEngine(),
		Store:      state.New(filepath.Join(dir, "state.json")),
		ServerID:   "test-server",
	})
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, socket
}

func call(t *testing.T, socket, command string, params any) api.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := api.Command(ctx, socket, command, params)
	if err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	return response
}

func TestDescribeOverWire(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "api.describe", nil)
	if !response.OK {
		t.Fatalf("describe failed: %+v", response.Error)
	}
	var description api.ApiDescription
	if err := json.Unmarshal(response.Data, &description); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	if description.APIVersion != api.Version || len(description.Commands) == 0 {
		t.Fatalf("describe = %+v", description)
	}
	if response.ServerID != "test-server" {
		t.Fatalf("serverId = %q", response.ServerID)
	}
}

func TestSourcesList(t *testing.T) {
	_, socket := startTestServer(t)
	response := call(t, socket, "sources.list", nil)
	if !response.OK {
		t.Fatalf("sources.list failed: %+v", response.Error)
	}
	var sources []api.SourceDescriptor
	if err := json.Unmarshal(response.Data, &sources); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, source := range sources {
		if source.ID == api.SourceAppleMusic {
			found = true
		}
	}
	if !found {
		t.Fatalf("sources = %+v, want apple-music", sources)
	}
}

func TestSessionStatusQueueProjection(t *testing.T) {
	_, socket := startTestServer(t)
	without := call(t, socket, "session.status", nil)
	if !without.OK {
		t.Fatalf("status failed: %+v", without.Error)
	}
	var status api.PlaybackStatus
	if err := json.Unmarshal(without.Data, &status); err != nil {
		t.Fatal(err)
	}
	if status.Source != api.SourceAppleMusic {
		t.Fatalf("source = %q", status.Source)
	}

	with := call(t, socket, "session.status", map[string]any{"includeQueue": true})
	if !with.OK {
		t.Fatalf("status(queue) failed: %+v", with.Error)
	}
	var state api.PlaybackState
	if err := json.Unmarshal(with.Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Source != api.SourceAppleMusic {
		t.Fatalf("queue state source = %q", state.Source)
	}
}

func TestUnknownCommandRejected(t *testing.T) {
	_, socket := startTestServer(t)
	unknown := call(t, socket, "does.not.exist", nil)
	if unknown.Error == nil || unknown.Error.Code != api.CodeUnknownCommand {
		t.Fatalf("unknown = %+v", unknown.Error)
	}
}

func TestVersionMismatchRejected(t *testing.T) {
	_, socket := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := api.Call(ctx, socket, api.Request{Version: 1, RequestID: "x", Command: "session.status"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("response = %+v", response)
	}
}

func TestShutdownSignals(t *testing.T) {
	server, socket := startTestServer(t)
	response := call(t, socket, "session.shutdown", nil)
	if !response.OK {
		t.Fatalf("shutdown failed: %+v", response.Error)
	}
	select {
	case <-server.ShutdownRequested():
	case <-time.After(time.Second):
		t.Fatal("shutdown was not signalled")
	}
}

func TestSecondServerConflicts(t *testing.T) {
	_, socket := startTestServer(t)
	if _, err := Start(Options{SocketPath: socket, Engine: fakeengine.NewFakeEngine()}); err == nil {
		t.Fatal("second Start succeeded on the same socket")
	}
}
