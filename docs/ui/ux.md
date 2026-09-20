# Spec: UX（当前 TUI 布局、导航、键位）

语义与跨 renderer 约束以 [model.md](model.md) 为准；组件、间距、信息层级与 theme token 以
[design-system.md](design-system.md) 为准。布局与密度取向的设计参考（orbit / cliamp / cmus）见
[references.md](references.md)。

## 布局

```text
 Apple Music                                       lilt  ← identity：位置在左，品牌在右
 1 Home · › 2 Recent                                      ← active surface 必须有 marker；紧贴面板
 ┌── RECENT (28) ─────────────────┐ ┌── UP NEXT (1/6) ──┐
 │ Recently Played Songs            │ │ · previous track   │
 │   Track — Artist                  │ │ ▶ current track    │
 └──────────────────────────────────┘ └──────────────────┘

 ┌── NOW PLAYING ────────────────────────────────────────────────────────────┐
 │ Track — Artist                                                              │
 │ ▶ Playing  2:42  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  4:30  ALAC 24/48  R All │
 └────────────────────────────────────────────────────────────────────────────┘

 Ready
 enter open/play · p play · space pause · n next · v stop · / search · q quit
```

**Hint 放置原则**：顶部只放位置与 navigation，**不放快捷键提示**；底部只放**与当前 surface
相关、最可能被用到**的快捷键，顺序由具体到全局/罕见，宽度不足时从尾部先截断。选中行可入队时底栏
出现 `e next · E append`（仅当该 source 声明 `queue`；Radio 不显示），因为搜索结果里不再能靠
Enter 连播，入队键必须在底栏可见。Source 切换
（`s`）与命令面板（`:`）属全局键，排在底部靠后，不在顶部重复。shell 的完整 band 与上下对称
外边距见 [design-system.md](design-system.md#2-页面骨架)。

- Apple Music surfaces: **Home, Recent**; Radio: **Home, Browse, Recent**; Audius:
  **Home, Discover, Recent**. Favorites and playlists are Home sections, not views; the full local
  favorites list opens from Go to → **All Favorites** as a pushed page (play/queue/favorite keys work
  in place; `f` unfavorites and the cursor stays on a stable row).
- Home is a dynamic initial loading frame. It shows non-empty Continue Playing, Recently Played, Trending
  (Audius), Your Playlists (Apple, or Audius when an account is linked), Favorites, then Go to entries
  (Search / Browse or Discover / Recent / All Favorites / All Playlists / Albums / Queue / Account);
  previews are capped at five.
- Enter on a song follows the page's intent: in a **search result page** it plays only that song (results
  are evidence for the query, not a playlist); on a surface (Home/Recent/Discover) it means **play from
  here** and queues that song plus the rest of its section (headers/non-songs end the run); a lone song
  falls back to single play. `p` always plays just that item. Chaining a search result section is
  explicit per row with `e`/`E`, or `playSongs` from the CLI/agent.
- Enter on an `album` row pushes the album detail page (`album.tracks`, Apple Music only) — albums are
  not playlist-detail aliases. In an **album detail**, Enter means **play from here**: the queue starts
  at the selected song and fills the rest of the album in order; `p` plays the album from the top and
  `S` restarts it shuffled. The album's own row is not repeated in the list: the page header and context
  row carry its identity.
- In a **playlist detail**, Enter means **play from here**: the queue starts at the selected track and runs to
  the end (earlier tracks are dropped, no history). `p` plays the whole playlist from the top.
- Up Next marks played entries with `·` (dimmed) and the current entry with `▶`, so played history is not
  mistaken for upcoming tracks. At sufficient width it is the right rail of the workspace, not a bottom-dock
  sibling. `0` focuses it; narrow terminals render it in the main area when focused. Radio's rail explicitly
  states that live streams have no finite queue.
- Now Playing spans the full width below the workspace and contains only track identity plus playback facts.
  It does not repeat Source, queue count or page context. A helper-reported current format is shown; the
  `System-selected` placeholder and `availableFormats` list are not presented as a current codec. The latter
  belongs to Track Info. Progress, time and enabled shuffle/repeat modes share the compact facts row.
- `/` is a central search overlay (Radio opens Search & Filters); results and playlist details are temporary
  pages. `s`, `:`, help, info, theme, and Radio query controls are overlays.
