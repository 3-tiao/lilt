# Round 15 · 完全新手（第四批 · 验证 Phase A）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r15*）
- 构建：工作区（Phase A/B/C 全部改动）
- 评分：7/10
- 目的：验证 A1 选中态标记、A2 动态确认标签/提交落点、A4 首屏定位
- 结果：A1/A2 明显生效；暴露两个小项（已在 R15 后立即修复）

## Agent 报告（原文）

# Round 15 报告：完全新手

## 时间线
1. 启动应用 → 看到顶部 `Apple Music`、`Radio`，底部写着 `Apple Music & radio — Tab switches source, / searches`。
   感受：大致知道这是听 Apple Music 和网络电台的终端播放器，但没有新手介绍。

2. 按 `Tab` → 进入 `[Radio]`，显示 `Favorites (0)`，并提示 `press a to add a stream URL, / to search stations`。

3. 按 `3`，等待加载 → 进入 `Browse`，显示 `Popular Worldwide (20)` 电台列表。当前项前的 `>` 很明显。

4. 按 `Down` ×13，`Enter` → 播放 `101 SMOOTH JAZZ`；`LIVE · buffering` → `LIVE · playing`；`Space` → `LIVE · paused`。

5. 按 `/` → `Search & Filters`；`Enter`，输入 `jazz`，`Enter`。界面显示 `Text jazz` 和按钮 `[ Search "jazz" ]`。
   感受：能看出文字已填入，但还需要手动移到搜索按钮，步骤偏多。

6. 按 `Tab` ×5，`Enter` → 显示 `Showing: jazz (0)`、`(no stations matched — press / to adjust the query)`，
   并出现原始网络错误 `Error: Get "https://.../stations/search?...name=jazz..."`。
   感受：知道搜索确实提交了，但技术错误直接露给普通用户。

7. `Escape`、`/`、`Enter`、输入 `rock`、`Enter`；`Tab` ×5、`Enter` → `Showing: rock (20)`，结果均为摇滚相关。

8. `Down` ×2、`f` → `Rock Antenne` 行出现 `★`，提示 `★ Favorited: Rock Antenne`。

9. `1` → `Favorites (1)` 中确有 `Rock Antenne`。

10. `q` → 正常退出。

## 交互清晰度评分（1-5，5 最好）
- 能不能一眼看出当前选中了哪一项？**4/5**：`>` 和顶部方括号标签清楚。
- 输入搜索词后，能不能知道「还没生效」以及怎么提交？**4/5**：有 `[ Search "rock" ]` 按钮，但要用多次 `Tab` 才能到达，不够直接。
- 启动时能不能知道这个 app 是干什么的？**3/5**：能从 `Apple Music & radio` 推断用途。

## 发现的问题
1. **中：搜索失败时暴露原始网络错误。** 最小复现：`/ → Enter → jazz → Enter → Tab×5 → Enter`。
2. **中：搜索提交流程偏长。** 最小复现：`/ → Enter → rock → Enter` 后仍需 `Tab×5` 才能到提交按钮。
3. **低：界面主要是英文。**

## 好的方面
- 顶部始终清楚显示当前来源和视图。
- 播放过程有 `buffering`、`playing`、`paused` 等明确状态。
- 搜索成功后标题 `Showing: rock (20)` 和结果共同证明条件生效。
- 收藏同时提供星标、即时提示和收藏列表三层确认。

## 改进建议
- 将网络错误转成可理解文案，并提供重试入口。
- 输入搜索词后支持直接提交，或明显提示如何提交。
- 提供简体中文界面或首次启动的简短中文说明。
- 空收藏页增加更直接的浏览入口提示。

## 总体可用性评分（1-10）与一句话结论
**7/10**：核心播放、搜索和收藏都能完成且反馈清晰，但搜索提交步骤和错误提示对完全新手不够友好。

## 编排者复核与即时修复

- **A1 验证通过**：选中标记被明确读到（`>`、`[Radio]`、`[3 Browse]`），清晰度 4/5，较前几批的「数着 Down 猜」显著改善。
- **A2 部分验证**：动态按钮 `[ Search "jazz" ]` 被理解；但「提交仍需多次 Tab」仍在。→ **R15 后立即修复**：
  文字编辑器 `Enter` 提交后焦点自动落到确认按钮（再一次 Enter 即执行），并补测试 `TestTextCommitLandsOnConfirm`。
- **问题 #1 修复**：Radio 目录错误不再暴露原始 URL，改为
  「Radio directory unavailable — check your connection, then retry (/ to search, 3 to browse)」，
  原始错误仅入日志；补测试 `TestRadioDirectoryFailureUsesFriendlyCopy`。
- **建议「空收藏页加浏览入口」修复**：空态追加 `, 3 to browse`。
- 问题 #3（中文化）维持不修（产品方向）。