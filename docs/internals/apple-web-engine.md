# Tech Design: Apple Music 全曲（跨平台浏览器引擎，阶段 2）

**Status: 阶段 2a 与 2b 均已实现。**
`internal/appleweb`（CDP 传输 + 目录 + 引擎）、`internal/server/apple_web.go`（页面版 Apple provider）、
`internal/playrouter`（把 streams 后端与浏览器合到 server 现有两个接口后面的路由组件）都已落地。
Linux 默认使用；macOS 保持签名 MusicKit helper 为默认，仅在 `LILT_APPLE_ENGINE=browser` 时启用。

登录入口是 `lilt auth apple-music`：开一个可见窗口让用户在 Apple 自己的页面上登录，完成后自动关窗
（会话在磁盘上，后续操作仍 headless）。

阶段 1 的 iTunes Search 路径（30s preview，不需要浏览器）已被本设计**取代并删除**：同一个来源只保留一套
catalog，否则 storefront 会在「搜到」与「播得了」之间漂移。

### 授权状态与启动预热

`Describe` **不为了回答授权状态去冷启动浏览器**——`authorization.list`/`session.watch` 在 TUI 启动和
agent 首次读取时都会被调用。server 启动后会在后台预热已有 profile；从未使用过 Apple、尚无 profile 时
则保持 lazy，不为无关 source 启动 Chromium。所以：

- 会话在跑 → 报**实时**状态（实测：登录后播放期间 `auth status` = `authorized`）。
- 会话没跑 → 报 `not_determined` + 「run `lilt auth apple-music`」提示；已有 profile 的这个窗口会由启动
  预热结束并通过 watch 发布结算后的状态。

这个取舍是**有意的，不是缓存**：不做「记住上次观测到 authorized」的持久缓存，因为那会撒谎——Apple 侧会话
过期后会变成「`lilt auth` 说已授权、播又播不了」的死角。预热也只报告页面实时结果，不持久化上次答案。

### storefront 语义：授权后跟随登录账号

页面的 `mk.storefrontId` 跟随页面 URL 的区，**不跟随登录账号**：新 profile 落在美区页面上，登录后
也不会自动切换，而 MusicKit 的全曲播放权按「账号订阅区 × 曲目目录区」裁决——国区订阅账号在美区页面
上只有 90 秒 preview，同时公开 mode 却报 `full`（OQ36 的原始症状）。

引擎在**每次浏览器启动、MusicKit 就绪后**做账号区对齐（`Engine.session` → `alignStorefront`）：

1. 读页面状态：`authorized` 为 false（未登录）→ 不对齐。preview 在哪个区都是 preview，
   未登录用户的页面维持现状。
2. 已登录 → 在页面里 evaluate `mk.api.music('/v1/me/storefront')`，取 `r.data.data[0].id`
   （注意与 catalog 查询 `r.data.results…` 的层级不同）。该调用在会话不可用（登出/过期）时会失败，
   页面内捕获并视为未登录，跳过对齐。
3. 账号区 ≠ `mk.storefrontId` → `window.location` 导航到 `https://music.apple.com/{账号区}/listen-now`，
   然后等**新页面**的 MusicKit 就绪并回报账号区（旧 document 在导航 commit 前仍会应答 evaluate，
   所以等的是「就绪且已在新区」这个正向信号，而不是单纯的 ready）。
4. 一次启动最多对齐 **2 次**（防循环）。导航失败或页面始终到不了账号区 → 放弃并**照常继续会话**：
   catalog 在当前区仍可用，播放退化为 preview——对齐是 best effort，不是浏览器启动的硬门槛。
5. 对齐成功后页面 `mk.storefrontId` 即账号区，既有 catalog 表达式（读 `mk.storefrontId`）与播放
   使用同一个目录，「搜到」与「播得了」不再分家。

对齐发生在 Widevine 探测之前，capability 与 catalog 看到的是同一个区。发生导航时经 journal 记一条
`apple.storefront_aligned`（`from`/`to` 为 storefront 稳定码，不含 URL）。`--restore-last-session`
会让下次启动直接恢复对齐后的页面，通常起步即已对齐。

## 决策

