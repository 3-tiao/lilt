# Round 14 · 完全新手（第三批 · 第三数据点）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r14*）
- 构建：4c8bf4d + 歌单起点修复
- 评分：7/10（R1=6，R6=7）
- 状态：已完成；搜索提交**成功**（对比 R6 失败），焦点可见性成为第四次跨轮问题

## 编排者 Prompt（全文）

（与第一批 Round 1 相同的人设与环境模板，会话名 usability-r14；目标增加第 3 项
「搜一个类型并确认搜索条件真的生效」，并要求单列「搜索提交过程详述」一节。）

## Agent 报告（原文）

# Round 14 报告：完全新手

## 时间线
1. 启动后等待加载。屏幕是 Apple Music 首页，有“Recently Played”“Your Playlists”，底部提示“Tab source”。
   感受：像音乐播放器，但还不清楚 lilt 的整体用途。

2. 按 `Tab`。进入“Radio”，显示“Favorites (0)”及“press a to add a stream URL, / to search stations”。
   感受：终于确认它也能播放网络电台，入口提示很有帮助。

3. 按 `3`，等待加载。出现“Popular Worldwide (20)”电台列表。
   感受：能从编号视图猜到 Browse，是合理的探索路径。

4. 按 `Enter` 播放选中的 `RTL`，先看到“LIVE · buffering”，约 3 秒后变为“LIVE · playing · live stream”。
   感受：成功确认网络电台正在播放；约 7 秒后按 `Space`，状态变为“LIVE · paused”，避免继续出声。

5. 按 `/`。弹出“Search & Filters”，有 Text、Language、Genre、Country 和“Confirm”。
   感受：搜索入口容易发现。

6. 按 `jazz`。界面切换为“Search text”，显示“Search text: jazz”。按 `Enter` 后回到筛选弹窗，Text 显示为 `jazz`。

7. 按 `Down`，再按 `Down Down Down Down`，按 `Enter`。等待加载后，标题变为“Showing: jazz (20)”，列表中有“101 SMOOTH JAZZ”“Jazz Radio”“Jazz 24”等。
   感受：已能明确确认搜索真正生效，而不是只保存了输入文字。

8. 按 `f` 收藏当前的“101 SMOOTH JAZZ”。屏幕显示星标及“★ Favorited: 101 SMOOTH JAZZ”。随后按 `1`，进入“Favorites (1)”，列表中确有该电台。
   感受：收藏反馈和二次确认都很清楚。

9. 按 `q` 退出。tmux session 已随应用退出自动消失；随后执行清理时提示 session 不存在，说明进程已结束。

## 搜索提交过程详述
我先从底部的“`/ search & filters`”猜测 `/` 是搜索，按下后看到“Search & Filters”。弹窗里“Text Not set”很像可输入项，但底部同时写着“Type to search … · Enter edit”，让人有一点犹豫：到底是直接打字，还是先按 Enter？

我尝试直接输入 `jazz`，结果进入了单独的“Search text”输入框，说明直接打字确实有效。按 `Enter` 后，输入框关闭，但没有立即出现结果，而是回到筛选弹窗，Text 变成 `jazz`。此时我不确定搜索是否已经提交，因为屏幕仍然是弹窗。

根据底部“↑↓/Tab move”和“[ Confirm ]”，我用 `Down` 移动；但当前选中项在视觉上没有明显高亮。我继续按了四次 `Down`，推测焦点到 Confirm，然后按 `Enter`。弹窗关闭并加载，最终标题明确显示“Showing: jazz (20)”，列表也集中出现 Jazz 电台。这是搜索条件真正生效的可靠屏幕证据。

## 发现的问题
1. **中：搜索筛选弹窗没有可见焦点。**
   最小复现：`Tab` → `3` → `/` → `jazz` → `Enter` → `Down`。
   按方向键后，底部帮助文案会变化，但 Text、Language、Genre、Confirm 等行没有明显的当前选中态；新手难以判断再按几次才能提交。

2. **中：文字搜索存在两步确认，提交心智模型不清晰。**
   最小复现：`/` → `jazz` → `Enter`。
   第一次 Enter 只是把文字带回筛选弹窗，不执行搜索；还需要移动到 Confirm 再 Enter。没有明确提示“已设置，尚未搜索”。

3. **低：首次进入应用时用途说明不足。**
   最小复现：启动后观察首页。
   初始 Apple Music 页面展示了历史歌曲和歌单，但没有一句话解释应用可在 Apple Music 与网络电台之间切换；必须从底部“Tab source”主动推断。

4. **低：界面文案全为英文，对中文普通用户的理解成本较高。**
   最小复现：启动或打开搜索。
   功能提示本身不错，但“source”“Browse”“Confirm”等需要基础英文理解。

## 好的方面
- 底部快捷键提示持续可见，`Tab`、`3`、`Enter`、`Space`、`f`、`q` 都可直接发现。
- 电台播放状态清晰区分“buffering / playing / paused”。
- 搜索成功后标题直接显示“Showing: jazz (20)”，结果也与关键词高度相关。
- 收藏操作即时显示星标和“Favorited”提示；Favorites 视图还能二次确认。
- `q` 正常退出，session 自动结束。

## 改进建议
- 给筛选弹窗增加明显焦点高亮，并在 Confirm 上提供默认焦点或快捷提交。
- 在输入文字后提示“已设置搜索词；选择 Confirm 开始搜索”，或让 Enter 直接提交文本搜索。
- 首屏增加一句简短用途说明，例如“在 Apple Music 与网络电台之间浏览、播放和收藏音乐”。
- 考虑提供中文本地化或中英文关键操作提示。

## 总体可用性评分（1-10）与一句话结论
**7/10**：播放、搜索和收藏都能靠屏幕提示完成，但搜索提交的焦点与双重确认会让完全新手犹豫。

## 编排者复核

- 搜索提交成功（R6 失败 → R14 成功）：Tab 循环 + 提示行的改进有效提升了可达性，但成功部分依赖
  「数四次 Down」的猜测——焦点可见性问题（第四次跨轮出现）仍是首要改进项。
- 问题 #2（两步确认）首次被明确表述为心智模型问题：文本 Enter 后回菜单，用户不知道
  「搜了没有」——与焦点问题叠加。两个问题一起构成新手搜索体验的主要摩擦。
- 收藏→验证链路第三次通过（R6/R8/R14），确认前两批修复稳定。