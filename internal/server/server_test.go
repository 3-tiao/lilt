package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

// startFakeAudius gives every default test server a hermetic Audius upstream.
// Without it, structural tests that only touch discovery (the provider gate)
// would still reach the real network just because the provider is registered.
func startFakeAudius(t *testing.T) *audius.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tracks/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"top1","title":"Trending","permalink":"/artist/top","is_streamable":true,"user":{"name":"Artist"}}]}`))
		case "/playlists/trending":
			_, _ = w.Write([]byte(`{"data":[{"id":"tp1","playlist_name":"Top List"}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	t.Cleanup(upstream.Close)
	return &audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
}

func startTestServer(t *testing.T) (*Server, string) {
	return startTestServerWithEngine(t, fakeengine.NewFakeEngine())
}

func startTestServerWithEngine(t *testing.T, engine Engine) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-srv-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	server, startErr := Start(Options{
		SocketPath:   socket,
		Engine:       engine,
		Store:        state.New(filepath.Join(dir, "state.json")),
		AudiusClient: startFakeAudius(t),
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
	if len(description.Commands) == 0 {
		t.Fatalf("describe = %+v", description)
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