| 项 | 决定 |
|---|---|
| 播放机制 | **A2：Apple 的试听与全曲都由浏览器引擎承载**（一个 source 一个机制，不做 mpv/浏览器 二选一） |
| discovery | **也走页面**（`api.music('/v1/catalog/{storefront}/search')`）：实时读取页面 `storefrontId`，与播放使用同一个 storefront；已有 profile 的冷启动由 server 后台预热承担 |
| 登录交互 | `lilt auth apple-music` 按需开窗；**与 Audius/Jamendo 的交互形态统一**，排在 2b |
| 打包 | 运行时探测系统 Chromium（`LILT_CHROMIUM_PATH` 可覆盖）；macOS 依次探测 `/Applications`、`~/Applications` 中的 Chrome、Chromium、Edge、Brave（Chrome 自带 Widevine）；Linux flake 提供可选 Widevine Chromium |

阶段 1 的 30s preview 成本优势（mpv 76 MiB vs 浏览器 632 MiB PSS）在 A2 下让位于「一个来源一个机制」；
浏览器只在 Apple 实际使用时启动，并应有空闲退出（见「未决」）。

## 实测结论（全部在本机验证过，不是推断）

| 问题 | 结论 | 证据 |
|---|---|---|
| 登录能否跨进程持久化 | ✅ 能，**但必须 `--restore-last-session`** | `curl` 自建服务放会话 cookie + 持久 cookie，优雅关闭后重开：无 restore 只剩 `pers=1`；有 restore 得到 `sess=1; pers=1`。cookie DB 里 `sess` 以 `persistent=0` 落盘 |
| 为什么必须用它 | Apple 的登录 cookie **全是会话 cookie**，浏览器默认不落盘 | 真登录后查 profile：`.apple.com` 3 个、`.idmsa.apple.com` 2 个均 `is_persistent=0`，只有 `.music.apple.com` 1 个持久 |
| 全曲 DRM 能否播放 | ✅ 能，**headless 也能** | `setQueue({song})` + `play()` → `state:2`、`dur:204`（全曲）、`t` 按真实时间推进、`playbackError:null` |
| 驱动缺什么 | **`Runtime.evaluate` 必须带 `userGesture: true`** | 不带时 `play()` 永远 pending、`state` 卡 `1`(loading)、`mediaKeys:true` 但 `readyState:0`、且**不报任何错误** |
| 读取路径 | ✅ 够用 | `isAuthorized`、`playbackState`、`currentPlaybackTime`、`currentPlaybackDuration`、`nowPlayingItem`、`queue`、`playbackError`、`volume` |
| 要不要 Node/Playwright | ❌ 不要 | `--remote-debugging-pipe`（fd 3 写 / fd 4 读，NUL 分隔 JSON）纯 Go stdlib 驱动通 |
| EME | ✅ Widevine | `com.widevine.alpha` OK（audio-only 与 A/V）；`com.apple.fps` NotSupported → MusicKit JS 选 Widevine |
| 凭据归属 | 无需自签 token | web player 自带 Apple 的 MusicKit JS v3，`window.MusicKit.getInstance()` 即官方 SDK |
| storefront | 授权 settled 后由引擎对齐到账号区 | 页面初始区由 URL 决定（新 profile 默认 us），登录后**不会**自动切换；对齐机制见上文「storefront 语义」 |

对照实验（用来分离「我们的自动化错了」和「浏览器播不了」）：**人在同一个浏览器窗口里手动播放，完全正常**，
此时探针记录 `state:2`、`t` 每 3 秒 +3、`dur:248`、`queueLen:258`、`err:null`。所以平台没问题，
问题只在驱动方式——就是上面的 `userGesture`。

## 必须遵守的约束（踩过的坑）

1. **每条驱动命令都带 `userGesture: true`**。缺了不报错、不失败，只是永远不播——最难查的一类。
2. **`--restore-last-session` 必需**，否则每次重启都要重新登录。
3. **它同时会恢复上次的标签页** → 引擎必须在**启动时**关掉除目标页以外的所有 page target，
   否则每跑一次就多留一个 Apple Music 标签（已实测：修前累积多个，修后 `page targets kept: 1`）。
   只在启动阶段清理：Apple 登录时可能后弹 popup，关掉它会直接弄坏登录。
