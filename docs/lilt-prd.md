# PRD: lilt — Apple Music 桌面控制层

状态：first vertical slice（macOS-first）

目标读者：Guo 与实现者（AI）

---

## 0. As-Is 现状

仓库已有可用的首个纵向切片：Go Bubble Tea/Lip Gloss 搜索/个人歌单界面、会话 socket
和 Swift helper。目标 Mac 上的签名 MusicKit catalog 与个人资料库请求已运行验证；歌曲和
歌单完整播放路径已实现并编译，实际音频输出仍需交互验收。

- 目录：当前仓库 `lilt/`。
- 本 PRD 取代旧草案，旧文件删除。

命名约定：

| 对象 | 名称 |
|---|---|
| 产品 / 项目 | `lilt` |
| 主二进制（CLI + TUI） | `lilt` |
| opencode skill | `lilt` |
| macOS 播放/内容 helper（签名 App bundle） | `lilt-player` |

---

## 1. 核心决策

1. **v1 仅交付 macOS**。Ronin / ZX505 全部放到后续阶段。
2. **TUI 是主前端**，CLI 是同一二进制的非交互入口；两者共享核心逻辑。
3. **AI 通过 opencode skill 包装 `lilt` CLI 控制**，CLI 提供稳定 `--json` 输出。
4. **已授权** macOS 内容与完整播放走原生 MusicKit，不手工维护 Developer Token / Music-User-Token；未授权时用 Apple 公开 iTunes Search API 查找并播放实际提供的 preview asset。
5. **ContentProvider / PlaybackTarget 抽象保留**，但 v1 只实现 macOS 侧。
6. **目标选择：显式优先，多目标要求显式**；只有一个可用 target 时才自动选择，否则报错。

### 1.1 为什么 Mac 上不用 REST

| | MusicKit（Swift 框架） | Apple Music REST API |
|---|---|---|
| 可用平台 | 仅 Apple 平台 | 任意平台 |
| 内容查询 | 框架内建 | 需 Developer Token(JWT) + Music-User-Token |
| 播放 | 内建 `ApplicationMusicPlayer` | 不提供，需官方 App / MusicKit JS |
| Token 管理 | 系统授权，框架内部处理 | 由调用方签发与轮换 |

REST 的唯一理由是 Linux/Ronin 用不了 MusicKit；因此 token 相关工作推迟到 Ronin 阶段。

### 1.2 平台路线

| 阶段 | 平台 | 方案 |
|---|---|---|
| v1 | macOS | Go TUI/CLI + 签名 Swift helper（MusicKit 内容+播放） |
| v2 | Ronin（NixOS） | 跨平台 TUI 复用；内容走 REST，播放 target 为 ZX505 |
| v2 | ZX505（Android 9） | 保持官方 Apple Music App，经 ADB/MPRIS bridge 控制 |
| 不做 | iPhone / 其他 | 继续使用官方 Apple Music |

---

## 2. 用户体验目标

日常只面对 Terminal，三条入口并列：

```sh
lilt tui                              # 主前端
lilt focus                            # 预设
lilt search "Nujabes" --play          # 搜索即播
lilt status --json                    # 供 AI / 脚本消费
```

TUI 形态（示意）：

```text
lilt · mac-native                            ● Playing

› Focus Lofi
  Jazz
  Ambient
  最近播放
  资料库歌单

Nujabes — Aruarian Dance               Lossless
[Space] 暂停  [n] 下一首  [/] 搜索  [t] 切换 target
```

`play` 的**内容选择**与**播放目标**分离：同一预设、URL 或搜索结果的选中项可发给
任意 PlaybackTarget；v1 只有 `mac-native`，但接口与 UI 已按多 target 设计。

---

## 3. 架构

```text
lilt（Go，单二进制）
  ├─ TUI（Bubble Tea / Lip Gloss）
  ├─ CLI（含 --json）
  └─ core
       ├─ ContentProvider（接口）
       ├─ PlaybackTarget（接口）
       └─ TargetResolver（环境探测 + 显式选择）
             │
             │  IPC（私有每会话 Unix socket 上的换行分隔 JSON-RPC）
             ▼
lilt-player（由 LaunchServices 启动的签名 Swift .app bundle）
  ├─ MusicKitContentProvider   search / library / playlists / recent / stations
  └─ MacNativeTarget           ApplicationMusicPlayer + audioVariant
```

