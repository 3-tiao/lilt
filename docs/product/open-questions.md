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
| OQ1 | 专辑队列的一次性赋值被 MusicKit 拒绝，而歌单可以 | 高 | **已修待复测**（`playSongs` one-shot 主路径 + append 回退；E2E：专辑秒起播 12 曲、jump 5 精确落位） | 下一批次盲测复测通过即归档 |
| OQ17 | 填充后 re-pin 被拒，停在"队列就绪未播放" | 中 | 已复现（间歇 ~30%；resume 3/3 失败、重播 1/3 成功）；2026-09-22 起填充仅存在于 append 回退路径，暴露面大幅缩小 | 复现时用检查保存的 helper 时间线定位 |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证；`e queue next` 底栏修复后可发现性增强（2026-09-22） | 下一批加 counter-persona 轮（想连听某歌手多首，无引导） |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ14 | shuffle 生效后队列显示仍是提交顺序，界面像没随机 | 中 | 已修待确认（rail 标注 `· SHUFFLED` + wire 已能读到 shuffle） | usability 复测确认标注足够 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 已复现（同专辑随后又成功）；2026-09-22 解析梯级重排（catalog 权威优先），需对新梯级复测 | 直连 helper 连续 albumTracks，看是否为解析回退偶发失败 |
| OQ18 | 再按一次 `S` 关不掉 shuffle | 中 | 已修待确认（S 统一为开关 + 播放携带 form） | TUI 真按两次确认 |
| OQ19 | 切歌后 `Space` 暂停不稳定（真实会话） | 低 | 已复现（2026-09-21 隔离重放；helper 时间线定位到 play/pause 异步竞态） | 设计修复：play 响应等待 play() 完成或 helper 内串行化暂停 |
| OQ20 | 队列焦点内 `f` 的收藏目标与反馈歧义 | 低 | 部分复现（fake 出现瞬时 toast，主列表选中行常为 header） | 复现后决定：焦点内作用于队列 cursor 行并命名目标 |
| OQ26 | 30s 后自动插入 Recently Played 组时光标跳变、toast 目标错位 | 中 | 单轮（r1 fake）待复现 | 干净装置重放键序；确认选中行漂移规则 |
| OQ27 | 低严重度候选集；来源弹窗 `›` 标记 + `1-4` 直选两项已修并经复测轮盲测通过（已归档） | 低 | 其余各单轮待复现 | 成组复现后逐条定级，见条目内清单 |
| OQ32 | `queue.add` 按 activeTransport 而非条目来源路由 | 中 | **已修待复测**（复测轮 r4-recheck 盲测命中；单测钉住 queue_unavailable） | 下一批次盲测复测通过即归档 |
| OQ33 | 命令面板 Enter 执行原始文本而非高亮项（非命令时报 Unknown） | 中 | 复测轮 r4-recheck 稳定复现（两次） | 设计确认：Enter 是否应回退到高亮项 |
| OQ29 | 复测轮低严重度候选集（焦点/队列等待/footer 溢出） | 低 | 复测 r2/r3 各单轮 | 成组复现后逐条定级，见条目内清单 |
| OQ30 | 单曲专辑（1 曲 Single）无法播放 | 中 | **已修待复测**（根因修正：库内关系只反映本地内容；改为 catalog 权威排序 + 非空接受。E2E：Single 正常播放） | 下一批次盲测复测通过即归档 |
| OQ31 | TUI 能力快照陈旧：descriptor 停在 degraded，capability 键被错误拒绝 | 中 | 2026-09-22 实测（TUI 弹窗 degraded vs server sources.list ready/full/shuffle；S 被 TUI 拒绝） | root-cause：server 未重发 sources.changed 还是 TUI 丢弃 |

## OQ1 · 专辑队列的一次性赋值被 MusicKit 拒绝（高，已修待复测）

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

**分析（2026-09-22，编排者）**——先补上既有证据的两个弱点：

1. **样本 n=1**：全部失败形状都在同一张专辑上验证过，从未换第二张专辑。若另一张能一次性赋值成功，
   "专辑整体不可用"就坍缩为内容问题。
2. 原定实验 1（"把歌单 Song 塞进专辑形状的赋值"）无效：两种情形的调用形状**完全相同**（都是
   `Queue(entries, startingAt:)`），差别只在 entries 内容——该实验只会复现已知成功例。
3. helper 全程没有读过任何曲目的 `playParameters`（`grep` 证实），最大嫌疑从未被检查过。

