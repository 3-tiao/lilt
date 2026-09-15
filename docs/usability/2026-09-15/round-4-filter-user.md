# Round 4 · 目的明确的找台用户（只用 filters）

- 日期：2026-09-15
- 终端：100x28（刻意不同于其他轮），隔离 state/socket/log（/tmp/usability-r4*）
- 构建：63392ce 工作区（提交前快照）
- 评分：6/10
- 状态：已完成；第三次独立复现收藏缓存问题；再次出现 p/暂停语义问题

## 编排者 Prompt（全文）

> 你是一名真实用户测试员，扮演一个「目的明确的找台用户」。你要以真实用户的方式实际操作这个终端应用。全程使用简体中文。
>
> 你的人设：你今晚想听日本的音乐电台。你听说这个 app 的搜索支持「按条件过滤」，你不关心具体哪个台，只要是日本的音乐台就行。你没用过这个 app，但你会读屏幕上的提示。
>
> 你的目标：
> 1. 不靠猜台名，用「过滤条件」（比如国家、语言、类型）找到日本的电台列表。
> 2. 从中挑一个看起来顺眼的播放确认。
> 3. 满意的话把它收藏，并且**确认收藏列表里能看到它**（这一点很重要，请务必验证）。
> 4. 结束收听后，回到「未过滤」的默认电台列表。
> 5. 试着再做一个组合查询：名字关键词 + 某个过滤条件同时生效（比如 jazz + 某国家），看结果是否符合直觉。
>
> 环境（严格按此操作，不要改）：
> - 应用目录：/Users/caiguo/Documents/workspace/github/lilt
> - 启动（不要重新编译，不要用 just）：
>   tmux new-session -d -s usability-r4 -x 100 -y 28 "cd /Users/caiguo/Documents/workspace/github/lilt && LILT_SOCKET=/tmp/usability-r4.sock LILT_STATE=/tmp/usability-r4-state.json LILT_LOG=/tmp/usability-r4.jsonl ./lilt tui"
> - 查看画面：tmux capture-pane -t usability-r4 -p
> - 发按键：tmux send-keys -t usability-r4 <键>。字母/符号直接写；特殊键用：Enter、Escape、Space、Up、Down、Left、Right、Tab、BSpace。
> - 节奏：单发或小批量按键后 sleep 0.5-1s 再 capture；触发加载后等 2-4s。
> - 真实应用会真的出声音：播放确认后尽快暂停，总音频时间控制在 30 秒以内。
> - 机器上可能有用户自己的 lilt 实例在跑。绝对不要碰其他 tmux session、不要 kill 任何 lilt/lilt-player 进程；只用你自己的 usability-r4 session。
>
> 铁律（真实用户模拟）：
> - 严禁阅读仓库源码、文档、git 历史、README。允许使用应用内任何屏幕信息（包括内置帮助，如果你猜得到怎么打开）。
> - 严禁读取 /tmp 下的 lilt 日志。一切结论只来自屏幕。
> - 不要修改仓库任何文件。
>
> 结束流程：完成后在 TUI 里按 q 退出，确认进程结束，然后 tmux kill-session -t usability-r4。若无法退出，记录为发现。
>
> 报告格式（最终消息完整输出，会被存档）：
> # Round 4 报告：目的明确的找台用户
> ## 时间线
> 逐步记录：按了什么键 → 屏幕发生了什么 → 你的即时感受。保留完整键序。
> ## 发现的问题
> 逐条编号，标注严重度（高/中/低），附最小复现键序。
> ## 好的方面
> ## 改进建议
> ## 总体可用性评分（1-10）与一句话结论

## Agent 报告（原文）

# Round 4 报告：目的明确的找台用户

