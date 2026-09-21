# Spec: Audio helper 拆分（MusicKit / AVPlayer 双 helper）

> **状态：已实现。** Audius/Radio 的 AVPlayer 播放可在 macOS
> Now Playing（控制中心/锁屏/媒体键）显示标题与封面。

## 1. 问题

现在只有一个 Swift app（`lilt-player`）同时链接 **MusicKit** 和 **AVFoundation**：

- Apple full：MusicKit 自己管理 `MPNowPlayingInfoCenter`，标题/艺人/封面正常。
- Audius/Radio（AVPlayer）：手写 `MPNowPlayingInfoCenter` 被同进程的 MusicKit 会话覆盖，
  控制中心只剩 app 名与按钮、无元数据。

根因是 **Now Playing 会话按进程归属**，MusicKit 与 AVPlayer 不能在同一进程里各自拥有。

## 2. 设计原则

播放天然互斥（`docs/internals/providers.md`）：同一时刻只有一个 source 在播，切换 source 会
整体停掉旧 source。因此可以把“按进程归属”直接映射为“按播放引擎拆分”：

- **`lilt-player`（MusicKit）**：只处理 Apple full/preview + discovery/queue。
- **`lilt-audio`（AVPlayer，不链接 MusicKit）**：只处理 Audius URL 队列与 Radio 流。
- 任一时刻只有一个 helper 在实际播放；切换 source 时停掉并**终止**另一个 helper，
  释放其 Now Playing 会话。

## 3. 进程与 RPC

复用私有 helper RPC 协议（[`helper-rpc.md`](helper-rpc.md)），但按能力拆分方法：

| helper | 方法 |
|---|---|
| `lilt-player`（MusicKit） | `ping`、`authorize`、`diagnose`、`libraryPlaylists`、`libraryAlbums`、`recommendations`、`playlistTracks`、`albumTracks`、`search`、`searchAlbums`、`searchPlaylists`、`recentPlayed`、`stations`、`resolveUrl`、`play`、`enqueue`、`queueJump`、`queueRemove`、`queueMove`、`queueClear`、`pause`、`resume`、`next`、`previous`、`setShuffle`、`setRepeat`、`state`、`subscribeState`、`unsubscribeState`、`shutdown`（另有调试用 `debugAlbumSongs`） |
| `lilt-audio`（AVPlayer） | `ping`、`urlPlay`、`urlStop`、`radioPlay`、`radioStop`、`radioProbe`、`pause`、`resume`、`stop`、`state`、`subscribeState`、`unsubscribeState`、`shutdown` |

- `lilt-audio` **不 import MusicKit**；它独占 `MPNowPlayingInfoCenter` + `MPRemoteCommandCenter`，
  因此标题/艺人/封面可用。
- `urlPlay` 参数新增可选 `artworkURL`（Audius 的 artwork；Radio 无）。audio helper 拉取封面并
  经 `MPMediaItemArtwork` 设置；失败则不显示封面。
- helper 只暴露自己声明的方法；调用未声明方法返回稳定错误（`unknown_command`）。
- **媒体失败必须上报**：audio helper 观察 `AVPlayerItem.status == .failed`、
  `AVPlayerItemFailedToPlayToEndTime` 与（作为兵底的）每秒时间观察器；失败时写 `playbackError`
  且 `status = "error"`。卡死且从未失败的情况由 server 兜底：URL 队列在无进展（position 不前进）
  超过 20s 时走与媒体失败相同的重试路径，重试仍无进展则结束会话并发布 `server.warning`。
  之前只依赖 `AVPlayer.timeControlStatus` 映射，死链会永远表现为 `buffering`、`playbackError: null`。

## 4. Server 路由

- Server 持有两个可选 helper：`musicEngine`（MusicKit）与 `audioEngine`（AVPlayer）。
- Transport 映射：`transportEngine` → music，`transportURLQueue`（Audius URL 队列）与新增
  `transportStream`（Radio 流）→ audio。
- **Audius** 仍由 server 侧 `URLQueuePlan` 管理队列，只是在每首启动时调用 audio helper 的
  `urlPlay`（`URLPlaybackDriver` 指向 audio）。
- **Radio** 从现在的 `engine.RadioPlay` 改到 audio helper 的 `radioPlay`（新增
  `StreamPlaybackDriver`）。
- 启动/停止：按需启动目标 helper；`beginPlaybackStartLocked` 提交新 source 时，停掉并
  `shutdown` 另一个 helper（幂等）。helper 崩溃重建逻辑对两者一致（沿用
  `engine_supervisor` 的重建 + `server.warning`/`engine.restarted`）。

## 5. Now Playing 归属

- music helper：full 模式**不写** `MPNowPlayingInfoCenter`（交给 MusicKit）；其余模式它不再处理。
- audio helper：写 `MPNowPlayingInfoCenter`，含 title/artist/duration/live/`artworkURL` 封面，
  `playbackState` 随播放状态；`pause/resume/stop` 走 `MPRemoteCommandCenter`。
- 切换 source 终止另一 helper → 其 Now Playing 会话随之释放，不会串台。

## 6. 构建与分发

- `player/project.yml` 新增 target `LiltAudio`（`com.caiguo.lilt-audio`，`PRODUCT_NAME lilt-audio`，
  `LSUIElement`，**无 MusicKit**），共享 `LiltPlayerLogic`（纯逻辑，无 MusicKit）与
  `LiltHelperKit`（RPC 载荷值类型：`JSONValue`/`RPCRequest`/`State`/`HelperTrack` 等）。
  两个 app 各自的 socket server 仍按结果枚举/服务接口分开实现，避免 MusicKit 类型（如 `Track`）
  与共享类型冲突。
- `player/scripts/build-app.sh` 一并构建两个 app 并打印其路径。
- 运行时定位：`LILT_PLAYER_PATH`（现有）+ `LILT_AUDIO_PATH`（新增，缺省
  `player/Build/Products/Release/lilt-audio.app`）。
- `just release` 的 tarball 同时包含 `lilt-player.app` 与 `lilt-audio.app`；Homebrew formula 安装两者
  并注入两个路径。

## 7. 测试

- Server：新增路由测试（Audius→audio、Radio→audio、Apple→music），切换时终止另一 helper；
  audio helper 的 `urlPlay` 携带 `artworkURL`。
- Swift：`LiltPlayerLogic` 保留可测纯逻辑；audio helper 的 AVPlayer/Now Playing 代码保持薄。
- 真机：Audius 与 Radio 播放时控制中心显示标题（Audius 含封面）；切回 Apple 后 MusicKit 接管。

## 8. 影响与风险

- 需要新增一个签名 app 与打包路径（release/CI 同步更新）。
- Server 的 engine supervisor 从“单 helper”变成“双 helper”，生命周期与重建需覆盖两种。
- 现有 `live_test`/`helper-rpc.md` 中 `radioPlay/urlPlay` 归属需迁移到 audio helper 文档。

## 9. 非目标

- 不为 Audius/Radio 增加队列能力（仍由 server 侧 URL 队列管理）。
- 不改变公开 Client API 形状；`artworkURL` 只在私有 helper 协议内。
