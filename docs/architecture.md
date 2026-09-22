# 架构

lilt 是 macOS 上的 Apple Music、Audius、Jamendo 与网络电台终端控制器。本文件说明系统由哪些
部分组成、谁拥有什么，以及一次操作的数据流。接口细节见
[`client-api/README.md`](client-api/README.md)。

> **状态**：Apple Music 与统一 Radio 已按本架构实现：`lilt serve` 是唯一 server，
> 持有 helper、队列、`state.json` 与 Radio 目录/探测；TUI、CLI 与 skill 通过
> Client API v0.1 访问。helper 传输失败后自动重建，live stream 通过 ICY 暴露
> `streamTitle`/`streamArtist`。server-owned 异步授权 flow（provider 抽象）已实现。
> Audius 的 discovery、播放（URL 队列、helper `urlPlay`、公开路由）与账号 OAuth，以及 Jamendo 的
> discovery、有限 URL 队列、TUI/skill 集成均已实现，见 [`internals/providers.md`](internals/providers.md)；Linux 引擎尚未实现。接口版本为 `v0.1`，处于快速迭代期，不做向后兼容
> （见 [`client-api/README.md`](client-api/README.md)）。

## 1. 组件

```text
        ┌──────────────┬──────────────┬───────────────┐
        │  lilt tui    │  lilt CLI    │  AI skill     │   client
        │ (watch 常连) │  (one-shot)  │  (one-shot)   │
        └──────┬───────┴──────┬───────┴───────┬───────┘
               │              │               │
               └──────────────┼───────────────┘
                              │  Unix socket（Client API v0.1）
                    ┌─────────▼──────────┐
                    │     lilt serve     │  唯一常驻进程（server）
                    │  ┌──────────────┐  │
                    │  │ 内容提供者   │  │  discovery / ref resolution
                    │  ├──────────────┤  │
                    │  │ 播放队列核心 │  │  播放事实的唯一 owner
                    │  ├──────────────┤  │
                    │  │ state 存储   │  │  state.json 的单写者
                    │  ├──────────────┤  │
                    │  │ watch hub    │  │  状态广播（多 client fan-out）
                    │  └──────────────┘  │
                    └─────────┬──────────┘
                               │
              ┌───────────────┴───────────────┐
       ┌──────▼───────┐               ┌───────▼──────┐
       │ lilt-player  │               │ lilt-audio   │
       │ MusicKit     │               │ AVPlayer     │
       │ Apple/preview│               │ Audius/Radio │
       └──────────────┘               └──────────────┘
```

| 组件 | 角色 | 关键约束 |
|---|---|---|
| `lilt serve` | 唯一 server：持有 source provider、资源 runtime、播放路由、队列与持久状态 | 播放状态、`activeSource`、队列、`state.json` 的唯一写入者；持有生命周期锁 |
| `lilt tui` | client：完整手工操作界面 | 通过 Client API 访问一切；不得直接持有 helper；退出不停止播放 |
| `lilt` CLI | client：脚本与 agent 入口 | 稳定 `--json` 输出；幂等命令可安全重试 |
| AI skill | client：自然语言编排 | API 原语 + skill 推理；不做服务端隐式 fallback |
| `lilt-player` | MusicKit helper（签名 Swift app） | 可作为只读 Apple resource runtime（catalog/library/resolve），也可作为独占 Apple playback backend；只有后者拥有播放 session；full 不写 Now Playing |
| `lilt-audio` | AVPlayer helper（签名 Swift app，不链接 MusicKit） | Audius URL 队列、Radio stream、probe 与 Now Playing/媒体键；内部协议见 [`internals/helper-rpc.md`](internals/helper-rpc.md) |

## 2. 所有权

明确的单一所有权是多 client 同步的前提：

| 关注点 | owner |
|---|---|
| 播放状态、队列、当前 track、active source | `lilt serve`（数据来自 helper 快照） |
| 收藏、播放历史、派生 Recent | `lilt serve`（Activity SQLite store 的唯一写入者） |
| 主题、上次来源等偏好 | `lilt serve`（`state.json` 的唯一写入者） |
| active playback helper 生命周期与重建 | `lilt serve` / PlaybackCoordinator | 只销毁被替换的实际出声 backend；不销毁 resource runtime |
| 授权 flow 生命周期与状态 | `lilt serve`；provider 负责具体交互与凭据 |
| source discovery 与 ref/resource 解析 | 对应 ContentProvider / resource runtime；server 注册并路由 |
| Radio Browser 目录查询与探测 cache | `lilt serve` / RadioProvider |
| BrowseNode 呈现、键位、布局、主题渲染 | 各 client（TUI 自行决定） |
| 进度条的平滑插值 | client 本地（不外推 queue/track/status） |

## 3. 一次操作的数据流

以“agent 让 lilt 播放一个歌单，TUI 同步显示”为例：

```text
skill/CLI                server                         helper
   │  playback.play ───────►│
   │                        │  provider.PreparePlayback(ref) │
   │                        │  transport.Start(private plan) ►│
   │                        │◄──────────── State + 通知流 ──│
   │                        │  提交状态、递增 sequence
   │                        │  写入每个 watcher buffer
   │◄── response(State) ────│
   │                        │  playback.changed ──────────►│  (所有 watch client，含 TUI)
```

关键点：

- 命令的**提交顺序**就是其他 client 的观察顺序；server 串行执行有副作用的命令。
- response 与 watch event 携带同一个 `state.sequence`；watcher 可能先于 caller
  看到事件，这是允许的。
