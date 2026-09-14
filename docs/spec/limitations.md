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

- 公开 MusicKit 不暴露实时码率；`AudioVariant` 在部分环境可能为空，此时 `Format` 显示 `Auto`，
  由 `Available` 列出曲目可用编码。
- `ApplicationMusicPlayer.playbackTime` 只读，无公开 seek；音量由系统控制。故 TUI 不提供 seek/音量。
- 后果：不做"强制无损/Hi-Res/Atmos"或任意跳转。

Apple 的「喜爱歌曲」以本地化名称匹配后倒序显示及播放；MusicKit 没有歌单类型标记，若 Apple 改名，此尽力而为的处理可能失效。

## 3. 无频谱可视化

选中 AVPlayer 作为广播引擎，稳定性优先，放弃了真频谱（无法取 PCM，且 Apple Music 本就不透明）。
如需可视化，仅做基于播放状态的动态视觉，不声称频谱。

## 4. Linux / 手机未实现（规范预留）

- 原生 MusicKit 仅 Apple 平台。Linux 若要播放 Apple Music，只能是内嵌 Chromium + MusicKit JS +
  Widevine，**AAC 256、需网页登录、依赖 Apple 网页播放不被打掉**。
- v2 不实现 Linux / 手机；`docs/spec/` 定义引擎（`musickit` / `web` / `native-mobile`）与数据
  schema 作为跨端契约。"无缝"承诺限定为**数据与操作**，不含音质。

## 5. 外部依赖

- Radio Browser（`de1.api.radio-browser.info`）为社区服务，可能不可用；失败以错误提示呈现，
  不阻塞其它来源。
- 广播走 AVPlayer，`NSAllowsArbitraryLoadsForMedia` 放行 http 媒体流。
