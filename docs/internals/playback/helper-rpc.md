# Spec: RPC（helper 协议）

本文件只定义 Go server 与私有 Swift helper 之间的内部协议。TUI、CLI、AI skill
和未来 client 使用的统一接口见 [`../client-api/README.md`](../../client-api/README.md)，不得直接依赖
helper method。

## 播放期间的进程活动断言

helper 在播放（`playing` / `waitingToPlayAtSpecifiedRate`）期间持有
`ProcessInfo.beginActivity([.userInitiated, .latencyCritical])`，停止时释放。原因：helper 是 accessory
app，macOS 会节流/挂起它（实测暂停前 1 秒采样器静默约 5 秒），随后 MusicKit 自己报 `paused`，用户看到
"只响一下就停"。两个 helper 并存时最容易触发（4/10 次），持有断言后 12 次全无；单进程时本来也不复现。

结论：activity assertion **不能替代** server 的 playback ownership：server 仍保证任一时刻只有一个实际
播放 backend。它只保证 idle 的 Apple resource client 与活动 `lilt-audio` 共存时不会因 App Nap 放大问题；
`LILT_PLAYER_ACTIVITY_ASSERT=0` 只用于对照实验。

## 传输

- 换行分隔、JSON-RPC 形状的消息，走**私有 Unix socket**。`error.code` 是稳定字符串
  （如 `preview_unsupported`），不是 JSON-RPC 2.0 要求的整数；以本文件为准。
- 请求默认顺序处理；`radioProbe` 是例外：它由独立任务执行并按完成顺序返回，
  不阻塞播放与状态（见 [`radio-discovery.md`](../providers/radio-discovery.md)）。
- 桌面：Go 用 LaunchServices 按需启动签名的 `lilt-player.app`（MusicKit）或 `lilt-audio.app`
  （AVPlayer），参数均为 `--rpc-socket <path>`。app 以 `0600` 绑定 socket，只接受一个 host；
  所有 response 和 notification 由同一个串行 writer 写入。
- 手机/其他端不使用该 socket，直接在进程内实现同一**行为契约**（本文件是行为参考，不要求复用传输）。

## 请求/响应

```json
{"jsonrpc":"2.0","id":1,"method":"play","params":{...}}
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":1,"error":{"code":"preview_unsupported","message":"..."}}
```

同一 socket 也是 helper 到 host 的 notification 流。`stateChanged` 没有 `id`，其
`params` 为 `{sequence,state,playbackGeneration?,transportSessionID?,origin?}`；见
[`playback-state-sync.md`](playback-state-sync.md)。

## 方法

方法归属：Apple discovery/library/resolve 与 Apple queue/full/preview 都由 `lilt-player` 提供，但 server
以独立 client 实例区分 resource role 和 playback role：resource role 只可调用前者，绝不调用播放/队列方法。
`radio*` 与 `url*` 只属于 `lilt-audio`。两类 app 共有 `ping`、`pause`、`resume`、`stop`、`state`、订阅和
`shutdown`；未声明方法返回 `unknown_command`。