**假设排序**：

- **H1（最可能）**：`Album.with([.tracks])` 关系加载的 Song **欠水化（`playParameters == nil`）**。
  `Queue.Entry(song)` 需要 play 信息才能 prepare；一次性赋值对整队列做 prepare，一条坏项即整体
  `Code=6`——与 0.3–1.5s 的快速失败吻合。歌单 entries 是流式构造，天然携带 play 信息。
- **H2**：所试专辑含**个别不可独立播放的曲目**（album-only、区域限制等），毒化整次赋值；测试歌单
  恰好全净。与 H1 不互斥（过滤可同时解决两者）。
- **H3**：`Entry(song)` 对专辑上下文的 Song 存在与水化无关的 MusicKit 缺陷——只有 re-fetch 后仍
  失败才成立。
- **H4（弱）**：storefront/账号上下文差异——同一 helper 会话内已基本排除。

**实验阶梯（按信息增益排序，全部用临时探针，不进主干）**：

- **E0**（5 分钟，先做）：换一张专辑重复一次性赋值——一张纯 catalog 未加库的 + 一张另一张库内的。
  任一成功 → H2 成立，问题从"专辑"缩到"内容"。
- **E1**（零音频）：dump 失败专辑曲目与成功歌单曲目的 `id` / `playParameters` 是否为 nil /
  storefront，直接检验 H1。
- **E2**：对专辑每条曲目做单条目一次性赋值（赋值后立刻停，音频秒级），定位毒化条目；命中则 E3
  二分。
- **E4**：每条曲目用 `MusicCatalogResourceRequest<Song>` 按 id 重新拉取后再一次性赋值——同时检验
  H1（欠水化）与 H3。
- **E5**：若 SDK 存在 storeID 形状的 Entry 构造，作为最后手段。

**决策树**：E0/E1 任一证实 H1/H2 → 专辑改走「解析 → re-fetch 或过滤 `playParameters != nil` →
一次性赋值」：专辑队列恢复可跳转、消灭 10–40s 节奏填充、消除 OQ17 在专辑上的暴露面，
`playSongs` 同受益；阶梯全败 → 维持现编排路径，结论降格进
[`limitations.md`](limitations.md)（Apple 平台限制），再评估 §7b 的"自有队列 + 有界预读"。

**下一步（可执行）**：按 E0→E1 顺序跑探针（临时 debug RPC 或 ad-hoc helper 构建），结果回填本条目。

**实验结果（2026-09-22，真实账号 + 签名 helper，临时 `debugQueueProbe` 探针，已还原不进主干）**：

- **E0 证伪原结论**：4 张专辑一次性赋值 + 真实播放全部成功——库内 A LA SALA（12 曲）、
  AngieAngieAngie（16 曲）、Angular Blues（9 曲）与 catalog Abbey Road 2019 Mix（17 曲），
  1.4–2.8s 起播。"一次性赋值对专辑整体不可用"是 **n=1 归纳错误**；2026-09-20 那张专辑的失败是
  其**内容特性**（个别不可播曲目毒化整次赋值的机制仍成立，原始专辑已不可考证）。
- **Jump 闭环**：一次性赋值的专辑队列 `queueJump` index 5 精确落到第 6 首（playing）——
  专辑队列可跳转实锤。
- **E1**：成功专辑曲目 `playParameters` 全部非 nil——欠水化假说对健康专辑不构成致病因。
- **顺带发现**：单曲专辑（1 首歌的 Single，如 "2step - Single"）**无法播放**——`albumSongs` 的
  `count > 1` 守卫直接 `invalid_reference`（新条目 OQ30）。

**修复方向（待实现）**：

- 方案 A：helper `play` 增加 `kind:"album"` 分支（内部解析 → one-shot → `startingAt`），server 在
  helper 失败时回退现有编排路径。
- 方案 B（推荐）：`playSongs` 从逐条 append 改为一次性赋值（与歌单同形状），MusicKit 拒绝时回退
  `startEngineQueueLocked`——一个机制同时修掉：专辑 10–40s 慢填充、专辑/playSongs 队列不可跳、
  OQ17 的"队列就绪未播放"窗口。

**关联**：[`limitations.md`](limitations.md) §7b、[`../internals/helper-rpc.md`](../internals/helper-rpc.md)。

