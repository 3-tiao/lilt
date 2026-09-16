# Round 21 · Console layout usability recheck

- 日期：2026-09-16
- 终端：120×32、110×30、30×8（隔离 tmux / state / socket / log）
- 构建：`feat/console-layout` 工作区
- 模式：假播放器验证 Apple Music 布局；真实 Radio directory 验证大列表导航
- 状态：完成；本轮发现的 3 个问题均已修复并有回归测试

## 目标

验证新的固定底部 dock：不遮挡浏览、不因状态变化跳动、Apple Music 队列仍可操作，且在小终端不出现破损边框。

## 实际操作与结果

1. **Apple Music，120×32**：进入 Playlists → 打开歌单 → Enter 播放。
   - 主列表保持原位置。
   - 底部左侧显示曲名、进度和 `Playing · Preview · AAC preview`。
   - 右侧显示 `UP NEXT · 1/2 · FAKE LIBRARY PLAYLIST`，当前曲目有 `▶` 标记。
   - 按 `0` 后，队列光标显示 `>`；`j` 移动光标，`K` 重排曲目；footer 显示相应操作。

2. **Radio，110×30**：Tab → Browse，等待目录加载后，在 100 项电台列表发送连续 `jjj`。
   - 修复前：终端将其交给 TUI 作为单个 `"jjj"` 事件，光标完全不动。
   - 修复后：事件被拆为三个 `j`，光标从 `RTL` 走到第 4 项 `RMC FR`。

3. **小终端，30×8**：播放状态下缩放窗口。
   - 修复前：Now Playing 只渲染上边框，底边被截断，像损坏的 UI。
   - 修复后：稳定显示 `Terminal too small — resize`；按 `?` 仍可打开可滚动的 Help。

4. **长队列信息密度**：dock 只能显示约 6 行队列时，标题现在会额外显示可见范围，例如 `Up Next · 13/48 · 10-15 shown · Playlist`。
   - 48 项队列标题有单元测试覆盖；假播放器 fixture 只有两首歌，无法在独立 TUI 会话中构造同样的长队列。

## 本轮发现与修复

1. **中：快速 `j`/`k` 或按键自动重复会丢失导航。**
   - 最小复现：在 Radio Browse 中一次发送 `jjj`。
   - 原因：Bubble Tea 可把多个普通字符合并为一个 `KeyMsg`，旧逻辑只识别单字符。
   - 修复：非粘贴的多字符事件逐个重放；Bracketed Paste 保持原样，不会触发快捷键。

2. **中：30×8 等小窗口渲染半截播放框。**
   - 最小复现：播放中 resize 到 30×8。
   - 原因：旧的 `24×8` 下限小于“header + 列表 + dock + 状态 + footer”所需高度。
   - 修复：console 页面要求最小 44 列、13 行；不足时显示 resize 提示。Help/Filters 弹层仍可在较小终端中滚动。

3. **低：长队列在短 dock 内看不出可见范围。**
   - 修复：队列超出可见行数时，在标题显示当前窗口范围。

## 结论

新的固定底部 dock 在 Apple Music、Radio、队列焦点和小窗口下均可用；本轮无遗留 blocker。

## 验证

```text
go test ./...
go vet ./...
git diff --check
```
