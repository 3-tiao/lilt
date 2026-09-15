# Round 7 · 听朋友介绍过（复跑验证 · 第二批）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r7*）
- 构建：工作区（含收藏缓存修复 + p 切换），未提交
- 评分：5/10
- 目的：验证收藏缓存修复；本轮遭遇 Radio Browser 大面积超时
- 结果：**发现并促成修复一个新的高严重度竞态 bug**（见编排者复核）

## 编排者 Prompt（全文）

（与第一批 Round 2 相同，仅替换会话/文件名为 usability-r7；目标增加第 5 项「试一次播放中的暂停，观察状态是否让你确信真的暂停了」。全文见 round-2-introduced.md。）

## Agent 报告（原文）

# Round 7 报告：听朋友介绍过

## 时间线
1. 启动应用 → 默认是 Apple Music「Home」，底部提示 `Tab source`。感觉入口清楚。
2. `?` → 帮助明确列出 `tab switch source`、`f favorite`、`/ Radio Search & Filters`、`space / c pause or resume`。
3. `Escape`、`Tab` → 切到 Radio，显示「Favorites (0)」及 `press a to add a stream URL, / to search stations`。Tab 切换成功。
4. `/` → 打开「Search & Filters」，有 Text、Language、Genre、Country、Reset filters。
5. `Down Enter`（Language）→ 加载后显示 `Unable to load options`；`Escape` 返回。
6. `Down Down Enter`（Country）→ 成功看到国家列表及数量；`Down×8 Enter` 选中 `China`；`Down Down Enter` 确认。随后显示 `Showing: China (0)`，最终 `no stations matched` 和超时错误。
7. `Escape` → 回到 `Popular Worldwide`，确认这是默认电台列表的返回方式；但列表仍为空，带超时错误。
8. `/ Enter jazz Enter Down×5 Enter` → 文字搜索流程可完成，但结果同样超时、为空。
9. `1 a` → 通过界面提示尝试手工添加电台 URL。先试 HTTPS SomaFM，状态一直为 `LIVE · buffering`，收藏仍为 0。
10. `v a` 后输入 HTTP SomaFM URL 并 `Enter` → 显示 `LIVE · playing · live stream`，实际开始播放。
11. `f` → 收藏列表仍显示 `Favorites (0)`，没有任何确认提示或新增条目。
12. `Space` → 显示 `LIVE · paused · live stream`，底部快捷键变为 `space resume`；屏幕状态足以确信已暂停，随即无声。
13. `q` → TUI 退出；再次 capture 显示 `no server running`，确认进程/会话已结束。随后执行了仅针对 `usability-r7` 的清理。

## 朋友说法核对表

| 朋友说的 | 实际体验 | 结论（属实/不符/部分） |
|---|---|---|
| Tab 能在 Apple Music 和网络电台之间切换 | 一次 Tab 即从 Apple Music Home 切到 Radio Favorites。 | 属实 |
| 电台能收藏 | `f` 没有将正在播放的手工电台加入 Favorites；列表持续为 0。 | 不符 |
| 能搜电台 | 有完整搜索界面，文字搜索可提交；本次实际请求超时，未得到结果。 | 部分 |
| 搜索可按语言、国家过滤 | 语言选项加载失败；国家选项可加载并选择 China，但查询超时且无结果。 | 部分 |
| 播放中能暂停 | HTTP 电台显示 playing 后，Space 变为明确的 paused，快捷键也变为 resume。 | 属实 |

## 发现的问题
1. **高**：手工添加并播放的电台无法收藏，且与界面承诺矛盾。
   最小复现：`Tab` → `1` → `a` → 输入 `http://ice1.somafm.com/groovesalad-128-mp3` → `Enter` → `f`。
   结果：播放区显示 `LIVE · playing`，但 Favorites 始终为 `Favorites (0)`。

2. **中**：电台搜索及默认热门列表请求频繁超时，导致核心发现功能不可用。
   最小复现：`Tab` → `/` → 输入 `jazz` → Confirm；或 `Escape` 返回 Popular Worldwide。
   结果：`context deadline exceeded`，列表为空。

3. **中**：Language 过滤项直接无法加载，而 Country 能加载，错误信息被截断。
   最小复现：`Tab` → `/` → `Down Enter`。
   结果：`Unable to load options`，下一行 URL/错误被截断，无法判断该怎么处理。

4. **低**：收藏失败没有反馈。
   最小复现：播放手工电台后按 `f`。
   结果：界面无成功、失败或“不支持此类电台”的提示。

## 好的方面
- 帮助页简洁且覆盖关键操作。
- Tab 切源、`/` 打开搜索、Escape 回到 `Popular Worldwide` 都容易发现。
- 暂停状态反馈非常明确：`playing` → `paused`，并同步提示 `space resume`。
- 国家筛选器展示电台数量，选择体验直观。

## 改进建议
- 修复手工 URL 的收藏流程，或明确说明 `f` 不适用于手工流；成功后应立即在 Favorites 出现条目。
- 为 Radio Browser 请求增加可靠性处理：重试、备用节点、超时后的可操作提示。
- 语言列表加载失败时展示完整且用户可理解的错误，并提供重试。
- 对收藏、搜索、筛选的成功/失败给出显式状态提示。

## 总体可用性评分（1-10）与一句话结论
**5/10**：基本导航和暂停很顺畅，但电台搜索不稳定且收藏无法完成，未能满足首次用户的核心预期。

## 编排者复核（本批次最高价值发现）

- **问题 #1 确认为真实高严重度竞态 bug，已修复**：
  - 根因：`actionMsg` 的 `afterSequence < m.sequence` 守卫丢弃整条消息。电台播放时 helper 在 RPC 响应后立即发布
    `stateChanged` 通知，该通知经不同 goroutine 常常先于 actionMsg 到达并使 sequence+1，导致已成功完成的动作
    的收藏/最近播放元数据被静默丢弃。`a` 流程的自动收藏几乎必然丢失；第一批 R1 的 recent 缺失同源。
  - 修复：通知较新只跳过状态快照与 queue context 应用；已完成动作的元数据（recent/favorite/preset/note/refresh）
    无条件落盘。回归测试改为 `TestNewerNotificationSkipsStaleStateButKeepsCompletedMetadata`；
    playback-state-sync.md 同步更新。PTY 复现修复后 `Favorites (1)` 正确出现。
- **问题 #2/#3 为环境性发现**：Radio Browser `de1` 在 04:34-04:36Z 段大面积超时（languages/browse 均超时），
  直接验证了 radio-discovery-health.md 中镜像发现与 failover 的必要性，升级该设计的优先级参考。
- **问题 #4 成立（低）**：无选中项时 `f` 静默无操作；可在 footer 隐藏 f 提示（已按选中项类型条件显示）或加 toast。
- 暂停链路本轮明确验证：`playing → paused` + `space resume` 提示，第一批的暂停疑虑未再出现。