# Spec: UI 模型与导航（Source / Surface / Home）

> **状态：已实现**（当前 terminal renderer）。本文是所有 lilt UI 的权威、renderer-agnostic
> 规范；当前 TUI 的布局细节见 [ux.md](ux.md)。任何新 UI（TUI、GUI、wizard、web、语音）
> MUST 以本文为产品语义来源，**不得**从 TUI 代码或像素布局反推语义。
>
> **读者**：想为 lilt 写新前端的贡献者，以及协助他们的 AI agent。
> **用法**：先读第 1–3 节建立词汇与数据来源；实现时严格对照第 4–14 节；第 16 节是可直接
> 执行的构建清单。所有 wire 细节以 [`docs/client-api/`](../client-api/README.md) 为准，
> 本文只写 UI 必须依赖的部分。

## 1. 术语与不变量

| 概念 | 含义 |
|---|---|
| **Source** | provider 暴露的内容域（`apple-music`、`audius`、`jamendo`、`radio`）。编译期注册，无运行期插件。 |
| **Surface** | 稳定、可寻址的顶层面（`home`、`discover`、`browse`、`recent`、`queue`、`auth`）。 |
| **Item** | 可播放或可进入的条目（`song`/`playlist`/`album`/`station`/`stream`）。 |
| **Action** | 语义操作（play/pause/next/previous/stop、favorite、queue-add、search、switch-source、jump）。 |
| **Page** | 被 push 的临时页（歌单详情、搜索结果），带自己的 items/selection/filter。 |
| **Overlay** | 不改变当前 surface 的浮层（search、source-switcher、palette、auth、help、info、theme、radio-discovery）。 |

不变量：

- UI **不拥有**播放状态、队列或持久状态；server 的唯一权威快照是唯一真值。
- capability 是**唯一**的可用性真值；UI 只读 `SourceDescriptor.capabilities`，不得维护并行
  “支持列表”。
- 播放严格互斥：同一时刻只有一个 Source 在播放。Source 切换是一次**原子、显式、有清理**的
  用户动作，不是 tab 循环。
- 短期/签名媒体 URL **绝不进入** UI 的持久字段、公开事件或日志；只在播放启动时由 server 解析。
- 没有 legacy/兼容路径：UI 只实现当前模型。

## 2. 数据来源（Client API）

UI 只通过 Client API 与 server 交互（见 [commands.md](../client-api/commands.md)、
[watch.md](../client-api/watch.md)）。最小必需集合：

| 目的 | 命令 / 事件 |
|---|---|
| 列出 source 与 capability | `sources.list`、`sources.changed` |
| 当前播放 | `session.status`（`includeQueue=true` 取完整 `PlaybackState`）、`playback.changed` |
| 播放动作 | `playback.play`、`playback.playSongs`、`playback.pause`、`playback.resume`、`playback.toggle`、`playback.next`、`playback.previous`、`playback.stop`、`playback.setShuffle`、`playback.setRepeat` |
| 队列 | `queue.list`、`queue.add`、`queue.jump`、`queue.remove`、`queue.move`、`queue.clear` |
| 发现 | `discovery.search`、`discovery.trending`（需 `search.trending`）、`album.tracks`、`playlist.tracks`、`library.albums`、`library.playlists`（需 `library`）、`recent.list`、`recommendations.list`（需 `recommendations`） |
| 电台 | `radio.search`、`radio.options`、`radio.probe` |
| 本地状态 | `state.get`、`favorites.list`、`favorites.set`、`ui.set`（`theme`、`lastSource`） |
| 授权 | `authorization.list`、`authorization.status`、`authorization.begin`、`authorization.flowStatus`、`authorization.cancel`、`authorization.disconnect` |

`session.watch` 订阅 `playback.changed`、`state.changed`、`sources.changed`、`authorization.changed`，
并可能收到 `server.warning`、`engine.restarted`。**只有 `position` 可以本地插值**以平滑进度；
queue/track/status MUST 来自最近的权威快照。事件带单调 `sequence`，迟到或更旧的事件必须丢弃。

