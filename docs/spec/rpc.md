# Spec: RPC（helper 协议）

## 传输

- 换行分隔的 JSON-RPC 2.0，走**私有 Unix socket**。
- 桌面：Go 用 LaunchServices 启动签名的 `lilt-player.app --rpc-socket <path>`，app 以 `0600` 绑定 socket，只接受一个 host，顺序处理请求；所有 response 和 notification 由同一个串行 writer 写入。
- 手机/其他端不使用该 socket，直接在进程内实现同一**行为契约**（本文件是行为参考，不要求复用传输）。

## 请求/响应

```json
{"jsonrpc":"2.0","id":1,"method":"play","params":{...}}
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":1,"error":{"code":"preview_unsupported","message":"..."}}
```

同一 socket 也是 helper 到 host 的 notification 流。`stateChanged` 没有 `id`，其
`params` 为 `{sequence,state}`；见 [`playback-state-sync.md`](playback-state-sync.md)。

## 方法

| 方法 | params | result |
|---|---|---|
| `ping` | — | `{pid}` |
| `authorize` | `{request?:bool}` | `Authorization` |
| `diagnose` | — | `TokenDiagnostics` |
| `search` | `{term,limit}` | `[Item]` 歌曲 |
| `searchPlaylists` | `{term,limit}` | `[Item]` 歌单 |
| `libraryPlaylists` | — | `[Item]` 资料库歌单 |
| `playlistTracks` | `{id}` | `[Item]` 歌单曲目 |
| `recentPlayed` | `{limit}` | `[Item]` |
| `stations` | `{term,limit}` | `[Item]` 电台（MusicKit） |
| `resolveUrl` | `{url}` | `[Item]` |
| `play` | `{kind,id?,url?,storefront?,startAt?,startTrackID?,startTitle?,reverse?}` | `State` |
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
| `state` | — | `State` |
| `subscribeState` | — | `{sequence,state}` |
| `unsubscribeState` | — | `{}` |
| `shutdown` | — | `{}` |

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
- `queue` 仅 `full` 有值（Apple Music）；radio/preview 为空数组。
- `availableFormats` 来自曲目可用编码；`format` 是 MusicKit 回报的当前编码，或其未回报时的 `System-selected`。后者不能据此判断实际是 AAC 还是 ALAC。
- `playbackError` 为 AVPlayer item 失败的可操作说明；`accountStatus/accountError` 可随状态推送更新 UI 指引。
- 歌单播放同时支持 catalog 与 library id。Music video、不可用项和其他非 song 项无法进入 `ApplicationMusicPlayer` song queue，因此浏览与播放都按相同规则过滤。起始项按 `startTrackID`（稳定 id）→相对于完整显示顺序的合法 `startAt`→`startTitle`（兼容兜底）解析；UI 临时过滤必须映射回该完整顺序，`reverse` 在解析起点之前应用。
- host 对每次 helper 调用设置 deadline。Swift/MusicKit 串行调用没有可靠的请求级取消；任何 RPC 超时都会关闭并永久作废该 transport、拒绝迟到 response/notification，并终止该私有 helper 实例以阻止迟到副作用。后续调用立即失败并要求退出/重启 lilt。socket 关闭后 host 不再插值旧进度。

## 错误码

`no_active_session`（TUI host socket 层）、`preview_unavailable`、`preview_search_unavailable`、`preview_unsupported`、`authorization_required`、`queue_unavailable`、`invalid_reference`、`invalid_search`、`unknown_method`、`music_error`、`player_unavailable`、`search_failed`、`library_failed`、`recent_failed`、`diagnostics_failed`。

## 互斥

严格互斥：`play` 会停止广播；`radioPlay` 会 `ApplicationMusicPlayer.stop()` 并停试听。任一时刻只有一个音源。
