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
| Stop | `v` |
| Queue next / append | `e` / `E` |
| Shuffle / repeat when supported | `S` / `R` |
| Save a local favorite | `f` |
| Focus Up Next | `2` (`x` remove, `u` undo within five seconds when offered, `J`/`K` move, `c` clear (press twice to confirm), `Enter` jump) |
| Theme / playback info / help | `t` / `i` / `?` |
| Back / clear local filter | `Esc` / `Backspace` |
| Exit outside text input | `q` |
| Add a radio stream URL | `a` in Radio |

`Enter` is contextual: a search result plays only that item; Home/Recent/Discover start at the selected row and continue through its section; album and playlist details continue from the selected track. `p` plays only the current item. In text fields, typing `q` inserts a character rather than exiting. TUI hints and the [full UI specification (Chinese)](../../ui/ux.md) cover other contexts.

Shuffle and repeat are toggles; a subsequent play command carries the selected mode. Themes use cliamp's TOML schema under `~/.config/lilt/themes/`. Preferences such as theme and last source live in server state. Visible actions follow the active source's advertised capabilities; Radio is a single live stream without a finite queue.
