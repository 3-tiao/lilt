# Client API —— 命令

每个命令的参数、返回与语义。数据形状见 [`models.md`](models.md)；事件订阅见
[`watch.md`](watch.md)；错误码见 [`errors.md`](errors.md)。

表格中的“预算”是 server 的内部超时（见 [`protocol.md`](protocol.md)）。

## 1. 契约与来源

| command | params | data | 预算 |
|---|---|---|---:|
| `api.describe` | — | `ApiDescription`（命令、schema、错误码、来源规则） | 1s |
| `sources.list` | — | `[SourceDescriptor]` | 5s |

CLI：`lilt api --json`、`lilt sources --json`。

## 2. 播放

| command | params | data | 预算 |
|---|---|---|---:|
| `playback.play` | `{ref, name?, shuffle?, repeat?, startAt?, startTrackID?, reverse?, fromHere?}` | `PlaybackState` | 60s |
| `playback.playSongs` | `{refs: [Reference], startIndex?, shuffle?, repeat?}` | `PlaybackState` | 60s |
| `playback.pause` | — | `PlaybackState` | 5s |
| `playback.toggle` | — | `PlaybackState` | 5s |
| `playback.resume` | — | `PlaybackState` | 5s |
| `playback.next` | — | `PlaybackState` | 5s |
| `playback.previous` | — | `PlaybackState` | 5s |
| `playback.stop` | — | `PlaybackState` | 5s |
| `playback.setShuffle` | `{on: bool}` | `PlaybackState` | 5s |
| `playback.setRepeat` | `{mode: "off" \| "all" \| "one"}` | `PlaybackState` | 5s |

CLI：

```text
lilt play <ref> [--name TEXT] [--shuffle] [--repeat off|all|one] --json
lilt play-songs <ref,ref,...> [--start N] [--shuffle] [--repeat off|all|one] --json
lilt pause|toggle|resume|next|previous|stop --json
lilt shuffle on|off --json
lilt repeat off|all|one --json
```

语义：

- `stop` 只停止播放，server 继续运行；总是成功且幂等，返回 stopped/no track/空队列。
- `pause`/`resume` 是 desired-state：已 paused 的 pause、已 playing 的 resume 都成功
  no-op；buffering 表示已处于“希望播放”的状态，因此 resume 也成功 no-op。pause
  保留当前项和位置。preview 与 stream/live 使用相同状态规则。
- `toggle` 在 playing/buffering 时进入 paused，在 paused 时恢复；stopped、error 或没有
  当前项时返回 `invalid_state`，不猜测要恢复什么。
- 所有状态的 `stop` 都成功：停止音频并清空当前项与队列；已经 stopped 时为 no-op。
- source 切换启动失败时，旧 source 保持 stopped，不隐式恢复；返回 `playback_error`，
  `details.state` 是最终 `PlaybackState`。

状态控制矩阵：

| 命令 | playing | paused | buffering | stopped / error / 无当前项 |
|---|---|---|---|---|
| pause | 进入 paused | 成功 no-op | 进入 paused | `invalid_state` |
| resume | 成功 no-op | 恢复播放 | 成功 no-op | `invalid_state` |
| toggle | 进入 paused | 恢复播放 | 进入 paused | `invalid_state` |
| stop | 进入 stopped 并清空 | 进入 stopped 并清空 | 进入 stopped 并清空 | 成功 no-op |

队列控制与上表状态正交：

| 命令 | 有限队列 | 无队列 / preview / stream/live |
|---|---|---|
| next / previous | 切换当前项；原状态为 paused 时保持 paused，否则启动新项 | `finite_queue_required` |
| setShuffle / setRepeat | 应用目标值；相同值成功 no-op | `finite_queue_required` |