4. **优雅关闭**（CDP `Browser.close` + 等退出）。SIGKILL 会丢掉 profile 未刷盘的部分。
5. Chromium + Widevine 是 **unfree**（nixpkgs 用 wrapper：`chromium.override { enableWideVine = true; }`）。
6. 音频来自默认 sink；没有 MPRIS/Now Playing（与 mpv 是同一个已知差异）；`player.volume` 可用。
7. profile 默认位于机器级目录（Linux：`XDG_DATA_HOME/lilt/apple-browser`；macOS：
   `~/Library/Application Support/lilt/apple-browser`），不随 state root 派生。所有权只在 lilt 用
   原子 `mkdir` 新建目标目录时建立并写 marker。已存在且无 marker 的目录可复用，
   但永不补写 marker、`disconnect` 也明确拒绝删除；不存在的目录可重复 disconnect。浏览器存活期间还会持有
   profile 同级 `<profile>.lock` 的非阻塞独占锁，另一 server 使用同一 profile 会明确失败。
8. 交互登录独占 Engine 生命周期；登录窗口存在时 catalog/playback 不等待窗口结束，也不另起 headless Chromium，
   而是立即返回当前状态不允许操作的错误。
9. **Widevine 探测随每次浏览器启动执行**：`Engine` 等 MusicKit 就绪后，在同一页面上 evaluate
   `navigator.requestMediaKeySystemAccess('com.widevine.alpha', …)`（secure context 与异常路径都在页面内处理），
   三态结果缓存到 Engine：ok / denied+reason / evaluate 失败视为「未确认」。探测为负时 provider 不声明
   `playback.full`（capability 是唯一真值，不静默降级为"宣称 full"），preview/search/queue 不受影响；server 用
   签名去重的慢轮询（~2s）重发布 `sources.changed`。`Available()` 只做 binary 发现，不能作为 full 的依据；
   浏览器从未启动过时 descriptor 保留"登录前提"表述，不为此付冷启动代价。
10. CDP pipe EOF/写失败是可区分的 fatal browser 错误。合并播放器发布带当前 generation/session 且
    `EngineFatal=true` 的终止状态并关闭更新流，server supervisor 随即重建；不自动重放结果未知的播放命令，
    下一次显式操作才启动新 browser。
11. 页面 `playbackError`、evaluate exception 与 Widevine probe reason 都是上游文本，必须在
    `internal/appleweb` 边界统一经 `sanitizeUpstreamMessage` 脱敏；公开 state/watch/log 不得再接触原文。

## 架构

`playrouter` 同时满足 server 已有的两个接口，并在内部把目标路由到两个后端：

```text
lilt serve
  ├── AudioEngine       (radio stream)          → 组件 → streams
  └── URLPlaybackDriver (finite URL queues)     → 组件 ─┬→ streams    (Audius/Jamendo)
                                                        └→ 浏览器引擎  (apple-music)
```

- **server 侧不改行为**：streams 在 Linux 为 mpv，在 macOS 为 `lilt-audio`；两者都同时实现
  `AudioEngine` 与 `URLPlaybackDriver`。
- **独占性由组件内部保证**：起任一后端前先停另一个（对应 server 的「同一时刻只有一个实际播放」）。
- Apple 的 queue 仍由 server 的 `URLQueueTransport` 拥有，所以 **add/remove/move/jump/clear 全都有**，
  与 Audius/Jamendo 一致——MusicKit JS 缺少 `removeFromQueue`/`moveInQueue` 因此不再是问题。
- `URLPlaybackTarget.URL` 对 Apple 为空（或放稳定页面 URL），引擎用 `Item.ProviderID`（catalog id）
  调 `setQueue({song: id})`。**这是要写清的接口借用**：URL 队列的 driver 抽象是「播一个 target」，
  Apple 的 target 由 catalog id 而非 media URL 标识。
- 试听与全曲由 MusicKit 自己决定（未登录 → 30s 试听；已登录+订阅 → 全曲），
  引擎从页面读回真实 `duration` 与状态，不猜。
- 启动策略：已有 profile 时由 server 启动后后台预热；没有 profile 时保持懒启动，首次 Apple 操作才启动
  浏览器。是否需要空闲退出见「未决」。

