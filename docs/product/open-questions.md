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
| OQ3 | 大队列填充期间没有进度、没有部分失败语义 | 中 | 已修待确认（进度走 watch 事件，真实路径已确认 2/19→19/19） | TUI 视觉确认 |
| OQ4 | 队列填充 pacing 700ms 是否可降低 | 中 | 已测（含交错批次）：700ms 9/9，400ms 6/9 且失败同轮聚集 | 默认保持 700ms；真正问题是填充后 player 停住（OQ17） |
| OQ17 | 填充后 re-pin 被拒，停在"队列就绪未播放" | 中 | 已复现（间歇 ~30%；resume 3/3 失败、重播 1/3 成功） | 复现时用检查保存的 helper 时间线定位 |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证 | 补一轮 counter-persona 走查 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ14 | shuffle 生效后队列显示仍是提交顺序，界面像没随机 | 中 | 已修待确认（rail 标注 `· SHUFFLED` + wire 已能读到 shuffle） | usability 复测确认标注足够 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 已复现（同专辑随后又成功） | 直连 helper 连续 albumTracks，看是否为解析回退偶发失败 |
| OQ18 | 再按一次 `S` 关不掉 shuffle | 中 | 已修待确认（S 统一为开关 + 播放携带 form） | TUI 真按两次确认 |
| OQ19 | 切歌后 `Space` 暂停不稳定（真实会话） | 低 | 已复现（2026-09-21 隔离重放；helper 时间线定位到 play/pause 异步竞态） | 设计修复：play 响应等待 play() 完成或 helper 内串行化暂停 |
| OQ20 | 队列焦点内 `f` 的收藏目标与反馈歧义 | 低 | 部分复现（fake 出现瞬时 toast，主列表选中行常为 header） | 复现后决定：焦点内作用于队列 cursor 行并命名目标 |
| OQ24 | 命令面板 `:browse` 列出但执行 Unknown command；`:discover` 静默无反馈 | 中 | 单轮稳定复现（两条执行路径） | 查面板列表与执行器的来源过滤是否不一致 |
| OQ25 | All Favorites 空态无引导文案 | 低 | 跨轮复现（r1+r4） | 补空态说明（同 Recent 的风格） |
| OQ26 | 30s 后自动插入 Recently Played 组时光标跳变、toast 目标错位 | 中 | 单轮（r1 fake）待复现 | 干净装置重放键序；确认选中行漂移规则 |
| OQ27 | 低严重度单轮候选集（导航/文案） | 低 | 各单轮待复现 | 成组复现后逐条定级，见条目内清单 |
| OQ28 | jamendo 曲名 HTML 实体未解码（`&amp;` 上屏） | 中 | 复测轮盲测命中 + server JSON 探针（`internal/jamendo` 无实体处理） | 在 jamendo 元数据层解码实体 + 单测；确认其他来源是否同病 |
| OQ29 | 复测轮低严重度候选集（焦点/队列等待/footer 溢出） | 低 | 复测 r2/r3 各单轮 | 成组复现后逐条定级，见条目内清单 |

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

**真实确认（2026-09-21）**：`scripts/check-open-questions.sh` 期间用 watch 客户端观察真实专辑填充，
`playback.changed` 依次给出 `queueFill=2/19 … 19/19`，随后 `none`（清空）+ 完整队列。

**通道说明**：进度只走 **watch 事件**。填充期间 server 持有命令锁，`session.status` 会一直等到填充
结束才返回，所以它看不到进行中的 `queueFill`（早先想让它也返回进度的做法不可达，已删）。

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

**交错批次（2026-09-21，700/400 交替、各自全新 server、每档 3 次 × 3 轮）**：

| pacing | 轮次 | 结果 |
|---|---|---|
| 700ms | 3 轮 × 3 | **9/9 成功**，warm 填充 ~15s |
| 400ms | 第 1、2 轮 | **6/6 成功**（填充 ~9.2–9.9s） |
| 400ms | 第 3 轮 | **3/3 失败**（`partial_failure`，队列 19 首保留） |

失败仍然**按时间聚集**（同一时段 700ms 9/9 通过），因此**无法归因于 pacing**。**结论：默认值保持
700ms**；真正的问题是"填充有时把 player 停在无法起播的状态"，见 OQ17。

**顺带发现（真 bug，已单列）**：填充成功后 re-pin 失败会让整个 `play` 返回 `playback_error`，丢掉
已经建好的 19 首队列；探针里 `stop` 紧接着 `play` 时 400ms 批次 5/5 触发。

**下一步（可执行）**：

