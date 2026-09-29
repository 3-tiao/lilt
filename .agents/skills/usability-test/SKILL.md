---
name: usability-test
description: 基于真实构建，以未知内部实现的独立 agent 走查 lilt 的可用性。用于发现真实运行中才会暴露的交互、反馈、布局与状态问题；不替代 hermetic 或 contract 测试，也不用于评审代码或规范。
---

# lilt 基于真实构建的 agent 可用性走查

本 skill 让多个**不了解内部实现**的 agent，通过 tmux PTY 操作同一个固定构建的 lilt。
**默认用假播放后端，不出声**；真实 MusicKit/Radio/音频只在用户批准的独立窗口验收。产出可复现的
问题与修复验证，隔离与音频边界以 `docs/testing/integration.md` §5a 为准。

它是**agent 走查，不是人类用户研究**：人设只用来改变探索视角，不能把 N 个 agent 轮次当作 N 位
独立用户，也不能用它宣称人类可用性覆盖率。每次运行的产物是运行时数据，不进仓库。

**为什么需要它**：单测覆盖纯函数时，真实运行仍会暴露跨层缺陷。例如歌单内“从所选歌曲播放”曾因
MusicKit 队列异步填充而失效；纯函数测试看不到这个问题。真实构建走查与 hermetic 测试互补，不能
互相替代。

## 边界

- 用：发现交互、反馈、文案、布局、状态可见性问题；验证修复能否被独立体验者感知。
- 不用：替代 `docs/testing/integration.md` 的 hermetic/contract 测试；评审代码质量、规范一致性或
  性能；作为功能验收的唯一证据。
- 若被测量的是 Client API 或 provider 契约，先写 hermetic 测试；本 skill 只回答“人能否顺利完成”。

## 铁律（不可协商）

1. **参与者必须无知**：agent 只能看屏幕和应用内帮助。严禁读源码、`docs/`、git 历史、README、
   `/tmp` 日志或 state；读了内部资料的“发现”只是复述。
2. **只隔离路径，不碰真实实例**：每个 round 用独立 socket/state/log/config/cache；绝不 kill、
   attach 或列出其他 tmux session、lilt/lilt-player 进程。
3. **测固定构建**：一批先生成 build manifest；每轮都绑定它，二进制或 helper 有变化就开新 batch。
4. **round 内不改仓库**：参与者不得写文件；编排者在 round 内不修代码。测试与修复分开批次。
5. **默认静音，real 单独授权**：完整 batch 可全部用 `--fake`，无需凑一轮 real；real 会出声，只有
   用户明确批准时间窗口、确认不在使用预发布播放时才能启动。不清楚就不跑，不自动停止日常 server。
   real 每轮总音频 ≤60s，验证后立即暂停或停止；共享 Apple 账号只能用于获批的播放验收，默认不得
   收藏、改资料库或断开授权。共享账号可能影响收听记录/推荐，不宣称零副作用。
6. **不确定就如实记录**：卡死、无法退出、报错都要写进报告，不许美化或用推测掩盖。
7. **只读探针不许自起 server**：编排者核对来源能力/状态只用 `$R probe` 直连本轮 socket；
   不手拼 `LILT_SOCKET` 调可自动启动 server 的 CLI，也不借探针发播放、队列或账号写命令。

## 装置

用 `scripts/round.sh`（依赖 tmux 与 `python3` 标准库）。从仓库根目录执行下面命令；`round.sh` 自行定位仓库。

```bash
R=.agents/skills/usability-test/scripts/round.sh

# 默认：静音完整 batch（3–5 轮 + index.md）；verify 不编译，必须随后 build。
just verify && just build-go
$R preflight 2026-09-16-layout --fake-only
$R start r1 --batch 2026-09-16-layout --fake --cols 110 --rows 30

# 真实播放仅在获批窗口另开 batch；必须构建并固定签名 helper。
# just build
# $R preflight 2026-09-16-audio --probe --real-enabled
# LILT_TEST_AUDIO=1 $R start r2 --batch 2026-09-16-audio --real --cols 110 --rows 30

$R send r1 /                         # 特殊键：Enter Escape Space Tab BSpace Up Down C-c
$R capture r1                        # 纯文本画面
$R capture r1 --ansi                 # 核对颜色/对比度时用
$R resize r1 80 18                   # 运行中改变尺寸
$R status r1                          # tmux 与 server 是否仍存活
$R probe r1 sources                   # 编排者只读；也可用 status / auth，不会自起 server
$R guard r2 &                         # real 专用：后台启动，参与者操作前须确认已 armed 且进程存活
$R wait-steady r1                     # 等画面不再变化
$R wait-frame-change r1 j             # 只判断帧有没有变化，不判断按键是否被处理
$R stop r1                            # 收掉该 round 的 server 与 tmux session
# 编排者先把原始 prompt、报告和复核写在 /tmp/lilt-usability/2026-09-16-layout/round-1-queue.md：
# $R evidence r1 >> /tmp/lilt-usability/2026-09-16-layout/round-1-queue.md
# $R report-check r1 /tmp/lilt-usability/2026-09-16-layout/round-1-queue.md
```

