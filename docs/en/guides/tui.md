# Use the TUI

[English](tui.md) | [简体中文](../../guides/tui.md)

`lilt tui` opens the full-screen interface, starting a server if necessary or attaching to the active one. Closing the TUI does not stop playback. For the full layout and interaction specification see [UI UX (Chinese)](../../ui/ux.md); this page covers daily use.

## Orientation

The top identity line names the current source. Below it are numbered surfaces (`1 Home`, `2 Recent`, and source-specific choices), a list next to **Up Next**, the **Now Playing** bar, and context-sensitive key hints. Use `1`–`9` or `[`/`]` for surfaces, `s` for the source switcher, and `:` for the command palette. There is no row of source tabs.

Every source has **Home** and **Recent**. Home contains previews such as Continue Playing, Recently Played, Your Playlists and Favorites where supported, plus a **Go to** section for full lists. Radio adds **Browse**; Audius and Jamendo add **Discover**. Apple Music's favorites and playlists are Home sections rather than extra surfaces.

## Everyday keys

| Action | Key |
|---|---|
| Search | `/` (Radio opens Search & Filters) |
| Open or play the selected item | `Enter` |
| Play only the selected item | `p` |
| Pause / resume | `Space` or `c` |
| Next / previous in a finite queue | `n` / `b` |
| Stop (also clears the queue) | `v` |
| Queue next / append | `e` / `E` |
| Shuffle / repeat when supported | `S` / `R` |
| Save a local favorite | `f` |
| Focus Up Next | `2` (`x` remove, `u` undo within five seconds when offered, `J`/`K` reorder — `J` moves a row down, `K` up, and the first/last row answers with a toast, `c` clear (press twice to confirm), `Enter` jump) |
| Theme / playback info / help | `t` / `i` / `?` |
| Back / clear local filter | `Esc` / `Backspace` |
| Exit outside text input | `q` |
| Add a radio stream URL | `a` in Radio only (other sources answer the press with the reason) |

`Enter` is contextual: a search result plays only that item; Home/Recent/Discover start at the selected row and continue through its section; album and playlist details continue from the selected track. `p` plays only the current item. In text fields, typing `q` inserts a character rather than exiting. TUI hints and the [full UI specification (Chinese)](../../ui/ux.md) cover other contexts.

## Feedback conventions

- **Esc closes one overlay layer at a time**: Jamendo setup opened from the Account overlay returns to Account, not to the page; a completed setup closes the whole stack with a toast.
- **Dead keys answer**: `a` outside Radio, `F` inside Radio (use `/` for search & filters), and `J`/`K` at the queue borders each get a one-shot toast naming the key and the reason.
- **A source switch that stopped playback says so**: once the switch commits, the toast `Stopped playback from <old source>` appears; no note when nothing was playing.
- The results-page group indicator explains itself: `Group 1/3 · Songs · [/] switch` names the position, the active group, and the switch keys.
- **Footer hints degrade by priority**: a narrow terminal drops contextual hints first; `q quit` never yields, and `? help` and `s source` are kept with priority though the tightest widths can still drop them (`s source` before `? help`); a shortened row ends with `…`. The full yield order lives in the [UI specification](../../ui/ux.md).
- **A track switch names its target**: while one playback replaces another, Now Playing reads `working… loading <new track>` instead of a bare `working…`; pausing the current track is unaffected.
- **Playback Info shows long text in full**: overlong titles and URLs wrap inside the dialog (it scrolls by rows); a live stream's ICY title matches Now Playing, with the submitted URL kept as its own detail row.
- **Jamendo setup errors stack above the shortcuts**: a validation error or `validating…` renders on its own row above the `ctrl+o · Enter · Esc` line instead of replacing it.
- **Placeholders read as placeholders**: input hints are italic, and a reopened filter or radio search prefills the previous term, saying `previous … prefilled` until edited.

## Help and legend

Beyond key bindings, Help (`?`) ends with a **Reference** group for what the interface uses without explaining inline:

- `:auth` — palette entry that opens the Account overlay (per-source authorization).
- `lilt jamendo setup` — the terminal command a Jamendo setup reason points to.
- Up Next row markers: `▶` current track, `·` played history; a playing list row indents `▶`.
- Radio row health markers: `○` queued, `◌` checking, `●` healthy (with latency), `×` failed.
- `v` stops playback and clears the queue.

Shuffle and repeat are toggles; a subsequent play command carries the selected mode. Themes use cliamp's TOML schema under `~/.config/lilt/themes/`. Preferences such as theme and last source live in server state. Visible actions follow the active source's advertised capabilities; Radio is a single live stream without a finite queue.