1. 用交错批次（`LILT_PROBE_PACINGS="700 400 700 400 …"`，或改脚本支持交替）重测，各 ≥10 次。
2. 若 400ms 与 700ms 无差异，把默认降到 400ms 并在注释里写明实测依据。
3. 若仍有差异，保留 700ms，并把"卡死"的真实触发条件写清楚（目前只有注释里的历史结论）。

**关联**：[`../internals/helper-rpc.md`](../internals/helper-rpc.md)、[`../testing/integration.md`](../testing/integration.md)。

## OQ17 · `stop` 之后紧接着播放会停在"队列已就绪但未播放"（中）

**现象**：`stop` 之后立刻 `play <album>`，填充全部成功（19 首），但 re-pin 被 MusicKit 拒绝，用户看到
的是"队列就绪但没在播放"（需要再按一次播放）。

**证据**（2026-09-21，`scripts/check-open-questions.sh OQ17`，真实 MusicKit）：
`play` 返回 `partial_failure` + `details.queueReady:true` + 19 首队列（这是修好"丢弃整条队列"之后的
正确行为）；同一 helper 内不插 `stop` 连播则成功起播。

**补充证据（2026-09-21，反复跑该检查）**：

- 命中率不稳定：某批 10 次里 3 次 `queueReady`，另一批 8 次 0 次 —— 与机器/守护进程状态相关。
- **恢复手段都不可靠**：命中后 `resume` 3/3 变成 `stopped`；重新发起整个播放 1/3 成功。
- 交错 pacing 批次里，失败集中在某一轮（同轮 700ms 9/9 通过），说明**不是 pacing**，而是**填充把
  player 停在无法起播的状态**（与 OQ4 的"wedge"同源）。
- 现在的检查在命中时会保存 helper 时间线（`oq17-timeline.log`），下次复现即可看到 MusicKit 当时的
  `playbackStatus` / `currentEntry`。

**未定**：为什么填充之后 `ResumeState` 会失败（`MPMusicPlayerControllerErrorDomain Code=1`）。
已知：填充本身没问题、队列完好、`resume` 与"重新播放"都不能稳定救回。

**下一步（可执行）**：

1. 用探针复现最小条件（`stop` → 立即 `play`），抓 helper 时间线里 `stop` 与随后 `resume` 的
   `playbackStatus`/`currentEntry`。
2. 二选一：helper 在 re-pin 失败时有界重试一次；或 server 在 `queueReady` 时自动重试起播一次，
   仍失败才回报。
3. 补回归：`stop` → `play` 的成功路径（现在只有 partial_failure 路径被覆盖）。

**关联**：[`../client-api/errors.md`](../client-api/errors.md)（`queueReady`）、
[`../internals/helper-rpc.md`](../internals/helper-rpc.md)。

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

**已修（wire 侧）**：`shuffle` 现在能在 wire 上读到了（见 OQ18 的 form 修复）；rail 标注保持不变。

**补充（2026-09-21，usability r13 r4）**：shuffle 下跳转曾把跳过的行置灰为已播历史——同一“行序 ≠
播放顺序”语义的另一个伤害面。已修：历史置灰规则只在 shuffle 关闭时生效（[`../ui/ux.md`](../ui/ux.md)），
跳过的行按 upcoming 呈现。

**待确认**：真实会话里再按一次 `S`，确认标注足以让人不再误判（usability 复测）。

## OQ18 · 再按一次 `S` 关不掉 shuffle（中，已修待确认）

**现象**（2026-09-21，用户报告）：TUI 里按 `S` 能开启 shuffle；**再按一次仍然是开启**，关不掉。

**根因（两个，都是 TUI/契约侧，不是 MusicKit）**：

1. **`S` 在容器详情页不是开关**：歌单/专辑详情页里 `S` 被绑成"洗牌播放这个容器"（一次性动作），
   再按只是再洗牌播放一次，永远关不掉。其他 surface 才是 `toggleShuffle`。
2. **播放会重置 form**：server 的播放路径"从已知 form 开始"（`playForm(nil,"") → off`），
   而 TUI 的播放请求不带 shuffle/repeat，于是"开了 shuffle 再按 Enter/`p`"会被重置为 off。

**排除过程**：先怀疑 MusicKit。真实探针（`scripts/check-open-questions.sh`）：

| 探针 | 结果 |
|---|---|
| OQ18（空闲时 `shuffle on`） | `shuffle:false`；helper 时间线也 `shuffle=false`（空闲态赋值/回读都无效） |
| OQ18P（**播放中** `shuffle on/off`） | `before=False → after on=True → after off=False` **PASS** |

即：播放中 MusicKit 的 shuffle **完全正常**，问题在 TUI 的按键语义与 form 传递。空闲态不生效属于
MusicKit 的正常行为（没有队列可洗牌），不是缺陷。

**已修**：

