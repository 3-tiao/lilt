# lilt 文档

lilt 是 macOS 与 Linux 上的 Apple Music、Audius、Jamendo 与网络电台终端控制器，可用 TUI 手工操作，
也可被 CLI 与 AI agent 编程控制。`lilt serve` 是每个隔离 state root 的唯一常驻 server，持有播放路由、
队列与 `state.json`；TUI、CLI 与 agent skill 都是 Client API v0.1 的平等 client。

产品总览（含功能、场景、架构、与同类方案的区别）见根目录
[`../README.zh-CN.md`](../README.zh-CN.md)。本页是 `docs/` 的路由表。

## 按“我要做什么”找

| 我想 | 从这里开始 |
|---|---|
| 安装 lilt、完成第一次播放 | [`getting-started/`](getting-started/README.md) |
| 用 TUI 做日常播放与控制 | [`guides/tui.md`](guides/tui.md) |
| 用 CLI / JSON 写脚本、接自动化 | [`guides/cli-and-scripting.md`](guides/cli-and-scripting.md) |
| 让 AI agent 用自然语言控制 | [`guides/agent.md`](guides/agent.md) |
| 听网络电台、筛选电台 | [`guides/radio.md`](guides/radio.md) |
| 排查故障、看日志、理解错误码 | [`guides/troubleshooting.md`](guides/troubleshooting.md) |

## 按“我要了解/改动哪一层”找

| 层次 | 入口 |
|---|---|
| 系统由哪些部分组成、谁拥有什么 | [`architecture.md`](architecture.md) |
| 对外接口契约（Client API v0.1） | [`client-api/README.md`](client-api/README.md) |
| client / agent 工具集成 | [`client-api/models.md`](client-api/models.md) → [`commands.md`](client-api/commands.md) → [`watch.md`](client-api/watch.md) → [`errors.md`](client-api/errors.md) |
| 新增内容来源（provider） | [`client-api/extending.md`](client-api/extending.md) → [`internals/providers/providers.md`](internals/providers/providers.md) → [`internals/providers/sources.md`](internals/providers/sources.md) |
| 实现契约（helper、播放引擎、持久化） | [`internals/README.md`](internals/README.md) |
| TUI 产品与设计 | [`ui/README.md`](ui/README.md) |
| 产品定位、发布与已知限制 | [`product/README.md`](product/README.md) |
| 测试分层与 provider 准入 | [`testing/README.md`](testing/README.md) |

## 文档结构

```text
docs/
├── README.md              你在这里：文档地图
├── architecture.md        系统架构：组件、所有权、数据流、平台路线
├── getting-started/       第一次使用：安装、平台要求、第一次播放
├── guides/                按任务：TUI、CLI/脚本、agent、电台、排障
├── client-api/            对外接口契约（Client API v0.1）
│   ├── README.md            概览、来源选择、命令总表、api.describe、实现状态
│   ├── protocol.md          传输、请求/响应、并发、去重重试、超时、engine 重建
│   ├── models.md            SourceDescriptor、Item、PlaybackState、AppState 等
│   ├── commands.md          每个命令的参数/返回/语义 + CLI 映射
│   ├── watch.md             实时事件订阅、topics、顺序、溢出
│   ├── errors.md            稳定错误码
│   └── extending.md         新增 Source、内置电台
├── internals/             实现契约（TUI/CLI 背后的机制）
│   ├── README.md            实现契约索引
│   ├── providers/           provider、来源树、Jamendo、Radio 发现
│   ├── playback/            helper RPC、音频双 helper、状态同步、mpv/浏览器引擎
│   └── persistence/         state.json、Activity SQLite
├── ui/                    TUI 产品与设计
├── product/               产品定位、限制、路线、发布
└── testing/               测试分层与 provider 准入
```

## 约定

- **规范语言**：MUST / SHOULD / MAY 分别表示必须、建议、可选。
- **单一事实来源**：接口形状以 [`client-api/`](client-api/README.md) 为准；
  持久数据以 [`internals/persistence/state.md`](internals/persistence/state.md) 为准；内部协议以
  [`internals/playback/helper-rpc.md`](internals/playback/helper-rpc.md) 为准。
- **术语**：接口版本为 `v0.1`（长期保持、快速迭代，不做向后兼容）；产品路线里的「v2」指跨端阶段，
  两者无关。