## 3. 模型类型

```text
UIState {
  sources:        [SourceDescriptor]      // 来自 sources.list / sources.changed
  currentSource:  SourceId                // 默认取 ui.set.lastSource，回落第一个可用 source
  currentSurface: SurfaceId               // 默认 home
  playback:       PlaybackState           // 权威快照；含 sequence
  authorization:  [SourceAuthorization]
  stack:          [Page]                  // 可为空
  overlay:        Overlay                      // 见第 10 节
  selection:      int                     // 当前 surface 的可操作项索引；header 不可选
  filter:         string                  // 仅当前临时列表；不持久化
}

SourceDescriptor { id, displayName, availability, capabilities: {name: {available, description?}} }
Page   { source, surface, title, items: [Item], selection, filter, detailKind, detailID, pageClass }
Item   { source, kind, id, ref, title, artist, url?, previewUrl?, radio? }
       // kind ∈ song | playlist | album | station | stream；header/entry 是纯 UI 行，不属于 Item
       // pageClass ∈ container | aggregate；“该列表是什么意图”只由它决定，不得从 title 文本推断
HomeRow = SectionHeader(title) | PreviewRow(Item) | EntryRow(Action) | ContinueRow(PlaybackState)
```

不变式：

- `selection` 永远落在可选行上；header 与不可用 entry 不是选择目标。
- `stack` 中的 `Page` 自带完整状态，pop 时恢复；push 不改变 `currentSurface` 语义。
- `filter` 只作用于当前临时列表，切换 surface/source 时清空；不写回 server。
- `Item.ref` 是唯一可回传 server 的规范引用（`playback.*`/`queue.add` 只接受 canonical ref）。

## 4. Surface catalog

| id | label | availability | 内容来源 | 主要动作 |
|---|---|---|---|---|
| `home` | Home | 每个 source 恒有，默认 | 第 5 节聚合 | 打开预览/入口 |
| `discover` | Discover / Browse | Audius=trending；Jamendo=song-only trending；Radio=directory | `discovery.trending`（Audius/Jamendo）；`radio.search`+`radio.options`（Radio） | play、open playlist、`/` 改 query |
| `browse` | Browse | `radio` | `radio.search`（分页）、`radio.options` | play、`/` 查询、`S` 重排 |
| `recent` | Recent | 每个 source | `recent.list`（由 Playback History 派生，实际听够阈值的 Item） | play、open playlist |
| `queue` | Up Next | 有 finite queue（Apple/Audius/Jamendo） | `session.status`/`PlaybackState.queue` | jump/remove/move/clear |
| `auth` | Account | command/palette 入口；Home 的 Account entry 同入口 | `authorization.list`（overlay 打开期间由 `authorization.changed` 触发整表重读） | 查看各 source 授权状态、发起/取消 sign-in、断开（Account overlay，第 10 节） |

**当前每个 source 的顶层表面集合严格为**：

- Apple Music：`Home`、`Recent`
- Radio：`Home`、`Browse`、`Recent`
- Audius：`Home`、`Discover`、`Recent`
- Jamendo：`Home`、`Discover`（song-only trending：`search.trending.songs`）、`Recent`

`favorites` 与 `playlists` **不是 surface**，只是 Home preview + `Go to` 全量页（All Favorites /
All Playlists push 临时 Page）。Favorites 页按 `addedAt` 最新在前，不设上限。`search` 不是 surface，是 `/`
overlay，结果 push 成临时 `Page`，并按 `Songs` / `Albums`（仅声明 `search.albums` 的 source）/ `Playlists`
分组。UI MUST NOT 引入未在此列出的顶层表面。

## 5. Home composition

Home 是线性有序的 rows；每个 preview ≤ **5** 条；空 section 完全隐藏；不提供折叠/开关。

