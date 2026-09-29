# Client API —— Watch（实时事件）

`session.watch` 让 client 持续接收 server 的状态变化。TUI 用它同步播放状态、
队列与收藏；状态栏、移动端等 client 只订阅自己关心的 topic。

协议见 [`protocol.md`](protocol.md)；状态形状见 [`models.md`](models.md)。

## 1. 建立订阅

```json
{"requestId":"...","command":"session.watch","params":{"includeState":true}}
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
  "data": {
    "sequence": 42,
    "playback": { /* 完整 PlaybackState，含 queue */ },
    "state": { /* AppState；请求 includeState 时存在 */ },
    "sources": [ /* 订阅 sources 或 topics 缺省时存在 */ ],
    "authorizations": [ /* 订阅 authorization 或 topics 缺省时存在 */ ],
    "warning": { "code": "storage_unavailable", "message": "..." } /* 仅当 Activity store 不可用时存在 */
  }
}
```

`warning` 是持久条件的只读投影，client 应在恢复界面持续可见；它不随事件重复推送（重连后的新
快照会再次携带）。

## 3. 事件行

```json
{"event":"playback.changed","sequence":43,"data":{"state":{}}}
{"event":"state.changed","sequence":44,"data":{"state":{}}}
{"event":"engine.restarted","sequence":45,"data":{"source":"apple-music"}}
{"event":"sources.changed","sequence":46,"data":{"sources":[]}}
{"event":"authorization.changed","sequence":47,"data":{"authorization":{},"flow":{}}}
{"event":"server.warning","sequence":48,"data":{"code":"...","message":"..."}}
{"event":"server.shuttingDown","sequence":49,"data":{}}
```

`server.warning` 与 `server.shuttingDown` MUST 始终送达且不受 `topics` 过滤，避免
client 在关键变化上失联。`server.warning` 的 `code` 采用
[errors.md](errors.md) 的稳定 code；同时 MUST 写入 journal（`lilt log` 可见，kind
`server.warning`），以便没有活跃 client 时也能解释播放为何终止。URL 队列的媒体流
失败/停滞（helper 报错或 20s stall 看门狗）先在 journal 记一条 `playback_stalled`
（仅日志，不发布，等待重试结果）；重解析成功则继续播放。二次失败时死项被跳过并发
布 `playback_skipped`（队列推进，播放继续；连续上限 2 个），连续第 3 个死项或最后
一项死链才发布 `playback_error`（源本身可用，是媒体停滞）；重解析阶段的上游失败发布
`source_unavailable`。

## 4. 顺序与一致性

- watch 注册与初始快照 MUST 有一个**原子线性化点**：快照序号为 S，注册完成后
  只向该连接发送 `sequence > S` 的 event，不得漏掉两者之间的变化。来源与授权投影
  优先在命令锁外读取；读取期间若 sequence 改变则重取，避免旧来源数据被标为新的快照序号。
  连续三次重取仍遇变化时返回 `session_unavailable`，client 稍后重新建立订阅；不得在播放命令锁内读取慢 provider。
- 每条语义 event（包括 `sources.changed` 与 `server.warning`）各占一个新序号；同一次
  跳过死曲先发布队列推进、再发布跳过警告，后者的 sequence 必须更大。
- 初始 `sources` 与 `authorizations` 是各自 topic 的完整权威快照；client 不需要先
  list 再 subscribe，因此不存在两步之间丢失变化的窗口。
- server-global `sequence` 由以下事件分配：playback 语义变化、成功的 AppState
  commit、engine 生命周期与 authorization 变化、server warning。纯进度插值不分配
  sequence。
- `sources.changed` 与 `authorization.changed` 是**快照型**事件：server 按事件 wire
  data 的规范化内容去重，与上一次已发布快照逐字节相同的完整快照 MUST NOT 再次发布，
  也 MUST NOT 消耗 sequence；内容变化才发布一次。每个事件的首次发布总是发出，用于
  纠正过早订阅的客户端（warm-up）。带发生次数/因果语义的事件（`server.warning`、
  `engine.restarted`、playback/state 变化）MUST NOT 套用此去重。
- 命令先完成状态提交、分配 sequence 并进入各 watcher buffer，再发送 response。
  watcher 可能先于命令 caller 观察到事件，这是允许的：对 `playback.changed`，
  event 与 response 的 `state.sequence` MUST 相同；`state.changed` 携带带
  `revision` 的 AppState，不适用该规则。
- client 断线重连后 MUST 用新的初始快照替换本地状态，不得沿用旧连接的状态。初始快照尚未返回时取消或到达 context deadline，也必须及时关闭连接并结束等待。

## 5. 慢 client 与溢出

- server MUST fan-out 给所有 watch client；一个慢 client 不得阻塞 helper socket
  reader 或其他 client。每个 watcher 使用独立有界 buffer（实现默认 64 个语义
  事件）。
- server MAY 合并尚未发送的**纯进度** event；如果**语义**事件仍溢出 buffer，
  server MUST 关闭该 watch 连接。client 重连并读取新快照恢复，不能继续使用可能
  缺事件的旧连接。
- server MAY 合并连续 playback progress event，但 MUST NOT 丢失 queue、track、
  status、shuffle、repeat 或 AppState 的语义变化。
- server 使用 helper instance、`playbackGeneration` 和不可复用 `transportSessionID` 丢弃
  superseded playback session 的通知；外部媒体键/系统事件带 observer 捕获的 generation/session，MUST
  产生正常 `playback.changed`，不能被丢弃。它们是内部字段，不属于公共 watch schema；`url` mode 会回显它们。

## 6. Engine 与来源事件

- `authorization.changed.data.authorization` 是完整 `SourceAuthorization`；`flow` 可选，
  严格限制为 `{flowId,source,status}` 摘要，不得包含 interaction URL、device code、
  account 信息或 provider details。授权造成 capability availability 变化时（即 descriptor
  快照内容确实变化时），server MUST 另发布 `sources.changed` 完整快照。
- Apple 资源 runtime 是惰性启动的：它不可用时 descriptor 报告 degraded（full/queue/shuffle/repeat
  不可用），可用后 helper 的账户能力仍异步结算（`authorization.status` 先报 "still being read"）。
  因此 runtime 就绪与结算完成各是一次 capability transition，server MUST 各发布一次
  `sources.changed` 完整快照；runtime 失效且重建失败时同样 MUST 发布。没有这些事件，
  watch client 会把启动时拿到的降级 capability 快照用满整个会话（usability batch
  2026-09-21-r13：帮助与底栏直到重启才出现 shuffle/queue 提示）。

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