- Overlays are modal boxes composited **over the live shell**, not screen replacements: the browsing frame
  stays visible behind the dialog, so the theme picker previews against real content and dialogs keep
  their context. Clicks outside the dialog still cancel the overlay (see the click rules above). An overlay
  binds only the keys it documents: the help overlay closes on `Esc`/`q`/`?` and leaves every other key
  inert, so a `v` or `p` pressed while reading help is not silently swallowed by the dismissal.
- No source tab row exists. Mouse selects list/queue rows and numeric **view** entries only; clicking the
  SOURCE breadcrumb opens the source switcher (it never switches implicitly). Inside an overlay, a click on a
  row selects/confirms it — the source switcher and `:` palette are fully mouse-operable; a click outside
  cancels. The switcher lists source **names only** (capability menus were dropped as noise).
- List click semantics: clicking a row selects it; a **double-click** on the same row activates it (like
  Enter). Two clicks count as a double-click only when they land on the same row consecutively within
  `doubleClickWindow` (500ms); a second click after that gap is a fresh select, not activation. A consumed
  double-click cannot repeat on a third click. This never toggles Up Next focus — use `0` or click the
  queue panel to focus the queue; the same double-click rule gates queue jump.

## Keys

| Key | Action |
|---|---|
| `s` | source switcher (names only); arrows/`j`/`k` or click, Enter commits, Esc cancels |
| `:` | command palette; Tab/↑↓ cycle candidates (highlight only), Enter runs highlighted, Esc cancels |
| `1..n`, `[`/`]` | select/cycle available surface; on a pushed results page `[`/`]` jump between result groups (Songs/Albums/Playlists) |
| `/` | provider search; Radio Search & Filters |
| `Space`/`c`, `n`/`b`, `v` | pause-resume, next-previous, stop |
| `S`, `R`, `e`/`E` | shuffle (Radio Browse re-sort), repeat, queue next/append |
| `0` | focus Up Next; `x`, `J`/`K`, `c` edit; Enter/`p` jump |
| `f`, `a`, `F` | favorite, add Radio URL, filter Apple list |
| `r`, `?`, `q` | retry, help, quit |

`Tab` never switches source and is inert in text inputs. Search stays `/`; Ctrl-P is unbound. Text controls
take all printable input literally.

## Feedback and interaction

- With shuffle on, the Up Next rail is titled `UP NEXT · SHUFFLED`: its rows stay in the submitted order
  (the space `queue jump/remove/move` index into), while the audio follows MusicKit's own order. The rail
  never reorders to the play order — that would break the index semantics.
- A starting URL/stream session reads as `Connecting…` for about 1.5s before `Buffering…`, then `Playing`;
  a stream that never starts still fails with an actionable error. The `Starting…` transient only applies
  while a playback command is in flight (`m.busy`): a settled `paused` at position 0 is a never-started
  track and reads `Paused`, while a finite queue that played to its end reads `■ Finished` (the helper
  reports `status:"ended"`; see [`../product/open-questions.md`](../product/open-questions.md) OQ11). Initial loads display `loading…`; a refresh retains usable rows. Errors take precedence over empty hints;
  `r` or the selected surface number retries. Stale async responses cannot overwrite a new destination.
- Source switch stops active playback before changing source, then clears stack, search/filter/detail state and
  session cache. The target is validated against the newest descriptor snapshot before stop; dependent steps are
  serialized. A stop failure or cancellation retains the old source and playback. A later persistence failure
  rolls browsing back to the old source, but does not replay audio already stopped successfully.
- Capability-driven UI: the TUI fetches `sources.list` at startup and gates shuffle, the library/trending
  previews, their footer hints, and the Help shuffle/repeat line by each source's declared capability (no
  per-source support list).
- The Account summary follows the current source: `:auth` and the Home Account entry report Apple Music's
  status, Audius's optional account link (label when linked), or Radio's "not required". It is fetched at
  startup and after a source switch (`authorization.status`).
- Radio Browse defaults to Popular Worldwide, pages at 100, supports retry and cached fallback, and `/` edits
  name/language/tag/country/sort. Esc restores Popular Worldwide only after clearing a local filter.
- Radio rows expose local reachability probes; probes never block navigation/playback. Radio is a live single
  stream (no queue); Apple Music and Audius are mutually exclusive finite queues.
- External metadata is terminal-sanitized. Small terminals show `Terminal too small — resize`; overlays remain
  cancellable. Click outside an overlay cancels it; list/queue clicks never move the viewport.
- Overlay headers contain only stable identity. Shortcut help, active filters and scroll/range context render in
  the overlay body/status rows.

See [model.md](model.md) for Home availability, palette semantics, Client API integration, and requirements for
new renderers.