### 新增包

- `internal/appleweb`（已实现）：CDP over pipe 的传输层 + 页面目录 + 惰性启动的 `Engine`。
  - 传输层：launch/attach/`Runtime.evaluate`（带 `userGesture`）/target 卫生/优雅关闭/进程回收。
  - 引擎：`PlayCatalogSong`/`Pause`/`Resume`/`Stop`/`State`，以及登录态查询。队列 next/previous 由
    server 的 `URLQueueTransport` 完成，不直接驱动 MusicKit 队列。
  - 无 build tag（纯 Go），hermetic 测试用**假 CDP 对端**：测试二进制以环境变量重入，在 fd 3/4 上
    说同一套协议（与 `internal/mpvplayer` 的假 mpv 同一手法）。

## 阶段切分

- **2a（已实现）**：`internal/appleweb`、同时满足 `AudioEngine` + `URLPlaybackDriver` 的跨平台路由组件、
  按每项起播时实时授权决定的 full/preview mode、真实 duration 与可编辑 server queue 均已落地；公开边界
  不泄漏 media assets。
- **2b（已实现）**：`lilt auth apple-music` 走 server-owned flow，`interaction.type = "browser"` + URL，
  与 Audius 的 pending 流同一形状（`authorization.begin` 返 pending，异步 `complete`）。
  引擎侧 `SignIn` 先关掉 headless 会话（同一 profile 不能有两个 Chromium），再起可见窗口，
  轮询到 `authorized` 后优雅关窗。`Cancel(flowID)` 取消并关窗。
  **flow 预算只有一个 owner**：provider 声明 `appleweb.SignInBudget`（10 分钟），server 按声明建
  flow context；`SignIn` 循环自身没有第二个 deadline，context 到期（`DeadlineExceeded`）→ flow 终态
  `expired`，与 `cancelled`/`error` 分支互不混淆。
  `lilt auth disconnect apple-music` 对不存在的 profile 幂等成功；只删除由 lilt 原子创建并带 marker 的
  profile，外部 profile 永不删除并返回 `invalid_state`。

## 打包与运行期依赖

```sh
NIXPKGS_ALLOW_UNFREE=1 nix develop .#apple   # 自带 Widevine Chromium，并导出 LILT_CHROMIUM_PATH
lilt tui
```

`.#apple` 与默认 devShell 的唯一区别就是多一个 `chromium.override { enableWideVine = true; }`
并把它写进 `LILT_CHROMIUM_PATH`。默认 shell **不含** unfree，所以不会替用户接受那份许可；
不带 `NIXPKGS_ALLOW_UNFREE=1` 时得到的是 nixpkgs 标准的 license 拒评提示。

非 Nix 环境：把 `LILT_CHROMIUM_PATH` 指向任意带 Widevine 的 Chromium，或让它出现在 `PATH` 上。
两者都没有时 `apple-music` 整体报 unavailable 并给出安装提示（不会静默降级成试听）。

macOS 默认无需此依赖，因为默认仍是签名 MusicKit helper。显式设置
`LILT_APPLE_ENGINE=browser` 后会禁用 MusicKit resource/library runtime，以 `lilt-audio` 作为 streams
侧，并使用上述浏览器 provider；非法值会阻止 server 启动。Google Chrome 自带 Widevine，仍以页面内
EME 探测结果作为 `playback.full` 的唯一依据。`lilt doctor` 只诊断 helper，browser 模式会明确拒绝。

## 未决与风险

| 项 | 说明 | 下一步 |
|---|---|---|
| 会话服务端有效期 | `--restore-last-session` 解决的是「本地不落盘」；Apple 侧何时过期未知 | 观察；过期时的 UX 应等价于 `authorization_required` + 引导重登 |
| 空闲退出策略 | 浏览器 632 MiB PSS；闲置时应否退出（退出后下次约 10s 冷启动） | 定一个 idle 阈值；先做懒启动 + 手动停止 |
| Apple 改 web app | 页面结构/`window.MusicKit` 不是公开契约 | 失败要报明确错误，不静默降级；`userGesture` 这类坑要有回归测试 |
| 条款 | 程序化驱动 Apple 自家 web player 不在 MusicKit JS 公开条款覆盖范围内（那套是「你自己建 web app + 自己的 token」） | 已知灰色地带；产品上接受，文档写明 |
| Chromium 版本 | Widevine 需要 CDM 与 Chromium 版本匹配 | 探测失败要给出明确安装提示（nixpkgs 配方 / `LILT_CHROMIUM_PATH`） |