设计要点：

- TUI/CLI 不含任何 Apple 私有框架；所有 Apple 能力集中在 Swift helper。
- Go 为每次前台会话创建短路径 `0700` 临时目录，通过 `/usr/bin/open -n -W <app> --args --rpc-socket <path>` 让 LaunchServices 启动新的签名 app 实例。整个 TUI 生命周期都使用该 app 身份，不再直接执行 `Contents/MacOS/lilt-player`。
- app 以 `0600` 绑定该 Unix socket，接受一个 Go host，并顺序处理带数字 request ID 的换行分隔 JSON-RPC。AppKit event loop 始终运行，以保持原生 MusicKit 授权身份与系统 UI。
- helper app 随 TUI 结束而退出；`shutdown` 会暂停 `ApplicationMusicPlayer` 与 preview `AVPlayer`、关闭并删除 socket，再终止这个确切实例。EOF/信号与无 host 启动超时也执行清理。任一 RPC deadline 到期后 Go 会作废 socket 并按该私有连接返回的 PID 终止实例，拒绝迟到结果；不按名称杀进程，后续操作要求重启 lilt。
- TUI 对 secondary CLI 另行暴露固定路径的 Go host Unix socket；它不是 helper RPC socket，两者不能混用，也都不提供 daemon 或 LaunchAgent。

统一播放请求：

```jsonc
{
  "kind": "song|album|playlist|station",
  "id": "…",            // 可选
  "storefront": "us",   // 可选
  "url": "https://music.apple.com/…", // 优先
  "startAt": 0          // 可选
}
```

优先保留 Apple Music URL 作为跨机器、跨实现的 hand-off 格式；不能直接建队列的
target 至少接受 URL。

---

## 4. 接口契约（v1）

### 4.1 ContentProvider

| 方法 | 输入 | 输出 |
|---|---|---|
| `search` | term, limit | item 列表 |
| `libraryPlaylists` | — | 个人歌单列表 |
| `recentPlayed` | limit | 最近播放 |
| `resolveUrl` | url | 规范化 item |
| `stations` | term | 电台候选 |

v1 由 `lilt-player` 用 MusicKit 实现。REST 实现仅在 §9 的 Ronin 阶段加入，接口不变。

### 4.2 PlaybackTarget

| 方法 | 说明 |
|---|---|
| `play` | 接收 `PlaybackRequest` |
| `pause` / `resume` | 暂停 / 继续 |
| `next` / `previous` | 切歌 |
| `state` | 返回 `{ track, position, status, audioVariant }` |

**v1 播放控制只作用于"当前活跃 target"**；不处理多 target 并发或跨 target 自动暂停。

### 4.3 目标选择规则（确定）

1. 命令行/TUI 显式 `--target <name>` → 使用它。
2. 未显式指定时，探测可用 target：
   - 检测到本机 `lilt-player` 可运行且已授权 → `mac-native`。
3. 恰好一个可用 → 自动使用。
4. 多个可用 → **报错**，要求显式指定。
5. 零个可用 → 报错并给出修复提示。

v1 实际上只有 `mac-native`，但规则按上述实现，以便 ZX505 加入后无需回改行为。

---

## 5. macOS 播放细节

`lilt-player` 使用 `ApplicationMusicPlayer`：

```swift
ApplicationMusicPlayer.shared.queue = .init(for: [song])
try await ApplicationMusicPlayer.shared.play()
```

- 需要显式 Bundle ID、开发签名，并在 Apple Developer 后台为 App ID 启用 MusicKit
  App Service；macOS 不使用 `com.apple.developer.music-kit` entitlement。helper 封装为
  无窗口 `.app` bundle，用户仍只从 Terminal 调用 `lilt`。
- 首启由系统弹窗完成 Apple Music 授权；MusicKit 使用当前 macOS 用户已经配置的系统
  Apple Music account。lilt 永不收集 Apple ID/密码，也不读取、持久化或手工维护 Music
  User Token；Fastlane 与 Apple Music 登录无关。
- `open -n -W …lilt-player.app --args --authorize` 可作为显式首次授权入口。正常 TUI
  会话先查询稳定的 `authorized|denied|restricted|not_determined|unknown` 状态；若为
  `not_determined`，CLI 明确提示后由同一个 LaunchServices app 身份调用系统授权请求。
  denied/restricted/未完成授权不会使 TUI 退出，而是保持 preview 模式。
