# Spec: UX（布局、导航、键位）

## 布局

```
 SOURCE   Apple Music    Radio                       ← Tab 切换 Source
 VIEW     1 Playlists   2 Recent   3 Presets          ← 1..n 选择子视图
 ┌─────────────────────────────────────────────────┐
 │ > 2001 年摇滚金曲                               │
 │   喜爱歌曲                                      │  ← 单列表（唯一光标）
 │   经典摇滚代表作品                              │
 └─────────────────────────────────────────────────┘
 ┌ Now Playing ────────────────────────────────────┐
 │ ▶ In the End — LINKIN PARK                      │
 │ [████░░░░] 1:23 / 3:39   full · shuffle         │
 └─────────────────────────────────────────────────┘
 ? help · Tab source · 1-9 view · enter open/play · q quit
```

- 只有**一个列表光标**；不再有"侧栏 + 内容"两个列表。
- **搜索不是视图**：`/` 在任意位置打开搜索输入，结果作为可返回的临时列表
  （`Esc`/`Backspace` 返回），Apple Music 结果按 `Songs`/`Playlists` 分组，Radio 结果为电台。
- `Now Playing` 是只读状态带；弹层（帮助/信息/主题）覆盖在中央。

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
| `p` / `x` | 播放当前项（歌单=整单播放） |
| `Esc` / `Backspace` / `h` | 返回上级 / 清除过滤 |

### 播放
| 键 | 作用 |
|---|---|
| `Space` / `c` | 暂停 / 继续 |
| `n` / `b` | 下一首 / 上一首（仅 Apple Music） |
| `v` | 停止 |
| `s` | shuffle 开关（仅 Apple Music） |
| `R` | repeat 循环（off → all → one） |
| `e` / `E` | 插到下一首 / 追加队列（仅 Apple Music） |

### 内容与视图
| 键 | 作用 |
|---|---|
| `/` | 全局搜索：Apple Music 目录或 Radio 电台；结果作为临时列表 |
| `F` | 过滤当前列表 |
| `f` | 收藏 / 取消收藏当前项（含电台） |
| `0` | 打开 Up Next 队列（enter 跳转 · x 移除 · J/K 重排 · c 清空） |
| `S` | 把当前队列保存为本地歌单 |
| `a` | 把选中项加入本地歌单（或电台 URL） |
| `t` | 主题选择器 |
| `i` | 曲目信息弹层 |
| `?` | 帮助弹层 |
| `q` / `Ctrl+C` | 退出 |

## 状态反馈

- **loading**：列表加载中，标题显示 `loading…`，内容区显示 `loading…`。
- **busy**：播放请求进行中，Now Playing 显示 `starting…`。
- **toast**：操作反馈（已收藏/已入队/错误）自动消失（成功 ~4s，错误 ~5s，红色）。
- **弹层**：`?`/`i`/`t` 居中显示，任意键关闭。

## 各来源语义

- **Apple Music**：有队列；支持 shuffle/repeat/next/prev/入队；歌单可进入详情看曲目。
- **Radio**：live 单流，无队列、无 next/prev；显示电台名；ICY 当前曲目（若能取到）显示在艺术家位。
- **互斥**：开始广播即停 Apple Music；开始 Apple Music 即停广播。
