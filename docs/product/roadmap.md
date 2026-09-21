# 产品路线

本文件是 lilt 的产品定位、范围与平台计划。接口契约见
[`../client-api/README.md`](../client-api/README.md)；架构见
[`../architecture.md`](../architecture.md)。

> **当前进度**：CS 架构迁移已完成——Apple Music 与统一 Radio（builtin + Radio
> Browser）由 `lilt serve` 持有，TUI/CLI/skill 为 Client API v0.1 client。helper
> 传输失败后自动重建，live stream 通过 ICY 暴露 `streamTitle`/`streamArtist`，
> server-owned 异步授权 flow（provider 抽象）已实现。Audius 的 REST discovery、URL 队列播放与账号
> OAuth（Authorization Code + PKCE）以及 TUI/skill 可见集成已实现；Linux 引擎尚未实现。

## 1. 定位

面向常年使用终端的用户：在终端里听 Apple Music、可选 Audius 与网络电台，并可被 AI agent
以自然语言控制。日常入口：

```sh
lilt tui                       # 完整手工操作（TUI 是 client）
lilt play <ref>                # 直接播放（canonical ref / URL / 流地址）
lilt status --json             # 供 agent 与脚本消费
```

在 opencode 等编码 agent 中，用户可以只说“播放适合作息的歌”，由 skill 依据
Client API 选择来源与播放形态。

## 2. 核心决策

1. **CS 架构**：常驻 `lilt serve` 持有播放与状态；TUI、CLI、skill 都是 client。
   TUI 退出不停止播放。
2. **来源优先级**：能力允许时 Apple Music full → Audius full → radio stream（含内置精选台）；
   用户明确来源始终优先。
3. **API 原语确定性，编排在 skill**：服务端不做隐式跨来源 fallback；自然语言
   理解与候选判断由 skill 完成。
4. **原生 MusicKit，不手工维护 token**：macOS 上通过签名 Swift helper 使用系统
   授权，不收集 Apple ID，不签发 Developer Token。
5. **来源可扩展**：正式公共 Source 是 `apple-music`、可选 `audius`、`radio`。Audius 已提供
   discovery、URL 队列播放与可选账号 OAuth；扩展方式见
   [`../client-api/extending.md`](../client-api/extending.md)。
6. **不做本地音乐库**：不扫描本地文件、不做播放列表文件管理、不做下载导出。

## 3. 平台与引擎

| 平台 | Apple Music | Audius | Radio | 状态 |
|---|---|---|---|---|
| macOS | MusicKit（签名 helper） | 官方 REST discovery + helper 有限 URL 队列、TUI/skill（已实现） | AVPlayer live stream | Audius Phase 1–4 已完成 |
| Linux | 不支持 | 官方 REST + mpv（future） | mpv（proposed，见 [`../internals/linux-mpv-engine.md`](../internals/linux-mpv-engine.md)） | 等硬件 |
| 其他 | 预留（`web` 引擎设计） | 预留 | 预留 | 未排期 |

跨端原则：**共享规范，不共享代码**。各端用各自语言实现同一数据与操作契约，
音质不作承诺。

## 4. 范围

**做**

- Apple Music：搜索、资料库歌单、lilt 本地最近播放（History 派生）、歌单/歌曲/目录电台播放、
  队列编辑、收藏（本地 Activity store，带完整 `favorite add|remove` CLI 与 `history.*` 查询）。
- Radio：Radio Browser 发现与筛选、内置精选台、收藏、探测与缓存。
- Audius：官方 public discovery/search/playlists、server-owned URL 队列播放、可选账号 OAuth，以及
  TUI Search/Recent/Favorites 与 skill 编排均已实现；
  分层设计见 [`../internals/providers.md`](../internals/providers.md)。
- TUI：完整手工操作；CLI/JSON：供脚本与 agent；agent skill：自然语言编排。
- 主题（沿用 cliamp TOML schema）；本地优先状态。

**不做**

- 本地文件、播客、歌词、EQ、频谱、音量与 seek（公开 MusicKit 不提供可靠的
  实时 bitrate / seek / 音量）。
- 创建或编辑 Apple Music 资料库歌单（macOS 不提供该 API）；lilt 不维护自己的
  歌单文件。
- 下载、导出、规避 DRM；强制无损/Hi-Res/Atmos。
- 用户插件系统；扩展点只有 Source registry。
- 手机端 UI。

## 5. 后续（未排期）

- 本地 Activity SQLite 已实现（存储、`history.*`/`favorites.add|remove`/`data reset`、TUI
  All Favorites）；真实验收待跑，后续见 [`../internals/local-activity.md`](../internals/local-activity.md)。
- macOS config/state 默认目录改为 native Application Support，并按
  [`../internals/state.md`](../internals/state.md#路径) 原子迁移现有 XDG-style 数据；当前 Activity 与
  lifecycle lock 已先收敛到现有 durable state root，不再跟随 cache/socket。
- Linux：`mpv` 引擎实现后开放 Radio；Apple Music 仍不在 Linux 范围。
- 状态云同步：合并策略见 [`../internals/state.md`](../internals/state.md)。
- 后台续播与开机自启。
- **新来源候选（2026-09-20 记录，未排期）**：
  - **SoundCloud**：搜索、播放、相关歌曲能力仍在；内容量大，Lofi / Ambient / Electronic / Indie
    特别丰富，产品上比 Audius 更容易被理解。**主要成本**：API / OAuth / 播放权限比 Audius 麻烦，
    需要先确认第三方 app 的播放授权范围与 stream URL 的短期性（签名 URL 绝不入库，见
    [`../internals/sources.md`](../internals/sources.md)）。推荐度 ★★★★★。
  - **Jamendo**：定位贴合"独立音乐 / Ambient / Lofi / 背景音乐"，官方 API 有搜索与 Radio，目录
    数十万级，授权体系明确（不是抓 Internet Radio）。**主要成本**：商业 app 的 API 与音乐使用
    需要单独购买授权，接入前必须先确认许可与配额。推荐度 ★★★★★。
  - 共同前提：两者都是 **编译期 provider**（无运行期插件），接入必须走
    [`../testing/provider-admission.md`](../testing/provider-admission.md) 门禁：只实现并声明真正
    支持的 capability、不静默降级、稳定 ID 用 `soundcloud:<kind>:<id>` / `jamendo:<kind>:<id>`、
    短期媒体 URL 只在校验后解析且不进入持久状态与日志。**不新增第三方依赖**（Go 优先 stdlib）。
- Spotify 等新来源接入。
- Audius integration milestone：hermetic shared contract suite 与 opt-in real E2E，见
  [`../testing/integration.md`](../testing/integration.md)。

## 6. 已决与待决

已决：

- ICY 电台“正在播放”元数据已实现：server 读取流内 `StreamTitle`，拆分为
  `streamTitle`/`streamArtist`（见
  [`../client-api/models.md`](../client-api/models.md)）。
- 音量与 seek 不做（依赖系统或 Apple Music 自身控制）。

待决：

- 状态云同步的合并策略。

工程层面尚未解决的实现问题（含证据与下一步）集中在
[`open-questions.md`](open-questions.md)，不在本文件维护副本。
