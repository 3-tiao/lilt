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
| OQ17 | 填充后 re-pin 被拒，停在"队列就绪未播放" | 中 | **已修待复测**（特定 Code=1 有界重试一次；hermetic 通过，真机仅命中 one-shot，长填充守护不可确认） | 先解决长命令期间音频守护的可靠停播，再在确有 append 回退的样本上复测 |
| OQ36 | browser 引擎 `mode=full` 谎报窗口与 storefront 覆盖 | 中 | 已修待真机确认 | 补 90 秒媒体与目录时长不符的真实负路径 |
| OQ37 | 撤销过期应提示 "Undo expired"，真实轮未看到 | 低 | 未修·待验证 | 确认定时器与 watch 两条路径是否都发该消息 |

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
MusicKit 拒绝整批、走 append 回退的罕见内容上；修复收益相应下降，根因仍未明。

**已修待复测**：填充后不再走普通 `resume`，改用专用 `resumeFilledQueue`；仅首次 `play()`
抛出 `MPMusicPlayerControllerErrorDomain Code=1` 时在 helper 内等待 200 ms 并重试至多一次。
再次拒绝、不相关错误或取消均保持原有 `partial_failure` + `queueReady`，不让 server 隐式重新播放；
helper 仍要求当前曲目的播放位置真实推进。Swift hermetic 测试覆盖首拒后成功、二次拒绝、
不相关错误、取消及首次抛错但已起播；server 覆盖填充后走专用路径的成功与失败，Go RPC 测试
核对私有 method。真实 MusicKit 的该 Code=1 故障尚未在修复后复现，不能据此称真机已通过。

**本轮复测边界（2026-09-29）**：获批的私有真机专辑轮中，守护的 `session.status` 在起播命令
持锁期间 2 秒超时、随后 `playback.stop` 未确认，故 `2026-09-29-oq17-real` 标为**无效**；事后
独立 status 为 stopped，helper 日志仅有耗时 4550 ms 的 `playSongs` 成功，未进入 append 或
`resumeFilledQueue`，不能验证这次修复。守护后改为 8 秒单次超时，仅缓解普通短起播；节奏填充
可能持续 10–40 秒，不能据此保证 OQ17 长命令仍有安全的音频上限。

**下一步（可执行）**：

1. ~~先为 append 长命令建立**可证明在音频上限内停止**的隔离守护~~ **已解决（2026-10-02）**：
   守护的 `playback.stop` 此前因缺 `ifServerInstanceId` 被服务器拒绝，安全网自 epoch
   强制（`025094a`）起一直失效；且它把切源瞬间的一次 `stopped` 当成"参与者停播"而退出
   并报成功。两个缺陷已修（`a6dc4d0`、`eabf801`），真实轮次里守护已能自己在 40 秒
   上限停播。下一步直接进入第 2 项。
2. 在确实触发 append 的内容上复现 `stop` → 立即 `play`，抓 helper 时间线里的 re-pin
   `playbackStatus`/`currentEntry`；普通 one-shot 成功不能充当重试证据。
3. 核对该轮 `Code=1` 后是否最多重试一次且真正起播；若仍失败，确认保留队列和
   `partial_failure`，不要把单次成功或无法复现误认为整体稳定性证明。

**关联**：[`../client-api/errors.md`](../client-api/errors.md)（`queueReady`）、
[`../internals/playback/helper-rpc.md`](../internals/playback/helper-rpc.md)。

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
目录时长不符的真实负路径仍未复测**（未为测试强改账号 storefront/profile）；2026-09-29
`2026-09-29-oq36-real` 私有有声轮仍得到 3:22 全曲而非 90 秒（守护记录观察播放 5.6 秒，正常停止）。
不能只凭全曲路径归档。下一步在不改共享账号地区设置的独占窗口取得可稳定复现的真实试听样本，确认公开
mode 不为 full，再按本台账规则归档。

**发现于**：2026-09-23 macOS browser 模式真机验收（国区订阅账号）。

## OQ37 · 撤销过期本应提示 "Undo expired"，但真实轮里没看到（低，待验证）

**现象**：在 Up Next 面板按 `x` 删除一首，5 秒内按 `u` 能正常恢复（48 → 47 → 48，光标回到原位）。
但等 offer 过期后再按 `u`，**画面没有任何变化**，`u undo` 也已从 footer 消失。

**为什么这仍值得记**：实现里过期**本来是有提示的**——`TestQueueUndoExpiryExplainsWhyHintDisappeared`
断言过期后 `message == "Undo expired"` 且 `messageErr` 为真。但真实轮里，等了 8 秒再按 `u`
之后，屏幕上既没有 `Undo expired`，也没有任何其他输出（已专门 grep `expired` / `undo`）。
两条可能都还成立：

1. "Undo expired" 确实出现过，但在 8 秒内被后续消息覆盖或清掉了；
2. 过期消息路径在真实运行中没有触发（测试走 `queueUndoExpiredMsg` 直喂，绕过了定时器与
   watch 失效的真实时序）。

**影响**：低。撤销功能本身无损坏，窗口内行为正确（`a2r` 与
`TestQueueRemoveOffersLatestUndoAndUUsesItsRevision` 覆盖）。受影响的是"过期之后用户还按
`u`"这一路径的反馈完整性。

**证据**（2026-10-02 真实轮 `a2r`，真实 Apple Music 授权账号、真实 48 首歌单队列）：
最小复现 `2` `j` `x` → 等 8 秒 → `u`；队列停在 47 首不变，屏幕无 `expired` 字样。
完整键序与屏幕事实见该轮报告（运行产物，不入库）。

**已排除的假设**：
- 不是"撤销失效"：同一轮窗口内按 `u` 确实恢复了曲目。
- 不是"过期无提示所以 `u` 静默是缺陷"：若第 1 种可能成立，过期已提示，那么之后的 `u`
  静默是合理的，条目可关闭。

**下一步（可执行）**：先用 hermetic 装置确认 `queueUndoExpiredMsg` 之外的时序——即定时器
到期与 watch 推进两条路径是否都会发出该消息。若两条都发，则本条按第 1 种可能关闭；若有一条
不发，补测试并修。5 秒窗口本身是否偏短属**产品判断**，不由实现侧决定。

**关联**：[`../ui/model.md`](../ui/model.md)（Up Next 键位）、[`../ui/ux.md`](../ui/ux.md)（footer 提示）。