**修复（2026-09-22，已落地，待复测）**：采用方案 B——`playSongs`（helper 一次性赋值，与歌单同
形状）成为专辑与 `playback.playSongs` 的**主路径**；MusicKit 拒绝整批（Code=6）时 server 回退到
原起播+节奏 append。E2E（隔离 server + 签名 helper + 真实账号）：A LA SALA 秒级起播 12 曲队列、
`queue jump 5` 精确落到 "Todavía Viva"（原先设计上不可跳）、单曲专辑正常播放。回归测试：
`TestAlbumPlayStartsOneShotQueue`/`FromHere`/`StartAt`/`FallsBackToPacedAppend`、
`TestPlaySongsStartsOneShotQueue`，4 个回退路径测试改为强制 `FailPlaySongs`。规范同步：helper-rpc.md
`playSongs` 行、commands.md 主路径描述、limitations.md §7b 收窄为回退路径限制。残余：OQ17 与
10–40s 填充只在回退路径存在；下一 usability 批次盲测复测通过即归档。

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
  player 停在无法起播的状态**（与原 pacing 探针观察到的 wedge 同源）。
- 现在的检查在命中时会保存 helper 时间线（`oq17-timeline.log`），下次复现即可看到 MusicKit 当时的
  `playbackStatus` / `currentEntry`。

**暴露面变化（2026-09-22）**：one-shot 主路径（OQ1 修复）无填充，`queueReady` 只可能出现在
MusicKit 拒绝整批、走 append 回退的罕见内容上；修复收益相应下降，但现象本身仍未解。

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

**可发现性变化（2026-09-22）**：搜索结果页底栏现在常驻 `e queue next · E append`（原 `e next`
歧义文案已修，OQ22 归档），用户不翻帮助就能看到连播入口——counter-persona 轮的证据环境已变。

**下一步**：下一批加一轮 counter-persona（想连听某歌手多首、不出现“一首/继续听”提示），
观察用户是否直接使用 `e`/`E`；若不用，再评估显式的“排入整节”动作。

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

**验证受阻（2026-09-22）**：真实 TUI 的 S 复测被新发现 OQ31 阻塞——TUI 的能力快照停在
`degraded`（server 实际 ready/full/shuffle ✓），`S` 被 TUI 侧以 "This source does not support
shuffle" 拒绝，开关行为到不了 helper。OQ31 修复后再做本条复测。

**关联**：OQ14、[`../client-api/commands.md`](../client-api/commands.md)、[`../ui/model.md`](../ui/model.md)。

## OQ15 · 资料库专辑详情偶发解析失败（中）

**现象**：打开资料库里的 `A LA SALA` 时底部报 `Apple Music album lookup failed`；同一次会话稍后
再打开同一张专辑又成功。

**证据**：`2026-09-20-form-fix-recheck` r3-recheck，日志中 `albumTracks` 两次返回
`invalidReference`（helper 的 `albumSongs` 在库内过滤与 catalog 标题搜索两条路径都没拿到曲目），
随后同一专辑的 `albumTracks` 又 `ok`。

**下一步**：直连 helper 连续调用 `albumTracks` 观察失败率，并在 helper 内为“库内过滤命中 0 首”
增加日志（哪条回退路径失败），再决定是加重试还是修解析。

**解析梯级变化（2026-09-22）**：OQ1/OQ30 修复把 `albumSongs` 重排为 catalog 标题搜索权威优先
（`.with([.tracks])`、库内标题作回退，理由：库内关系只反映本地内容）。本条的偶发失败发生在
旧梯级上，需在新梯级复测后再定级；若 catalog 搜索成为新的失败点，回退顺序值得再议。

**新梯级复测（2026-09-22，探针，已还原）**：A LA SALA `albumTracks` 连续 20 次 **20/20 成功**、
每次 12 曲（直连 helper，单连接）。旧梯级的偶发未在新梯级重现；条目保留观察，直到一次真实
批次复测（正常使用中再次命中即记录键序与时间线）。

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

**已排除**：非 TUI 能力快照问题（高-1 已修）；非队列限速（pacing 交错批次已排除）；非 OQ17 的“队列就绪
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

## OQ26 · 自动插入 Recently Played 组时光标跳变、toast 目标错位（中）

**现象**：播放满 30s 时 Home 自动出现 "Recently Played" 分组，光标跳到新组；此时按 `f`
意图取消先前收藏的 playlist，toast 却显示 "Unfavorited: fake track"（该曲从未被收藏），
且 playlist 的 ★ 与 Favorites 分组消失——操作对象与反馈都不符合用户预期。