```text
home(source):
  rows = []
  if playback for source has an active finite queue:
      rows += SectionHeader("Continue Playing") + ContinueRow(playback) + EntryRow(queue)
  # 只有对应 capability/source 才请求，且在渲染前按 source 再 gate 一次
  # 区块顺序对齐 Apple Music「立即收听」：正在播 → 推荐 → 热门 → 最近 → 歌单 → 收藏
  if source declares recommendations:                  # Apple Music（helper 与 browser 两引擎）
      recommended = recommendations.list(source)       # 登录态决定内容而非 capability
      if recommended nonempty: rows += Header("Recommended") + first(recommended, 5)
  if source declares search.trending / search.trending.songs:
      rows += Header("Trending") + first(discovery.trending(source), 5)
  recent = recent.list                                  # 全 source，由 Playback History 派生
  if recent nonempty: rows += Header("Recently Played") + first(recent, 5)
  if source declares library:   # Apple, or Audius when linked
      rows += Header("Your Playlists") + first(library.playlists(source), 5)
  favorites = favorites.list(source)                    # 全 source，本地，按 addedAt 最新在前
  if favorites nonempty: rows += Header("Favorites") + first(favorites, 5)
  rows += Header("Go to") + entries                    # 恒定
  return rows

entries = [Search]                         # 恒有
        + ([Browse]   if source == radio)
        + ([Discover] if source declares search.trending / search.trending.songs)
        + [Recent]
        + [All Favorites]                  # 全量本地收藏页（Home 只预览 5 条）
        + ([All Playlists] if source declares library)   # 全量歌单页（Home 只预览 5 条）
        + ([Albums]       if source declares library)   # 资料库专辑页；Enter push 专辑详情页
        + ([Queue]    if active finite queue)
        + ([Account]  if source exposes authorization)
```

规则：

- Browse 的大量结果**永远不嵌入** Home；只给 Browse 入口。
- 每次进入 Home 都动态加载；初始帧可以是 loading。可选 slice 必须在 capability/source
  边界再 gate，**不得因陈旧 cache 显示 Recommended、Trending 或 library**。
- 每个 preview 走独立请求；单个失败不阻塞其它 row（该 row 隐藏或显示错误）。
- Recommended 的空态归属：登录态影响**内容**而非 capability（与 helper 模式语义一致）。
  未登录时 `recommendations.list` 返回空或错误 → 区块隐藏；不为它显示 loading 遗留或错误行。
  推荐分组的拍平规则由 server 侧决定（playlists/albums 为行，browser 引擎丢弃无 station
  播放路径的 stations），UI 不解析分组。

## 6. Item 与激活语义

| kind | 可选 | Enter/激活行为 |
|---|---|---|
| `song` | 是 | **依页面的 `pageClass` 而定**：`container`（显式打开的歌单/专辑详情）= 从该曲播到容器末（`playback.play{..., startAt/startTrackID, fromHere:true}`，队列从该曲到末尾、丢弃历史）；`aggregate`（搜索结果等查询页）= 只播该行（`playback.play`），因为列表是查询的证据而不是用户组装的意图；surface（Home/Recent/Discover）= 从该曲播到本节末（`playback.playSongs(refs[selected:sectionEnd], 0)`），本节只有一首时回退 `playback.play`。 |
| `playlist` | 是 | push playlist detail（`playlist.tracks`，`pageClass: container`），不立即播放；detail 内再选曲 |
| `album` | 是 | push album detail（`album.tracks`，`pageClass: container`），不立即播放；detail 内 Enter = 从该曲播放到专辑末，`p` 播放整张专辑；列表不重复专辑行，页头与 context row 承担专辑名 |
| `station` / `stream` | 是 | `playback.play`（Radio stream / preview） |
| `header` | 否 | — |
| `entry`（Search/Browse/Recent/Queue/Account） | 是 | 执行对应 Action |
| `continue` | 是 | 聚焦 Up Next 或跳到当前项 |

