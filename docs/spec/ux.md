# Spec: UX（布局、导航、键位）

## 布局

```
 SOURCE   Apple Music    Radio                       ← Tab 切换 Source
 VIEW     1 Home   2 Playlists   3 Recent   4 Presets ← 1..n 选择子视图
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
- **搜索不是视图**：`/` 在任意位置打开搜索输入，结果作为可返回的临时列表
  （`Esc`/`Backspace` 返回），Apple Music 结果按 `Songs`/`Playlists` 分组，Radio 结果为电台。
- `Now Playing` 是只读状态带：Apple 队列播放时显示来源列表、当前位置和 `0 Up Next`。
  弹层（帮助/信息/主题）覆盖在中央。
- Apple Music Home 是线性分组页（非卡片）：Continue Playing、Recently Played、Quick Start、Your Playlists；空分组不显示。没有 Music User Token 或推荐数据时仍可用。
- 底部只显示当前焦点的主要操作：根页显示 Source/View 导航；Apple 歌单详情显示
  顺序播放、随机播放、从选中曲播放；Up Next 焦点显示编辑操作。`?` 显示完整键表。

## 键位

### 窗口与导航
| 键 | 作用 |
|---|---|
| `Tab` / `Shift+Tab` | 切换 Source（Apple Music / Radio） |
| `1`–`9` | 选择当前 Source 的子视图（记住每个源上次视图） |
| `[` / `]` | 循环子视图 |
| `j`/`k`、`↑`/`↓` | 移动选择 |
| `g`/`G` | 顶部 / 底部 |
| `Ctrl+d`/`Ctrl+u` | 半页 |
| `Ctrl+f`/`Ctrl+b` | 整页 |
| `Enter` | 打开（歌单详情/子节点）或播放 |
| `p` / `x` | 播放当前项；Apple 歌单详情 `p` 从首曲顺序播放，`s` 随机播放，`Enter` 从选中曲开始 |
| `Esc` / `Backspace` / `h` | 返回上级 / 清除过滤 |

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
| `/` | 全局搜索：Apple Music 目录或 Radio 电台；结果作为临时列表 |
| `F` | 过滤当前列表 |
| `f` | 收藏 / 取消收藏当前项（含电台） |
| `0` | 聚焦/取消聚焦 Up Next（enter/p 跳转 · x 移除 · J/K 重排 · c 清空） |
| `a` | 添加电台 URL |
| `t` | 主题选择器 |
| `i` | 曲目信息弹层 |
| `?` | 帮助弹层 |
| `q` / `Ctrl+C` | 退出 |

## 状态反馈

- **loading**：列表加载中，标题显示 `loading…`，内容区显示 `loading…`。
- **busy**：播放请求进行中，Now Playing 显示 `starting…`。
- **进度**：非 live 且播放中的曲目在 helper 状态快照之间按本地时间插值（不修改快照），到时长为止；`buffering` 时冻结在最后位置并显示中间状态，状态请求不重叠。
- **toast**：操作反馈（已收藏/已入队/错误）自动消失（成功 ~4s，错误 ~5s，红色）。
- **弹层**：`?`/`i`/`t` 居中显示，任意键关闭。
- **Up Next**：右侧面板标题显示来源与位置；已播放历史变暗、当前项带播放标记、后续项正常显示。长标题单行省略（`…`），不换行、不溢出边框。窄终端聚焦时回退为全页队列。

## 各来源语义

- **Apple Music**：有队列；支持 shuffle/repeat/next/prev/入队；歌单可进入详情看曲目。
- **Radio**：live 单流，无队列、无 next/prev；显示电台名；ICY 当前曲目（若能取到）显示在艺术家位。
- **互斥**：开始广播即停 Apple Music；开始 Apple Music 即停广播。
