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
| OQ3 | 大队列填充期间没有进度、没有部分失败语义 | 中 | 已修待确认（`queueFill` 进度 + `partial_failure` 计数） | 真实专辑播放确认 |
| OQ4 | 队列填充 pacing 700ms 是否可降低 | 中 | 已测 300–700ms，失败与 pacing 无关（疑似时间相关） | 交错批次重测后再决定默认值 |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证 | 补一轮 counter-persona 走查 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ11 | 单曲队列播完后状态停在 `paused`，与用户暂停无法区分 | 中 | 已修待确认（`ended` 契约 + `■ Finished` + toggle 可重播） | 真实播完一次确认端到端后归档 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ14 | shuffle 生效后队列显示仍是提交顺序，界面像没随机 | 中 | 已修待确认（rail 标注 `· SHUFFLED`，不重排） | usability 复测确认标注足够 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 已复现（同专辑随后又成功） | 直连 helper 连续 albumTracks，看是否为解析回退偶发失败 |
| OQ17 | 填充成功后 re-pin 失败丢弃整条已建队列 | 中 | 已修待确认（保留队列 + `partial_failure` + `queueReady`） | 真实复测 stop→play |
| OQ18 | 再按一次 `S` 关不掉 shuffle | 中 | 已复现（用户报告）；TUI/server 已排除 | 探针区分"赋值不生效"vs"回读滞后" |
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

## OQ3 · 大队列填充期间没有进度，也没有部分失败语义（中，已修待确认）

**现象**：专辑（12–20 首）或 `playback.playSongs` 填充时，界面只有 `working…`，约 10–40s 才完成；
期间客户端收不到任何中间状态，因为填充发生在**一个尚未返回的 RPC** 内（server 只在结束时 commit
一次）。被 engine 拒绝的条目还会被静默跳过，用户只看到队列变短。

**已修（契约选 B：保持命令同步，用已有 watch 通道发布进度）**：

- `PlaybackStatus.queueFill:{queued,total}` 只在填充进行中出现，随每条 append 的
  `playback.changed` 发布，结束后为 `null`。没有选方案 A（提前返回 + 后台填充）：那会改变提交语义，
  并绕过 re-pin 保护（OQ17）。
- 被拒绝的 append 计入 `partial_failure` 的 `details.added/skipped/total`，队列保留，不再静默缩短。
- TUI Now Playing 显示 `working… 9/16 — large queues are added track by track`，仅在无进度时才退回
  按耗时估算的文案。
- hermetic 覆盖：`fakeengine.RefuseEnqueue` 复现部分填充；测试断言进度事件与最终计数。

**待确认**：真实专辑播放时确认进度可见、部分失败文案可理解。

**关联**：[`../client-api/models.md`](../client-api/models.md)、[`../client-api/commands.md`](../client-api/commands.md)、
OQ17。

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

**真实复测（2026-09-21，第一次跑）**：`stop` → `play` 专辑**没有失败**，队列按预期填满（helper 日志
`queueSongs` 2 → 19、`status=playing`，18 次 `enqueue` 全部 `ok`）。但那次响应与提交的状态是**空的**
（`status:""`、无 queue），也就是说复测顺带抓到**另一个**回归：album/`playSongs` 成功路径里
`state, fill, fillErr := …` 用 `:=` 遮蔽了外层 `state`，于是"音频在放、状态说没在放"。已修，并补
`TestSuccessfulFillReturnsTheQueue`（去掉修复即失败，JSON 与真机一致）。

**待确认**：修完这个回归后需要再跑一次 `stop` → `play` 专辑，确认 OQ17 本身（re-pin 失败时保留队列）
在真实路径上的表现。

**关联**：[`../internals/helper-rpc.md`](../internals/helper-rpc.md)、OQ3（填充进度与部分失败语义）。

## OQ5 · A1（搜索结果 Enter 只播该行）的证据强度（中）