“本节”指当前列表中连续的同 kind 区块（到下一个 header 或换 kind 为止）。radio 没有有限队列，
所以 stream 始终单曲播放；`p` 仍是单曲 play/toggle，只有 Enter 带“从这儿开始”语义。

`pageClass` 是唯一意图真值（不得用 `title` 前缀等显示文本判定）：

- `container`：由用户显式打开的一个序列（歌单/专辑详情）。
- `aggregate`：查询结果页（`pushAggregate`，当前只有搜索结果）。列表只是证据，Enter 不排队。
- 无 `pageClass`：surface（Home/Recent/Discover/Browse）与普通 pushed 列表，保持“继续听”的
  节内连播。

搜索结果页不再提供“把这一节连着播”的动作；需要连播时逐行 `e`/`E` 入队，或由 CLI/agent 用
`playSongs`。`Esc`/Backspace 返回时 `pageClass` 与页面一起恢复。

Radio `browse` 结果按 `radio.origin` 标注来源（`builtin` / `directory`）。

## 7. Action catalog

| Action | server 命令 | 前置条件 | 失败语义 |
|---|---|---|---|
| play item | `playback.play` / `playback.playSongs` | canonical ref | `playback_error`；`details.state` 为最终状态 |
| pause/resume/toggle | `playback.pause`/`resume`/`toggle` | 有当前项 | 无当前项 → `invalid_state` |
| next/previous | `playback.next`/`previous` | finite queue | 无队列 → `finite_queue_required` |
| stop | `playback.stop` | — | 总是成功、幂等 |
| shuffle/repeat | `playback.setShuffle`/`setRepeat` | finite queue | 无队列 → `finite_queue_required` |
| favorite | `favorites.set` | Item | 幂等；失败不改变权威状态 |
| queue add | `queue.add`（TUI 带 `ifQueueRevision`） | finite queue，ref 同源 | 修订不符 → `conflict`（含最新 revision） |
| queue jump/remove/move/clear | `queue.*` | finite queue | `queue_unavailable` / `conflict` |
| search | `discovery.search`（`source` 必填） | source 有对应 search capability | 指定 kind 不支持 → `unsupported_command` |
| switch source | 第 8 节原子序列 | — | 失败保留原 source/播放 |
| refresh | 重新发对应 discovery 命令 | — | 旧请求作废（generation） |

错误码与 `details` 形状以 [errors.md](../client-api/errors.md) 为准；UI 只展示稳定 code 与
脱敏信息，上游原文只可作 `details.providerCode`。

## 8. Source switching（原子）

`s` 打开 source-switcher overlay：列出所有 source + availability + 关键 capability 摘要，
当前 source 高亮。对**不同** source 按 Enter 时执行一次原子、用户可见的转移：

1. 先确认目标存在于最新 `SourceDescriptor` 快照，且至少一个目标播放 capability 可用；无效或不可用目标不得停止当前播放。
   唯一例外：目标为未配置的 Jamendo 且 UI 具备进程内 setup 能力时，Enter 打开 `jamendo-setup`
   modal（输入 client_id → 校验 → 直写 Keychain），成功后刷新 `sources.list`；校验失败保留输入与 modal。
2. 若当前 source 正在 playing/paused/buffering：先 `playback.stop`（会清空该 source 的临时有限队列）。
3. stop 失败：保留原 source、播放与 overlay，显示错误；**不**迁移。
4. 清空 push stack、search/filter、detail 状态与全部会话级列表 cache，并把浏览 source 转到目标的默认 `home`。
5. 发送 `ui.set({lastSource})`；成功后加载 Home 并关闭 overlay。保存失败时回滚浏览 source，保留 overlay 并显示错误；已经成功的 stop 不自动重放。

Esc 取消且无任何变更。该语义来自 server 的 active-source 互斥；**不是** tab 循环，也**不得**
在 mid-queue 跨 source fallback。

