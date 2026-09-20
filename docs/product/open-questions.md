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
| OQ4 | 队列填充 pacing 700ms 是否可降低 | 中 | 未做 | 受控探针测 300/400ms |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证 | 补一轮 counter-persona 走查 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ11 | 单曲队列播完后状态停在 `paused`，与用户暂停无法区分 | 中 | 已复现 | 探测 MusicKit 结束后能否区分，再定 `ended`/`stopped` 语义 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ14 | shuffle 生效后队列显示仍是提交顺序，界面像没随机 | 中 | 已复现 | 决定 rail 是否标注“已随机”或按播放顺序显示 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 已复现（同专辑随后又成功） | 直连 helper 连续 albumTracks，看是否为解析回退偶发失败 |

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

**下一步**：受控探针（直连 helper）测 300ms / 400ms 间隔在 12 首专辑上是否稳定；能降就降，
不能降就把实测结论写回注释。

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

**现象**：有限队列的最后一首自然播完后，`PlaybackState.status` 仍是 `paused`，Now Playing 区域
因此显示“Paused”。它与“用户主动暂停”完全同形，客户端无法表达“已播完”。这不是崩溃，但语义不准确。

**证据**（2026-09-20，`just manual-test` 真实 Apple Music 会话）：单曲队列
`apple-music:song:471749203` 播完后立即 `./lilt status --json`：

```json
{"track": {"id": "am:471749203"}, "position": 0, "duration": 322.467,
 "status": "paused", "repeatMode": "off", "isLive": false, "mode": "full",
 "playbackError": null}
```

**未定**：MusicKit 的 `ApplicationMusicPlayer` 在有限队列耗尽时是否提供可区分的状态
（`playbackStatus`、queue index、`currentEntry` 是否为空），因此还不知道能否在不猜测的前提下
提供 `ended`。

**下一步（可执行）**：

1. 直连 helper 探针：单曲队列播完后读 `state` 与 MusicKit 原始属性，记录与“用户暂停”的差异。
2. 若能区分，在 `PlaybackState` 增加明确语义（例如 `status:"ended"` 或 `queueExhausted:true`），
   同步 [`../client-api/models.md`](../client-api/models.md) 与 TUI Now Playing 显示。
3. 若不能区分，定产品行为二选一：播完自动进入 `stopped`（播放源保留、可重播），或者保留
   `paused` 并把“平台不提供结束态”写进 [`limitations.md`](limitations.md)。
4. 补回归：有限队列末首结束后的状态投影（含 TUI 显示文案）。

**关联**：`repeatMode:"all"` 与 live stream 不受影响（前者会回到首首，后者无队列）。

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

## OQ14 · shuffle 生效后队列显示仍是提交顺序（中）

**现象**：在歌单里按 `S` 后提示 `Shuffling: …`，但 `UP NEXT` 的顺序与歌单原顺序逐首一致，
用户据此判断“随机没有生效”。实际播放顺序已随机，只是 wire 状态按 canonical 提交顺序投影。

**证据**：`2026-09-20-form-and-playlist-fixes` r1（键序 `Enter → S`），日志中 `setShuffle=true`
成功；状态投影按 `docs/internals/helper-rpc.md` 的 canonical 规则输出。

**未定**：rail 是否应标注“已随机（显示为提交顺序）”，或改为按播放顺序显示（会与 queue 编辑的
index 语义冲突，见 limitations §7）。

**下一步**：先定展示语义（标注 vs 重排），若只是标注则 Title 上加一个 `shuffled` 状态即可。

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
