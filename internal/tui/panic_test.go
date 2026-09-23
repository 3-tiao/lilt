package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A panicking command must be demoted to a panicMsg: the TUI stays alive, the
// journal records the stack, and the failure is visible instead of fatal.
func TestGuardedCmdCatchesPanicsAndReports(t *testing.T) {
	var fields map[string]any
	log := func(kind string, f map[string]any) {
		if kind == "tui.panic" {
			fields = f
		}
	}
	msg := guardedCmd(func() tea.Msg {
		panic("boom in cmd")
	}, log)()
	report, ok := msg.(panicMsg)
	if !ok {
		t.Fatalf("msg = %T, want panicMsg", msg)
	}
	if report.Err != "boom in cmd" {
		t.Fatalf("report = %+v", report)
	}
	if fields == nil || fields["panic"] != "boom in cmd" || fields["scope"] != "cmd" ||
		!strings.Contains(fields["stack"].(string), "panic_test.go") {
		t.Fatalf("journal record = %+v", fields)
	}
}

func TestGuardedCmdPassesThroughNilAndHappyPath(t *testing.T) {
	if guardedCmd(nil, func(string, map[string]any) {}) != nil {
		t.Fatal("nil cmd became a wrapper")
	}
	msg := guardedCmd(func() tea.Msg { return "ok" }, nil)()
	if msg != "ok" {
		t.Fatalf("msg = %v", msg)
	}
}

// A panic inside a batch command is caught too: the wrapper recurses into the
// batch and each nested command is guarded independently.
func TestGuardedCmdWrapsBatchCommands(t *testing.T) {
	var panics []string
	log := func(kind string, f map[string]any) {
		if kind == "tui.panic" {
			panics = append(panics, f["panic"].(string))
		}
	}
	msg := guardedCmd(tea.Batch(
		func() tea.Msg { return "fine" },
		func() tea.Msg { panic("boom in batch") },
	), log)()
	if _, ok := msg.(tea.BatchMsg); !ok {
		t.Fatalf("msg = %T, want a batch of wrapped commands", msg)
	}
	var sawPanicReport bool
	for _, inner := range msg.(tea.BatchMsg) {
		if report, ok := inner().(panicMsg); ok {
			sawPanicReport = true
			if report.Err != "boom in batch" {
				t.Fatalf("report = %+v", report)
			}
		}
	}
	if !sawPanicReport || len(panics) != 1 || !strings.Contains(panics[0], "boom in batch") {
		t.Fatalf("panics = %v", panics)
	}
}

// journalPanic is the crash record shape every guard shares: the stack in the
// record is the debugging entry point.
func TestJournalPanicWritesKindStackAndScope(t *testing.T) {
	var logged []map[string]any
	log := func(kind string, f map[string]any) { logged = append(logged, f) }
	record := journalPanic(log, "view", "boom in view")
	if len(logged) == 0 {
		t.Fatal("nothing journaled")
	}
	if record["panic"] != "boom in view" || record["scope"] != "view" ||
		!strings.Contains(record["stack"].(string), "panic_test.go") {
		t.Fatalf("record = %+v", record)
	}
}
