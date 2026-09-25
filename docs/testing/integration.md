# 集成测试设计 / Integration Testing

> **状态**：分层已落地。Apple Music 与 Radio（builtin + Radio Browser）有确定性
> contract/集成覆盖；真实 provider E2E 为 opt-in。Audius 的 discovery、播放与账号 OAuth 的 hermetic
> 覆盖已完成（另含 opt-in `LILT_AUDIUS_E2E=1` 真实 discovery/stream，以及一次人工真实 OAuth 验收）。
> 日常使用与默认静音测试的隔离入口见 §5a；真实音频仍只在 opt-in 窗口验收。

## 1. Purpose / scope

测试分两层：真实 provider E2E 验证公开协议与主要降级；确定性 contract suite 验证所有
Source 共享的 Client API 契约。完整接口见 [`../client-api/README.md`](../client-api/README.md)，
Source/queue 规则见 [`../client-api/models.md`](../client-api/models.md#有限队列不变量) 与
[`../client-api/commands.md`](../client-api/commands.md)。

## 2. Coverage matrix

| Provider / fixture | Real E2E 覆盖 |
|---|---|
| Apple Music | `system_dialog`、subscription/capabilities、native finite queue、full/preview |
| Audius | mock REST search/playlists/error mapping、URLQueueTransport、helper URL playback、OAuth（PKCE/refresh/revoke/secure store）；opt-in 真实 discovery/stream 与人工真实 OAuth 验收 |
| Radio Browser | no auth、directory/filter/paging、health probe、live stream/metadata、partial outage |
| Apple Music（Linux / macOS opt-in 浏览器 composition） | 假 `PageCatalog`：search/album 形状、canonical ref、Widevine 探测驱动 capability、每项实时登录态决定 `preview|unverified` 初态，假 CDP 媒体时长对照目录确认 `full|preview`（缺目录时长仍 `unverified`）、失败映射、启动预热、**登录流**（pending + `interaction.type=browser`、authorized/cancelled/expired/error 四条终态、登录先停止 Apple 播放、Disconnect 对不存在目录幂等且拒删外部 profile）；`playrouter` 路由单测（互斥、停止失败不转移所有权、generation/session 过滤、fatal 终态与状态流重建）；macOS composition 覆盖 helper/browser/非法三态及 provider/router wiring |
| Apple 浏览器引擎（`internal/appleweb`） | 假 CDP 对端（测试二进制重入）：启动参数（`--restore-last-session`）、profile 原子所有权与跨进程锁、标签页卫生、Widevine 探测、上游错误脱敏、CDP fatal、**evaluate 必带 `userGesture`**（缺失即报错）、分片帧重组、页面异常、优雅关闭；opt-in 真实 E2E（`LILT_APPLE_E2E=1`）验证全曲 DRM 播放 |
| mpv（Linux 播放后端） | 假 mpv 进程驱动的 hermetic IPC 套件（`LILT_TEST_FAKE_MPV`，无需安装 mpv）；HTTP 探测；opt-in 真实 mpv E2E（`LILT_MPV_E2E=1`） |
| builtin radio | vendored deterministic fallback，独立于 directory |
| user URL | direct stream identity/error |
| fixture provider + mock upstream | OAuth reject/state mismatch/callback timeout/device code/token expiry/refresh fail/malformed payload/rate limit/timeout/disconnect/concurrency/source switch failure |

Audius real path 只使用官方 REST APIs 与 OAuth 2 Authorization Code + PKCE；不用 yt-dlp/cookies。
真实账户能力仅在官方 API capability 可用且已授权时测试，不能假定未确认 endpoint。

## 3. Tiers / secrets

1. **Hermetic CI contract suite（默认）**：fixture provider 与 mock upstream，无网络、无
   真实 Keychain/凭据；使用进程内 fake secure store 验证凭据生命周期契约。
2. **Real integrations（opt-in）**：显式环境开关，并满足 provider 环境与 Keychain 前置条件。
   不存储 secrets、tokens、authorization code 或 PKCE verifier；已注册 callback URL 是配置而非
   secret。前置条件不满足时以带原因的 **skip** 标记，绝不伪造 pass。现存开关：`LILT_AUDIUS_E2E=1`、
   `LILT_MPV_E2E=1`（真实 mpv 解码 + 进程回收，需要 `mpv` 在 `PATH` 上）、
   `LILT_APPLE_E2E=1`（真实 Apple DRM 全曲；macOS 自动发现已安装 Chrome，需先用 browser 模式登录；
   可用 `LILT_CHROMIUM_PATH` / `LILT_APPLE_PROFILE` 覆盖）。
3. 默认测试不能借真实账号或音频来证明界面功能。没有独立测试账号时，**显式授权的有声验收可共享
   日常 Apple 账号**，但必须独占时间窗口，不与日常播放并行；不得做收藏、资料库改动、授权断开等
   账号写操作（除非该项单独获批）。即使只播放也可能影响服务端收听记录或推荐，不能宣称零副作用。
   必须创建的本地测试状态在 cleanup 删除。

## 4. Shared contract suite

每个 Source 都必须覆盖：`api.describe`/Source capabilities、identity/ref、search shape、play/result
state、single-source queue invariant、watch ordering、auth state、error mapping、disconnect。fixture 必须
从公开 API 验证 provider discovery 与播放传输的路由边界；不可把短期签名 URL 当作持久 Item
字段，并扫描 state、recent、favorites、public response、watch event 与测试日志。有限队列
还验证 `QueueState.source` 与每项/`PlaybackState.source` 一致、错误 source 的 `queue.add/ref`
返回 `source_mismatch`、source switch 不恢复旧队列。并发 discovery/auth 可以跨 Source；播放不可以。

## 5. Real E2E acceptance / cleanup

执行真实 E2E 时，至少验证一次可观察 happy path 与表中主要 degradation；未获有声授权则跳过真实
播放验收，**静音测试可独立完成**，但不得声称覆盖了真实播放。结束时 MUST stop playback，删除
测试创建的本地 state/cache/fixture 资源；不得自动撤销用户的 Apple system permission。Audius
disconnect/revoke 仅在隔离的测试账户且测试明确要求时执行。所有 skip、环境要求与 cleanup 结果必须
可审计。

## 5a. 预发布使用与测试隔离

**状态：构建/会话隔离与默认静音门禁已实现；共享账号的真实音频尚需获批窗口验收。**
`just promote` 先运行 `just verify && just build`，将 CLI 与签名 helper 复制并验证到 gitignored 的
`.lilt-prerelease/<hash>/`；只在日常 socket 空闲时原子切换 `current`，不覆盖旧制品或停止播放。
第一次 `just run` 前必须显式 `just promote`。之后 `just run` 只运行固定制品：没有 server 时按需启动，
同引擎附着；引擎不同（`just run --browser`）时仅在日常 server 确认空闲时自动让位，正在播放则拒绝
并提示 `just stop-daily`。`just run --env dev` 从不碰日常实例，始终使用私有 state root。
`just run` 是**本地调试**入口，server 注入 `LILT_LOCAL_DEBUG=1`：
起播成功的 journal `rpc.start` 行、失败的 `rpc` 行分别记录 helper 私有的预期/实际曲目稳定 ID、
标题、艺人、专辑、时长、队列行号、状态与进度，供区分同曲异 ID 和真播错曲；
Client API response/watch 不包含这些原始身份信息。每次起播只记录一次，不逐帧刷元数据。
日志仍是用户私有文件（`0600`、5 MB 轮换），默认级别不得存入凭据或短期签名媒体 URL；分享前脱敏。
**复现一整轮**：把该轮的日志隔离到独立文件并对可复现的开发会话开启全保真诊断——

```sh
LILT_LOG_LEVEL=debug LILT_LOG=/tmp/lilt-round-<name>/lilt.jsonl just run
```

一轮的产物清单：`/tmp/lilt-usability/<batch>/manifest.txt`（commit + dirty 指纹）、每轮
`/tmp/lilt-round-<name>/lilt.jsonl`（`requestId` 串起 CLI→server→helper，`sequence` 串起 watch）、
`keys.log`（经脚本发送的键；手动会话由 `key` 事件覆盖）、需要时 `/tmp/lilt-player-timeline.log`。
级别、事件与脱敏边界见 [`../internals/troubleshooting/journal.md`](../internals/troubleshooting/journal.md)。
对于一次性歌单起播的真实回归，可在**当前 pinned 的日常 server 已启动、状态为 stopped、
且确认独占有声窗口后**运行（可选 `--runs 2`）：

```sh
LILT_TEST_AUDIO=1 python3 scripts/check-apple-start.py --playlist-index 0 --song-index 9
```

索引来自同次只读 `library.playlists` / `playlist.tracks`；
脚本不会启动或重启 server；按 TUI 同形状从所选曲起播、核对实际曲目 metadata / 队列索引，
成功或失败都检查最终状态并在其仍活动时停止测试播放。失败不自动重试，私有 journal 保存原始诊断。
默认门禁仅用 hermetic 假响应测试该脚本，不进行真实点播或占用真实账号。

| 环境 | 实际用途与边界 |
|---|---|
| 预发布（日常 `just run`） | 固定、已验证的构建与日常数据/账号；开发构建及测试不得自动重启它、覆盖其运行制品或修改其状态。两种 Apple 引擎切换同一个日常使用环境时，仅在确认空闲时自动让位；正在播放必须显式 `just stop-daily`，不能暗中切换。 |
| 静音开发测试（默认） | 纯 TUI 渲染/输入用进程内单测；PTY 走查用独立 socket/state/activity/config/cache 的假播放后端。可测界面与 server 协作，**不能由 fake 结果推断真实 provider 或音频正确**；假后端必须禁止任何真实播放启动。 |
| 真实播放验收（仅 opt-in） | 用私有 server 与状态，但可共享日常 Apple 账号；Apple Music/MusicKit、浏览器 profile、音频设备仍是共享资源，两个 server **不等于**两套独立播放环境。仅在用户明确批准的短窗口执行，不与预发布播放并行，不因冲突自动关闭日常实例。 |

完整 `usability-test` batch **允许全 fake**，如实标记只验证界面/假播放；真实音频另开获批的
probe/batch。编排者在启动前确认用户当前不在听音乐、开会或使用预发布播放；无法确认就不启动。
即使预发布未出声，浏览器 profile 仍可能被其持锁占用：发生冲突时报明原因，等待明确安排释放，
不得接管 profile 或强制停服。音量设为 0 或仅隔离 Unix socket **不能**充当静音保证，MusicKit 无
per-playback 音量。只读的真实目录检查也要避开共享 profile/账号冲突。

`just run --env dev --fake` 与 `round.sh start` 默认使用私有路径和假播放后端；fake server
使用内存凭据，fake TUI/CLI 不允许 Jamendo 账号设置或浏览器跳转。**fake 不保证离线**：实际来源的
目录查询仍可能访问网络；确定性、无网络测试继续使用第 3/4 节的 hermetic suite。
普通 `just test`、`just verify`、`just provider-gate` 清除真实 Go E2E/有声开关；它们不会重建预发布
制品。直接运行带 opt-in 环境变量的 `go test` **不经过此门禁**，仍须用户授权且不能与预发布并发。`round.sh` 默认 fake-only；real 必须 `preflight --real-enabled`、`start --real` 和
`LILT_TEST_AUDIO=1`，试驾入口 `just test-drive` 同样是**真实**会话（命令本身即授权，不再需要
`LILT_TEST_AUDIO`）。真实探针
另需 `LILT_PROBE_AUDIO=1`。脚本对日常 socket 做只读检查，并用互斥 reservation 阻止运行期间
启动预发布 server；已在使用日常 server 时拒绝真实轮，绝不自动 `quit`/`pkill` 它。

**验证边界**：确认静音、路径/构建不冲突以及「占用时拒绝」可用 hermetic/假会话测试；
MusicKit 实际争抢、共享账号副作用及有声播放正确性需要用户另行批准，未测则标「未验证」。
这些入口无法判断用户是否正在开会或其他应用是否出声：即使允许 real，也必须先由用户确认窗口。
原始 `./lilt` 仍是开发 CLI，不能把它当作预发布客户端；日常用 `just run` 或 `just` 的预发布快捷命令。

## 5b. 试驾会话（`just run` / `just test-drive`）

`just run`（纯 TUI）与 `just test-drive`（Herdr tab + agent）共用 `scripts/session.sh` 与同一组
选项：`--env pre-release|dev`、`--fake`、`--browser`。默认 `--env pre-release`；`run` 默认用日常
server 的固定构建，`test-drive` 以及任何 `--env dev`/`--fake`/`--browser`(私有) 组合都使用
`/tmp/lilt-<label>-<stamp>/` 私有 state root，不碰日常实例。

试驾会话是**真实 Apple Music 会话**，执行命令本身即音频授权（不再需要 `LILT_TEST_AUDIO`）；日常
server 确认空闲时会自动让位，正在播放或状态无法确认则拒绝并提示 `just stop-daily`，绝不打断收听。
它使用 `--env` 选定的构建（默认 pre-release 固定制品；`--env dev` 先 `just build`，macOS 含签名
helper），保留 real reservation，并把构建标识（commit、dirty 文件数、二进制 sha256，macOS 另含
helper sha256）写入 manifest，再在**调用者所在的 Herdr workspace**（`$HERDR_WORKSPACE_ID`，不用 UI
当前聚焦的那个）开一个新 tab。需要假播放的开发/vibe coding 会话用 `just run --env dev --fake`
（私有、静默、内存凭据），不用于试驾。

**同一时刻只保留一个试驾会话**：新运行会先替换上一个会话——关闭它的 tab（TUI + agent；若旧会话
就是本次运行所在的 tab，则只关旧 TUI pane，避免自杀），并用 `lilt quit` 停掉它留在
`/tmp/lilt-test-drive-*/sock` 上的私有 server（对已死的 socket 是成功 no-op）。当前活跃会话记录在
`/tmp/lilt-test-drive-session`（dir/tab/tui pane）；此外还按 "lilt test drive" 标签清扫无 pointer 的
遗留 tab。
- 左 pane：`pi`（Herdr agent 名同会话名，例如 `test-drive-20260920-114007`）；
- 右 pane：`lilt tui`；
- 两个 pane 共用同一个**私有且新启动的** server（`/tmp/lilt-test-drive-<stamp>/{sock,state.json,config,radio.json}`），
  所以 agent 用会话里的 CLI 执行的操作会实时出现在 TUI 上；日常实例不被替换；
-  机器级配置会透传进会话（`LILT_CHROMIUM_PATH`、`LILT_APPLE_PROFILE`）；真实会话共享系统 Apple
  账号及浏览器 profile（路径见
  [`../internals/persistence/state.md`](../internals/persistence/state.md#路径)），不得与日常播放并行。
- 键盘与鼠标写入 `/tmp/lilt-test-drive-<stamp>/log.jsonl`（`kind:"key"` / `"mouse"`，另有 `rpc`、
  `helper`、`navigate`/`play`/`queue` 等）；右 pane 退出时 pane 里的 shell 会补一条 `lilt quit`，
  关 tab 前也可用输出的 `cleanup` 命令收掉 server。

脚本在报告成功前会检查私有 socket 与 journal 文件确实存在（Herdr 的 `tab create --env` 不会传给
split pane，漏传会静默落到默认 socket）；检查失败会关掉自己开的 tab 并以非零退出。

它是**人工探索**，不替代第 4/5 节的 hermetic 与 contract 测试，也不产出可重放的 round 报告；需要可
重放的 agent 走查仍用 [`.agents/skills/usability-test/`](../../.agents/skills/usability-test/SKILL.md)。

### 已发生的真实会话排障

用户说「检查刚才的操作/日志/截图」时，**先分析已有证据，不重新启动 `just test-drive`**（会替换
旧的试驾会话，导致证据丢失）。先确认操作和时间窗，从该会话的 `manifest.txt`、journal 与用户截图定位
运行构建；依次核对**实际听到什么（只能向用户确认，不能从日志推断）→ helper 状态 → server
response/watch/journal → TUI 显示**。分清已观察事实、推断与缺失证据，再给最小复现、根因所在层及
下一项可验证动作；不要只因 TUI 文案或成功响应就断言音频正确。日志若含账号或 URL，分享前脱敏。

默认只分析：不改代码、不碰正在使用的 server、不自动进行有声复测；需要重现时先按本文件的隔离与
可听性规则取得用户授权。触发与报告配方见 [`.agents/skills/session-triage/`](../../.agents/skills/session-triage/SKILL.md)；
与主动组织陌生 agent 走查的 `usability-test` 分开。

## 6. Boundary

真实 provider 覆盖 happy path 和 major degradation；mock 覆盖需要确定性重现的全部分支。两者互补，
真实成功不能替代 mock 的错误断言。

## 7. Links

- [`provider-admission.md`](provider-admission.md) — provider 接入准入条件与门禁边界
- [`../internals/providers/providers.md`](../internals/providers/providers.md) — provider/discovery 与播放传输分层
- [`../client-api/README.md`](../client-api/README.md) — Source priority 与 API 目录
- [`../client-api/extending.md`](../client-api/extending.md) — Audius auth、cliamp builtin provenance
- [`../internals/providers/sources.md`](../internals/providers/sources.md) — Browse/identity/queue
- [`../internals/persistence/state.md`](../internals/persistence/state.md) — local state 与 Keychain 边界
- [`../internals/providers/radio-discovery.md`](../internals/providers/radio-discovery.md) — Radio Browser health behavior

## 播放时间线探针（真实 MusicKit）

需要真实 MusicKit 才能回答的问题（自然播完、helper 仲裁暂停、shuffle 开关等）用
`scripts/playback-probe.sh <ref> [seconds]` 采集：

- 隔离 socket/state/activity 到临时目录，只播放真实音频，不触碰日常会话；
- 给 helper 打开 `LILT_PLAYER_TIMELINE=1`（脚本会同时 `launchctl setenv`，因为 helper 经
  LaunchServices 启动不保证继承 shell 环境）；
- 时间线写在 `/tmp/lilt-player-timeline.log`，每行是 `key=value`：`raw`/`mapped` 状态、
  `pos`/`dur`、`entry`/`index`/`songs`/`entries`、`repeat`/`shuffle`、`stalled`、`peers`/`active`；
- 状态变化与 1 秒采样各一行；`peers` 是本机 helper 进程数（判断是否两个 MusicKit 客户端在争抢）。

`LILT_PROBE_APPEND=1` 保留上一次的时间线，用于连续多轮对比。脚本结束会 `launchctl unsetenv`。

`LILT_PROBE_PACING_MS=<ms>` 会传给 server 的 `LILT_QUEUE_PACING_MS`，用于调整有限队列分条填充
（append 回退路径）的间隔；不设置时 server 用默认 700ms。

`LILT_PROBE_ASSERT=0` 关闭 helper 的播放期进程活动断言，用于「helper 进程数 × 断言开关」的 2×2
对照；默认开启，关闭只用于对照实验。

### 可听性规则（重要）

- **Apple Music（MusicKit）没有 per-playback 音量**：输出电平归 macOS 所有，探针无法只降低自己的
  声音。因此播放 Apple Music 的探针**默认拒绝运行**，必须显式 `LILT_PROBE_AUDIO=1` 批准；脚本
  **绝不**改动系统音量（会干扰用户的其他播放）。
- **电台流与 preview（AVPlayer）** 是 lilt 自己的播放器，支持 `LILT_PLAYER_VOLUME=0.1` 这类
  per-playback 音量；它只能降低音量，**不算静音测试**，仍需用户批准有声窗口。
- 需要安静地复测 MusicKit 专属问题（OQ11/OQ16）时，只能约定一个短暂窗口；批量跑完即恢复。
  这台开发机的默认输出是 Yamaha 接口，`get volume settings` 返回 `missing value`（无软件音量），
  所以连"临时调低系统音量"都不一定有效——更不该依赖它。

## 真实会话回归探针（一条命令）

`scripts/check-open-questions.sh` 打包了一组必须真实会话才能确认的契约检查，每项输出 PASS/FAIL，
原始证据留在临时目录里；编号沿用历史台账条目名，多数对应条目已归档，仅 OQ17 仍未决：

```text
scripts/check-open-questions.sh --list                 # 有哪些检查
LILT_PROBE_AUDIO=1 scripts/check-open-questions.sh     # 全跑
LILT_PROBE_AUDIO=1 scripts/check-open-questions.sh OQ18 OQ17   # 只跑子集
```

覆盖：OQ18（`shuffle off` 是否真的生效）、OQ17（`stop` → 播专辑是否保留队列）、OQ16（并存两个 helper
时 10 次起播是否仍会自行暂停，需要第二个 helper，脚本会自行准备）、OQ11（单曲播完后是否报
`ended`，约 6 分钟）、OQ14（shuffle 状态是否上 wire；rail 文案由 hermetic 测试覆盖）。

同样遵守可听性规则：Apple Music 需要 `LILT_PROBE_AUDIO=1`，脚本不改系统音量。PASS 之后按台账生命
周期处理：删条目，把结论落到权威文档。