**证据**：batch `2026-09-22-jamendo-tui` r1（fake），键序见 `round-1-keys.log`。

**已排除**：——（尚未在干净装置重放；fake 数据命名 "fake track" 与列表名不一致为装置噪声，
不影响本条结论。）

**下一步**：干净装置重放：播放任意曲满 30s 后观察光标归属与 `f` 的目标行；确定列表插入分组时
的选中行保持规则与收藏目标的绑定。

## OQ27 · 低严重度候选集（低）

**现象**（各单轮一次，batch `2026-09-22-jamendo-tui`）：

- r1：Recent/Discover 顶层 tab 里 `Escape` 无返回效果；`Escape` 返回后光标不保留；Account
  行 `Enter` 只弹 toast 无页面。
- r2：UP NEXT 窄面板同名曲目截断难区分；`e` 入队无明确 toast；仅 5 首也提示
  "large queues are added track by track"。
- r3：`v` 停止并清空队列后 Home "Continue Playing — 1/4" 仍引用已不存在的队列。
- r4：Track Info 在从未播放时显示 "Status paused"；`i` 非 toggle；搜索历史逐层压栈，
  `Escape` 一次只退一页（`1` 可直达 Home）。

**已归档（2026-09-22-recheck2 盲测通过）**：来源弹窗 `›` 选中标记（4 轮命中的可读性问题，
`TestSourceSwitcherShowsAvailabilityAndCapabilities`）与 `1-4` 数字直选
（`TestSourceSwitcherNumberKeyPicksDirectly`，PTY 探针确认 `2`→Audius 即时切换）——
结论落在 TUI 代码与测试。

**下一步**：其余各条成组复现后逐条定级，未命中即关闭。

## OQ29 · 复测轮低严重度候选集（低）

**现象**（各单轮，均待复现，batch `2026-09-22-recheck`）：

- r2-recheck：播放态 footer 在 110 宽下把 `f favorite`/`F filter`/`/ search` 挤出（加入 skip
  键后的溢出优先级取舍）；`v` 停止清空整队无预告（队列语义见
  [`../internals/providers.md`](../internals/providers.md) §5，属提示缺口）；buffering 静态文字、
  进度停在 0:00。
- r3-recheck：数字键切标签后焦点停在标签栏（列表无选中行，footer 仍是通用提示，需再按 Down）；
  建队等待期 UP NEXT 持续显示 "Nothing queued yet" 与 NOW PLAYING 的 working 相矛盾。

**下一步**：成组复现后逐条定级；footer 溢出顺序与 stop 预告先做设计确认再动手。

## OQ30 · 单曲专辑无法播放（中，已复现）

**现象**：播放只有 1 首歌的专辑（Single，如 "2step (feat. Lil Baby) - Single"）时，
`albumTracks`/专辑播放直接 `invalid_reference`。

**证据**：2026-09-22 OQ1 实验顺带发现——`albumSongs` 的三条解析路径全部带
`count > 1` 守卫（库内按 albumTitle、`Album.with([.tracks])`、标题搜索回退），1 曲专辑全被跳过，
落到 `invalidReference`。探针实测量：1 曲 Single resolve 只剩 `album-with-tracks` 形状可用。

**已排除**：授权/订阅（同会话多曲专辑正常）。

**下一步**：~~把三处 `count > 1` 放宽为 `count > 0`~~（该结论是错的，见下）；与 OQ1 的修复（方案 B）同批落地。

**根因修正与修复（2026-09-22，已修待复测）**：`count > 1` 守卫不是简单的噪声过滤，而是**承重的**：
它歪打正着地挡住了"库内结果只反映本地内容"的情况——用户库里只有某专辑 1 首歌时，
`MusicLibraryRequest` 按 albumTitle 与库内专辑的 `.with([.tracks])` 都只返回那 1 首（实测 A LA SALA：
本地 1 首的关系加载返回 1，而专辑真身是 12 曲）。单纯放宽为 `> 0` 的第一版修复让这 1 首本地歌
冒充了整张专辑（E2E 当场抓回，正是"修复前先写成可测断言"的价值）。真正的修法：**catalog 标题搜索
提为第一优先**（对 catalog 与库内专辑都是权威曲目表），`.with([.tracks])` 与库内标题作为离线/
搜索未命中的回退，接受条件统一为非空。E2E：A LA SALA 解析回 12 曲且 one-shot 起播；
"2step - Single" 正常播放（OQ30 主诉求）。待下一批次复测归档。

