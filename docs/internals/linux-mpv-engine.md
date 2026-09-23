# Tech Design: Linux Radio 播放（mpv IPC 后端）

**Status: implemented（2026-09-22）。** 包为 `internal/mpvplayer`，由
`cmd/lilt/composition_linux.go` 注入；hermetic 测试用假 mpv 进程（`LILT_TEST_FAKE_MPV`），
真实 mpv 走 opt-in E2E（`LILT_MPV_E2E=1`）。

## 决策

跨平台策略是**共享协议与状态层，播放后端按平台实现**，而不是用一套 mpv
实现在所有端共用：

- 共享层（纯 Go）：TUI、state、Radio Browser client、RPC 协议与状态语义。
- macOS 后端：两个签名 helper——`lilt-player` 负责 MusicKit，`lilt-audio` 负责 AVPlayer URL/Radio；
  两个进程分别拥有原生 Now Playing 会话。
- Linux 后端：进程内 mpv IPC，实现 server 的 audio playback driver 边界。

不采用「mpv 统一所有端」的原因：

1. Apple Music 必须走 MusicKit，macOS helper 无论如何都要存在；Radio/Audius 继续使用已经隔离的
   `lilt-audio`，若再引入 mpv 会形成第三套播放进程与生命周期。
2. mpv-everywhere 引入硬依赖（用户必须装 mpv），破坏「Go 二进制 + 自带
   helper」的自包含安装。
3. mpv 不接 `MPNowPlayingInfoCenter`，mac 会失去系统媒体控制集成。
4. 需要按平台替换的只有音频引擎这一小块，重复成本很低；真正值钱的部分
   （协议、状态、TUI）已经在共享。

## Linux 后端架构

不引入独立 helper 进程：mpv 后端是 lilt 进程内的 Go 包（`internal/mpvplayer`
或类似），满足 `internal/server` 的 audio playback driver；TUI/CLI 仍只看 Client API。

### mpv 启动

```sh
mpv --no-config --idle=yes --no-terminal --force-window=no \
    --input-ipc-server=/tmp/lilt-mpv-<uid>-<rand>/mpv.sock \
    --audio-display=no --no-video
```

- 首次播放时懒启动；socket 放进按会话私有、0700 的 runtime 目录，语义对齐
  现有 helper socket 的安全要求。只回答 `status` 不会启动 mpv。
- `--no-config` 是实现在设计中加的：用户的 `mpv.conf`、脚本与窗口行为不得改变 lilt 的请求。
- lilt 退出时发 `quit`，等待退出，超时才 `Kill`；随后删除 runtime 目录。
  没有 mpv 依赖残留实例。
- mpv 二进制解析顺序：`LILT_MPV_PATH` → `PATH` 上的 `mpv`。

### IPC 语义（JSON IPC）

| lilt 操作 | mpv 命令/属性 |
|---|---|
| `RadioPlay(url, name)` | `loadfile <url> replace` |
| `PlayURL(target)` | 同上，带 generation/session 回显 |
| `Pause` / `Resume` | `set_property pause true/false` |
| `Stop` / `radioStop` | `stop`（回到 idle） |
| 状态快照 | observe `pause`、`idle-active`、`eof-reached`、`duration`；`get_property time-pos` 按需查询 |

- 事件流对应 helper 的 `stateChanged` notification：observe 属性变化 → 组装
  `core.PlaybackState{IsLive: true, ...}` → 走 TUI 现有的有序通知通道。
  发布被合并（consumer 慢时只保留最新快照），且 `time-pos` 由 1s 采样器查询，
  与 macOS helper 的采样节奏一致。
- **ICY**：不需要 Linux 专属实现。server 的 `icy.Client` 本来就是纯 Go HTTP，
  对 macOS 与 Linux 都在 `radio.play` 后读流内 `StreamTitle`；Linux 端不另读
  mpv 的 `metadata`，避免两个来源竞争同一对 `streamTitle`/`streamArtist`。
  （本设计早期写的「Linux 用 mpv icy-title」已废弃。）
- 错误传播：`end-file` 的 `error` reason 映射到 `state.Error`（即 `playbackError`）。
  `loadfile` 后有一个**受调用方 deadline 约束的短等待**（默认 2s，且至少给后续
  状态读取留 500ms）：死链在同一命令内同步报错，慢台则以 `buffering` 返回、稍后
  异步转 `playing`。等待不会吃掉命令自身的超时预算。
