# Spec: UX（布局、导航、键位）

## 布局

```
 SOURCE   Apple Music    Radio                       ← Tab 切换 Source
 VIEW     1 Home   2 Playlists   3 Favorites   4 Recent   5 Presets ← 1..n 选择子视图
 ┌──────────────────────────┐ ┌ Up Next · Queue ──┐
 │ > 2001 年摇滚金曲        │ │ ▶ In the End      │
 │   喜爱歌曲               │ │   Numb            │
 │   经典摇滚代表作品       │ │   ...             │
 └──────────────────────────┘ └────────────────────┘
 ┌ Now Playing ────────────────────────────────────┐
 │ ▶ In the End — LINKIN PARK                      │
 │ [████░░░░] 1:23 / 3:39   full · shuffle         │
 └─────────────────────────────────────────────────┘
 ? help · Tab source · 1-9 view · enter open/play · q quit
```

- Apple Music 有活动队列且终端宽度足够时，浏览列表保持在主区域，右侧常驻
  **Up Next** 面板。`0` 将焦点切至/切回面板；焦点在面板时其有独立光标，主列表不移动。
  窄终端则在焦点进入后以主区域全页显示队列。
- **搜索不是视图**：Apple Music 的 `/` 打开搜索输入，结果作为可返回的临时列表
  （`Esc`/`Backspace` 返回），按 `Songs`/`Playlists` 分组。Radio 的 `/` 打开统一的
  Search & Filters 菜单。
- `Now Playing` 是只读状态带：Apple 队列播放时显示来源列表、当前位置和 `0 Up Next`。
  弹层（帮助/信息/主题）覆盖在中央。
- Apple Music Home 是线性分组页（非卡片）：Continue Playing、Recently Played、Quick Start、Your Playlists；空分组不显示。没有 Music User Token 或推荐数据时仍可用。
- Radio 只有 Favorites、Recent、Browse 三个顶层视图；每次进入 Radio 默认显示 Favorites。
- 底部只显示当前焦点的主要操作：根页显示 Source/View 导航；输入框显示提交/取消操作；Apple 歌单详情显示
  顺序播放、随机播放、从选中曲播放；Up Next 焦点显示编辑操作。`?` 显示完整键表。

## 键位

### 窗口与导航
| 键 | 作用 |
|---|---|
| `Tab` / `Shift+Tab` | 切换 Source（Apple Music / Radio） |
| `1`–`9` | 选择当前 Source 的子视图（Apple Music 记住上次视图；Radio 每次进入 Favorites） |
| `[` / `]` | 循环子视图 |
| `j`/`k`、`↑`/`↓` | 移动选择 |
| `g`/`G` | 顶部 / 底部 |
| `Ctrl+d`/`Ctrl+u` | 半页 |
| `Ctrl+f`/`Ctrl+b` | 整页 |
| `Enter` | 打开（歌单详情/子节点）或播放 |
| `p` | 播放当前项；光标在「正在播放的项目」上时切换暂停/继续；Apple 歌单详情 `p` 从首曲顺序播放（光标在当前曲目时同样切换暂停），`s` 随机播放，`Enter` 从选中曲开始 |
| `x` | 仅在 Up Next 获得焦点时移除选中的队列项；主列表中无操作 |
| `Esc` / `Backspace` / `h` | 返回上级 / 清除过滤；Browse 有查询时一键恢复 `Popular Worldwide` |

**文本优先规则**：当前可见控件接受文本时，所有可打印字符均为文本（包括
`j`/`k`/`h`/`l`、`q`、`?`、`/`），选项使用方向键移动。其他普通列表/菜单使用
`j`/`k` 垂直移动；适用时 `h` 返回/离开上下文，`h`/`l` 选择可见的横向操作。