## 9. Navigation 状态机

| 状态 | 输入 | 转移 |
|---|---|---|
| top-level | `1..n` / `[`/`]` | 选择**可用** surface；重选错误页触发重试 |
| top-level | `Enter` | play item / push detail / 执行 Home entry |
| pushed page | `Esc` / Backspace / `h` | pop stack，恢复保存的页面状态 |
| 任意普通页 | `/` | Search overlay（Radio 为 query builder） |
| 任意普通页 | `s` | source-switcher overlay |
| 任意普通页 | `:` | palette overlay |
| overlay | `Esc` | 取消；不修改 surface/source/playback |
| source-switcher | `Enter` | 第 8 节原子转移 |
| palette | `Enter` | 第 10 节执行命令 |

所有异步加载携带 generation + destination；**陈旧结果必须忽略**，不得清除当前 loading。

## 10. Overlay 与 `:` 命令面板

Overlay 类型：`search`、`source-switcher`、`palette`、`auth`、`help`、`info`、`theme`、`radio-discovery`、`jamendo-setup`（复用文本输入形态，见第 8 节）。
Overlay 独占键盘焦点；`Esc` 取消且不产生副作用。help overlay 只由 `Esc`/`q`/`?` 关闭，
其他键既不生效也不关闭 help（避免吞掉用户想执行的键）；overlay 内已声明的控制键（如滚动）
仍然生效。

`:` 打开聚焦输入的命令面板：输入实时过滤候选命令。**空输入不高亮任何候选**，直接 Enter 是
no-op；一旦输入，自动高亮第一个匹配项。`Tab`/`↓` 与 `Shift-Tab`/`↑` 在候选间循环移动高亮
（**不**改写已输入文本）；无高亮时 `Tab` 选第一个、`Shift-Tab` 选最后一个。`Enter` 执行
**当前高亮**的候选；若输入带有无法匹配任何候选的自由参数（如 `play am:123`），则按输入执行。
`Esc` 关闭；未知命令报 `Unknown command: :…`。已实现命令集：

| 命令 | 语义 |
|---|---|
| `:home` | 切到当前 source 的 Home |
| `:browse` | 切到 `browse`（仅 Radio） |
| `:discover` | 切到 `discover`（声明 `search.trending` 的 source） |
| `:recent` | 切到 `recent` |
| `:queue` | 聚焦 Up Next；无队列时提示而非臆造 |
| `:auth` | 打开 Account overlay（见下） |
| `:help` | 打开帮助 overlay |
| `:source <id>` | 执行第 8 节原子切换 |
| `:play <ref>` | `playback.play` 该 canonical ref |

命令必须在执行时校验 availability；扩展时只加命令，不加“模式”。命令集故意保持小而明确，
按实际使用增补。

### Account overlay（`auth`）

`auth` overlay 是 Account 摘要（Now Playing 的授权受限提示、Home 的 Account entry）的
**可操作版本**：一行一个已声明 source（descriptor 顺序），显示 `authorization.list` 的实时
状态；overlay 打开期间收到 `authorization.changed` 就整表重读。打开入口是 `:auth` 与
Home 的 Account entry。

- **Enter 按 source 分派**：
  - apple-music / audius → `authorization.begin`。进度行的文案由 flow 的 `Interaction.Type`
    驱动：`system_dialog` → 等待系统授权对话框；`browser` → 显示 flow URL（可能晚于 begin
    到达）并等待登录。UI **不按平台分支**——helper 与浏览器引擎的差异已由 wire 收敛。
    URL 的打开是手动的（`ctrl+o`，复用 jamendo-setup modal 的注入点与键位先例），**不自动开窗**。
  - jamendo → 直接打开 `jamendo-setup` modal（进程内 client_id 配置，见第 8 节），不发起 flow。
  - 无授权概念的 source（radio）Enter 无动作。
