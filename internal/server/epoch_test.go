package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/fakeengine"
	"github.com/3-tiao/lilt/internal/securestore"
	"github.com/3-tiao/lilt/internal/state"
)

// rawCall sends one wire request exactly as written, bypassing the client-side
// epoch bootstrap, so the server's epoch rule is observed directly.
func rawCall(t *testing.T, socket string, request api.Request) api.Response {
	t.Helper()
	if request.RequestID == "" {
		request.RequestID = api.NewRequestID()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := api.Call(ctx, socket, request)
	if err != nil {
		t.Fatalf("%s: %v", request.Command, err)
	}
	return response
}

func epochOf(t *testing.T, socket string) string {
	t.Helper()
	response := rawCall(t, socket, api.Request{Command: "session.status"})
	if !response.OK {
		t.Fatalf("session.status: %+v", response.Error)
	}
	if response.ServerInstanceID == "" {
		t.Fatal("session.status did not report a serverInstanceId")
	}
	return response.ServerInstanceID
}

func themeOf(t *testing.T, socket string) string {
	t.Helper()
	response := rawCall(t, socket, api.Request{Command: "state.get"})
	if !response.OK {
		t.Fatalf("state.get: %+v", response.Error)
	}
	var appState api.AppState
	if err := json.Unmarshal(response.Data, &appState); err != nil {
		t.Fatal(err)
	}
	return appState.Theme
}

// A mutation must name the server instance it was composed against: an omitted
// epoch is invalid_request and a stale epoch is conflict, both with no side
// effect. A pure query may omit it.
func TestMutationEpochIsRequiredAndSideEffectFree(t *testing.T) {
	_, socket := startTestServer(t)
	epoch := epochOf(t, socket)

	missing := rawCall(t, socket, api.Request{RequestID: "missing", Command: "ui.set",
		Params: json.RawMessage(`{"theme":"dark"}`)})
	if missing.Error == nil || missing.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("mutation without epoch = %+v, want invalid_request", missing.Error)
	}

	stale := rawCall(t, socket, api.Request{RequestID: "stale", Command: "ui.set",
		IfServerInstanceID: "not-this-server", Params: json.RawMessage(`{"theme":"dark"}`)})
	if stale.Error == nil || stale.Error.Code != api.CodeConflict {
		t.Fatalf("mutation with stale epoch = %+v, want conflict", stale.Error)
	}
	if reason, _ := stale.Error.Details["reason"].(string); reason != "server_epoch" {
		t.Fatalf("conflict details.reason = %q, want server_epoch", reason)
	}
	if reported, _ := stale.Error.Details["serverInstanceId"].(string); reported != epoch {
		t.Fatalf("conflict reported epoch %q, want %q", reported, epoch)
	}
	if theme := themeOf(t, socket); theme == "dark" {
		t.Fatal("a rejected mutation changed persistent state")
	}

	applied := rawCall(t, socket, api.Request{RequestID: "applied", Command: "ui.set",
		IfServerInstanceID: epoch, Params: json.RawMessage(`{"theme":"dark"}`)})
	if !applied.OK {
		t.Fatalf("mutation with epoch: %+v", applied.Error)
	}
	if theme := themeOf(t, socket); theme != "dark" {
		t.Fatalf("theme after accepted mutation = %q, want dark", theme)
	}

	// Queries may omit the epoch, and every response reports it.
	query := rawCall(t, socket, api.Request{RequestID: "query", Command: "sources.list"})
	if !query.OK || query.ServerInstanceID != epoch {
		t.Fatalf("query = ok:%v epoch:%q, want ok with %q", query.OK, query.ServerInstanceID, epoch)
	}
	if applied.ServerInstanceID != epoch {
		t.Fatalf("accepted mutation response epoch = %q, want %q", applied.ServerInstanceID, epoch)
	}
}