### 播放
| 键 | 作用 |
|---|---|
| `Space` / `c` | 暂停 / 继续 |
| `n` / `b` | 下一首 / 上一首（仅 Apple Music） |
| `v` | 停止 |
| `s` | shuffle 开关；Apple 歌单详情为随机播放整个歌单（仅 Apple Music） |
| `R` | repeat 循环（off → all → one） |
| `e` / `E` | 插到下一首 / 追加队列（仅 Apple Music） |

### 内容与视图
| 键 | 作用 |
|---|---|
| `/` | Apple Music 文本搜索；Radio Search & Filters（可在任意 Radio 页面打开，Confirm 后统一落到 Browse） |
| `F` | 仅过滤当前 Apple Music 列表；Radio 中无操作 |
| `f` | 收藏 / 取消收藏当前项（含电台）；Apple Music 侧是 lilt 本地列表（视图标题 `Favorites · local`），与 Apple Music 官方「喜爱歌曲」不同步 |
| `0` | 聚焦/取消聚焦 Up Next（enter/p 跳转 · x 移除 · J/K 重排 · c 清空） |
| `a` | 添加电台 URL 到 Favorites 并立即播放 |
| `t` | 主题选择器 |
| `i` | 曲目信息弹层 |
| `?` | 帮助弹层 |
| `q` / `Ctrl+C` | 退出 |

## 状态反馈

- **loading**：首次列表加载时标题和内容区显示 `loading…`；已有项目刷新时标题显示 `refreshing…`，列表保持可读、可操作。Radio Browse 追加下一页时标题显示 `loading more…`，已加载列表保持可读、可操作。本地 Presets、Favorites 和 Radio Recent 直接显示，不闪烁 loading。
- **busy**：任何异步播放或队列操作进行中，Now Playing 显示 `working…`；这与列表加载、player `buffering…` 和 Radio 行探测状态相互独立。
- **进度**：非 live 且播放中的曲目在 helper 状态快照之间按本地时间插值（不修改快照），到时长为止；`buffering…` 时冻结在最后位置并显示中间状态，状态请求不重叠。
- **鼠标**：滚轮滚动主列表或 Up Next（在面板上滚动会聚焦面板）；左键点击选择行，再次点击执行（打开/播放/跳转）。首次点击保持当前列表窗口，不会把被点击行滚走，因此同一位置的第二次点击始终执行同一项目；点击 SOURCE/VIEW 标签切换来源与视图；有弹层时点击关闭。
- **toast**：操作反馈（已收藏/已入队/已删除/错误）自动消失（成功 ~4s，错误 ~5s，红色）。
- **空状态**：给出下一步提示（如 `(empty) — press a to add a stream URL`），而不是只显示 `(empty)`；列表加载错误优先显示错误，不能同时显示正常空状态建议。
- **弹层**：帮助和信息弹层以 `Esc close` 明确提示；`Esc`/`q` 关闭弹层，`Ctrl+C` 才退出 lilt，其他普通键也可关闭。内容高于终端时自动变为可滚动（标题显示 `1-10/23`、`↑↓ scroll · Esc close`），`↑↓`/`j`/`k`、`PgUp`/`PgDn`、`g`/`G` 与鼠标滚轮滚动；主题弹层使用
  `j`/`k`、方向键或 `Tab`/`Shift+Tab` 预览、`Enter` 保存、`Esc` 取消、`q` 退出。
