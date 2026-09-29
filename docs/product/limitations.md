# Spec: Known Limitations（已知限制）

本文件记录**已接受**的限制：有意取舍、平台限制、或经排查后确认无法在本地修复的问题。
这些不是 bug；实现时不要反复尝试绕过。

## 1. Apple 个性化请求在本机失败（macOS，已接受）

**症状**：显式请求用户令牌失败：
```
DefaultMusicTokenProvider().userToken(for:options:) -> MusicTokenRequestError.unknown
```
`MusicRecentlyPlayedRequest` 与 `MusicPersonalRecommendationsRequest` 也以同样的
`.unknown` 失败。

**排查证据**（2026-09-18，`lilt doctor` / 签名 helper 实测）：
- Developer Token 有效：kid `PWMR05QYGW`、team `9Y6KG228YM`，storefront US/CN 均 HTTP 200。
- `MusicSubscription.current` 正常：`canPlayCatalogContent=true`、`hasCloudLibraryEnabled=true`。
- 资料库歌单、歌单曲目（`Playlist.with([.entries])`）、`ApplicationMusicPlayer` 完整播放均正常。
- A/B：移除强制 `MusicDataRequest.tokenProvider` 的 custom provider 后，推荐和云端最近播放仍为
  `.unknown`；资料库访问保持正常。此前 custom provider 仅强制 developer token 使用
  `.ignoreCache`，现已删除，恢复 MusicKit 默认的自动 token 管理。
- 对 `DefaultMusicTokenProvider` 的显式 user-token 请求，`ignoreCache` 开/关都为 `.unknown`。
- 使用同一 Team、bundle ID 与签名身份构建的独立最小 macOS app（无 lilt server、RPC、helper
  provider）得到相同结果：authorization、subscription、country code、library 与 developer token
  均成功；显式 user token、personal recommendations 与 cloud recently played 均失败。三项错误都是
  `MusicKit.MusicTokenRequestError` code `0`，`userInfo` 为空且没有 underlying error。
- explicit App ID 已启用 MusicKit App Service；Apple 对原生 MusicKit 的配置说明只要求该服务与
  target bundle ID 一致。签名 app 不携带 MusicKit entitlement。

| 条件 | library / playlist tracks | recommendations | cloud recent | 显式 user token |
|---|---|---|---|---|
| custom provider（developer token 强制 `.ignoreCache`） | 成功 | `.unknown` | `.unknown` 后回退到 library recent | `.unknown`（`.ignoreCache`） |
| 默认 `MusicDataRequest` provider | 成功 | `.unknown` | `.unknown` 后回退到 library recent | `.unknown`（`.ignoreCache`） |
| 默认 provider，显式 token 不使用 `.ignoreCache` | — | — | — | `.unknown` |

**结论**：当前签名 bundle 在这台 macOS 机器上无法通过显式 API 取得 Music User Token，两个个性化
请求也失败；证据排除了该 custom provider 与 developer-token cache 策略。资料库请求仍成功，而 Apple
说明原生 MusicKit 会自动管理用户令牌，因此这里**不能**断言 Music User Token 整体不可用，也不能外推为
所有第三方 macOS app 都不支持。独立 app 复现进一步排除了 lilt 的 server、RPC 与业务请求封装。
`.unknown` 未提供可诊断根因；Apple 的原生 MusicKit 配置说明未把 App Store Connect app record 列为
前置条件，本项目也没有证据表明它会改变结果。下一项有效隔离实验是为新 explicit App ID 启用 MusicKit
App Service 后运行同一最小 app；该步骤需要在 Developer Portal 手工配置，尚未执行。

**影响与降级**：
- 受影响：仅"按用户的云端"接口——For You / 个人推荐、云端最近播放、`v1/me`。
- 不受影响：目录搜索、资料库歌单、歌单曲目、完整播放、广播、队列、shuffle/repeat。
- 最近播放：`recent.list` 始终是 **lilt 本地 playback history**，不读取或伪装 Apple
  library 的 `lastPlayedDate`。将来的 provider recently-played 必须另设命令/模型。
- For You：不提供。

## 2. 无实时 bitrate / seek / 音量

- 公开 MusicKit 不暴露实时码率；`AudioVariant` 在部分环境可能为空，此时 `Format` 显示 `System-selected`，
  不能判断实际播放 AAC 或 ALAC；曲目可用编码仅在 `Playback Info` 中显示。
