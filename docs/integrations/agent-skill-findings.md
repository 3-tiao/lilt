# AI Skill 使用问题记录

本文件记录按 [`agent-skill.md`](agent-skill.md) / 仓库 skill
（[`../../skills/lilt/SKILL.md`](../../skills/lilt/SKILL.md)）操作 lilt 时遇到的真实问题、
最小复现与结论。它面向 skill / agent 集成的维护者，是**问题台账**而非运行报告；每条问题的
长期结论仍应落到对应权威文档或代码修复。

> 观察环境：2026-09-19，commit `33bc5b2`，macOS；用仓库根 `./lilt`（`just build-go` 产物），
> **未**设置 `LILT_PLAYER_PATH` / `LILT_AUDIO_PATH`。会话目标：用 Audius 播放中文歌。
> 期间同机另有并行 harness（tmux `./lilt tui`、`./lilt serve --fake`、多个 server/helper），
> 相关观察按「装置噪声」单独标注，见第 5 节。

## 摘要

| # | 问题 | 严重度 | 状态 |
|---|---|---|---|
| 1 | skill 假设 `lilt` 在 PATH，仓库内只有 `./lilt` | 高 | open |
| 2 | `play --shuffle --repeat` 对不支持该能力的 source 静默成功 | 高 | open（疑似缺陷） |
| 3 | skill / agent-skill 未标注 shuffle/repeat 的来源能力约束 | 中 | open |
| 4 | 已安装 skill 落后于仓库版本 | 低 | open |
| 5 | Audius URL 播放可永久卡在 `buffering`，无错误、无超时 | 高 | open（疑似缺陷，待隔离复现） |

严重度按「发生频率 × 影响 × 持久性」衡量；1、2、5 在核心路径上稳定复现。

## 1. skill 假设 `lilt` 在 PATH，仓库内只有 `./lilt`（高）

**症状**：按 skill 直接执行 `lilt sources --json`、`lilt search ...` 全部返回
`zsh: command not found: lilt`。

**原因**：`just build-go` 把二进制输出到仓库根 `<root>/lilt`（见 `Justfile` 的
`binary := root / "lilt"`），且 `just` 包装的调用会注入本机签名 helper 路径
（`LILT_PLAYER_PATH` / `LILT_AUDIO_PATH`）。仓库内的 agent 既不满足 PATH 假设，也不满足
helper 环境假设。

**复现**：任意干净 shell 中运行 `lilt sources --json`；改用 `./lilt sources --json` 成功。
本次 Audius 播放用 `./lilt` 直接成功（Audius 走 URL 队列，未验证 Apple Music helper 路径）。

**涉及**：[`agent-skill.md`](agent-skill.md) §1「接入形态」与 skill 的示例都直接写 `lilt`。

**待办**：在 agent 接入指南 / skill 中说明仓库内调用方式（`./lilt`；Apple Music 需经
`just` 注入 helper 路径），或提供安装到 PATH 的步骤。这属于安装/环境问题，不是 server 缺陷。

## 2. `play --shuffle --repeat` 对不支持该能力的 source 静默成功（高，疑似缺陷）

**症状**：对 Audius 传启动期形态参数时命令报成功，但形态并未生效，也没有任何错误或降级提示。

**复现**：

```sh
./lilt play audius:playlist:e4Ep0 --shuffle --repeat all --json
# -> {"ok":true,...,"shuffle":false,"repeatMode":"off"}   # 参数被忽略，仍返回 ok

./lilt shuffle on --json   # -> unsupported_command: "audius does not support shuffle"
./lilt repeat all --json   # -> unsupported_command: "audius does not support repeat"
```

即：单独调用形态命令会如实报 `unsupported_command`，但 `play` 的组合形态参数被静默丢弃。

**涉及规范**：

- [`../client-api/models.md`](../client-api/models.md)：Audius 只声明 `playback.full`、`queue`
  等 capability，**没有** `shuffle` / `repeat`。
- [`../client-api/commands.md`](../client-api/commands.md)：source 未声明支持的操作返回
  `unsupported_command`（能力缺失），且 `play --shuffle --repeat all` 是一个逻辑命令——播放成功
  但形态设置失败时应返回 `partial_failure`，`error.details` 含 `state` 与 `applied`。

**结论**：当前行为与规范不符，属**静默降级**，违反「capability 是唯一真值、不静默降级」的
provider 约定。影响：agent 会向用户谎报 shuffle/repeat 已开启，且无法据此触发换源或降级。

**待办**：在 server/CLI 让 `play` 对未声明 `shuffle`/`repeat` 的 source 返回
`partial_failure`（首选，保留已开始的音频）或 `unsupported_command`；补 Go 回归测试。

## 3. skill / agent-skill 未标注 shuffle/repeat 的来源能力约束（中）

**症状**：文档把 `--shuffle` / `--repeat` 写成通用播放参数，未提示它们依赖来源 capability，
直接导致问题 2 被触发。

**位置**：

- [`../../skills/lilt/SKILL.md`](../../skills/lilt/SKILL.md)：`play` 速查表与「循环 / shuffle」
  recipe 无差别使用 `--shuffle --repeat all`；「播放〈艺人〉的歌」也直接对任意来源套用。
- [`agent-skill.md`](agent-skill.md) §4「循环播放」同样不带来源限定。

**待办**：在 skill 与接入指南中标明 shuffle/repeat 目前仅 Apple Music 声明，Audius/radio
不具备；或让配方先读 `lilt sources --json` 的对应 capability 再决定是否传参。