任何 source/engine 未声明支持的操作仍返回 `unsupported_command`；这表示能力缺失，
不同于当前状态不允许操作的 `invalid_state`。
- `play --shuffle --repeat all` 是一个**逻辑命令**：server 先成功启动播放，再
  设置 shuffle/repeat，最后返回 resulting state。
  - **新播放不继承上一次的形态**：MusicKit 会跨播放保留 shuffle/repeat，所以 `play` 与
    `playSongs` 不传 `shuffle`/`repeat` 时，本次播放从 `shuffle:false`、`repeat:"off"` 开始
    （server 显式下发默认值）。只有调用方显式请求的形态才会与默认值不同。
  - **形态参数受 capability 限制**：来源未声明 `shuffle` / `repeat` 时，传入对应参数在
    起播前就返回 `unsupported_command`（不静默忽略、不启动播放）——capability 是唯一真值，
    调用方不应相信未生效的形态。
  - 执行期间 server MUST 暂存该命令引起的 helper 通知，不能向 watch client 发布
    中间的“已播放但未 shuffle/repeat”状态。
  - 主操作与附加操作结束后，server 为 resulting state 分配一个 sequence 并发布
    最多一个语义事件；该 sequence 与 response 中的 `state.sequence` 相同。
  - 如果来源**声明**了能力但实际设置失败（例如 engine 报错），返回 `partial_failure`，
    `error.details` MUST 含 `state` 与 `applied`；不得谎称播放失败，也不回滚已开始的音频。
- `playback.play` 可选 `startAt` / `startTrackID` / `reverse` / `fromHere`：歌单或专辑从指定曲目
  开始（`startTrackID` 优先于 `startAt`），`reverse` 同时反转队列顺序与起点选择（Apple 本地化
  「喜爱歌曲」用）。`fromHere:true` 表示**向前播放**：丢弃起点之前的曲目，队列从所选曲开始
  （TUI 歌单/专辑详情的 Enter 用它；`p` 播放整个容器）。CLI 暂未暴露这些字段。
- `playback.playSongs.refs` MUST 非空并使用 discovery 返回的 canonical `Item.ref`；server
  从 refs 推导唯一 Source。所有 refs MUST 属于同一 finite-queue Source，否则返回
  `source_mismatch`。Apple Music 与 Audius 是当前指定的 finite-queue Source；wire 与 CLI
  都只接受 canonical refs。Apple 端由 server 编排为"起播首选曲目 + 逐条 enqueue"；
  个别无法入队的曲目被跳过（helper stderr 记录），不再让整批播放失败。
- 播放严格互斥：开始另一 Source 前 MUST 停止当前 Source 并清空/替换旧有限队列；新 start
  失败时最终状态保持 stopped，MUST NOT 恢复旧 Source 或队列。不得 mid-queue 跨 Source
  fallback；skill 只能在开始前选择 Source。

## 3. 队列

| command | params | data | 预算 |
|---|---|---|---:|
| `queue.list` | — | `QueueState` | 5s |
| `queue.add` | `{ref, position: "next" \| "append", ifQueueRevision?}` | `PlaybackState` | 30s |
| `queue.jump` | `{index, ifQueueRevision?}` | `PlaybackState` | 5s |
| `queue.remove` | `{index, ifQueueRevision?}` | `PlaybackState` | 5s |
| `queue.move` | `{from, to, ifQueueRevision?}` | `PlaybackState` | 5s |
| `queue.clear` | `{ifQueueRevision?}` | `PlaybackState` | 5s |

CLI 初始只公开：

```text
lilt queue [list] --json
lilt queue add <ref> --next|--append --json
lilt queue remove <index> --json
lilt queue move <from> <to> --json
lilt queue jump <index> --json
lilt queue clear --json
```

语义与乐观并发：

- `ifQueueRevision` 是可选前置条件。若提供且与当前 `queueRevision` 不符，server
  MUST NOT 执行，返回 `conflict`；`error.details` 含最新 `queueRevision` 与
  `QueueState`，client 重新拉取后再决定。
- 多 client 并发编辑同一队列时（典型：TUI 与 skill 同时操作），TUI MUST 带
  `ifQueueRevision`，因为它的 index 来自屏幕快照；skill 顺序操作 MAY 省略。
