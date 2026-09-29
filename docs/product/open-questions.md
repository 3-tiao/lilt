# 待决问题台账

本文件记录**尚未解决**的工程问题：现象、已有证据、已试过什么、下一步。它是接手入口，
不是结论仓库。

分工：

| 内容 | 归属 |
|---|---|
| 未解决、需要继续查或需要决策 | 本文件 |
| 已接受的限制（不再尝试绕过） | [`limitations.md`](limitations.md) |
| 已定的产品/接口契约 | 对应 `docs/` 设计文档（如 [`../ui/model.md`](../ui/model.md)、[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)） |
| 已排期/未排期的产品范围 | [`roadmap.md`](roadmap.md) |

生命周期（三个状态，本文件只留前两个）：

1. **未修** —— 条目在，下一步写清。
2. **已修待复测** —— 代码已改但验证尚未完成。用户体验类复用同一人设与尺寸做 usability
   任务回归（报告名 `round-N-recheck.md`）；装置/能力类可用 hermetic 契约测试加固定构建的
   one-off fake PTY 探针，明确证据形态与未覆盖范围。
3. **已归档** —— 对应验证通过后从这里**删除**，结论落到上表的权威文档（代码 + 回归测试 + 对应规范）。
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
| OQ17 | 填充后 re-pin 被拒，停在"队列就绪未播放" | 中 | 已复现（间歇 ~30%；resume 3/3 失败、重播 1/3 成功）；2026-09-22 起填充仅存在于 append 回退路径，暴露面大幅缩小 | 复现时用检查保存的 helper 时间线定位 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未修；用户已选择短时 Undo | 定义过期与队列变化边界后实现、复测 |
| OQ15 | 资料库专辑详情偶发 lookup failed | 中 | **已修待复测**（旧解析梯级失败；新梯级 helper 探针 20/20，尚无同任务盲轮） | 同账号隔离轮从资料库打开该专辑，确认曲目可见 |
| OQ27 | 短队列进度提示称 large | 低 | 五首队列曾观察到；固定文案仍在代码，未按同场景复测 | 用五首队列确认后决定是否去掉 large 限定 |
| OQ31 | TUI 能力快照陈旧：descriptor 变化不重发 sources.changed | 中 | **已修待复测**（签名改为 status+accountStatus，E2E watch 确认重发；近期 real 轮只见启动后 ready，未覆盖运行中变化） | 隔离真实轮捕获 degraded→ready 的 TUI 快照变化 |
| OQ35 | 同名同专辑搜索行不可区分 + 版本关键词被截断 | 中 | 待查数据/待批 | 取真实搜索 JSON 后决定去重或补时长 |
| OQ36 | browser 引擎 `mode=full` 谎报窗口与 storefront 覆盖 | 中 | 已修待真机确认 | 补 90 秒媒体与目录时长不符的真实负路径 |
| OQ41 | 低严重度界面候选集 | 低 | 剩余 footer 截断、Radio 授权串场、长列表提示三项 | 按已裁决修法实现后复测；紧凑窗不抢正常尺寸优先级 |

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

**暴露面变化（2026-09-22）**：one-shot 主路径（2026-09-22 起）无填充，`queueReady` 只可能出现在
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
[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)。

## OQ6 · Up Next 删除待排项没有 Undo（低）

**现象**：`0` → 选中行 → `x` 立即删除，只有 `Removed: …` 提示，没有撤销入口。

**决定与下一步**：用户已选择短时 Undo。先定义有效期、连续删除和队列切换时撤销什么，
再实现并用队列单测及同任务 PTY 验证；未实现前不归档。

## OQ15 · 资料库专辑详情偶发解析失败（中，已修待复测）

**现象与证据**：`2026-09-20-form-fix-recheck` r3-recheck 打开资料库的 `A LA SALA` 时，
`albumTracks` 两次返回 `invalidReference`，同一会话稍后又成功。该故障发生在旧解析梯级；
2026-09-22 改为 catalog 标题搜索优先、库内标题回退后，直连 helper 对同一专辑连续调用
20 次均得到 12 首曲目。近期任务轮未走资料库专辑详情。

**已排除**：不能把旧梯级的失败当成新梯级仍失败；也不能把 20/20 helper 成功当作已完成
用户任务的盲复测，更不能用 2026-09-29 的歌单起播轮代替专辑详情轮。

