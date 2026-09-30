# Client API —— 传输与并发

本文件定义 client 如何连接 server、请求与响应的线格式，以及超时、幂等和重试
规则。数据形状见 [`models.md`](models.md)，命令清单见 [`commands.md`](commands.md)。

## 1. 传输

### 1.1 Unix socket

- 默认路径由 [`../internals/persistence/state.md`](../internals/persistence/state.md#路径) 决定；`LILT_SOCKET`
  仅供测试/显式覆盖。
- server 为它创建的 socket 目录 MUST 为 `0700`（显式 `LILT_SOCKET` 指向的既有目录不 chmod）；
  socket 本身 MUST 为 `0600`。
- 普通命令：每个连接发送一个 NDJSON request，收到一个 response 后关闭。
- `session.watch` 使用长连接：先返回一个 response，再持续发送 NDJSON event
  （见 [`watch.md`](watch.md)）。
- 本接口不是 helper 的 JSON-RPC。不得把
  [`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md) 的 method 名直接当作
  Client API 命令。

### 1.2 Request

```json
{
  "requestId": "01K5C7V5M3ZTQ2QY8Y4ZQ0DV2R",
  "command": "playback.play",
  "params": {
    "ref": "apple-music:playlist:pl.317c99a2e9a44527a4160bebe2678daa",
    "shuffle": true,
    "repeat": "all"
  }
}
```

| 字段 | 类型 | 规则 |
|---|---|---|
| `requestId` | string | client 生成的不透明唯一值；用于响应关联与副作用命令去重 |
| `command` | string | 注册表中的命令名 |
| `params` | object | 无参数时可省略；未知字段 MUST 返回 `invalid_request`（schema 关闭，见 §3） |

### 1.3 Response

成功：

```json
{"ok":true,"requestId":"01K5C7V5M3ZTQ2QY8Y4ZQ0DV2R","data":{}}
```

失败：

```json
{
  "ok": false,
  "requestId": "01K5C7V5M3ZTQ2QY8Y4ZQ0DV2R",
  "error": {
    "code": "authorization_required",
    "message": "Apple Music is not available",
    "details": {"source": "apple-music"}
  }
}
```

- `error.details` 可省略。错误消息面向用户；稳定判断 MUST 使用 `error.code`
  （见 [`errors.md`](errors.md)）。

## 2. 并发

- server MUST 串行执行所有有副作用的命令；只读 discovery MAY 并发，但同一
  MusicKit helper 的调用仍受其串行约束。
- command 的提交顺序即其他 client 观察到的顺序：server 先完成状态提交、分配
  sequence 并写入各 watcher buffer，再发送 response。watcher 可能先于 caller
  观察到事件，这是允许的。对 `playback.changed`，event 与 response 的
  `state.sequence` MUST 相同；`state.changed` 携带的是带 `revision` 的 AppState，
  不适用该规则。

## 3. 请求去重与重试

- 每个 request MUST 带唯一 `requestId`。server MUST 在执行前原子注册 in-flight
  request。重复比较的是**校验并按键名排序归一化后的** `{command, params}` 指纹，不是
  原始 JSON；字段顺序不得造成不同指纹（schema 目前不声明默认值，因此省略字段与显式
  传值仍是不同指纹）。command params schema MUST 设置 `additionalProperties:false`；
  未知字段返回 `invalid_request`，不得静默忽略。
- 相同 `requestId` 与相同指纹必须等待并复用同一结果；相同 id、不同指纹返回
  `invalid_request`。完成结果 body cache MAY 限为最近 4096 条，但每个完成请求的
  id/指纹 tombstone MUST 至少保留 10 分钟。
- 若结果 body 已逐出而 tombstone 仍在，重复请求 MUST NOT 重执行，而返回稳定的
  `duplicate_result_unavailable`。响应丢失时 client 应先用原 requestId 重试：body 仍在
  cache 时可取得原结果；收到此错误则说明结果已不可恢复，必须先读状态，再由用户或
  确定性策略决定是否用新 requestId 发起新操作。
- server 重启会清空内存中的去重记录。重启后 client MUST NOT 自动重放结果未知的非幂等
  命令（尤其 `queue.add` 与播放启动）。
- 幂等性：
  - `playback.pause`、`playback.stop`、`queue.clear` MUST 对当前状态幂等。
  - `favorites.set` 幂等；响应丢失后可以重试同一目标值。
  - `favorites.toggle` 类“翻转”语义不使用；需要明确目标状态，避免重试把状态
    翻回去。

## 4. 超时预算

| 类别 | 预算 |
|---|---:|
| 播放启动（`playback.play`、`playback.playSongs`） | 60s |
| `queue.add` | 30s |
| 其余播放控制与队列编辑、状态读写 | 5s（`pause` 8s、`toggle`/`resume`/`next`/`previous` 15s、`queue.jump` 20s） |
| 内容发现（`discovery.*`、`playlist.tracks`、`library.*`） | 45s |
| Radio Browser 查询与探测 | 15s |
| `api.describe` | 1s |
| 授权 begin/status/cancel/disconnect | 5–10s |

有限 URL 队列的**自动换曲与媒体失败重试**（含 stall 看门狗触发）也 MUST 有执行预算：
共用 `playback.next` 的 15s，覆盖目标项解析与起播。预算耗尽后，以独立的 `playback.stop`
5s 预算尽力停止旧音频，清空队列、提交 stopped，并按播放中解析失败发布
`source_unavailable` 警告（journal + watch）。迟到的解析结果 MUST NOT 起播；迟到的起播结果
MUST NOT 提交为成功。控制命令仍按串行顺序排队，不会立即抢占自动操作；单次超时处理占用
控制通道最多 15s + 5s（resolver 与 driver 必须响应 context 取消）。清理音频是尽力操作，
不保证故障后端实际停止。

`session.shutdown` 的 5s 仅计开始执行后的关闭工作；它在命令队列中的等待不计入。
client/CLI SHOULD 为它等待“此前已接受命令的最大预算 + 5s”，不得仅因队列等待超过
5s 就断言 `operation_outcome_unknown`。

CLI 的等待时间 SHOULD 至少比对应预算长（例如播放启动 90s），以便观察到 server
的完整处理结果。

## 5. helper 超时与 engine 重建

MusicKit 调用超时会永久作废当前 helper transport（原因见
[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)）。server 的处理规则：

- 隔离旧 helper、拒绝迟到结果，并自动重建一个新的 helper 实例；不得让 Client
  API socket 保持假活。
- 触发超时的原命令返回 `operation_outcome_unknown` 并缓存该结果。client MUST NOT
  用新 requestId 自动重试；应等待 engine 重生后查询 playback/queue 状态，再由
  用户或确定性策略决定是否执行新命令。
- 重建期间，依赖该 engine 的新命令返回 `engine_restarting`（确定未执行，可稍后
  重试）；不依赖它的命令（例如 Radio Browser 目录查询）仍可服务。
- 重生后的播放状态是 `stopped`；server MUST NOT 自动重放超时或未确认的命令。
- availability 变化时更新 `sources.list` 并发布 `sources.changed`
  （见 [`watch.md`](watch.md)）。

## 6. 关闭服务线性化

`session.shutdown`（CLI：`lilt quit`）的执行顺序：

1. 在线性化点立即停止接受新的**有副作用**命令（返回 `session_unavailable`），并等待
   此前已接受者完成；只读请求可完成到 listener 关闭。
2. 此后 5s 执行预算内停止音频并释放播放引擎（含 helper）、持久化已提交状态、取消 helper 重建。
   同时取消所有 pending authorization flow。
3. 关闭/保存失败以 `server.warning` 发布；尽力停止/关闭仍继续。
4. 发布一次 `server.shuttingDown`，回复 caller，**之后**才关闭 listener 与 watch。回复先于
   listener/进程退出，caller 不会收到被截断的响应。

CLI `lilt quit` 在 server 已不存在时 MAY 作为便利行为返回成功；wire 层没有可
响应的 server，因此不能把这种情况称为 RPC 幂等成功。