- **flow 进度**：begin 成功后按 1s 轮询 `authorization.flowStatus`；终态由 server 判定。
  终态（authorized/denied/expired/cancelled/error）→ 停止轮询、重读列表、隐藏进度行。
  begin 失败（如该 source 已有 flow）在 overlay 内显示错误行，不静默。
- **Esc**：flow pending 时是 `authorization.cancel`（带 flowId），overlay 保留供查看结果；
  无 pending flow 时关闭 overlay。overlay 外的点击只是关闭，不打断 server 持有的 flow。
- **断开**：`d` 对选中行执行 `authorization.disconnect`。它是破坏性操作（移除机器级
  credential，helper 模式的 apple-music 同此语义），第一次 `d` 显示确认行、第二次 `d` 才执行；
  移动选择即取消确认。成功后重读列表。

## 11. Key map（推荐，语义 MUST 保留）

| 范围 | 键 | 行为 |
|---|---|---|
| 全局 | `Space`/`c` | pause-resume |
| 全局 | `n`/`b` | next/previous（有限队列） |
| 全局 | `v` | stop |
| 全局 | `f` | favorite 当前项 |
| 全局 | `/` | search overlay |
| 全局 | `s` | source-switcher |
| 全局 | `:` | command palette |
| 全局 | `?` | help |
| 全局 | `1..n` / `[`/`]` | 选择 / 循环可用 surface；pushed 结果页 `[`/`]` 在结果分组间跳转 |
| 全局 | `q` / Ctrl-C | 退出 |
| 列表 | `j`/`k`、方向键、`g`/`G`、Ctrl-U/D、Ctrl-B/F | 移动与翻页 |
| 列表 | `Enter` | 打开/播放 |
| Up Next | `0` 聚焦；`Enter`/`p` 跳转；`x` 删除；`J`/`K` 移动；`c` 清空 | 队列编辑 |
| 播放 | `S` | shuffle **开关**（所有 surface 同一语义）；Radio Browse 用 `S` 显式重排。乱序播放一个容器 = 先 `S` 打开，再 `Enter`/`p` |
| 播放 | `R` | cycle repeat |
| 播放 | `e`/`E` | queue next / append |
| Radio | `a` | 添加并播放 stream URL |
| Apple | `F` | 过滤列表 |

`Tab` **不**切换 source；在文本输入中无副作用。search 固定为 `/`；不提供 `Ctrl-P` 或任何
第二入口。renderer MAY 重映射按键，但 MUST 保留上表行为与“source 切换是显式动作”。

**Hint 放置**：顶部只呈现**位置**（当前 source/surface），不呈现快捷键提示；底部只呈现**与当前
surface 相关、最可能被用到**的快捷键，具体项在前、全局/罕见项在后，宽度不足时从尾部截断。
`model.md` 的全局键（`s`、`:`、`?`、`q`）属于底部靠后项，不得在顶部重复。

## 12. Renderer 契约

新 UI **MUST** 渲染：

- 当前 source 与 surface（面包屑或等价指示）。
- 可选行 vs 不可选 header；loading / error / empty 三态与可重试入口。
- 播放状态（status、当前项、position）；有 finite queue 时渲染队列及其编辑。
- overlay 的焦点与取消语义。
- capability 不可用时的原因（来自 `SourceDescriptor`），而不是静默隐藏或伪造。

新 UI **MUST NOT**：

- 假设存在 tab、固定 screen 几何、固定 source 数量或顺序。
- 假设每个 source 都支持 queue/library/trending。
- 自行推测 queue/track/status，或缓存绕过 server 权威快照。
- 持久化或渲染短期签名媒体 URL；渲染外部元数据前必须脱敏。

可访问性建议：状态不只用颜色区分；错误信息包含稳定 code；键盘可达所有操作。

## 13. 生命周期与持久化

