# Spec: TUI 设计系统（目标）

> **状态：已实现**（当前 terminal renderer）。本文定义 lilt TUI 的视觉语言与组件语义。
> 产品语义以 [model.md](model.md) 为准，参考输入见 [references.md](references.md)。

## 1. 目的与边界

lilt 的终端界面由少数稳定组件组合，而不是由各个 surface 临时拼字符串。每个组件必须先有
明确语义，再使用主题 token 渲染。设计目标是：像 orbit 一样简洁、现代、低噪声，同时保留
lilt 的多 Source、有限队列和 Client API 约束。

- 不引入新 UI 依赖；实现继续使用 Bubbletea/Lipgloss。
- 不复制 orbit 的本地文件库、Buckets、EQ、visualizer 或 zen mode。
- 不以视觉装饰替代状态：颜色不是唯一状态载体，所有关键状态必须有稳定文本或 glyph。
- 不因数据可得就常驻显示；每项数据有固定的显示位置、优先级和缺失语义。

## 2. 页面骨架

宽终端的固定拓扑如下。`UP NEXT` 在工作区右侧，**不**属于底部播放区；`NOW PLAYING`
横跨全宽。

```text

  Apple Music                                                          lilt
  1 Home · > 2 Recent
  +-- RECENT (28) --------------------------------+ +-- UP NEXT (1/6) -------+
  | Recently Played Lists                          | | . previous track       |
  |   Adele Essentials                             | | > current track        |
  | Recently Played Songs                          | |   next track           |
  |   Track - Artist                               | |                        |
  +------------------------------------------------+ +------------------------+

  +-- NOW PLAYING ------------------------------------------------------------+
  | Track - Artist                                                           |
  | > Playing  2:42  ================================  4:30  ALAC 24/48  R A |
  +---------------------------------------------------------------------------+

  Ready
  enter open/play · p play · space pause · n next · v stop · / search · q quit

```

### 2.1 Band

从上到下只允许以下 band，不能由单个 view 私自插入或删除空行：

| Band | 行数 / 行为 | 语义 |
|---|---|---|
| `canvas.inset.top` | 宽终端 1 行；紧凑终端可折叠 | 与底部对称的外边距，不承载信息 |
| `identity` | 1 行 | 左侧为当前 Source/位置，右侧为品牌 `lilt` |
| `surface-nav` | 1 行 | 当前 Source 可用的 Surface；当前项有非颜色唯一标识 |
| `workspace` | 弹性高度，**紧贴 nav** | 主浏览区与 Up Next rail |
| `playback-gap` | 1 行 | workspace 与播放区的固定分隔 |
| `now-playing` | border + 2 正文行 | 横跨全宽的当前播放状态 |
| `feedback` | 1 行，始终保留 | toast、loading completion、错误；无消息时视觉静默 |
| `NOW PLAYING` 事实区 | 2 行，始终保留 | 播放事实；超出部分在区内换行 |
| `footer` | 1 行 | 当前可用操作与全局操作的快捷键 |
| `canvas.inset.bottom` | 宽终端 1 行；紧凑终端可折叠 | 与顶部对称的外边距 |

`feedback` 是独立组件，不是 bottom padding。它的固定高度保证异步消息不会推动 workspace；
空 feedback 行不得以装饰性内容填充。

### 2.1.1 行距与垂直节奏

- 终端内容行距固定为 **1**（相邻物理行），lilt 不在列表行之间、NOW PLAYING 行之间插入空行；
  更大的行距只能由终端自身的行高设置提供。
- nav 行与 workspace 面板上边框**紧挨**，中间不留空行；shell 与内容的边界由边框本身表达。
- `playback-gap` 保持 1 行，是 workspace 与 NOW PLAYING 之间唯一的垂直分隔。
- `NOW PLAYING` 正文为 border + 身份行 + 事实行（共 4 行）。
- 空行的唯一来源是 band gap 与 canvas inset；不得为任何组件随手插空行。

### 2.2 Workspace

- 宽终端采用两栏：`main` + 1 cell `panel-gap` + `queue rail`。
- `main` 始终承担 Home、Recent、Browse、Discover、搜索结果和 detail page；不得被 queue 或
  playlist rail 复用。
- `queue rail` 始终是 `UP NEXT`，只展示有限队列。对 Radio 或无有限队列的 Source，它显示
  明确的 capability empty state，例如 `Live radio has no finite queue.`，不得变成歌单栏或被
  静默隐藏。
- 窄终端只显示 main；有限队列仍由 `0` 聚焦或 `:queue` 打开。没有有限队列时这些动作必须说明
  原因，不得伪造空 queue。
- 具体 breakpoints 必须同时满足 main 可读宽度、queue 最小可读宽度和 `panel-gap`；不能只按
  终端宽度的固定百分比猜测。

## 3. Panel 与 Header

