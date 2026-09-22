# 集成测试设计 / Integration Testing

> **状态**：分层已落地。Apple Music 与 Radio（builtin + Radio Browser）有确定性
> contract/集成覆盖；真实 provider E2E 为 opt-in。Audius 的 discovery、播放与账号 OAuth 的 hermetic
> 覆盖已完成（另含 opt-in `LILT_AUDIUS_E2E=1` 真实 discovery/stream，以及一次人工真实 OAuth 验收）。

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
   `LILT_MPV_E2E=1`（真实 mpv 解码 + 进程回收，需要 `mpv` 在 `PATH` 上）。
3. 真实测试避免生产账户 mutation；能读则不写，必须创建的本地测试状态在 cleanup 删除。

## 4. Shared contract suite

每个 Source 都必须覆盖：`api.describe`/Source capabilities、identity/ref、search shape、play/result
state、single-source queue invariant、watch ordering、auth state、error mapping、disconnect。fixture 必须
从公开 API 验证 provider discovery 与播放传输的路由边界；不可把短期签名 URL 当作持久 Item
字段，并扫描 state、recent、favorites、public response、watch event 与测试日志。有限队列
还验证 `QueueState.source` 与每项/`PlaybackState.source` 一致、错误 source 的 `queue.add/ref`
返回 `source_mismatch`、source switch 不恢复旧队列。并发 discovery/auth 可以跨 Source；播放不可以。

## 5. Real E2E acceptance / cleanup

真实 E2E 至少验证一次可观察 happy path 与表中主要 degradation。结束时 MUST stop playback，删除
测试创建的本地 state/cache/fixture 资源；不得自动撤销用户的 Apple system permission。Audius
disconnect/revoke 仅在隔离的测试账户且测试明确要求时执行。所有 skip、环境要求与 cleanup 结果必须
可审计。

## 5b. 手动测试会话（`just manual-test`）

人 + agent 一起看真实行为时用它：`just manual-test` 先停止日常 `lilt serve`（因此停止日常播放），再
`just build`（macOS：CLI + 两个签名 helper；Linux：仅 CLI，播放走进程内 mpv；两种平台都记录构建标识
——commit、dirty 文件数、二进制的 sha256，macOS 另含 helper 的 sha256）到
`/tmp/lilt-manual-<stamp>/manifest.txt`，然后在**调用者所在的 Herdr workspace**
（`$HERDR_WORKSPACE_ID`，不用 UI 当前聚焦的那个）开一个新 tab：

**同一时刻只保留一个手动会话**：新运行会先替换上一个会话——关闭它的 tab（TUI + agent；若旧会话
就是本次运行所在的 tab，则只关旧 TUI pane，避免自杀），并用 `lilt quit` 停掉它留在
`/tmp/lilt-manual-*/sock` 上的私有 server（对已死的 socket 是成功 no-op）。当前活跃会话记录在
`/tmp/lilt-manual-session`（dir/tab/tui pane）；此外还按 "lilt manual" 标签清扫无 pointer 的
遗留 tab。
- 左 pane：`pi`（Herdr agent 名同会话名，例如 `manual-20260920-114007`）；
- 右 pane：`lilt tui`；
- 两个 pane 共用同一个**私有且新启动的** server（`/tmp/lilt-manual-<stamp>/{sock,state.json,config,radio.json}`），
  所以 agent 用 `./lilt` 执行的操作会实时出现在 TUI 上；日常实例已在 build 前停止，不会被复用；
- 键盘与鼠标写入 `/tmp/lilt-manual-<stamp>/log.jsonl`（`kind:"key"` / `"mouse"`，另有 `rpc`、
  `helper`、`navigate`/`play`/`queue` 等）；右 pane 退出时 pane 里的 shell 会补一条 `lilt quit`，
  关 tab 前也可用输出的 `cleanup` 命令收掉 server。

脚本在报告成功前会检查私有 socket 与 journal 文件确实存在（Herdr 的 `tab create --env` 不会传给
split pane，漏传会静默落到默认 socket）；检查失败会关掉自己开的 tab 并以非零退出。

它是**人工探索**，不替代第 4/5 节的 hermetic 与 contract 测试，也不产出可重放的 round 报告；需要可
重放的 agent 走查仍用 [`.agents/skills/usability-test/`](../../.agents/skills/usability-test/SKILL.md)。

## 6. Boundary

真实 provider 覆盖 happy path 和 major degradation；mock 覆盖需要确定性重现的全部分支。两者互补，
真实成功不能替代 mock 的错误断言。

## 7. Links

- [`provider-admission.md`](provider-admission.md) — provider 接入准入条件与门禁边界
- [`../internals/providers.md`](../internals/providers.md) — provider/discovery 与播放传输分层
- [`../client-api/README.md`](../client-api/README.md) — Source priority 与 API 目录
- [`../client-api/extending.md`](../client-api/extending.md) — Audius auth、cliamp builtin provenance
- [`../internals/sources.md`](../internals/sources.md) — Browse/identity/queue
- [`../internals/state.md`](../internals/state.md) — local state 与 Keychain 边界
- [`../internals/radio-discovery.md`](../internals/radio-discovery.md) — Radio Browser health behavior

## 播放时间线探针（真实 MusicKit）

无法用 hermetic 测试回答的问题（OQ11 自然播完、OQ16 helper 仲裁暂停）用
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

`LILT_PROBE_ASSERT=0` 关闭 helper 的播放期进程活动断言，用于 OQ16 的 2×2 对照（helper 进程数 ×
断言开关）；默认开启，关闭只用于对照实验。

### 可听性规则（重要）

- **Apple Music（MusicKit）没有 per-playback 音量**：输出电平归 macOS 所有，探针无法只降低自己的
  声音。因此播放 Apple Music 的探针**默认拒绝运行**，必须显式 `LILT_PROBE_AUDIO=1` 批准；脚本
  **绝不**改动系统音量（会干扰用户的其他播放）。
- **电台流与 preview（AVPlayer）** 是 lilt 自己的播放器，支持 `LILT_PLAYER_VOLUME=0.1` 这类
  per-playback 音量，可在不影响其他音频的前提下安静复测。
- 需要安静地复测 MusicKit 专属问题（OQ11/OQ16）时，只能约定一个短暂窗口；批量跑完即恢复。
  这台开发机的默认输出是 Yamaha 接口，`get volume settings` 返回 `missing value`（无软件音量），
  所以连"临时调低系统音量"都不一定有效——更不该依赖它。

## 未决问题的复测（一条命令）

`scripts/check-open-questions.sh` 把台账里等待真实会话的检查打包在一起，每项输出 PASS/FAIL，原始
证据留在临时目录里：

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
