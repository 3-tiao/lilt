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
Enter 连播，入队键必须在底栏可见。选中行可收藏时紧随其后出现 `f favorite`/`f unfavorite`：它是当前焦点行的
库操作，排在播放控制（`n next`/`space pause`/`v stop`）之前，否则播放中宽度不足时会被先截掉
（batch 2026-09-23-postaudit-recheck N1）。容器行（歌单/专辑）的 Enter 只打开详情，提示写作
`enter open`，不写 `open/play`（batch 2026-09-23-postaudit-recheck N2）。Source 切换
（`s`）与命令面板（`:`）属全局键，排在底部靠后，不在顶部重复。shell 的完整 band 与上下对称
外边距见 [design-system.md](design-system.md#2-页面骨架)。

- Apple Music surfaces: **Home, Recent**; Radio: **Home, Browse, Recent**; Audius:
  **Home, Discover, Recent**; Jamendo: **Home, Discover, Recent** (song-only trending). Favorites and playlists are Home sections, not views; the full local
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
  `S` toggles shuffle like everywhere else (then `Enter`/`p` plays it in that order). The album's own row is not repeated in the list: the page header and context
  row carry its identity.
- In a **playlist detail**, Enter means **play from here**: the queue starts at the selected track and runs to
  the end (earlier tracks are dropped, no history). `p` plays the whole playlist from the top.
- Up Next marks played entries with `·` (dimmed) and the current entry with `▶`, so played history is not
  mistaken for upcoming tracks. This history rule holds only when shuffle is off: shuffle advances in
  MusicKit's own order, so rows before the current one were skipped, not played, and stay rendered as
  upcoming (no `·`, no dim). The rail never invents played-state it cannot observe. The rail paints
  the keyboard cursor (fill + `›`) only while it is focused; an unfocused rail keeps its rows and markers
  but no cursor, so the main list and the rail never both look selected. A focused cursor on a played row
  keeps the fill and the `·` glyph. At sufficient width it is the right rail of the workspace, not a
  bottom-dock sibling. `0` focuses it; narrow terminals render it in the main area when focused. Radio's
  rail explicitly states that live streams have no finite queue.
- In Apple browser mode, a signed-in item starts as `unverified` until the page's media duration can be
  compared with the catalog duration. The facts row shows `Full length unverified` in that interval
  (and when the catalog has no usable duration); a confirmed short media asset shows `Preview`, not `Full`.
  Example at startup: `◌ Connecting…  0:00  Full length unverified`; once the page settles it becomes
  `▶ Playing  0:02  ━━━  1:30  Preview` or ordinary full playback. The existing band geometry does not change.
- Now Playing spans the full width below the workspace and contains only track identity plus playback facts.
  It does not repeat Source, queue count or page context. A helper-reported current format is shown; the
  `System-selected` placeholder and `availableFormats` list are not presented as a current codec. The latter
  belongs to Track Info. Progress, time and enabled shuffle/repeat modes share the compact facts row.
- `/` is a central search overlay (Radio opens Search & Filters); results and playlist details are temporary
  pages. `s`, `:`, auth, help, info, theme, and Radio query controls are overlays.
- Overlays are modal boxes composited **over the live shell**, not screen replacements: the browsing frame
  stays visible behind the dialog, so the theme picker previews against real content and dialogs keep
  their context. When the terminal is too narrow for side margins (below 8 cells total) the dialog spans
  the full width instead — a couple of base cells peeking out beside a dialog read as broken borders
  (batch 2026-09-23-postaudit M1). Clicks outside the dialog still cancel the overlay (see the click rules above). An overlay
  binds only the keys it documents: the help overlay closes on `Esc`/`q`/`?` and leaves every other key
  inert, so a `v` or `p` pressed while reading help is not silently swallowed by the dismissal.
- No source tab row exists. Mouse selects list/queue rows and numeric **view** entries only; clicking the
  identity row (the Source name on the left) opens the source switcher (it never switches implicitly).
  Inside an overlay, a click on a row selects/confirms it — the source switcher and `:` palette are fully
  mouse-operable; a click outside cancels. Each switcher row shows the source name, its availability, and a
  summary of its available capabilities (for example `Apple Music · ready · full, preview, queue`).
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
| `1..n`, `[`/`]` | select/cycle available surface; on a pushed results page `[`/`]` jump between result groups (Songs/Albums/Playlists); the page's context row names the active group, its index and the jump (`Songs 1/3 · [/] group`) |
| `/` | provider search; Radio Search & Filters |
| `Space`/`c`, `n`/`b`, `v` | pause-resume, next-previous, stop |
| `S`, `R`, `e`/`E` | shuffle toggle (Radio Browse re-sort), repeat cycle (off→all→one), queue next/append |
| `0` | focus Up Next; `x`, `J`/`K`, `c` edit; Enter/`p` jump |
| `f`, `a`, `F` | favorite current focus（Up Next 聚焦时为其 cursor 行）, add Radio URL, filter list (all sources except Radio) |
| `r`, `?`, `q` | retry, help, quit |

`Tab` never switches source and is inert in text inputs. Search stays `/`; Ctrl-P is unbound. Text controls
take all printable input literally. On an ordinary page, a single unbound Unicode character (such as fullwidth `：`)
is inert; only ASCII `:` opens the palette. Coalesced multi-character key events still replay each character once.

## Feedback and interaction

- `S` and `R` are toggles on every surface, and a play command carries the current shuffle/repeat with it:
  the server starts playback from a known form, so a play that omitted them would clear what the user just
  turned on. Shuffle-play is therefore `S` then `Enter`/`p`.
- While a finite queue fills, the Now Playing dock reads `working… 9/16 — large queues are added track by
  track` from the state's `queueFill` — including when another client started the fill — and only falls
  back to an elapsed-time message when no progress is reported. A single playback start names its target
  (`working… loading Bohemian Rhapsody`, then `working… 12s loading …`) instead of a bare `working…`, so
  a slow start reads as connecting to a known track rather than a stuck app
  (batch 2026-09-23-postaudit-recheck N5).
- With shuffle on, the Up Next rail is titled `UP NEXT · SHUFFLED`: its rows stay in the submitted order
  (the space `queue jump/remove/move` index into), while the audio follows MusicKit's own order. The rail
  never reorders to the play order — that would break the index semantics. For the same reason a jump
  under shuffle does not dim the skipped rows: they were not played.
- A starting URL/stream session reads as `Connecting…` for about 1.5s before `Buffering…`, then `Playing`;
  a stream that never starts still fails with an actionable error. The `Starting…` transient only applies
  while a playback command is in flight (`m.busy`): a settled `paused` at position 0 is a never-started
  track and reads `Paused`, while a finite queue that played to its end reads `■ Finished` (the helper
  reports `status:"ended"`). Initial loads display `loading…`; a refresh retains usable rows. Errors take precedence over empty hints;
  `r` or the selected surface number retries. Stale async responses cannot overwrite a new destination.
- Source switch stops active playback before changing source, then clears stack, search/filter/detail state and
  session cache. The target is validated against the newest descriptor snapshot before stop; dependent steps are
  serialized. A stop failure or cancellation retains the old source and playback. A later persistence failure
  rolls browsing back to the old source, but does not replay audio already stopped successfully.
- An **unconfigured Jamendo** turns the source switcher into its setup path: Enter opens the `Jamendo Setup` modal,
  which explains the free read-only devportal app and takes the app-level client_id (not a secret). `ctrl+o` opens
  the devportal, Enter validates against the API and saves to the Keychain in-process (the same path as
  `lilt jamendo setup`; the Client API stays unchanged), Esc cancels. Validation failures stay in the modal with the
  sanitized error and the typed value; an empty submit keeps the modal and shows `Client ID is required` instead of
  reading as a dead key. A success closes it, toasts the configured prefix, and refreshes
  `sources.list` — the server reads the credential lazily, so Jamendo becomes ready without a restart.
- Capability-driven UI: the TUI fetches `sources.list` at startup and gates shuffle, the library/trending
  previews, their footer hints, the Help shuffle/repeat line, and every queue-editing affordance (the
  `0 edit queue` hint, Help's Up Next rows and `e / E` row, Track Info's queue hint) by each source's declared
  capability (no per-source support list).
- `:auth` and the Home Account entry open the **Account overlay**, the actionable version of the account
  summary: one row per declared source in descriptor order, each with its live status from
  `authorization.list` (the whole list is re-read whenever `authorization.changed` arrives while the
  overlay is open). Enter dispatches by source — Apple Music begins a flow whose progress line follows
  the wire `Interaction.Type` (a system dialog prompt, or the flow URL plus "waiting for sign-in"; the
  URL may arrive after the begin response), Audius begins a flow and shows its URL, and Jamendo opens
  the existing setup modal. The link is never auto-launched: `ctrl+o` opens it manually (the same key
  precedent as the Jamendo devportal). A pending flow polls `authorization.flowStatus` every second and
  the server decides the terminal state, after which the list is re-read and the progress line hides.
  A failed begin (for example a source that already has a flow) shows an error row inside the overlay.
  `Esc` cancels a pending flow (`authorization.cancel`) and keeps the overlay open; without one it
  closes. `d` disconnects the selected source — a destructive action, so the first press shows a
  confirm row and the second executes; a failure keeps the overlay and wraps its sanitized reason
  across rows (the stable error code stays machine-facing, not in the notice). The per-source summary
  line itself is still fetched at startup and after a source switch (`authorization.status`).
- Radio Browse defaults to Popular Worldwide, pages at 100, supports retry and cached fallback, and `/` edits
  name/language/tag/country/sort. Esc restores Popular Worldwide only after clearing a local filter.
- Radio rows expose local reachability probes; probes never block navigation/playback. Radio is a live single
  stream (no queue); Apple Music, Audius, and Jamendo are mutually exclusive finite queues.
- Playback-control hints name their real scope (batch 2026-09-22-recheck): a finite queue playing with more
  than one item, and no live stream, shows `n next · b prev` beside pause/stop, and the queue hint reads
  `e queue next · E append` so it cannot be mistaken for skipping. Help annotates `n / b` by the live-stream
  gate, and hides `e / E` plus the Up Next rows entirely when the source does not declare the queue capability,
  instead of naming a source list.
- External metadata is terminal-sanitized. Small terminals show a too-small screen that states the current
  size (on its own row, so a narrow width never truncates it), the console minimum, and `q quit`; overlays
  remain cancellable. Click outside an overlay cancels it; list/queue clicks never move the viewport.
- The command palette names what each command does on its row (`:queue — focus the Up Next panel`,
  `:play <ref> — play a ref, e.g. apple-music:song:1440845629`): a bare keyword list made `:play` unusable
  without knowing what a "ref" is (batch 2026-09-23-polish p4).
- The footer keeps `q quit` visible at any width: when the joined hints exceed the width, later hints drop
  before the quit key does. Help always lists `q` under Interface.
- Help scrolls by entry, never by row: a page starts and ends on an entry start, so no page opens on an
  orphan continuation row and no entry is split across pages. The last page shows the tail in full. The
  status row reports the visible range and stays at the bottom of the box, and its box height reserves the
  row, so a full Help page or Track Info never clips the close hint; at narrow widths the status drops the
  scroll-keys hint before it ever drops `Esc/? close` (batch 2026-09-23-postaudit-recheck N4,
  2026-09-23-polish p4/p5).
- Overlay headers contain only stable identity. Shortcut help, active filters and scroll/range context render in
  the overlay body/status rows.

See [model.md](model.md) for Home availability, palette semantics, Client API integration, and requirements for
new renderers.
