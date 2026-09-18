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
| `text.muted` | 已播历史、hint、无数据说明 | 最低对比度，仍须可读 |
| `text.status` | Playing、Paused、Buffering、错误 | 与状态 token 组合，必须有文字/glyph |

通用列表 row 的顺序固定为：

```text
selection marker · kind marker · primary label · secondary metadata · state marker
```

- `selection marker` 表示键盘焦点；`playing marker` 表示当前播放。两者可同时存在，不能互相覆盖。
- 已播 queue entry 使用独立 glyph + muted text；当前 entry 使用 `>`/播放 glyph + playing token；
  后续 entry 使用 primary/secondary text。
- item kind glyph 只在混合列表中出现；同质列表不重复为每行加图标。

## 5. Now Playing 信息契约

`NOW PLAYING` 固定两行正文：身份行与播放事实行。它不显示 Source、queue count 或页面上下文。

```text
Track - Artist
state · elapsed · progress · duration · current-format? · modes?
```

| 信息 | 位置 | 显示条件 |
|---|---|---|
| 曲目、艺人 | 身份行 | 有 track 时必须显示 |
| 播放状态 | 事实行开头 | 始终显示；`playing`、`paused`、`buffering`、`stopped` 使用稳定文字/glyph |
| elapsed、progress、duration | 事实行 | finite item 有 duration 时显示；live stream 显示 `LIVE`，不伪造 duration |
| 当前实际编码 | duration 后 | 仅 `format` 是 helper 报告的实际值时，例如 `ALAC 24/48`、`AAC 256` |
| shuffle / repeat | 事实行末尾 | 只在启用时显示，使用短形式；例如 `S`、`R All`、`R One` |
| preview | 事实行 | 仅 preview mode 显示 `Preview` |
| playback error | feedback + 可见状态文本 | 不能只靠红色，完整可操作详情进入 Track Info |

`System-selected`、空值或“系统自动选择”的 format 不是实际编码，MUST NOT 在 Now Playing 中显示为
事实。`availableFormats` 是“可用变体”而非当前正在使用的变体，MUST 只在 Track Info overlay 中
以 `Available formats` 展示，不能替代当前 format。

## 6. Theme 到语义 Token

主题 TOML 继续使用既有 palette，不为组件添加猜测性配置键。renderer 必须先从 palette 派生
semantic token，再由组件消费 token；组件不得直接随意取 `accent`/`green`。

| Semantic token | 当前 palette 来源 | 用途 |
|---|---|---|
| `surface.background` | `bg` | canvas 与 panel 背景 |
| `surface.selection` | `selection`，缺失时 reverse | 键盘焦点 row |
| `text.primary` | `bright_fg` | 主内容 |
| `text.secondary` / `text.muted` | `fg` | metadata、数量、history、hint |
| `text.panel-title` / `accent` | `accent` | Header title、section、progress fill |
| `border` | `fg` | 所有静态 panel border |
| `state.playing` / `state.success` | `green` | 当前播放、成功 |
| `state.warning` | `yellow` | loading、warning |
| `state.error` | `red` | error、destructive action |
| `text.on-state` | `ActiveForeground(...)` | 有色背景上的可读文字 |

`progress.track` 从 `text.muted` 派生，`progress.fill` 从 `accent` 派生。单色终端必须仍能通过
glyph、文字和背景/reverse 区分 focus、playing、warning 与 error。

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
