# TUI 异步命令与状态一致性

本文定义 Bubble Tea 前端如何并发执行 Client API 命令、消费 `session.watch`，并保持本地 Model
与 server 权威状态一致。它是 UI 实现契约；wire 顺序、事件形状与 server 线性化分别以
[protocol.md](../client-api/protocol.md)、[watch.md](../client-api/watch.md) 和
[models.md](../client-api/models.md) 为准。

目标不是让 UI 猜测 server，而是让三类异步输入在同一个 `Update` 中确定性汇合：

```text
user intent ── tea.Cmd ── command result ──┐
session.watch ─────────── watch event ─────┼── Update ── Model ── View
timer / resize / input ─── local message ──┘
```

## 1. 所有权

| 状态 | 权威来源 | TUI 可做什么 |
|---|---|---|
| playback、queue、shuffle、repeat | `WatchSnapshot.playback` / `playback.changed` | 替换快照；仅在快照间插值 position |
| favorites、recent、theme、lastSource | `WatchSnapshot.state` / `state.changed` | 发起写命令；用新 AppState 替换本地投影 |
| source、capability、availability | `WatchSnapshot.sources` / `sources.changed` | 从完整 descriptor 快照派生可见动作 |
| authorization | 初始快照 / `authorization.changed` | 替换对应 source 的状态 |
| selection、offset、focus、overlay、filter | TUI Model | 在 `Update` 中同步修改，不持久化 |
| loading、feedback、operation in-flight | TUI Model | 表达工作流进度，不冒充 server 状态 |
| queue remove undo offer | server response 签发；TUI 仅暂存 token/revision/deadline | 显示短时 `u` 入口；不得缓存被删 item 后自行恢复 |
| render time、动画帧 | timer message | 存入 Model 后供 `View` 读取 |

**命令 response 不是第二份权威状态。** 它回答“这次请求是否完成”，watch 回答“系统现在是什么
状态”。两者可能以任意先后顺序到达；持久状态投影只能由初始快照和有序 watch event 写入。

## 2. 四种序号不可混用

| 名称 | 作用域 | 用途 |
|---|---|---|
| server `instanceId`（epoch） | 一个 server 进程 | 标出对端进程是否被替换；跨 epoch 不比较任何序号（TUI 目前靠无条件采纳快照达到同样效果） |
| watch `sequence` | 一个 server 进程（epoch 内），全事件共享 | 丢弃迟到或重复 event |
| `AppState.revision` | 持久状态 | 表示成功持久化的版本 |
| queue revision / playback state sequence | 播放与队列模型 | 乐观并发与播放快照关联 |
| UI operation ID / generation | 单个 TUI 进程 | 丢弃旧 command result 或旧页面加载结果 |