- **选中态**：所有可选列表（Browse 主列表、Search & Filters 菜单、facet 选项、Theme）与顶栏 SOURCE/VIEW 都使用非颜色标记表达当前项：列表行前缀 `›`，激活标签用方括号 `[Radio]`、`[2 Recent]`（仅加粗在无色终端不可辨）。
- **Radio Search & Filters**：菜单分为 Search（仅 Text）与 Filters（Language、Genre、Country、Reset filters）。Search text 是支持 Unicode 的可见文本输入；各 facet 使用可搜索的引导列表，`Any` 清除单项。Reset filters 只清除 pending facet，不改变 Text、不执行也不离开菜单；编辑器 `Esc` 返回菜单且不采用编辑，菜单 `Esc` 或 Cancel 丢弃全部 pending 编辑。菜单内 `Tab`/`Shift+Tab` 循环移动字段，Confirm/Cancel 之间还可用 `←`/`→`（或 `h`/`l`）切换；文字提交（编辑器 `Enter`）后焦点自动落到确认按钮，再按一次 `Enter` 即执行。确认按钮按当前条件动态命名：`Show all`（空条件，恢复 Popular Worldwide）、`Search "词"`（仅文本）、`Apply filters`（仅 facet）、`Search + filters`（组合）。Confirm 把查询直接应用到 Browse：标题显示当前条件（如 `Showing: city pop · Japanese`）；不再产生临时 Results 页。Browse 中重新打开 `/` 会预填当前条件，其他视图从空条件开始；Browse 有查询时按 `Esc`/`Backspace`/`h`（footer 提示 `esc popular`）一键恢复默认列表，本地过滤先清除；条件仅会话内有效，不会持久化。
- **搜索分页**：Apple Music 搜索仍受 MusicKit 限制，每次最多 25 项；Radio Browse 的 Popular Worldwide 与 Search & Filters 每页 100 项，光标靠近末尾时自动加载下一页。
- **Radio 探测状态**：Radio 列表的电台行显示本机 HTTP 首字节（TTFB）可达性（`○ unchecked`/`○ queued`、`◌ checking…`、`● <latency>`、`× TLS error` 等）——颜色、符号、文字三者并存，无色终端仍可区分。探测只针对当前可见项、并发上限 2，且不阻塞播放与导航；它验证 reachability/TLS/HTTP status，不验证 codec 支持。probe 和播放启动 guard 都是 10 秒，所以 `× timeout` 表示该流播放也会超时，而不是 probe 更严格；RPC 为返回结果保留额外余量。终态在当前进程内缓存，但 `timeout` 会在下次进入 view 时重试；播放失败探测项相当于手动重试、会提示原因并清除缓存。流在 10 秒内未进入播放会显示错误而不是无限 `buffering…`；`buffering…` 时 `Space`/`c` 会暂停。
- **小终端**：小于安全布局尺寸时只显示确定性的 `Terminal too small — resize`，并忽略除 `q`/`Ctrl+C` 之外的按键，避免操作不可见的页面；弹层宽高不超过终端，主题列表围绕当前项滚动；Search & Filters 在高度不足时自动切换为紧凑布局（去掉空行与分隔线）以保证 Confirm/Cancel 始终可见；帮助/信息弹层超出高度时可滚动（见上）。
- **异步隔离**：列表请求携带 generation 与目标页面身份；导航、来源、视图或详情变化后的旧结果被忽略，且不能清除新页面的 loading 状态。搜索返回保留完整父页上下文（source/view/detail/filter/selection/items）。
- **断开**：helper notification 流关闭即冻结进度并显示 `quit and restart lilt`；不再把旧快照表现为仍在播放。
- **安全**：所有外部元数据在展示边界删除 ESC、C0/C1/OSC 等终端控制输入，tab/换行各替换为一个固定宽度空格，保留正常 Unicode；行宽计算不接收外部控制字符或可变 tab stop。
- **Up Next**：右侧面板标题显示位置与来源（`Up Next · 12/48 · 歌单名`）；已播放历史变暗、当前项带播放标记、后续项正常显示。长标题单行省略（`…`），不换行、不溢出边框。窄终端聚焦时回退为全页队列。

## 各来源语义

- **Apple Music**：有队列；支持 shuffle/repeat/next/prev/入队；歌单可进入详情看曲目。
- **Radio**：live 单流，无队列、无 next/prev；显示电台名；ICY 当前曲目（若能取到）显示在艺术家位。
- **互斥**：开始广播即停 Apple Music；开始 Apple Music 即停广播。