**下一步**：在获批隔离真机窗口复用打开该资料库专辑的人设与尺寸，保留键序和屏幕结果，
按 `round-N-recheck.md` 报告；通过后归档。若再次失败，再抓 helper 时间线定位当前回退路径。

## OQ27 · 短队列进度提示称 large（低）

**现象**：短队列填充也可能提示 `large queues are added track by track`，让人误读为很长的队列。

**证据**：`2026-09-22-jamendo-tui` r2 的五首队列观察；当前 `internal/tui/views.go`
仍返回该固定文案。近期静音轮没覆盖填充中短队列；不能把“没撞到”当作修复。

**已排除**：Playback Info 的 `i` 非 toggle 已由用户决定维持，规范与测试约束 `Esc/?` 关闭；
Recent 顶层 `Esc` no-op 也已定并修正 Help 文案。这两项不再属于未决问题。

**下一步**：用五首队列复现进度提示，若仍出现就去掉与队列长度不符的 `large queues` 限定。

## OQ31 · TUI 能力快照陈旧：descriptor 变化不重发 sources.changed（中，已修待复测）

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

**根因（已定位，双层）**：① helper 的握手分两步 settle——MusicAuthorization 先翻
`authorized`，订阅读取（1-3s）再填 `accountStatus`；② `publishAppleAvailabilityLocked`
的去重门只比较授权字符串 `status.Status`，第二步翻转时字符串未变 → `sources.changed`
永不重发 → TUI 快照停在 degraded。watch 探针零事件与此吻合。

**已修（2026-09-22）**：去重门改为完整签名 `status|accountStatus`（两步 settle 各重发一次，
重复快照不重发）；回归测试 `TestAppleAuthSettleStepsRepublishSourcesChanged` 经真实 watch
连接断言；E2E watch 探针确认重发恢复（20 秒 3 条 `sources.changed`）。

**下一步**：2026-09-25 real-b 及 2026-09-29 独立 real 轮只确认 Apple Music 已是 ready，
未捕获同轮从 degraded 到 ready 的 descriptor 变化；在隔离真机轮观察该变化时 TUI 是否更新，
通过后再归档。不能把 `sources.list` 初态 ready 当作 watch/TUI 重发的证据。

## OQ35 · 同名同专辑搜索行仍不可区分 + 版本关键词被截断（中，待查数据/待批）

**现象**（usability probe 2026-09-23-apple-browser-preview 及 recheck1）：apple-music
browser 引擎搜索 "Blinding Lights" 时，secondary metadata 链加入专辑（本批修复）后，
Remix/KIDZ BOP 等**不同版本**已可辨；但仍有两行 "Blinding Lights — The Weeknd ·
After Hours" 逐字相同（无时长/年份维度），且列表被右侧面板压到 ~60 列，专辑名尾部
截断恰好吞掉 "Deluxe Version"/"Single" 等版本关键词。

**已排除假设**：不是 wire/投影丢字段（`api.Item.Album` 全链路有测试锚定）；不是 TUI
不显示（行渲染测试锚定）。

**待查**：逐字相同的行是不同 catalog id（地区/榜单变体——去重需产品决策，因为它们
可能是不同可播放资产）还是相同 id（可安全去重）。需要真实搜索 JSON 样本
（LILT_APPLE_E2E 或真机抓取）。

**候选方向**：a) 元数据完全相同时去重；b) 行内补时长/年份；c) 列表全宽切换或中位
省略避免吞版本词。均待数据结论后再定。

**发现于**：2026-09-23 Apple Music browser 引擎 preview 可用性走查（r1 + recheck1）。

## OQ36 · browser 引擎 mode=full 谎报窗口与 storefront 覆盖（已修待真机确认）

**现象（原版已修）**：国区 Apple Music 订阅账号在 browser 引擎下播放美区目录只有 90 秒 preview，
且公开 mode 报 `full`（authorized）而实际媒体 90 秒——UI 说谎。真机证据：`auth status` =
authorized，搜索 URL = `music.apple.com/us/...`，播放 `mode:full duration:90`（2026-09-23 用户实放）。

