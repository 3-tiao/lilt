# 实现契约（internals）

本目录放“实现者需要遵守的契约”：TUI / CLI / skill 背后的机制、provider 与播放传输、持久化。
对外接口形状以 [`../client-api/README.md`](../client-api/README.md) 为准，系统总览见
[`../architecture.md`](../architecture.md)；本目录不重复命令、参数与 wire 模型。

## Provider 与来源

| 文档 | 内容 |
|---|---|
| [`providers/providers.md`](providers/providers.md) | Source provider、播放传输、activeSource/generation、分阶段实施 |
| [`providers/sources.md`](providers/sources.md) | BrowseNode 树、Item identity、稳定 id 方案 |
| [`providers/jamendo.md`](providers/jamendo.md) | Jamendo provider：凭据、discovery、播放、错误映射、许可限制 |
| [`providers/radio-discovery.md`](providers/radio-discovery.md) | Radio Browser 目录发现、健康探测与 `radio-cache.json` |

## 播放与 helper

| 文档 | 内容 |
|---|---|
| [`playback/helper-rpc.md`](playback/helper-rpc.md) | Go server ↔ Swift helper 的内部 JSON-RPC 协议 |
| [`playback/audio-helper.md`](playback/audio-helper.md) | MusicKit / AVPlayer 双 helper 拆分与路由 |
| [`playback/playback-state-sync.md`](playback/playback-state-sync.md) | 播放状态同步：所有权、wire 形状与 UI 行为 |
| [`playback/linux-mpv-engine.md`](playback/linux-mpv-engine.md) | Linux Radio/URL 引擎（进程内 mpv JSON IPC） |
| [`playback/apple-web-engine.md`](playback/apple-web-engine.md) | Apple Music 浏览器引擎（Apple web player + Widevine） |

## 持久化

| 文档 | 内容 |
|---|---|
| [`persistence/state.md`](persistence/state.md) | `state.json` schema、路径、迁移与规则 |
| [`persistence/local-activity.md`](persistence/local-activity.md) | Activity SQLite store：Favorites / Playback History / Recent |

## 约定

- **单一事实来源**：接口以 [`../client-api/`](../client-api/README.md) 为准；持久数据以
  [`persistence/state.md`](persistence/state.md) 与
  [`persistence/local-activity.md`](persistence/local-activity.md) 为准；helper 协议以
  [`playback/helper-rpc.md`](playback/helper-rpc.md) 为准。
- **短期/签名媒体 URL 绝不进入**持久状态、公开 response、watch event、日志或 fixture，只在播放启动时解析。
