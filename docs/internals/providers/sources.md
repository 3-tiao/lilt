# Spec: Sources（来源与浏览树）

> 本文标题与正文中的「v2」指**产品路线版本**（macOS → 跨端），与
> [`../client-api/README.md`](../../client-api/README.md) 的 **Client API v0.1**（接口版本）无关。

## 概念

- **Source（来源）**：公开的可浏览、可播放内容域；当前支持的来源清单见
  英文 [`../../product/roadmap.md`](../../product/roadmap.md) 的 “Supported services”。
  **provider** 是实现组件，不能与 Source 混称；builtin/directory 是 radio origin/provider。
- **ContentProvider**：一个 source 的编译期 discovery/plan preparation 实现，负责搜索、容器、
  identity、ref 与 transport-specific 私有播放 plan。它与实际出声的播放传输不同；权威分层见
  [`providers.md`](providers.md)。
- **BrowseNode**：来源下的一个可导航内容页（例如 Radio 的 `Browse` 或 playlist detail）。
  每个视图是一个可导航的条目列表，条目可以是：
  - **Item**：可播放或可进入的实体（歌单、歌曲、电台）。
  - **Entry**：进入另一个 BrowseNode 的动作（例如 Radio 的 "Browse countries"）。
- **Playable Item**：能经 server 路由到对应播放传输的项。

## 来源与浏览树（v2）

### `apple-music`
| 视图 | 内容 | `Enter` 行为 |
|---|---|---|
| `Home` | Continue Playing、最近播放、资料库歌单、本地收藏与入口的线性摘要；空分组省略 | Continue Playing 打开 Up Next；歌单打开详情；歌曲播放 |
| `Recent` | 最近播放的歌单（本地容器）+ 最近播放的歌曲 | 歌单打开详情；歌曲播放 |

搜索不是视图：`/` 在任意位置全局搜索 Apple Music 目录（Songs / Albums / Playlists 分组），
结果作为可返回的临时列表（`Esc`/`Backspace` 返回）。

专辑入口在 Home 的 `Albums`（资料库专辑列表，`library.albums`）。专辑详情（`album.tracks`）
显示专辑曲目；`Esc`/`Backspace` 返回。
- 详情内 `Enter` 从选中曲目开始播放到专辑末尾；`p` 从首曲顺序播放整张专辑。
- `album` 是公共 kind，播放 ref 形如 `apple-music:album:<id>`；专辑不是歌单详情的别名。

歌单详情（Home 的 `Your Playlists` 或 Recent 的二级）：显示该歌单曲目列表；`Esc`/`Backspace` 返回。
- 歌单曲目和播放按 id 先查 catalog、再查 library；因此搜索/URL 打开的 catalog 歌单与资料库歌单均可播放。
- 详情内 `Enter` 从选中曲目开始播放；`p` 从首曲顺序播放；`s` 随机播放整个歌单。
- 「喜爱歌曲」（Apple Favorite Songs personal mix）按本地化名称匹配后以最新在前显示及播放：`喜爱歌曲`、`喜愛歌曲`、`Favorite Songs`、`Favourite Songs`。MusicKit 未提供歌单类型标记，故为尽力而为的名称匹配。

### `radio`
| 视图 | 内容 | 说明 |
|---|---|---|
| `Home` | 最近播放、本地收藏与 Browse/Recent 入口 | Radio 默认页；空分组省略 |
| `Recent` | 最近播放的电台 | 本地状态 |
| `Browse` | 当前查询下的 Radio Browser 电台（分页，默认每页 100） | 默认 Popular Worldwide；`/` 打开 Search & Filters 并把查询直接应用到本视图；行内显示本机探测状态（见 [radio-discovery.md](radio-discovery.md)） |

