# TUI 设计

本目录是 lilt 终端界面（`internal/tui`）的产品与呈现语义基线，也是改 TUI 时的规范入口。
wire 语义以 [`../client-api/README.md`](../client-api/README.md) 为准；本目录描述 client 侧
如何组织、呈现与响应。

| 文档 | 内容 |
|---|---|
| [`model.md`](model.md) | UI 模型：Source / Surface / Home、导航与 source 切换的不变量 |
| [`async-state.md`](async-state.md) | Bubble Tea command、watch sequence 与状态一致性 |
| [`design-system.md`](design-system.md) | 页面骨架、Band/Workspace、Header/Row 文法、theme token |
| [`ux.md`](ux.md) | 当前 TUI 布局、键位、反馈与弹层 |
| [`theme.md`](theme.md) | 主题 TOML schema 与语义 token 映射 |
| [`references.md`](references.md) | 设计参考：orbit / cliamp / cmus 的借鉴与取舍 |

面向使用者的操作指南见 [`../guides/tui.md`](../guides/tui.md)。