- `S` 在所有 surface 统一为 **shuffle 开关**（容器页不再变成洗牌播放）；乱序播放 = 先 `S` 再
  `Enter`/`p`。顺带删掉 `playAlbum/playPlaylist` 不再使用的 `shuffle` 参数。
- 播放请求携带当前 form：`core.PlaybackForm`（shuffle/repeat）随 `playback.play` 与
  `playback.playSongs` 发送，TUI 的每条播放路径都带上用户的当前状态；否则 server 的"已知 form"
  会把刚打开的 shuffle 清掉。
- 真机验证：`play album --shuffle` 全程 `shuffle=True / playing / queue=19`；`shuffle off` 立即生效；
  不带参数的播放把 form 重置为 off。

**待确认**：TUI 里真实按 `S` 两次（歌单页与专辑页各一次）确认开关行为与提示（usability 复测）。

**关联**：OQ14、[`../client-api/commands.md`](../client-api/commands.md)、[`../ui/model.md`](../ui/model.md)。

## OQ15 · 资料库专辑详情偶发解析失败（中）

**现象**：打开资料库里的 `A LA SALA` 时底部报 `Apple Music album lookup failed`；同一次会话稍后
再打开同一张专辑又成功。

**证据**：`2026-09-20-form-fix-recheck` r3-recheck，日志中 `albumTracks` 两次返回
`invalidReference`（helper 的 `albumSongs` 在库内过滤与 catalog 标题搜索两条路径都没拿到曲目），
随后同一专辑的 `albumTracks` 又 `ok`。

**下一步**：直连 helper 连续调用 `albumTracks` 观察失败率，并在 helper 内为“库内过滤命中 0 首”
增加日志（哪条回退路径失败），再决定是加重试还是修解析。

## OQ19 · 切歌后 `Space` 暂停不稳定（低，已复现）

**现象**：usability batch 2026-09-21-r13 r4（real）：播放刚启动/切歌后立即 `Space`，界面仍显示
`Playing` 且进度继续；第二次 `Space` 才暂停。

**复现与证据**（2026-09-21，隔离 server + `LILT_PLAYER_TIMELINE=1`，真实 MusicKit）：

1. 播放歌单（48 首）→ `queue jump 2` → helper 时间线：`playing(idx=0)` → jump 后 `paused`
   → `playing(idx=2)` → 6ms 后 `paused` —— jump 响应返回 paused，但播放实际还起了一下，
   未请求暂停而音量静掉。
2. `resume` 到 playing 后发起新播放（单曲）→ **play 响应返回 `paused`**（音质尚未起）→
   紧接的 `pause` 响应返回 **`playing`**（用户暂停被吞）→ 时间线：`t=339.24 paused`
   → `t=340.005 playing`（play 的 `play()` 才落地）→ `t=340.055 paused`（pause 此时才应用）。
   第二次 `pause` 才稳定为 paused。

**根因层**：helper。server 已按命令串行化，但 helper 的 `play()` 是异步的：play 响应在
queue 构建后就返回（此时未起播），而 `play()` 完成晚于响应；落在这个窗口内的 pause 已被
应用，但响应读到的是 play 完成前/后的旧状态，造成响应与实际相反。r4 症状即此窗口。

**已排除**：非 TUI 能力快照问题（高-1 已修）；非队列限速（OQ4）；非 OQ17 的“队列就绪
未播放”本身（那只是同窗口的另一表现）。

**下一步（设计后修复）**：play/pause 响应语义二选一：① helper 的 play 响应等待 `play()`
promise 完成后再返回（响应反映真实状态）；② helper 内部把 pause 排队到 play 完成之后，
且响应由 pause 落地后的状态生成。修后用同一重放脚本回归。

## OQ20 · 队列焦点内 `f` 的收藏目标与反馈歧义（低）

**现象**：r4（real）：队列焦点内按 `f`，参与者未见反馈；离开焦点后 `f` 收藏的是主列表选中行
而非正在播放的歌。fake 复核：`f` 始终作用于主列表选中行（`selectedItem()`），进入详情页后选中行
常是 header，toast `This row can't be favorited` 短暂且易错过。

**下一步**：复现后决定语义：队列焦点内 `f` 应作用于队列 cursor 行，并在反馈中写出目标歌曲；
或至少把 toast 持久化到 feedback band。

**关联**：r4#3、[`../ui/ux.md`](../ui/ux.md)。

## OQ24 · 命令面板 `:browse` 列出但执行 Unknown command；`:discover` 静默无反馈（中）

**现象**：`:` 面板列表显示 `:browse`，执行（直接输入或 Tab 选中）都报
`Unknown command: :browse`；`:discover` 被识别但执行后无任何可见变化（当时来源无 Discover
tab）。面板广告与执行器行为不一致。

