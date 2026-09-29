package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// The queue-editing affordances (footer hint, help rows, Playback Info hint) are
// gated by the source's declared queue capability, so a preview-only source
// never advertises keys that fail on use (batch 2026-09-28-rounds F1).
func TestQueueEditingHintsFollowCapability(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Queue: []core.Item{{Title: "A"}}}

	if got := strings.Join(m.footerSegments(), " · "); !strings.Contains(got, "0 edit queue") {
		t.Fatalf("declared queue lost the footer hint: %q", got)
	}
	var infoUpNext, helpUpNext bool
	for _, line := range m.infoLines(80) {
		if strings.Contains(line, "0 focus") {
			infoUpNext = true
		}
	}
	for _, line := range m.helpLines(90) {
		if strings.Contains(line, "0") && strings.Contains(line, "focus or leave") {
			helpUpNext = true
		}
	}
	if !infoUpNext || !helpUpNext {
		t.Fatalf("declared queue lost hints: info=%v help=%v", infoUpNext, helpUpNext)
	}

	m.descriptors = []api.SourceDescriptor{{ID: api.SourceAppleMusic, Available: true,
		Capabilities: map[string]api.Capability{api.CapPlaybackFull: {Available: true}}}}
	if got := strings.Join(m.footerSegments(), " · "); strings.Contains(got, "0 edit queue") {
		t.Fatalf("undeclared queue still advertised in the footer: %q", got)
	}
	for _, line := range m.infoLines(80) {
		if strings.Contains(line, "0 focus") {
			t.Fatalf("undeclared queue still advertised in Playback Info: %q", line)
		}
	}
	for _, line := range m.helpLines(90) {
		if strings.Contains(line, "focus or leave") || strings.Contains(line, "queue the selected item") {
			t.Fatalf("undeclared queue still advertised in Help: %q", line)
		}
	}
}

// Playback Info shows the same displayed position as the Now Playing dock, so
// the two views of one fact cannot disagree.
func TestPlaybackInfoPositionMatchesNowPlaying(t *testing.T) {
	m, _, _ := newModel(t)
	m.state = core.PlaybackState{Status: "playing", Position: 10, Duration: 180}
	m.renderTime = time.Now()
	m.snapshotAt = m.renderTime.Add(-2 * time.Second)
	want := fmt.Sprintf("%.0f / %.0f s", m.displayPositionAt(m.renderTime), m.state.Duration)
	found := false
	for _, line := range m.infoLines(80) {
		if strings.Contains(line, "Position") {
			found = true
			if !strings.Contains(line, want) {
				t.Fatalf("Playback Info position %q != displayed %q", line, want)
			}
		}
	}
	if !found {
		t.Fatal("Playback Info has no Position row")
	}
}
