# Spec: RPC（helper 协议）

## 传输

- 换行分隔的 JSON-RPC 2.0，走**私有 Unix socket**。
- 桌面：Go 用 LaunchServices 启动签名的 `lilt-player.app --rpc-socket <path>`，app 以 `0600` 绑定 socket，只接受一个 host，顺序处理请求。
- 手机/其他端不使用该 socket，直接在进程内实现同一**行为契约**（本文件是行为参考，不要求复用传输）。

## 请求/响应

```json
{"jsonrpc":"2.0","id":1,"method":"play","params":{...}}
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":1,"error":{"code":"preview_unsupported","message":"..."}}
```

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
| `play` | `{kind,id?,url?,storefront?,startAt?}` | `State` |
| `pause` / `resume` | — | `State` |
| `next` / `previous` | — | `State` |
| `stop` | — | `State` |
| `setShuffle` | `{on:bool}` | `State` |
| `setRepeat` | `{mode:"off"\|"all"\|"one"}` | `State` |
| `enqueue` | `{kind,id?,url?,position:"next"\|"tail"}` | `State` |
| `radioPlay` | `{url,name?}` | `State` |
| `radioStop` | — | `State` |
| `state` | — | `State` |
| `shutdown` | — | `{}` |

## State 形状

```jsonc
{
  "track": { "kind":"song|playlist|station|stream", "id?","url?","title","artist?","previewURL?" },
  "position": 0.0, "duration": 0.0, "status": "stopped|playing|paused|buffering",
  "audioVariant": null, "format": "Auto", "availableFormats": [],
  "shuffle": false, "repeatMode": "off|all|one", "isLive": false,
  "mode": "none|preview|full|stream", "authorization": "authorized|denied|restricted|not_determined|unknown",
  "queue": [ ...Track... ], "queueIndex": 0
}
```

- `mode`：`preview`=30s 试听（AVPlayer）、`full`=MusicKit 完整播放、`stream`=广播（AVPlayer，`isLive=true`）。
- `queue` 仅 `full` 有值（Apple Music）；radio/preview 为空数组。
- `availableFormats` 来自曲目可用编码；`format` 是当前编码或 `Auto`。

## 错误码

`no_active_session`（TUI host socket 层）、`preview_unavailable`、`preview_search_unavailable`、`preview_unsupported`、`authorization_required`、`queue_unavailable`、`invalid_reference`、`invalid_search`、`unknown_method`、`music_error`、`player_unavailable`、`search_failed`、`library_failed`、`recent_failed`、`diagnostics_failed`。

## 互斥

严格互斥：`play` 会停止广播；`radioPlay` 会 `ApplicationMusicPlayer.stop()` 并停试听。任一时刻只有一个音源。
