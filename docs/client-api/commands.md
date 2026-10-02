# Client API —— 命令

每个命令的参数、返回与语义。数据形状见 [`models.md`](models.md)；事件订阅见
[`watch.md`](watch.md)；错误码见 [`errors.md`](errors.md)。

表格中的“预算”是命令开始执行后的执行预算（见 [`protocol.md`](protocol.md) §4）；排队等待另有
统一的 admission 预算。除 `query:true` 的纯查询外，所有命令 MUST 携带 `ifServerInstanceId`
（见 [`protocol.md`](protocol.md#14-server-instance-epoch)）；`api.describe` 为每个命令标明
`query`、`concurrent`、`timeoutMs` 与 `admissionMs`。`concurrent:true` 的命令不进入
admission 队列（见 [`protocol.md`](protocol.md) §2），因此没有 `admissionMs`。

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
| `playback.pause` | — | `PlaybackState` | 8s |
| `playback.toggle` | — | `PlaybackState` | 15s |
| `playback.resume` | — | `PlaybackState` | 15s |
| `playback.next` | — | `PlaybackState` | 15s |
| `playback.previous` | — | `PlaybackState` | 15s |
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

- `stop` 只停止播放，server 继续运行；对当前会话幂等，成功时返回 stopped/no track/空队列。
  已无活动 URL 会话时成功 no-op，stream 后端已释放时也直接提交 stopped；实际停止调用失败
  返回对应错误，MusicKit 后端不可用时也可能失败。幂等不承诺所有请求都成功。
  有限 URL 队列若已因终止性播放失败清空，后续 stop 的 no-op 仅确认 server 没有活动会话，
  不能证明故障后端的旧音频已停止；此前的尽力清理见
  [`失败清理预算`](protocol.md#4-超时预算)。
- `pause`/`resume` 是 desired-state：已 paused 的 pause、已 playing 的 resume 都成功
  no-op；buffering 表示已处于“希望播放”的状态，因此 resume 也成功 no-op。pause
  保留当前项和位置。preview 与 stream/live 使用相同状态规则。有限 URL 会话内，成功 pause 的意图
  在自动媒体重试和切换当前项后仍保持；显式 resume 或新播放会话替换该意图。后端启动与恢复暂停
  不是原子调用，执行方式与失败语义见
  [`有限 URL 队列`](../internals/providers/providers.md#41-有限-url-队列)。
- `toggle` 在 playing/buffering 时进入 paused，在 paused 时恢复；stopped、error 或没有
  当前项时返回 `invalid_state`，不猜测要恢复什么。原生 MusicKit 起播/切歌会等待当前曲目的
  实际进度开始推进；暂停会等待 MusicKit 确认。等待超时返回 `playback_error` 并停止 helper 播放，
  不把尚未落地的动作报为成功。
- source 切换启动失败时，旧 source 保持 stopped，不隐式恢复；返回 `playback_error`，
  `details.state` 是最终 `PlaybackState`。

状态控制矩阵（描述成功时的转换；实际调用失败仍返回错误）：

| 命令 | playing | paused | buffering | stopped / error / 无当前项 |
|---|---|---|---|---|
| pause | 进入 paused | 成功 no-op | 进入 paused | `invalid_state` |
| resume | 成功 no-op | 恢复播放 | 成功 no-op | `invalid_state` |
| toggle | 进入 paused | 恢复播放 | 进入 paused | `invalid_state` |
| stop | 进入 stopped 并清空 | 进入 stopped 并清空 | 进入 stopped 并清空 | 成功 no-op |

队列控制与上表状态正交：

| 命令 | 有限队列 | 无队列 / preview / stream/live |
|---|---|---|
| next / previous | 切换当前项；原状态为 paused 时保持 paused，否则启动新项 | `unsupported_command`（该来源不声明有限队列，如 radio）或 `invalid_state`（无当前可操作项） |
| setShuffle / setRepeat | 应用目标值；相同值成功 no-op | `unsupported_command`（该来源不声明 shuffle/repeat，如 radio） |

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
  - `playback.play` / `playback.playSongs` 的 `shuffle`/`repeat` 在**建队列之前**生效；省略即表示
    `off`。想保留用户当前形态的客户端 MUST 把当前值一起发送，否则会被重置。
  - 有限队列填充**进行中**会在 `playback.changed` 上带 `queueFill:{queued,total}`；被 engine 拒绝的条目
    不会静默丢弃——最终返回 `partial_failure`，`details` 带 `added`/`skipped`/`total`。
  - 有限队列（album / `playback.playSongs`）填充完成后若 MusicKit 拒绝起播，返回
    `partial_failure` 且 `details.queueReady:true`：整条队列已建好并已提交到
    `details.state`，客户端应提示重试播放而不是重建队列。
  - 如果来源**声明**了能力但实际设置失败（例如 engine 报错），返回 `partial_failure`，
    `error.details` MUST 含 `state` 与 `applied`；不得谎称播放失败，也不回滚已开始的音频。
- `playback.play` 可选 `startAt` / `startTrackID` / `reverse` / `fromHere`：歌单或专辑从指定曲目
  开始（`startTrackID` 优先于 `startAt`），`reverse` 同时反转队列顺序与起点选择（Apple 本地化
  曲目列表会反向）。`startTrackID` 传的是 item 的 **provider id**（`Item.providerId`）——客户端把
  `api.Item.ID`（稳定拼写，如 `am:1234`）转成自己的 item id 时取的就是它，服务端也按它匹配。
  「喜爱歌曲」用）。`fromHere:true` 表示**向前播放**：丢弃起点之前的曲目，队列从所选曲开始
  （TUI 歌单/专辑详情的 Enter 用它；`p` 播放整个容器）。CLI 暂未暴露这些字段。
- `playback.playSongs.refs` MUST 非空并使用 discovery 返回的 canonical `Item.ref`；server
  从 refs 推导唯一 Source。所有 refs MUST 属于同一 finite-queue Source，否则返回
  `source_mismatch`。Apple Music、Audius 与 Jamendo 是当前指定的 finite-queue Source；wire 与 CLI
  都只接受 canonical refs。
  - Audius 的显式 song refs MUST 按提交顺序完整准备（重复 ref 保留），`startIndex` 按该顺序选曲。
    上游批量响应乱序不改变队列；任何请求曲目缺失或不可播放时返回 `invalid_reference`，不静默
    缩短队列，也不解析媒体 URL 或起播其他曲目。歌单展开后同样遵循此规则；`fromHere:true`
    主动丢弃的前序曲目不属于待准备队列。discovery 仍只展示可播放曲目。
  - Apple 端的**主路径是一次性赋值整个队列**（可跳转、无填充）；
    仅当 helper **明确报告整批 prepare 拒绝**且请求未取消时，才回退为“起播首选曲目 + 逐条
    enqueue”；私有分类见 [helper 协议](../internals/playback/helper-rpc.md)。传输超时/断连
    MUST NOT 触发起播回退，返回 `operation_outcome_unknown`；授权、资源解析与起播确认失败
    保留各自错误映射，不再尝试其他起播路径。只有获准的回退路径才有 `queueFill` 进度与
    append 队列的跳转限制，个别无法入队的曲目被跳过（helper stderr 记录）。
- 播放严格互斥：开始另一 Source 前 MUST 停止当前 Source 并清空/替换旧有限队列；新 start
  失败时最终状态保持 stopped，MUST NOT 恢复旧 Source 或队列。不得 mid-queue 跨 Source
  fallback；skill 只能在开始前选择 Source。

## 3. 队列

| command | params | data | 预算 |
|---|---|---|---:|
| `queue.list` | — | `QueueState` | 5s |
| `queue.add` | `{ref, position: "next" \| "append", ifQueueRevision?}` | `PlaybackState` | 30s |
| `queue.jump` | `{index, ifQueueRevision?}` | `PlaybackState`（失败可返回 `queue_not_jumpable`、`partial_failure`、`operation_outcome_unknown`、`playback_error`） | 20s |
| `queue.remove` | `{index, ifQueueRevision?}` | `QueueRemoveResult {state, undo?}` | 5s |
| `queue.undoRemove` | `{token, ifQueueRevision}` | `PlaybackState` | 5s |
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

上述 CLI 命令当前不提供 `ifQueueRevision` 参数；需原子并发保护时使用 Client API。
`queue.undoRemove` 是 TUI 使用的短时恢复原语，CLI 暂不提供入口。

没有队列属主时（无播放会话、stream/live、URL 队列已清空或耗尽），`queue.list`、`queue.jump`、
`queue.remove`、`queue.move`、`queue.clear` 统一返回 `queue_unavailable`。preview 播放中同样没有有限队列，但 engine
transport 活跃：`queue.jump`、`queue.remove`、`queue.move` 返回 `queue_unavailable`（helper 内部的
`preview_unsupported` 不会透出到公开错误码），`queue.list` 返回当前队列，`queue.clear` 按 engine 幂等语义返回成功并停止 preview。

语义与乐观并发：

- `ifQueueRevision` 是可选前置条件。若提供且与当前 `queueRevision` 不符，server
  MUST NOT 执行，返回 `conflict`；`error.details` 含最新 `queueRevision` 与
  `QueueState`，client 重新拉取后再决定。
- 多 client 并发编辑同一队列时（典型：TUI 与 skill 同时操作），TUI MUST 带
  `ifQueueRevision`，因为它的 index 来自屏幕快照；skill 顺序操作 MAY 省略。
- `index` 是相对**当前队列构成**的绝对位置；不得使用 client 缓存的旧索引。
- `queue.remove` 只在删除**当前项之后的 future song**且 transport 能保留精确对象时返回
  `undo:{token,expiresAt}`。offer 从删除提交起保留 5 秒；每次后续成功删除（包括不可撤销的删除）
  都取代前一条。当前项、历史项以及无法保留原始 MusicKit `Song` 的条目仍可删除，但不返回 offer。
- `queue.undoRemove` 的 `token` 与 `ifQueueRevision` 都必填。server 只恢复最近一次 offer，并要求
  revision、playback session 与删除时的当前曲目/索引均未变化；自然切歌虽不增加 revision，也会使
  Undo 失效。成功恢复的是删除时保留的同一对象和原 canonical 位置，不重新 resolve，不以
  `queue.add` + `queue.move` 拼接。过期/被取代/播放推进返回 `undo_unavailable`；并发构成变化返回
  `conflict`；RPC 后结果无法确认返回 `operation_outcome_unknown`，client 不得自动重试。
- `queue.move` MUST NOT 中断正在播放的曲目：移动一行（跨过正在播放的行，或移动当前行本身）后，当前
  曲目与其播放位置保持不变；未开 shuffle 时重排对后续播放生效（列表顺序即播放顺序；shuffle 下的推进
  顺序见 [`models.md`](models.md)）。无法在不打断当下的前提下保住当前曲目时（例如位移过大、helper
  无法走回），MUST 返回 `partial_failure` 并附真实 `details.state`，MUST NOT 静默成功。Apple Music
  helper 侧的约束与实现见 [`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)。
- Apple Music 的 append 队列无法原地跳转时，server 只尝试一次性赋值**同序队列**并从目标行起播；
  不自动改用逐首追加。失败后核对播放状态：队列与当前项未变，返回 `queue_not_jumpable`（附
  `details.state`）；已变，提交真实状态并返回 `partial_failure`（附 `details.state`）；无法确认时
  返回 `operation_outcome_unknown`，并递增 `queueRevision` 作废旧行号、广播 `server.warning`；
  client 应重新读取状态，不能自动重试。一次性赋值返回成功也要核对队列顺序与落点。
  该操作会等待一次性赋值结束，TUI 在此期间显示工作状态。
- 队列只服务于 Apple Music/Audius/Jamendo 等 finite-queue Source；Radio/preview 没有队列，
  返回 `queue_unavailable`。Apple Music、Audius 与 Jamendo 都支持 `queue.add/remove/move/clear`
  （Audius 与 Jamendo 版本由 server 侧 URL 队列实现）。`queue.add` 的 ref Source 与非空 `QueueState.source` 不同 MUST
  返回稳定 `source_mismatch`，不得混入或隐式切换 Source。
- 整个队列命令族（`queue.add/list/jump/remove/move/clear`）共用同一条路由判定，只接受**当前持有播放的
  队列**：ref 或命令落到 engine 路径（Apple Music）时要求 engine transport 活跃且 helper 已在运行，
  落到 URL 队列路径（Audius/Jamendo）时要求 URL 队列会话仍在。URL 队列已清空或耗尽、stream 播放中
  或尚无任何播放会话时，一律 `queue_unavailable`，server MUST NOT 为一次队列操作悄悄拉起被搁置的
  engine（engine transport 活跃但 helper 正在重启时返回 `engine_restarting`）。`queue.list` 走同一判定：
  无属主时同样 `queue_unavailable`，有属主时返回当前队列。
- `queue.clear` 因此只清空**仍持有会话的队列**：URL 队列已清空或耗尽后再 clear 返回
  `queue_unavailable`，不对已终结的会话报成功；engine 队列在 engine transport 活跃时保持幂等，
  清空已空的 engine 队列仍成功。preview 播放正属于这种 engine transport 活跃而无有限队列的状态，
  clear 成功并停止 preview。

## 4. 内容发现

| command | params | data | 预算 |
|---|---|---|---:|
| `discovery.search` | `{source, term, type: "song"\|"album"\|"playlist"\|"station"\|"all", limit?}` | `SearchResult` | 45s |
| `discovery.trending` | `{source, type: "song"\|"playlist"\|"all", limit?}`；`type` 缺省为 `all` | `SearchResult` | 45s |
| `album.tracks` | `{ref}` | `{album: Item, items: [Item]}` | 45s |
| `playlist.tracks` | `{ref}` | `{playlist: Item, items: [Item]}` | 45s |
| `library.playlists` | `{source}` | `[Item]` | 45s |
| `library.albums` | `{source}` | `[Item]` | 45s |
| `recent.list` | `{limit?}` | `[Item]` | 5s |
| `recommendations.list` | `{source, limit?}` | `[Item]` | 45s |
| `radio.search` | `{name?, tag?, language?, countryCode?, limit?, offset?, origin?}` | `RadioSearchResult` | 15s |
| `radio.options` | `{facet: "tag"\|"language"\|"country", origin?}` | `{options: [{value,count}], degradedOrigins?}` | 15s |
| `radio.probe` | `{url}` | `RadioProbeResult` | 15s |
| `radio.cache` | — | `RadioCache` | 5s |

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

- `source` 在 wire 上**必填**；缺失返回 `invalid_request`。CLI 的 `--source` 是**确定性默认值**：
  `lilt search` 默认 `apple-music`，`lilt trending` 默认 `audius`；需要别的来源必须显式传
  `--source`（这是已定决策，见 [`../product/roadmap.md`](../product/roadmap.md) §2）。CLI 不做运行时
  选源；按 capability 选来源是 skill/agent 的编排职责（[来源选择规则](README.md#来源选择规则)）。
- 该命令只服务内容发现 provider（`apple-music`、`audius`、`jamendo`）。`radio` 不是它的 provider：
  `source:"radio"` 返回 `unsupported_command`，radio 发现一律用 `radio.search`。
- `type` 语义由该 source 声明的 capability 决定：
  - `type:"all"`：只返回该 source 声明支持的 search 分组，不支持的分组被跳过、不报错
    （例如 `apple-music` 可含 `albums`/`stations`，`audius` 只有 songs/playlists；Jamendo J1 同样只声明
    songs/playlists）。
  - `type` 指定具体 kind 但该 source 未声明对应 capability：返回 `unsupported_command`，
    MUST NOT 静默降级。
- client（含 TUI）应先读 `sources.list` 的 capability 决定请求什么；`all` 只是便利，不是契约。
- `discovery.trending` **requires `search.trending`**（或 song-only 的 `search.trending.songs`，仅当请求含
  song kind）：返回请求来源的 trending songs 或 playlists（单个或多个 group 的 `SearchResult`）。
  `type` 缺省为 `all`，与 `discovery.search` 同语义：**只返回声明了的 kind 分组**——generic 能力返回
  songs+playlists，song-only 能力只返回 songs。显式请求未声明的 kind（如对 Jamendo 传 `playlist`）返回
  `unsupported_command`，MUST NOT 静默降级；client MUST 从 `SourceDescriptor.capabilities` 路由，不得维护
  并行支持列表。Apple Music（browser 引擎）的 charts 只排序歌曲，声明的是 song-only 形式。

`recommendations.list` 只对声明 `recommendations` capability 的 Source 可用（当前只有 `apple-music`；
macOS 走 MusicKit helper、其他平台走 browser 引擎，server 按 capability 路由到对应实现）。两引擎
返回的 kind 不同：MusicKit helper 返回 **playlists + stations**；browser 引擎返回
**playlists + albums**（browser 没有 station 播放路径，分组里的 stations 被丢弃）。`limit` 缺省为 20。未登录时返回 `authorization_required`——登录态影响**内容**
而非 capability，capability 恒为 available（与 library 的授权后可用不同）。

`library.playlists` 只对声明 `library` capability 的 Source 可用。Apple Music 返回用户
资料库歌单；Audius 仅在官方账户 API capability 已确认且授权后返回用户歌单。其他账户
集合在有独立 command/model 前不得由 TUI 臆造。

`library.albums` 同样只对声明 `library` capability 的 Source 可用；是否暴露由 source 实现
的 library 扩展决定——目前只有 Apple 的 MusicKit helper 资料库暴露 album（browser 引擎无
资料库，返回 `source_unavailable`），无 album 概念的 source（如 Audius）返回
`unsupported_command`。album 是公共 kind（`album`），可播放 ref 形如
`apple-music:album:<id>`。

`album.tracks` 对声明可播放 album 的 Source 可用（当前只有 Apple Music）：`{ref}` 是
`apple-music:album:<id>`，返回 `{album: Item, items: [Item]}`——与 `playlist.tracks` 同构。
其他 source 返回 `unsupported_command`。

`playback.play` 接受 album ref：server 先展开专辑曲目，再走与 `playback.playSongs` 相同的
一次性赋值主路径（append 仅为 MusicKit 拒绝整批时的回退）。
`startTrackID` 优先于 `startAt` 选择起点；`fromHere:true` 丢弃起点之前的曲目（TUI 专辑详情
的 Enter）；不传 `fromHere` 时队列仍包含起点之前的曲目（TUI 的 `p` 传 `startAt:0`）。

CLI：

```text
lilt search <term> [--source SOURCE] [--type song|album|playlist|station|all] [--limit N] --json
lilt trending [--source SOURCE] [--type song|playlist|all] [--limit N] --json
lilt album <ref> --json
lilt playlist <ref> --json
lilt albums [--source SOURCE] --json
lilt library [--source SOURCE] --json
lilt recent [N] --json
lilt radio search [--name TEXT] [--tag TAG] [--language LANG] [--country CC] [--limit N] [--offset N] [--origin builtin|directory|all] --json
lilt radio options --facet tag|language|country [--origin builtin|directory|all] --json
lilt radio probe --url URL --json
lilt radio cache --json
```

`favorites.list` 的 CLI 支持可选 `--source S`。

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

## 5. 状态、收藏与历史

| command | params | data | 预算 |
|---|---|---|---:|
| `state.get` | — | `AppState` | 5s |
| `favorites.list` | `{source?}` | `[Item]` | 5s |
| `favorites.set` | `{item: Item, favorited: bool}` | `{favorited: bool, item: Item}` | 5s |
| `favorites.add` | `{ref}` | `{favorited: bool, item: Item}` | 45s |
| `favorites.remove` | `{ref}` | `{favorited: bool, item: Item}` | 5s |
| `history.list` | `{source?, before?, limit?}` | `HistoryPageResult` | 5s |
| `history.stats` | `{refs: [string]}` | `[HistoryStats]` | 5s |
| `history.clear` | `{confirm: true}` | `{cleared: int}` | 5s |
| `activity.reset` | `{confirm: true}` | `{archived: bool, archivePath?}` | 10s |
| `ui.set` | `{theme?, lastSource?}` | `AppState` | 5s |

规则：

- 收藏与历史保存在 Activity store；写入成功后 server 发布新 AppState 与 watch event。偏好
  （theme/lastSource）MUST 先写临时文件、成功原子替换后才发布。
- `favorites.set/add/remove` 都是幂等的：重复 add 不改变原 `addedAt`，remove 不存在的收藏成功。
  `favorites.add` 先从 Activity store 取 Item，没有时通过 provider 解析；无法得到带 title 的完整
  Item MUST 返回错误，不得只收藏裸 ref。Radio 的 URL 可以直接构造 Item。
- `recent` 由 server 从 Playback History 派生（每个不同 Item 的最后一次达标播放）；client 不得
  直接写。`recent.list` 专指 lilt 本地跨 source 派生 Recent；将来 provider/library
  recently-played 必须另命名。
- **recent/写入阈值**：每次播放 occurrence 仅在累计 monotonic `status=playing` 时间达到
  `min(30s, 已知有限 duration 的 50%)` 时写入一条不可变历史记录；未知/live 为 30s。paused、
  buffering、stopped 与 seek/position jump 不计时。每次达标播放都是新历史记录，可重复 Item。
- `history.list` 按 `(playedAt, id)` keyset cursor 分页；`before` 是上页 `nextCursor`，不透明。
  `limit` 默认 50、单页上限 200。`history.stats` 一次最多 500 个 refs，按输入顺序返回，未知 ref
  的 `playCount` 为 0。“听过”语义见 [`../internals/persistence/local-activity.md`](../internals/persistence/local-activity.md)。
- `history.clear` 清空历史与派生 stats，保留 Favorites；`activity.reset` 归档整个 Activity 数据库
  （含 WAL/SHM）后重建空库，只用于损坏恢复。两者都 MUST 要求 `confirm:true`。清除/reset 与自动写入
  串行化；当前播放 occurrence 不会在清除后重新写入，下一曲或单曲重播形成新 occurrence 后照常记录。
- Activity store 不可用时播放继续；Activity 读写返回 `storage_unavailable`，watch 快照携带
  `warning`。自动历史写入失败时向 watch 发布 `storage_unavailable` 警告并写入 journal；后续采样用
  同一个 occurrence ID 重试，成功后最多记一条。不得自动重建空库；只有显式 `activity.reset`
  可以归档后恢复。

CLI 公开：`lilt favorites --json`、`lilt favorite add|remove <ref>`、
`lilt history [--source S] [--before C] [--limit N] --json`、`lilt history stats <ref,..>`、
`lilt history clear --confirm`、`lilt data reset --confirm`。TUI MUST 使用幂等的 `favorites.set`。

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
lilt jamendo setup [--client-id ID] --json  # local CLI setup; not a Client API command
lilt serve [--detach] --json
lilt quit --json
```

规则：

- `quit` 映射 `session.shutdown`，结束 server 与 helper；wire params 可省略或为 `{}`。
  请求必须通过校验与去重且处理成功才启动关闭；错误响应不得触发关闭副作用。
  拒绝规则、重试与线性化顺序见 [`protocol.md`](protocol.md#6-关闭服务线性化)。
- `authorization.begin` 的 source MUST 明确，且立即返回 `AuthorizationFlow`（通常为
  pending）；它 MUST NOT 在 RPC 中等待用户交互。每个 source 同时最多一个 active flow；重复 begin
  返回 `authorization_in_progress`，其 `details.flowId` 是既有 flow。
  若该 source 的 sign-in 会摧毁其播放运行时（如 Apple 的浏览器会话），server MUST 在 flow 启动前
  通过既有 stop 路径停止该 source 的播放并发布显式 stopped 迁移；这是正常语义，不发
  `server.warning`，登录完成后也不自动重放。
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
  `authorization.list` 每项的 `canDisconnect` 是唯一真值：为 false 的 source（例如没有本地
  凭据可删的 radio、以及不支持程序化撤权的 helper 模式 apple-music）MUST 返回
  `unsupported_command`，client MUST NOT 提供该操作。
- 只有用户明确要求授权时，skill 才能建议或执行上述交互式 CLI 命令；skill MUST NOT
  自行 begin。
- TUI 启动时如果 server 不存在 MUST 自动启动，已存在则直接 attach。
- `serve --detach` 必须在取得生命周期锁、绑定 socket 且 Client API 已接受请求后才成功，
  返回 `{pid}`；source/engine 尚在初始化时由 availability 表示，而非伪造启动成功。
- `session.watch` 的参数与事件语义见 [`watch.md`](watch.md)。
