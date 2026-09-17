# Spec: UX（当前 TUI 布局、导航、键位）

语义与跨 renderer 约束以 [model.md](model.md) 为准；本页只定义已实现 terminal UI。

## 布局

```text
 lilt  SOURCE: Apple Music · HOME                 ← breadcrumb，不是 source tab
 1 Home · 2 Recent   s source · : commands        ← 1..n surface
 ┌── HOME ──────────────────────┐ ┌── UP NEXT ──┐
 │ Recently Played               │ │ ▶ track     │
 │ Your Playlists                │ │ ...         │
 └──────────────────────────────┘ └──────────────┘
 ┌── NOW PLAYING ─────────────────────────────────┐
 ? help · s source · : commands · q quit
```

- Apple Music surfaces: **Home, Recent**; Radio: **Home, Browse, Recent**; Audius:
  **Home, Discover, Recent**. Favorites and playlists are Home sections, not views.
- Home is a dynamic initial loading frame. It shows non-empty Continue Playing, Recently Played, Trending
  (Audius), Your Playlists (Apple), Favorites, then Go to entries; previews are capped at five.
- A finite queue at sufficient width renders Up Next beside Now Playing. `0` focuses it; narrow terminals
  render it in the main area when focused.
- `/` is a central search overlay (Radio opens Search & Filters); results and playlist details are temporary
  pages. `s`, `:`, help, info, theme, and Radio query controls are overlays.
- No source tab row exists. Mouse selects list/queue rows and numeric **view** entries only; it does not
  switch sources. The source switcher is keyboard-driven.

## Keys

| Key | Action |
|---|---|
| `s` | source switcher; arrows/`j`/`k`, Enter commits, Esc cancels |
| `:` | command palette; Tab completes, Enter executes, Esc cancels |
| `1..n`, `[`/`]` | select/cycle available surface |
| `/` | provider search; Radio Search & Filters |
| `Space`/`c`, `n`/`b`, `v` | pause-resume, next-previous, stop |
| `S`, `R`, `e`/`E` | shuffle (Radio Browse re-sort), repeat, queue next/append |
| `0` | focus Up Next; `x`, `J`/`K`, `c` edit; Enter/`p` jump |
| `f`, `a`, `F` | favorite, add Radio URL, filter Apple list |
| `r`, `?`, `q` | retry, help, quit |

`Tab` never switches source and is inert in text inputs. Search stays `/`; Ctrl-P is unbound. Text controls
take all printable input literally.

## Feedback and interaction

- Initial loads display `loading…`; a refresh retains usable rows. Errors take precedence over empty hints;
  `r` or the selected surface number retries. Stale async responses cannot overwrite a new destination.
- Source switch stops active playback before changing source, then clears stack, search/filter/detail state and
  session cache. Failed or cancelled switches retain the old source and playback.
- Radio Browse defaults to Popular Worldwide, pages at 100, supports retry and cached fallback, and `/` edits
  name/language/tag/country/sort. Esc restores Popular Worldwide only after clearing a local filter.
- Radio rows expose local reachability probes; probes never block navigation/playback. Radio is a live single
  stream (no queue); Apple Music and Audius are mutually exclusive finite queues.
- External metadata is terminal-sanitized. Small terminals show `Terminal too small — resize`; overlays remain
  cancellable. Click outside an overlay cancels it; list/queue clicks never move the viewport.

See [model.md](model.md) for Home availability, palette semantics, Client API integration, and requirements for
new renderers.
