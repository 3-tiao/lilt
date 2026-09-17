package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

func TestActiveSourceStartsFromPersistedStateAndFreshProjectionDefaultsApple(t *testing.T) {
	fresh, _ := startTestServer(t)
	if fresh.activeSource != "" || fresh.publicActiveSourceLocked() != api.SourceAppleMusic {
		t.Fatalf("fresh active=%q public=%q", fresh.activeSource, fresh.publicActiveSourceLocked())
	}

	dir := shortServerTempDir(t)
	store := state.New(filepath.Join(dir, "state.json"))
	store.LastPlaybackSource = string(api.SourceRadio)
	server, err := Start(Options{SocketPath: filepath.Join(dir, "s.sock"), Engine: fakeengine.NewFakeEngine(), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if server.activeSource != api.SourceRadio {
		t.Fatalf("activeSource=%q, want radio", server.activeSource)
	}
}

func TestSuccessfulPlaybackStartCommitsSessionAndPersistsSource(t *testing.T) {
	dir := shortServerTempDir(t)
	path := filepath.Join(dir, "state.json")
	store := state.New(path)
	server, err := Start(Options{SocketPath: filepath.Join(dir, "s.sock"), Engine: fakeengine.NewFakeEngine(), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	response := call(t, filepath.Join(dir, "s.sock"), "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if !response.OK {
		t.Fatalf("play=%+v", response.Error)
	}
	if server.activeSource != api.SourceAppleMusic || server.playbackGeneration != 1 || server.transportSessionID == "" {
		t.Fatalf("session source=%q generation=%d id=%q", server.activeSource, server.playbackGeneration, server.transportSessionID)
	}
	loaded, loadErr := state.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.LastPlaybackSource != string(api.SourceAppleMusic) {
		t.Fatalf("lastPlaybackSource=%q", loaded.LastPlaybackSource)
	}
	firstSession := server.transportSessionID
	response = call(t, filepath.Join(dir, "s.sock"), "playback.playSongs", map[string]any{"refs": []string{"apple-music:song:1"}})
	if !response.OK || server.playbackGeneration != 2 || server.transportSessionID == firstSession {
		t.Fatalf("second start response=%+v generation=%d id=%q", response.Error, server.playbackGeneration, server.transportSessionID)
	}
}

type failingStartEngine struct{ *fakeengine.FakeEngine }

func (e *failingStartEngine) PlayState(context.Context, core.PlaybackRequest) (core.PlaybackState, error) {
	return core.PlaybackState{}, errors.New("start failed")
}

func TestFailedPlaybackStartKeepsRequestedSourceAndPublishesStopped(t *testing.T) {
	dir := shortServerTempDir(t)
	store := state.New(filepath.Join(dir, "state.json"))
	store.LastPlaybackSource = string(api.SourceRadio)
	server, err := Start(Options{SocketPath: filepath.Join(dir, "s.sock"), Engine: &failingStartEngine{fakeengine.NewFakeEngine()}, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	response := call(t, filepath.Join(dir, "s.sock"), "playback.play", map[string]any{"ref": "apple-music:song:new"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodePlaybackError {
		t.Fatalf("response=%+v", response)
	}
	if server.activeSource != api.SourceAppleMusic || server.playbackGeneration != 1 || server.transportSessionID == "" {
		t.Fatalf("failed session source=%q generation=%d id=%q", server.activeSource, server.playbackGeneration, server.transportSessionID)
	}
	raw, marshalErr := json.Marshal(response.Error.Details["state"])
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var projected api.PlaybackState
	if err := json.Unmarshal(raw, &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Source != api.SourceAppleMusic || projected.Status != "stopped" || len(projected.Queue) != 0 || projected.QueueSource != nil {
		t.Fatalf("failed state=%+v", projected)
	}
	if store.LastPlaybackSource != string(api.SourceRadio) {
		t.Fatalf("failed start persisted source=%q", store.LastPlaybackSource)
	}
}

func TestPlaybackPersistenceFailureIsPartialFailure(t *testing.T) {
	dir := shortServerTempDir(t)
	blockedParent := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(blockedParent, "state.json"))
	server, err := Start(Options{SocketPath: filepath.Join(dir, "s.sock"), Engine: fakeengine.NewFakeEngine(), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	response := call(t, filepath.Join(dir, "s.sock"), "playback.play", map[string]any{"ref": "apple-music:song:1"})
	if response.OK || response.Error == nil || response.Error.Code != api.CodePartialFailure || response.Error.Details["state"] == nil {
		t.Fatalf("response=%+v", response)
	}
	if server.activeSource != api.SourceAppleMusic || server.playbackGeneration != 1 {
		t.Fatalf("playback did not remain committed: source=%q generation=%d", server.activeSource, server.playbackGeneration)
	}
}

func shortServerTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