| 方法 | params | result |
|---|---|---|
| `ping` | — | `{pid}` |
| `authorize` | `{request?:bool}` | Apple/MusicKit 内部授权状态 |
| `diagnose` | — | `TokenDiagnostics` |
| `search` | `{term,limit}` | `[Item]` 歌曲 |
| `searchAlbums` | `{term,limit}` | `[Item]` 专辑（Apple catalog） |
| `searchPlaylists` | `{term,limit}` | `[Item]` 歌单 |
| `libraryPlaylists` | — | `[Item]` 资料库歌单 |
| `libraryAlbums` | — | `[Item]` 资料库专辑 |
| `recommendations` | — | `[Item]` Apple 推荐（playlists + stations） |
| `trackInfo` | `{kind,id}` | `[Item]` 单个 catalog/资料库项的展示元数据；未授权时返回裸 identity（供 `favorites.add` 补全） |
| `playlistTracks` | `{id}` | `{playlist: Item, items: [Item]}`：歌单行（名称/作者）+ 曲目 |
| `albumTracks` | `{id}` | `{album: Item, items: [Item]}`：资料库或目录专辑及其曲目 |
| `stations` | `{term,limit}` | `[Item]` 电台（MusicKit） |
| `play` | `{kind,id?,url?,storefront?,startAt?,startTrackID?,reverse?,fromHere?}`（kind 为 `song`/`playlist`/`station`；`album` 由 server 展开后走 `playSongs`） | `State` |
| `playSongs` | `{ids:[…],startAt}` | `State`。一次性赋值整个有限队列并从 `startAt` 起播：队列可被 `queueJump` 重建、无需节奏填充。MusicKit 拒绝整批 prepare（Code=6）时以错误返回，由 server 回退到起播+节奏 append |
| `queueJump` | `{index}` | `State` |
| `queueRemove` | `{index}` | `{state,undoHandle?}`；仅 future canonical Song 返回私有 handle |
| `queueRestore` | `{undoHandle}` | `State`；原子插回 helper 保留的原始 `Song`，不重新查询 |
| `queueMove` | `{from,to}` | `State` |
| `queueClear` | — | `State` |
| `pause` / `resume` | — | `State` |
| `resumeFilledQueue` | — | `State`；仅供 server 的节奏 append 回退在填充完成后 re-pin，普通恢复仍用 `resume` |
| `next` / `previous` | — | `State` |
| `stop` | — | `State` |
| `setShuffle` | `{on:bool}` | `State` |
| `setRepeat` | `{mode:"off"\|"all"\|"one"}` | `State` |
| `enqueue` | `{kind,id?,url?,position:"next"\|"tail"}` | `State` |
| `radioPlay` | `{url,name?}` | `State` |
| `radioStop` | — | `State` |
| `urlPlay` | `{url,title,artist?,artworkURL?,duration?,providerID?,playbackGeneration,transportSessionID}` | `State + playbackGeneration + transportSessionID` |
| `urlStop` | `{playbackGeneration,transportSessionID}` | `State + playbackGeneration + transportSessionID` |
| `radioProbe` | `{url,timeoutMs}` | `RadioProbeResult` |
| `state` | — | `{state,playbackGeneration?,transportSessionID?}` |
| `subscribeState` | — | `{sequence,state,playbackGeneration?,transportSessionID?}` |
| `unsubscribeState` | — | `{}` |
| `shutdown` | — | `{}` |

`playbackGeneration`/`transportSessionID`/`origin` 是私有协议字段。server 在每个 state-changing
RPC 中传入 generation/session，并在发送 start 前绑定该二元组；helper 在 response 与
`stateChanged` 中回显它，`state`/`subscribeState` 对 active URL session 也回显。表格只列各方法
自身参数。所有 observer 在注册时捕获该 generation/session；媒体键变化使用其所属 session 并标记
`origin:"external"`。只读方法与 `radioProbe` 不携带这些字段。

启动顺序是固定的：server 在锁内创建并绑定 pending generation/session，然后发送 start；helper 安装该
session、注册 observer，并在写出 start response 前缓冲 observer notification。host 先处理 response，
把 pending binding 提交为 active，再按 socket 顺序合并缓冲 notification。helper restart 会废弃所有
binding。server 对 subscribe snapshot 同样要求匹配 active binding；无 active session 的 snapshot 仅可投影
为 stopped，不能改变 source/queue 归属。

Apple 有限队列的主路径是 `playSongs` 一次性赋值并起播，MusicKit 拒绝整批 prepare（Code=6）
时由 server 回退为 `play` 所选曲目 + 有节奏的逐条 `enqueue`，并如实报告填充不完整。
回退路径填充结束、最终状态为 stopped/paused 且有当前曲目时（包括跳过部分无法入队的曲目），
用私有 `resumeFilledQueue` 起播：仅当首次 `play()` 抛出
`MPMusicPlayerControllerErrorDomain Code=1` 时等待 200 ms、检查队列仍在且未已起播，至多再调用
一次 `play()`；其他错误、取消或再次拒绝原样失败，不重建/重放队列。随后仍须等待位置真实推进，
不能将 MusicKit 的瞬时 `playing` 当作成功；server 失败时返回 `partial_failure` + `queueReady` 并保留队列。
专辑也先经 `albumTracks` 解析曲目，再走同一有限队列路径；`play` 不接受 `kind:album`。
`albumTracks` 的解析按 catalog 权威优先：先以「标题 + 艺人」做 catalog 搜索，命中同标题专辑后取
`.with([.tracks])`（对目录与资料库专辑都给出真实曲目表）；其次是专辑自身的 `.with([.tracks])`，
最后是库内按 `albumTitle` 的本地歌曲；任一梯级非空即接受（末档直接读传入对象已水化的
`album.tracks`）。库内关系只反映本地内容，且 1 曲 Single
必须按非空接受，因此不设 `count > 1` 守卫。返回专辑行（kind `album`）加曲目；资料库骨架与目录
专辑走同一解析。