**证据**：batch `2026-09-22-jamendo-tui` r4（fake/Apple Music preview），参与者两条路径各试
一次均稳定复现。

**已排除**：输入错误（Tab 选中列表项路径同样失败）。

**下一步**：root-cause 面板命令列表与执行器的来源过滤逻辑；`:discover` 在无该视图的来源应
给出提示而非静默。

## OQ25 · All Favorites 空态无引导文案（低）

**现象**：ALL FAVORITES (0) 只显示孤零零 `(empty)`，对比 RECENT (0) 有
"(empty) — tracks show here after 30s of listening" 的解释；不一致且无引导。

**证据**：batch `2026-09-22-jamendo-tui` r1 + r4 跨轮独立命中。

**下一步**：补空态说明（如"播放时按 f 收藏"），与 Recent 空态风格对齐。

## OQ26 · 自动插入 Recently Played 组时光标跳变、toast 目标错位（中）

**现象**：播放满 30s 时 Home 自动出现 "Recently Played" 分组，光标跳到新组；此时按 `f`
意图取消先前收藏的 playlist，toast 却显示 "Unfavorited: fake track"（该曲从未被收藏），
且 playlist 的 ★ 与 Favorites 分组消失——操作对象与反馈都不符合用户预期。

**证据**：batch `2026-09-22-jamendo-tui` r1（fake），键序见 `round-1-keys.log`。

**已排除**：——（尚未在干净装置重放；fake 数据命名 "fake track" 与列表名不一致为装置噪声，
不影响本条结论。）

**下一步**：干净装置重放：播放任意曲满 30s 后观察光标归属与 `f` 的目标行；确定列表插入分组时
的选中行保持规则与收藏目标的绑定。

## OQ27 · 低严重度单轮候选集（低）

**现象**（各单轮一次，均待复现，batch `2026-09-22-jamendo-tui`）：

- r1：Recent/Discover 顶层 tab 里 `Escape` 无返回效果；`Escape` 返回后光标不保留；Account
  行 `Enter` 只弹 toast 无页面。
- r2：UP NEXT 窄面板同名曲目截断难区分；`e` 入队无明确 toast；仅 5 首也提示
  "large queues are added track by track"。
- r2/r3：来源切换弹窗无数字快捷键；纯文本下选中项高亮不可见（`--ansi` 才可见）。复测
  （2026-09-22-recheck r2/r3-recheck）再次独立命中，共 4 轮——仍为低（仅文本抓屏/色弱场景），
  下一步与其它条目一并成组修复。
- r3：`v` 停止并清空队列后 Home "Continue Playing — 1/4" 仍引用已不存在的队列。
- r4：Track Info 在从未播放时显示 "Status paused"；`i` 非 toggle；搜索历史逐层压栈，
  `Escape` 一次只退一页（`1` 可直达 Home）。

**下一步**：修复 OQ24 后的复测批次里成组复现（同批顺带），命中即定级，未命中即关闭。

## OQ28 · jamendo 曲名 HTML 实体未解码（中）

**现象**：UP NEXT / 列表里出现 `Human Light — John Dada &amp; t…`——`&amp;` 原样上屏，用户看到
网页源码式的脏数据。

**证据**：usability batch `2026-09-22-recheck` r3-recheck 盲测命中（Jamendo Discover 第 19 首
附近）；同批 server 探针 `./lilt trending --source jamendo --type all --json` 输出中即含
`amp;`，`internal/jamendo` 无任何实体处理代码。

**已排除**：TUI 渲染层（JSON 出 server 前已是实体形式）——层级在 jamendo 元数据解码。

**下一步（可执行）**：在 jamendo 客户端把 title/artist/album 等文本字段做 HTML 实体解码
（stdlib `html.UnescapeString`），补 fixture 驱动的单测；顺带确认 audius/radio 元数据是否同病
（若同病则抽到公共解码点）。

## OQ29 · 复测轮低严重度候选集（低）

**现象**（各单轮，均待复现，batch `2026-09-22-recheck`）：

- r2-recheck：播放态 footer 在 110 宽下把 `f favorite`/`F filter`/`/ search` 挤出（加入 skip
  键后的溢出优先级取舍）；`v` 停止清空整队无预告（队列语义见
  [`../internals/providers.md`](../internals/providers.md) §5，属提示缺口）；buffering 静态文字、
  进度停在 0:00。
- r3-recheck：数字键切标签后焦点停在标签栏（列表无选中行，footer 仍是通用提示，需再按 Down）；
  建队等待期 UP NEXT 持续显示 "Nothing queued yet" 与 NOW PLAYING 的 working 相矛盾。

**下一步**：成组复现后逐条定级；footer 溢出顺序与 stop 预告先做设计确认再动手。
