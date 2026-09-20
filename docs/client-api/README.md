# lilt Client API v0.1

`lilt serve` 对外暴露的统一接口。`lilt tui`、`lilt` CLI、AI skill，以及未来的
桌面、Web 或移动 client，都是这份接口的平等使用者。

> **状态**：契约已实现。`api.describe` 由实际 command registry 生成，server 按本
> 规范分发命令；Apple Music 与统一 Radio 已迁移，TUI/CLI/skill 均通过本接口。
> 尚未实现的部分见 [实现状态](#实现状态)。
>
> **术语**：接口版本写作 **`v0.1`**（长期保持）。接口处于**快速迭代期**，会直接改
> wire 模型/命令/错误语义，不做向后兼容；与产品路线里的「v2（跨端阶段）」无关。
> 产品路线见 [`../product/roadmap.md`](../product/roadmap.md)。

本文档使用 MUST、SHOULD、MAY 表示必须、建议和可选行为。

## 怎么读

| 你想了解 | 读这份 |
|---|---|
| 系统由哪些部分组成、谁拥有什么 | [`../architecture.md`](../architecture.md) |
| 连接方式、请求/响应格式、超时与重试 | [`protocol.md`](protocol.md) |
| Item、Source、播放状态等数据形状 | [`models.md`](models.md) |
| 每个命令的参数、返回与语义 | [`commands.md`](commands.md) |
| 实时事件订阅（TUI 同步、状态栏） | [`watch.md`](watch.md) |
| 稳定错误码 | [`errors.md`](errors.md) |
| Audius 与未来来源扩展 | [`extending.md`](extending.md) |
| 让 AI agent 用 CLI 驱动本接口 | [AI agent 接入](#ai-agent-接入) |
| Source provider 与播放传输顶层设计 | [`../internals/providers.md`](../internals/providers.md) |
| Swift helper 内部协议（不是本接口） | [`../internals/helper-rpc.md`](../internals/helper-rpc.md) |

## AI agent 接入

面向 agent 的自包含操作速查在仓库
[`../../.agents/skills/music-control/SKILL.md`](../../.agents/skills/music-control/SKILL.md)（与其它 skill 同目录，
`.opencode/skills` 是指向该目录的软链）；`just agent-install` 把它复制到 opencode 的全局 skills
目录。skill 是**自包含**的：它不引用本目录，只依赖运行时的 `lilt api --json`（命令名、参数
schema、返回模型、稳定错误码）与 `lilt sources --json`（capability）。本页与
[`commands.md`](commands.md)、[`errors.md`](errors.md) 是它的权威依据。

**分工**：skill 只写触发、策略与配方；命令名/参数/返回/错误码由 `lilt api --json` 提供，不在
skill 里重复（重复会漂移）。如果某条 skill 文字其实是在绕开 CLI/API 的毛病，正确做法是修 CLI/API
并删掉那段文字，而不是把它留在 skill 里。

agent 编排时必须遵守的契约要点：

- **capability 决定传参**：`shuffle` / `repeat` 等形态参数只在该 source 声明对应 capability 时传；
  未声明会在起播前返回 `unsupported_command`，不静默忽略（见 [`commands.md`](commands.md)）。
- **不自行发起交互式授权**：`authorization_required` 时告知用户运行 `lilt auth <source> --json`；
  只有用户明确要求才执行 `auth disconnect`。
- **非幂等命令不重放**：结果未知时先读状态（`operation_outcome_unknown`），不要换 requestId 重放；
  带 index 的队列操作先读最新队列（`ifQueueRevision`，见 [`commands.md`](commands.md)）。
- **不要启动 `lilt tui`**：那是给人用的全屏界面，agent 只走 CLI。

## 设计目标

1. 一个 server 持有播放引擎、队列和持久状态；任意 client 的操作对其他 client
   立即可见。
2. API 同时适合 TUI 的完整手工操作和 AI skill 的低 token、可组合调用。
3. API 原语保持确定性；自然语言理解、候选判断和 fallback 由 skill 编排。
4. Source（公开内容域）可扩展：编译期 ContentProvider 负责 discovery/ref，私有播放传输
    负责实际出声。正式公共 Source 是 `apple-music`、可选 `audius`、`radio`；Audius Phase 1 discovery 已实现，
   参考真实 E2E provider。完整分层见 [`../internals/providers.md`](../internals/providers.md)。

## 所有权摘要

完整的架构说明见 [`../architecture.md`](../architecture.md)。对 client 最重要的
三条约定：

- `lilt serve` MUST 是播放状态、队列和 `state.json` 的唯一写入者。
- TUI MUST 通过本接口访问 source 内容、player 和 state，不得直接持有 helper；
  TUI 退出 MUST NOT 停止播放或退出 server。
- 一个 server MAY 同时接受多个 one-shot client 和多个 watch client，并 MUST
  串行化有副作用的命令，保证观察顺序与执行顺序一致。

## 来源选择规则

API 原语 MUST 不做隐式跨来源 fallback。client 明确调用某个 source 时，该 source
不可用就返回错误，不得悄悄换源。

自然语言 client（skill）的默认算法：

1. 调用 `sources.list`。
2. 在所需 capability 自身 available 的 source 中按 `priority` 降序选择。普通
   “播放音乐”要求完整播放能力（`playback.full`），不能把 preview 当成 full playback。
3. 当前可播放优先级：Apple Music full → Audius full → radio stream。前者未授权、无订阅或
   不可用时才考虑下一个。
4. radio 内部的候选顺序：`origin=builtin`（不依赖网络目录，确定性最高）
   → `origin=directory`（Radio Browser）。目录不可达时仍有 builtin 可用。
5. 用户明确指定来源时 MUST 尊重用户选择。

这个分工让 API 结果可预测，同时把“选最优来源”的决策留给 skill。

### Browse 归属

本接口不尝试用一份通用 schema 自动生成所有 provider 的界面。server 拥有内容数据与
provider 调用；client 按 [`../internals/sources.md`](../internals/sources.md) 定义
的 BrowseNode 树组织呈现。TUI 的 Source tab、Home 分组和 Radio 过滤表单属于
client 表现层，数据来自本接口的 discovery、state 与 radio 命令。

因此 Audius（或未来 Spotify）需要注册 server ContentProvider、auth provider 和播放路由，并在
TUI 增加对应 tab 与 BrowseNode 组合；本接口不承诺“装个 provider 插件就自动生成界面”。公共 Item、search、
playlist、queue、playback 和 watch 模型保持不变。

## 命令总表

完整参数与语义见 [`commands.md`](commands.md)。

| 组 | 命令 | CLI |
|---|---|---|
| 契约 | `api.describe` | `lilt api --json` |
| 契约 | `sources.list` | `lilt sources --json` |
| 播放 | `playback.play` | `lilt play <ref> [--name T] [--shuffle] [--repeat MODE] --json` |
| 播放 | `playback.playSongs` | `lilt play-songs <ref,..> [--start N] [--shuffle] [--repeat MODE] --json` |
| 播放 | `playback.pause` / `resume` / `toggle` | `lilt pause\|resume\|toggle --json` |
| 播放 | `playback.next` / `previous` / `stop` | `lilt next\|previous\|stop --json` |
| 播放 | `playback.setShuffle` / `setRepeat` | `lilt shuffle on\|off --json` / `lilt repeat off\|all\|one --json` |
| 队列 | `queue.list` / `add` / `remove` / `move` / `clear` | `lilt queue [list]` / `queue add <ref> --next\|--append` / `queue remove <index>` / `queue move <from> <to>` / `queue clear`（均 `--json`） |
| 队列 | `queue.jump` | TUI 专用 |
| 发现 | `discovery.search` | `lilt search <term> [--source S] [--type T] [--limit N] --json` |
| 发现 | `album.tracks` | `lilt album <ref> --json` |
| 发现 | `playlist.tracks` | `lilt playlist <ref> --json` |
| 发现 | `library.playlists` / `library.albums` | `lilt library [--source S] --json` / `lilt albums [--source S] --json` |
| 发现 | `recent.list`（lilt 本地历史） | `lilt recent [N] --json` |
| 发现 | `recommendations.list` | 暂无 CLI |
| 发现 | `radio.search` / `radio.options` / `radio.probe` | `lilt radio search [...] [--origin builtin\|directory\|all] --json` |
| 发现 | `radio.cache`（server 探测缓存快照） | `lilt radio cache --json` |
| 状态 | `state.get` / `favorites.list` / `favorites.set` / `ui.set` | `lilt favorites --json` |
| 会话 | `session.status` | `lilt status [--queue] --json` |
| 会话 | `session.watch` | 由 client 直接连接 |
| 授权 | `authorization.list` / `status` / `begin` / `flowStatus` / `cancel` / `disconnect` | `lilt auth status [SOURCE] --json` / `lilt auth <SOURCE> --json` / `lilt auth cancel <FLOW_ID> --json` / `lilt auth disconnect <SOURCE> --json` |
| 会话 | `session.shutdown` | `lilt quit --json` |
| 生命周期 | — | `lilt serve [--detach] --json` |

## API 自描述

`api.describe`（CLI：`lilt api --json`）MUST 返回机器可读的接口目录：

```jsonc
{
  "commands": [
    {
      "name": "playback.play",
      "cli": "lilt play <ref> [--shuffle] [--repeat MODE] --json",
      "timeoutMs": 60000,
      "paramsSchema": {},
      "resultSchema": "PlaybackState",
      "errors": ["invalid_reference", "source_unavailable", "partial_failure", "playback_error"],
      "description": "Start playback for a canonical ref."
    }
  ],
  "models": {},
  "errors": {}
}
```

约束：

- 输出 MUST 由实现使用的同一 command registry 生成，不能维护一份会漂移的手写
  JSON。
- `lilt api --json` 从编译进二进制的 registry 生成，不要求 server 正在运行；
  wire 命令 `api.describe` 返回同一内容。
- `paramsSchema` 与 model schema 使用 JSON Schema Draft 2020-12，通过 `$ref`
  引用公共模型（见 [`models.md`](models.md)）。
- 每个 command 可带可选 `description`，`sources.list` 的 descriptor/capability 也可带
  `description`。它们是**非规范性**文字，只为 agent/skill 提供线索；client 行为 MUST 只依赖
  schema、capability `available`/`reason` 与稳定错误码，MUST NOT 依赖这些文字。
- SKILL.md 可以摘要常用命令，但这份输出是机器可读的权威目录。
- 若描述 builtin radio origin，`lilt api` MUST 归属 cliamp/cliamp.stream，并包含无
  affiliation/endorsement、无可用性保证及第三方 station audio rights 的说明；完整
  provenance/refresh 规则见 [`extending.md`](extending.md#2-内置电台)。

## 实现状态

本接口是当前实现契约。已实现：`{requestId,command,params}` wire 格式、
command registry 与 `api.describe`、`sources.list`、播放/队列/发现/state/radio/
session/authorization 命令族、请求去重与 `conflict`、`session.watch`（原子快照、
topics、溢断）、server 单写者 state、统一 Radio（`builtin` + `directory`）、
Apple Music 搜索/资料库/歌单/播放、shuffle/repeat、`queue`、`favorites`、分级
超时、helper 传输失败后的自动重建（`server.warning` → `engine.restarted`、
重建期间 `engine_restarting`、超时命令返回 `operation_outcome_unknown` 且不自动重放）、
ICY 流内元数据（`streamTitle`/`streamArtist`）、server-owned 异步授权 flow
（provider 抽象 + 状态机/cancel/终态保留/断线续存/shutdown 取消，Apple 用系统对话框，
可用 fixture provider 做 hermetic 验证）、server 端 recent 阈值计时（累计
`status=playing` 达到 `min(30s, 50% 已知时长)` 才记录）、server-owned 探测缓存
（`radio.cache` 供 client 读取，探测成功与失败都持久化）。

尚未实现或尚未完整实现：Linux 引擎。Audius 的 discovery、播放与账号 OAuth 均已实现（OAuth 需部署者配置 developer app）。

迁移以本接口为准；旧扁平 wire 格式与旧 CLI 命令语义已移除，不提供兼容层。