MusicKit 的 `play()` / `skipTo...` 可以先返回、后起播；`play`、`playSongs`、`queueJump`、
有目标的 `next/previous`、`resume` 与 `resumeFilledQueue` 的成功 response 必须等待**当前曲目的播放位置真实推进**
（最长 12 秒），不能只凭队列已填或瞬间的 `playing`。shuffle 后 next/previous 不猜固定
目标行，必须看到实际 entry 变化；确认自然结束时允许没有后继项。`pause` 最长等待 4 秒确认稳定的
`paused`。所选曲身份先用 Song ID 核对；MusicKit 将 library Song 换为 catalog ID 时，仅当
标题、艺人、专辑、时长把实际 Song **唯一映射到所选 canonical 队列行**才确认同一首；
另一行、重复或无法确认时保持失败，不能用“有进度”代替正确落点。超时停止播放并返回
`playback_error`，不能回退为试听或宣称已完成；这也避免切歌后立刻暂停被迟到的 `play()` 覆盖。
无下一项的队列边界不等待不存在的目标。
起播确认超时的私有 helper error message 附带**一次有界汇总** `startDiagnostic`：采样数、
`playing` / 进度推进的次数、推进时实际曲目不匹配 / 缺失 / entry 未变化的次数，以及最后的
状态、匹配结果、进度和队列条目数；不记录歌曲名、原始 song/entry ID 或媒体 URL。
server 将它写入 journal 的 `rpc.error`，并仅放在公开 `playback_error.details.detail` 中，
用户界面仍显示稳定的 `Playback could not be started`。成功起播的私有 RPC response 带顶层
`debug`，失败时 `playback_error` 的私有 RPC error 带 `debug`；两者只含预期/实际曲目的稳定 ID、
标题、艺人、专辑、时长、canonical 队列索引、shuffle、状态、进度及确认依据（ID 或唯一 metadata）。
仅 `just run` 的 server 在本机 journal 把白名单标量字段分别写成 `rpc.start`（成功）或 `rpc`（失败）
的 `debug*`，普通运行不写。`debug` 不能进入 Client API response、watch、持久状态或日志中的短期
签名媒体 URL；journal 仍以 `0600` 存储、5 MB 轮换。只在起播返回时附一次身份快照，
不记录每 100 ms 的元数据。
这些观测只能解释 helper 为什么没确认起播，**不能单凭进度或 `playing` 断言实际听到的声音**；
确认规则与停播行为不变。

Audius 有限播放由 server 在起播时准备私有 URLQueuePlan；每项取得 URL 与 artwork URL，
再调用 `lilt-audio` 的 `urlPlay`。`urlPlay` 由 helper 的私有 `url` mode 实现。URLQueueTransport 在 server 侧操作有限公开队列并做
next/previous/jump；helper 的 `url` mode 只播放当前 item。`queueRemove`、`queueMove`、`enqueue`
由 server 侧 URL 队列处理，不经过 helper 的 MusicKit 队列方法。签名 URL 为运行期输入，
helper 和 server 都不得持久化。

## State 形状

```jsonc
{
  "track": { "kind":"song|playlist|station|stream", "id?","url?","title","artist?","previewURL?" },
  "position": 0.0, "duration": 0.0, "status": "stopped|playing|paused|buffering|error",
  "audioVariant": null, "format": "System-selected", "availableFormats": [],
  "shuffle": false, "repeatMode": "off|all|one", "isLive": false,
  "mode": "none|preview|full|stream|url", "authorization": "authorized|denied|restricted|not_determined|unknown",
  "accountStatus": "ready", "accountError": null, "playbackError": null,
  "queue": [ ...Track... ], "queueIndex": 0
}
```

- `mode`：`preview`=30s 试听（AVPlayer）、`full`=MusicKit 完整播放、`stream`=广播
  （AVPlayer，`isLive=true`）。内部 `url`=有限 direct-URL 队列（AVFoundation），不是公开
  Client API 枚举；server MUST 将它投影为 `mode:"full"` 和显式 `source`（`audius` 或 `jamendo`）。
