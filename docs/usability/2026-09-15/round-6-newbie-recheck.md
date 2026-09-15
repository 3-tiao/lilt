# Round 6 · 完全新手（复跑验证 · 第二批）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r6*）
- 构建：工作区（含收藏缓存修复 + p 切换），未提交
- 评分：7/10（第一批 R1 为 6/10）
- 目的：验证第一批高严重度问题（收藏缓存）是否修复
- 结果：**收藏链路修复确认**——`f` 收藏后 `Favorites (1)` 正确显示

## 编排者 Prompt（全文）

（与第一批 Round 1 相同，仅替换会话/文件名为 usability-r6；目标一致：弄清用途、播放电台、收藏并确认生效。全文见 round-1-newbie.md，此处不重复。）

## Agent 报告（原文）

# Round 6 报告：完全新手

## 时间线
1. 启动应用 → 约 4 秒后看到 `lilt`，有 `Apple Music`、`Radio` 两个来源，底部提示 `? help`。即时感受：像音乐播放器，但不知道具体定位。
2. 按 `?` → 出现 Help，看到 `a add a stream URL`、`f favorite`、`/ search` 等说明。即时感受：确认这是可播放 Apple Music 和网络电台的终端音乐应用。
3. 按 `Escape`、`Tab` → 切到 `Radio`，显示 `Favorites (0)` 与提示“`press a to add a stream URL, / to search stations`”。即时感受：能理解这里是电台功能。
4. 按 `/` → 打开 `Search & Filters`；输入 `jazz`，按 `Enter` 保存文本。
5. 按 `Tab Tab Tab Tab Enter Right Enter Tab Tab Enter` → 回到收藏页；按 `3` 进入 Browse。约 8 秒加载后显示 `Popular Worldwide (20)`，而非明显的 jazz 搜索结果。即时感受：搜索流程和是否生效不清楚。
6. 按 `Down` ×15、`p` → 选中并播放 `101 SMOOTH JAZZ`；先显示 `LIVE · buffering`，约 4 秒后变为 `LIVE · playing · live stream`。播放验证成功。
7. 按 `f` → 条目和播放栏均出现 `★`，底部显示 `★ Favorited: 101 SMOOTH JAZZ`。即时感受：收藏反馈很明确。
8. 按 `Space` → 状态从 playing 变成 `buffering`，没有显示“已暂停”。按 `1` → `Favorites (1)` 中确实看到 `101 SMOOTH JAZZ`，收藏确认成功。
9. 按 `v` → 显示 `Nothing playing`。按 `q` → 应用退出；确认 tmux session 已结束。随后执行清理 session。

## 发现的问题
1. **中：应用用途缺少首屏说明。**  
   最小复现：启动。  
   首屏只有来源和音乐列表；必须主动注意并按 `?`，才能知道它同时支持 Apple Music、网络电台、收藏等功能。

2. **中：电台搜索的提交流程不直观，结果状态不透明。**  
   最小复现：`Tab` → `/` → `jazz` → `Enter` → 尝试用 `Tab/Enter` 提交 → `3`。  
   填入 `jazz` 后，难以判断当前焦点是否在 Confirm、Cancel 或某个筛选项；最终 Browse 标题是 `Popular Worldwide (20)`，列表也混有大量非 jazz 电台，没有明确告知搜索条件是否已应用。

3. **低：暂停提示与实际状态不一致。**  
   最小复现：播放电台后按 `Space`。  
   底部明确写 `space pause`，但屏幕变为 `LIVE · buffering · live stream`，没有“paused”状态，容易让人以为暂停失败或仍会继续出声。

4. **低：界面几乎全英文，对中文新手不够友好。**  
   最小复现：启动或打开帮助。  
   `Search & Filters`、`Browse`、`Favorites`、键位说明等均为英文；基础使用尚可，但理解成本更高。

## 好的方面
- 首屏底部直接展示 `? help`、`q quit`、`Tab source`，可发现性不错。
- Radio 收藏页为空时给出了明确下一步：`/ to search stations`。
- 播放状态清晰区分 `buffering` 和 `playing`。
- 收藏操作反馈非常好：条目星标、播放栏星标、toast 文案，以及 `Favorites (1)` 均能确认结果。
- `v` 能可靠停止播放，`q` 能正常退出。

## 改进建议
- 首屏增加一句简短定位，例如“终端音乐播放器：浏览 Apple Music 与网络电台”。
- 在搜索弹窗中显式高亮焦点，并在确认后显示“已应用：jazz”及结果数量。
- 搜索结果页面标题应反映搜索词或筛选条件，而不是继续显示 `Popular Worldwide`。
- 暂停后应稳定显示 `paused`，而不是长期/短暂显示 `buffering`。
- 提供中文界面或至少中文帮助文案。

## 总体可用性评分（1-10）与一句话结论
**7/10**：播放和收藏电台都能完成且反馈明确，但新手最关键的“搜索并确认已搜索”流程仍显得不够确定。

## 编排者复核

- **收藏缓存修复验证通过**：R1/R2/R4 的阻断问题在本轮完全消失，`Favorites (1)` 正确出现。
- 问题 #2 为第一批「焦点不醒目」的加强版：新手用 Tab 循环时误入 facet 行并打开选择器，最终没能提交查询（看到的是 Popular Worldwide 而非 Showing: jazz）。选中态可见性升级为第二批最高优先级。
- 问题 #3 属瞬态快照：pause 后 AVPlayer 的 timeControlStatus 先短暂停留在 waiting（buffering），随后 KVO 才发 paused。低优先级；可考虑 pause 立即发送一次 paused 快照。
- 问题 #4（中文化）记录为产品方向性建议，暂不排期。