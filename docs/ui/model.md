# Spec: UI 模型与导航（Source / Surface / Home）

> **状态：已实现。** 这是所有 lilt UI 的权威、renderer-agnostic 规范；当前 terminal
> renderer 的像素级布局见 [ux.md](ux.md)。新 UI 不得从 TUI 代码或布局推导产品语义。

## 1. 模型

UI 读取 server 的 `SourceDescriptor`、`PlaybackState` 和本地状态投影，并维护下列短暂状态：

```text
NavigationState {
  currentSource: SourceId
  currentSurface: SurfaceID
  stack: [Page]                 // playlist detail、search result 等 pushed page
  overlay: none|search|source-switcher|palette|help|info|theme|radio-discovery
  selection: item index         // 当前可操作项；header 不可选择
  filter: string                // 仅当前临时列表；不持久化
}
Page { source, surface, title, items, selection, filter, detailKind, detailID }
Surface { id, label, availability, content, actions }
HomeRow = header | preview Item | entry Action | continue
```

`Source` 是 provider 暴露的内容域；`Surface` 是稳定、可寻址的顶层面；`Item` 是可播放或可进入条目；
`Action` 是 play/pause/next/previous/stop、favorite、queue-add、search、switch-source 或 jump。
Overlay 不改变当前 surface。短期媒体 URL 不属于任一模型字段。

## 2. Surface catalog

| id | label | 内容 | 当前 availability | 主要动作 |
|---|---|---|---|---|
| `home` | Home | §3 的聚合行 | 每个 source 恒有，默认 | 打开条目/入口 |
| `discover` | Discover | Audius trending tracks/playlists | `audius` | play/open playlist |
| `browse` | Browse | Radio directory、query 与分页 | `radio` | play、`/` 改 query |
| `recent` | Recent | source-scoped local recent；Apple 可合并 provider recent | 每个 source | play/open playlist |
| `queue` | Up Next | active finite queue | 有 Apple/Audius queue | jump/remove/move/clear |
| `auth` | Account | source authorization summary | command/palette entry | show status |

`favorites` 和 `playlists` **不是 surface**：它们是 Home preview。`search` 是 `/` overlay，结果为
pushed temporary page，不是 surface。当前可选顶层集合严格为：Apple Music `Home, Recent`；Radio
`Home, Browse, Recent`；Audius `Home, Discover, Recent`。

## 3. Home composition

Home 是按顺序的线性 rows；每个 preview 最多 **5** 个，空 section 完全隐藏，不提供折叠。

1. `Continue Playing`：仅 active finite queue；显示当前条目及 Queue 入口。
2. `Recently Played`：source-scoped recent containers 后接 recent items，总数 ≤5。
3. `Trending`：仅 `search.trending` 可用（当前 Audius）。
4. `Your Playlists`：仅 `library` 可用（当前 Apple Music）。
5. `Favorites`：source-scoped local favorites。
6. `Go to`：始终有 Search 与 Recent；Radio 加 Browse，Audius 加 Discover；活动 queue 加 Queue；Apple
   加 Account。

Browse 的大量结果永远不嵌入 Home；只提供 Browse 入口。实现必须在 capability/source 边界再次
gate optional slices，不能因陈旧 cache 显示 Trending 或 library。Home 每次进入均动态加载：它可读取
playback/local state，并按 source 请求 recent/trending/library；初始帧可为 loading。

## 4. Navigation state machine

| state | input | transition |
|---|---|---|
| top-level | `1..n`, `[`/`]` | 选择可用 surface；reselect error surface 重试 |
| top-level | `Enter` | play item、open playlist/detail，或执行 Home entry |
| pushed page | `Esc`/Backspace/`h` | pop stack，恢复保存的页面状态 |
| any normal page | `/` | Search overlay（Radio 为 query builder） |
| any normal page | `s` | source-switcher overlay |
| any normal page | `:` | palette overlay |
| overlay | `Esc` | cancel；不修改 surface/source/playback |
| source-switcher | `Enter` | §6 atomic source transition |

`r` reloads an errored list. Radio Browse with a non-default query consumes Esc to restore Popular Worldwide;
a local filter is cleared first. All async loads carry a generation and destination; stale results must be ignored.

## 5. Key map

Global: `Space`/`c` pause-resume, `n`/`b` next-previous for finite queues, `v` stop, `f` favorite,
`/` search, `s` source switcher, `:` palette, `?` help, `1..n` surface, `q`/Ctrl-C quit.

Lists use `j`/`k` or arrows, `g`/`G`, Ctrl-U/D, Ctrl-B/F, and Enter. `0` focuses Up Next; focused queue
uses Enter/`p` jump, `x` remove, `J`/`K` move and `c` clear; unowned global keys still work. `S` toggles
shuffle (or plays a playlist shuffled; Radio Browse explicitly re-sorts); `R` cycles repeat; `e`/`E` queue
next/append. `a` adds and plays a Radio URL; `F` filters Apple lists. Text controls own all printable keys.
`Tab` never switches source; in text input it is inert. Search remains `/`; there is no Ctrl-P binding.

## 6. Source switching

`s` renders all sources with availability and capability summary, current source selected. On Enter for a
different source, the UI performs one atomic user-visible transition:

1. if playing/paused/buffering, call `playback.stop` (which clears the temporary finite queue);
2. if stop fails, retain the old source, playback, and overlay state and show an error;
3. clear stack, search/filter state, detail state, and all session list cache;
4. set the new source to its default `home`, load it, and persist `ui.set(lastSource)`;
5. dismiss the overlay only after successful stop/switch.

Esc cancels with no mutation. This follows server active-source mutual exclusion; it is not a tab cycle.

## 7. `:` palette

The palette is a focused text overlay. It filters command names while typing; Tab completes the first matching
name or parameter. Enter executes and dismisses it; an unknown command reports `Unknown command: :…`.
The implemented catalog is `:home`, `:discover`, `:browse`, `:recent`, `:queue`, `:auth`, `:help`,
`:source apple-music|audius|radio`, and `:play <ref>`. Source/surface commands enforce their availability;
`:queue` reports no active queue rather than inventing one; `:play` requires a nonempty canonical ref.

## 8. Renderer contract and integrations

A renderer **must** render current source/surface, selectable rows versus non-selectable headers, loading/error/
empty state, playback state, queue when present, overlay focus/cancellation, and availability reasons. It must
derive actions from surface/capability/state, preserve async generation isolation, sanitize external metadata,
and never persist or render transient signed media URLs. It **may not** assume tabs, screen geometry, a fixed
number/order of sources, or that every source supports queue/library/trending.

Current TUI mapping: breadcrumb `SOURCE: X · SURFACE`, numeric surface row, main list, optional Up Next rail,
Now Playing dock, and centered overlays. A wizard/installer UI can map the exact same model to steps:
**choose source → choose Home/surface → choose item → confirm play**, with a breadcrumb and Back; overlays can
be separate dialog steps. Neither mapping changes source-switch atomicity or Home rules.

Client API integration: call `sources.list` for descriptors; `discovery.search`, `discovery.trending`,
`library.playlists`, `recent.list`, `radio.search/options`, `playlist.tracks`, and `favorites.*` for content;
use `playback.*`/`queue.*` for actions and `ui.set` for last source. Subscribe with `session.watch` to
`playback.changed`, `state.changed`, `sources.changed`, and `authorization.changed`; apply monotonic sequence
updates and refresh affected projections.

## Links

- [ux.md](ux.md) — current TUI layout and detailed feedback
- [sources.md](../internals/sources.md) — identities and provider views
- [Client API models](../client-api/models.md) and [commands](../client-api/commands.md)