## 时间线
1. 启动后看到 Apple Music 首页；按 `Tab` 切到 Radio。
2. 看到 Radio 的收藏页为空，并提示可按 `/` 搜索；按 `/`。
3. 搜索筛选框中按 `Down Down Down Enter` 进入国家选择，等待加载后输入 `Japan`，看到“Japan · 128 stations”，按 `Enter` 选中。
4. 返回筛选框后按 `Down Down Enter` 确认；得到“Showing: Japan (19)”列表。列表中的国家均为 Japan，且有 jpop、jazz、music 等类型，符合预期。
5. 按 `Down Down Down Down p` 播放 `Jazz Sakura (asia dream radio)`；底部显示 `LIVE · playing`，完成播放确认。
6. 按 `f` 收藏；条目和播放栏出现 `★`，并显示 `★ Favorited: Jazz Sakura...`。随后按 `p` 想停止，界面一度显示 buffering，之后仍为 playing。
7. 按 `1` 打开 Favorites 验证收藏；页面却显示 `Favorites (0)` 和 `(empty)`，找不到刚收藏的电台。即时感受：收藏成功提示与收藏列表不一致，目标无法完成。
8. 按 `?` 查看帮助，发现暂停应使用 `Space`；关闭帮助后按 `Space`，状态正确变为 `paused`。
9. 按 `3` 回到 Browse，按 `Escape` 清除筛选，回到 `Popular Worldwide (20)` 未过滤默认列表。
10. 按 `/ Enter jazz Enter` 输入关键词；再按 `Down Down Down Enter` 选择国家、输入 `France Enter`，最后按 `Down Down Enter` 确认。
11. 得到 `Showing: jazz · France (20)`：结果包括 Jazz Radio、FIP Jazz、TSF Jazz，且均为 France，组合查询符合直觉。
12. 按 `Escape` 回到 `Popular Worldwide` 默认列表；按 `q` 退出。会话已结束。

## 发现的问题
1. **高：收藏成功但收藏列表为空，无法验证或再次找到收藏。**  
   最小复现：`Tab / Down Down Down Enter Japan Enter Down Down Enter Down Down Down Down p f 1`  
   现象：电台行和播放区出现 `★`，并提示 `Favorited`；但 Favorites 页面显示 `Favorites (0)`、`(empty)`。

2. **中：播放控制的直觉不够清晰，`p` 不会暂停已播放电台。**  
   最小复现：播放电台后按 `p`。  
   现象：界面短暂显示 buffering，随后仍显示 playing；必须查看帮助才知道 `Space` 才是暂停。底部只写了 `p play`，缺少暂停快捷键提示。

## 好的方面
- `/` 打开搜索与筛选的入口很容易发现。
- 国家选择支持直接键入 `Japan` / `France`，不需要猜电台名。
- 筛选结果标题清楚展示条件，例如 `Showing: Japan`、`Showing: jazz · France`。
- 搜索词与国家过滤能同时生效，结果与用户直觉一致。
- `Escape` 能快速清除条件并回到全球热门默认列表。
- 播放状态 `playing` / `paused` 明确可见。

## 改进建议
- 修复或统一“★ 收藏状态”、成功提示与 Favorites 列表的数据来源，确保收藏后立即在列表可见。
- 在底部快捷键栏增加 `Space pause/resume`，或让 `p` 对当前播放项具备更直觉的播放/暂停切换。
- 收藏成功后可提供“查看收藏”的快捷操作或跳转提示，方便用户完成确认。

## 总体可用性评分（1-10）与一句话结论
**6/10：筛选找台和组合查询很好用，但收藏后无法在收藏列表确认，是阻断核心任务的严重问题。**

## 编排者复核

- 问题 #1 为收藏缓存 bug 的第三次独立复现（Round 1/2/4），根因同：`f` 不失效 `cache["radio/Favorites"]`。
- 问题 #2 与 Round 3 的 p 语义问题呼应（两次出现）：`p` 对正在播放的电台再次触发播放而非暂停，且 footer 仅显示 `p play`。注意 Round 4 的场景实际是 `p` 重新 play 当前台——行为可辩护但提示不完整。
- 组合查询（jazz + France）与 Esc 清过滤均工作正常且被自然发现。