- `preflight <batch>` 在 `/tmp/lilt-usability/<batch>/manifest.txt` 记录完整 commit、dirty worktree 指纹与
  lilt/helper SHA-256。manifest 不可重写；worktree、CLI 或 helper 变化都会拒绝复用，必须换 run 名。
- `start` 必须带 `--batch`，且会核对 hash。仅在拿到首帧后才写 `<round>/ready`；每次同名 `start`
  都会清空旧的 `/tmp/lilt-round-<name>/`，以免旧 state/log 进入新一轮。
- **启动中断或超时 = 本轮无效。**`ready` 缺失时，不得发 prompt、不得计分或进入问题汇总；运行
  `$R stop <name>`，再用**新 round 名**重新 `start`。`$R status` 会拒绝未 ready 的 round，且只探测
  Unix socket，绝不自动启动 replacement server。
- 每轮私有目录还隔离 `LILT_CONFIG` 与 `LILT_RADIO_CACHE`，并以 `0700` 权限创建：
  `/tmp/lilt-round-<name>/` 下有 socket、state、log、config、radio cache 与 `keys.log`。
  `keys.log` 自动记录所有经脚本发送的键；它是复现入口，不要另靠记忆。
- `wait-frame-change` 只说明捕获帧变化；进度、动画或后台加载也会改变帧。**它不能证明按键有效，
  也不能单独证明卡死。**卡死至少要求：`status` 仍显示进程/server 存活，并且在稳定条件下两个应
  有效操作都没有可见反馈。
- `probe <round> status|sources|auth` 只允许 `session.status` / `sources.list` / `authorization.list`，
  需 ready 并直连该轮的 `/tmp/lilt-round-<name>/session.sock`。socket 不存在/失效时明确失败，
  **不会调用 CLI 的自动启动路径**；只供编排者复核，参与者仍只看屏幕。
- real 轮必须在交给参与者前后台启动 `$R guard <round>`，并确认守护进程仍存活、本轮
  `audio-guard.jsonl` 有 `armed`。它直连本轮 socket 读取状态，首次观察到 playing 后最多累计
  40 秒或经过 55 秒便用 `playback.stop` 停止；等待超过 300 秒或探针失败也会尝试直连停止，
  **不会向参与者 TUI 注入按键**。`participant_stopped` 表示参与者先停播；
  `probe_failed` / `watch_expired` 或 `stop_unconfirmed` 都令该轮无效。停止无法确认时立即中止操作、
  手工核实本轮 server/音频状态；不得继续播放或把轮次计分。guard 不替代参与者立即暂停/停止，
  也不证明真实音频听感。不要再用临时轮询脚本向 TUI 注入 `v` 做安全守护。
- `evidence <round>` 将完整 `keys.log` 输出为报告中的 `## 完整键序` 块；
  `report-check <round> <report.md>` 要求报告内有**非空** `## 参与者 prompt`（探针用
  `## 探针目标与命令`）及 `## 屏幕事实与复核`，并核对
  逐字完整的键序。它不判断屏幕事实或结论真伪，人工仍需核对。键序可能含输入内容，**不要将
  凭据或私密内容写进 round，也不要把报告发布或提交到仓库**。