- `index` 是相对**当前队列构成**的绝对位置；不得使用 client 缓存的旧索引。
- 队列只服务于 Apple Music/Audius 等 finite-queue Source；Radio/preview 没有队列，
  返回 `queue_unavailable`。Apple Music 与 Audius 都支持 `queue.add/remove/move/clear`
  （Audius 版本由 server 侧 URL 队列实现）。`queue.add` 的 ref Source 与非空 `QueueState.source` 不同 MUST
  返回稳定 `source_mismatch`，不得混入或隐式切换 Source。

## 4. 内容发现

| command | params | data | 预算 |
|---|---|---|---:|
| `discovery.search` | `{source, term, type: "song"\|"album"\|"playlist"\|"station"\|"all", limit?}` | `SearchResult` | 45s |
| `discovery.trending` | `{source, type: "song"\|"playlist", limit?}` | `SearchResult` | 45s |
| `album.tracks` | `{ref}` | `{album: Item, items: [Item]}` | 45s |
| `playlist.tracks` | `{ref}` | `{playlist: Item, items: [Item]}` | 45s |
| `library.playlists` | `{source}` | `[Item]` | 45s |
| `library.albums` | `{source}` | `[Item]` | 45s |
| `recent.list` | `{limit?}` | `[Item]` | 5s |
| `recommendations.list` | `{source, limit?}` | `[Item]` | 45s |
| `radio.search` | `{name?, tag?, language?, countryCode?, limit?, offset?, origin?}` | `RadioSearchResult` | 15s |
| `radio.options` | `{facet: "tag"\|"language"\|"country", origin?}` | `{options: [{value,count}], degradedOrigins?}` | 15s |
| `radio.probe` | `{url}` | `RadioProbeResult` | 15s |

`SearchResult` 形状固定，不随 type 改变：

```jsonc
{
  "source": "apple-music",
  "term": "Nicky Lee",
  "groups": {
    "songs":     [ /* Item */ ],
    "albums":    [ /* Item */ ],
    "playlists": [ /* Item */ ],
    "stations":  [ /* Item */ ]
  }
}
```

未请求或为空的 group 可省略。`discovery.search` 的行为按 provider 划分：

