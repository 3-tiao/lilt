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
   报告名 `round-N-recheck.md`）；此时状态写成“已修待复测”。
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
| OQ6 | Up Next 删除待排项没有 Undo | 低 | 未修；用户已选择短时 Undo | 定义过期与队列变化边界后实现、复测 |
| OQ12 | 播放时主面板仍是浏览列表，用户觉得“体验一般” | 低 | 需求待澄清 | 先让用户把“不好”具体化，再决定是否动布局 |
| OQ15 | 资料库专辑详情偶发 lookup failed | 中 | **已修待复测**（旧解析梯级失败；新梯级 helper 探针 20/20，尚无同任务盲轮） | 同账号隔离轮从资料库打开该专辑，确认曲目可见 |
| OQ27 | 短队列进度提示称 large | 低 | 五首队列曾观察到；固定文案仍在代码，未按同场景复测 | 用五首队列确认后决定是否去掉 large 限定 |
| OQ31 | TUI 能力快照陈旧：descriptor 变化不重发 sources.changed | 中 | **已修待复测**（签名改为 status+accountStatus，E2E watch 确认重发；近期 real 轮只见启动后 ready，未覆盖运行中变化） | 隔离真实轮捕获 degraded→ready 的 TUI 快照变化 |
| OQ34 | warm-up 完成发布与签名去重门不一致 | 低 | 未修；用户已选择统一去重 | 先用 watch 测试锁定“纠正过早读取者”与新 flow 事件 |
| OQ35 | 同名同专辑搜索行不可区分 + 版本关键词被截断 | 中 | 待查数据/待批 | 取真实搜索 JSON 后决定去重或补时长 |
| OQ36 | browser 引擎 `mode=full` 谎报窗口与 storefront 覆盖 | 中 | 已修待真机确认 | 补 90 秒媒体与目录时长不符的真实负路径 |
| OQ38 | fake 引擎不能播 URL，但 audius/jamendo descriptor 声明 `playback.full+queue` | 中 | 未修；用户已选择 FakeEngine 实现 URL 假播放 | 补纯假播放/队列测试并实现，不访问真实媒体 |
| OQ40 | Account 对不支持 disconnect 的来源仍提供 `d` | 中 | 未修；用户已选 `authorization.list` 增加 per-source 支持字段 | 定义 wire 语义、按字段隐藏 `d` 并复测 |
| OQ41 | 低严重度界面候选集 | 低 | 剩余紧凑窗提示、播放信息等单轮候选，详见条目 | 按任务影响复测；紧凑窗视觉问题不抢正常尺寸优先级 |

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

**已排除**：Track Info 的 `i` 非 toggle 已由用户决定维持，规范与测试约束 `Esc/?` 关闭；
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

## OQ34 · warm-up 完成发布与签名去重模式不一致（低，未修）

**现象**：`warmUpAuthProviders` 在 WarmUp 完成后**无条件**发布
`authorization.changed` + `sources.changed`（`internal/server/server.go`
`warmUpAuthProviders`，注释声明目的是纠正订阅过早的客户端）；而
`publishAppleAvailabilityLocked` 与 `AvailabilitySignature` 轮询都按签名去重。
两条路径风格不一致。实测中 warm-up 发布是 A-04 Widevine 探测测试里"快速 settled
事件"的来源：warm-up 完成时 descriptor 还没做过探测，会先发一条 pre-probe
verdict（如 full 可用），~2s 后签名轮询再发一条降级 verdict——客户端在启动窗口
内看到 capabilities 抖动一次。

**为什么还没修**：无条件发布是注释声明过的刻意行为（correct too-early readers），
不是事故。授权 flow 在**同一个 flowID** 下可先发 pending 再发 terminal；即使授权
descriptor 未变化，也必须发状态迁移。只按授权状态和 flowID 去重会丢掉 terminal；
同时新 flowID 即使授权状态相同也不能被吞。减少冗余事件的收益不能压过这两项正确性。

**决定与下一步**：用户已选择统一去重。先为 `authorization.changed` 和
`sources.changed` 各自定义基于**实际规范化事件内容**（排除 sequence）的最后发布签名，
让 warm-up 与 flow 发布走同一套判定；保留首次快照纠正过早订阅者的行为。
watch 测试必须分别覆盖同一 flowID 的 pending→terminal、同状态的新 flowID、
warm-up 首次纠正及重复快照不重发；通过后才改发布门，不能只以事件数量减少验收。

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

## OQ38 · fake 引擎不能播 URL，但 audius/jamendo descriptor 声明 full+queue（中，未修）