**已修部分**（机制详见 [`../internals/playback/apple-web-engine.md`](../internals/playback/apple-web-engine.md)
「storefront 语义」）：根因是 `mk.storefrontId` 跟随页面 URL 区（新 profile 默认 us），登录后不自动
切换；MusicKit 全曲播放权按「账号订阅区 × 曲目目录区」裁决。引擎现在在授权 settled 后把页面带到
账号区（evaluate `/v1/me/storefront` 取 `r.data.data[0].id`，与页面区不一致时导航到
`https://music.apple.com/{账号区}/listen-now` 并重等 MusicKit 就绪；一次启动上限 2 次；未登录或
me 调用失败不动页面；对齐失败照常继续会话，播放退化为 preview）。catalog 与播放权因此对齐，
opt-in 真机 E2E 断言「页面已跟随账号区 + 媒体时长等于目录时长」。

**本轮修复（待真机确认）**：已登录只进入 `mode:unverified`，起播后以该项目录时长对比实际媒体
时长；90 秒媒体对 204 秒目录返回 `preview`，全长返回 `full`，目录时长缺失则保持 `unverified`。
有 URLQueueTransport 单测与假 CDP → playrouter → server 组合测试，覆盖两种媒体时长和起播前的
`unverified`；TUI 事实行也标注未确认。2026-09-24 隔离真实 Chrome + 国区账号的全曲路径已验证：
真实浏览器 E2E（目录时长匹配、进度推进、暂停）、私有 server 的 CLI `play` 初态为
`buffering/unverified`，约 7 秒后为 `playing/full duration:204`，随后进度推进。**90 秒媒体与
目录时长不符的真实负路径仍未复测**（未为测试强改账号 storefront/profile）；不能只凭全曲
路径归档。下一步在不改共享账号地区设置的独占窗口取得可稳定复现的真实试听样本，确认公开
mode 不为 full，再按本台账规则归档。

**独立候选（不阻挡本条关闭）**：显式 `LILT_APPLE_STOREFRONT` 覆盖用于调试外区目录，仍需产品决定。

**发现于**：2026-09-23 macOS browser 模式真机验收（国区订阅账号）。

## OQ41 · 跨批低严重度界面候选集（低，未修）

除注明外均为单轮发现（待复现），不能用近期任务轮没有撞到来归档：

- `2026-09-28-rounds` r3#2、r5#4（两轮命中）及 `2026-09-29-fake-recheck` r3：80×18 播放中 `v stop`
  先于次要提示被截；Audius footer 的全局键
  `s source · : commands · 1-9 view` 被截。已裁决：播放中保留 `v stop`，`s source`/`: commands`/
  `1-9 view` 等低优先级全局提示允许被截。
- `2026-09-28-rounds` r5#6：Radio 源下 Playback Info 显示 `Auth denied`（apple 授权语义串场）。
- 2026-09-29 `three-rounds` r2 的 Genre 与 `2026-09-29-oq42-ui-recheck` r3 的 Country
  均在 80×18 长列表下看不到 Filter/操作提示；`internal/tui/views.go` 把提示放在候选尾部，
  再按选中项裁切。属紧凑窗问题，**低于合理尺寸的审美与任务问题**；若要修，先用固定长列表
  证明提示可见且不会挤掉核心操作，再做同尺寸 PTY。

**已排除**：`2026-09-29-oq42-ui-recheck` r3 的 Browse `S` 实际出现短暂
`Sorted by Recommended — 6/97 measured`，推翻早先“完全无反馈”的判断；不再作为待修项。
`three-rounds` r2 成功搜台、筛选并收藏；目录数量与可播放性不是 fake 的确定性证据。
首次搜索输入看到的 `o` 没进完整键序且随输入消失，不作为独立问题。
帮助滚动范围（`2026-09-28-rounds` r3#5）已改为按条目报告；`i` 弹层已改名 **Playback Info**
并固定为播放诊断用途；直播暂停 ICY 标题继续更新已接受为 limitation（见
[`limitations.md`](limitations.md) §11）；Browse 标题已带
字段名（`Text=`/`Genre=` 等）。这些不再是未决项。

**下一步**：剩余三项按任务影响排序——紧凑窗 footer 截断、Radio 源下 Playback Info 的授权串场、
长列表提示；修法与取舍已裁决（`v stop` 保留、低优先级全局键允许被截；授权只显示当前来源、
Radio 不渲染 Auth 行；长列表固定预留状态行），实现后复测。紧凑窗视觉问题不抢正常尺寸优先级。
