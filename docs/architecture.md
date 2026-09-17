# 架构

lilt 是 macOS 上的 Apple Music、Audius 与网络电台终端控制器。本文件说明系统由哪些
部分组成、谁拥有什么，以及一次操作的数据流。接口细节见
[`client-api/README.md`](client-api/README.md)。

> **状态**：Apple Music 与统一 Radio 已按本架构实现：`lilt serve` 是唯一 server，
> 持有 helper、队列、`state.json` 与 Radio 目录/探测；TUI、CLI 与 skill 通过
> Client API v2 访问。helper 传输失败后自动重建，live stream 通过 ICY 暴露
> `streamTitle`/`streamArtist`。server-owned 异步授权 flow（provider 抽象）已实现。
> 尚未实现的部分（Audius、Linux 引擎）在各规范中标注。术语「Client API v2」是接口版本，与产品路线版本
> 无关（见 [`product/roadmap.md`](product/roadmap.md)）。

## 1. 组件

```text
        ┌──────────────┬──────────────┬───────────────┐
        │  lilt tui    │  lilt CLI    │  AI skill     │   client
        │  (watch 常连) │  (one-shot)  │  (one-shot)   │
        └──────┬───────┴──────┬───────┴───────┬───────┘
               │              │               │
               └──────────────┼───────────────┘
                              │  Unix socket（Client API v2）
                    ┌─────────▼──────────┐
                    │     lilt serve     │  唯一常驻进程（server）
                    │  ┌──────────────┐  │
                     │  │ Source 注册表 │  │  apple-music / audius / radio
                    │  ├──────────────┤  │
                    │  │ 播放与队列核心 │  │  播放事实的唯一 owner
                    │  ├──────────────┤  │
                    │  │ state 存储    │  │  state.json 的单写者
                    │  ├──────────────┤  │
                    │  │ watch hub     │  │  状态广播（多 client fan-out）
                    │  └──────────────┘  │
                    └─────────┬──────────┘
                              │
              ┌───────────────┴────────────────┐
              │                                │
     ┌────────▼─────────┐            ┌─────────▼─────────┐
     │  lilt-player     │            │  未来引擎          │
     │  (MusicKit /     │            │  mpv（Linux radio）│
      │   AVPlayer)      │            │  Audius remote    │
     └──────────────────┘            └───────────────────┘
```

| 组件 | 角色 | 关键约束 |
|---|---|---|
| `lilt serve` | 唯一 server：持有播放引擎、队列与持久状态 | 播放状态、队列、`state.json` 的唯一写入者；持有生命周期锁 |
| `lilt tui` | client：完整手工操作界面 | 通过 Client API 访问一切；不得直接持有 helper；退出不停止播放 |
| `lilt` CLI | client：脚本与 agent 入口 | 稳定 `--json` 输出；幂等命令可安全重试 |
| AI skill | client：自然语言编排 | API 原语 + skill 推理；不做服务端隐式 fallback |
| `lilt-player` | macOS 播放引擎（签名 Swift app） | MusicKit + AVPlayer；内部协议见 [`internals/helper-rpc.md`](internals/helper-rpc.md) |

## 2. 所有权

明确的单一所有权是多 client 同步的前提：

| 关注点 | owner |
|---|---|
| 播放状态、队列、当前 track | `lilt serve`（数据来自 helper 快照） |
| 收藏、最近、最近容器、主题 | `lilt serve`（`state.json` 的唯一写入者） |
| helper 生命周期与重建 | `lilt serve` |
| 授权 flow 生命周期与状态 | `lilt serve`；provider 负责具体交互与凭据 |
| Radio Browser 目录查询与探测 cache | `lilt serve` |
| BrowseNode 呈现、键位、布局、主题渲染 | 各 client（TUI 自行决定） |
| 进度条的平滑插值 | client 本地（不外推 queue/track/status） |

## 3. 一次操作的数据流

以“agent 让 lilt 播放一个歌单，TUI 同步显示”为例：

```text
skill/CLI                server                         helper
   │  playback.play ───────►│
   │                        │  play(ref) ──────────────────►│
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
- `play --shuffle/repeat` 等 compound 操作由 server 分配 `actionEpoch`；仅同 epoch 的
  helper 通知可被合并。外部/媒体键操作有自己的 epoch/origin，始终可观察。

## 4. 来源（Source）与引擎（Engine）

- **Source** 是可浏览、可播放的内容域：已实现的公共 Source 为 `apple-music` 与
  `radio`；`audius` 是契约预留、尚未实现的未来 Source（也是参考真实 integration/E2E
  provider）。
  每个 source 声明能力与可用性，见
  [`client-api/models.md`](client-api/models.md#1-sourcedescriptor)。
- **Engine** 是实际执行播放的后端（macOS 的 `lilt-player`、Audius remote stream
  engine、未来的 mpv）。一个 source 可以由 discovery 与 playback 两部分共同实现。
- 播放严格互斥：任一时刻只有一个活动 playback Source/engine；这不限制多 source 并发
  discovery 或 auth。
- 扩展方式见 [`client-api/extending.md`](client-api/extending.md)，测试分层见
  [`testing/integration.md`](testing/integration.md)。

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
| `state.json` | 收藏、最近、最近容器、主题、上次来源 | [`internals/state.md`](internals/state.md) |
| `internal/builtin` snapshot | 内置电台列表（内嵌 m3u，含 provenance） | [`client-api/extending.md`](client-api/extending.md#2-内置电台) |
| `themes/*.toml` | 主题（沿用 cliamp TOML schema） | [`ui/theme.md`](ui/theme.md) |
| `radio-cache.json` | 可删除的 Radio Browser 探测 cache | [`internals/radio-discovery.md`](internals/radio-discovery.md) |

内置电台 snapshot/list 源自 [cliamp](https://github.com/bjarneo/cliamp)
及 [cliamp.stream](https://cliamp.stream/)，不由 lilt 创建或拥有；provenance
（upstream、list URL、检索时间、SHA-256、免责声明）由 `internal/builtin` 提供并被
测试断言。详见 [`client-api/extending.md`](client-api/extending.md#2-内置电台)。

## 6. 平台与引擎路线

| 平台 | Apple Music | Audius | Radio | 说明 |
|---|---|---|---|---|
| macOS | MusicKit（签名 helper） | 官方 REST + remote stream engine（目标） | AVPlayer | 当前仅 Apple Music/Radio 已实现 |
| Linux | 不支持 | 官方 REST + mpv（future） | mpv（proposed） | 见 [`internals/linux-mpv-engine.md`](internals/linux-mpv-engine.md) |
| 其他 | 预留 | 预留 | 未排期 |

产品路线与范围见 [`product/roadmap.md`](product/roadmap.md)；已知限制见
[`product/limitations.md`](product/limitations.md)。

## 7. 进一步阅读

完整的文档地图与阅读路径见 [`README.md`](README.md)。
