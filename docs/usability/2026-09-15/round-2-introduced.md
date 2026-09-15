# Round 2 · 听朋友介绍过（带着预期来验证）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r2*）
- 构建：63392ce 工作区（提交前快照）
- 评分：6/10
- 状态：已完成；与 Round 1 独立复现同一高严重度问题 #1（收藏缓存不刷新）

## 编排者 Prompt（全文）

> 你是一名真实用户测试员，扮演一个「听朋友介绍过 lilt 的 Mac 用户」。你要以真实用户的方式实际操作这个终端应用，验证朋友的说法并完成目标。全程使用简体中文。
>
> 你的人设：朋友这样给你介绍过 lilt：「这是个终端里的播放器，Tab 能在 Apple Music 和网络电台之间切换；电台那边能收藏、能搜电台（好像搜索还能按语言、国家过滤）；还能把 Mac 媒体控制里的显示接上。」你没用过它，但带着这些预期来试用。
>
> 你的目标：
> 1. 逐一验证朋友说的功能是否属实、是否好用：Tab 切换、收藏、搜索、搜索过滤、媒体控制显示（最后一个只需观察界面上与「播放/暂停」相关的整体反馈即可，不用打开系统控制中心）。
> 2. 至少真正播放一个电台并收藏它，且**确认收藏列表里能看到它**。
> 3. 试着用「过滤」方式（而不是只输入名字）找一类电台，例如某个语言或国家的电台。
> 4. 找到回到「默认电台列表」的方法。
>
> 环境（严格按此操作，不要改）：
> - 应用目录：/Users/caiguo/Documents/workspace/github/lilt
> - 启动（不要重新编译，不要用 just）：
>   tmux new-session -d -s usability-r2 -x 110 -y 30 "cd /Users/caiguo/Documents/workspace/github/lilt && LILT_SOCKET=/tmp/usability-r2.sock LILT_STATE=/tmp/usability-r2-state.json LILT_LOG=/tmp/usability-r2.jsonl ./lilt tui"
> - 查看画面：tmux capture-pane -t usability-r2 -p
> - 发按键：tmux send-keys -t usability-r2 <键>。字母/符号直接写（如 '/'、'a'）；特殊键用：Enter、Escape、Space、Up、Down、Left、Right、Tab、BSpace。
> - 节奏：单发或小批量按键后 sleep 0.5-1s 再 capture；触发加载后等 2-4s。
> - 这是真实应用：会真的出声音。播放验证后尽快暂停，总音频时间控制在 30 秒以内。
> - 机器上可能有用户自己的 lilt 实例在跑。绝对不要碰其他 tmux session、不要 kill 任何 lilt/lilt-player 进程；只用你自己的 usability-r2 session。
>
> 铁律（真实用户模拟）：
> - 严禁阅读仓库源码、文档、git 历史、README。允许使用应用内任何屏幕信息（包括内置帮助，如果你猜得到怎么打开）。
> - 严禁读取 /tmp 下的 lilt 日志。一切结论只来自屏幕。
> - 不要修改仓库任何文件。
>
> 结束流程：完成后在 TUI 里按 q 退出，确认进程结束，然后 tmux kill-session -t usability-r2。若无法退出，记录为发现。
>
> 报告格式（最终消息完整输出，会被存档）：
> # Round 2 报告：听朋友介绍过
> ## 时间线
> 逐步记录：按了什么键 → 屏幕发生了什么（引用关键文案）→ 你的即时感受。保留完整键序。
> ## 朋友说法核对表
> | 朋友说的 | 实际体验 | 结论（属实/不符/部分） |
> ## 发现的问题
> 逐条编号，标注严重度（高/中/低），附最小复现键序。
> ## 好的方面
> ## 改进建议
> ## 总体可用性评分（1-10）与一句话结论

## Agent 报告（原文）

# Round 2 报告：听朋友介绍过

