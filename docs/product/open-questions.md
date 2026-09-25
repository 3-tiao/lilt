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
| OQ17 | 填充后 re-pin 被拒，停在"队列就绪未播放" | 中 | 已复现（间歇 ~30%；resume 3/3 失败、重播 1/3 成功）；2026-09-22 起填充仅存在于 append 回退路径，暴露面大幅缩小 | 复现时用检查保存的 helper 时间线定位 |
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未做 | 设计确认后再改 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ15 | 资料库专辑详情偶发 `Apple Music album lookup failed` | 中 | 观察项（新梯级 20/20）；2026-09-25 隔离 r1 未覆盖 | 真机批直连 helper 连续 albumTracks，命中即记录时间线 |
| OQ27 | 低严重度候选集；来源弹窗 `›` 标记 + `1-4` 直选两项已修并经复测轮盲测通过（已归档） | 低 | 2026-09-25 隔离 r1：Account 行 Enter 已打开 Account 弹层、Track Info 空闲显示 `stopped`（两条**已改善**）；`i` 非 toggle 与 Recent 顶层 Esc 不返回**仍复现** | 只剩两条待修，见条目清单 |
| OQ31 | TUI 能力快照陈旧：descriptor 变化不重发 sources.changed | 中 | **已修待复测**（根因：去重门只看授权字符串；签名改为 status+accountStatus。E2E watch 流确认重发） | 下一批次盲测复测通过即归档 |
| OQ34 | warm-up 完成发布与签名去重门不一致 | 低 | 观察项 | 统一签名去重前先保住“纠正过早读取者”承诺 |
| OQ35 | 同名同专辑搜索行不可区分 + 版本关键词被截断 | 中 | 待查数据/待批 | 取真实搜索 JSON 后决定去重或补时长 |
| OQ36 | browser 引擎 `mode=full` 谎报窗口与 storefront 覆盖 | 中 | 已修待真机确认 | 补 90 秒媒体与目录时长不符的真实负路径 |

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

## OQ15 · 资料库专辑详情偶发解析失败（中）

**现象**：打开资料库里的 `A LA SALA` 时底部报 `Apple Music album lookup failed`；同一次会话稍后
再打开同一张专辑又成功。

**证据**：`2026-09-20-form-fix-recheck` r3-recheck，日志中 `albumTracks` 两次返回
`invalidReference`（helper 的 `albumSongs` 在库内过滤与 catalog 标题搜索两条路径都没拿到曲目），
随后同一专辑的 `albumTracks` 又 `ok`。

**下一步**：直连 helper 连续调用 `albumTracks` 观察失败率，并在 helper 内为“库内过滤命中 0 首”
增加日志（哪条回退路径失败），再决定是加重试还是修解析。

**解析梯级变化（2026-09-22）**：2026-09-22 的修复把 `albumSongs` 重排为 catalog 标题搜索权威优先
（`.with([.tracks])`、库内标题作回退，理由：库内关系只反映本地内容）。本条的偶发失败发生在
旧梯级上，需在新梯级复测后再定级；若 catalog 搜索成为新的失败点，回退顺序值得再议。

**新梯级复测（2026-09-22，探针，已还原）**：A LA SALA `albumTracks` 连续 20 次 **20/20 成功**、
每次 12 曲（直连 helper，单连接）。旧梯级的偶发未在新梯级重现；条目保留观察，直到一次真实
批次复测（正常使用中再次命中即记录键序与时间线）。

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

**复测（2026-09-25 隔离 r1，fake）**：

- **已改善（关闭）**：Account 行 `Enter` 现在打开 `Account` 弹层（列出各来源与授权状态），不再是
  只弹 toast；Track Info 空闲时显示 `Status stopped`（旧报告为 paused）。
- **仍复现**：`i` 不是 toggle（再按无反应，需 `Esc`）；Recent 等顶层 surface 上 `Esc` 无返回效果。

**下一步**：只剩 `i` toggle 与顶层 `Esc` 两条待修；其余关闭。

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

**残余**：`s` 弹窗对「当前源」仍无文字标记（低，OQ27 未修项）。
**下一步**：下一批次盲测复测通过即归档（2026-09-25 真机 real-b 已确认 Apple Music 快照为 ready 且
`S` 正常，但未触发 descriptor 变化，重发路径仍待复测）。

## OQ34 · warm-up 完成发布与签名去重模式不一致（低，观察项）

**现象**：`warmUpAuthProviders` 在 WarmUp 完成后**无条件**发布
`authorization.changed` + `sources.changed`（`internal/server/server.go`
`warmUpAuthProviders`，注释声明目的是纠正订阅过早的客户端）；而
`publishAppleAvailabilityLocked` 与 `AvailabilitySignature` 轮询都按签名去重。
两条路径风格不一致。实测中 warm-up 发布是 A-04 Widevine 探测测试里"快速 settled
事件"的来源：warm-up 完成时 descriptor 还没做过探测，会先发一条 pre-probe
verdict（如 full 可用），~2s 后签名轮询再发一条降级 verdict——客户端在启动窗口
内看到 capabilities 抖动一次。

**为什么还没修**：无条件发布是注释声明过的刻意行为（correct too-early readers），
不是事故；若改为签名去重需要为 authorization.changed 建立与
`providerSignatures` 同形的"最后发布"种子，且 flow 事件携带 flowID（同状态新
flowID 必须发），签名必须包含 flowID，否则 begin 会丢事件。改动收益（去掉启动
窗口内一条冗余事件）小于回归风险。

**候选方向**：若要统一，先给 `publishAuthorizationChangeLocked` 建签名
（授权状态 + flowID），warm-up 与 flow terminal 共用同一去重门；或让 warm-up
在 WarmUp 返回后先读 provider 描述符再决定是否发布。两者都要保住"纠正过早读取
者"的注释承诺，并用现有 watch 测试锚定。

**发现于**：2026-09-23 Apple Music 修正批次（A-04 Widevine 探测接入时调试观察到）。

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
