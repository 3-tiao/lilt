# 待决问题台账

本文件记录**尚未解决**的工程问题：现象、已有证据、已试过什么、下一步。它是接手入口，
不是结论仓库。

分工：

| 内容 | 归属 |
|---|---|
| 未解决、需要继续查或需要决策 | 本文件 |
| 已接受的限制（不再尝试绕过） | [`limitations.md`](limitations.md) |
| 已定的产品/接口契约 | 对应 `docs/` 设计文档（如 [`../ui/model.md`](../ui/model.md)、[`../internals/helper-rpc.md`](../internals/helper-rpc.md)） |
| 已排期/未排期的产品范围 | [`roadmap.md`](roadmap.md) |

生命周期（三个状态，本文件只留前两个）：

1. **未修** —— 条目在，下一步写清。
2. **已修待复测** —— 代码已改但**必须**先用一次 usability 复测确认（复用同一人设与尺寸，
   报告名 `round-N-recheck.md`）；此时状态写成“已修待确认”。
3. **已归档** —— 复测通过后从这里**删除**，结论落到上表的权威文档（代码 + 回归测试 + 对应规范）。
   本文件不留历史，也不留“已修复清单”。

规则：

- **只写未解决项**（含“已修待复测”）。归档 = 删除，不是移到文末。
- 每条必须写清：现象、证据（批次名/命令/日志）、已排除的假设、下一步（可执行）。
- 严重度按「发生频率 × 影响 × 持久性」：核心路径反复失败为高；能绕过但需猜测为中；文案/留白为低。
- 真实会话证据来自 [`../../.agents/skills/usability-test/SKILL.md`](../../.agents/skills/usability-test/SKILL.md)
  的批次；运行产物（`/tmp/lilt-usability/*`）不入库，结论只保留在本文件与上表的权威文档。

## 摘要

| # | 问题 | 严重度 | 状态 | 下一步 |
|---|---|---|---|---|
| OQ1 | 专辑队列的一次性赋值被 MusicKit 拒绝，而歌单可以 | 高 | 已复现，原因未定 | 对比 `Playlist.entries` 与 `Album.with([.tracks])` 的曲目对象 |
| OQ3 | 大队列填充期间没有进度、没有部分失败语义 | 中 | 未做 | 先定契约（提前返回 vs 发布中间状态） |
| OQ4 | 队列填充 pacing 700ms 是否可降低 | 中 | 已测 300–700ms，失败与 pacing 无关（疑似时间相关） | 交错批次重测后再决定默认值 |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证 | 补一轮 counter-persona 走查 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ11 | 单曲队列播完后状态停在 `paused`，与用户暂停无法区分 | 中 | 已修待确认（`ended` 契约 + `■ Finished` + toggle 可重播） | 真实播完一次确认端到端后归档 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ14 | shuffle 生效后队列显示仍是提交顺序，界面像没随机 | 中 | 已复现 | 决定 rail 是否标注“已随机”或按播放顺序显示 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 已复现（同专辑随后又成功） | 直连 helper 连续 albumTracks，看是否为解析回退偶发失败 |
| OQ17 | 填充成功后 re-pin 失败丢弃整条已建队列 | 中 | 已修待确认（保留队列 + `partial_failure` + `queueReady`） | 真实复测 stop→play |
| OQ16 | 并存 MusicKit helper 下起播后自动转 paused（position 冻结） | 中 | 已修待确认（2 进程 4/10 → 断言后 0/12；1 进程 0/10） | 真实 TUI + 并存 helper 复测后归档 |

## OQ1 · 专辑队列的一次性赋值被 MusicKit 拒绝（高）

**现象**：专辑改用歌单那条已验证的路径（解析曲目 → 一次性
`ApplicationMusicPlayer.Queue(entries, startingAt:)` 赋值）时，`play` 失败：
`MPMusicPlayerControllerErrorDomain Code=6 "Failed to prepare to play"`。

**证据**（2026-09-20，真实账号 + 签名 helper，直连 helper RPC 探针）：