- 未授权时 MusicKit catalog search 会被系统拒绝，因此 helper 降级到无需 token 的 Apple
  iTunes Search API；若结果有 preview asset，则用 `AVPlayer` 播放 preview/demo；
  通常约 30 秒且可能不存在。state 以 `mode: full|preview` 和 `authorization` 明确区分，
  preview 的 `next/previous` 返回稳定 `preview_unsupported`，绝不静默当作完整播放。
- 已授权但 MusicKit 暂时拒绝系统签发的 token 时，搜索和播放也明确降级为 preview，
  避免整个 TUI 不可用；完整播放问题仍通过 helper 日志保留诊断信息。
- 实际音质由系统按曲目、网络、输出设备自动选择，TUI 只显示、不强制：

```swift
ApplicationMusicPlayer.shared.state.audioVariant
// .lossless / .highResolutionLossless / .dolbyAtmos / ...
```

- 可用版本受曲目、系统设置与硬件限制（无损 ≤24/48；Hi-Res ≤24/192 且需对应 DAC；
  Atmos 需曲目与兼容输出）。**不提供"强制 192 kHz / Atmos"开关。**

---

## 6. 预设（Preset）

预设**不预载 Apple Music ID**，而是"命名规则 + 实时解析"：

```toml
# ~/.config/lilt/presets.toml
[focus]
label  = "Focus Lofi"
query  = "lofi focus"
kind   = "station"   # station | playlist | song

[jazz]
label = "Jazz"
query = "jazz classics"
kind  = "playlist"
```

解析流程：读取预设 → 经 ContentProvider（Mac 上即 MusicKit）取电台/歌单候选 →
结合本地播放记录做加权/去重 → 生成 `PlaybackRequest` 交给当前 target。
本地记录（最近播放、使用频次）存于 `~/.local/state/lilt/`，属于辅助信号，缺失不影响可用。

---

## 7. CLI 与 AI 接口

命令面（v1）：

```sh
lilt tui
lilt play <Apple-Music-URL|kind:id> [--target NAME]  # 后续 M2；需已有 TUI
lilt focus [--target NAME]            # TUI 启动模式
lilt search <term> [--play]           # --play 为 TUI 启动模式，选择第一个 song 结果
lilt status
lilt pause | resume | next | previous
lilt target list
```

- `status --json`、`pause --json`、`resume --json`、`next --json`、`previous --json` 使用稳定 envelope：`{"ok":true,"data":...}` 或 `{"ok":false,"error":{"code":"...","message":"..."}}`。不存在 TUI 时错误码固定为 `no_active_session`。
- opencode skill 不启动 TUI；仅可执行上述已有会话控制命令。
- 未支持能力（如 v1 的 `--target zx505`）必须返回明确错误，而非静默降级。
- opencode skill `lilt`：描述命令与 JSON schema，指导 AI 完成"放某首歌/切预设/查状态"。

---

## 8. 范围

### v1 必做

1. `lilt-player` 签名 App bundle：MusicKit 授权、入队播放、暂停/切歌、state/audioVariant 类别。
2. Go 侧：IPC 客户端、core 接口、私有会话 socket、`--json` CLI。
3. ContentProvider（MusicKit）：搜索、资料库歌单、最近播放、电台候选、URL 解析。
4. 预设：`~/.config/lilt/presets.toml` + 实时解析 + 本地记录辅助。
5. TUI（Bubble Tea）：播放控制、搜索、资料库/个人歌单浏览、最近播放、预设触发、
   target 切换。
6. opencode skill `lilt`。

### v1 不做

- Ronin / ZX505 target、REST ContentProvider、token 签发与轮换。
- MusicKit JS / Chromium web 播放。
- 队列编辑（v1 只显示，不编辑）。
- 自建 iOS/Android 播放器；下载、导出、规避 DRM；强制无损/Hi-Res/Atmos。
- 后台续播、daemon、LaunchAgent（退出 TUI 即停止）。
- 修改 Sidra、抓取 Apple Music App UI 或网页 DOM。

---

## 9. 实现阶段

> M0/M1 已开始实现；各阶段仅在对应验收全部通过后视为完成。

### M0：MusicKit 可行性 spike（0.5–1 天）

- 造一个最小签名 `.app`，在 App ID 启用 MusicKit App Service（macOS 不需要额外代码签名
  entitlement），跑通授权。