**现状**：A1 已实现（`pageClass` 决定激活语义，见 [`../ui/model.md`](../ui/model.md) §6），并由一轮
**中立目标**走查支持：用户搜到精确歌名 → Enter → 只播一首，没有疑惑，随后自己用 `?` 找到 `e` 排下一首。

**已知弱点**：该轮之前的一轮 prompt 里写过“播放一首（单曲）”，存在引导；且“想连着听某位歌手多首”
的 counter-persona 轮尚未跑。

**下一步**：下一批加一轮 counter-persona（想连听某歌手多首、不出现“一首/继续听”提示），
观察用户是否能自己发现 `e`/`E` 或退回歌单详情；若发现不了，再评估是否需要显式的“排入整节”动作。

## OQ6 · Up Next 删除待排项没有 Undo（低）

**现象**：`0` → 选中行 → `x` 立即删除，只有 `Removed: …` 提示，没有撤销入口。

**下一步**：产品确认是否需要（考虑 `x` 的误触成本与队列可重建性）；需要时给短时 Undo 提示。

## OQ11 · 单曲队列播完后状态停在 `paused`（中）

**现象**：有限队列的最后一首自然播完后，`PlaybackState.status` 仍是 `paused`，与"用户主动暂停"
完全同形，客户端无法表达"已播完"。

**证据**（2026-09-20，`scripts/playback-probe.sh`，真实 MusicKit，单曲队列 322.467s）：

| 时刻 | raw status | position |
|---|---|---|
| 结束前最后 3 个 playing 采样 | `playing` | 320.164 / 321.164 / **322.164** |
| 状态变化 | `paused` | **0.011**（位置被归零） |
| 之后每秒采样 | `paused` | 0.638 → 0.000 |

`entry` 与 `duration` 保持不变，`currentEntry` 仍在。**结论：MusicKit 不提供静态的"结束"属性，
但结束是可判定的**——"观察到接近末尾" + "随后 paused" 这个序列只在自然播完时出现；用户暂停时
position 停在暂停点（>0 且 < duration），起播后立刻暂停则从未观察到高位置。

**已定判据**（helper 侧，纯函数 + 单测）：按 entry 维护位置高水位；`paused` + 高水位 ≥
`duration - 1s` + 有限队列 + `repeat=off` + 非 shuffle ⇒ 自然结束。见
`player/Sources/LiltPlayerLogic/PlaybackProbe.swift` 的 `playbackProbeHasEnded` /
`reachedEndOfEntry`。

**已修（契约已落地）**：`PlaybackStatus.status` 新增 `ended`；helper 用位置高水位判定后由
`endedPlaybackStatus` 输出，`playback.toggle` 对 `ended` 执行 resume（不再报 `invalid_state`），
TUI 显示 `■ Finished`。文档同步到 [`../client-api/models.md`](../client-api/models.md) 与
[`../ui/ux.md`](../ui/ux.md)；单测覆盖判定规则、状态映射与 TUI 文案。

**待确认**：需要一次真实播放（单曲队列自然播完）确认端到端出现 `status:"ended"` 且 TUI 显示
`Finished`，之后按台账生命周期归档。

**关联**：`repeatMode:"all"` 与 live stream 不受影响（前者回到首首，后者无队列）。

## OQ12 · 播放时主面板仍是浏览列表（低，需求待澄清）

**现象**（2026-09-20 手动会话，用户截图）：播放中左侧仍是 Home/浏览列表，用户评价“体验一般”，
但一时说不清想要什么；也不能确定“不显示列表”后用户如何切换浏览。

**已查到的数据**（但不代表问题被定位）：30 行终端下 `layout()` 的实测——

| 终端宽 | main | rail | rail 每行可见字符 | main 每行可见字符 |
|---:|---:|---:|---:|---:|
| 92 | 53 | 36 | 25 | 44 |
| 110 | 64 | 43 | 32 | 44 |
| 140 | 89 | 48 | 37 | 44 |

