package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/protocol"
)

func shortSocket(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "lilt-"+name+"-"+time.Now().Format("150405.000000000"))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestServerControlsFakeTargetAndCleansSocket(t *testing.T) {
	path := shortSocket(t, "control")
	target := NewFakeTarget()
	server, err := Start(path, target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions = %o, want 0600", info.Mode().Perm())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := Call(ctx, path, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("resume failed: %#v", response)
	}
	response, err = Call(ctx, path, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("status failed: %#v", response)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after close: %v", err)
	}
}
func TestMissingSocketHasStableError(t *testing.T) {
	response, err := Call(context.Background(), shortSocket(t, "missing"), "status")
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error.Code != "no_active_session" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestServerPlaysReference(t *testing.T) {
	path := shortSocket(t, "play")
	target := NewFakeTarget()
	server, err := Start(path, target)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := CallRequest(ctx, path, protocol.SessionRequest{Command: "play", Reference: "song:42"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("play failed: %#v", response)
	}
	state, err := target.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Track == nil || state.Track.ID != "42" {
		t.Fatalf("played track = %#v, want id 42", state.Track)
	}
}

func TestServerRejectsInvalidReference(t *testing.T) {
	path := shortSocket(t, "invalid")
	server, err := Start(path, NewFakeTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := CallRequest(ctx, path, protocol.SessionRequest{Command: "play", Reference: "not-a-reference"})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error.Code != "invalid_reference" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestStartRejectsActiveSocket(t *testing.T) {
	path := shortSocket(t, "active")
	first, err := Start(path, NewFakeTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := Start(path, NewFakeTarget()); err != ErrActive {
		t.Fatalf("got %v, want ErrActive", err)
	}
}