// The watch handshake response carries the epoch, which is how a long-lived
// client learns whether a reconnect reached the same process. The snapshot does
// not repeat it: one field on the response that delivers the snapshot is enough.
func TestWatchHandshakeCarriesServerInstanceID(t *testing.T) {
	_, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, watcher, err := api.Watch(ctx, socket, []string{"playback"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if !response.OK {
		t.Fatalf("watch: %+v", response.Error)
	}
	if response.ServerInstanceID != epoch {
		t.Fatalf("watch response epoch = %q, want %q", response.ServerInstanceID, epoch)
	}
	var snapshot api.WatchSnapshot
	if err := json.Unmarshal(response.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Data), "\"sequence\"") {
		t.Fatalf("snapshot does not carry a sequence: %s", response.Data)
	}
	if strings.Contains(string(response.Data), "serverInstanceId") {
		t.Fatalf("snapshot repeats the epoch that the response already carries: %s", response.Data)
	}
}

// startIsolatedServer starts one server on a caller-owned socket path so a test
// can stop it and start another process on the same path and state root.
func startIsolatedServer(t *testing.T, dir, socket string) *Server {
	t.Helper()
	// Load, not New: the second process must inherit what the first persisted,
	// which is exactly what a restart on the same state root does.
	store, err := state.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	server, err := Start(Options{
		SocketPath:  socket,
		Engine:      fakeengine.NewFakeEngine(),
		Store:       store,
		SecureStore: securestore.NewMemory(),
		LockPath:    filepath.Join(dir, "server.lock"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return server
}

// A new process on the same socket and state root is a new epoch, so a request
// composed against the old one is rejected instead of silently targeting the
// new server. This is the restart case the epoch exists for: not concurrent
// servers, but the same root over time.
func TestRestartRejectsStaleEpochWithoutReexecuting(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-epoch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	first := startIsolatedServer(t, dir, socket)
	firstEpoch := epochOf(t, socket)
	// Seed a value the second process would inherit if the mutation ran.
	if response := rawCall(t, socket, api.Request{RequestID: "seed", Command: "ui.set",
		IfServerInstanceID: firstEpoch, Params: json.RawMessage(`{"theme":"light"}`)}); !response.OK {
		t.Fatalf("seed: %+v", response.Error)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first server: %v", err)
	}

	second := startIsolatedServer(t, dir, socket)
	t.Cleanup(func() { _ = second.Close() })
	secondEpoch := epochOf(t, socket)
	if secondEpoch == firstEpoch {
		t.Fatal("a restarted server reused the previous epoch")
	}

	// The retried request id is deliberately the same: without the epoch rule a
	// fresh in-memory dedup ledger cannot tell that this already ran.
	retry := rawCall(t, socket, api.Request{RequestID: "seed", Command: "ui.set",
		IfServerInstanceID: firstEpoch, Params: json.RawMessage(`{"theme":"dark"}`)})
	if retry.Error == nil || retry.Error.Code != api.CodeConflict {
		t.Fatalf("stale-epoch retry = %+v, want conflict", retry.Error)
	}
	if theme := themeOf(t, socket); theme != "light" {
		t.Fatalf("theme after stale-epoch retry = %q, want the inherited light", theme)
	}

	// The caller re-reads and decides again; the same intent now succeeds.
	reapplied := rawCall(t, socket, api.Request{RequestID: "seed", Command: "ui.set",
		IfServerInstanceID: secondEpoch, Params: json.RawMessage(`{"theme":"dark"}`)})
	if !reapplied.OK {
		t.Fatalf("reapplied mutation: %+v", reapplied.Error)
	}
	if theme := themeOf(t, socket); theme != "dark" {
		t.Fatalf("theme after reapplied mutation = %q, want dark", theme)
	}
}

// The client helper bootstraps the epoch once, attaches it automatically, and
// drops a stale cached epoch on server_epoch conflict without retrying the
// intent itself: the next explicit call bootstraps fresh.
func TestClientAttachesEpochAndRecoversAfterRestart(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lilt-epoch-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	first := startIsolatedServer(t, dir, socket)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	response, err := api.Command(ctx, socket, "ui.set", map[string]any{"theme": "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("first ui.set: %+v", response.Error)
	}
	cached := api.Epoch(socket)
	if cached == "" {
		t.Fatal("client did not cache the epoch from the response")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := startIsolatedServer(t, dir, socket)
	t.Cleanup(func() { _ = second.Close() })
	conflicted, err := api.Command(ctx, socket, "ui.set", map[string]any{"theme": "light"})
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Error == nil || conflicted.Error.Code != api.CodeConflict {
		t.Fatalf("stale cached epoch = %+v, want conflict", conflicted.Error)
	}
	if api.Epoch(socket) != "" {
		t.Fatal("a server_epoch conflict left the stale epoch cached")
	}
	if theme := themeOf(t, socket); theme != "dark" {
		t.Fatalf("theme after rejected call = %q, want the inherited dark", theme)
	}

	// A fresh explicit call bootstraps the new epoch and succeeds.
	recovered, err := api.Command(ctx, socket, "ui.set", map[string]any{"theme": "light"})
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.OK {
		t.Fatalf("recovered ui.set: %+v", recovered.Error)
	}
	if theme := themeOf(t, socket); theme != "light" {
		t.Fatalf("theme after recovered call = %q, want light", theme)
	}
}

// radio.search and radio.probe persist the radio cache, so they are not pure
// queries: they must claim an epoch like any other side-effecting command.
func TestRadioCacheWritersRequireAnEpoch(t *testing.T) {
	_, socket := startTestServer(t)
	for _, command := range []string{"radio.search", "radio.probe"} {
		response := rawCall(t, socket, api.Request{RequestID: "no-epoch-" + command, Command: command,
			Params: json.RawMessage(`{"name":"jazz"}`)})
		if response.Error == nil || response.Error.Code != api.CodeInvalidRequest {
			t.Fatalf("%s without epoch = %+v, want invalid_request", command, response.Error)
		}
	}
}

// Every command with side effects must name the server instance, and every
// command classified as a pure query must not. This verifies that the epoch
// gate and api.describe read the same classification; it cannot catch a
// command whose label itself is wrong (a mutation listed as a query would
// skip the rule and pass here) — that is protected by the catalog's
// fail-closed default plus the explicit pins for the cache-writing radio
// commands. The check runs before param validation, so an empty params object
// still reaches it.
func TestEpochRequirementMatchesEveryCommand(t *testing.T) {
	_, socket := startTestServer(t)
	registry := api.NewRegistry()
	for _, command := range registry.List() {
		if command == "session.watch" {
			continue // long-lived connection; covered by TestWatchHandshakeCarriesServerInstanceID
		}
		response := rawCall(t, socket, api.Request{RequestID: "classify-" + command, Command: command})
		missingEpoch := response.Error != nil && strings.Contains(response.Error.Message, "ifServerInstanceId is required")
		switch {
		case registry.Query(command) && missingEpoch:
			t.Errorf("%s is classified as a query but demands an epoch", command)
		case !registry.Query(command) && !missingEpoch:
			t.Errorf("%s has side effects but was accepted without an epoch: %+v", command, response.Error)
		}
	}
}

// A request rejected for a missing epoch never reaches dispatch, so it leaves no
// ledger entry behind: the same requestId is still usable for the real command.
func TestEpochRejectionLeavesNoLedgerEntry(t *testing.T) {
	_, socket := startTestServer(t)
	epoch := epochOf(t, socket)
	const requestID = "epoch-rejected-once"

	rejected := rawCall(t, socket, api.Request{RequestID: requestID, Command: "ui.set",
		Params: json.RawMessage(`{"theme":"dark"}`)})
	if rejected.Error == nil || rejected.Error.Code != api.CodeInvalidRequest {
		t.Fatalf("mutation without epoch = %+v, want invalid_request", rejected.Error)
	}
	// Reusing the id with the same params succeeds, which is the observable proof
	// that no entry was registered: a tracked entry would have replayed the
	// rejection from the cache instead of executing.
	accepted := rawCall(t, socket, api.Request{RequestID: requestID, Command: "ui.set",
		IfServerInstanceID: epoch, Params: json.RawMessage(`{"theme":"dark"}`)})
	if !accepted.OK {
		t.Fatalf("the same requestId was not reusable after the rejection: %+v", accepted.Error)
	}
}

// An unregistered command has no side effects to protect, so it must report the
// stable unknown_command whether or not the caller sent an epoch. The stale
// epoch matters most: conflict here would tell the client to re-read state on a
// command that can never exist.
func TestUnknownCommandReportsUnknownCommandRegardlessOfEpoch(t *testing.T) {
	_, socket := startTestServer(t)
	missing := rawCall(t, socket, api.Request{RequestID: "unknown-no-epoch", Command: "no.such.command"})
	if missing.Error == nil || missing.Error.Code != api.CodeUnknownCommand {
		t.Fatalf("unknown command without epoch = %+v, want unknown_command", missing.Error)
	}
	stale := rawCall(t, socket, api.Request{RequestID: "unknown-stale-epoch", Command: "no.such.command",
		IfServerInstanceID: "0123456789abcdef01234567"})
	if stale.Error == nil || stale.Error.Code != api.CodeUnknownCommand {
		t.Fatalf("unknown command with a stale epoch = %+v, want unknown_command", stale.Error)
	}
}
