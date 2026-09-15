# Round 3 · 老用户日常流（seeded state）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r3*）
- 构建：63392ce 工作区（提交前快照）
- 评分：8/10
- 状态：v2 有效（v1 因测试装置失败作废，见下方说明）

## 编排者说明：v1 失败与重跑

v1 的种子 state 因编排者脚本错误（zsh glob 无匹配中止了 `&&` 链，heredoc 未执行）根本没写入，
agent 因此看到空收藏/空最近，v1 报告的「预置收藏为空」不是应用 bug。
重写种子（含 lastSource: radio）并经编排者亲自验证（启动即显示 `Favorites (3)`）后重跑为 v2。
v1 中仍然有效的观察（搜索确认流程偏长）在 v2 中再次出现。

## 编排者 Prompt（v2 全文）

> 你是一名真实用户测试员，扮演一个「用过 lilt 两周的老用户」。你要以真实用户的方式实际操作这个终端应用，走一遍日常使用流程。全程使用简体中文。
>
> 你的人设：你每天用 lilt 听网络电台，收藏了几个台，习惯 j/k 导航。你不知道最近版本改了什么（你以为界面跟两周前一样），但你会自然地重新探索。当前环境里你的 lilt 已经有：3 个收藏电台（SomaFM Groove Salad、BOX : Japan City Pop、SomaFM Drone Zone）和最近播放记录（SomaFM Groove Salad、BOX : Japan City Pop、SomaFM Lush），上次退出时停留在 Radio。
>
> 你的目标（模拟日常流程）：
> 1. 启动后尽快从收藏里播放 SomaFM Groove Salad。
> 2. 看一眼最近播放，确认它反映了你的历史。
> 3. 找一个新的爵士（jazz）电台试听一下（随意方式，搜索或浏览均可）。
> 4. 把主题从默认换成一个别的，并确保它被保存（你希望下次启动还是新主题）。
> 5. 顺手确认你现在常用的操作是否还顺手；如果哪里跟你记忆中的行为不一样，明确记录。
>
> 环境（严格按此操作，不要改）：
> - 应用目录：/Users/caiguo/Documents/workspace/github/lilt
> - 启动（不要重新编译，不要用 just；state 已预置好你的收藏/最近/上次来源）：
>   tmux new-session -d -s usability-r3 -x 110 -y 30 "cd /Users/caiguo/Documents/workspace/github/lilt && LILT_SOCKET=/tmp/usability-r3.sock LILT_STATE=/tmp/usability-r3-state.json LILT_LOG=/tmp/usability-r3.jsonl ./lilt tui"
> - 查看画面：tmux capture-pane -t usability-r3 -p
> - 发按键：tmux send-keys -t usability-r3 <键>。字母/符号直接写；特殊键用：Enter、Escape、Space、Up、Down、Left、Right、Tab、BSpace、t（小写 t 是你记忆里换主题的键，如果不对就自己找）。
> - 节奏：单发或小批量按键后 sleep 0.5-1s 再 capture；触发加载后等 2-4s。
> - 真实应用会真的出声音：两个电台（Groove Salad 和一个爵士台）各验证一下播放状态即可，总音频时间控制在 40 秒以内，播完就暂停。
> - 机器上可能有用户自己的 lilt 实例在跑。绝对不要碰其他 tmux session、不要 kill 任何 lilt/lilt-player 进程；只用你自己的 usability-r3 session。
>
> 铁律（真实用户模拟）：
> - 严禁阅读仓库源码、文档、git 历史、README。允许使用应用内任何屏幕信息（包括内置帮助）。
> - 严禁读取 /tmp 下的 lilt 日志。一切结论只来自屏幕。
> - 不要修改仓库任何文件。
>
> 结束流程：完成后在 TUI 里按 q 退出，确认进程结束，然后 tmux kill-session -t usability-r3。若无法退出，记录为发现。
>
> 报告格式（最终消息完整输出，会被存档）：
> # Round 3 报告：老用户日常流
> ## 时间线
> 逐步记录：按了什么键 → 屏幕发生了什么 → 你的即时感受。保留完整键序。
> ## 发现的问题
> 逐条编号，标注严重度（高/中/低），附最小复现键序。特别标注「与预期/记忆不符」的行为。
> ## 好的方面
> ## 改进建议
> ## 总体可用性评分（1-10）与一句话结论