- `ApplicationMusicPlayer.playbackTime` 只读，无公开 seek；音量由系统控制。故 TUI 不提供 seek/音量。
- 后果：不做"强制无损/Hi-Res/Atmos"或任意跳转。

Apple 的「喜爱歌曲」以本地化名称匹配后倒序显示及播放；MusicKit 没有歌单类型标记，若 Apple 改名，此尽力而为的处理可能失效。

## 2b. 收藏为 lilt 本地列表（不写 Apple Music）

- MusicKit 公开 API **没有** favorite/loved 的读写（本机 SDK 实证 0 匹配）；`MPMediaLibrary`/`MPMediaQuery`
  在 macOS 头文件中标为 `API_UNAVAILABLE(macos)`。因此 `f` 只能维护 lilt 本地列表（Activity
  SQLite store，见 [`internals/persistence/local-activity.md`](../internals/persistence/local-activity.md)）。
- Apple 的官方收藏只能**间接只读**：以「喜爱歌曲」智能歌单呈现（只含歌曲、只读、名称本地化），
  该歌单在 Playlists 中可见可播；lilt 不做逐项 favorite 标志读取。
- 已评估并放弃：用 AppleScript/ScriptingBridge 读写 Music.app 的 `favorited` 属性。原因：需要
  Automation(TCC) 授权与拒绝降级、依赖 Music.app 运行与同步、按 id 查找脆弱、CI 无法覆盖，
  且写操作会真实修改用户 Apple 账号数据（跨设备同步），副作用远重于本地标记；Radio 侧永远只能本地，
  会造成 `f` 语义按来源分叉。

## 3. 无频谱可视化

选中 AVPlayer 作为广播引擎，稳定性优先，放弃了真频谱（无法取 PCM，且 Apple Music 本就不透明）。
如需可视化，仅做基于播放状态的动态视觉，不声称频谱。

## 4. Apple Music 浏览器模式与手机端边界

- 原生 MusicKit 仅 Apple 平台。Linux 的 Apple Music 走 **Apple 自家 web player 的浏览器引擎**；macOS
  默认仍走签名 MusicKit helper，仅在设置 `LILT_APPLE_ENGINE=browser` 时使用同一浏览器引擎
  （见 [`../internals/playback/apple-web-engine.md`](../internals/playback/apple-web-engine.md)）：catalog 每次从页面
  `storefrontId` 取得当前 storefront，播放使用同一页面上下文。**资料库、个人歌单、目录电台与
  shuffle/repeat 不可用**（web player 的 catalog API 不暴露资料库，服务端队列不提供 shuffle/repeat）；
  推荐可用。
- 未登录时每项起播为约 90 秒试听（时长由 Apple 的 web player 决定，实测 1:30）；登录且当前 storefront
  有全曲播放权时为全曲。授权在**每个 item 起播时**实时读取：未登录报 `preview`；已登录先报
  `unverified`，目录完整时长与当前媒体时长核对后才报 `preview` 或 `full`。目录时长不可用时
  保持 `unverified`，不冒充全曲；不会把建队列时的旧授权/上首曲目的判定冻结到后续曲目。
- **storefront 跟随登录账号**：页面初始区跟随浏览器（新 profile 默认 `us`）；授权 settled 后引擎读取
  `/v1/me/storefront`，与页面区不一致时把页面导航到账号区（catalog 与全曲播放权对齐），最多重试 2 次，
  失败后继续在原区尝试播放（此时全曲权可能缺失，实际可能只有 90 秒 preview，公开 mode 依媒体时长核验）。未登录不导航。
  订阅区域与页面区不一致是 browser 模式下“登录了却只有 90 秒”的最常见原因。
- `lilt auth apple-music` 打开 Apple 自己的登录页，lilt 看不到凭据。登录窗口与播放浏览器必须独占同一个
  profile，因此开始登录会显式停止当前 Apple 播放并发布 `stopped`，登录后不自动重播。
- 默认 profile 是机器级目录：Linux 为 `XDG_DATA_HOME/lilt/apple-browser`（通常为
  `~/.local/share/lilt/apple-browser`），macOS 为 `~/Library/Application Support/lilt/apple-browser`；
  不随 `LILT_STATE` 派生，可用 `LILT_APPLE_PROFILE` 覆盖。
  只有 lilt 原子创建目录时才写 ownership marker。既有外部 profile 可复用，但永不补写 marker、也永不被
  disconnect 删除；不存在的目录可重复 disconnect。运行中的 Chromium 持有同级 `<profile>.lock` 的非阻塞
  `flock`，同一 profile 不能被两个 server 同时使用。