- 用固定 URL 播放并读取 `state.audioVariant`。
- 验收：本机能播放且能读到真实 audio variant；确认签名/授权链路可用。

### M1：helper、会话与 IPC（约 1 天）

- `lilt-player`：实现 `play/pause/resume/next/previous/state/shutdown` 与 Unix socket 上的换行 JSON-RPC。
- Go 侧 LaunchServices app 生命周期、helper 私有 Unix RPC socket、TUI host socket 管理。
- 验收：运行 `lilt tui` 后，`lilt status --json` 返回当前曲目；`pause/resume/next/previous` 可控制该会话；退出 TUI 后返回 `no_active_session`。

### M2：CLI 核心（约半天）

- core 接口、TargetResolver（§4.3 规则）、统一 `PlaybackRequest`、退出码与错误文本。
- 验收：`lilt play`、`lilt pause|next`、`lilt status` 行为与错误符合 §4。

当前切片：`lilt play <Apple-Music-URL|kind:id>` 已实现，作用于运行中的 TUI 会话；
未指定会话时返回 `no_active_session`。TargetResolver、`--target` 与多 target 探测仍待做。

### M3：ContentProvider（约 1 天）

- helper 内实现 MusicKit `search/libraryPlaylists/recentPlayed/stations/resolveUrl`。
- 验收：`lilt search "Nujabes" --json` 返回可播放结果，`--play` 能播放。

当前切片：`search`、`libraryPlaylists`、`recentPlayed`、`stations`、`resolveUrl` 均已实现，
并提供 `lilt search <term> --json` 与 `lilt recent [limit] --json` 一次性输出。`search`、
`libraryPlaylists` 与完整播放已在签名运行时验证（`mode: full` / `status: playing`）。该系统
上显式 Music User Token 请求返回 `MusicTokenRequestError.unknown`，但不影响 catalog 搜索、
资料库读取与 `ApplicationMusicPlayer` 完整播放；因此 `recentPlayed` 在云端 API 不可用时
改用本地资料库按 `lastPlayedDate` 排序。`focus` 明确报 `not_implemented`，不假装可用；
TUI 的预设展示仍待接入（M4）。

### M4：预设（约半天）

- 预设文件模型、实时解析、本地记录加权。
- 验收：`lilt focus` 不经预载 ID、由查询实时解析并播放。

当前切片：`~/.config/lilt/presets.toml` 解析、`station|playlist|song` 三类实时解析、
`~/.local/state/lilt/state.json` 本地记录候选排序与 `lilt focus` TUI 预设模式均已实现，
并通过单元测试与签名运行时内容验证。

### M5：TUI（约 1–1.5 天）

- Bubble Tea：播放控制、搜索、资料库/个人歌单浏览、最近播放、预设触发、target 切换。
- 验收：TUI 可浏览并播放个人歌单与最近播放，实时显示曲目与 audio variant。

当前切片：TUI 采用 lazygit 风格全屏单列表。顶层为 `SOURCE`（`Tab` 切换 Apple Music /
Radio）和 `VIEW`（`1`–`9` 选择，`[`/`]` 循环）两行；单列表光标。Apple Music 子视图
`Home/Playlists/Recent/Presets`，`/` 搜索为可返回临时页；歌单可 `Enter` 进入曲目详情（`Esc`/`Backspace` 返回）；
Radio 子视图 `Home/Favorites/Recent/Countries/Tags`（Radio Browser），`a` 可添加流 URL。`Now
Playing` 为只读状态带：当前曲目、进度或 `LIVE`、编码、shuffle/repeat 标志与 Apple Music
实时队列。helper 通过有序 `stateChanged` notification 推送快照，TUI 仅用 250ms 重绘计时器插值进度，不轮询。主列表中 `Enter`/`p` 播放，`x` 无操作；聚焦 Up Next 后 `Enter`/`p` 跳转、`x`
移除、`J`/`K` 重排、`c` 清空。广播与 Apple Music 严格互斥。支持播放/暂停/切歌/停止、`s`/`R`、
`e`/`E` 入队、`f` 收藏、`t` 主题、`?`/`i`/`t` 弹层与自动消失 toast。公开 MusicKit 不提供
实时 bitrate 与 seek/音量；`audioVariant` 可能为空，此时显示 `Auto` 并列出可用编码。

### M6：AI skill 与打磨（约半天）