- `Ended`：`eof-reached`/`end-file reason=eof` 只上报一次，避免同一曲被推进两次。
  这是有限 URL 队列自动续播的依据。
- `loadfile ... replace` 会先给被替换的文件发 `end-file reason=stop`：该事件既不
  置 `idle`（由 observe 到的 `idle-active` 决定），也不结算在途的 load 等待。

### 生命周期与超时

- 每个命令带 bounded context（沿用 `operationTimeout` 语义）。mpv 无响应时不存在 macOS
  那种「transport 永久作废」的串行 MusicKit 约束：下一次操作可以重试。
- mpv 崩溃：`SubscribeState` 的更新流关闭，server 现有的 audio-engine 重建机制
  （`onAudioEngineStreamClosed` → `rebuildAudioEngine`）会在退避后换上新的 mpv 实例；
  Now Playing/最近播放等 UI 状态不受影响。
- `Probe`：Linux 不用 AVFoundation，而是 HTTP GET 等待第一个非空音频字节，并复用 macOS
  helper 的错误码词汇（`unsupported` / `http` / `timeout` / `network`），因此两端对
  `radio.probe` 的答案形状一致。

### 构建隔离

- 用 Go build tags：Darwin composition 提供现有 helper factories；Linux composition 提供 mpv driver。
  `cmd/lilt` 只把平台实现注入 server，不向 TUI 暴露后端接口。
- **已落地**：`cmd/lilt/composition_darwin.go` / `composition_linux.go`；NixOS 开发环境由
  `flake.nix` 提供（`nix develop` / `nix run`），`just build` / `just verify` 在 Linux 上自动跳过
  Swift 部分。`internal/mpvplayer` 本身不带 build tag（纯 Go），所以它的 hermetic 测试在
  macOS CI 上也跑。
- Apple Music 不经过 mpv：Linux composition 另注入 `internal/appleweb`，并由 `internal/linuxengine`
  在 browser 与 mpv 之间保证播放互斥。UI 仍只从 `SourceDescriptor` 派生可用动作。

### 有限 URL 队列

mpv driver 同时实现 `server.URLPlaybackDriver`，所以 Audius 与 Jamendo 的有限队列在 Linux 上
走同一个后端（`URLQueueTransport` 自己管队列、重解析与死链跳过）。
`PlaybackGeneration`/`TransportSessionID` 回显在状态里，被替换的会话会被拒绝，
与 macOS 的会话语义一致。本设计早期把它列为 future，实际随第一期一起完成。

## mpv 缺失时的降级

首次播放找不到 `mpv` 二进制时报 `playback_error`，message 给出安装提示
（nixpkgs `mpv` / `apt install mpv`）；Radio 浏览、收藏、最近播放仍可用，仅播放失败。
不做自动下载。启动时不预检（懒启动），因此没有 mpv 的机器仍能跑 `nix run` 浏览。

## Apple Music on Linux

- Apple Music 已由 `internal/appleweb` 驱动 Apple 自家的 web player，支持页面 catalog、试听、登录后
  全曲；不使用 mpv，也不需要 Linux 上存在原生 MusicKit。
- `internal/linuxengine` 把该 browser transport 与本文件的 mpv transport 组合在 server 的
  `AudioEngine` / `URLPlaybackDriver` 边界后，并在切换前停止旧后端。具体契约见
  [`apple-web-engine.md`](apple-web-engine.md)。

## 验收清单

1. ✅ `nix run` 场景下 lilt 可浏览/搜索/收藏电台，播放经 mpv 出声（真实 mpv E2E：
   `LILT_MPV_E2E=1 go test ./internal/mpvplayer/`）。
2. ✅ ICY 电台标题出现在艺术家位（server 原有 `icy.Client`，两端同一条路径）。
3. ✅ mpv 缺失报安装提示、mpv 被杀后 server 自动重建、`end-file`/`file-loaded` 失败
   都映射到 `playbackError`。
4. ✅ 退出 lilt 后无 mpv 残留进程，也无残留 runtime 目录。
5. ✅ macOS 行为零变化（`composition_darwin.go` 原样搬运，`GOOS=darwin go build/vet` 通过）。
6. ✅ state schema 未动，Linux 与 macOS 交替使用同一 state 文件无迁移。