## OQ31 · TUI 能力快照陈旧：descriptor 变化不重发 sources.changed（中，根因已定位）

**现象**：全新隔离 server + 真实账号启动后数秒，TUI 里按 `S` 得到
"This source does not support shuffle"；同一时刻 server 的 `sources.list` 显示
apple-music `ready` 且 `shuffle/playback.full` 均声明可用。来源弹窗也停在
`Apple Music · degraded · preview, library`。再按一次 `S`（>10s 后）依旧拒绝。

**证据**（2026-09-22，probe 轮 oq18-tui + watch 探针 oq31）：

1. TUI 弹窗文本 vs 同刻 `lilt sources --json`（ready + shuffle available:true）矛盾；
   再按 `S`（+10s）依旧被拒。
2. **watch 探针决定性证据**：watch 客户端挂上后，触发 `sources.list`（resource client
   惰性挂载）→ CLI 同刻看到 `ready shuffle=True`，但 18 秒窗口内 watch 流
   **零条 `sources.changed`**。

**根因（已定位）**：descriptor 的 capability 随 **resource client 惰性挂载**而变化
（首次 `sources.list`/doctor 等调用触发 MusicKit token 流程），但 `sources.changed` 的
重发钩子只挂在 **engine 状态流的 authorization 翻转**上（`publishAppleAvailabilityLocked`
监听 engine 的 SubscribeState）。resource 侧的变化永远不触发重发 → TUI 快照停在启动时刻。

**已排除**：fake 模式因素（real 模式同样命中）；`S` 键的 gate 逻辑本身（它如实反映了快照）；
TUI 丢弃事件（watch 流根本没有该事件）。

**修复方向（需决策，二选一或组合）**：

- 方案 A：resource client 挂载/授权结算后也重发 `sources.changed`（resource 侧订阅
  authorization 变化，或挂载完成后主动 publish 一次）——事件驱动，语义最正。
- 方案 B：TUI 侧对 descriptor 做一次性刷新（收到任意 state.changed/首次交互后重拉
  `sources.list`）——改动小，但属于轮询式补偿。
- 方案 C：engine 惰性 attach 策略改为「首个 Apple 资源/播放调用即 attach」——改动最大，
  影响 resource role 与 playback role 的职责分离（helper-rpc.md §方法归属）。

**下一步**：按用户决策选方案实施；`S`/来源弹窗的复测在修复后进行（OQ18 同步解锁）。

## OQ32 · `queue.add` 按 activeTransport 而非条目来源路由（中，已修待复测）

**现象**：fake 轮里 jamendo 播放失败（fake 无 URL 传输，装置噪声）后按 `e`，状态行显示
"Playing next: You and Me"，UP NEXT 却持续 "Nothing queued yet"，Track Info 显示
Queue 0 entries——成功反馈与实际队列矛盾。

**证据**：batch `2026-09-22-recheck2` r4-recheck 盲测，`e`/`E` 各两次稳定复现。

**根因（已定位）**：`handleQueueAdd` 按 `s.activeTransport` 路由而非条目来源。播放被拒后
transport 过期/为空，jamendo 条目跌进 MusicKit engine 路径，fake engine 无条件接受 →
成功假象。真实账号下会以 MusicKit 解析失败 surfaced，但路由本身是错的。

**已修（2026-09-22）**：`handleQueueAdd` 先解析 ref，按条目来源路由——URL 来源（声明
PlaybackPreparer）走 `addURLQueueItem`（会话/来源校验 → `queue_unavailable`），其余走
engine 路径；radio 保留显式回答。回归测试
`TestQueueAddRoutesJamendoToTheURLQueuePath`。剩余动作：下一批次盲测复测通过即归档。

## OQ33 · 命令面板 Enter 执行原始文本而非高亮项（中，设计确认）

**现象**：`:` → 输入 `pl` → Enter：高亮第一项是 `:source apple-music`（过滤把 "apple" 含
"pl" 排前），Enter 却报 `Unknown command: :pl`——执行了原始输入而非高亮项；Tab 移到
`:play <ref>` 后 Enter 正确执行。两次稳定复现（batch `2026-09-22-recheck2` r4-recheck）。

**已排除**：Tab 选择路径（其行为正确）；过滤排序本身。

**未定**：Enter 的语义设计——原始输入 vs 高亮项，孰优先。

**下一步（设计确认后实施）**：若 Enter 回退到高亮项（仅当原始文本不是可执行命令时），
补回归测试；此为交互语义变更，先确认。