- 任何一个 client 的操作（CLI、skill、TUI 手工）走同一条路径，因此天然同步。
- server 为 playback session 分配不可复用的 `playbackGeneration` 与 transport session ID，
  并在 start 前绑定它们；只提交当前 helper instance/generation/session 的通知。外部/媒体键 observer
  使用其捕获的 generation/session、保持可观察。

## 4. 来源、Provider 与播放传输

- **Source** 是可浏览、可播放的公开内容域：已实现的是 `apple-music`、`radio`、`audius` 与
  Jamendo discovery + 有限 URL 队列播放与 TUI/skill 集成。每个 source 声明能力与可用性，见
  [`client-api/models.md`](client-api/models.md#1-sourcedescriptor)。
- **Item identity 属于 `internal/api`**：`api.Identity` 是 `id`/`providerId`/`ref` 与 stream URL
  规范化的唯一实现；provider 只负责产出 provider-native id 与展示字段，广播 URL 规范化、Apple/Audius/Jamendo
  前缀拼装、radio 身份都由该实现统一完成。
- **ContentProvider** 是 source 的编译期实现组件，负责 discovery、identity、canonical ref，并将
  ref 准备为 transport-specific 私有 plan。它可以依赖自己的 resource runtime（例如 Apple Music 的
  只读 MusicKit client）；resource runtime 不直接拥有公开播放状态、播放队列或持久化短期资源。
- **播放传输** 是实际出声的后端。macOS 按进程归属拆成 `lilt-player`（MusicKit）与
  `lilt-audio`（AVPlayer）；server 在 transport 切换时只停止并终止另一**playback backend**，避免两个
  Now Playing session 竞争。resource runtime 不属于这条互斥链。
- 播放严格互斥：任一时刻只有一个活动 playback Source/engine；资源读取可在另一 source 播放时按需
  串行执行，且不限制多 source 的 discovery 或 auth。
- server 在启动 source 时显式提交 `activeSource` 和 generation；失败、stop、helper restart、外部
  媒体键的状态转换不得依靠 helper mode 或 `isLive` 推断。
- `URLQueueTransport` 在 server 内持有稳定公开 Item、index 与 queue revision，只在 start、
  next/previous/jump 时解析当前一项的短期 URL；driver 调用携带 generation/session。该 URL 不回写 plan、
  server state、公开投影或持久文件。Audius 的 `playback.full`/`queue` 已接入 Client API；Apple 走
  MusicKit transport，Radio 走 audio stream transport。
- 完整契约、路由与分阶段实施见 [`internals/providers.md`](internals/providers.md)；扩展步骤见
  [`client-api/extending.md`](client-api/extending.md)，测试分层见 [`testing/integration.md`](testing/integration.md)。

术语：**Source** 是公开的内容域（client 看到的概念）；**provider** 是它的实现
组件。二者不是同一层。

每个需要授权的 Source 都通过 server-owned 异步 flow 暴露统一状态；开始授权的 client
断线不影响 flow，server 关闭时取消 pending flow。凭据只属于 provider 的 OS secure
storage，不属于 server state 或 Client API。

数据 identity、BrowseNode 树与 id 方案见
[`internals/sources.md`](internals/sources.md)。

## 5. 数据与状态

| 文件 | 作用 | 规范 |
|---|---|---|
| Activity store（SQLite） | Item、Favorites、完整 Playback History、派生 Recent | [`internals/local-activity.md`](internals/local-activity.md) |
| `state.json` | 主题、上次来源等轻量偏好 | [`internals/state.md`](internals/state.md) |
| `internal/builtin` snapshot | 内置电台列表（内嵌 m3u，含 provenance） | [`client-api/extending.md`](client-api/extending.md#2-内置电台) |
| `themes/*.toml` | 主题（沿用 cliamp TOML schema） | [`ui/theme.md`](ui/theme.md) |
| `radio-cache.json` | 可删除的 Radio Browser 探测 cache | [`internals/radio-discovery.md`](internals/radio-discovery.md) |

内置电台 snapshot/list 源自 [cliamp](https://github.com/bjarneo/cliamp)
及 [cliamp.stream](https://cliamp.stream/)，不由 lilt 创建或拥有；provenance
（upstream、list URL、检索时间、SHA-256、免责声明）由 `internal/builtin` 提供并被
测试断言。详见 [`client-api/extending.md`](client-api/extending.md#2-内置电台)。

## 6. 平台与引擎路线

| 平台 | Apple Music | Audius | Jamendo | Radio | 说明 |
|---|---|---|---|---|---|
| macOS | MusicKit（签名 helper） | 官方 REST discovery + helper 有限 URL 队列（已实现） | 官方 REST discovery + helper 有限 URL 队列（J1/J2 已实现） | AVPlayer live stream | Jamendo 需自备 `client_id`，仅非商业 |
| Linux | 不支持 | 官方 REST + mpv（future） | 官方 REST + mpv（future） | mpv（proposed） | 见 [`internals/linux-mpv-engine.md`](internals/linux-mpv-engine.md) |
| 其他 | 预留 | 预留 | 预留 | 预留 | 未排期 |

产品路线与范围见 [`product/roadmap.md`](product/roadmap.md)；已知限制见
[`product/limitations.md`](product/limitations.md)。

## 7. 进一步阅读

完整的文档地图与阅读路径见 [`README.md`](README.md)。
