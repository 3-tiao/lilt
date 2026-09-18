# Spec: UX（当前 TUI 布局、导航、键位）

语义与跨 renderer 约束以 [model.md](model.md) 为准；本页只定义已实现 terminal UI。

## 布局

```text
 lilt  SOURCE: Apple Music                       ← 来源品牌（accent 色，非 tab、无高亮、不重复 surface）
 1 Home · 2 Recent                                ← 1..n surface（只放位置）
 ┌── HOME ──────────────────────┐ ┌── UP NEXT ──┐
 │ Recently Played               │ │ ▶ track     │
 │ Your Playlists                │ │ ...         │
 └──────────────────────────────┘ └──────────────┘
 ┌── NOW PLAYING ─────────────────────────────────┐
 enter open/play · p play · f favorite · / search · ? help · q quit   ← 底部 hint
```

**Hint 放置原则**：顶部只放位置信息（breadcrumb 与 `1..n` surface 列表），**不放快捷键提示**；
底部只放**与当前 surface 相关、最可能被用到**的快捷键，顺序由具体到全局/罕见，宽度不足时
从尾部先截断。源切换（`s`）与命令面板（`:`）属全局键，排在底部靠后，不在顶部重复。

- Apple Music surfaces: **Home, Recent**; Radio: **Home, Browse, Recent**; Audius:
  **Home, Discover, Recent**. Favorites and playlists are Home sections, not views.
- Home is a dynamic initial loading frame. It shows non-empty Continue Playing, Recently Played, Trending
  (Audius), Your Playlists (Apple), Favorites, then Go to entries; previews are capped at five.
- Enter on a song in a list means **play from here**: it queues that song and the rest of its section
  (headers/non-songs end the run). A lone song falls back to single play; `p` always plays just that item.
- In a **playlist detail**, Enter plays the whole playlist starting at the selected track: the queue keeps
  the earlier tracks as dimmed history and the panel title shows the current position (`3/10`). `p` plays the
  playlist from the top.
- A finite queue at sufficient width renders Up Next beside Now Playing. `0` focuses it; narrow terminals
  render it in the main area when focused.
- The Now Playing dock shows status plus only meaningful facts (preview mode, a real audio format,
  shuffle/repeat flags). It does not repeat the source — the breadcrumb already names it — and the server's
  `System-selected` placeholder (unknown format) is hidden rather than shown as fact.
- `/` is a central search overlay (Radio opens Search & Filters); results and playlist details are temporary
  pages. `s`, `:`, help, info, theme, and Radio query controls are overlays.
- No source tab row exists. Mouse selects list/queue rows and numeric **view** entries only; clicking the
  SOURCE breadcrumb opens the source switcher (it never switches implicitly). Inside an overlay, a click on a
  row selects/confirms it — the source switcher and `:` palette are fully mouse-operable; a click outside
  cancels. The switcher lists source **names only** (capability menus were dropped as noise).
- List click semantics: clicking a row selects it; clicking the **already-selected** row activates it (like
  Enter). This never toggles Up Next focus — use `0` or click the queue panel to focus the queue.

## Keys

| Key | Action |
|---|---|
| `s` | source switcher (names only); arrows/`j`/`k` or click, Enter commits, Esc cancels |
| `:` | command palette; Tab/↑↓ cycle candidates (highlight only), Enter runs highlighted, Esc cancels |
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