| 试的形状 | 结果 |
|---|---|
| 一次性赋值 + 库内解析曲目（`MusicLibraryRequest<Song>` 按 albumTitle） | `Code=6` |
| 一次性赋值 + catalog 解析曲目（`Album.with([.tracks])` / 按标题搜索命中） | `Code=6`（~1.5s） |
| 一次性赋值 + 纯 catalog 专辑 id | `Code=6`（~0.3s） |
| 同一台机器：歌单一次性赋值（`Playlist.entries`） | 成功，且 35 首队列 `jump 5` 准确 |

**已排除**：曲目来源（库内 vs catalog）、专辑 id 类型（资料库 vs catalog）、签名/授权（同一 helper
会话里歌单成功）。

**未定**：为什么歌单可以而专辑不可以。两者差别只剩曲目对象来源
（`Playlist.entries` 的 `Song` vs `Album.with([.tracks])` 的 `Song`）。

**下一步（可执行）**：

1. 用探针把**歌单**解析出的 `Song` 对象塞进“专辑形状”的赋值（`Queue(entries, startingAt:)`），
   看是否成功——若成功，说明差别在对象而非调用形状。
2. 对比两者 `Song` 的 `id` / `storefront` / `isLibrary` 等可读属性。
3. 若仍无解，考虑 [`limitations.md`](limitations.md) §7b 列的另一条路（我们拥有队列 +
   有界预读）或维持现状。

**关联**：[`limitations.md`](limitations.md) §7b、[`../internals/helper-rpc.md`](../internals/helper-rpc.md)。

## OQ3 · 大队列填充期间没有进度，也没有部分失败语义（中）

**现象**：专辑（12–20 首）或 `playback.playSongs` 填充时，界面只有 `working…`，约 10–40s 才完成；
期间客户端收不到任何中间状态，因为填充发生在**一个尚未返回的 RPC** 内（server 只在结束时 commit
一次）。

**下一步（需要先定契约）**：

1. 选项 A：`playback.play` 在“已经开始播放”时即返回，剩余填充在后台继续，队列随
   `playback.changed` 增长（客户端需要能显示“仍在加入”的队列状态）。
2. 选项 B：server 在填充过程中发布中间状态（新的公开字段或事件），客户端显示 `9/16`。
3. 无论哪种，都要定义**部分失败**语义：已加入的曲目保留、错误按“已加入 N/M”报告、不回滚已开始的音频。

**关联**：[`../ui/async-state.md`](../ui/async-state.md)、[`../client-api/commands.md`](../client-api/commands.md)。

## OQ4 · 队列填充 pacing 700ms 是否可降低（中）

**现象**：`internal/server/handlers_playback.go` 的 `startEngineQueueLocked` 每条 `enqueue` 后固定
等待 700ms（注释理由：背靠背 insert 会把 MusicKit player 卡死）。这是填充耗时的主要部分。

**测试台**：`scripts/queue-pacing-probe.sh`（每个 pacing 一个隔离 server；先 warm-up 一次单曲；
同一 helper 内连续播放 19 首专辑，避免"每次重启 helper"的启动噪声）。填充间隔用
`LILT_QUEUE_PACING_MS` 注入。

**数据**（2026-09-20，真实 MusicKit，19 首专辑 = 18 次 append）：

| pacing | 结果 | 填充耗时（秒，warm） |
|---|---|---|
| 700ms（默认） | 13/13 成功，queue=19，+3s 仍 playing | 14.1 / 14.7 / 14.8 / 14.9 / 15.5 / 15.6（快档） |
| 500ms | 3/3 成功 | 11.3 / 11.7 |
| 400ms | 一批 3/3 成功（9.0 / 8.9）；后一批 6/10（2 次 `playback_error`、2 次返回 `queue=0`） | 8.9–9.9（成功档） |
| 300ms | 3/3 成功（8.0 / 8.2） | — |

**未定（关键）**：400ms 的失败全部集中在**批次顺序的后半段**，同一时段 700ms 也出现过 1 次失败，
因此当前数据**无法把失败归因于 pacing**，更像是与时间相关的 helper/MusicKit 状态退化。**结论：
暂不改默认值**；需要**交错顺序**（700/400 交替、各自全新 server）的批次才能判定。

