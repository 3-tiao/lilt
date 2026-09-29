# 用 TUI

`lilt tui` 打开全屏界面；没有 server 时会自动起一个，有则附着。TUI 是一个 client，退出不停止播放。

布局、键位、反馈与弹层的完整规范见 [`../ui/ux.md`](../ui/ux.md)；UI 模型与导航不变量见
[`../ui/model.md`](../ui/model.md)。本页只给日常操作路径。

## 认识界面

从上到下：身份行（左侧当前 Source 名，右侧品牌 `lilt`）、surface 行（`1 Home · › 2 Recent` 等编号
surface，`›` 标当前项）、主工作区（列表 + 右侧 Up Next 轨道）、Now Playing 条（当前曲目、进度或
`LIVE`、格式、shuffle/repeat）、底部上下文提示。

没有 source tab 行：`1`-`9` 选 surface，`[`/`]` 循环，`s` 打开 source 切换器，`:` 打开命令面板，
点身份行也能打开 source 切换器。

## 每个来源的 surface

- 所有来源都有 **Home** 与 **Recent**。Home 是聚合页：Continue Playing、Recently Played、
  Trending（Audius）、Your Playlists、Favorites，再加一个 `Go to` 块（Search、Browse 或 Discover、
  Recent、All Favorites、All Playlists、Albums、Queue、Account）。预览项各最多五条。
- **Radio** 额外有 **Browse**（唯一的发现面，见 [`radio.md`](radio.md)）。
- **Audius / Jamendo** 额外有 **Discover**（trending）。
- Apple Music 不加 surface；Favorites 与歌单是 Home 的区块，`Go to → All Favorites` 打开完整列表。

## 常用操作

| 操作 | 键 |
|---|---|
| 搜索（全局） | `/`（Radio 下打开 Search & Filters） |
| 打开选中项 / 播放 | `Enter` |
| 只播放选中项 | `p` |
| 暂停 / 继续 | `Space` 或 `c` |
| 下一首 / 上一首 | `n` / `b`（有限队列） |
| 停止 | `v` |
| 入队：下一首 / 追加 | `e` / `E` |
| shuffle / repeat | `S` / `R` |
| 收藏 | `f` |
| 打开并编辑 Up Next | `0`（`x` 删除、`J`/`K` 重排、`c` 清空、`Enter` 跳转） |
| 主题 | `t` |
| 播放信息 / 帮助 | `i` / `?` |
| 返回临时页 / 清除本地过滤 | `Esc` / `Backspace`（顶层无过滤时不切换页面；用数字键切换） |
| 退出 | `q`（非输入态包括 Help / Playback Info 弹层；搜索、命令及筛选文本框内输入字母 `q`） |
| 添加电台 URL | `a`（Radio） |

`Enter` 的语义随上下文变化：在搜索结果里只播该行；在 surface（Home/Recent/Discover）里表示“从这里
开始播到本区块末尾”；在歌单/专辑详情里表示“从选中曲目播到末尾”。`p` 永远只播当前项。完整规则见
[`../ui/ux.md`](../ui/ux.md)。

## 开关与偏好

- `S` / `R` 是 toggle，播放命令会带上当前 shuffle/repeat 一起提交（`S` 后按 `Enter`/`p` 即随机播放）。
- 主题用 cliamp 的 TOML schema，放在 `~/.config/lilt/themes/`；内置主题可用 `t` 预览。
- 偏好（主题、上次来源）由 server 写入 `state.json`，schema 见
  [`../internals/persistence/state.md`](../internals/persistence/state.md)。

## 能力由来源决定

TUI 启动时读取 `sources.list`，按每个 source 声明的 capability 决定是否显示 shuffle、library/trending
预览及其快捷键提示。没有独立的“来源支持列表”。Radio 是直播单流、没有队列；Apple Music、Audius、
Jamendo 是互斥的有限队列。
