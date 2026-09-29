package mpvplayer

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
)

// TestRealMPVPlaybackE2E exercises the parts a fake mpv cannot: that a real mpv
// process actually decodes audio, reports its progress, and is reaped on
// shutdown. It is opt-in because it needs mpv on PATH and real timing.
func TestRealMPVPlaybackE2E(t *testing.T) {
	if os.Getenv("LILT_MPV_E2E") != "1" {
		t.Skip("set LILT_MPV_E2E=1 to run against a real mpv process (requires mpv on PATH)")
	}
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.ServeContent(w, request, "stream.wav", time.Time{}, bytes.NewReader(silenceWAV(10)))
	}))
	defer stream.Close()

	client := New()
	subscription, err := client.SubscribeState(context.Background())
	if err != nil {
		t.Fatalf("SubscribeState: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	state, err := client.RadioPlay(context.Background(), stream.URL+"/stream.wav", "E2E Station")
	if err != nil {
		t.Fatalf("RadioPlay: %v", err)
	}
	if state.Status != "buffering" && state.Status != "playing" {
		t.Fatalf("status = %q after starting a real stream", state.Status)
	}
	playing := waitForState(t, subscription.Updates, func(state core.PlaybackState) bool {
		return state.Status == "playing"
	})
	if !playing.IsLive || playing.Track == nil || playing.Track.Title != "E2E Station" {
		t.Fatalf("playing state = %+v", playing)
	}

	// The sampler must publish an advancing position, which is what the TUI
	// progress line and the URL stall watchdog read.
	advanced := waitForState(t, subscription.Updates, func(state core.PlaybackState) bool {
		return state.Position > 0
	})
	if advanced.Duration <= 0 {
		t.Fatalf("duration = %v, want the decoded stream length", advanced.Duration)
	}

	paused, err := client.PauseState(context.Background())
	if err != nil {
		t.Fatalf("PauseState: %v", err)
	}
	if paused.Status != "paused" {
		t.Fatalf("status = %q, want paused", paused.Status)
	}
	frozen := paused.Position
	time.Sleep(1500 * time.Millisecond)
	after, err := client.State(context.Background())
	if err != nil {
		t.Fatalf("State while paused: %v", err)
	}
	if after.Position != frozen {
		t.Fatalf("position moved from %v to %v while paused", frozen, after.Position)
	}

	client.mu.Lock()
	process := client.cmd.Process
	client.mu.Unlock()
	if process == nil {
		t.Fatal("no mpv process was started")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("mpv is still running after Close")
	}
}

// silenceWAV builds a small mono PCM stream, so the E2E needs no fixture file
// and mpv has something real to decode.
func silenceWAV(seconds int) []byte {
	const rate = 8000
	dataSize := rate * seconds * 2
	var buffer bytes.Buffer
	write := func(value any) { _ = binary.Write(&buffer, binary.LittleEndian, value) }
	buffer.WriteString("RIFF")
	write(uint32(36 + dataSize))
	buffer.WriteString("WAVEfmt ")
	write(uint32(16))
	write(uint16(1)) // PCM
	write(uint16(1)) // mono
	write(uint32(rate))
	write(uint32(rate * 2))
	write(uint16(2))
	write(uint16(16))
	buffer.WriteString("data")
	write(uint32(dataSize))
	buffer.Write(make([]byte, dataSize))
	return buffer.Bytes()
}