## 实现形状

```text
appleweb.Engine ─┬─→ appleWebProvider   (server.ContentProvider + PlaybackPreparer + AlbumProvider)
                 │      discovery 与 queue 准备都读同一个页面
                 └─→ playrouter.Player (server.AudioEngine + server.URLPlaybackDriver)
                         ├→ streams    Linux mpv / macOS lilt-audio
                        └→ 浏览器      apple-music（试听与全曲，由页面决定）
```

- provider 的 `PreparePlayback` 用**实时登录态**决定 plan 的 mode：已登录 → `full`，未登录 → `preview`。
  谎报 full 会把 30 秒片断当整曲呈现。plan 的 mode 只是建队列时刻的采样：**每个 item 起播时
  resolver 重新采样实时登录态**并覆盖队列 mode，登录/登出/会话过期后的下一次起播自然正确，
  队列不会冻结在建队列时刻的 mode 上。
- **登录开始时显式停止当前 Apple 播放**：sign-in 窗口要独占 profile，headless 会话会随之关闭；
  server 在 flow 启动前走既有 `playback.stop` 路径（`urlTransport.Stop` + commit）发布显式
  stopped 迁移，watch 端可见，不发 `server.warning`（正常产品语义，不是故障），不自动重放；
  用户可在登录后显式重新起播。
- 队列仍由 server 的 `URLQueueTransport` 拥有，所以 add/remove/move/jump/clear 与 Audius/Jamendo 完全一致；
  MusicKit JS 缺 `removeFromQueue`/`moveInQueue` 因此不构成问题。
- 路由按 `target.Item.Source` 分派，所以 `publicCoreItem` 现在把 `Source` 带出来（原先丢掉，导致每个排队项
  都像无来源）。
- 切断所有权前先停另一个后端；**停止失败不转移所有权**，否则会有没人控制得住的后端在出声。
- 路由组件把两路状态合成一条流：mpv 的更新在 mpv 拥有时转发，浏览器侧由 1 Hz 采样器上报（与两边节奏一致）。
- 每个 Apple 采样都携带开始该项时不可变的 playback generation 与 transport session ID；handover、stop、
  起播失败和 close 会使旧身份失效，阻塞后迟到的旧采样不会推进新队列。
- `Engine.Close()` 只关浏览器、不退役引擎：重建播放侧会关掉它，下一次 Apple 操作会自己重启。

## 验收清单（2a）

1. ✅ 已登录 profile 下，headless 可播全曲：`duration > 60`、位置推进（opt-in 真实 E2E）。
2. ✅ 未登录 profile 的 mode 为 `preview`（不谎报 full）；由 provider 单测钉住两个分支。
3. ✅ 队列编辑与 Audius/Jamendo 同语义（复用同一个 `URLQueueTransport`）。
4. ✅ Apple 与 mpv 互斥：起一个必停另一个（路由单测覆盖，含停止失败不转移所有权）。
5. ✅ 启动后恰好一个 page target（假 CDP 对端 + 真机都验过）；退出走 `Browser.close`。
6. ✅ 公开状态只带稳定页面 URL；有测试断言响应里不出现 media asset。
7. ✅ 每条 evaluate 都带 `userGesture` —— 假 CDP 对端对缺失该参数直接回错，测试钉住。
8. ✅ 真实 E2E opt-in（`LILT_APPLE_E2E=1`）已跑通。
9. ✅ 2b：`lilt auth apple-music` 走 pending 流（`interaction.type = browser`），登录成功 → `authorized`；
   Cancel → `cancelled`；超时 → `expired`；Disconnect 对不存在的 profile 幂等成功、只删除 lilt-owned profile。
   真机验证：CLI 打出 URL、开窗、自动确认已登录、关窗，随后 `play` 得 `mode: full`、`dur: 204`、
   位置推进、退出无 Chromium 残留。
