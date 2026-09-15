# Feature · 广播健康探测（radioProbe）

依据 [`docs/spec/radio-discovery-health.md`](../../spec/radio-discovery-health.md) 实现。

## 交付内容

| 层 | 内容 |
|---|---|
| Helper | 新 RPC `radioProbe`（`{url, timeoutMs}` → `{status, latencyMs?, errorCode?, message?}`）；独立静音 `AVPlayer` + `AVPlayerItem` 轮询 `readyToPlay`，6s 内部超时以普通结果返回；在 process 循环中用 detached Task 应答，绝不阻塞 `play`/`pause`/`radioPlay`/`shutdown`；不触碰任何 playback-owned 状态、不 `play()`、不发声 |
| 错误分类 | `probeErrorCode(domain:code:)` 纯函数（放 LiltPlayerLogic，可测）：`tls`/`timeout`/`network`/`unsupported`/`unknown` |
| Go client | `core.RadioProbeResult` + `(*Client).Probe(ctx, url, timeoutMs)`；TUI 用 10s 上下文（> helper 6s），避免常规 RPC 超时作废 transport |
| TUI 调度 | 进程内 `probes` 状态机（unchecked → queued → checking → healthy/failed，终态缓存不重复探测）；只调度当前可见 `stream`/`station`（选中项优先、自上而下）；全局并发上限 2；滚动/换页/加载/窗口变化时重算；页面切换丢弃旧队列但保留缓存；断连时清除在途项 |
| 渲染 | 行内状态段（颜色+符号+文字）：`○ unchecked`/`○ queued`、`◌ checking…`、`● <ms>ms`、`× TLS error` 等；仅改行内容，不重排、不动光标 |
| 交互 | 失败项仍可 `Enter`/`p` 播放并提示重试原因；流 10s 未进入播放显示 `LIVE · error`；buffering 时 `Space`/`c` 暂停 |

## 实现过程中被真机否决的三个方案（留档）

1. `AVPlayerItem` 脱离 `AVPlayer`：`status` 永远不到 `readyToPlay` → 全部误报 `timeout`。
2. `AVURLAsset.load(.isPlayable)`：部分 URL 不触网即返回 true → 0–3ms 假阳性。
3. `AVURLAsset.load(.tracks)`：直播流返回空 tracks → 全部误报 `unsupported`。

另有一个并发陷阱：`withTaskGroup` 在返回前等待被取消的子任务，而 `asset.load` 不响应取消 →
单次探测曾超过 10s，触发 Go 超时并**作废整个私有 transport**（规格第 274 条明确禁止）。
最终以独立静音 player + 自有轮询实现，从根本上保证 6s 上限。

## 验证

- Go：`TestRadioProbeSchedulesVisibleWithTwoWorkers`、`TestRadioProbeTerminalStatesAreCachedAndDoNotReorder`、
  `TestRadioProbeRecordsFailureCodes`、`TestRadioProbeRenderingStates`、`TestRadioProbeScopeSwitchDropsQueue`、
  `TestRadioProbeDisconnectClearsInFlight`、`TestRadioProbeFailureWarnsBeforeRetry`、`TestSpacePausesWhileBuffering`。
- Swift：`testProbeErrorCodeClassifiesTransportFailures`（7 项全绿）。
- 真机 120x32：延迟 3.8–6.0s 真实、`× timeout` 正确、无 RPC 失败、并发 2、探测期间
  Now Playing 不变且 Recent 未写入、播放/暂停/切页/退出全程流畅。
- 用户复验 Round 20：7/10，价值确认；4 项改进已即时修复。

## 未实现（仍为 Future Work）

- 探测结果持久化/TTL、click counter 上报、动态镜像发现（SRV）、健康优先排序或隐藏失败台、
  `n` 跳到下一个健康台。

## 线上问题与修复：切换页面后队列永久停滞（用户报告）

- **现象**：用户日志显示探测只在按键/切页时成对启动；停在同一页面时，除前 2 个外其余行永久停留在
  `○ queued`（RPC 日志无失败、无 transport 异常）。
- **根因**：`scheduleProbes` 在页面作用域变化时清空 `probeQueue`，但被丢弃的条目仍留在
  `m.probes` 里标记为 `queued`。它们既不在队列中，又因「已知」而不再入队 → 永久停滞。
  用真实 state（favorites=2、recent=20）复现：`Enter` 播放 + `2→3→2` 快速切页后仅 4 次探测，
  其余 7 行卡死。
- **修复**：丢弃队列时同步删除这些 key（恢复 `unchecked`），使再次进入该页面时可重新入队；
  规格 Scheduling 一节补充该约束。补回归测试
  `TestRadioProbeDroppedQueueBecomesEligibleAgain`。
- **验证**：同一复刻序列下 11/11 行全部得到终态；`just verify` 全绿。
- **附带**：新增 `probe` 调度日志（`event=schedule|start|done|paused`，带 queue/active 计数，
  URL 按 scheme://host/path 脱敏）——正是本次定位所依赖的可观测性，保留为长期诊断能力。
