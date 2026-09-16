# Round 25 · Overlay exit clarity

- 日期：2026-09-16
- 重点：Help / Track Info 的关闭指令是否会误导用户退出整个 app
- 状态：完成；标题、行为、规范与回归测试已同步

## 发现

旧标题：

```text
Track Info · any other key closes · q quit
```

用户会自然把 `q quit` 理解为“退出这个弹层”；实际旧行为却是退出整个 lilt。这是高风险、低收益的快捷键例外。

## 修复

- 标题改为正向、单一的 `Track Info · Esc close`（Help 同理）。
- Help / Track Info 中：`Esc` 或 `q` 关闭弹层；`Ctrl+C` 仍退出 lilt。
- 保留其他普通键关闭弹层的快捷行为，但不再将其作为主要指令。

## 真实终端验证

在 100×24 隔离 fake TUI：播放 → `i` → 看到 `Track Info · Esc close` → `q`。结果回到播放界面，进程仍运行。
