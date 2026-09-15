# D2 · 帮助/信息弹层滚动与换行（Round 19）

## 问题（R13/R17）

60x12 等低高度终端下，帮助弹层被硬截断：内容不可达，且说明文字以 `…` 截断，用户无法完整读懂。

## 实现

1. **可滚动**：内容高于弹层时，按 `visible` 窗口切片；标题显示位置与操作
   （`Help · 3-12/23 · ↑↓/PgUp/PgDn scroll · Esc close`）。
2. **按键**：`↑↓`/`j`/`k` 逐行、`PgUp`/`PgDn` 翻页、`g`/`G`（Home/End）首尾、鼠标滚轮滚动；
   `Esc` 关闭；`q`/`Ctrl+C` 仍退出；其余键关闭（保留「任意键关闭」的便利）。
   每次打开帮助/信息重置滚动位置。
3. **自动换行**：帮助说明按词换行到对齐的续行（缩进 = 键名列宽 + 1），
   键名列宽按最长键动态扩展（`esc / backspace / h` 不再溢出），彻底消除 `…` 截断。
4. **布局复用**：新增 `helpOverlay(width, height)` 与 `helpScrollMax()`，
   按键与渲染共用同一套布局计算，避免两处公式漂移。

## 验证

- 单测：`TestSmallHelpScrolls`（`j` 滚动、百分比计数、`g/G`、`Esc`、重开复位、`q` 退出）、
  `TestHelpWrapsInsteadOfTruncating`（46 列下完整文本、无 `…`）、
  `TestSmallOverlayKeepsActionsVisible`（小窗帮助可滚动标题）。
- 真机 PTY 60x12：`1-10/23` → `j j` → `3-12/23` → `G` → `14-23/23`；
  帮助全文无 `…`（`no truncation`）。
- `just verify` 全绿（Go 测试/race/vet、Swift build+6 测试、`git diff --check`）。
- 用户复验 Round 19：7/10，可完整遍历并确认到达末尾；截断问题已即时修复。

## 决策记录

- 滚动仅在被裁剪时启用：内容适配终端时保持「任意键关闭」的快速关闭语义不变。
- 不额外加「已到底部」提示：`14-23/23` 已表达末尾信息（避免冗余）。

至此 Phase A/B/C/D 全部完成。