## 4. 已安装 skill 落后于仓库版本（低）

**症状**：`~/.config/opencode/skills/lilt/SKILL.md` 与 `skills/lilt/SKILL.md` 不一致。仓库版本
的 `library` 已补「Audius 需先连接账号」，安装副本没有。

**复现**：`diff skills/lilt/SKILL.md "$HOME/.config/opencode/skills/lilt/SKILL.md"`。

**待办**：修改 skill 后运行 `just agent-install`（见 `Justfile` 的 `agent-install`）。

## 5. Audius URL 播放可永久卡在 `buffering`，无错误、无超时（高，疑似缺陷，待隔离复现）

**症状**：播放 Audius 歌单，第 1 首正常播放；自然结束后自动切到第 2 首，第 2 首永久停在
`buffering`、`position` 冻结在 0、`playbackError` 为 `null`、`sequence` 不再增长，没有错误、
没有重试、队列也不推进。

**本次会话证据**（日志为 UTC，下列为本地时间）：

- 22:39:50 起播歌单第 1 首 `audius:song:NP9Rx`，正常进入 `playing`。
- 22:43:19 第 1 首自然结束，server 自动对第 2 首 `audius:song:Pbp75` 发起 `urlPlay`，该 RPC 返回
  `ok`（耗时约 5.7s）。
- 之后连续 `lilt status` 轮询：始终 `buffering`、`position=0`、`playbackError=null`、`sequence`
  冻结。
- 再单独 `play audius:song:Pbp75`（新 server/helper，sequence 重置）**同样**永久 `buffering`。
  即不是"切歌瞬间"专有，而是该 track 解析出的 URL 在 AVPlayer 上无法进入 `playing`。

**代码路径分析**：

- helper `player/Sources/LiltHelperKit/LiltHelperKit.swift` 的 `AudioService.state()`：`status`
  仅由 `AVPlayer.timeControlStatus` 映射（`.playing`→`playing`，
  `.waitingToPlayAtSpecifiedRate`→`buffering`，其余→`paused`）；`playbackError` 恒为 `nil`。
- 该 `AudioService` **没有**观察 `AVPlayerItem.status == .failed`，也没有
  `.AVPlayerItemFailedToPlayToEndTime`，因此条目加载失败/卡死时只能一直报 `buffering`。对照
  `player/Sources/LiltPlayer/LiltPlayer.swift`（约 425–446 行，MusicKit/preview 路径）有 item
  status / failure 观察并会写 `playbackError`。
- server `internal/server/engine_supervisor.go`（约 72 行）只在 `update.State.Error != ""` 时调用
  `RetryCurrent`；URL 播放的错误永远为空，所以没有重试路径。
- `internal/server/playback_transport.go` 的 `AdvanceEnded`/`playCurrentLocked` 只是把 helper 返回
  的 `buffering` 当最终状态提交，之后完全依赖 helper 后续事件；helper 不再发事件就永久卡住。
  相比之下 Apple Music 起播路径（`internal/server/handlers_playback.go` 约 201–210 行）有显式
  "等待真正 playing"的轮询，URL 路径没有等价等待或超时。

**并发环境（可能干扰，需隔离复现）**：分析时 `ps` 显示同一默认路径上并行运行着 tmux
`./lilt tui`（10:36 起）、`./lilt serve --fake`，以及 22:48 新起的 `lilt serve` + `lilt-audio`。
它们可能造成 helper 重建/状态竞争，按走查约定属"装置噪声"。但两点降低其解释力：第 1 首在同一环境
下正常播放；第 2 首在全新 server/helper 上单独播放仍卡住。

**结论**：需要分开两件事：

1. **产品缺陷（真实）**：URL（Audius/流）播放缺少加载失败观察、`buffering` 超时与错误上报，
   把"卡死"表现为永久 buffering。最低要求：helper 观察 `AVPlayerItem.status` /
   `FailedToPlayToEndTime` 并写 `playbackError`；server 对 URL `buffering` 增加 stall 超时，
   转成 `playback_error`（或按策略重试/跳过），而不是无限等待。
2. **该 track/URL 是否真的不可播**：需在隔离装置（独立 `LILT_SOCKET`/`LILT_STATE`、单 server、
   无并行 harness）用同样的解析 URL 复测，才能判定是 Audius 上游问题还是 AVPlayer 兼容问题。

**待办**：先做第 1 项的确定性改造（配 Go/Swift 回归测试）；第 2 项用隔离最小复现确认后，把边界写入
[`../product/limitations.md`](../product/limitations.md)。

## 环境与上游噪声（非 lilt 缺陷）

- **Audius 的中文发现质量**：`search "中文"` 多为播客与 DJ 混音，`search "华语"` 为空；
  `search "mandarin"` 才命中真实中文歌单（如 santo wu 的 `mandarin`）。这是上游内容与检索的
  语言特性，不是 lilt 缺陷；可作为 skill 选词线索（中文内容优先试英文关键词）。
- **歌单自带重复曲目**：该歌单内 `Pbp75`、`NP9Rx` 各出现两次，播放后需 `queue remove` 去重。
  属上游数据问题。

## 做得好的方面

- `sources` / `search` / `playlist` / `play` / `queue` / `status` 的 `--json` 信封稳定，
  `ok` / `data` 与 track、queue 字段可直接消费，便于 agent 编排与回读。
- 本次 `play` 直接成功，未触发 `no_active_session`，无需手动 `serve`。
- 队列去重与 `status --queue` 回读一致，`queueIndex` / `queueRevision` 语义清晰。