这些值都可能递增，但不在同一坐标系中。UI MUST NOT 比较不同种类的序号，也不能用 operation ID
推断 server 提交顺序。`sequence` 与 `queueRevision` 都是**进程内**计数器，所以重连时不能让新快照
与旧水位比较大小：当前 TUI 的做法是**无条件**采纳新快照（重置 `sequence`、清空 undo、重置
`appRevision`），这等价于丢弃所有按旧 epoch 建立的本地缓存，也覆盖了 epoch 切换的情况。
TUI 尚未显式比较 `snapshot.serverInstanceId`；显式判断属于未实施项，见
[`concurrency.md`](../internals/concurrency.md) §12.1。规则与 wire 字段见
[`watch.md`](../client-api/watch.md#2-初始快照)（单一权威位置）。

应用 watch event 的规则：

```text
if event.sequence <= model.lastWatchSequence:
    ignore
else:
    model.lastWatchSequence = event.sequence
    replace the event-owned projection
```

页面 discovery 等只读请求不产生 watch event，应使用 `generation + destination`：结果只有在 generation
仍是最新且目标页面仍匹配时才能应用。

## 3. `Update`、`tea.Cmd` 与 `View`

- `Update` MUST NOT 执行网络、磁盘或其他可能阻塞的 I/O；它只更新 Model 并返回 `tea.Cmd`。
- command closure 捕获的输入 MUST 是不可变快照。不得让异步 closure 继续读取或修改共享
  `*state.Store`、Model、slice 或 map。
- command MUST 返回 typed message；错误也是 message，不得在 goroutine 中直接改 Model。
- `View` 只从 Model 渲染：不发 I/O、不读取 wall clock、不修改 package-global theme/style。
- tick message 携带时间并写入 Model；不同 TUI model 的 renderer/theme 互不影响。

示例：收藏命令可捕获 `{operationID, source, ref, desired}`，但不能捕获 store 指针后在 command 中
调用 `ToggleFavorite`。成功 response 只结束 operation；随后到达的 `state.changed` 才替换 favorites。

## 4. Mutation 串行，查询可并发

server 会串行提交有副作用命令，但多个 client command 的**到达顺序**不等于同一 UI 的手势顺序。
因此 TUI 自己必须维护一个 mutation in-flight 槽：

- playback、queue、favorite、持久设置和 source switch 共用该槽；
- 所有入口都检查它，包括键盘、鼠标、palette 和内部 helper，不只检查某几个 key binding；
- operation 完成前拒绝新的 mutation，并给出稳定反馈；
- 有依赖的步骤只从前一步的 typed result 启动，不能放进无顺序保证的 `tea.Batch`；
- result 携带 operation ID；不匹配当前 operation 的迟到结果必须忽略。

只读、互不依赖的 discovery 请求 MAY 并发；同一页面的刷新与分页仍用 generation 隔离陈旧结果。

### Source switch 示例

```text
validate latest descriptor
  -> reserve operation
  -> stop active playback (only when needed)
  -> snapshot and tentatively switch navigation
  -> persist lastSource
  -> load target Home and close overlay
  -> release operation
```

目标不可用时不得 stop。stop 失败时不迁移。持久化失败时恢复完整导航快照；已经成功的 stop 不自动
重放。每一步只由前一步的 result 推进，Esc 或鼠标路径不能绕过 operation ownership。

## 5. Response 与 watch 的竞态

server 在发送 command response 前，已经提交状态、分配 sequence 并把 event 放入 watcher buffer；
因此以下顺序是正常的：

```text
TUI sends favorites.set(A=true)
watch receives state.changed(revision=12, A=true)
another client commits state.changed(revision=13, A=false)
TUI receives the original command success
```

最终值 MUST 是 `A=false`。如果 success handler 再本地 toggle A，就会覆盖更新的权威状态。

所以：

- mutation response 只推进/结束 workflow、显示错误或反馈；
- `state.changed` 是 AppState 投影的唯一增量 writer；
- `playback.changed` 是播放投影的唯一增量 writer；命令返回的 playback 可用于关联结果，但不得覆盖
  已应用的更高 watch sequence；
- optimistic UI 只能用于明确的 transient preview，并必须可回滚；不能伪装成已提交状态。
- `queue.remove` response 的 Undo offer 是 workflow capability，不是第二份 queue state。只有 response 的
  `state.queueRevision` 仍等于已应用的 watch 投影时才能保存；后续 watch revision 不同即清除。expiry
  message 必须带 token，旧 offer 的 timer 不能清掉连续删除产生的新 offer。Undo 仍占用同一个 mutation
  slot，失败不在本地插回条目。

## 6. 启动、断线与 engine restart

1. `session.watch` 的原子初始快照建立基线 S；先用它一次性初始化 playback、AppState、sources 与
   authorizations，再处理 `sequence > S` 的 event。
2. 初始快照已有的数据不得再发无版本 fallback read；否则旧 response 可能覆盖新的 watch event。
   测试/非生产 feed 缺字段时 MAY fallback，但请求必须记录起始 sequence，结果迟到时丢弃。
3. watch 关闭表示可能丢失事件：停止 position 插值、标记 disconnected、清理会话探测状态，并重新
   连接。新连接的初始快照整体替换旧投影，不能接着使用旧 sequence；快照同时给出新的
   `serverInstanceId`，因此不能把旧水位、undo 与 token 带过来。
4. `engine.restarted` 是生命周期事件，不是独立读取状态的许可。TUI 等待同一有序 watch 流随后给出的
   完整 playback/source event；不得用多个无版本 RPC read 拼接“快照”并覆盖更新事件。
5. 启动快照若显示另一 source 正在活动，浏览 source 对齐该 source；这是本地导航调整，不触发 stop。

## 7. 测试契约

测试必须验证真实竞态，而不只是按期望顺序手工投递 message：

- 阻塞第一个 fake mutation，确认第二个键盘/鼠标入口没有调用 server；
- 让 watch event 先于 command response，确认迟到 response 不回写权威投影；
- 交错新 `state.changed` 与旧 persistence success，最终状态取较新 revision；
- 在 engine restart 后先应用新 playback event，再投递任何旧读取结果，确认状态不回退；
- 关闭 watch 后确认 position 不再插值，重连快照完整替换旧状态；
- source switch 的 stop、persist、load 严格逐步发生，失败恢复可操作的原页面；
- 异步查询开始后修改 Model/store，结果仍只使用启动时捕获的不可变快照；
- 两个不同 theme 的 Model 并行渲染，输出互不污染且同一 Model 重复 `View` 稳定。

单元测试 MAY 直接注入 typed message 验证 reducer，但 mutation 顺序与 watch 重连至少要有一层
hermetic fake transport/server 测试，覆盖 command 实际启动时机。

## Links

- [UI 模型与导航](model.md) — 状态所有权、Source/Surface 与生命周期
- [设计系统](design-system.md) — 状态在界面中的呈现位置
- [Client API 并发](../client-api/protocol.md#2-并发) — server command 线性化
- [watch 顺序与一致性](../client-api/watch.md#4-顺序与一致性) — 原子快照、sequence 与重连
- [Client API 模型](../client-api/models.md) — PlaybackState、AppState 与 WatchSnapshot
