---
name: tui
description: 仅在修改或评审 lilt 终端界面（internal/tui：布局、组件、样式、交互、主题、测试）时使用。提供规范加载顺序、文档优先工作流与核心设计原则；具体规则以 docs/ui/ 为准。不适用于独立的 server、CLI 渲染或播放逻辑。
---

# lilt TUI 工作流

lilt 的 TUI（`internal/tui/`，Go + Bubble Tea + Lipgloss）已成型且有权威设计系统。本 skill
只服务一类任务：**在既有系统内修改或评审**。技术栈固定，不引入新 UI 依赖。

## 边界

- 用：TUI 的布局、组件、样式、交互、主题、测试。
- 不用：`cmd/lilt/render.go`、Client API、server/provider、Swift helper——各有其权威文档。
- TUI 改动若触及 Client API 或 server 行为，同时遵循其权威规范；本 skill 只约束 TUI 一侧，不能
  以像素或本地 UI 状态取代服务端契约。

## 代码布局

按主题定位，不要只往 `model.go` 里加：`model.go`（类型、消息、`Update` 路由、`Run`）、
`input.go`（键鼠、弹层、palette）、`views.go`（布局与所有渲染）、`effects.go`（Provider/Player/Remote
调用与 `tea.Cmd`）、`navigation.go`（页面栈、游标、AppState 投影）、`radio.go`（电台发现与探测）、
`renderer.go`（主题渲染器）。测试按同一主题分文件。

## 权威规范：先读，再动手

| 文档 | 决定 |
|---|---|
| `docs/ui/model.md` | 产品语义（Source/Surface/Home、导航）与反模式 |
| `docs/ui/async-state.md` | Bubble Tea command、watch sequence 与状态一致性 |
| `docs/ui/design-system.md` | band 骨架、Header/Row 文法、信息层级、theme token |
| `docs/ui/ux.md` | 已实现布局、键位、反馈与弹层 |
| `docs/ui/theme.md` | 主题 TOML → 语义 token |

`docs/ui/` 是当前 TUI 产品与呈现语义的基线，不从代码或像素反推；wire 语义仍以
`docs/client-api/` 为准。规范不是不可挑战的教条：发现更优方案时，先向用户说明收益、代价与
影响并请求确认；确认后先更新规范再实现。未经确认不得静默偏离，规范与代码冲突时须点名双方。

## 工作流

1. **文档先行**：改变设计或交互规则时，先更新 design-system.md 或 ux.md；仅修复既有规则的实现
   不重复文档。
2. **Mockup**：影响可见帧的改动先用定宽 ASCII 描绘真实 band 骨架并标注行语义；错位的 mockup
   比没有更糟。存在设计取舍时用它向用户确认，否则直接按规范实现。
3. **实现**：复用既有组件及其文法，不从零发明。
4. **验证**：文档改动交文档工程师编写、文档测试工程师按
   [docs-maintenance](../docs-maintenance/SKILL.md) 审阅；代码改动先跑相关 hermetic 测试，再按仓库门禁跑
   `just verify`。视觉改动在可用的隔离环境中以 `tmux capture-pane` 对照
   mockup，覆盖宽屏、窄屏与 too-small，而不依赖真实账户或网络。

## 核心原则（原则 + 示例）

1. **不发明文法**：新 UI 沿用既有组件文法。示例：Header 只承担稳定身份与可选计数；页面上下文
   进正文首行，loading/错误进状态行或 feedback band，永不进 Header。
2. **一致性高于局部最优**：同一语义只有一种渲染。示例：row 的 cursor/状态 marker 在样式外，
   播放高亮优先于选中；重复出现的结构抽成组件而非复制。
3. **键鼠语义分离且遵循平台常识**：示例：键盘 `Enter` 首次按下即激活；鼠标单击选中，只有
   文档定义的连续点击才激活，鼠标不能成为任何操作的唯一入口。
4. **几何与状态各有单一来源**：布局计算统一产出边界，鼠标命中、滚动窗口与测试坐标从同一边界
   派生。渲染保持纯粹，I/O 通过 `tea.Cmd` 返回；播放/队列/capability 只来自 server 快照，
   UI 只持有 transient 导航状态。
5. **主题是三层映射**：palette → semantic token → 组件；组件不直接取 palette 色，状态不只用
   颜色表达。
6. **宽度按终端 cell 度量**：使用终端宽度感知的工具（如 `lipgloss.Width`），不用字节数；
   CJK 歌名是必须覆盖的代表用例。
7. **状态也是界面**：不能只设计 happy path；同一组件的 loading、empty、error 与 unsupported
   capability 都应有明确归属。例如无 finite queue 是 rail 的能力空状态，不是隐藏错误。
8. **结论先行、可验证**：改动表述为"把 X 改成 Y，因为 Z"并引用规范条款；视觉改动附
   before/after；确认过的 mockup 是契约，偏离时修 mockup 或修代码，二选一并说明。

## 评审反射（对任何布局都先跑）

- **杂乱审计**：把"忙"数出来——边框嵌套层数、同一状态的重复信号、每行都有的 marker；输出要
  点名删哪几个具体元素，而非"简化一下"。
- **地板压测**：以 80×24 和约 60 列的 tmux split 为代表，说明谁赢、什么隐藏、何时进入
  too-small；多栏设计必须有单一明确的单栏回退。
- 不要把高密度本身当作杂乱；lilt 的审计对象是无效边框与重复信号。