所有 box 都有左上 title。Header 是身份标签，不是状态栏。

```text
PanelHeader {
  title: fixed short label
  count: optional integer | fraction
}
```

### 3.1 Header 文法

```text
TITLE
TITLE (N)
TITLE (N/M)
```

允许：

```text
RECENT (28)
SEARCH (12)
UP NEXT (1/6)
NOW PLAYING
```

禁止：

```text
UP NEXT · 1/6 · QUEUE
RECENT · filter:rock · loading…
NOW PLAYING · buffering…
```

- `title` 必须是稳定、简短的对象名：`HOME`、`RECENT`、`BROWSE`、`DISCOVER`、`SEARCH`、
  `PLAYLIST`、`UP NEXT`、`NOW PLAYING`。
- `count` 是唯一可附在 title 后的信息，统一使用括号；它是 secondary text，不抢 title 的视觉
  重点。空间不足时先省略 count，永远保留 title。
- 歌单名、搜索词、Source 名、filter、可见窗口范围、测量进度、loading、working、buffering、
  错误和快捷键都不得进入 Header。
- 同一规则适用于 overlay：`HELP`、`TRACK INFO`、`SEARCH`、`CHOOSE LANGUAGE` 等稳定身份留在
  Header；`Esc close`、过滤词、滚动范围和选择提示进入正文末行或状态行。

### 3.2 Context 与暂态状态

- 具体页面上下文放在 main 正文首行。例如 `PLAYLIST (18)` 的第一正文行显示
  `Adele Essentials`；Search 的查询值显示在搜索 overlay/正文首行。
- content section 使用 section header，例如 `Recently Played Songs`。它不是 PanelHeader，不使用
  panel-title 的字体层级。
- loading、refreshing、buffering 与 error 进入对应正文、`NOW PLAYING` 状态行或 feedback band；
  它们永远不能改变 panel title 的身份。

## 4. 文本层级与 Row

终端无法可靠控制字体家族与字号；“字体规范”由字重、大小写、对比度、前缀与位置定义。

| Token | 用途 | 规则 |
|---|---|---|
| `text.brand` | `lilt` | 右侧品牌，仅 identity band 使用 |
| `text.navigation` | Source、Surface | Source 表示位置；active Surface 使用 marker + 强调 |
| `text.panel-title` | PanelHeader title | 大写、强对比、短文本 |
| `text.primary` | 曲名、歌单名、可选 row | 正文最高优先级 |
| `text.secondary` | 艺人、数量、技术摘要 | 比 primary 弱，不盖过主标签 |
| `text.muted` | 已播历史、hint、无数据说明 | 比 primary 弱；不得低到不可读（不叠加 faint） |
| `text.status` | Playing、Paused、Buffering、错误 | 与状态 token 组合，必须有文字/glyph |

通用列表 row 的顺序固定为：

```text
selection marker · kind marker · primary label · secondary metadata · state marker
```

- `selection marker` 表示键盘焦点；`playing marker` 表示当前播放。两者可同时存在，不能互相覆盖。
  主列表与 Up Next 使用同一个 `▶` 作为 playing marker，所以只靠文字与 glyph 也能读出播放状态。
  selection marker 固定为 accent 色的 `›`；它必须带主题 token，不能作为裸文字继承终端前景色。
- **填充行留白**：任何带底色的 row（playing、键盘焦点、选中项），底色内文字两侧各保留 1 格空白；
  底色不得顶到面板 border 或让截断省略号贴住文字。列表行的行宽预算必须把这份留白算进去
  （主列表与 Up Next 一致：cursor 2 + 留白 1 + 文本 + 留白 1 + 滚动条 1）。
- 键盘与鼠标的激活语义分离：键盘 `Enter` 首次按下即激活；鼠标遵循系统常识——单击选中，
  同一行在 `doubleClickWindow`（500ms）内的连续两次点击构成一次双击并激活（见 [ux.md](ux.md)），
  超时或非连续的再次点击只是重新选中。
- 已播 queue entry 使用独立 glyph + muted text；当前 entry 使用 `▶` + playing token（`green` 文字 +
  `surface.selection` 底色）；后续 entry 使用 primary/secondary text。
- item kind glyph 只在混合列表中出现；同质列表不重复为每行加图标。

## 5. Now Playing 信息契约

`NOW PLAYING` 正文为**身份行 + 事实区**，事实区**恒定预留 2 行**（border + 身份行 + 事实区 2 行 +
border，共 5 行）。它不显示 Source、queue count 或页面上下文。

```text
Track - Artist
state · elapsed · progress · duration · current-format? · modes?
(事实区第 2 行：上一行放不下时在此续行；放得下则留白)
```

