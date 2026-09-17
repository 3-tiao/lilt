# Client API —— Watch（实时事件）

`session.watch` 让 client 持续接收 server 的状态变化。TUI 用它同步播放状态、
队列与收藏；状态栏、移动端等 client 只订阅自己关心的 topic。

协议见 [`protocol.md`](protocol.md)；状态形状见 [`models.md`](models.md)。

## 1. 建立订阅

```json
{"version":2,"requestId":"...","command":"session.watch","params":{"includeState":true}}
```

参数：

| 参数 | 说明 |
|---|---|
| `includeState` | 初始快照是否包含 `AppState` |
| `topics` | 可选的事件过滤列表；缺省订阅全部 |

已知 topic 与事件的映射：

| topic | event |
|---|---|
| `playback` | `playback.changed` |
| `state` | `state.changed` |
| `sources` | `sources.changed` |
| `authorization` | `authorization.changed` |
| `engine` | `engine.restarted` |
| `server` | `server.warning`、`server.shuttingDown` |

未知 topic MUST 返回 `invalid_request`（避免拼写错误静默丢事件）。过滤只作用于
后续 event；初始快照的 `playback` 始终完整，`AppState` 仅在 `includeState=true`
时存在。

## 2. 初始快照

第一行是普通 response：

```jsonc
{
  "ok": true,
  "requestId": "...",
  "serverId": "...",
  "data": {
    "sequence": 42,
    "playback": { /* 完整 PlaybackState，含 queue */ },
    "state": { /* AppState；请求 includeState 时存在 */ },
    "sources": [ /* 订阅 sources 或 topics 缺省时存在 */ ],
    "authorizations": [ /* 订阅 authorization 或 topics 缺省时存在 */ ]
  }
}
```

## 3. 事件行

```json
{"serverId":"...","event":"playback.changed","sequence":43,"data":{"state":{}}}
{"serverId":"...","event":"state.changed","sequence":44,"data":{"state":{}}}
{"serverId":"...","event":"engine.restarted","sequence":45,"data":{"source":"apple-music"}}
{"serverId":"...","event":"sources.changed","sequence":46,"data":{"sources":[]}}
{"serverId":"...","event":"authorization.changed","sequence":47,"data":{"authorization":{},"flow":{}}}
{"serverId":"...","event":"server.warning","sequence":48,"data":{"code":"...","message":"..."}}
{"serverId":"...","event":"server.shuttingDown","sequence":49,"data":{}}
```

`server.warning` 与 `server.shuttingDown` MUST 始终送达且不受 `topics` 过滤，避免
client 在关键变化上失联。

## 4. 顺序与一致性

- watch 注册与初始快照 MUST 有一个**原子线性化点**：快照序号为 S，注册完成后
  只向该连接发送 `sequence > S` 的 event，不得漏掉两者之间的变化。
- 初始 `sources` 与 `authorizations` 是各自 topic 的完整权威快照；client 不需要先
  list 再 subscribe，因此不存在两步之间丢失变化的窗口。
- server-global `sequence` 由以下事件分配：playback 语义变化、成功的 AppState
  commit、engine 生命周期与 authorization 变化、server warning。纯进度插值不分配
  sequence。
- 命令先完成状态提交、分配 sequence 并进入各 watcher buffer，再发送 response。
  watcher 可能先于命令 caller 观察到事件，这是允许的：对 `playback.changed`，
  event 与 response 的 `state.sequence` MUST 相同；`state.changed` 携带带
  `revision` 的 AppState，不适用该规则。
- client 断线重连后 MUST 用新的初始快照替换本地状态，不得沿用旧连接的状态。

## 5. 慢 client 与溢出

- server MUST fan-out 给所有 watch client；一个慢 client 不得阻塞 helper socket
  reader 或其他 client。每个 watcher 使用独立有界 buffer（实现默认 64 个语义
  事件）。
- server MAY 合并尚未发送的**纯进度** event；如果**语义**事件仍溢出 buffer，
  server MUST 关闭该 watch 连接。client 重连并读取新快照恢复，不能继续使用可能
  缺事件的旧连接。
- server MAY 合并连续 playback progress event，但 MUST NOT 丢失 queue、track、
  status、shuffle、repeat 或 AppState 的语义变化。
- server 使用 helper 协议的 `actionEpoch` 只合并 compound 命令自身的中间通知；
  外部媒体键/系统事件具有独立因果标识，MUST 产生正常 `playback.changed`，不能被
  compound coalesce 吞掉。`actionEpoch` 是内部归因字段，不属于公共 watch schema。

## 6. Engine 与来源事件

- `authorization.changed.data.authorization` 是完整 `SourceAuthorization`；`flow` 可选，
  严格限制为 `{flowId,source,status}` 摘要，不得包含 interaction URL、device code、
  account 信息或 provider details。授权造成 capability availability 变化时，server MUST
  另发布 `sources.changed` 完整快照。

- helper transport 死亡时 server MUST 发布 `server.warning`，重建 helper，成功后
  发布 `engine.restarted` 与新的完整 playback state。server MUST NOT 自动重放
  超时或未确认成功的命令；重生后的播放状态是 `stopped`。
- 重建期间，依赖该 engine 的新命令返回 `engine_restarting`；不依赖它的命令
  （如 Radio Browser 查询）仍可服务。server 使用有上限的退避重建，并在每次
  availability 变化时更新 `sources.list` 结果、发布 `sources.changed`
  （`data` 是完整 `[SourceDescriptor]` 快照）。
- `session.shutdown` 开始后 MUST 取消重建并禁止再次启动 helper，避免 quit 触发
  意外重生。

## 7. client 行为

- TUI MAY 在两个权威快照之间本地插值 `position` 以获得平滑进度；MUST NOT 自行
  推测 queue、track 或 status。
- 除 `position` 显示外，任何 UI 状态都应来自最近的权威快照。
- client SHOULD 在收到 `server.warning` 后提示用户，并在 `engine.restarted` 后用
  新的 playback state 覆盖本地状态。
