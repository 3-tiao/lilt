# Round 20 · 想避开坏台的电台用户（健康探测验证）

- 日期：2026-09-15
- 终端：120x32，隔离 state/socket/log（/tmp/usability-r20*）
- 构建：工作区（radioProbe 新实现）
- 评分：7/10
- 目的：验证广播健康探测的显示与交互
- 结果：功能价值被确认；4 条改进项已即时修复

## Agent 报告（原文）

# Round 20 报告：想避开坏台的电台用户

## 时间线
1. 启动在 Apple Music Home。
2. `?` 帮助确认 `Tab` 切来源、`3` 切 Browse、`Enter` 播放。
3. `Tab`、`3` → Radio Browse，先 `loading…`。
4. 约 8 秒后出现 `● checking…`、`○ queued`、`● timeout`。第一感受：确实在帮我排坏台。
5. 再约 40 秒后 20 行都有结果：`● 4915ms`、`● timeout`、`● TLS error`。
6. `Down Down Enter` 选 `Europe 1 — ● 4915ms`：`LIVE · buffering` → 约 5 秒后 `LIVE · playing`；`Space` → `LIVE · paused`。
7. `Up Enter` 选失败台 `France Info — ● timeout`：未被阻止，显示 `LIVE · buffering`；约 20 秒仍是 buffering，无失败提示。
8. buffering 时 `Space`：仍显示 buffering，暂停意图无反馈。
9. `1`、`3` 切换即时；`G` 到底部 `Dance Wave! ● 3391ms`，未见卡顿。
10. `v` → `Stopped`；`q` 正常退出。

## 状态标记可理解性
- `○ queued` / `○ unchecked`：已排队 / 尚未检查。
- `● checking…`：正在检查。
- `● 4915ms`：检查成功可连通（也提示启动偏慢）。
- `● timeout` / `● TLS error`：检查失败及原因。
- 文字足以区分；但成功与失败同用 `●`，只看符号不够直观。

## 失败电台的播放尝试
允许尝试，Now Playing 显示 `France Info` / `LIVE · buffering · live stream`，20 秒仍无失败说明，容易以为应用卡住。

## 性能与流畅度
- 探测期间列表可见、状态逐步更新，无卡顿；播放/暂停顺畅；切换视图与跳转即时；退出正常。
- 失败台 buffering 时切视图仍流畅，但 `Space` 无明确变化。

## 发现的问题
1. **中：失败台播放后长时间 buffering，无失败结果。**（`Tab → 3 → 等待 → 选 timeout 台 → Enter → 20s`）
2. **中：已标记 timeout 的台可播放但缺少风险提示。**
3. **低：成功与失败都用 `●`，纯符号扫读不直观。**
4. **低：buffering 时按暂停无可见反馈。**

## 好的方面
- 状态直接放在电台名后，对「避开坏台」很有价值。
- `timeout`/`TLS error` 原因明确；毫秒数可用于优先选快台。
- 状态与实际播放基本对应；后台探测不妨碍浏览/切视图/退出。

## 改进建议
- 成功/失败使用不同字符（`✓`、`×`、`…`）。
- 播放失败台给出确认或醒目提示。
- 为 buffering 增加明确超时与失败原因。
- buffering 下暂停/停止反馈明确。
- 可选按可播放优先排序或隐藏失败台。

## 总体可用性评分（1-10）与一句话结论
**7/10。** 状态标记确实能快速帮我避开坏台，但点击已失败电台后长期卡在 buffering、没有解释，削弱了这项功能的可信度。

## 编排者复核与即时修复

| 问题 | 处置 |
|---|---|
| #1 buffering 无失败结果 | helper 增加流启动看门狗：非暂停、非 playing 超过 10 秒即置 `playbackError`，Now Playing 显示 `LIVE · error` + 明确文案；实测 DNS 失败流会更快经 AVPlayerItem KVO 直接报错 |
| #2 失败台重试无提示 | 播放缓存状态为 failed 的电台时，toast 提示 `Retrying <name> — earlier probe failed (<reason>)`；播放仍被允许（符合规格） |
| #3 成功/失败同符号 | 改为 `○`/`◌`/`●`/`×` 四态区分，文字不变；规格 UI Contract 表同步更新 |
| #4 buffering 下 Space 无反馈 | TUI 的 `Space`/`c` 在 buffering 时改为暂停（原先把 buffering 归入 resume 分支）；补测试 `TestSpacePausesWhileBuffering` |

补充验证：
- 真机 120x32 探测实测：真实延迟 3.8–6.0s、`× timeout` 分类正确、全程无 RPC 失败（transport 未再被探测超时作废）、并发 2 生效、探测期间 Now Playing 保持 `Nothing playing` 且未写入 Recent。
- 关键实现教训：`AVPlayerItem` 未绑定 player 不加载（全部误报 timeout）；`AVURLAsset.load(.isPlayable)` 会假阳性（0–3ms）；`load(.tracks)` 对直播返回空（全部误报 unsupported）；`withTaskGroup` 会等待不响应取消的加载（曾导致 10s 超时作废 transport）。最终采用「独立静音 AVPlayer + 轮询 readyToPlay」，完全符合规格第 4/274 条。