- fake 只测界面骨架（布局、层级、键盘、弹层、文案、尺寸状态）。它只替换 playback engine，**不保证**
  来源内容确定性、离线或可播放；不要由 fake 的来源内容、capability 或 `source_unavailable` 判断真实链路。
  真实内容、账号、provider 能力、网络降级与播放传输只在获批的 real 轮测；fake-only batch 可以
  完成，但不得宣称验证了真实播放。脚本默认 fake-only；真实模式需 `--real-enabled` manifest、
  `--real` 和 `LILT_TEST_AUDIO=1`，会拒绝与日常 server/其他 real 轮同时运行。授权开关不代替用户
  对时间窗口的明确确认。
- `stop` 后保留本 run 复核需要的 state/log/config/cache/keys；本批复测和汇总结束就删除对应
  `/tmp/lilt-round-*` 与 `/tmp/lilt-usability/<batch>/`。删除装置目录前，先将 prompt、完整键序、
  关键屏幕事实与编排者复核写入**自包含**单轮报告并跑 `report-check`；不可把已删除的 `keys.log`
  当成唯一复现入口。one-off probe 同样要有报告，CLI-only 探针记录命令与输出事实（键序可为空）。

## 轮型

只有两种。“回归”不是轮型，是任务型的一种标记（见下节）。

| 轮型 | 输入 | 产出 | 模板 |
|---|---|---|---|
| **任务型** | 人设 + 一组目标（可收窄到某路径及相邻路径） | `round-N-<persona>.md`，含评分 | [references/task-round-prompt.md](references/task-round-prompt.md) |
| **探索型** | 人设 + 分配给该轮的覆盖格子，无具体目标 | 异常清单 + 完整键序 | [references/exploratory-round.md](references/exploratory-round.md) |

- 想验证“人能不能完成这件事” → 任务型。
- 版本迭代、重构、新 surface 上线后想扫广度 → 1 个探索型 + 2–3 个落在改动区域的任务型。
  探索型负责未知路径，任务型负责主流程。

## 轮次设计

**一轮 = 一个人设 +（一组目标或 3–6 个覆盖格子）+ 一份报告。**运行分两种：

- **one-off probe**：一轮、一个 immutable manifest、一份报告；用于确认单一任务、重放候选或验证
  装置。用 `$R preflight <name> --probe`，**不要求 `index.md`，不做跨轮评分结论**。
- **完整 batch**：3–5 轮同构建 + `index.md`；用于发现问题、修复后复测与跨轮结论。用
  `$R preflight <name>`。

小批次的目的是真正修复并复测，而不是把 agent 数量伪装成统计样本。

- **任务导向，不是功能清单**：写“找到电台并查看播放反馈”（目标 + 可验证结果；fake 轮只判断
  界面反馈），不要写“测试 f 键”。共享账号的 real 轮不得默认要求收藏等账号写入。
- **评分先检查任务前提**：编排者可用 `probe sources` 核对 fake 环境的能力。如果目标被 fake 的
  授权、capability 或播放传输限制挡住，保留参与者的界面观察，但该目标标“未可测”，
  不把任务失败计成真实来源可用性失败；任务型评分标 `N/A（fake 前提不成立）` 或明确限定为
  “fake 场景体验”，不作为正常来源的回归基线。比较轮次须同人设、目标、尺寸，且**任务前提相同**。
- **不引导**：prompt 不出现按键名、界面文案或入口位置；让“找不到”本身成为发现。
- **人设要有依据**：涉及账号能力的人设，先核实机器现实（`lilt auth status`、`lilt doctor`）。
  不要像 Round 11 那样假定机器无订阅、实际却有订阅。
- **人设库**：完全新手 / 听人介绍过 / 老用户（seeded state）/ 目标搜索者 / 小窗口（80×18；
  60×12 低于 console 最小高度 17 行，会被按设计拒绝，不作为可用性人设）/ 极小窗口（30×8）/ Apple
  Music 歌单与队列 / 无订阅试听 / 自定义流 URL / 整理取消
  收藏 / 慢网络与断网 / 非拉丁文本 / 乱按型（探索）。当前装置不能可靠注入鼠标事件，**不安排
  “鼠标优先”轮**；鼠标改动以 TUI 单测和人工 opt-in 检查覆盖。

### 探索型的覆盖地图（批次级）

纯随机按键会被 `j`/方向键吃掉，几百次仍留在同一屏。覆盖地图是**批次的到过清单**，不是参与者的
任务说明：先列完整范围，再把每轮分配到 3–6 个格子。