## Agent 报告（v2 原文）

# Round 3 报告：老用户日常流

## 时间线
1. 启动 → 直接回到 Radio / Favorites，光标已在 `SomaFM Groove Salad`。感觉很顺手，没有迷路。
2. `p` → 约 3 秒后显示 `LIVE · playing`，Groove Salad 成功播放。
3. `p`、`2` → 以为 `p` 会暂停，但仍显示 playing；切到 Recent，看到：
   - SomaFM Groove Salad ★
   - BOX : Japan City Pop ★
   - SomaFM Lush  
   历史与我的使用习惯相符。
4. `Space`、`?` → Space 成功暂停；帮助明确说明 `space / c` 才是暂停/继续，`p` 只是播放。
5. `Escape`、`/`、`jazz`、`Enter`、`Down Down Down Down Down Enter` → 搜索到 20 个 jazz 电台。
6. `p` → 试听首项 `101 SMOOTH JAZZ`，显示 `LIVE · playing`。
7. `Space`、`t` → 暂停爵士台并打开主题选择器。
8. `j`、`Enter` → 主题保存为 `nord`，主界面底部明确显示 `Theme: nord`。
9. `Space` → 确认爵士台已暂停。
10. `q` → 应用退出；tmux session 随命令结束自动消失，后续尝试查看该 session 返回“no server running”，确认进程已结束。因 session 已自动销毁，无需再执行 kill-session。

## 发现的问题
1. **低｜与预期/记忆不符：`p` 不是播放/暂停切换。**  
   最小复现：`p` 播放 → `p`。  
   第二次 `p` 后仍是 playing；实际暂停键是 `Space`。帮助里有说明，但底部短提示仅写了 `p play`，老用户容易凭习惯误按。

2. **低｜主题选择器没有明显的当前主题/当前光标视觉标记。**  
   最小复现：`t`。  
   列表展示多个主题，但画面中不易判断当前主题和将要保存的选项；我按 `j Enter` 后才从底部 `Theme: nord` 确认结果。

## 好的方面
- 启动后准确恢复到 Radio 收藏页，且 Groove Salad 默认选中，日常开播非常快。
- 收藏和最近播放记录内容正确，收藏项有 ★ 标识。
- `/` 搜索爵士电台流程完整，结果有格式、国家和标签，选台判断方便。
- 播放状态明确显示为 `playing` / `paused`。
- 内置帮助覆盖全面，保留了熟悉的 `j/k` 导航。
- 主题保存后立即有 `Theme: nord` 反馈，保存结果清晰。

## 改进建议
- 在播放中的底部提示增加 `Space 暂停/继续`，或让 `p` 支持切换播放/暂停。
- 主题选择器应高亮当前光标，并标注当前已保存主题，例如 `✓ nord`。
- 主题保存后可提示“已保存，下次启动仍会使用”。

## 总体可用性评分（1-10）与一句话结论
**8/10**：核心听台、查历史、搜新台和换主题都顺畅，只有播放暂停键与老习惯不一致、主题选择反馈略弱。

## 编排者复核

- 「老用户从收藏开播」核心流 3 步内完成（启动即 Radio Favorites + 默认选中 Groove Salad + p），验证 lastSource/默认视图设计有效。
- 问题 #1（p 不切换暂停）成立，属键位语义设计取舍；footer `p play` 提示如实反映实现。
- 问题 #2 与 Round 1/2 的「焦点/高亮不明显」属同一根因：终端下仅靠加粗区分选中态不够醒目。
- v1 结论修正：带 lastSource: radio 的 state 启动即恢复 Radio，推翻 v1「总是从 Apple Music 启动」的观察。