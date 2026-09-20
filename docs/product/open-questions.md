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

规则：

- **只写未解决项**。修好后从这里删除，并把结论落到上表对应的权威文档；本文件不留历史。
- 每条必须写清：现象、证据（批次名/命令/日志）、已排除的假设、下一步（可执行）。
- 严重度按「发生频率 × 影响 × 持久性」：核心路径反复失败为高；能绕过但需猜测为中；文案/留白为低。
- 真实会话证据来自 [`../../.agents/skills/usability-test/SKILL.md`](../../.agents/skills/usability-test/SKILL.md)
  的批次；运行产物（`/tmp/lilt-usability/*`）不入库，结论只保留在本文件与上表的权威文档。

## 摘要

| # | 问题 | 严重度 | 状态 | 下一步 |
|---|---|---|---|---|
| OQ1 | 专辑队列的一次性赋值被 MusicKit 拒绝，而歌单可以 | 高 | 已复现，原因未定 | 对比 `Playlist.entries` 与 `Album.with([.tracks])` 的曲目对象 |
| OQ2 | append 构建的 Apple 队列无法在 Up Next 跳转 | 高 | 已接受（[`limitations.md`](limitations.md) §7b） | 依赖 OQ1，或改用有界预读传输 |
| OQ3 | 大队列填充期间没有进度、没有部分失败语义 | 中 | 未做 | 先定契约（提前返回 vs 发布中间状态） |
| OQ4 | 队列填充 pacing 700ms 是否可降低 | 中 | 未做 | 受控探针测 300/400ms |
| OQ5 | A1（搜索结果 Enter 只播该行）的证据强度 | 中 | 部分验证 | 补一轮 counter-persona 走查 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ7 | 帮助层的 Esc 在快速连发时被终端吞 | 低 | 已缓解 | 仅在复现时再处理 |
| OQ8 | 走查未覆盖的格子（30×8、Radio/Audius 广度、主题弹层等） | 低 | 未覆盖 | 下一批分配格子 |

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
3. 若仍无解，走 OQ2 的第二条路（有界预读传输）或维持现状。

**关联**：[`limitations.md`](limitations.md) §7b、[`../internals/helper-rpc.md`](../internals/helper-rpc.md)。

## OQ2 · append 构建的 Apple 队列无法在 Up Next 跳转（高，已接受）

**现象**：专辑 / `playback.playSongs` 播放中，在 Up Next 选行按 Enter 得到
`could not jump to row N of M: … Code=6 … Playback continues with the current track.`；跳转不发生，
播放不被中断（早期版本会直接中断播放并报 `invalid_reference`，已修）。

**证据与取舍**：见 [`limitations.md`](limitations.md) §7b（含 Code=6、`skipToNextEntry` 落点偏移
“目标第 4 行、实际第 6 行”、歌单对照）。

**下一步**：优先 OQ1；OQ1 无解时评估“我们拥有队列 + provider 只播单条 + 有界预读”的传输模型
（代价：Apple 端自己接管推进/结束检测，削弱无缝衔接，当前明确不做迁移）。

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

## OQ7 · 帮助层的 Esc 在快速连发时被终端吞（低，已缓解）

**现象**：`?` 打开帮助后**快速**连发 `Escape` 与下一个字符时，帮助不关闭且后续按键无效
（Esc 与字符被终端解析成 alt 组合序列，属装置层输入歧义）。单发 `Escape` 正常关闭。

**已缓解**：帮助状态行写 `Esc/? close`，用户有可见的替代键；帮助只由 `Esc`/`q`/`?` 关闭，
其他键保持惰性（不会吞掉用户想执行的键）。

**下一步**：仅在真实会话再次复现时处理；不在应用层为终端输入歧义加特例。

## OQ8 · 走查未覆盖的格子（低）

未覆盖：30×8 极小窗口、Radio 与 Audius 的广度轮、主题弹层、非拉丁文本、慢网络/断网、
shuffle+repeat 下的队列编辑。

**下一步**：下一批按 [`../../.agents/skills/usability-test/references/exploratory-round.md`](../../.agents/skills/usability-test/references/exploratory-round.md)
的覆盖地图分配 3–6 个格子。

## 已定的决策（记录以免反复讨论）

- **搜索结果 Enter = 只播该行**（`pageClass: aggregate`）；连播改用逐行 `e`/`E` 或 CLI/agent 的
  `playSongs`。见 [`../ui/model.md`](../ui/model.md) §6。
- **专辑是容器**：Enter = 从该行播到专辑末，`p` 整张，`S` 洗牌；专辑详情不重复专辑行。
- **专辑播放不记录 recent container**：`recentContainers` 只保存歌单（[`../internals/state.md`](../internals/state.md)）。
- **队列所有权暂不迁移**：Apple 继续由 MusicKit 拥有队列（helper 维护 canonical 影子列表），
  Audius/Radio 由 server 拥有；不改为“provider 只播单条”，因为会削弱 Apple 的无缝衔接。