内置候选数据来自 vendored m3u snapshot（`internal/builtin`，随二进制内嵌），
由 Radio Browse/搜索按 `origin=builtin` 呈现，而不是单独 public source/view。station snapshot/list
派生自 [cliamp](https://github.com/bjarneo/cliamp) / [cliamp.stream](https://cliamp.stream/)，不由 lilt
创建或拥有；上游列表为 `https://radio.cliamp.stream/streams.m3u`。文档、TUI 信息和
`lilt api` 必须归属它们，并声明无 affiliation/endorsement、无可用性
保证、音频权利归第三方，cliamp code license 不授予 station content 权利。内置台与目录台共用播放路径和 identity 规则
（`radio:<normalized-url>`），仅在候选来源上区分 `radio.origin`
（`builtin` / `directory` / `user`），见 [`../client-api/models.md`](../../client-api/models.md)。

无查询时 Browse 使用无条件 top-click（Popular Worldwide，`hidebroken=true`，按
`clickcount` 降序）。Radio `/` 是查询构建器：名称和 Language、Genre/tag、Country
使用 Radio Browser advanced search 的 AND 语义，同样按 `clickcount` 降序、启用
`hidebroken=true`；Confirm 后 Browse 即显示该查询的结果（标题如
`Showing: Text=city pop · Language=Japanese`），空条件 Confirm 恢复 Popular Worldwide。
条件仅会话内有效，不会持久化。

### `audius`（可选）
| 视图 | 内容 | `Enter` 行为 |
|---|---|---|
| `Discover` | 官方 trending tracks / playlists（分组） | song 播放；playlist 打开详情 |
| `Home` | 最近播放、Trending、本地收藏与 Discover/Recent 入口 | Audius 默认页；空分组省略 |
| `Recent` | lilt-local 且过滤为 Audius 的最近播放 | song 播放；playlist 打开详情 |

`/` 从任意 Audius 视图查询官方目录；Audius 不支持 station，`type:"all"` 仅返回
songs/playlists。歌单详情通过 `playlist.tracks` 打开。TUI 目前不显示未声明的账户 library
视图；账号连接仍是可选的，匿名 discovery/playback 不受影响。

### `jamendo`（J1/J2/J4 已完成）
| 视图 | 内容 | `Enter` 行为 |
|---|---|---|
| `Discover` | 官方 featured/popular tracks（`featured=1`、按 `popularity_month` 排序；song-only，无 playlist trending） | song 播放 |
| `Home` | 最近播放、Trending、本地收藏入口 | Jamendo 默认页；空分组省略 |
| `Recent` | lilt-local 且过滤为 Jamendo 的最近播放 | song 播放；playlist 打开详情 |

`/` 从任意 Jamendo 视图查询官方目录（自由文本 `search`）；Jamendo 不支持 station/album，
`type:"all"` 仅返回 songs/playlists。trending 通过 kind-specific 的 `search.trending.songs` 声明
（generic `search.trending` 不声明：playlist 无 popularity 排序），因此 Discover surface 与
Home 的 Trending 分节只含歌曲。媒体直链在起播时解析，`Item.url` 始终是 `shareurl`
canonical 页面。Jamendo 需要用户自带的 `client_id`：未配置时 source 为 `unavailable`，
`reason` 指向 `lilt jamendo setup`（TUI 内则在 source switcher 选中 Jamendo 直接打开 setup modal，同一条进程内路径）；授权语义、授权状态与归属要求见 [`jamendo.md`](jamendo.md)。

> 队列语义：Apple Music、Audius 与 Jamendo 是 finite-queue Source；radio 是无限 live 单流，不进队列。Audius 与 Jamendo 的媒体直链（Audius 签名 URL、Jamendo `audio` URL）只由
> URLQueueTransport 在曲目启动时解析，绝不成为 Item 的持久 identity、public queue、长期状态或 helper queue。

## 队列（Up Next）

有限队列由 server 拥有公开 source/revision，并由 helper 的对应内部播放 mode 执行
（Apple Music 的 MusicKit queue；Audius 的 server-owned URLQueueTransport）。lilt 提供：
有活动队列且终端足够宽时，Up Next 作为右侧常驻面板显示，来源和位置在标题中，并以历史（变暗）/当前/后续分层。`0` 聚焦或取消聚焦面板；窄终端在聚焦后回退为主区域全页队列。
焦点在 Up Next 时，`Enter` 或 `p` 跳转到该曲；`x` 移除选中项；`J`/`K` 重排；`c` 清空。
主列表中的 `x` 无操作。
任意有限队列 Source 视图 `e`（下一首播放）/`E`（追加）入队；队列中所有 Item 必须属于同一
`QueueState.source`，错误 source 的 `queue.add/ref` 返回 `source_mismatch`。开始另一 Source 停止
当前播放并替换/清空旧队列；失败保持 stopped，不恢复旧队列，且不允许 mid-queue fallback。**Apple 资料库歌单在 macOS 不可编辑**
（`MusicLibrary.createPlaylist/add/edit` 均 `@available(macOS, unavailable)`）。

URL 队列（Audius/Jamendo）支持 play/pause/resume/next/previous/stop、位置、queue list 与
jump；`queue.remove`、`queue.move`、`queue.add`、`queue.clear` 已由 server 侧 URL 队列实现，并遵循
`ifQueueRevision`。公开 `PlaybackState.mode` 仍为 `full`，以 `source:"audius"` 区分来源；
helper 的内部 `url` mode 不向 Client API 泄露。

Jamendo 在 J2 复用同一条 URL 队列与 `lilt-audio` 私有 `url` mode，公开投影为
`mode:"full"`、`source:"jamendo"`、`isLive:false`；凭据与错误映射见
[`jamendo.md`](jamendo.md)。

## Item 模型

```
Item {
  source: "apple-music" | "audius" | "jamendo" | "radio"
  kind:   "song" | "playlist" | "album" | "station" | "stream"
  id:     string            // lilt 稳定 identity，见 id 方案
  providerId?: string       // provider-native id
  ref:    string            // Client API 可播放引用；radio stream 使用 URL
  url?:    string            // provider canonical URL 或广播流 URL；不得是短期签名播放 URL
  title:  string
  artist?: string
  subtitle?: string         // 电台：国家/标签/码率
  isLive?: bool
}
```

## id 方案（跨端一致的稳定标识）

| 类型 | 方案 | 示例 |
|---|---|---|
| Apple Music 歌曲/歌单/专辑 | `am:<musicitem-id>` | `am:1440845629`、`am:-3750669790803871374` |
| Audius song/playlist | `audius:<kind>:<provider-id>` | `audius:song:abc123`、`audius:playlist:def456` |
| Jamendo song/playlist | `jamendo:<kind>:<numeric-id>` | `jamendo:song:1848357`、`jamendo:playlist:1234` |
| 广播电台 | `radio:<normalized-url>` | `radio:https://radio.cliamp.stream/lofi/stream` |

**identity 只有一个实现**：`internal/api` 的 `Identity`（`identity.go`）。所有
`id`/`providerId`/`ref`/stream URL 规范化都由它产生，其他包不得自己 `TrimPrefix`、拆冒号或写
URL 规范化。跨层一致性由 `FuzzIdentityRoundTrip` 保证（parse → build → parse 必须回到同一
Identity）。

规范化 URL：小写 scheme/host、删除默认端口（http :80 / https :443）、删除全部末尾 `/`
（根路径收敛为无斜杠形式）、保留 query 但去掉首尾空白、删除 fragment/userinfo。电台以 URL 为
身份（同名不同流视为不同电台），且 URL 必须是绝对 http(s) 端点；无法规范化的输入不产生 identity。

`id`、`providerId` 和 `ref` 不可混用：例如 Apple Music song 的 `id` 是
`am:1440845629`，`providerId` 是 `1440845629`，`ref` 是
`apple-music:song:1440845629`。Apple 的 `am:` 形式刻意不携带 kind（kind 单独存字段），因此由
`ref`（携带 kind）优先决定 identity；不带前缀的 provider id 不做拆分，`am:fake:album` 这类含冒号的
id 保持原样。完整 Client API 模型见 [`../client-api/README.md`](../../client-api/README.md)。

## 新增来源的步骤

1. 按 [`providers.md`](providers.md) 实现并注册 ContentProvider；其 descriptor、auth provider 和
   capability 必须通过 provider gate。
2. 定义 Item 的 id 方案（必须稳定、可跨端）与播放资源的短期/长期边界。
3. 若声明 `playback.*`，实现 `PreparePlayback`，将 source 的稳定 ref 映射到现有或新增的私有 transport plan；server 在提交时写入 active source/generation，不能读取私有 target。discovery-only source 跳过此项。
4. 覆盖 canonical ref、错误映射；声明 `playback.*` 时再覆盖 source 互斥、generation 过期通知和有限队列不变量的 fixture。
5. 在 UI 模型中声明可用 surface/Home capability，并通过 `s` source switcher（不是 Source tab）暴露它；CLI/skill 可先通过 `--source` 使用。