| 维度 | 格子写法 | 例子 |
|---|---|---|
| 来源 × 视图 | 屏幕可见的来源和其子视图 | Radio × 搜索结果 |
| 弹层与临时态 | 可打开的界面状态 | 帮助、搜索输入、命令面板、主题、加载、空列表、错误提示 |
| 尺寸 | 启动尺寸或运行中 `resize` 后的尺寸 | 120×32、80×18、30×8 |

批次汇总要标出每个格子由哪一轮完成、哪些未覆盖及原因。一次探索轮只说明“这一轮没有撞到”，
**不构成“没有回归”**。

## 执行

1. 默认选新 run 名，运行 `just verify && just build-go`，再运行 `$R preflight <name> --fake-only
   [--probe]`，所有轮次都带 `--fake`；完整 batch 把 manifest 路径写进汇总，one-off probe 只写入
   单轮报告。获批真实播放才运行 `just build`、`preflight --real-enabled`，另开 batch 并
   用 `LILT_TEST_AUDIO=1 $R start … --real`。开发构建与预发布固定制品已分开，但真实轮仍共享账号、
   profile 和音频；任何资源占用都必须明确拒绝或交由用户安排，不能自行停服。
2. **preflight 后冻结运行环境**：不得编辑仓库、运行 `just`、重建 CLI/helper 或再跑门禁；任一动作
   都会改变 worktree 或二进制 hash，当前 run 必须结束，重新 `verify → build → preflight`。
3. 完整 batch 先做覆盖地图，再为每轮分配 3–6 个格子或任务；任务型用
   [references/task-round-prompt.md](references/task-round-prompt.md)，探索型用
   [references/exploratory-round.md](references/exploratory-round.md)。
4. 编排者起装置后先运行 `$R status <name>`，确认 ready、tmux 与 server 都存活；任务依赖来源能力时
   再用 `$R probe <name> sources` 核对测试前提，才发 prompt 给
   独立 agent；real 轮还要先启动 guard 并确认 armed，再让它**仅通过 `$R send/capture/resize` 操作**
   → 收集最终报告与 guard 记录 → 编排者 `$R stop`。
   若 `start` 被中断、超时或 status 失败，本轮作废：stop 后换新名字重开。一次只跑一轮，避免争
   音频设备和网络带宽。
5. 编排者复核每份报告，把“参与者说的问题”变成可判定结论；把发给参与者的原始 prompt、其完整
   时间线、`evidence` 输出的键序及自己引用的画面事实保存在**同一份**报告，执行 `report-check`。
   探针记录目标、命令、响应事实和复核；无效探针也如实标注，不作为产品证据。
6. 完整 batch 汇总写进 `/tmp/lilt-usability/<batch>/index.md`；one-off probe 无需 index，但必须有
   经 `report-check` 核对的单轮报告。index 与各轮报告对齐，回归完成、证据自包含后再清理运行目录。

## 编排者复核

参与者只会描述现象。编排者必须区分三类东西：

| 现象 | 复核动作 |
|---|---|
| 疑似真实 bug | 用干净隔离 state 复现；定位 TUI/server/helper/provider 层；有代码证据才叫 bug |
| 装置噪声 | 上游 API 超时、网络抖动、agent 误操作 → 标注环境发现，不进修复清单 |
| 主观偏好 | 单轮且能完成但别扭 → 候选项，等跨轮复现再升级 |

- 同一问题跨轮复现时，写清轮次编号；单轮发现标“待复现”，不直接排期。
- 不要停在“上层参数正确”。用最小复现 + 可回读探针定位根因层，不靠猜。
- 每条候选在单轮报告与 index 中标证据状态：**参与者观察 / 有效复现 / 无效探针 / 已推翻 /
  待验证**。socket 错误、构建不匹配等探针必须标“无效”，不能继续被引用为 CLI 证据；
  新探针推翻早期判断时，要同步改 index 的严重度、归因、覆盖状态及台账，而不是只在末尾加说明。
- 修复前先写成可测断言：每条修复有 Go/Swift 回归测试**和**一次同任务 PTY 验证。

## 报告、回归与修复闭环