- opencode skill `lilt`、JSON schema 文档、错误提示统一。
- 验收：AI 能通过 skill 完成"放某预设 / 搜某歌并播放 / 查询状态"。

---

## 10. 验收矩阵

| 场景 | 预期 |
|---|---|
| `lilt focus` | 启动 Focus TUI 模式；M4 后 Mac 原生 MusicKit 开始播放 |
| `lilt search "Nujabes" --play` | 启动搜索播放 TUI 模式；M3 后选择第一个 song 结果播放 |
| 浏览个人歌单并播放 | 资料库返回歌单，可整单入队播放 |
| 最近播放 | 显示并可重播 |
| `--json` | 输出稳定 JSON，退出码语义明确 |
| 多 target 可用且未指定 | **未实现/后续里程碑**；当前只有本地 macOS target |
| `--target zx505`（v1） | **未实现**；当前 CLI 不接受 `--target`，不会静默切换远端 |
| AI skill | 在 TUI 会话运行时，能通过 CLI 完成播放/搜索/查询 |
| 无运行中 TUI | `status/pause/resume/next/previous --json` 返回 `no_active_session` |
| TUI 退出 | helper 被终止、socket 被删除且播放停止 |

---

## 11. 风险与处理

| 风险 | 处理 |
|---|---|
| MusicKit 签名/授权链路不通 | M0 前置 spike 验证。macOS 不使用 `com.apple.developer.musickit`；靠 App ID 的 MusicKit Service + `NSAppleMusicUsageDescription` + 系统授权 |
| `music-kit` 授权被用户在系统层撤销 | helper 返回明确错误码；CLI/TUI 提示重新授权 |
| helper app 崩溃或残留 | socket EOF 使 TUI 报错；退出优先发 `shutdown`，再只按私有握手返回的 PID 兜底，避免误杀其他实例；M1 不自动重启 |
| **Music User Token 返回 `.unknown`** | **已接受为已知限制**：只影响 For You/云端最近播放；最近播放回退本地资料库。详见 [`spec/limitations.md`](spec/limitations.md) |
| MusicKit 电台/候选不如 REST 丰富 | 预设允许多种 `kind`；必要时回退到 playlist/搜索 |
| helper 中途断开 | notification EOF 冻结进度并显示退出/重启指引；M1 不自动重启 |
| TUI 异步 IPC 返回乱序 | 请求绑定 generation + 目标页面身份；过期结果不得覆盖页面或清除 loading |

---

## 12. 跨端规范（v2）

v2 的跨端契约见 [`docs/spec/`](spec/)：

- `sources.md`：来源（Apple Music / Radio）与浏览树、可播放项、稳定 id 方案。
- `state.md`：本地优先状态 schema（收藏/最近/预设/主题/上次来源），同步留口。
- `rpc.md`：helper JSON-RPC 方法与 `State` 形状。
- `ux.md`：布局、键位、加载/错误/toast/弹层。
- `theme.md`：主题 TOML（沿用 cliamp schema）。
- `limitations.md`：已接受的已知限制（含 Music User Token 不可用）。

原则：**不共享代码，共享规范**。各端用各自语言实现，数据与操作一致，**音质不作承诺**。

引擎：
- `musickit`（macOS/iOS）：原生 MusicKit + AVPlayer（广播）。
- `web`（Linux/Windows，v2 不实现，仅预留）：内嵌 Chromium + MusicKit JS + Widevine；仅 AAC 256、需网页登录，明确标注限制与风险。
- 广播与 Apple Music **严格互斥**；队列只属 Apple Music。

---

## 13. 后续（不属 v2）

- Ronin（NixOS）：`web` 引擎实现（持久 Chromium + localhost MusicKit JS）+ 广播原生播放；`zx505` target 复用既有 `bridge.py` / `zx505.nix`。
- 手机端：按 `docs/spec/` 用原生实现（iOS MusicKit / Android 官方 App 或 web）。
- 后台续播、状态云同步。
- 最近播放：REST `v1/me/recent/played` 与 MusicKit 结果对齐 / 缓存兜底。

---

## 14. 待决问题

1. 状态云同步的合并策略（见 `spec/state.md` 的留口）。
2. 预设文件是否允许用户手写 Apple Music URL 或 `kind:id` 作为高级覆盖。
3. ICY 电台当前曲目的取法（旁路 `Icy-MetaData`）是否纳入 v2。
