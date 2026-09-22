package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A digit in the switcher picks that row directly and runs the same switch
// flow as Enter (batch 2026-09-22-jamendo-tui OQ27: two rounds asked for
// numeric shortcuts; the switch itself completes asynchronously).
func TestSourceSwitcherNumberKeyPicksDirectly(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.overlay, m.overlaySelected = "source-switcher", 0
	next, cmd := m.handleSourceSwitcherKey(runeKey(rune("2"[0])))
	m = next.(Model)
	if m.pendingSource != "audius" {
		t.Fatalf("number 2 must start switching to audius, pendingSource=%q", m.pendingSource)
	}
	if m.overlay != "source-switcher" {
		t.Fatalf("overlay closes with the switch, not on the keypress: %q", m.overlay)
	}
	_ = tea.Batch
	m = run(m, cmd)
	if m.source != "audius" || m.overlay != "" {
		t.Fatalf("switch did not complete: source=%q overlay=%q", m.source, m.overlay)
	}
}
