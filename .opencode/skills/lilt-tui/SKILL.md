---
name: lilt-tui
description: 修改 lilt 的终端界面（internal/tui：布局、组件、样式、交互、主题）时使用。加载 TUI 改动的工作流与不变量：权威规范加载顺序、固定 band 骨架、Header/Row 文法、键盘/鼠标激活语义、几何与测试同步清单、真机验证流程。不适用于 server、CLI 渲染或播放逻辑。
---

# lilt TUI 工作流

lilt 的 TUI（`internal/tui/`，Go + Bubble Tea + Lipgloss）是一个已经成型、有权威设计系统
的产品界面。本 skill 只服务一类任务：**在既有设计系统内修改或评审 lilt TUI**。技术栈固定，
不引入新 UI 依赖；不存在"从零生成 TUI"或"选框架"的问题。

## 何时用 / 何时不用

- 用：改布局、panel、row、Now Playing、overlay、footer、主题映射、鼠标/键盘交互、TUI 测试。
- 不用：CLI 渲染（`cmd/lilt/render.go`）、Client API、server/provider/播放逻辑、Swift helper——
  这些各自的权威文档在 `docs/`，与本 skill 无关。

## 第一步：按顺序加载权威规范（不得跳过）

| 顺序 | 文档 | 决定 |
|---|---|---|
| 1 | `docs/ui/model.md` | 产品语义：Source/Surface/Home、导航、`:` 面板（不可从像素反推） |
| 2 | `docs/ui/design-system.md` | 组件与视觉：band 骨架、Header/Row 文法、信息层级、theme token |
| 3 | `docs/ui/ux.md` | 已实现布局、键位、反馈与弹层 |
| 4 | `docs/ui/theme.md` | 主题 TOML → 语义 token 映射 |

规范与代码冲突时，先判断哪一方正确，修对的一方，并在交付说明中点名矛盾的双方。
参考项目（orbit/cliamp/cmus）的取舍见 `docs/ui/references.md`——借鉴布局决策，不照搬功能。

## 第二步：文档先行

任何视觉/交互改动都先落 `docs/ui/`，再动代码。顺序：

1. 在 design-system.md（组件语义）或 ux.md（已实现交互）写出新规则；
2. 画一张**定宽对齐**的 ASCII 目标稿（错位的 mockup 比没有 mockup 更糟），与用户确认；
3. 再动 `internal/tui/model.go`。

## 不可破坏的不变量

实现或评审时逐条对照；违反任何一条都是 bug：

- **Band 骨架固定**：`canvas.inset.top → identity → surface-nav → workspace → playback-gap →
  NOW PLAYING → feedback → footer → canvas.inset.bottom`。上下 inset 对称；nav 与 workspace
  面板**紧挨**，中间不留空行；行距固定为 1，任何组件不得插入空行。
- **feedback band 常驻**：异步消息只出现在该 band，绝不推动 workspace。
- **Header 文法封闭**：`TITLE` 或 `TITLE (N)`。loading/filter/buffering/来源名/搜索词永远不进
  header；上下文进正文首行，暂态进正文状态行或 feedback。
- **Row 文法统一**：`cursor/状态 marker 在样式之外`，样式只包住文本。播放高亮**优先于**选中
  （光标压在播放行上时保留播放色，选中由 `>` 表达）。
- **键盘与鼠标激活分离**：键盘 `Enter` 首次按下即激活；鼠标单击选中、同一行 500ms 内的连续
  两次点击构成一次双击激活（消耗后不可重复；非连续或超时的再次点击只是重新选中）。
- **鼠标几何与 View 同源**：`mouseTarget`/`handleClick`/`handleWheel` MUST 复用 `layout()` 的
  数字；点击映射必须减去 prefix/状态行数，滚动窗口与点击窗口用同一函数。
- **主题三层**：palette → semantic token → 组件；组件不得直接取 palette 色。状态不能只靠颜色
  （marker/glyph 文本同时表达）。
