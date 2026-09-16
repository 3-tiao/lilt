# Spec: Known Limitations（已知限制）

本文件记录**已接受**的限制：有意取舍、平台限制、或经排查后确认无法在本地修复的问题。
这些不是 bug；实现时不要反复尝试绕过。

## 1. Music User Token 不可用（macOS，已接受）

**症状**：显式请求用户令牌失败：
```
DefaultMusicTokenProvider().userToken(for:options:) -> MusicTokenRequestError.unknown
```
`MusicRecentlyPlayedRequest` 与 `MusicPersonalRecommendationsRequest` 也以同样的
`.unknown` 失败。

**排查证据**（`lilt doctor` / 实测）：
- Developer Token 有效：kid `PWMR05QYGW`、team `9Y6KG228YM`，storefront US/CN 均 HTTP 200。
- `MusicSubscription.current` 正常：`canPlayCatalogContent=true`、`hasCloudLibraryEnabled=true`。
- 资料库歌单、歌单曲目（`Playlist.with([.entries])`）、`ApplicationMusicPlayer` 完整播放均正常。
- 加 `com.apple.developer.musickit` 会被 Xcode 拒绝（该 key 在 macOS 无效；社区同结论）。
- 通过 Keychain Sharing 强制生成开发描述文件后，user token 仍为 `.unknown`；描述文件中也没有
  任何 MusicKit entitlement（macOS 不存在）。

**结论**：本机 macOS 环境下，第三方独立 app 无法取得 Music User Token；并非 entitlement 或
描述文件缺失所致。未验证的剩余选项：为该 bundle id 建 App Store Connect app record（成功率评估偏低）。

**影响与降级**：
- 受影响：仅"按用户的云端"接口——For You / 个人推荐、云端最近播放、`v1/me`。
- 不受影响：目录搜索、资料库歌单、歌单曲目、完整播放、广播、队列、shuffle/repeat。
- 最近播放：回退为**本地资料库**按 `lastPlayedDate` 排序（`recentPlayed` RPC 内部降级）。
- For You：不提供。

## 2. 无实时 bitrate / seek / 音量

- 公开 MusicKit 不暴露实时码率；`AudioVariant` 在部分环境可能为空，此时 `Format` 显示 `System-selected`，
  不能判断实际播放 AAC 或 ALAC；曲目可用编码仅在 `Track Info` 中显示。
- `ApplicationMusicPlayer.playbackTime` 只读，无公开 seek；音量由系统控制。故 TUI 不提供 seek/音量。
- 后果：不做"强制无损/Hi-Res/Atmos"或任意跳转。

Apple 的「喜爱歌曲」以本地化名称匹配后倒序显示及播放；MusicKit 没有歌单类型标记，若 Apple 改名，此尽力而为的处理可能失效。

## 2b. 收藏为 lilt 本地列表（不写 Apple Music）

- MusicKit 公开 API **没有** favorite/loved 的读写（本机 SDK 实证 0 匹配）；`MPMediaLibrary`/`MPMediaQuery`
  在 macOS 头文件中标为 `API_UNAVAILABLE(macos)`。因此 `f` 只能维护 lilt 本地列表
  （`favorites.appleMusic` / `favorites.radio`），AM 收藏视图标题为 `Favorites · local`。
- Apple 的官方收藏只能**间接只读**：以「喜爱歌曲」智能歌单呈现（只含歌曲、只读、名称本地化），
  该歌单在 Playlists 中可见可播；lilt 不做逐项 favorite 标志读取。
- 已评估并放弃：用 AppleScript/ScriptingBridge 读写 Music.app 的 `favorited` 属性。原因：需要
  Automation(TCC) 授权与拒绝降级、依赖 Music.app 运行与同步、按 id 查找脆弱、CI 无法覆盖，
  且写操作会真实修改用户 Apple 账号数据（跨设备同步），副作用远重于本地标记；Radio 侧永远只能本地，
  会造成 `f` 语义按来源分叉。

## 3. 无频谱可视化

选中 AVPlayer 作为广播引擎，稳定性优先，放弃了真频谱（无法取 PCM，且 Apple Music 本就不透明）。
如需可视化，仅做基于播放状态的动态视觉，不声称频谱。

## 4. Linux / 手机未实现（规范预留）

- 原生 MusicKit 仅 Apple 平台。Linux 若要播放 Apple Music，只能是内嵌 Chromium + MusicKit JS +
  Widevine，**AAC 256、需网页登录、依赖 Apple 网页播放不被打掉**。
- v2 不实现 Linux / 手机；`docs/spec/` 定义引擎（`musickit` / `web` / `native-mobile`）与数据
  schema 作为跨端契约。"无缝"承诺限定为**数据与操作**，不含音质。
- Linux 的 **Radio** 播放有 proposed 设计：进程内 mpv IPC 后端
  （[`linux-mpv-engine.md`](linux-mpv-engine.md)），等 Linux 机器到位后实现；
  Linux Apple Music 播放仍不在范围。

## 5. 外部依赖

- Radio Browser 为无可用性保证的社区服务。当前实现以 `de1.api.radio-browser.info` 为主、
  `de2.api.radio-browser.info` 为静态回退（每个请求 7s 超时，主镜像失败后按序尝试下一个），
  全部失败时在 Browse 显示「Radio directory unavailable — check your connection, then retry」并记入日志；
  动态镜像发现（SRV）仍保留在 proposed [`radio-discovery-health.md`](radio-discovery-health.md)。
- 自定义流地址（`a`）通过流自身的 `icy-name` 头解析电台名，失败则保留原始地址。
- 广播播放走 AVPlayer，可达性探测走 URLSession HTTP 首字节；ATS 只启用 `NSAllowsArbitraryLoads`（与 `…ForMedia` 等更窄的 ATS 键并存时全局键会被系统忽略），以放行任意 http/https 电台 URL。
- AVPlayer item/status failure 会通过 `playbackError` 显示；由于重连策略依流而异，lilt 不自动重连。

## 6. 部分公开连续流不兼容 AVPlayer（已接受）

**症状**：个别公开电台的普通 `GET` 可返回音频，但 macOS `AVPlayer` 在缓冲或播放中请求
HTTP `Range` 后失败。例如 `https://www.getsubwave.com/stream.mp3`：无 Range 请求返回
`200 audio/mpeg` 和 `ICY-Name: SUB/WAVE`，而 `Range: bytes=0-4095` 返回 `404`；播放器最终显示
`Audio failed to load`。

**结论**：URL 可达或首字节 probe 健康不等于 AVPlayer 可播放。lilt 继续使用 AVPlayer 作为 macOS
Radio 的原生播放后端，并在失败时展示具体错误；不把此类源伪装为网络断开。

**当前取舍**：不为第一个已知样本引入 FFmpeg normalizer、mpv 或本机代理。它们会增加打包、签名、
许可证、进程生命周期与额外延迟的长期成本，须在出现更多不兼容公开流后再评估。未来若实现，方案是
仅对已确认 Range 不兼容的连续音频做平台专属 fallback，HLS 与标准流仍直接使用 AVPlayer。
