# Round 1 · 完全新手（零提示）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r1*）
- 构建：63392ce 工作区（提交前快照，含全部本轮改动）
- 评分：6/10
- 状态：已完成；高严重度问题 #1 已经编排者复核确认为真实 bug

## 编排者 Prompt（全文）

> 你是一名真实用户测试员，扮演一个「完全不知道 lilt 是什么」的普通 Mac 用户。你要以真实用户的方式实际操作这个终端应用，评估它对新手的可用性。全程使用简体中文。
>
> 你的人设：你从没用过 lilt，也没有人给你介绍过。你只会打开它、看屏幕、试按键。你不知道任何快捷键，唯一的先验是：终端应用通常能猜到一些通用操作。
>
> 你的目标（按顺序尝试）：
> 1. 弄清楚这个 app 是干什么的。
> 2. 播放一个网络电台（能听到/看到正在播放即可，不需要听很久）。
> 3. 如果顺利，试着把你喜欢的电台「收藏」起来，并确认收藏生效。
>
> 环境（严格按此操作，不要改）：
> - 应用目录：/Users/caiguo/Documents/workspace/github/lilt
> - 启动（不要重新编译，不要用 just）：
>   tmux new-session -d -s usability-r1 -x 110 -y 30 "cd /Users/caiguo/Documents/workspace/github/lilt && LILT_SOCKET=/tmp/usability-r1.sock LILT_STATE=/tmp/usability-r1-state.json LILT_LOG=/tmp/usability-r1.jsonl ./lilt tui"
> - 查看画面：tmux capture-pane -t usability-r1 -p
> - 发按键：tmux send-keys -t usability-r1 <键>。字母/符号直接写（如 '/'、'a'）；特殊键用：Enter、Escape、Space、Up、Down、Left、Right、Tab、BSpace、C-c。
> - 节奏：单发或小批量按键后 sleep 0.5-1s 再 capture；触发加载后等 2-4s。真实网络和真实播放需要时间。
> - 这是真实应用：会真的出声音。播放验证后尽快暂停或离开播放状态，总音频时间控制在 30 秒以内。
> - 机器上可能有一个用户自己的 lilt 实例在跑。绝对不要碰其他 tmux session、不要 kill 任何 lilt/lilt-player 进程；只用你自己的 usability-r1 session。
>
> 铁律（真实用户模拟）：
> - 严禁阅读仓库源码、文档、git 历史、README——真实用户看不到这些。允许使用应用内任何屏幕信息（包括内置帮助，如果你猜得到怎么打开）。
> - 严禁读取 /tmp 下的 lilt 日志。一切结论只来自屏幕。
> - 不要修改仓库任何文件。
>
> 结束流程：测试完成后在 TUI 里按 q 退出（若按不出 quit 也要如实记录怎么退出的），确认进程结束（pane 变化或 capture 为空），然后 tmux kill-session -t usability-r1。如果应用卡死或无法退出，把这也当成一个发现记录下来。
>
> 报告格式（最终消息完整输出，会被存档）：
> # Round 1 报告：完全新手
> ## 时间线
> 逐步记录：按了什么键 → 屏幕发生了什么（引用关键文案）→ 你的即时感受。保留完整键序。
> ## 发现的问题
> 逐条编号，标注严重度（高/中/低），附最小复现键序。包括：困惑点、死路、错误信息、与你预期不符的地方。
> ## 好的方面
> ## 改进建议
> ## 总体可用性评分（1-10）与一句话结论

## Agent 报告（原文）

# Round 1 报告：完全新手

## 时间线
1. 启动后等待加载。看到顶部有 `Apple Music`、`Radio`，首页展示“Recently Played”和歌单；底部写着 `? help · q quit · Tab source`。
   - 感受：推测这是一个同时支持 Apple Music 和网络电台的终端播放器，但没有直接说明。

2. 按 `?`。看到完整帮助，包括“switch source (Apple Music / Radio)”“`a` add a stream URL to Favorites and play it”“`f` favorite / unfavorite”。
   - 感受：帮助很实用，确认了应用用途和主要能力。

3. 按 `Escape`、`Tab` 切到 Radio。看到 `Favorites (0)` 及提示：`(empty) — press a to add a stream URL, / to search stations`。
   - 感受：知道可以搜索电台，下一步很明确。

