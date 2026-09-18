package server

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/fakeengine"
	"github.com/caiguo/lilt/internal/state"
)

// routingMusic counts MusicKit-helper calls so a test can assert Apple routes
// to it and never to the audio helper.
type routingMusic struct {
	*fakeengine.FakeEngine
	mu         sync.Mutex
	playStates int
	stops      int
	closed     bool
}

func (e *routingMusic) PlayState(ctx context.Context, request core.PlaybackRequest) (core.PlaybackState, error) {
	e.mu.Lock()
	e.playStates++
	e.mu.Unlock()
	return e.FakeEngine.PlayState(ctx, request)
}

func (e *routingMusic) Stop(context.Context) (core.PlaybackState, error) {
	e.mu.Lock()
	e.stops++
	e.mu.Unlock()
	return core.PlaybackState{Status: "stopped", Mode: "none"}, nil
}

func (e *routingMusic) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

// routingAudio is the AVPlayer helper: it records radio/url starts and is
// closed when MusicKit takes over.
type routingAudio struct {
	mu        sync.Mutex
	radio     int
	url       int
	stops     int
	closes    int
	status    string
	subscribe chan core.PlaybackStateUpdate
}

func (a *routingAudio) State(context.Context) (core.PlaybackState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status == "" {
		a.status = "stopped"
	}
	return core.PlaybackState{Status: a.status, Mode: "stream"}, nil
}
func (a *routingAudio) PauseState(context.Context) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "paused", Mode: "stream"}, nil
}
func (a *routingAudio) ResumeState(context.Context) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "stream"}, nil
}
func (a *routingAudio) Stop(context.Context) (core.PlaybackState, error) {
	a.mu.Lock()
	a.stops++
	a.mu.Unlock()
	return core.PlaybackState{Status: "stopped", Mode: "none"}, nil
}
func (a *routingAudio) RadioPlay(context.Context, string, string) (core.PlaybackState, error) {
	a.mu.Lock()
	a.radio++
	a.status = "playing"
	a.mu.Unlock()
	return core.PlaybackState{Status: "playing", Mode: "stream", IsLive: true}, nil
}
func (a *routingAudio) RadioStop(context.Context) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "stopped", Mode: "none"}, nil
}
func (a *routingAudio) Probe(context.Context, string, int) (core.RadioProbeResult, error) {
	return core.RadioProbeResult{}, nil
}
func (a *routingAudio) SubscribeState(context.Context) (core.StateSubscription, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subscribe == nil {
		a.subscribe = make(chan core.PlaybackStateUpdate, 4)
	}
	return core.StateSubscription{Updates: a.subscribe}, nil
}
func (a *routingAudio) UnsubscribeState(context.Context) error { return nil }
func (a *routingAudio) Close() error {
	a.mu.Lock()
	a.closes++
	a.mu.Unlock()
	return nil
}
func (a *routingAudio) PlayURL(context.Context, core.URLPlaybackTarget) (core.PlaybackState, error) {
	a.mu.Lock()
	a.url++
	a.status = "playing"
	a.mu.Unlock()
	return core.PlaybackState{Status: "playing", Mode: "url", Position: 1, Duration: 120}, nil
}
func (a *routingAudio) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (a *routingAudio) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (a *routingAudio) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
}
func (a *routingAudio) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}

func startRoutingServer(t *testing.T, music *routingMusic, audio *routingAudio, upstream string) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lilt-route-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	options := Options{
		SocketPath:         socket,
		Engine:             music,
		Store:              state.New(filepath.Join(dir, "state.json")),
		AudioEngineFactory: func() (AudioEngine, error) { return audio, nil },
	}
	if upstream != "" {
		client := audius.Client{BaseURL: upstream}
		options.AudiusClient = &client
	}
	srv, err := Start(options)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, socket
}

func TestHelperRoutingByTransportAndSourceSwitch(t *testing.T) {
	upstream := audiusPlaybackUpstream(nil)
	defer upstream.Close()
	music := &routingMusic{FakeEngine: fakeengine.NewFakeEngine()}
	audio := &routingAudio{}
	_, socket := startRoutingServer(t, music, audio, upstream.URL)

	// Radio -> audio helper (radioPlay).
	response := call(t, socket, "playback.play", map[string]any{"ref": "https://radio.example/live"})
	if !response.OK {
		t.Fatalf("radio play: %+v", response.Error)
	}
	audio.mu.Lock()
	radioCalls := audio.radio
	audio.mu.Unlock()
	if radioCalls != 1 {
		t.Fatalf("audio.radioPlay = %d, want 1", radioCalls)
	}
	music.mu.Lock()
	musicPlays := music.playStates
	music.mu.Unlock()
	if musicPlays != 0 {
		t.Fatalf("music helper handled radio: playStates=%d", musicPlays)
	}

	// Audius -> audio helper (urlPlay via the URL queue transport).
	response = call(t, socket, "playback.play", map[string]any{"ref": "audius:playlist:p1"})
	if !response.OK {
		t.Fatalf("audius play: %+v", response.Error)
	}
	audio.mu.Lock()
	urlCalls := audio.url
	audio.mu.Unlock()
	if urlCalls == 0 {
		t.Fatal("audio helper did not receive urlPlay for Audius")
	}

	// Apple -> music helper, and the audio helper is stopped/closed.
	response = call(t, socket, "playback.play", map[string]any{"ref": "apple-music:song:1440845629"})
	if !response.OK {
		t.Fatalf("apple play: %+v", response.Error)
	}
	music.mu.Lock()
	musicPlays, musicStops := music.playStates, music.stops
	music.mu.Unlock()
	if musicPlays != 1 {
		t.Fatalf("music.playState = %d, want 1", musicPlays)
	}
	_ = musicStops
	audio.mu.Lock()
	audioStops, audioCloses := audio.stops, audio.closes
	audio.mu.Unlock()
	if audioStops == 0 || audioCloses == 0 {
		t.Fatalf("audio helper kept alive after switching to Apple: stops=%d closes=%d", audioStops, audioCloses)
	}
}

func TestAudioHelperRebuildsAfterStreamClose(t *testing.T) {
	music := &routingMusic{FakeEngine: fakeengine.NewFakeEngine()}
	var mu sync.Mutex
	built := 0
	instances := make(chan *routingAudio, 4)
	factory := func() (AudioEngine, error) {
		audio := &routingAudio{}
		mu.Lock()
		built++
		mu.Unlock()
		instances <- audio
		return audio, nil
	}
	dir, err := os.MkdirTemp("/tmp", "lilt-audio-rebuild-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := Start(Options{
		SocketPath:         socket,
		Engine:             music,
		Store:              state.New(filepath.Join(dir, "state.json")),
		AudioEngineFactory: factory,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Starting Radio launches the audio helper and its watcher.
	if response := call(t, socket, "playback.play", map[string]any{"ref": "https://radio.example/live"}); !response.OK {
		t.Fatalf("radio play: %+v", response.Error)
	}
	first := <-instances
	first.mu.Lock()
	updates := first.subscribe
	first.mu.Unlock()
	if updates == nil {
		t.Fatal("audio helper was not subscribed")
	}

	// Simulate helper death: the update stream closes.
	close(updates)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := built
		mu.Unlock()
		if count >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	count := built
	mu.Unlock()
	t.Fatalf("audio factory built %d helpers, want at least 2", count)
}