- `source` 在 wire 上**必填**；缺失返回 `invalid_request`。CLI 的 `--source` 可省略：client 按
  [来源选择规则](README.md#来源选择规则) 在发送前解析出一个具体 source；wire 请求中的 `source`
  MUST 明确。
- 该命令只服务内容发现 provider（如 `apple-music`、`audius`）。`radio` 不是它的 provider：
  `source:"radio"` 返回 `unsupported_command`，radio 发现一律用 `radio.search`。
- `type` 语义由该 source 声明的 capability 决定：
  - `type:"all"`：只返回该 source 声明支持的 search 分组，不支持的分组被跳过、不报错
    （例如 `apple-music` 可含 `albums`/`stations`，`audius` 只有 songs/playlists）。
  - `type` 指定具体 kind 但该 source 未声明对应 capability：返回 `unsupported_command`，
    MUST NOT 静默降级。
- client（含 TUI）应先读 `sources.list` 的 capability 决定请求什么；`all` 只是便利，不是契约。
- `discovery.trending` **requires `search.trending`**: it only returns the requested source's trending songs or
  playlists (`SearchResult` with one group). A source without that capability returns `unsupported_command`;
  clients MUST route it from `SourceDescriptor.capabilities`, not from a parallel support list.

`library.playlists` 只对声明 `library` capability 的 Source 可用。Apple Music 返回用户
资料库歌单；Audius 仅在官方账户 API capability 已确认且授权后返回用户歌单。其他账户
集合在有独立 command/model 前不得由 TUI 臆造。

`library.albums` 同样只对声明 `library` capability 的 Source 可用；目前只有 Apple
资料库暴露 album，其他 source 返回 `unsupported_command`。album 是公共 kind（`album`），
可播放 ref 形如 `apple-music:album:<id>`。

`album.tracks` 对声明可播放 album 的 Source 可用（当前只有 Apple Music）：`{ref}` 是
`apple-music:album:<id>`，返回 `{album: Item, items: [Item]}`——与 `playlist.tracks` 同构。
其他 source 返回 `unsupported_command`。

`playback.play` 接受 album ref：server 先展开专辑曲目，再用与 `playback.playSongs` 相同的
起播 + 逐条追加路径构建有限队列（整张专辑交给 MusicKit 会卡成“队列已建满但未播放”）。
`startTrackID` 优先于 `startAt` 选择起点；`fromHere:true` 丢弃起点之前的曲目（TUI 专辑详情
的 Enter）；不传 `fromHere` 时队列仍包含起点之前的曲目（TUI 的 `p` 传 `startAt:0`）。

CLI：

```text
lilt search <term> [--source SOURCE] [--type song|album|playlist|station|all] [--limit N] --json
lilt trending [--source SOURCE] [--type song|playlist] [--limit N] --json
lilt album <ref> --json
lilt playlist <ref> --json
lilt albums [--source SOURCE] --json
lilt library [--source SOURCE] --json
lilt recent [N] --json
lilt radio search [--name TEXT] [--tag TAG] [--language LANG] [--country CC] [--limit N] [--origin builtin|directory|all] --json
```

`radio.search` 的 `origin` 缺省为 `all`：

- `builtin`：lilt 内置精选台（源自 cliamp / cliamp.stream 的 vendored snapshot，非 lilt
  创建或拥有；见
  [`extending.md`](extending.md#2-内置电台)）。不依赖任何网络目录；只有流本身
  需要网络。
- `directory`：Radio Browser 目录。目录不可达时返回 `search_failed`，不影响
  builtin。
- `all`：两段合并，builtin 命中排在前面，各自带 `radio.origin`。某一 origin 失败而
  至少一个 origin 成功完成时仍返回成功（即使成功方零命中），并在
  `degradedOrigins` 返回失败方的 `{origin,code,message}`；所有请求 origin 都失败才
  返回 `search_failed`。`radio.options` 对多 origin 使用同一语义与字段。

每个结果 Item 的 `radio.origin` MUST 标注来源；`radio.options` 的 facet 计数只对
请求的 origin 集合统计（`all` 时为合并集合）。

内置（builtin）快照只有名称与 URL，没有 tag/language/country 元数据：当请求带结构化过滤
（`tag`/`language`/`countryCode`）时，builtin MUST 被排除，不得把未匹配的精选台当成命中结果；
`name` 文本过滤仍可用于 builtin。

## 5. 状态与偏好

| command | params | data | 预算 |
|---|---|---|---:|
| `state.get` | — | `AppState` | 5s |
| `favorites.list` | `{source?}` | `[Item]` | 5s |
| `favorites.set` | `{item: Item, favorited: bool}` | `{favorited: bool, item: Item}` | 5s |
| `ui.set` | `{theme?, lastSource?}` | `AppState` | 5s |

规则：

- server MUST 先写临时文件、成功原子替换后，才发布新 AppState 与 watch event。
- 保存失败 MUST 返回错误，且不得修改权威内存状态。
- `recent`、`recentContainers` 由 server 更新；client 不得直接写。`recent.list` 专指
  lilt 本地跨 source 播放历史；将来 provider/library recently-played 必须另命名。
- **recent 阈值**：每次播放 occurrence 仅在累计 monotonic `status=playing` 时间达到
  `min(30s, 已知有限 duration 的 50%)` 时记录一次；未知/live 为 30s。paused、
  buffering、stopped 与 seek/position jump 不计时。后续合格重播刷新 `playedAt`，仍按
  identity 去重。`recentContainers` 不受阈值影响，容器成功启动即记录。

CLI 初始公开 `lilt favorites --json`。收藏修改的 CLI 是否
公开可后置，但 TUI MUST 使用幂等的 `favorites.set`。

## 6. 会话、授权与生命周期

| command | params | data | 预算 |
|---|---|---|---:|
| `session.status` | `{includeQueue?: bool}` | `PlaybackStatus`（默认）/ `PlaybackState` | 5s |
| `session.watch` | `{includeState?: bool, topics?: [string]}` | 初始 `WatchSnapshot`，随后 event | 长连接 |
| `authorization.list` | — | `[SourceAuthorization]` | 5s |
| `authorization.status` | `{source}` | `SourceAuthorization` | 5s |
| `authorization.begin` | `{source, interactive: true}` | `AuthorizationFlow` | 10s |
| `authorization.flowStatus` | `{flowId}` | `AuthorizationFlow` | 5s |
| `authorization.cancel` | `{flowId}` | 终态 `AuthorizationFlow` | 5s |
| `authorization.disconnect` | `{source}` | `SourceAuthorization` | 10s |
| `session.shutdown` | — | `{}` | 5s |

CLI：

```text
lilt status [--queue] --json
lilt auth status [SOURCE] --json
lilt auth <SOURCE> --json
lilt auth cancel <FLOW_ID> --json
lilt auth disconnect <SOURCE> --json
lilt serve [--detach] --json
lilt quit --json
```

规则：

- `quit` 映射 `session.shutdown`，结束 server 与 helper；线性化顺序见
  [`protocol.md`](protocol.md#6-关闭服务线性化)。
- `authorization.begin` 的 source MUST 明确，且立即返回 `AuthorizationFlow`（通常为
  pending）；它 MUST NOT 在 RPC 中等待用户交互。每个 source 同时最多一个 active flow；重复 begin
  返回 `authorization_in_progress`，其 `details.flowId` 是既有 flow。
- 已授权 source 的 begin 返回一个 `interaction.type="none"` 的终态 authorized flow，
  不重复交互；不需要或不实现授权的 source 返回 `unsupported_command`。未知 source
  返回 `invalid_request`。如果 server 在建立 flow 记录或启动 provider 交互前失败，RPC
  返回 `authorization_failed`；一旦 flow 已返回，后续失败写入 flow 的 terminal error
  状态并发布 `authorization.changed`，不再变成另一条 RPC error。
- flow 由 server 持有，发起 client 断线后继续；server shutdown MUST 取消所有 pending
  flow。终态 flow 至少保留 10 分钟供查询；之后查询返回
  `authorization_flow_not_found`。cancel 对 pending flow 产生 cancelled 终态，对已有终态
  幂等返回原结果。provider 决定并展示系统对话框、浏览器或 device-code 交互。
- `lilt auth <SOURCE> --json` 是 CLI 便利层：先 begin，再轮询 flowStatus 到终态。JSON
  模式同样等待并只在 stdout 输出最终 `AuthorizationFlow`；pending interaction 的 URL、
  device code 或“请完成系统对话框”提示写到 stderr。非 JSON 模式以适合 provider 的方式
  呈现同一 interaction。CLI 等待到 flow 终态或 `interaction.expiresAt`；用户中断 CLI
  不取消 server-owned flow，显式 cancel 才取消。
  `lilt auth status` 无 source 时对应 list，指定 source 时对应 status。
- `authorization.disconnect` 是 desired-state 幂等操作：取消该 source 的 pending flow；
  当前由该 source 播放时先进入 stopped 并清空其队列；随后删除 lilt 持有的 secure-storage
  凭据并发布 authorization/sources 变化。停止播放或删除本地凭据失败时中止并返回
  `playback_error` 或 `authorization_failed`，不得报告已断开。已经断开时成功 no-op。
  远端 revoke 是尽力而为，失败通过 `server.warning` 报告但不恢复本地凭据。系统不允许
  程序化撤权的 provider 返回 `unsupported_command`，并在 message 中给出系统设置指引。
- 只有用户明确要求授权时，skill 才能建议或执行上述交互式 CLI 命令；skill MUST NOT
  自行 begin。
- TUI 启动时如果 server 不存在 MUST 自动启动，已存在则直接 attach。
- `serve --detach` 必须在取得生命周期锁、绑定 socket 且 Client API 已接受请求后才成功，
  返回 `{pid}`；source/engine 尚在初始化时由 availability 表示，而非伪造启动成功。
- `session.watch` 的参数与事件语义见 [`watch.md`](watch.md)。