也就是说 rail 的每行预算约为 `panelWidth-11`（cursor 2 + padding 2 + 内边距 2 + 滚动条 1 + 播放
marker 2 + 文本），`docs/ui/design-system.md` §2.2 还规定 rail 宽度必须由“main/queue 可读宽度”
推导，而代码用的是固定百分比 `clamp(width*2/5, 36, 48)`。

**用户明确表示这不是重点**：宽度截断的结论已撤回，不要把 OQ12 当成“rail 太窄”的 bug。

**已排除（契约层面）**：不能让队列占用 main 面板——`design-system.md` §2.2 规定 main 始终承担
Home/Recent/Browse/结果页/detail；窄终端只显示 main，队列靠 `0` / `:queue` 打开。要改这条得同时
改键位、底栏提示与鼠标命中规则。

**下一步**：等用户把不满具体化（例如：播放时想看到与当前曲目相关的内容？想让列表自动跟随播放？
还是纯视觉上的空旷？），再决定是否动布局；它属体验候选，不是已确认缺陷。

## OQ14 · shuffle 生效后队列显示仍是提交顺序（中，已修待确认）

**现象**：在歌单里按 `S` 后提示 `Shuffling: …`，但 `UP NEXT` 的顺序与歌单原顺序逐首一致，用户据此
判断"随机没有生效"。实际播放顺序已随机，只是 wire 状态按 canonical 提交顺序投影。

**已修（展示语义已定：标注，不重排）**：shuffle 开启时 rail 标题变为 `UP NEXT · SHUFFLED`，并在
[`../ui/ux.md`](../ui/ux.md) 说明"行序 = 提交顺序，播放顺序由 MusicKit 决定"。**不按播放顺序重排**：
`queue jump/remove/move` 都按提交顺序的 index 操作，重排会破坏这套语义
（[`limitations.md`](limitations.md) §7）。顺带修掉一处死代码：`queueTitle()` 已存在但四个调用点都
硬编码了标题。

**待确认**：真实会话里再按一次 `S`，确认标注足以让人不再误判（usability 复测）。

## OQ16 · 并存 MusicKit helper 下起播后自动转 paused（中，已修待确认）

**现象**：新 helper 起播成功（`status=playing`，position 正常前进），若干秒后**自行**变为
`paused`，position 冻结；`playback.pause/toggle` 无法恢复，`playback.stop` 正常。用户视角是
"点播了但只响了一下就停"。

**证据**（2026-09-20，`scripts/playback-probe.sh`，真实 MusicKit，单曲 `apple-music:song:471749203`，
每格 10–12 次起播，10s 窗口）：

| helper 进程数 | 播放期活动断言 | 自动暂停 |
|---|---|---|
| 2（另一个空闲 helper 在场） | 关 | **4 / 10**（暂停时 position 2.6 / 8.3 / 9.2 / 14.2s，之后冻结） |
| 2 | 开 | **0 / 12** |
| 1 | 开 | **0 / 10** |
| 1 | 关 | **0 / 10** |

机制证据：

- 暂停由 **MusicKit 自己**报告（时间线 `raw=paused`），lilt 的 stall 推断没有参与。
- 暂停前 1 秒采样器静默约 **5 秒**，说明进程被系统节流/挂起，而非音频缓冲失败。
- 旧记录的"暂停期间 position 仍推进"**不成立**：本次观测中 position 在暂停时冻结。
- 触发条件需要**第二个 helper 进程**（1 个进程时，断言开关都不复现）；但**修复不需要跨进程协调**：
  播放期间持有 `ProcessInfo.beginActivity([.userInitiated, .latencyCritical])` 即在 2 进程下归零。
  因此**不做 helper lease/所有权**。

