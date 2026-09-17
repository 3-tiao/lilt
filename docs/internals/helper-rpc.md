# Spec: RPC（helper 协议）

本文件只定义 Go server 与私有 Swift helper 之间的内部协议。TUI、CLI、AI skill
和未来 client 使用的统一接口见 [`../client-api/README.md`](../client-api/README.md)，不得直接依赖
helper method。

## 传输

- 换行分隔、JSON-RPC 形状的消息，走**私有 Unix socket**。`error.code` 是稳定字符串
  （如 `preview_unsupported`），不是 JSON-RPC 2.0 要求的整数；以本文件为准。
- 请求默认顺序处理；`radioProbe` 是例外：它由独立任务执行并按完成顺序返回，
  不阻塞播放与状态（见 [`radio-discovery.md`](radio-discovery.md)）。
- 桌面：Go 用 LaunchServices 启动签名的 `lilt-player.app --rpc-socket <path>`，app 以 `0600` 绑定 socket，只接受一个 host，顺序处理请求；所有 response 和 notification 由同一个串行 writer 写入。
- 手机/其他端不使用该 socket，直接在进程内实现同一**行为契约**（本文件是行为参考，不要求复用传输）。

## 请求/响应

```json
{"jsonrpc":"2.0","id":1,"method":"play","params":{...}}
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":1,"error":{"code":"preview_unsupported","message":"..."}}
```

同一 socket 也是 helper 到 host 的 notification 流。`stateChanged` 没有 `id`，其
`params` 为 `{sequence,state,actionEpoch?,origin?}`；见
[`playback-state-sync.md`](playback-state-sync.md)。

## 方法

| 方法 | params | result |
|---|---|---|
| `ping` | — | `{pid}` |
| `authorize` | `{request?:bool}` | Apple/MusicKit 内部授权状态 |
| `diagnose` | — | `TokenDiagnostics` |
| `search` | `{term,limit}` | `[Item]` 歌曲 |
| `searchPlaylists` | `{term,limit}` | `[Item]` 歌单 |
| `libraryPlaylists` | — | `[Item]` 资料库歌单 |
| `playlistTracks` | `{id}` | `[Item]` 歌单曲目 |
| `stations` | `{term,limit}` | `[Item]` 电台（MusicKit） |
| `resolveUrl` | `{url}` | `[Item]` |
| `play` | `{kind,id?,url?,storefront?,startAt?,startTrackID?,startTitle?,reverse?,actionEpoch?}` | `State + actionEpoch` |
| `playSongs` | `{ids:[string],startIndex}` | `State` |
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
| `radioProbe` | `{url,timeoutMs}` | `RadioProbeResult` |
| `state` | — | `State` |
| `subscribeState` | — | `{sequence,state}` |
| `unsubscribeState` | — | `{}` |
| `shutdown` | — | `{}` |

所有会改变播放状态的方法都接受公共可选字段 `actionEpoch`，并在 response 中回显；
表格只列各方法自身参数。无 epoch 的系统/媒体键变化由 helper 生成新 epoch，并标记
`origin:"external"`。只读方法与 `radioProbe` 不携带 action epoch。

`playSongs.ids` 是 Apple helper 的内部 provider IDs。Client API 的
`playback.playSongs.refs` 使用 canonical refs；server 验证单一 Source 后才为 Apple Music
投影为本方法的 ids。Audius 由自己的 remote stream engine 执行，不调用此 helper。

## State 形状

```jsonc
{
  "track": { "kind":"song|playlist|station|stream", "id?","url?","title","artist?","previewURL?" },
  "position": 0.0, "duration": 0.0, "status": "stopped|playing|paused|buffering|error",
  "audioVariant": null, "format": "System-selected", "availableFormats": [],
  "shuffle": false, "repeatMode": "off|all|one", "isLive": false,
  "mode": "none|preview|full|stream", "authorization": "authorized|denied|restricted|not_determined|unknown",
  "accountStatus": "ready", "accountError": null, "playbackError": null,
  "queue": [ ...Track... ], "queueIndex": 0
}
```

- `mode`：`preview`=30s 试听（AVPlayer）、`full`=MusicKit 完整播放、`stream`=广播（AVPlayer，`isLive=true`）。
- 当前私有 helper 的 `queue` 仅 MusicKit `full` 有值；radio/preview 为空数组。它不是
  公开 Client API 的 queue 限制；目标通用有限队列规则见
  [`../client-api/models.md`](../client-api/models.md#有限队列不变量)。
- `availableFormats` 来自曲目可用编码；`format` 是 MusicKit 回报的当前编码，或其未回报时的 `System-selected`。后者不能据此判断实际是 AAC 还是 ALAC。
- `playbackError` 为 AVPlayer item 失败的可操作说明；`accountStatus/accountError` 可随状态推送更新 UI 指引。
- `authorization`、`accountStatus` 与 `accountError` 是 Apple/MusicKit helper 的内部字段。
  server 在产生公共 `PlaybackState` 时 MUST 排除或映射它们；公共来源授权仅通过
  Client API authorization 命令和 Source capability 表达。
- 歌单播放同时支持 catalog 与 library id。Music video、不可用项和其他非 song 项无法进入 `ApplicationMusicPlayer` song queue，因此浏览与播放都按相同规则过滤。起始项按 `startTrackID`（稳定 id）→相对于完整显示顺序的合法 `startAt`→`startTitle`（兼容兜底）解析；UI 临时过滤必须映射回该完整顺序，`reverse` 在解析起点之前应用。
- host 对每次 helper 调用设置 deadline。Swift/MusicKit 串行调用没有可靠的请求级取消；任何 RPC 超时都会关闭并永久作废该 transport、拒绝迟到 response/notification，并终止该私有 helper 实例以阻止迟到副作用。该 helper 实例不能复用。旧的前台 host 要求退出/重启；采用 [`../client-api/README.md`](../client-api/README.md) v2 的常驻 server 可以创建全新的私有 helper 实例，但不得自动重放超时命令。socket 关闭后 host 不再插值旧进度。
- compound Client API 操作由 server 分配 `actionEpoch` 并传给每个相关的状态变更 helper call。helper
  response 与每个因该 call 发出的 `stateChanged` MUST 回显它。系统/媒体键事件自行生成
  epoch 并标为 `origin:"external"`；server 只能 coalesce 同一 compound epoch。

## 错误码

helper 自身返回的错误码：`preview_unavailable`、`preview_search_unavailable`、`preview_unsupported`、`authorization_required`、`queue_unavailable`、`invalid_reference`、`invalid_search`、`unknown_method`、`music_error`、`player_unavailable`、`search_failed`、`library_failed`、`recent_failed`、`diagnostics_failed`。`no_active_session` 是 Client API socket 层错误，不出现在 helper 协议中。

## 互斥

严格互斥：`play` 会停止广播；`radioPlay` 会 `ApplicationMusicPlayer.stop()` 并停试听。任一时刻只有一个音源。
