---
name: lilt-tui
description: 修改或评审 lilt 终端界面（internal/tui：布局、组件、样式、交互、主题）时使用。提供规范加载顺序、改动工作流（文档 → mockup → 实现 → 验证）与核心设计原则；具体规则以 docs/ui/ 为准。不适用于 server、CLI 渲染或播放逻辑。
---

# lilt TUI 工作流

lilt 的 TUI（`internal/tui/`，Go + Bubble Tea + Lipgloss）已成型且有权威设计系统。本 skill
只服务一类任务：**在既有系统内修改或评审**。技术栈固定，不引入新 UI 依赖。

## 边界

- 用：TUI 的布局、组件、样式、交互、主题、测试。
- 不用：`cmd/lilt/render.go`、Client API、server/provider、Swift helper——各有其权威文档。

## 权威规范：先读，再动手

| 文档 | 决定 |
|---|---|
| `docs/ui/model.md` | 产品语义（Source/Surface/Home、导航）与反模式 |
| `docs/ui/design-system.md` | band 骨架、Header/Row 文法、信息层级、theme token |
| `docs/ui/ux.md` | 已实现布局、键位、反馈与弹层 |
| `docs/ui/theme.md` | 主题 TOML → 语义 token |

`docs/ui/` 是 UI 语义的单一真值，不是代码或像素。规范与代码冲突时修对的一方，并在交付说明中
点名矛盾的双方。

## 工作流

1. **文档先行**：新规则先写入 design-system.md 或 ux.md。
2. **Mockup**：定宽 ASCII，描绘真实 band 骨架，标注每行语义；错位的 mockup 比没有更糟。
   与用户确认后再动代码。
3. **实现**：复用既有组件及其文法，不从零发明。
4. **验证**：`go test -count=1 ./internal/tui` → `just verify` + `just docs-check` → 真机
   `tmux capture-pane` 截图（宽屏 / 窄屏 / 无播放 / 播放中 / too-small）。

## 核心原则（原则 + 示例）

1. **不发明文法**：新 UI 沿用既有组件文法。示例：面板标题只有 `TITLE (N)`；页面上下文进正文
   首行，loading/错误进状态行或 feedback band，永不进 header。
2. **一致性高于局部最优**：同一语义只有一种渲染。示例：row 的 cursor/状态 marker 在样式外，
   播放高亮优先于选中；重复出现的结构抽成组件而非复制。
3. **键鼠语义分离且遵循平台常识**：示例：键盘 `Enter` 首次按下即激活；鼠标单击选中、同一行
   500ms 内的连续两次点击才构成双击激活。
4. **几何与状态单一来源**：`layout()` 是唯一几何来源，鼠标命中、滚动窗口与测试坐标都从它
   派生——改一处必须全链路推导消费者。渲染只读 Model；播放/队列/capability 只来自 server
   快照，UI 只持有 transient 导航状态。
5. **主题是三层映射**：palette → semantic token → 组件；组件不直接取 palette 色，状态不只用
   颜色表达。
6. **宽度按终端 cell 度量**：一律用 `lipgloss.Width`/`fit`/`clip`；CJK 歌名会让字节级计算
   直接错位。
7. **结论先行、可验证**：改动表述为"把 X 改成 Y，因为 Z"并引用规范条款；视觉改动附
   before/after；确认过的 mockup 是契约，偏离时修 mockup 或修代码，二选一并说明。

## 评审反射（对任何布局都先跑）

- **杂乱审计**：把"忙"数出来——边框嵌套层数、同一状态的重复信号、每行都有的 marker；输出要
  点名删哪几个具体元素，而非"简化一下"。
- **地板压测**：80×24 与 60 列 tmux split 下谁赢、什么隐藏、何时出现 "Terminal too small"；
  多栏设计必须有单一明确的单栏回退。
- 行密度本身不是问题（行距固定 1）；审计对象是边框与重复信号。

## 已知陷阱（示例，非清单）

- 单测直接调用渲染函数时，注意 `newModel` 默认 `loading=true` 会多出一行正文。
- 嵌套样式会截断行高亮：播放/选中行只允许一次 reset。
- 语义只能来自 `docs/ui/`；发现旧式/兼容渲染残留直接删除。