**已修（helper 侧）**：`syncPlaybackActivity` 在播放（含 `waitingToPlayAtSpecifiedRate`）期间持有
进程活动断言，停止时释放；`LILT_PLAYER_ACTIVITY_ASSERT=0` 只是上面这张对照表的探针开关。

**待确认**：探针只覆盖 CLI 单曲起播。需要一次真实复测（TUI + 并存 helper 场景）确认用户路径同样
不再出现"只响一下就停"，然后按台账生命周期归档本条目。

**关联**：[`limitations.md`](limitations.md) §8、[`../internals/helper-rpc.md`](../internals/helper-rpc.md)、
[`../testing/integration.md`](../testing/integration.md) 的播放时间线探针。

## OQ18 · 再按一次 `S` 关不掉 shuffle（中）

**现象**（2026-09-20，用户报告）：TUI 里按 `S` 能开启 shuffle；**再按一次仍然是开启**，关不掉。

**代码路径**：`S` → `Model.toggleShuffle()`（`on := !m.state.Shuffle`）→ `playback.shuffle{on}`
→ `handleSetShuffle` → helper `setShuffle`
（`ApplicationMusicPlayer.shared.state.shuffleMode = on ? .songs : .off`）→ 立即 `state()` 回读
（`shuffle: player.state.shuffleMode == .songs`）→ 提交并回给 TUI。

**真实证据**（2026-09-21，`scripts/check-open-questions.sh OQ18 OQ17 OQ14`，隔离会话）：

- `lilt shuffle on` 返回 `ok`，但响应里 `shuffle:false`、`mode:none`、`status:stopped`（当时没有在播放）。
- server journal 显示该命令确实转发到了 helper（`rpc setShuffle ms=0 ok=True`），所以不是 server 吞掉了。
- 同一批次里 `lilt shuffle off` 之后仍是 `shuffle:false` —— 也就是说 **wire 上永远读不到 `shuffle:true`**，
  TUI 于是每次都算 `!false` 发 `on:true`，这正是"再按一次关不掉"。
- **OQ14 的 wire 检查因此同样失败**：两者同一个根因（`shuffle` 状态读不出来）。

**已排除**：

- **TUI 侧**：`TestShuffleKeyTogglesBackOff` 用 fake engine 连按两次 `S`，断言 `shuffle` 回到 `false`。
- **server 侧**：`handleSetShuffle` 正常 `SetShuffle` 并 `commitPlaybackLocked`，没有条件分支会吞掉 `on:false`；
  且 journal 证明命令到达了 helper。
- 因此问题在 **helper 的 MusicKit 交互**：要么 `state.shuffleMode` 赋值不生效，要么赋值后**立即回读是旧值**
  （异步生效），两种都会让 TUI 一直以为"当前是关"，于是每次按 `S` 都发 `on:true`。

**好消息**：这条探针**不产生音频**（没有播放），可以随时安静地跑。

**下一步（明天一起做，工具已就绪）**：

1. 打开 helper 时间线（`LILT_PLAYER_TIMELINE=1`，已记录 `shuffle=`）：依次 `lilt shuffle on` /
   `lilt shuffle off`，读 `/tmp/lilt-player-timeline.log`，看第二次之后 `shuffle` 字段是 `true` 还是
   `false`。
2. 直连 helper RPC 连续调 `setShuffle{on:true}` → `setShuffle{on:false}`，打印两次返回的
   `state.shuffle`；再隔 1 秒 `state` 复读一次，区分"赋值不生效"与"回读滞后"。
3. 若只是回读滞后：helper 在 `setShuffle` 后确认生效再返回（有界重试/复读）。
4. 若赋值不生效：改走已验证的路径——按当前顺序重建队列（`queueJump` 的 rebuild 流程已经会临时
   `shuffleMode = .off` 再恢复，可复用同一套手法）。
5. 修完补 hermetic 测试（TUI 已覆盖）+ 真实复测。

**关联**：OQ14（shuffle 的显示语义）、[`limitations.md`](limitations.md) §7。

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
