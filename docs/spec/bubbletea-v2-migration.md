# Plan: Migrate to Bubble Tea v2

**Status: planned, not started.**

## Why

Mouse-wheel scrolling occasionally switches tabs. Root cause is Bubble Tea v1's
input layer:

```go
// bubbletea v1 key.go
canHaveMoreData := numBytes == len(buf) // only waits when a read fills the buffer
```

A short read is treated as an event boundary, so a mouse escape sequence split
across writes (`\x1b[<65;28;87M`, observed arriving in pieces 28-30ms apart)
becomes `alt+[` followed by `<`, digits and `;`. Because lilt binds `[` to view
cycling and digits to SOURCE/VIEW switching, scrolling appeared to switch views.

Bubble Tea v2 replaces the input layer (`ultraviolet.TerminalReader`) with:

- a byte buffer accumulated across reads,
- a state-machine scanner that consumes only complete sequences,
- `DefaultEscTimeout = 50ms` before an incomplete sequence is treated as a lone
  Esc.

That natively covers the observed 28-30ms gaps. v1 has the defect in every
release up to v1.3.10, so upgrading is the only library-level fix.

Until this migration lands the bug can recur; the temporary two-layer workaround
(input coalescing + model-level fragment suppression) was removed on request and
is no longer in the tree.

## Scope

The charm libraries moved to a vanity domain, so three modules move together:

| v1 | v2 |
|---|---|
| `github.com/charmbracelet/bubbletea v1.1.1` | `charm.land/bubbletea/v2 v2.0.9` |
| `github.com/charmbracelet/bubbles v0.20.0` | `charm.land/bubbles/v2 v2.2.1` |
| `github.com/charmbracelet/lipgloss v0.13.0` | `charm.land/lipgloss/v2 v2.0.6` |

Only two files import them today, which keeps the work bounded:

- `internal/tui/model.go` — `tea.*` ~296 uses, `lipgloss.*` ~95, `textinput.*` ~4
- `internal/tui/model_test.go` — same imports, plus key/mouse test helpers

`lipgloss` usage is small and nearly v2-compatible: `NewStyle`, `Width`,
`Color`, `Center`, `Left`, `Place`, `JoinVertical`, `Style`. We do not use the
removed renderer, `TerminalColor`, or `AdaptiveColor` APIs, so this part should
be import path plus compile fixes only.

## Steps

Each step keeps the tree buildable except where noted; run `go build ./...`
after every step.

1. **Branch and module swap**
   - `git switch -c feat/bubbletea-v2`
   - `go get charm.land/bubbletea/v2 charm.land/bubbles/v2 charm.land/lipgloss/v2`
   - `go mod tidy`, then rewrite the three imports.
   - Expect a large, mechanical compile-error list; that list is the work queue.

2. **Declarative view**
   - `func (m Model) View() tea.View` returning `tea.NewView(content)`.
   - Move `tea.WithAltScreen()` and `tea.WithMouseCellMotion()` out of
     `Run` into `View`: `AltScreen = true`, `MouseMode = tea.MouseModeCellMotion`.
   - Keep `consoleFrame`/render helpers returning strings; only the top-level
     `View` changes.

3. **Key messages**
   - `tea.KeyMsg` → `tea.KeyPressMsg` in `Update` and `handleKey`.
   - `msg.Type` → `msg.Code`; `msg.Runes` → `msg.Text` (`string`);
     `msg.Alt` → `msg.Mod.Contains(tea.ModAlt)`.
   - `tea.KeyRunes` is gone: multi-rune/paste handling becomes
     `len(msg.Text) > 0` plus `tea.PasteMsg` / `PasteStartMsg` / `PasteEndMsg`.
   - `case " ":` → `case "space":` (space bar `String()` changed).
   - Audit key constants we use: `KeyEnter`, `KeyEsc`, `KeyTab`, `KeyShiftTab`,
     `KeyUp`, `KeyDown`, `KeyRight`, `KeyBackspace`, `KeyCtrlC`.
   - The existing `msg.String()` switch in `handleKey` mostly survives unchanged.

4. **Mouse messages**
   - `tea.MouseMsg` becomes an interface; events split into
     `MouseClickMsg`, `MouseReleaseMsg`, `MouseMotionMsg`, `MouseWheelMsg`.
   - Rewrite `handleMouse` to take the concrete messages and read coordinates
     via `msg.Mouse()` (`X`, `Y`) instead of `msg.X`/`msg.Y`.
   - Rename buttons: `MouseButtonLeft` → `MouseLeft`, `MouseButtonWheelUp` →
     `MouseWheelUp`, etc. `msg.Action` checks disappear (the type carries it).
   - Our click handling requires a press plus release at the same cell for tab
     switching, which the split types make more natural than the old `Action`.