- 启动：若 server 不存在则拉起（TUI 自动启动），否则 attach；订阅 `session.watch`，并直接使用其
  原子初始快照中的 playback、AppState、sources 与 authorizations。只有不提供完整初始快照的非生产
  feed 才可发 fallback read，且必须按启动 sequence 丢弃晚于 watch 的旧结果。
- `currentSource` 默认取 `ui.set.lastSource`；不可用时回落第一个可用 source。**启动时的首次播放
  快照**若显示另一 source 正在 playing/paused/buffering，UI SHOULD 把浏览 source 对齐到该
  source（仅导航，不停播，并更新 lastSource），避免用户启动后被迫做一次“停播式”切换。
- 收藏、recent、`lastSource`、theme 由 server 持久化；UI 只通过 `favorites.set`/`ui.set` 写入。
- Bubble Tea 的 command、watch 与本地状态合并必须遵守
  [异步命令与状态一致性](async-state.md)：`Update` 不阻塞、mutation 串行、异步查询捕获不可变快照，
  持久状态只由原子初始快照与有序 watch event 写入。
- 退出 UI **不**停止播放；停止播放必须显式 `playback.stop`。
- 收到 `server.warning` 提示用户；`engine.restarted` 后等待同一 watch 流中随后到达的完整
  `playback.changed`，不得用独立、无版本 RPC read 覆盖它。

## 14. 构建新 UI 的清单

1. 连接：实现 Client API 传输（Unix socket），调 `sources.list`、`state.get`，订阅 `session.watch`。
2. 建 `UIState`（第 3 节），把 watch 事件接到对应字段；实现 sequence 单调与陈旧丢弃。
3. 渲染 Surface 集合：按 `currentSource` 选第 4 节的表面列表；只渲染可用项。
4. 实现 Home 算法（第 5 节），逐 row 请求；caption 与 gating 对齐 capability。
5. 实现 Item/激活（第 6 节）与 Action（第 7 节）；所有播放/队列写操作带权威 state。
6. 实现导航状态机（第 9 节）与 overlay（第 10 节），含 push/pop 与 generation 隔离。
7. 实现原子 Source 切换（第 8 节）与 `:` 命令面板（第 10 节）。
8. 按渲染契约（第 12 节）补齐 loading/error/empty/queue/authorization 状态。
9. 用 hermetic fixture 测 Home gating、原子切换、陈旧结果、capability 路由；真实 E2E 仅 opt-in。

映射示例：

- **当前 TUI**：identity 行左侧为当前位置/Source、右侧为 `lilt` 品牌；数字 surface 行以 `› ` 标记
  活动 surface；工作区为主列表 + Up Next rail；Now Playing 横跨全宽并位于工作区下方；居中 overlay。
  组件与信息层级以 [`design-system.md`](design-system.md) 为准。
- **Wizard/installer UI**：同一模型映射为步骤——**选 source → 选 Home/surface → 选 item →
  确认播放**，配面包屑与 Back；overlay 可做成独立对话框步骤。两者都不改变 Source 切换的
  原子性与 Home 规则。

## 15. 反模式

- 用 tab 行承载 source 或 surface（已移除）。
- 在 Home 内嵌 Browse 全量结果，或提供折叠/开关堆叠。
- 维护并行 capability “支持列表”，或静默降级 `unsupported_command`。
- 把短期签名 URL 写进状态、事件、日志或 fixture。
- 保留 deprecated 别名、格式嗅探或“万一”的兼容分支。

## Links

- [ux.md](ux.md) — 当前 TUI 布局与细粒度反馈
- [async-state.md](async-state.md) — Bubble Tea command、watch sequence 与状态一致性
- [sources.md](../internals/sources.md) — identity 与 provider 视图
- [providers.md](../internals/providers.md) — provider/capability/传输设计
- [models.md](../client-api/models.md)、[commands.md](../client-api/commands.md)、
  [watch.md](../client-api/watch.md)、[errors.md](../client-api/errors.md)