事实区**在预留的 2 行内换行**（按终端 cell 宽度、保留样式），不截断；只有连 2 行也放不下时才在
第 2 行末省略。预留而不是按内容长高，是为了维持 §1 的骨架不变量：事实的变化（出现/消失、变长）
MUST NOT 推动 workspace。这与 `feedback` band「1 行始终保留、无消息时视觉静默」是同一个取舍。

| 信息 | 位置 | 显示条件 |
|---|---|---|
| 曲目、艺人 | 身份行 | 有 track 时必须显示 |
| 播放状态 | 事实行开头 | 始终显示；`playing`、`paused`、`buffering`、`stopped` 使用稳定文字/glyph |
| elapsed、progress、duration | 事实行 | finite item 有 duration 时显示；live stream 显示 `LIVE`，不伪造 duration |
| 当前实际编码 | duration 后 | 仅 `format` 是 helper 报告的实际值时，例如 `ALAC 24/48`、`AAC 256` |
| shuffle / repeat | 事实行末尾 | 只在启用时显示，使用短形式；例如 `S`、`R All`、`R One` |
| preview | 事实行 | 仅 preview mode 显示 `Preview` |
| playback error | feedback + 可见状态文本 | 不能只靠红色，完整可操作详情进入 Track Info |
| 授权受限提示 | 事实区 | 仅在当前 source 播放确实受限时显示；它是信号位，可操作细节（`:auth` 等）用最短形式，完整说明进 Account surface |

`System-selected`、空值或“系统自动选择”的 format 不是实际编码，MUST NOT 在 Now Playing 中显示为
事实。`availableFormats` 是“可用变体”而非当前正在使用的变体，MUST 只在 Track Info overlay 中
以 `Available formats` 展示，不能替代当前 format。

## 6. Theme 到语义 Token

主题 TOML 继续使用既有 palette，不为组件添加猜测性配置键。renderer 必须先从 palette 派生
semantic token，再由组件消费 token；组件不得直接随意取 `accent`/`green`。

| Semantic token | 当前 palette 来源 | 用途 |
|---|---|---|
| `surface.background` | `bg` | canvas 与 panel 背景 |
| `surface.selection` | `selection`，缺失时由 `bright_fg` 朝 `bg` 派生 | 键盘焦点 row、正在播放 row 底色 |
| `text.primary` | `bright_fg` | 主内容 |
| `text.secondary` / `text.muted` | `fg` | metadata、数量、history、hint |
| `text.panel-title` / `accent` | `accent` | Header title、section、progress fill |
| `border` | `fg` 朝 `bg` 混合派生 | 所有静态 panel border、scrollbar gutter |
| `state.playing` / `state.success` | `green` | 当前播放文字、成功 |
| `state.warning` | `yellow` | loading、warning |
| `state.error` | `red` | error、destructive action |

`progress.track` 从 `text.muted` 派生，`progress.fill` 从 `accent` 派生。单色终端必须仍能通过
glyph、文字和背景/reverse 区分 focus、playing、warning 与 error。

palette 色只以两种身份进入组件：文字前景，或 `surface.background` / `surface.selection` 底色。
任何 palette 色都不得被当作装饰性填充（例如用 `green` 铺一整行）。派生规则见
[theme.md](theme.md#语义-token-映射)。

输入控件（搜索、过滤、URL、命令面板）也 MUST 用 `text.primary` / `text.muted` / `accent` 上色：
Bubbles 的默认输入样式继承终端前景色，而终端前景色是相对**终端背景**选的，一旦 canvas 被主题
填充就会失效——浅色主题下搜索词完全不可见。
同一根因的变体都按同一条规则处理：**行内每个文本段 MUST 自带 token，不得依赖外层 style 延续**。
Lipgloss 的嵌套样式以 reset 结尾，会终结外层样式——光标 marker、收藏星、行首 glyph 之后紧跟的
裸文字都因此回到终端前景色，在被填充的 canvas 上消失。组件里出现"外层包一层 + 内层嵌套"时，
内层之后的所有段必须各自着色（`listLabel` 即按此实现），filled row 则改用无嵌套的 plain 形式。

## 7. 实现与验收

renderer 按组件推进：shell → PanelHeader → row → workspace rail → Now Playing → overlay/footer。
每一步都补 hermetic view tests；改动组件时 MUST 同步更新本页与 [ux.md](ux.md)。

验收至少覆盖：

1. Apple Music 宽终端：main + Up Next + 全宽 Now Playing。
2. Radio 宽终端：右 rail 的 finite-queue unavailable state。
3. 窄终端：无 rail，queue 通过现有导航进入。
4. Home、Recent、Search、playlist detail 的 Header 文法与正文 context。
5. playing、paused、buffering、preview、live、error 的 Now Playing 信息位置。
6. 当前 format、未知 format 与 available formats 的可信度区别。
7. 默认、gruvbox、tokyo-night 及无 `selection` 色主题；`just verify` 与 `just docs-check`。
