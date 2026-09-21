# Spec: RPC（helper 协议）

本文件只定义 Go server 与私有 Swift helper 之间的内部协议。TUI、CLI、AI skill
和未来 client 使用的统一接口见 [`../client-api/README.md`](../client-api/README.md)，不得直接依赖
helper method。

## 播放期间的进程活动断言

helper 在播放（`playing` / `waitingToPlayAtSpecifiedRate`）期间持有
`ProcessInfo.beginActivity([.userInitiated, .latencyCritical])`，停止时释放。原因：helper 是 accessory
app，macOS 会节流/挂起它（实测暂停前 1 秒采样器静默约 5 秒），随后 MusicKit 自己报 `paused`，用户看到
"只响一下就停"。两个 helper 并存时最容易触发（4/10 次），持有断言后 12 次全无；单进程时本来也不复现。

结论：**不需要 helper 之间的所有权/lease 协调**——这个断言就够了。
`LILT_PLAYER_ACTIVITY_ASSERT=0` 只用于对照实验。

## 传输

- 换行分隔、JSON-RPC 形状的消息，走**私有 Unix socket**。`error.code` 是稳定字符串
  （如 `preview_unsupported`），不是 JSON-RPC 2.0 要求的整数；以本文件为准。
- 请求默认顺序处理；`radioProbe` 是例外：它由独立任务执行并按完成顺序返回，
  不阻塞播放与状态（见 [`radio-discovery.md`](radio-discovery.md)）。
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

方法归属：Apple discovery/queue/full/preview 只属于 `lilt-player`；`radio*` 与 `url*` 只属于
`lilt-audio`。两者共有 `ping`、`pause`、`resume`、`stop`、`state`、订阅和 `shutdown`；未声明方法
返回 `unknown_command`。

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
| `playlistTracks` | `{id}` | `{playlist: Item, items: [Item]}`：歌单行（名称/作者）+ 曲目 |
| `albumTracks` | `{id}` | `{album: Item, items: [Item]}`：资料库或目录专辑及其曲目 |
| `stations` | `{term,limit}` | `[Item]` 电台（MusicKit） |
| `play` | `{kind,id?,url?,storefront?,startAt?,startTrackID?,reverse?,fromHere?}`（kind 为 `song`/`playlist`/`station`；`album` 由 server 展开为歌曲队列，见下） | `State` |
| `queueJump` | `{index}` | `State` |
| `queueRemove` | `{index}` | `State` |
| `queueMove` | `{from,to}` | `State` |
| `queueClear` | — | `State` |
| `pause` / `resume` | — | `State` |
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

Apple helper 不再提供批量 `playSongs` RPC：MusicKit 对一次性批量队列会在个别
"慢准备"曲目上整批失败（Code=6）甚至挂起。`playback.playSongs` 由 server 编排——用
`play` 启动所选曲目（等 transport 报告 active 后）再逐条 `enqueue` 追加，每条之间留
节奏间隔；被 helper 拒绝的曲目跳过而不失败整批。Audius 有限播放同样不经过批量队列：
server 在播放启动时由 Audius provider 准备一个私有 URLQueuePlan；URLQueueTransport 在
每次曲目启动时取得 URL 与可选 artwork URL，再调用 `lilt-audio` 的 `urlPlay`。

`play` 不再接受 `kind:album`：把整张专辑交给 MusicKit 会在起播窗口内追加队列而把 player 卡成
“队列已建满但未播放”（Code=1 / “Stopped”）。专辑播放由 **server 编排**：先用 `albumTracks` 解析
曲目，再用 `play`（所选曲）+ 逐条 `enqueue` 追加其余曲目，与 `playback.playSongs` 同一条已验证
路径。`albumTracks` 的解析：库骨架 album 先查库内同专辑歌，再用 catalog 搜索补齐，返回专辑行
（kind `album`）加曲目；资料库骨架与目录专辑都走同一回退。

`urlPlay` 由 helper 的私有 `url` mode 实现。URLQueueTransport 在 server 侧操作有限公开队列并做
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
  Client API 枚举；server MUST 将它投影为 `mode:"full"` 和显式 `source:"audius"`。