5. **Text input (bubbles v2)**
   - `textinput.Model`/`New`/`Focus`/`Blur`/`SetValue`/`Value`/`View` are used
     in a handful of places (`openTextInput`, `handleTextInputKey`, overlay
     edits). Confirm v2 equivalents and adjust `Blink`.
   - Overlay rendering embeds `m.input.View()`; keep behaviour identical.

6. **Tests**
   - `runeKey` helper builds `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}`
     → `tea.KeyPressMsg{Code: r, Text: string(r)}`.
   - `mouseWheel`/click helpers → the new mouse message types.
   - `m.View()` now returns `tea.View`; the ~32 string assertions in
     `model_test.go` must read `.Content`.
   - Re-run the full suite; expect semantic, not just mechanical, failures in
     the mouse tests.

7. **Delete the old workaround assumption and verify the fix**
   - Confirm no `internal/tui/input*.go` exists (already removed).
   - Re-run the controlled pty probe that writes `\x1b[<65;28` and `;87M` with
     28ms and 35ms gaps, then asserts that no key events are logged and the
     view does not change. v2's 50ms escape timeout must absorb both.
   - Re-run the same probe with a 60ms gap to document the new limit.

8. **Docs and release pass**
   - Remove the "输入分片（已知问题）" bullet from `docs/spec/ux.md`, or rewrite
     it as "handled by the input layer".
   - Full `go test ./...`, `go test -race ./...`, `go vet ./...`,
     `git diff --check`.
   - Manual pass in a real terminal: wheel scroll and scrollbar, tab clicks,
     `/` → Sort → `Enter`, `a` URL overlay, `f` on the playing row, `r` retry.

## Symbol mapping (for search-and-replace)

| v1 | v2 |
|---|---|
| `View() string` | `View() tea.View`, `tea.NewView(s)` |
| `tea.WithAltScreen()` | `view.AltScreen = true` |
| `tea.WithMouseCellMotion()` | `view.MouseMode = tea.MouseModeCellMotion` |
| `tea.KeyMsg` | `tea.KeyPressMsg` |
| `msg.Type` | `msg.Code` |
| `msg.Runes` | `msg.Text` |
| `msg.Alt` | `msg.Mod.Contains(tea.ModAlt)` |
| `case " "` | `case "space"` |
| `msg.Paste` | `tea.PasteMsg` / `PasteStartMsg` / `PasteEndMsg` |
| `tea.MouseMsg` struct | interface; concrete `Mouse*Msg` types |
| `msg.X`, `msg.Y` | `msg.Mouse().X`, `msg.Mouse().Y` |
| `tea.MouseButtonLeft` | `tea.MouseLeft` |
| `tea.MouseButtonWheelUp` | `tea.MouseWheelUp` |
| `tea.KeyRunes` | check `len(msg.Text) > 0` |
| `tea.KeyCtrlC` | `msg.String() == "ctrl+c"` |
| `tea.Sequentially` | `tea.Sequence` |
| `tea.WindowSize()` | `tea.RequestWindowSize` |

Unchanged: `tea.Model`, `tea.Msg`, `tea.Cmd`, `tea.Batch`, `tea.Tick`,
`tea.Quit`, `tea.WindowSizeMsg`, `tea.NewProgram`, `p.Run()`.

## Risks

| Risk | Mitigation |
|---|---|
| Mouse semantics differ subtly (press/release vs action) | Keep the existing geometry tests; re-verify click, double-click, tab click, wheel by hand |
| `textinput` v2 API drift | It is used in one place; fall back to a plain rune buffer if v2's API is awkward |
| lipgloss v2 color/renderer removal | We use none of the removed APIs; if `Width` changes semantics, add a targeted test |
| Large mechanical diff hides a behavioural regression | Migrate on a branch, land tests and the pty probe before merging |
| v2 is young (`v2.0.9`) | Pin exact versions; the migration is isolated to the TUI package |

## Estimate

Focused work: roughly 4-6 hours, dominated by key/mouse message rewrites and the
~32 test assertions, plus one real-terminal verification pass. No data, state, or
player code is touched.

## Verification checklist

- [ ] Controlled pty probe: 28ms and 35ms split mouse sequences produce no keys and no view change
- [ ] Wheel over the list scrolls the viewport and moves the scrollbar
- [ ] Click selects, second click activates, no viewport jump
- [ ] Esc closes overlays immediately; `q` quits
- [ ] Sort menu, `S` re-sort, `r` retry, `/` and `a` overlays
- [ ] `go test ./...`, `go test -race ./...`, `go vet ./...`

## References

- Bubble Tea v2 upgrade guide: `charm.land/bubbletea/v2@v2.0.9/UPGRADE_GUIDE_V2.md`
- Lip Gloss v2 upgrade guide: `charm.land/lipgloss/v2@v2.0.6/UPGRADE_GUIDE_V2.md`
- Observed evidence: `~/.local/state/lilt/log/lilt.jsonl`, bursts 28-30ms apart,
  each parsed as `alt+[`, `<`, digits, `;`, `M`