- server 会后台预热已有 profile，并发布结算后的实时授权状态；尚无 profile 时保持 lazy。`Describe` 本身
  不冷启动浏览器，也不持久缓存上次授权结果，所以会话尚未运行时保守报告 `not_determined`。
- **代价与依赖**：需要带 Widevine 的 Chromium；macOS 的 Google Chrome 自带 Widevine，Linux/NixOS 上用
  `NIXPKGS_ALLOW_UNFREE=1 nix develop .#apple`，非 Nix 环境自行提供。浏览器实测 PSS 632 MiB
  （mpv 76 MiB）。每次浏览器启动都会在页面内探测 Widevine；负向结果使 descriptor 不声明
  `playback.full` 并给出原因，不能仅凭找到 Chromium 就承诺全曲。Apple 的登录 cookie 是会话 cookie，
  所以必须使用 `--restore-last-session`。
- Apple 改自己的 web player 就会破；程序化驱动它不在 MusicKit JS 公开条款覆盖范围内。
- 浏览器 Apple 后端不接 macOS Now Playing / 媒体键；macOS browser 模式下 Radio 与 Audius/Jamendo
  仍由 lilt-audio 提供原生系统集成。Linux 的 mpv 与 browser 均无该集成。两侧由
  `internal/playrouter` 互斥路由。浏览器模式不注册 MusicKit 资料库/个人歌单能力。
- 手机端未排期；跨端承诺只覆盖数据与操作契约，不承诺音质。

## 5. 外部依赖

- Radio Browser 为无可用性保证的社区服务。当前实现以 `de1.api.radio-browser.info` 为主、
  `de2.api.radio-browser.info` 为静态回退（每个请求 7s 超时，主镜像失败后按序尝试下一个），
  全部失败时在 Browse 显示「Radio directory unavailable — check your connection, then retry」并记入日志；
  动态镜像发现（SRV）仍保留在 proposed [`../internals/providers/radio-discovery.md`](../internals/providers/radio-discovery.md)。
- 自定义流地址（`a`）通过流自身的 `icy-name` 头解析电台名，失败则保留原始地址。
- 广播播放走 AVPlayer，可达性探测走 URLSession HTTP 首字节；ATS 只启用 `NSAllowsArbitraryLoads`（与 `…ForMedia` 等更窄的 ATS 键并存时全局键会被系统忽略），以放行任意 http/https 电台 URL。
- AVPlayer item/status failure 会通过 `playbackError` 显示；由于重连策略依流而异，lilt 不自动重连。

## 6. 部分公开连续流不兼容 AVPlayer（已接受）

**症状**：个别公开电台的普通 `GET` 可返回音频，但 macOS `AVPlayer` 在缓冲或播放中请求
HTTP `Range` 后失败。例如 `https://www.getsubwave.com/stream.mp3`：无 Range 请求返回
`200 audio/mpeg` 和 `ICY-Name: SUB/WAVE`，而 `Range: bytes=0-4095` 返回 `404`；播放器最终显示
`Audio failed to load`。

**结论**：URL 可达或首字节 probe 健康不等于 AVPlayer 可播放。lilt 继续使用 AVPlayer 作为 macOS
Radio 的原生播放后端，并在失败时展示具体错误（audio helper 现在观察 item 失败并写
`playbackError`；见 [`../internals/playback/audio-helper.md`](../internals/playback/audio-helper.md)）；
不把此类源伪装为网络断开，也不让它无限停在 `buffering`。

**当前取舍**：不为第一个已知样本引入 FFmpeg normalizer、mpv 或本机代理。它们会增加打包、签名、
许可证、进程生命周期与额外延迟的长期成本，须在出现更多不兼容公开流后再评估。未来若实现，方案是
仅对已确认 Range 不兼容的连续音频做平台专属 fallback，HLS 与标准流仍直接使用 AVPlayer。

## 7. Queue jump 的 MusicKit 回退（已解决，保留部分回退）

**症状**：在 Up Next 里跳转到某些条目时，MusicKit 返回 `MPMusicPlayerControllerErrorDomain Code=6`
`Prepare queue failed with unexpected start item`。原因是原先用**当前队列实例的 `Queue.Entry` 对象**
去新建 queue 并作为 `startingAt`；MusicKit 不接受来自另一队列实例的 entry。

