# Spec: Sources（来源与浏览树）

## 概念

- **Source（来源 / Provider）**：一个可浏览、可播放的内容域。v2 有两个：`apple-music`、`radio`。
- **BrowseNode**：来源下的一个"视图"（例如 Apple Music 的 `Playlists`、Radio 的 `Browse`）。
  每个视图是一个可导航的条目列表，条目可以是：
  - **Item**：可播放或可进入的实体（歌单、歌曲、电台）。
  - **Entry**：进入另一个 BrowseNode 的动作（例如 Radio 的 "Browse countries"）。
- **Playable Item**：能交给引擎播放的项。

## 来源与浏览树（v2）

### `apple-music`
| 视图 | 内容 | `Enter` 行为 |
|---|---|---|
| `Home` | Continue Playing、最近播放、预设、资料库歌单的线性分组摘要；空分组省略 | Continue Playing 打开 Up Next；歌单打开详情；歌曲播放；预设执行 |
| `Playlists` | 用户资料库歌单 | 进入曲目详情（见下） |
| `Favorites` | 本地收藏的 Apple Music 歌曲与歌单（`f` 切换；与 Apple Music 资料库的「喜爱歌曲」智能歌单无关） | 歌曲播放；歌单打开详情 |
| `Recent` | 最近播放的歌单（本地容器）+ 最近播放的歌曲 | 歌单打开详情；歌曲播放 |
| `Presets` | 本地预设（见 state） | 解析并播放 |

搜索不是视图：`/` 在任意位置全局搜索 Apple Music 目录，结果作为可返回的临时列表
（`Esc`/`Backspace` 返回），按 `Songs` / `Playlists` 分组。

歌单详情（`Playlists` 的二级）：显示该歌单曲目列表；`Esc`/`Backspace` 返回。
- 歌单曲目和播放按 id 先查 catalog、再查 library；因此搜索/URL 打开的 catalog 歌单与资料库歌单均可播放。
- 详情内 `Enter` 从选中曲目开始播放；`p` 从首曲顺序播放；`s` 随机播放整个歌单。
- 「喜爱歌曲」（Apple Favorite Songs personal mix）按本地化名称匹配后以最新在前显示及播放：`喜爱歌曲`、`喜愛歌曲`、`Favorite Songs`、`Favourite Songs`。MusicKit 未提供歌单类型标记，故为尽力而为的名称匹配。

### `radio`
| 视图 | 内容 | 说明 |
|---|---|---|
| `Favorites` | 收藏的电台（`a` 添加自定义 URL 也进这里） | 本地状态；进入 Radio 时默认显示 |
| `Recent` | 最近播放的电台 | 本地状态 |
| `Browse` | 当前查询下的最多 20 个 Radio Browser 电台 | 默认 Popular Worldwide；`/` 打开 Search & Filters 并把查询直接应用到本视图；行内显示本机探测状态（见 radio-discovery-health.md） |

无查询时 Browse 使用无条件 top-click（Popular Worldwide，`hidebroken=true`，按
`clickcount` 降序）。Radio `/` 是查询构建器：名称和 Language、Genre/tag、Country
使用 Radio Browser advanced search 的 AND 语义，同样按 `clickcount` 降序、启用
`hidebroken=true`；Confirm 后 Browse 即显示该查询的结果（标题如
`Showing: city pop · Japanese`），空条件 Confirm 恢复 Popular Worldwide。
条件仅会话内有效，不会持久化。

> 队列语义：**队列只属于 Apple Music**。Radio 是无限 live 单流，不进队列。

## 队列（Up Next）

`ApplicationMusicPlayer.queue.entries` 是可编辑的（`get set`）。lilt 提供：
有活动队列且终端足够宽时，Up Next 作为右侧常驻面板显示，来源和位置在标题中，并以历史（变暗）/当前/后续分层。`0` 聚焦或取消聚焦面板；窄终端在聚焦后回退为主区域全页队列。
焦点在 Up Next 时，`Enter` 或 `p` 跳转到该曲；`x` 移除选中项；`J`/`K` 重排；`c` 清空。
主列表中的 `x` 无操作。
任意视图 `e`（下一首播放）/`E`（追加）入队。**Apple 资料库歌单在 macOS 不可编辑**
（`MusicLibrary.createPlaylist/add/edit` 均 `@available(macOS, unavailable)`）。

## Item 模型

```
Item {
  source: "apple-music" | "radio"
  kind:   "song" | "playlist" | "station" | "stream"
  id:     string            // 见 id 方案
  url?:   string            // Apple Music URL 或广播流 URL
  title:  string
  artist?: string
  subtitle?: string         // 电台：国家/标签/码率
  isLive?: bool
}
```

## id 方案（跨端一致的稳定标识）

| 类型 | 方案 | 示例 |
|---|---|---|
| Apple Music 歌曲/歌单 | `am:<musicitem-id>` | `am:1440845629`、`am:-3750669790803871374` |
| 广播电台 | `radio:<normalized-url>` | `radio:https://radio.cliamp.stream/lofi/stream` |

规范化 URL：小写 scheme/host、删除非根路径末尾 `/`、保留 query、删除 fragment/userinfo。加载旧状态时同一规范化身份去重。电台以 URL 为身份（同名不同流视为不同电台）。

## 新增来源的步骤（未来）

1. 定义该来源的 BrowseNode 树与 `Enter` 语义。
2. 定义 Item 的 id 方案（必须稳定、可跨端）。
3. 实现播放：Apple 平台走 MusicKit/web 引擎，其他走原生播放器。
4. 在 UI 顶层注册为新的 Source tab。
