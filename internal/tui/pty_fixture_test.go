package tui

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/radio"
)

// longOptionsRadio provides deterministic directory choices without HTTP. It
// exists only in the test binary used for a silent, opt-in terminal probe.
type longOptionsRadio struct{ fakeRadio }

func (longOptionsRadio) Tags(context.Context) ([]radio.Tag, error) {
	items := make([]radio.Tag, 25)
	for i := range items {
		items[i] = radio.Tag{Name: fmt.Sprintf("Genre %02d", i), StationCount: 100 - i}
	}
	return items, nil
}

func (longOptionsRadio) Countries(context.Context) ([]radio.Country, error) {
	items := make([]radio.Country, 25)
	for i := range items {
		items[i] = radio.Country{Name: fmt.Sprintf("Country %02d", i), Code: fmt.Sprintf("%02d", i), StationCount: 100 - i}
	}
	return items, nil
}

// TestPTYFixture runs a real Bubble Tea terminal program only when explicitly
// requested. Ordinary tests skip it; a private PTY can drive Enter/j/k/q and
// capture the screen while every provider and playback fact remains in memory.
// This probes renderer/keyboard geometry, not the production server or media.
func TestPTYFixture(t *testing.T) {
	scene := os.Getenv("LILT_TUI_PTY_FIXTURE")
	if scene == "" {
		t.Skip("opt-in terminal fixture")
	}
	m, player, _ := newModel(t)
	switch scene {
	case "genre", "country":
		m.radio = longOptionsRadio{}
		m.source, m.view, m.title = "radio", "Browse", "Popular Worldwide"
		m.overlay = "discovery"
		m.discoverySelected = discoveryGenre
		if scene == "country" {
			m.discoverySelected = discoveryCountry
		}
		// Enter takes the normal async loader path and populates 25 choices.
	case "five-track-fill":
		m.state = core.PlaybackState{Status: "buffering", Source: "apple-music", Mode: "full",
			QueueFill: &core.QueueFill{Queued: 2, Total: 5}}
	case "queue-undo":
		m.state = core.PlaybackState{Status: "paused", Source: "apple-music", Mode: "full",
			Queue: []core.Item{{Kind: "song", ID: "a", Title: "Current", Artist: "Fixture"},
				{Kind: "song", ID: "b", Title: "Restore Me", Artist: "Fixture"},
				{Kind: "song", ID: "c", Title: "Keep Me", Artist: "Fixture"}},
			QueueIndex: 0, QueueRevision: 1}
		player.state = m.state
		player.offerQueueUndo = true // in-memory fake tests TUI keys, not server deadline
		m.queueFocus, m.queueCursor = true, 1
	case "search-collision":
		player.searchItems = []core.Item{
			{Kind: "song", ID: "1499378607", Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours", DurationMs: 200000},
			{Kind: "song", ID: "1499386265", Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours", DurationMs: 200000},
			{Kind: "song", ID: "1505683988", Title: "Blinding Lights", Artist: "The Weeknd", Album: "After Hours (Deluxe Version)", DurationMs: 202000},
		}
	case "source-settle":
		degraded := api.SourceDescriptor{ID: api.SourceAppleMusic, Label: "Apple Music", Available: true,
			Availability: api.AvailabilityDegraded, Capabilities: map[string]api.Capability{
				api.CapLibrary: {Available: true}, api.CapPlaybackPreview: {Available: true},
			}}
		ready := api.SourceDescriptor{ID: api.SourceAppleMusic, Label: "Apple Music", Available: true,
			Availability: api.AvailabilityReady, Capabilities: map[string]api.Capability{
				api.CapLibrary: {Available: true}, api.CapPlaybackPreview: {Available: true},
				api.CapPlaybackFull: {Available: true}, api.CapQueue: {Available: true}, api.CapShuffle: {Available: true},
			}}
		m.descriptors = []api.SourceDescriptor{degraded}
		m.hasInitialWatch = true // no startup fetch overwrites the injected first snapshot
		updates := make(chan api.WatchUpdate, 1)
		m.watchUpdates = updates
		go func() {
			time.Sleep(12 * time.Second)
			updates <- api.WatchUpdate{Kind: "sources.changed", Sequence: 1, Sources: []api.SourceDescriptor{ready}}
		}()
	default:
		t.Fatalf("unknown PTY scene %q", scene)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		t.Fatal(err)
	}
}