4. 按 `/`、`Enter`、输入 `rock`、`Enter`，然后按 `Down Down Down Down Enter` 确认搜索。等待加载。
   - 看到 `Showing: rock (20)`，列出多个网络电台。
   - 感受：搜索成功，但确认按钮没有明显焦点标识；我是靠尝试向下移动多次才提交的。

5. 按 `Down Down p` 选择并播放 `Rock Antenne`。等待后看到：
   - `Now Playing · LIVE`
   - `Rock Antenne`
   - `LIVE · playing · live stream`
   - 感受：明确确认已开始播放网络电台。

6. 按 `f` 收藏。屏幕显示：
   - 列表项：`Rock Antenne ★`
   - 播放区：`★ Rock Antenne`
   - 提示：`★ Favorited: Rock Antenne`
   - 感受：看起来收藏成功。

7. 按 `Space` 暂停，看到 `LIVE · paused · live stream`。按 `1` 进入 Favorites，等待 2 秒。
   - 却看到：`Favorites (0)` 和 `(empty) — press a to add a stream URL...`。
   - 感受：收藏提示和星标都出现了，但收藏列表为空，无法确认收藏真正生效。

8. 按 `q` 退出。随后 capture 返回 `no server running`，说明该会话已随应用正常结束；再执行 `tmux kill-session -t usability-r1` 也显示同样信息。

## 发现的问题
1. **高：收藏操作显示成功，但 Favorites 列表为空。**
   - 最小复现：`Tab` → `/` → `Enter` → 输入 `rock` → `Enter` → `Down Down Down Down Enter` → `Down Down p` → `f` → `1`。
   - 现象：显示 `★ Favorited: Rock Antenne`，且播放区和搜索结果都有星标；进入 `Favorites` 后却是 `Favorites (0)` 和 `(empty)`。
   - 影响：新手无法确认收藏是否保存，核心目标失败。

2. **中：搜索筛选弹窗没有清晰可见的当前焦点。**
   - 最小复现：Radio 页面按 `/`，输入搜索词后回到“Search & Filters”。
   - 现象：必须依赖底部“↑↓/Tab move”说明，并通过多次 `Down` 猜测移动到 `[ Confirm ]`；屏幕中没有足够明显的选中态。
   - 影响：容易卡在已输入关键词却不知道如何提交的步骤。

3. **低：首次进入应用缺少一句直接的用途说明。**
   - 最小复现：启动应用。
   - 现象：首页直接展示 Apple Music 内容；需要观察顶部来源或主动按 `?` 才能理解它也能播放 Radio。
   - 影响：完全不了解 lilt 的用户需要自行推断产品定位。

## 好的方面
- 底部快捷键提示始终可见，`Tab` 切换来源、`/` 搜索、`p` 播放、`Space` 暂停、`q` 退出都可发现。
- `?` 帮助完整且可读，涵盖播放、搜索、收藏和退出。
- Radio 搜索真实可用，关键词 `rock` 得到 20 个结果。
- 播放状态反馈明确，从 `buffering` 变为 `playing`，并标明 `LIVE · live stream`。
- `q` 正常退出，无需强制终止。

## 改进建议
1. 修复电台搜索结果的收藏写入/收藏列表同步问题，并在 Favorites 中立即显示刚收藏的电台。
2. 为搜索弹窗当前选项增加明显的高亮、光标或 `>` 标记。
3. 启动页或页脚增加简短定位，例如“Apple Music 与网络电台终端播放器”。
4. 收藏成功后提供更强的可验证反馈，例如“已收藏，可在 1 Favorites 查看（共 1 项）”。

## 总体可用性评分（1-10）与一句话结论
**6/10**：找台和播放过程较顺畅，但“收藏成功”与空收藏列表相矛盾，破坏了新手对核心功能的信任。

## 编排者复核

- **问题 #1 确认为真实 bug（高）**：state 文件确实写入了 Rock Antenne 收藏（/tmp/usability-r1-state.json），日志显示 `f`（Browse, 03:44:40）→ `1`（03:44:52）→ navigate Favorites。根因：会话启动时 Radio 默认进入 Favorites 并把空列表写入 `cache["radio/Favorites"]`；在其他视图 `f` 收藏后未失效该缓存，回 Favorites 读到旧缓存。
- 问题 #2 部分成立：选中态有加粗高亮但不够醒目；agent 实际按了 4 次 Down 落在 `Reset filters`（无操作），靠再按才提交——低可见度选中态会放大 off-by-one。
- 问题 #3 成立（低，产品层面）。