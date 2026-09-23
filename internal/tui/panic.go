package tui

import (
	"fmt"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
)

// panicMsg reports a command whose guard caught a panic. The stack is in the
// journal already; the model surfaces the failure instead of dying with it.
type panicMsg struct {
	Err   string
	Stack string
}

// journalPanic writes one crash record: kind tui.panic with the panic value
// and full stack. It is the debugging entry point for every TUI panic.
func journalPanic(log func(kind string, fields map[string]any), scope string, r any) map[string]any {
	fields := map[string]any{
		"panic": fmt.Sprint(r),
		"stack": string(debug.Stack()),
		"scope": scope,
	}
	if log != nil {
		log("tui.panic", fields)
	}
	return fields
}

// guardedCmd demotes a panicking command into a panicMsg: bubbletea runs every
// command in its own goroutine, and an unrecovered panic there kills the whole
// TUI with the trace on stderr only. The wrapper catches it, journals the
// stack, and hands the model a panicMsg (which surfaces the failure) so the
// rest of the session keeps working. Batch commands are wrapped recursively so
// a panic in any nested command is caught too; tea.Sequence commands keep
// bubbletea's own guard, whose trace goes to stderr.
func guardedCmd(cmd tea.Cmd, log func(kind string, fields map[string]any)) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		var (
			msg   tea.Msg
			fatal string
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					fatal = fmt.Sprint(r)
					journalPanic(log, "cmd", r)
				}
			}()
			msg = cmd()
		}()
		if fatal == "" {
			// Batch commands are re-wrapped so a panic in any nested command is
			// caught by this guard instead of bubbling to bubbletea's fatal
			// handler.
			if batch, ok := msg.(tea.BatchMsg); ok {
				wrapped := make(tea.BatchMsg, 0, len(batch))
				for _, inner := range batch {
					wrapped = append(wrapped, guardedCmd(inner, log))
				}
				return wrapped
			}
			return msg
		}
		return panicMsg{Err: fatal, Stack: "see journal: tui.panic"}
	}
}