参与者轮的单轮报告必须含：**原样复制的参与者 prompt**、时间线（键序 → 屏幕事实 →
感受）、按严重度编号的问题（最小复现键序）、做得好的方面、改进建议、
`evidence` 导出的完整键序、编排者复核；任务前提成立的任务型轮次还需 1–10 评分与一句话结论。
前提被 fake 挡住时保留观察，评分标 `N/A` 或明确限定为 fake 场景；不要给真实来源体验打分。
one-off 探针报告只需原样目标与命令、响应或屏幕事实、编排者复核、完整键序（可以为空）；
不假造人设、评分或参与者时间线。

`index.md` 必须含：

1. batch、manifest 路径、构建 commit、fake-only/real 覆盖范围与装置说明；real 另记录用户批准的
   窗口与清理结果（不写账号凭据）。
2. 评分表（轮次 / 人设 / 任务前提 / 上一批评分 → 本批评分）；只有任务前提一致的盲回归轮
   才填写可比较的上一批评分，被 fake 挡住的任务用 `N/A`；如记录假场景体验评分，必须标明
   **不可与正常来源比较**。
3. 上一批每条修复的验证结论，区分**编排者同任务 PTY 探针**与**独立参与者盲回归**；前者不能
   代替后者，不能把探针分数填进盲回归评分列。
4. 高/中/低问题汇总：复现轮次、最小键序；探索型还注明覆盖格子与命中方式。
5. 多轮验证的“做得好的方面”、未覆盖格子与下一批建议。

严重度按**发生频率 × 影响 × 持久性**：核心路径反复失败为高；能绕过但需要猜测为中；文案、
对比度、留白为低。

**回归 = 任务型 + 三条约定**：

1. 被测构建包含修复。
2. 复用上一轮的人设、目标与尺寸；只换构建，才可比较。
3. 报告名 `round-N-recheck.md`，汇总表填写上一批评分。

不要把“修了什么”写进 prompt；给任务即可。修完后先复跑高严重度问题；中严重度可成组修复。
每条修复在本 batch 报告中写：问题 → 根因层 → 修复 → 单测名 + PTY 观察事实。修复后运行
`just verify`，文档改动按 [docs-maintenance](../docs-maintenance/SKILL.md) 进行编写与审阅。交付时区分**最终构建**的实机成功路径、实机失败路径和仅由
hermetic 测试覆盖的路径；旧构建的 PTY 证据不得算作修复验证，未覆盖的路径明确标为未验证。

## 反模式

- 让参与者读文档后“发现”文档里写过的问题。
- 只记录 commit，不构建或不记录二进制/helper hash。
- 复用同名 round 却把旧 state/cache 带进新一轮。
- 将 `wait-frame-change` 的成败当作“按键有效”或“应用卡死”的证明。
- 把上游 API 超时当产品缺陷（或把真实缺陷归给网络）。
- 一次跑完大量轮次却不修，问题越攒越不可信。
- 把启动中断、watch 断连等装置事故当产品问题；无效轮不计分、不进入问题汇总。
- 手填 socket 路径调用会自动起服的 CLI；删掉唯一的 keys.log 才发现报告只有摘要；
  在 index/台账里继续引用已无效或被推翻的探针。
- 把 fake capability 阻断的任务分数当真实来源基线，或把编排者 PTY 探针写成独立盲回归。
- 纯随机按键当探索，或把探索轮没撞到当没有回归。
- 把 fake 当成确定性、离线或全来源可播放环境；fake 只能判断界面骨架，真实链路需要另行获批 real。
- 未获授权就启动 real，或认为私有 socket 能隔离共享账号、浏览器 profile 和系统音频。
- preflight 后继续 edit/build/verify，却复用旧 manifest。
- 用真实账户判断布局。

## 运行产物（不入库）

```text
/tmp/lilt-usability/<run>/
├── manifest.txt                 固定构建标识与 run_kind（probe 或 batch）
├── index.md                     仅完整 batch：本批汇总；复测完成后删除
└── round-N-<persona>.md         本轮 prompt、报告与编排者复核；复测完成后删除

/tmp/lilt-round-<name>/          装置目录：socket/state/log/config/cache/keys；同批复核后删除
```

- 不要 `git add` 这些文件；`docs/` 下不写测试报告。
- 需要长期保留的结论写进正式文档：**未解决的项**进 `docs/product/open-questions.md` 台账（现象、
  证据、已排除假设、下一步），已接受的限制进 `docs/product/limitations.md`，设计契约进对应
  `docs/` 设计文档。代码修复与回归测试才是结论的长期载体；台账里只留未解决项，修好后删除。