**处理**：helper 记录播放时解析出的 `[Song]`（队列增删/移动时同步维护），jump 时用这些 Song
**重新构造** `Queue.Entry` 再 `Queue(..., startingAt:)`——与初次播放同一条成功路径；若重建失败，
先**恢复原队列**再退化为逐条 `skipToNextEntry`/`skipToPreviousEntry`。队列被整体插入 playlist/station
时 Song 列表会失效，此时只保留 step 回退。真机验证：对 130 首的「喜爱歌曲」跳到 1/59/120 均成功
（59 之前必失败）。

**后续发现的 shuffle 序号空间 bug（已修复）**：开启 shuffle 后 MusicKit 会重排 live
`queue.entries`，而 state 此前直接从 live entries 投影 `queue`/`queueIndex`——TUI 显示的是
**洗过的顺序**，jump/remove/move 却按**提交顺序**解释 index，点击的行和实际播放的曲目对不上。
修复后所有 wire 状态与 index 只有**一个序号空间**：canonical 提交顺序（见
[`../client-api/models.md`](../client-api/models.md) 与
[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md) 的 canonical 队列投影）。shuffle 回归
Apple 语义：on/off 开关；推进随机、一轮内不重复、耗尽 no-op（`repeat all` 重洗一轮）；jump 先
短暂关闭 shuffle 重建（`startingAt` 才被尊重），play 成功后恢复。修复过程中还发现一个投影
bug：`Queue.Entry.id` 是 MusicKit 本地 id 而非 catalog id，按它匹配 canonical 列表永远落空
（表现为 jump 后状态总显示第一首）；现按 entry payload 内的 Song id 匹配。canonical 投影与序号
解析有 Swift 单测（`canonicalQueue`/`removedQueue`/`movedQueue`），真机回归：shuffle 开启时
8 首队列 jump(2)/jump(4) 音频与状态均落在点击项。

**局限**：极少数库内条目仍可能无法被 MusicKit 重新匹配；此时报错并保留原队列，不会破坏当前播放。

**入口防护**：Up Next 的鼠标命中必须

- 把 dock 与列表之间的空行（dockGap）计算在内，否则选中会偏移一行；
- 在点击后保持队列窗口稳定（和主列表一样）。否则每次点击都会重新居中，同一格的第二次点击会落到别的条目，从而跳转到非预期曲目。

两项都已修复并有回归测试。每次队列操作都会记录 `queue` 日志（action、index、queueLength、目标），便于定位。

## 7b. append 构建的 Apple 队列无法跳转（仅回退路径；主路径已改 one-shot）

**症状**：对 append 构建的队列，在 Up Next 里选一行按 Enter，得到
`could not jump to row N of M: … Code=6 "Failed to prepare to play" … Playback continues with the
current track.` 跳转不发生，但播放不被中断。

**现状（2026-09-22 起）**：专辑与 `playback.playSongs` 的**主路径是一次性赋值**（helper
`playSongs`，与歌单同形状）——队列可跳转、秒级起播、无节奏填充。OQ1 探针（2026-09-22，真实账号）
证伪了"专辑整体不可一次性赋值"：四张真实专辑（库内 9–16 曲 + catalog 17 曲）one-shot 全部成功，
`queueJump` index 5 精确落位；2026-09-20 的失败是**单张专辑的内容特性**（n=1 归纳错误）。

**残留限制**：MusicKit 仍会拒绝个别专辑内容的整批 prepare（Code=6）。此时 server 回退到
起播 + 节奏 append——该路径构建的队列不可原地跳转，且十几首要等约 10–40s 填满。

**跳转的自愈（OQ37）**：`queue.jump` 遇到 append 队列的 `queue_not_jumpable` 时，server
只尝试以当前 canonical 顺序一次性赋值并从目标行起播。成功且落点、队列顺序经核对，才报告成功；
**一次性赋值失败不自动改为逐首追加**（那会把目标曲放到队首、改变后续顺序，还可能静默漏歌）。
失败后核对真实状态：队列与当前项仍在原位，返回 `queue_not_jumpable` + `details.state`；队列或播放
已变，发布状态并返回 `partial_failure` + `details.state`；状态无法确认，返回
`operation_outcome_unknown`，server 会作废旧 `queueRevision` 并广播警告，客户端先查状态、不自动重试。
MusicKit 的赋值不是事务：它可能在
`play()` 报错前改动队列，因此不能承诺失败时原播放一定继续。`queue.jump` 为等待一次性赋值
设置 15s 预算。helper 对 append 队列不原地跳转（尝试重建会连带杀掉正在播的队列）；helper
`playSongs` 先短暂关闭 shuffle 再恢复，让 `startingAt` 生效。普通专辑/`playback.playSongs` 的
既有逐首追加回退本次保持不变；是否改变其产品语义需另行决定。已验证的回退路径行为保持不变：
- `skipToNextEntry` 步进不可靠（MusicKit 会跳过无法 prepare 的条目，实测目标第 4 行、实际播第 6 行）。