**现象**：fake 会话里 audius/jamendo 的一切播放与入队都失败：`playback.play` 返回
`source_unavailable: direct URL playback is unavailable`；`queue.add` 返回
`queue_unavailable: there is no active URL queue`。Radio（stream transport）正常。
fresh 会话即失败，与是否先播过 Radio 无关——r4 参与者的"Radio 会话污染"假设已被对照推翻。

**证据**：batch `2026-09-28-rounds` r4#1、r5#7；编排者对照复现（正确 round socket
`/tmp/lilt-round-rep2/session.sock`）：`play audius:song:1` 直接失败，`play <radio url>` 成功，
radio 后 `play audius:song:2` 仍失败。CLI `sources --json` 显示 audius 声明
`playback.full`+`queue`。

**根因**：audius/jamendo 走 `URLQueueTransport`，其 `URLPlaybackDriver` 需要 AudioEngine 实现；
`FakeEngine` 未实现，`urlPlaybackAvailable()` 为 false。descriptor 由 provider 声明，
不知道引擎能力，于是 capability 与引擎不一致。

**决定与下一步**：用户已选择让 `FakeEngine` 实现 `URLPlaybackDriver`（按 session 假播 URL、
支持 pause/resume/stop 与 finite queue）；先用 hermetic 测试锁定 URL 不访问真实媒体、不出声、
入队/跳转/停止后的状态，再实现并做 fake PTY 走查。`capability` 仍是唯一真值；真实
Audius/Jamendo 传输不由 fake 通过来证明。

## OQ40 · Account 对不支持 disconnect 的来源仍提供 `d`（中，未修）

**现象**：apple-music 的 disconnect 返回 `unsupported_command`（macOS 不允许客户端撤销授权），
但 Account 弹层对每一行都提供 `d`，用户要经过二次确认才看到失败；失败信息曾含机器 code 前缀且被
fit 截断（r5#2）。

**已修（本批）**：失败提示改为稳定 message（去 `code:` 前缀）并按弹层宽度换行；
`TestDisconnectFailureTextUsesTheHumanMessage`、`TestDisconnectNoticeWrapsLongReason`。

**决定与下一步**：用户选择在 `authorization.list` 暴露 per-source disconnect 支持字段。
先定义 wire 字段和缺省语义（当前 API v0.1 可直接改，不保留兼容分支），让 Account 弹层
按字段显示操作；为支持与不支持的来源各补 hermetic 测试，并做一次 fake PTY 复测。
契约权威位置是 [`../client-api/`](../client-api/README.md)。

## OQ41 · 跨批低严重度界面候选集（低，未修）

除注明外均为单轮发现（待复现），不能用近期任务轮没有撞到来归档：

- `2026-09-28-rounds` r3#5：帮助滚动范围 `1-14/30 → 2-14/30` 语义难读；按条目滚动是既定规则，仅文案候选。
- 同批 r3#2、r5#4（两轮命中）及 `2026-09-29-fake-recheck` r3：80×18 播放中 `v stop`
  先于次要提示被截；Audius footer 的全局键
  `s source · : commands · 1-9 view` 被截。属"后面先截断"预算的排序取舍，需设计确认。
- `2026-09-28-rounds` r5#6：Radio 源下 Track Info 显示 `Auth denied`（apple 授权语义串场）。
- 同批 r4#4：直播暂停中曲目标题仍随 ICY 更新；是否冻结标题属产品取舍。
- 2026-09-29 `fake-recheck` r2：`i` 标题为 Track Info，却展示播放/授权状态而非搜索结果中
  选中的歌曲元数据。代码里的 `infoLines` 是当前播放信息，非 wire 丢字段；是否改弹层名称或
  增加选中项详情，先定用途，不能由 fake 时长数据判真实曲目缺失。
- 2026-09-29 `three-rounds` r2 的 Genre 与 `2026-09-29-oq42-ui-recheck` r3 的 Country
  均在 80×18 长列表下看不到 Filter/操作提示；`internal/tui/views.go` 把提示放在候选尾部，
  再按选中项裁切。属紧凑窗问题，**低于合理尺寸的审美与任务问题**；若要修，先用固定长列表
  证明提示可见且不会挤掉核心操作，再做同尺寸 PTY。`Showing: jazz · jazz` 缺字段名仍是文案候选。

**已排除**：`2026-09-29-oq42-ui-recheck` r3 的 Browse `S` 实际出现短暂
`Sorted by Recommended — 6/97 measured`，推翻早先“完全无反馈”的判断；不再作为待修项。
`three-rounds` r2 成功搜台、筛选并收藏；目录数量与可播放性不是 fake 的确定性证据。
首次搜索输入看到的 `o` 没进完整键序且随输入消失，不作为独立问题。

**下一步**：其余候选以任务影响排序，紧凑窗次要提示的优先级低于正常尺寸体验。
产品取舍类由用户决定是否接受，再考虑记入
[`limitations.md`](limitations.md)。
