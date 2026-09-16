# Round 32 · Footer spacing and frame hierarchy

- 日期：2026-09-16
- 触发：真实终端截图视觉审查
- 状态：完成；两项布局/色彩问题已修复

## 修复

1. **Footer 贴近终端底边**
   - 底部区域改为三行：状态/toast、footer、底部留白。
   - footer 上移一行，位于其区域中间；toast 出现也不移动播放 dock。

2. **持久面板标题前的 `──` 使用标题颜色**
   - 将 `──` 从 title style 拆出，归入 border style。
   - 仅标题文字使用 active/accent 或 dim title style。

## 实际终端验证

120×30 tmux ANSI capture：

```text
border: ┌──        (ANSI 37)
title:  FAVORITES  (ANSI bold cyan)
```

footer 后仍保留一整行空白；回归测试覆盖此位置。
