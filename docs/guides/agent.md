# 接入 AI agent

lilt 对 agent 的控制路径只有一条：**通过 `lilt` CLI**。agent 不碰 TUI、server，也不直接读写本地
存储；命令名、参数、返回模型与稳定错误码从 `lilt api --json` 读取，来源与能力从
`lilt sources --json` 读取。

对外的自包含 skill 在 [`../../skills/music-control/SKILL.md`](../../skills/music-control/SKILL.md)：
它是产品制品，`just agent-install` 会安装到 harness 全局 skills；仓库内通过
`.agents/skills/music-control` 软链加载同一份文件，不存副本。skill 只写**触发、策略与配方**，
不重复接口清单——重复会漂移。

## agent 必须遵守的契约要点

这些是 [`../client-api/README.md`](../client-api/README.md) 中面向 agent 的硬约束：

- **capability 决定传参**：`shuffle`/`repeat` 等形态参数只在来源声明了对应 capability 时传；
  未声明会在起播前返回 `unsupported_command`，不静默忽略。
- **不自行发起交互式授权**：`authorization_required` 时告知用户运行 `lilt auth <source> --json`；
  只有用户明确要求才执行 `auth disconnect`。
- **非幂等命令不重放**：结果未知时先读状态（`operation_outcome_unknown`），不要换 `requestId`
  重放；带 index 的队列操作先读最新队列（`ifQueueRevision`）。
- **不要启动 `lilt tui`**：那是给人用的全屏界面，agent 只走 CLI。
- **不做隐式换源**：API 原语不做跨来源 fallback；换源是 agent 的显式决定，且要先确认目标来源能力。
- **每次改变播放状态后用 `lilt status --json` 确认**，并向用户一句话汇报（播了什么 + 为什么选它）。

## 来源选择（用户未指定时）

按 `lilt sources --json` 的事实决策，不凭记忆：

1. 具备所需 capability 且 `available:true` 的来源，按 `priority` 降序。
2. 完整播放优先级：Apple Music full → Audius full → Jamendo full → radio stream。普通“播放音乐”
   要求 `playback.full`，不能把 `preview` 当完整播放。
3. radio 内部：`origin=builtin`（确定性最高）→ `origin=directory`。
4. 用户明确指定来源时，永远尊重用户选择。

完整算法与“API 原语确定性、编排在 skill”的分工见
[`../client-api/README.md`](../client-api/README.md) 的“来源选择规则”。

## 契约由测试守住

`just skill-check`（`go test ./internal/skillcheck`）把 skill 里出现的每个 `lilt …` 命令与错误码
对照进程内 catalog，检查软链指向发布制品，并断言六条安全策略仍在（不跑 `lilt tui`、选源前先读
capability、不做隐式换源、未经明确要求不授权、不重试 `unsupported_command`、mutation 后用
`status` 确认）。改 skill 文案不会静默偏离接口。