- 私有 helper 的 `queue` 仅 MusicKit `full` 有值；radio/preview 为空数组。`url` mode
  不保存整条 queue，有限 queue 由 server/URLQueueTransport 持有。它不是公开 Client API 的 queue 限制；通用有限队列规则见
  [`../client-api/models.md`](../client-api/models.md#有限队列不变量)。
- **canonical 队列投影（Apple full mode）**：helper 记录播放时解析出的 `[Song]`（增删/移动同步
  维护）作为唯一 canonical 顺序。`state()` 的 `queue`/`queueIndex` 从它投影；helper 在创建或
  重建队列时记录稳定的 MusicKit local `Queue.Entry.id` → canonical index 映射，据此定位当前项，
  并只在该映射缺失时回退到 currentEntry payload 的 Song id 和最后确认的 cursor；`next`/`previous`
  成功后按实际步进推进该 cursor（`advancedQueueCursor`），否则 currentEntry 报 catalog id 而
  canonical 队列持有 library/queue id 时，投影会把标题与 `queueIndex` 留在上一首。**不从**
  MusicKit 的 live `queue.entries` 顺序投影——shuffle 会重排该数组，
  它只驱动随机推进（一轮内不重复、耗尽 no-op、`repeat all` 重洗），绝不进入 wire 状态。
  `queueJump`/`queueRemove`/`queueMove` 的 index 都按 canonical 顺序解释；jump 先把 shuffle 短暂
  置 off 再重建（`startingAt` 才被尊重），play 成功后恢复 shuffle；remove 按 song id 从 live
  entries 移除；move 只在未开 shuffle 时同步重排 live entries（shuffle 下重排它只会干扰随机
  推进）。**append 构建的队列无法重建**：MusicKit 对这类队列返回 `Code=6 Failed to prepare to
  play` 并丢掉 live queue，且 `skipToNextEntry` 会跳过无法 prepare 的条目（实测落点偏移）。
  helper 对这类队列不再尝试跳转：直接报错说明该行跳不过去，绝不拿正在播的队列去冒险重建；
  见 [`../product/limitations.md`](../product/limitations.md) 第 7b 节。
  容器整体入队（playlist/station）使 Song 列表失效时，helper 回退为 live entries 投影，
  此时 queue 顺序即 MusicKit 实际顺序。
- `availableFormats` 来自曲目可用编码；`format` 是 MusicKit 回报的当前编码，或其未回报时的 `System-selected`。后者不能据此判断实际是 AAC 还是 ALAC。
- `playbackError` 为 AVPlayer item 失败的可操作说明；`accountStatus/accountError` 可随状态推送更新 UI 指引。
- `authorization`、`accountStatus` 与 `accountError` 是 Apple/MusicKit helper 的内部字段。
  server 在产生公共 `PlaybackState` 时 MUST 排除或映射它们；公共来源授权仅通过
  Client API authorization 命令和 Source capability 表达。
- 歌单播放同时支持 catalog 与 library id。Music video、不可用项和其他非 song 项无法进入 `ApplicationMusicPlayer` song queue，因此浏览与播放都按相同规则过滤。起始项按 `startTrackID`（稳定 id）→相对于完整显示顺序的合法 `startAt` 解析；UI 临时过滤必须映射回该完整顺序，`reverse` 在解析起点之前应用。
- host 对每次 helper 调用设置 deadline。Swift/MusicKit 串行调用没有可靠的请求级取消；任何 RPC 超时都会关闭并永久作废该 transport、拒绝迟到 response/notification，并终止该私有 helper 实例以阻止迟到副作用。该 helper 实例不能复用。旧的前台 host 要求退出/重启；采用 [`../client-api/README.md`](../client-api/README.md) v0.1 的常驻 server 可以创建全新的私有 helper 实例，但不得自动重放超时命令。socket 关闭后 host 不再插值旧进度。
- server 为一次 playback session 分配 `playbackGeneration` 和不可复用 `transportSessionID`，
  并在 start 前传给 helper。helper response 与每个因该 call 发出的 `stateChanged` MUST 回显两者；server
  丢弃 helper instance、generation 或 session 不匹配的通知。系统/媒体键事件使用 observer 捕获的
  generation/session 并标为 `origin:"external"`。`State.ended` 是只限 helper→server 的私有 bool：仅
  `url` AVPlayer 自然结束时为 true，public Client API、watch 和持久 schema 必须剥离它。

## 错误码

helper 自身返回的错误码：`preview_unavailable`、`preview_search_unavailable`、`preview_unsupported`、`authorization_required`、`queue_unavailable`、`invalid_reference`、`invalid_search`、`unknown_command`、`music_error`、`audio_error`、`player_unavailable`、`search_failed`、`library_failed`、`recent_failed`、`diagnostics_failed`。`no_active_session` 是 Client API socket 层错误，不出现在 helper 协议中。

## 互斥

严格互斥由 server 跨进程执行：进入 MusicKit transport 前停止并 shutdown `lilt-audio`；进入
Radio stream 或 Audius URL transport 前停止并 shutdown `lilt-player`。任一时刻只有一个 helper
实际播放并拥有 Now Playing。公开 source 由 server 在提交时记录。完整路由见
[`providers.md`](providers.md)。