**历史证据（2026-09-20，batch `2026-09-20-search-and-queue`）**：当时的"一次性赋值对专辑不可用"
结论已被 2026-09-22 探针证伪；解析与回退契约见
[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)。
（代价见本节的取舍）。

## 7b2. preview（无订阅/未授权）模式下队列编辑被拒绝（已对齐真值）

**行为**：preview 模式下 helper 的 `queueJump`/`queueRemove`/`queueMove` 返回
`preview_unsupported`（2026-09-23 前remove/move 是静默 no-op，现按约定显式报错）；descriptor 的
`queue` capability 也只在订阅（`full`）时声明。TUI 的 `0`/`e`/`E`/`:queue` 使用同一道门。fake
engine（装置）在 preview 模式仍接受队列操作——这是装置与真机的已知差异，fake 轮的队列观察以真机
为准（batch 2026-09-23-polish P2）。

## 7c. MusicKit 偶发丢弃刚填满的队列（已接受；lilt 如实报错）

**症状**：专辑页按 `p` 后 `UP NEXT` 已列出全部曲目，但播放没有开始（`Stopped 0:00`）。2026-09-20
的两个真实会话里出现两次，之后同样的路径连续 3 次全部成功（隔离 server：`status=playing queue=12`），
直连 helper 的等价序列也 12/12 成功。

**证据**（`2026-09-20-form-fix-recheck` r3-recheck）：

- helper debug 显示填充过程中 `queueSongs` 从 1 增到 4、`status=playing`、position 正常前进，
  随后同一秒变成 `song=nil status=stopped`；`resume` 返回 `MPMusicPlayerControllerErrorDomain Code=1`。
