package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/jamendo"
)

// jamendoSetupModel returns a model whose Jamendo descriptor is unconfigured
// (unavailable with the setup reason), plus an injected setup hook and URL
// opener so the modal flow runs hermetically.
func jamendoSetupModel(t *testing.T, setup func(ctx context.Context, clientID string) error, opened *[]string) Model {
	t.Helper()
	m, _, _ := newModel(t)
	descriptors := make([]api.SourceDescriptor, 0, len(m.descriptors)+1)
	for _, descriptor := range m.descriptors {
		if descriptor.ID == api.SourceJamendo {
			continue
		}
		descriptors = append(descriptors, descriptor)
	}
	descriptors = append(descriptors, api.SourceDescriptor{
		ID: api.SourceJamendo, Label: "Jamendo", Available: false, Availability: api.AvailabilityUnavailable,
		Reason: "Jamendo is not configured; run `lilt jamendo setup`",
		Capabilities: map[string]api.Capability{
			api.CapPlaybackFull: {Available: false, Reason: "not configured"},
		},
	})
	m.descriptors = descriptors
	m.jamendoSetup = setup
	if opened != nil {
		m.openURL = func(url string) { *opened = append(*opened, url) }
	}
	return m
}

func openJamendoSwitcher(m Model) Model {
	next, _ := m.handleKey(runeKey('s'))
	m = next.(Model)
	m.overlaySelected = indexOf(m.sourceChoices(), string(api.SourceJamendo))
	return m
}

func TestUnavailableJamendoOpensSetupModal(t *testing.T) {
	m := jamendoSetupModel(t, func(context.Context, string) error { return nil }, nil)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "input" || m.inputMode != "jamendo-setup" {
		t.Fatalf("setup modal = overlay=%q mode=%q", m.overlay, m.inputMode)
	}
	m.width, m.height = 80, 24
	view := plainText(m.View().Content)
	for _, want := range []string{"Jamendo Setup", "devportal.jamendo.com", "ctrl+o open devportal", "Enter validate & save"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup modal missing %q:\n%s", want, view)
		}
	}
}

func TestUnavailableNonJamendoSourceKeepsReasonToast(t *testing.T) {
	m, _, _ := newModel(t)
	m.descriptors = []api.SourceDescriptor{
		{ID: api.SourceAppleMusic, Available: true, Capabilities: map[string]api.Capability{api.CapPlaybackFull: {Available: true}}},
		{ID: api.SourceRadio, Available: false, Availability: api.AvailabilityUnavailable, Reason: "no network"},
	}
	m = openJamendoSwitcher(m)
	m.overlaySelected = indexOf(m.sourceChoices(), string(api.SourceRadio))
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != "source-switcher" || !strings.Contains(m.message, "no network") {
		t.Fatalf("unavailable radio should toast in the switcher, not open a modal: overlay=%q msg=%q", m.overlay, m.message)
	}
}

func TestJamendoSetupEscCancelsAndEmptyEnterIsNoOp(t *testing.T) {
	m := jamendoSetupModel(t, func(context.Context, string) error { return nil }, nil)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	// An empty client_id does not start validation.
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.jamendoValidating {
		t.Fatalf("empty Enter acted: cmd=%v validating=%v", cmd != nil, m.jamendoValidating)
	}

	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(Model)
	if m.overlay != "" || m.inputMode != "" || m.jamendoValidating {
		t.Fatalf("Esc did not cancel: overlay=%q mode=%q", m.overlay, m.inputMode)
	}
}

func TestJamendoSetupSuccessClosesModalAndRefreshesSources(t *testing.T) {
	var configured string
	m := jamendoSetupModel(t, func(_ context.Context, clientID string) error {
		configured = clientID
		return nil
	}, nil)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	m.input.SetValue("abc123def456")

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.jamendoValidating {
		t.Fatal("submit did not enter validating state")
	}
	message := cmd()
	setup, ok := message.(jamendoSetupMsg)
	if !ok {
		t.Fatalf("unexpected message %#v", message)
	}
	next, _ = m.Update(setup)
	m = next.(Model)
	if configured != "abc123def456" {
		t.Fatalf("setup hook got %q", configured)
	}
	if m.overlay != "" || m.jamendoValidating {
		t.Fatalf("success must close the modal: overlay=%q validating=%v", m.overlay, m.jamendoValidating)
	}
	if !strings.Contains(m.message, "Jamendo configured (abc123de…)") {
		t.Fatalf("toast = %q", m.message)
	}
}

func TestJamendoSetupFailureStaysInModalWithSanitizedError(t *testing.T) {
	m := jamendoSetupModel(t, func(context.Context, string) error {
		return errors.New("Jamendo rejected the stored client_id; run `lilt jamendo setup`")
	}, nil)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	m.input.SetValue("bad")
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	setup := cmd().(jamendoSetupMsg)
	next, _ = m.Update(setup)
	m = next.(Model)
	if m.overlay != "input" || m.jamendoValidating || m.jamendoSetupErr == "" {
		t.Fatalf("failure must stay in the modal: overlay=%q err=%q", m.overlay, m.jamendoSetupErr)
	}
	// The typed value survives for a retry.
	if m.input.Value() != "bad" {
		t.Fatalf("client_id lost on failure: %q", m.input.Value())
	}
	m.width, m.height = 80, 24
	if view := plainText(m.View().Content); !strings.Contains(view, "Jamendo rejected") {
		t.Fatalf("error not rendered:\n%s", view)
	}
}

func TestJamendoSetupCtrlOOpensDevportalWithoutEatingText(t *testing.T) {
	opened := []string{}
	m := jamendoSetupModel(t, func(context.Context, string) error { return nil }, &opened)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "", Mod: tea.ModCtrl})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("ctrl+o did not produce a launcher command")
	}
	_ = cmd()
	if len(opened) != 1 || opened[0] != jamendo.DeveloperPortalURL {
		t.Fatalf("opened = %v", opened)
	}

	// A plain 'o' stays typable text so client_ids are never mangled.
	next, _ = m.handleKey(runeKey('o'))
	m = next.(Model)
	if m.input.Value() != "o" {
		t.Fatalf("plain o was swallowed: %q", m.input.Value())
	}
}

// An empty submit must not read as a dead key: the modal stays open with the
// validation error instead of silently doing nothing (batch 2026-09-28-rounds F7).
func TestJamendoSetupEmptySubmitShowsFeedback(t *testing.T) {
	m := jamendoSetupModel(t, func(context.Context, string) error {
		t.Fatal("setup must not run for an empty client_id")
		return nil
	}, nil)
	m = openJamendoSwitcher(m)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	m.input.SetValue("   ")
	next, cmd := m.submitInput()
	m = next.(Model)
	if cmd != nil {
		t.Fatal("empty submit returned a command")
	}
	if m.overlay != "input" || m.inputMode != "jamendo-setup" {
		t.Fatalf("modal closed on empty submit: overlay=%q mode=%q", m.overlay, m.inputMode)
	}
	if m.jamendoSetupErr != "Client ID is required" {
		t.Fatalf("empty submit feedback = %q", m.jamendoSetupErr)
	}
}