- `status` 的语义边界：`paused` 只表示**真的被暂停**（客户端 `pause` 或系统媒体键/Now Playing 面板的
  pause/toggle 命令），MUST NOT 用来表示“流不再推进”。AVPlayer 在流停止推进时也会落到
  `timeControlStatus == .paused`，且**不报任何错误**；helper MUST 把它报成 `buffering`。否则 server
  的 stall 看门狗会把一个已经死掉的流当成“用户在休息”，永不重试（实测：Jamendo 某曲因 CDN 吞吐不足
  而永远停在 position 0，无错误、无声音）。
  反方向同样重要：`paused` 一旦上报，server MUST 视为休息而**不重试**——媒体键暂停是 server 从未
  见过的命令，重试会把用户主动暂停的曲目重新拉起来（2026-09-22 实测：媒体键暂停后等待 30s 仍
  保持暂停，再按一次可继续播放）。映射规则与测试见
  `player/Sources/LiltPlayerLogic/PlaybackSelection.swift` 的 `mediaSessionStatus`。
- 私有 helper 的 `queue` 仅 MusicKit `full` 有值；radio/preview 为空数组。`url` mode
  不保存整条 queue，有限 queue 由 server/URLQueueTransport 持有。它不是公开 Client API 的 queue 限制；通用有限队列规则见
  [`../client-api/models.md`](../../client-api/models.md#有限队列不变量)。
- **canonical 队列投影（Apple full mode）**：helper 记录播放时解析出的 `[Song]`（增删/移动同步
  维护）作为唯一 canonical 顺序。`state()` 保持该队列的提交顺序；当前项先用 MusicKit
  `currentEntry` 的 Song id 唯一匹配，再用标题、艺术家、专辑和时长唯一匹配（MusicKit 可能
  播放 catalog Song，而列表保存 library Song）。`Queue.Entry.id` 会重建，上一首的索引也
  不能代表自然切歌或 shuffle 的落点，因此不得靠它们猜测。无法唯一定位时，`track` 报告
  `currentEntry` 实际曲目，`queueIndex=-1` 表示当前队列行未知；不会把旧行伪报为当前。
  **不从**
  MusicKit 的 live `queue.entries` 顺序投影——shuffle 会重排该数组，
  它只驱动随机推进（一轮内不重复、耗尽 no-op、`repeat all` 重洗），绝不进入 wire 状态。
  `queueJump`/`queueRemove`/`queueMove` 的 index 都按 canonical 顺序解释；jump 先把 shuffle 短暂
  置 off 再重建（`startingAt` 才被尊重），play 成功后恢复 shuffle；remove 按 song id 从 live
  entries 移除；move 只在未开 shuffle 时把 canonical 的移动应用到 live entries（shuffle 下重排它
  只会干扰随机推进）。重排 live entries 会让 MusicKit 按 live **下标**重新解析当前项，因此凡使正在
  播放条目 live 下标变化的 move（跨过当前行，或移动当前行本身）都必须在赋值后把播放步进回该条目
  **移动后所在的新行**，并恢复 position 与暂停态；否则会切到落在那条旧下标上的曲目并从 0:00 重播
  （相邻跨位时即被移动的曲目；实测 2026-09-30）。该重解析是异步的，且赋值会重新生成 `Queue.Entry.id`：helper 用 Song payload id
  而非 entry id 判定当前项，并在重载落定后再走回（赋值后立即 skip 会被丢成重启当前项，实测）。
  下标不变的 move 无额外等待。走回以赋值前的 live 行号计算、以赋值后的 live 行号落定：不能用
  Song payload id（同一首歌可在队列中重复），而赋值前的 `Queue.Entry.id` 只用于找出该行。走回靠逐行 `skipToNextEntry/PreviousEntry`，为不超出 RPC 预算只在
  位移 ≤8 行时执行；更远的移动不做走回，由 server 依据真实状态返回 `partial_failure`
  （[`../../client-api/commands.md`](../../client-api/commands.md)）。**append 构建的队列无法重建**：MusicKit 对这类队列返回 `Code=6 Failed to prepare to
  play` 并丢掉 live queue，且 `skipToNextEntry` 会跳过无法 prepare 的条目（实测落点偏移）。
  helper 对这类队列不再尝试跳转：直接报错说明该行跳不过去，绝不拿正在播的队列去冒险重建；
  见 [`../product/limitations.md`](../../product/limitations.md) 第 7b 节。
  容器整体入队（playlist/station）使 Song 列表失效时，helper 回退为 live entries 投影，
  此时 queue 顺序即 MusicKit 实际顺序。
- **删除 Undo**：`queueRemove` 删除 future canonical song 时保留该次解析得到的原始 Swift `Song`、
  原 index、删除后的 canonical fingerprint 与当时 current index，并返回不透明 `undoHandle`；只有最近
  一次删除可恢复。`queueRestore` 在一个串行 RPC 内复核 handle、完整 fingerprint 与 current row，随后
  用保存的 `Song` 创建新 `Queue.Entry` 并插回 canonical 原位。不得按 ID 重查，也不得重建并重播整队。
  `queueSongs == nil`（例如无法建立 Song canonical mapping 的容器 entry）、自然切歌、其他 queue mutation
  或 helper restart 均返回 `queue_undo_unavailable` 且不修改队列。公开 5 秒期限、revision 与 token 由
  server 持有；helper handle 不进入 Client API。
- `availableFormats` 来自曲目可用编码；`format` 是 MusicKit 回报的当前编码，或其未回报时的 `System-selected`。后者不能据此判断实际是 AAC 还是 ALAC。
- `playbackError` 为 AVPlayer item 失败的可操作说明；`accountStatus/accountError` 可随状态推送更新 UI 指引。
- `authorization`、`accountStatus` 与 `accountError` 是 Apple/MusicKit helper 的内部字段。
  server 在产生公共 `PlaybackState` 时 MUST 排除或映射它们；公共来源授权仅通过
  Client API authorization 命令和 Source capability 表达。
- 歌单播放同时支持 catalog 与 library id。Music video、不可用项和其他非 song 项无法进入 `ApplicationMusicPlayer` song queue，因此浏览与播放都按相同规则过滤。起始项按 `startTrackID`（稳定 id）→相对于完整显示顺序的合法 `startAt` 解析；UI 临时过滤必须映射回该完整顺序，`reverse` 在解析起点之前应用。
- host 对每次 helper 调用设置 deadline。Swift/MusicKit 串行调用没有可靠的请求级取消；任何 RPC 超时都会关闭并永久作废该 transport、拒绝迟到 response/notification，并终止该私有 helper 实例以阻止迟到副作用。该 helper 实例不能复用。旧的前台 host 要求退出/重启；采用 [`../client-api/README.md`](../../client-api/README.md) v0.1 的常驻 server 可以创建全新的私有 helper 实例，但不得自动重放超时命令。socket 关闭后 host 不再插值旧进度。
- server 为一次 playback session 分配 `playbackGeneration` 和不可复用 `transportSessionID`，
  并在 start 前传给 helper。helper response 与每个因该 call 发出的 `stateChanged` MUST 回显两者；server
  丢弃 helper instance、generation 或 session 不匹配的通知。系统/媒体键事件使用 observer 捕获的
  generation/session 并标为 `origin:"external"`。`State.ended` 是只限 helper→server 的私有 bool：仅
  `url` AVPlayer 自然结束时为 true，public Client API、watch 和持久 schema 必须剥离它。

## 错误码

helper 自身返回的错误码。`lilt-player`：`preview_unavailable`、`preview_search_unavailable`、`preview_unsupported`、`authorization_required`、`queue_unavailable`、`queue_not_jumpable`（append 构建的队列拒绝 jump，message 说明播放是否继续与出路）、`queue_undo_unavailable`（未改队列的精确恢复拒绝）、`invalid_reference`、`invalid_search`、`unknown_command`、`music_error`、`playback_error`、`nothing_playing`。`lilt-audio`：`invalid_reference`、`nothing_playing`、`unknown_command`、`audio_error`。
`player_unavailable` 与 `diagnostics_failed` 是 CLI `doctor` 的诊断错误，不是 helper 协议码；`no_active_session` 是 Client API socket 层错误，也不出现在 helper 协议中。

## 互斥

严格互斥由 server 跨进程执行：进入 MusicKit transport 前停止并 shutdown 活动的 `lilt-audio`
playback backend；进入 Radio stream 或 Audius/Jamendo URL transport 前停止并 shutdown 活动的 `lilt-player`
playback backend。Apple resource client 不属于该互斥链：它可继续执行只读 MusicKit 请求，但不得持有
队列、调用 `play` 或发布公开 playback state。任一时刻只有一个 helper 实际播放并拥有 Now Playing。
公开 source 由 server 在提交时记录。完整路由见 [`providers.md`](../providers/providers.md)。