- 失败窗口的旁证是 MusicKit 退化：`enqueue` RPC 从 ~10ms 涨到 ~300ms，旧解析梯级的
  `albumTracks` 两次 `invalidReference`，同一时段 `resume` 报 Code=1。专辑解析梯级随后已替换；
  此旁证不是当前路径仍会失败的证据（当前路径仍待独立复测，见 [`OQ15`](open-questions.md#oq15--资料库专辑详情偶发解析失败中已修待复测)）。
- 已排除“前一次播放处于 shuffle”“形态应用顺序”两个假设（探针与日志），也未能在当前环境复现。

**当前取舍**：不尝试绕过 MusicKit 的这个行为（与 §7b 的 append 构建方式同源）。lilt 的行为是
**如实报错**：填充结束仍停在 stopped 且重新拉起失败时，`playback.play` 返回
`playback_error: playback did not start (…)` 并带 `details.state`，不再把“Stopped + 满队列”当成功
返回（回归测试 `TestWedgedQueueFillReportsPlaybackError`）。

**下一步**：下次复现时抓 helper 侧时间线（`enqueue` 耗时、`state` 投影），与 §7b 的“append 构建的
队列与 MusicKit 的兼容性”一起调查。

## 7d. 终端把 Esc 与后续字符解析成 alt 序列（已缓解）

**症状**：`?` 打开帮助后**快速**连发 `Escape` 与下一个字符（例如 `Esc` 后立刻 `v`）时，帮助不关闭、
后续按键无效，用户以为“卡住”。

**结论**：这是终端层的输入歧义（Esc 后紧跟字符会被解析为 `alt+<char>`），不是应用缺陷；单发
`Escape` 正常关闭。

**当前取舍**：帮助层的状态行写 `Esc/? close`，只由 `Esc`/`?` 关闭；`q` 直接退出 TUI，其他键保持惰性
（不吞掉用户想执行的键）。不在应用层为终端歧义加特例。

## 8. provider 切换瞬间的旧状态尾巴（已接受）
**症状**：切换到另一 provider 后，被切走的 provider 可能继续上报约 3 秒（例如 MusicKit 的
`stop()` 后仍会短暂报告 `playing`）。这些通知不带 session 戳，若不处理会短暂把旧 provider 的
状态投影到新的 `activeSource` 下。

**处理**：server 在每次 start 后开启一个约 3 秒的切换窗口（`switchSettleUntil`）；窗口内，
若某条 engine 通知的状态形状（`isLive`/`mode=stream`/stream track）推导出的 source 与已提交的
`activeSource` 不符，就丢弃它。这只影响切换瞬间，稳定后通知形状与 `activeSource` 一致，正常投影。
Audius URL 会话有 generation/session 戳，由另一套过滤处理。

**局限**：窗口内同 provider 的“停止/空状态”也可能因形状不匹配被短暂丢弃；由于 stop/restart 由
server 自身提交状态，不影响最终一致性。彻底方案是给所有 helper 模式加通用 generation/session
回显，目前按“不同时使用多个 provider + 切换窗口检测”接受。

## 9. Audius 账号 OAuth（已接受）

**现状**：Phase 3 的 OAuth 2 Authorization Code + PKCE、token refresh/revoke、Keychain 存储与
disconnect 已实现，hermetic 覆盖 + 一次真实账号验收通过（`authorized` + account label，disconnect
后本地凭据删除、状态回到 `not_determined`）。

**限制**：
- 账号关联需要部署者自建 Audius developer app 并注册 `http://localhost:<port>/callback`；未配置时
  `authorization.begin audius` 直接返回 `authorization_failed` 与配置指引（匿名功能不受影响）。
- macOS Keychain 通过系统 `security` 工具写入，secret 短暂出现在进程参数中（系统允许范围内）。
- 连接账号后 Audius 额外声明 `library`（用 bearer token 读该账号的歌单）；未连接时不声明，匿名
  discovery/playback 不受影响。

## 10. Jamendo 需要用户自备 client_id，且仅限非商业使用（已接受）

**现状**：Jamendo 的公开 API 只要 `client_id`（无 secret、无 token），但 Jamendo 的 API Terms
规定 credential 严格个人、不得向第三方披露，因此 lilt MUST NOT 内置或共享 client_id。首次使用需要
用户在 `devportal.jamendo.com` 建一个免费开发者 app（默认 read-only plan），再运行
`lilt jamendo setup`。未配置时该 source 为 `unavailable`，`reason` 直接给出该命令。

**限制**：
- 这是 lilt 第一个需要用户自备凭据的 Source；其他 Source（Apple Music 走系统授权、Audius 匿名、
  radio 无凭据）都不需要这一步。
- 配额 35,000 请求/月，按用户自己的 client_id 计算；lilt MUST NOT 轮询，每曲起播最多一次解析请求。
- Jamendo 读接口本身不稳定：对一个完全有效的请求有 30–50% 概率返回空结果（HTTP 200 +
  `code 0`），并间歇重置 TLS。lilt 用有界重试（最多 5 次，退避共 ~1.5s）把用户可见失败率压到 1%
  以下；真正无匹配的查询因此最多花 5 次请求。度量和策略见
  [`../internals/providers/jamendo.md`](../internals/providers/jamendo.md#61-上游不稳定空结果与传输重置)。
- API 仅对**非商业**用途免费；广告、付费、affiliate 或其它商业利益/金钱补偿用途在开始前 MUST
  先取得 Jamendo 商业许可。公开分发本身不等于商业使用，见 [`roadmap.md`](roadmap.md) §5 与
  [`../internals/providers/jamendo.md`](../internals/providers/jamendo.md)。
- 在第二个需要用户自备凭据的 Source 出现前，setup 不提升为公开 `interaction.type=input` 流程。
  TUI 的来源弹窗对未配置的 Jamendo 提供进程内 client_id 输入、校验和保存，并主动刷新来源列表；
  直接在另一个 shell 运行 `lilt jamendo setup` 后，已有 TUI watch client 仍需重新连接才能刷新来源可用性。

## 11. 直播暂停时标题仍可变化（已接受）

Radio 的 pause 只暂停本机音频后端；`streamTitle`/`streamArtist` 来自 server 独立读取的 ICY
当前公告，不是可 seek 的缓冲时间线。因此暂停期间标题可能随电台更新，表示电台此刻公告的节目，
而不保证是暂停瞬间的音频片段。lilt 不冻结或回放 ICY metadata。
