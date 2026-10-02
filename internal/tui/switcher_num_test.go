package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A digit in the switcher picks that row directly and runs the same switch
// flow as Enter (batch 2026-09-22-jamendo-tui: two rounds asked for
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

// A number switches immediately, so the hint must not describe it as a "pick"
// that Enter confirms. The old "1-4 pick · Enter/click switch" wording sent a
// user's Enter to the new source's Home, where it played the selected row
// (real round 2026-10-02, api.audius.co).
func TestSourceSwitcherHintMatchesNumberKeyBehavior(t *testing.T) {
	m, _, _ := newModel(t)
	m.width, m.height = 110, 30
	m.overlay = "source-switcher"
	box := plainText(m.overlayDialog(110, 30))
	if strings.Contains(box, "pick") {
		t.Fatalf("hint still calls the number key a pick: %q", box)
	}
	if !strings.Contains(box, "1-4 switch") {
		t.Fatalf("hint does not say the number switches: %q", box)
	}
}
