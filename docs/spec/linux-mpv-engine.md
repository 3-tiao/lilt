# Tech Design: Linux Radio 播放（mpv IPC 后端）

**Status: proposed，未实现。等拿到 Linux 机器后开工。**

## 决策

跨平台策略是**共享协议与状态层，播放后端按平台实现**，而不是用一套 mpv
实现在所有端共用：

- 共享层（纯 Go）：TUI、state、Radio Browser client、RPC 协议与状态语义。
- macOS 后端：现有签名 helper（MusicKit + AVPlayer）。helper 因 Apple Music
  必须存在，电台播放继续搭它的便车，并保留原生 Now Playing 集成。
- Linux 后端：进程内 mpv IPC，实现同一个 `core.PlaybackTarget` 接口。

不采用「mpv 统一所有端」的原因：

1. Apple Music 必须走 MusicKit，macOS helper 无论如何都要存在；若 mac 电台也
   用 mpv，会出现两个播放进程（MusicKit helper + mpv），复杂度上升。
2. mpv-everywhere 引入硬依赖（用户必须装 mpv），破坏「Go 二进制 + 自带
   helper」的自包含安装。
3. mpv 不接 `MPNowPlayingInfoCenter`，mac 会失去系统媒体控制集成。
4. 需要按平台替换的只有音频引擎这一小块，重复成本很低；真正值钱的部分
   （协议、状态、TUI）已经在共享。

## Linux 后端架构

不引入独立 helper 进程：mpv 后端是 lilt 进程内的 Go 包（`internal/mpvplayer`
或类似），直接满足 `internal/player` 的 `Player`/`core.PlaybackTarget` 接口。

### mpv 启动

```sh
mpv --idle=yes --no-terminal --force-window=no \
    --input-ipc-server=/tmp/lilt-<uid>/mpv.sock \
    --audio-display=no --no-video
```

- 首次播放时懒启动；socket 放进按会话私有、0700 的 runtime 目录，语义对齐
  现有 helper socket 的安全要求。
- lilt 退出时发 `quit`，残留实例用启动握手返回的 PID 兜底清理。

### IPC 语义（JSON IPC）

| lilt 操作 | mpv 命令/属性 |
|---|---|
| `RadioPlay(url, name)` | `loadfile <url>`；`observed icymetadata` |
| `Pause` / `Resume` | `set_property pause true/false` |
| `Stop` / `radioStop` | `stop`（回到 idle） |
| 状态快照 | observe `pause`、`time-pos`、`idle-active`、`metadata` |

- 事件流对应 helper 的 `stateChanged` notification：observe 属性变化 → 组装
  `core.PlaybackState{IsLive: true, ...}` → 走 TUI 现有的有序通知通道。
- **ICY 加分项**：mpv 暴露 `icy-title`（电台当前曲目），AVPlayer 在 macOS 上拿
  不到；Linux 端把它显示在艺术家位，正好符合 ux.md 的 Radio 语义。
- 错误传播：loadfile 后观察 `idle-active`/`file-loaded`，加载失败映射为现有
  `playbackError` 文案，不静默失败。

### 生命周期与超时

- 每个命令带 bounded context（沿用 `operationTimeout` 语义）；mpv 无响应时
  只终止 mpv 实例并报错，不存在 macOS 那种「transport 永久作废」的串行
  MusicKit 约束，可以安全重启后端重试。
- mpv 崩溃：下次操作时检测 socket EOF，自动重启实例；Now Playing/最近播放
  等 UI 状态不受影响。

### 构建隔离

- 用 Go build tags：`player_darwin.go` 走现有 helper；`player_linux.go` 走
  mpv 后端。`cmd/lilt` 的启动分支只看 `core.PlaybackTarget` 接口。
- Apple Music source 在 Linux 构建中禁用（无 MusicKit）：隐藏 Tab 的
  Apple Music 侧或显示不可用说明；Radio 功能与状态 schema 完全一致。

## mpv 缺失时的降级

启动或首次播放检测不到 `mpv` 二进制时，给出明确错误与安装提示
（nixpkgs `mpv` / `brew install mpv`），Radio 浏览、收藏、最近播放仍可用，
仅播放失败。不做自动下载。

## Apple Music on Linux

- 正式播放：不支持（无 MusicKit），维持 README/spec 现状。
- 可选加值（以后再议）：公开 iTunes Search API 返回 30s preview URL，可
  复用现有 iTunesSearch 逻辑让 Linux 跑 preview-only 模式；不在本设计的
  第一期范围。

## 验收清单（实现时）

1. `nix run` 场景下 lilt 可浏览/搜索/收藏电台，播放经 mpv 出声。
2. ICY 电台标题出现在艺术家位。
3. mpv 缺失/崩溃/网络断开都有可恢复的错误路径。
4. 退出 lilt 后无 mpv 残留进程。
5. macOS 行为零变化（build tag 隔离，不触碰 darwin 后端）。
6. state schema 双向兼容：Linux 与 macOS 交替使用同一 state 文件无迁移。