## 时间线
1. 启动应用后看到 Apple Music 首页，底部提示含“`Tab source`”。
2. 按 `Tab` → 切到 Radio，显示“`Favorites (0)`”，切换功能直观。
3. 按 `?` → 打开帮助；看到 `tab switch source`、`f favorite / unfavorite`、`/ Radio Search & Filters` 等说明。
4. 按 `Escape`，再按 `3` → 进入“`Popular Worldwide (20)`”默认电台列表。
5. 按 `/` → 打开“`Search & Filters`”；按 `Down Down Down Enter` 进入 Country；按 `Down`×8、`Enter` → 选中“`China`”。
6. 曾误按 `Right Enter` → 又打开国家选择器；按 `Escape` 返回。即时感受：确认按钮的焦点/操作方式不够明显。
7. 按 `Down Down Enter` → 应用筛选，显示“`Showing: China (19)`”，验证了按国家过滤。
8. 按 `Down`×3、`Enter` → 播放“`怀集音乐之声`”；约 3 秒后界面显示“`LIVE · playing · live stream`”。
9. 按 `Space` → 界面立即变为“`LIVE · paused · live stream`”；按 `f` → 电台项、播放区出现 `★`，并提示“`★ Favorited: 怀集音乐之声`”。
10. 按 `1` → 进入收藏页，但仍显示“`Favorites (0)`”和“`(empty)`”。等待 2 秒后仍为空，无法确认收藏列表中有该电台。
11. 按 `3` → 回到刚才的中国筛选结果；按 `Escape` → 成功回到“`Popular Worldwide (20)`”默认列表。
12. 按 `/`、输入 `jazz`、按 `Enter`、按 `Down`×5、`Enter` → 显示“`Showing: jazz (20)`”，结果包含多个 Jazz 电台，验证文字搜索。
13. 按 `Tab` → 回 Apple Music；再按 `Tab` → 回 Radio，双向切换正常。
14. 按 `q` 退出；tmux session 随程序退出自动消失。随后执行 `tmux kill-session -t usability-r2`，提示无 tmux server，未影响其他实例。

## 朋友说法核对表
| 朋友说的 | 实际体验 | 结论（属实/不符/部分） |
|---|---|---|
| Tab 能在 Apple Music 和网络电台之间切换 | `Tab` 可双向切换，视图和底部帮助均有提示。 | 属实 |
| 电台能收藏 | `f` 后有 `★ Favorited` 提示，播放区也显示星标；但收藏页仍为 0 条。 | 部分 |
| 能搜电台 | 搜索 `jazz` 后得到 20 条相关结果。 | 属实 |
| 搜索可按语言、国家过滤 | 搜索面板明确有 Language / Genre / Country；实际按 China 得到 19 条结果。 | 属实 |
| 能接上 Mac 媒体控制显示 | 未打开系统控制中心；应用内播放、暂停状态能同步清晰显示为 `LIVE · playing/paused`。 | 部分 |

## 发现的问题
1. **高：收藏成功提示与收藏列表不一致。**
   最小复现：`Tab` → `3` → 选择电台并 `Enter` → `Space` → `f` → `1`。
   现象：先出现“`★ Favorited: 怀集音乐之声`”，电台行和播放区均有 `★`，但收藏页显示“`Favorites (0)`”及“`(empty)`”。核心目标“收藏后从收藏列表找回”无法完成。

2. **中：筛选确认操作不够清晰。**
   最小复现：`/` → 选择 Country → 选择 China → 试图通过 `Right Enter` 确认。
   现象：会重新打开 Country 选择器；必须用 `Down Down Enter` 移到 Confirm 才能提交。键盘焦点不可见或不明显，首次使用容易迷路。

3. **低：Radio 收藏页与 Browse 的状态感不一致。**
   最小复现：收藏后 `1`，再 `3`。
   现象：Browse 中电台仍有 `★`，但 Favorites 页为空，用户难以判断收藏究竟是否生效。

## 好的方面
- `Tab` 切换快速，Apple Music 与 Radio 的顶部视图信息明确。
- 内置帮助覆盖了主要快捷键，能帮助首次用户探索。
- 搜索与国家筛选结果加载正常，结果标题“`Showing: China`”“`Showing: jazz`”清楚。
- 播放状态反馈即时；播放后快速暂停时，`playing` / `paused` 文案非常明确。
- `Escape` 能从筛选结果回到“`Popular Worldwide`”默认列表，恢复路径可发现。

## 改进建议
- 优先修复或明确收藏持久化/列表刷新逻辑，确保“已收藏”必然出现在 Favorites。
- 在筛选对话框显示当前焦点，或允许更直观的 `Enter` 提交，减少确认操作误触。
- 收藏成功后可自动刷新收藏页，或提供“查看收藏”的快捷入口。
- 在 Radio 收藏页为空时，如当前已有星标电台，可避免显示与状态矛盾的 `Favorites (0)`。

## 总体可用性评分（1-10）与一句话结论
**6/10**：电台浏览、搜索、筛选和播放反馈可用，但“收藏成功却无法在收藏列表找到”破坏了最关键的回访使用流程。

## 编排者复核

- 问题 #1 与 Round 1 独立复现同一根因（Favorites 缓存不失效），交叉验证成立，严重度维持「高」。
- 问题 #2 新增细节：用户尝试 `Right Enter` 提交（直觉性操作），结果重新打开 Country 选择器。`←→` 在字段行无操作且不提示，属真实困惑点。
- 附加观察：`Escape` 从 `Showing: China` 回默认列表的路径被自然发现并成功，验证 Esc-reset 设计有效。