- **能力降级不伪造**：Radio 的 rail 显示"no finite queue"等 capability empty state，不得复用成
  别的面板或静默隐藏。
- **宽度按终端 cell 度量**：截断/填充一律走 `lipgloss.Width`/`fit`/`clip`，禁止 `len()` 或
  字节数；lilt 的内容大量是 CJK 歌名，任何字节级宽度计算都会错位。
- **键盘可达**：鼠标只能加速，不能成为任何操作的唯一入口；新增鼠标行为时先确认同一操作有键位。
- **事件循环不阻塞**：网络/磁盘/队列操作只通过 `tea.Cmd` 回来；渲染路径不得直接 I/O。
- **面板位置稳定**：异步内容（toast、probe、刷新）不得移动 band 位置；空间记忆是导航的一部分。

## 评审反射（对任何布局都先跑这两问）

1. **杂乱审计——把"忙"数出来**：边框嵌套几层（终端边到内容超过一层边框通常太多）；同一状态
   被几种信号重复表达（如 `[PASS]`+绿+勾+行标 = 4 次）；是否每行都带 marker（等于什么都没标）；
   chrome/标签/重复样板占了多少列。输出必须点名**删哪几个具体元素**，不许只说"简化"。
2. **地板压测**：80×24 与 60 列 tmux split 下，哪个面板赢、什么被隐藏、什么截断、何时出现
   "Terminal too small"。多栏设计的窄终端回退必须单一且明确（lilt：rail 消失、`0` 聚焦队列）。

lilt 的对照约束：可见条目密度高不是缺点（行距固定 1），杂乱审计对象是**边框、重复信号、
无用 marker**，而不是行密度本身。

## 第三步：改布局时的同步清单

几何常数（`consoleHeaderRows`/`nowBoxRows`/`minWorkspaceRows`…）被这些位置共享，改一处必须
全查：`layout()`、`consoleMinimum()`、`mouseTarget()`、`handleClick()`、`handleWheel()`、
`scrollQueue`/`scrollMainList`/`keepMainSelectionVisible`、`queuePanelRows`、
`listLines`/`queueLines`/`nowBody` 的 rows 参数，以及 `model_test.go` 中的点击坐标与窗口断言。

## 第四步：验证（缺一不可）

1. `go test -count=1 ./internal/tui` —— 行为测试先行；新组件必须带 hermetic 测试。
2. `just verify`（Go + race + vet + Swift + `git diff --check`）与 `just docs-check`。
3. 真机确认（mockup 与真实渲染可能有差）：

```bash
just build-go
tmux new-session -d -s lilt-ux -x 118 -y 30 "./lilt tui" && sleep 6
tmux capture-pane -t lilt-ux -p          # 检查 band、高亮、header、行宽
tmux kill-session -t lilt-ux
```

覆盖至少四种状态：无播放、finite queue 播放中（宽终端 rail）、窄终端（无 rail）、
"Terminal too small"（`consoleMinimum` 之下）；主题至少验证 `default`（ANSI 16 色）与一个
`#RRGGBB` 主题。地板压测：再跑一次 60 列宽度，确认布局按规范回退而不是错位换行。

## 常见坑（已踩过）

- `newModel` 的默认 `loading=true` 会在正文渲染 `refreshing…` 行——直接调
  `listLines`/`queueLines` 的单测必须显式 `m.loading=false`。
- 新增/删除 band 行会移动一切鼠标命中坐标；先改 `layout()`，再让测试按新几何算 y。
- 选中样式用 reverse 兜底（无 `selection` 色的主题），嵌套样式会截断行高亮——播放/选中行
  用纯文本 + 单层样式渲染，reset 只能出现一次。
- 语义只能来自 `model.md`/`design-system.md`；发现"旧式/兼容"渲染残留直接删除。
- 测试断言颜色时用转义序列（如 `\x1b[1;30;102m`），不要猜字段名。