**顺带发现（真 bug，已单列）**：填充成功后 re-pin 失败会让整个 `play` 返回 `playback_error`，丢掉
已经建好的 19 首队列；探针里 `stop` 紧接着 `play` 时 400ms 批次 5/5 触发。

**下一步（可执行）**：

1. 用交错批次（`LILT_PROBE_PACINGS="700 400 700 400 …"`，或改脚本支持交替）重测，各 ≥10 次。
2. 若 400ms 与 700ms 无差异，把默认降到 400ms 并在注释里写明实测依据。
3. 若仍有差异，保留 700ms，并把"卡死"的真实触发条件写清楚（目前只有注释里的历史结论）。

**关联**：[`../internals/helper-rpc.md`](../internals/helper-rpc.md)、[`../testing/integration.md`](../testing/integration.md)。

## OQ17 · 填充成功后 re-pin 失败会丢弃整条已建队列（中，已修待确认）

**现象**：有限队列填充完成后，`startEngineQueueLocked` 会 re-pin（`ResumeState`）以防 MusicKit 停在
stopped/paused。re-pin 失败时整个 `playback.play` 返回 `playback_error`，而**队列其实已经建好**
（19 首），用户看到"播放失败"且队列不可见。

**证据**（2026-09-20，`scripts/queue-pacing-probe.sh`，真实 MusicKit）：`stop` 紧接着 `play` 专辑时
400ms 批次 5/5 失败（填充耗时 9.2–17.8s，说明填充已完成）；同一 helper 内不插 `stop` 连播 3/3 成功。

**已修（契约 + 实现）**：`startEngineQueueLocked` 用 `errQueueReadyNotPlaying` 区分"队列已建、仅起播
失败"，调用方改为提交这条队列并返回 `partial_failure` + `details.queueReady:true`，state 带
`playbackError` 说明原因、status 为 `paused`。客户端保留队列、提示重按播放，不再重建。
hermetic 覆盖：`fakeengine` 新增 `ParkAfterEnqueue` / `FailResume`，测试断言队列未丢且已提交到
session state。文档同步 [`../client-api/errors.md`](../client-api/errors.md) 与
[`../client-api/commands.md`](../client-api/commands.md)。

**待确认**：需要一次真实复测（`stop` → 立即 `play` 专辑）确认失败时队列确实保留且可 resume。

**关联**：[`../internals/helper-rpc.md`](../internals/helper-rpc.md)、OQ3（填充进度与部分失败语义）。

## OQ15 · 资料库专辑详情偶发解析失败（中）

**现象**：打开资料库里的 `A LA SALA` 时底部报 `Apple Music album lookup failed`；同一次会话稍后
再打开同一张专辑又成功。

**证据**：`2026-09-20-form-fix-recheck` r3-recheck，日志中 `albumTracks` 两次返回
`invalidReference`（helper 的 `albumSongs` 在库内过滤与 catalog 标题搜索两条路径都没拿到曲目），
随后同一专辑的 `albumTracks` 又 `ok`。

**下一步**：直连 helper 连续调用 `albumTracks` 观察失败率，并在 helper 内为“库内过滤命中 0 首”
增加日志（哪条回退路径失败），再决定是加重试还是修解析。

## 已定的决策（记录以免反复讨论）

- **搜索结果 Enter = 只播该行**（`pageClass: aggregate`）；连播改用逐行 `e`/`E` 或 CLI/agent 的
  `playSongs`。见 [`../ui/model.md`](../ui/model.md) §6。
- **专辑是容器**：Enter = 从该行播到专辑末，`p` 整张，`S` 洗牌；专辑详情不重复专辑行。
- **专辑播放不记录 recent container**：`recentContainers` 只保存歌单（[`../internals/state.md`](../internals/state.md)）。
- **队列所有权暂不迁移**：Apple 继续由 MusicKit 拥有队列（helper 维护 canonical 影子列表），
  Audius/Radio 由 server 拥有；不改为“provider 只播单条”，因为会削弱 Apple 的无缝衔接。
