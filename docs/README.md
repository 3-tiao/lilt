# lilt 文档

lilt 是 macOS 上的 Apple Music、可选 Audius 与网络电台终端控制器，可用 TUI 手工操作，也可被
CLI 与 AI agent 编程控制。这份目录同时面向三类读者：

| 你是 | 从这里开始 |
|---|---|
| 用户 / 评审者 | [架构](architecture.md) → [产品路线](product/roadmap.md) → [已知限制](product/limitations.md) |
| 集成者（写 client / agent 工具） | [Client API v0.1](client-api/README.md) → [数据模型](client-api/models.md) → [命令](client-api/commands.md) |
| 想新增内容来源（Audius 或未来 Spotify） | [扩展来源](client-api/extending.md) → [Provider 与播放传输](internals/providers.md) → [来源模型](internals/sources.md) |
| 想验证真实 provider 与契约 | [集成测试设计](testing/integration.md) → [Provider 接入准入](testing/provider-admission.md) |
| 想改 TUI | [UI 模型与导航](ui/model.md) → [交互](ui/ux.md) → [主题](ui/theme.md) |
| 想发版本 | [发布流程](product/release.md) |

> **实现状态**：Apple Music 与统一 Radio（builtin + Radio Browser）已迁移到
> `lilt serve` 单一 server；TUI、CLI 与 AI skill 都是 Client API v0.1 的 client。
> helper 传输失败后自动重建；live stream 通过 ICY 暴露 `streamTitle`/`streamArtist`；
> server-owned 异步授权 flow（provider 抽象，可用 fixture provider 验证）已实现。
> Audius 的 discovery、播放（Phase 1/2/2.5）与账号 OAuth（Phase 3）均已实现；Linux 引擎尚未实现。

## 文档结构

```text
docs/
├── README.md              你在这里：文档地图
├── architecture.md        系统架构：组件、所有权、数据流、平台路线
├── client-api/            对外接口契约（Client API v0.1）
│   ├── README.md            概览、来源选择、命令总表、api.describe、实现状态
│   ├── protocol.md          传输、请求/响应、并发、去重重试、超时、engine 重建
│   ├── models.md            SourceDescriptor、SourceAuthorization、AuthorizationFlow、Item、PlaybackState/PlaybackStatus、AppState
│   ├── commands.md          每个命令的参数/返回/语义 + CLI 映射
│   ├── watch.md             实时事件订阅、topics、顺序、溢出
│   ├── errors.md            稳定错误码
│   └── extending.md         新增 Source、内置电台
├── testing/
│   ├── integration.md        真实 E2E 与确定性 contract 测试设计
│   └── provider-admission.md provider 接入准入条件与门禁边界
├── internals/             实现契约（TUI/CLI 背后的机制）
│   ├── helper-rpc.md        Go server ↔ Swift helper 的内部协议
│   ├── audio-helper.md      MusicKit / AVPlayer 双 helper 拆分（设计）
│   ├── providers.md         Source provider、播放传输、active source 设计
│   ├── playback-state-sync.md  状态同步设计（实现说明）
│   ├── sources.md           BrowseNode 树、Item identity、id 方案
│   ├── state.md             state.json 持久 schema 与规则
│   ├── radio-discovery.md   Radio Browser 发现、探测与 cache
│   └── linux-mpv-engine.md  Linux radio 引擎（proposed）
├── ui/                    TUI
│   ├── model.md             UI 模型：Source / Surface / Home、导航与切换（设计）
│   ├── ux.md                布局、导航、键位、错误与弹层（当前实现）
│   └── theme.md             主题 TOML schema
├── integrations/
│   └── agent-skill.md       AI agent 接入指南（skill 如何编排本 API）
└── product/
    ├── roadmap.md           产品路线、范围与平台计划
    ├── release.md           发布流程（版本、制品、Homebrew tap）
    └── limitations.md       已接受的已知限制
```

## 约定

- **规范语言**：MUST / SHOULD / MAY 分别表示必须、建议、可选。
- **单一事实来源**：接口形状以 [`client-api/`](client-api/README.md) 为准；
  持久数据以 [`internals/state.md`](internals/state.md) 为准；内部协议以
  [`internals/helper-rpc.md`](internals/helper-rpc.md) 为准。
- **术语**：接口版本为 `v0.1`（快速迭代，不做向后兼容）；产品路线里的「v2」指跨端阶段。两者无关。
