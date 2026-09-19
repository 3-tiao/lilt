# Client API —— 数据模型

client 与 server 之间传递的所有数据形状。命令如何返回它们见
[`commands.md`](commands.md)；持久化 schema 见
[`../internals/state.md`](../internals/state.md)。

## 1. SourceDescriptor

描述一个可用来源及其每一项能力的状态（`sources.list` 的返回元素）。

```jsonc
{
  "id": "apple-music",
  "label": "Apple Music",
  "priority": 100,
  "available": true,
  "availability": "ready",
  "reason": "",
  "description": "Apple Music through the signed MusicKit helper.",
  "capabilities": {
    "search.songs":     {"available": true, "reason": "", "description": "Search the Apple Music catalog for songs."},
    "search.playlists": {"available": true, "reason": ""},
    "search.stations":  {"available": true, "reason": ""},
    "library":          {"available": true, "reason": ""},
    "playback.full":    {"available": true, "reason": ""},
    "playback.preview": {"available": true, "reason": ""},
    "queue":            {"available": true, "reason": ""},
    "shuffle":          {"available": true, "reason": ""},
    "repeat":           {"available": true, "reason": ""}
  }
}
```

字段与规则：

- `availability`：`ready | authorization_required | subscription_required |
  unavailable | degraded`。
- 顶层 `available` 表示**至少有一个** capability 可用，不代表全部可用。
- 来源选择 MUST 检查所需 capability 自身的 `available`，而不是只看顶层。例如
  Radio Browser 查询失败不应让“播放已有 stream URL”的 `playback.stream` 变成
  不可用。
- `priority` 越高，未指定来源时越优先。
- `description`（descriptor 与 capability 均可选）是**非规范性**的文字，为 agent/skill 提供
  线索（这个 source 是什么、该 capability 做什么）。client/TUI MUST NOT 依赖它做分支逻辑，
  只以 schema、`available`/`reason` 和稳定错误码为准。

稳定 capability 名：

```text
search.songs  search.playlists  search.stations  search.radio  search.trending
library  recommendations
playback.full  playback.preview  playback.stream
queue  shuffle  repeat
```

新增 source 可以增加 namespaced capability，但已有名称的语义不得改变。

默认优先级：

| Source | priority | 可用条件 |
|---|---:|---|
| `apple-music` | 100 | 各 capability 独立。正常音乐选择要求 `playback.full`；未授权时可能只剩 search/preview |
| `audius` | 80 | discovery（`search.songs`、`search.playlists`、`search.trending`）与播放（`playback.full`、`queue`）已实现；匿名且 `not_required`。连接账号后额外声明 `library`（用户歌单）。 |
| `radio` | 50 | `search.radio` 取决于 Radio Browser；`playback.stream` 取决于平台 stream engine，二者互不连坐 |

## 2. Item

可播放或可浏览的实体。所有 discovery 结果、队列元素、收藏和 recent 都用它。

```jsonc
{
  "source": "apple-music",
  "kind": "song",
  "id": "am:1646769334",
  "providerId": "1646769334",
  "ref": "apple-music:song:1646769334",
  "url": "https://music.apple.com/...",
  "title": "夏天",
  "artist": "Nicky Lee",
  "previewURL": "https://...",
  "radio": null
}
```

必填：`source`、`kind`、`id`、`title`、`ref`。

- `id` 是 lilt 的稳定 identity，用于收藏、去重和持久化；规则见
  [`../internals/sources.md`](../internals/sources.md)。
- `kind` 是封闭的公共枚举：`song | playlist | album | station | stream`；Audius track 映射为
  `song`。`album` 目前仅 Apple 资料库暴露。provider 原生类型可放在 source-specific metadata，client 不需要 unknown-kind
  fallback。
- `providerId` 是 provider 原生 id；没有原生 id 的 radio stream 可省略。
- `url` MAY 是 provider 的 canonical public URL 或 radio 流 URL。Audius `stream.url` 是短期签名
  播放资源，不是公开 Item URL，MUST 在播放启动时由 provider 重新解析，MUST NOT 出现在持久状态。
- `ref` 是可播放引用（见下节）。`id`、`providerId`、`ref` 语义不同，不得互相
  猜测或复用字段。
- `radio` 仅用于 radio typed metadata：`origin`、`tags`、`languages`、`country`、
  `codec`、`bitrate`、`votes`、`clickCount`、`clickTrend`、`lastCheckOK`、
  `lastCheckTime` 等。wire schema（`api/schema.go` 的 `RadioMetadata`）与
  `api.RadioMetadata` 必须保持同字段集：客户端回传带完整电台元数据的 item
  （如 `favorites.set`）会被严格校验拒绝，任一侧缺字段即互操作破裂。
  - `radio.origin`：`builtin`（源自 cliamp/cliamp.stream 的 lilt vendored snapshot）、`directory`（Radio Browser）、
    `user`（用户添加或收藏的 URL）。

## 3. Reference

规范输入（`playback.*` 的 `ref`、`queue.add` 的 `ref`）：

```text
apple-music:song:<id>
apple-music:playlist:<id>
apple-music:station:<id>
audius:song:<provider-id>
audius:playlist:<provider-id>
spotify:song:<id>                # 仅未来 hypothetical source
https://radio.example/live.mp3   # radio stream
```

规则：

- ref 语法是 `source:kind:id`，`source` 与 `kind` 都不可省略；裸 `kind:id`
  不是合法输入，返回 `invalid_reference`。
- radio 的持久 identity 是 `radio:<normalized-url>`，它是状态标识，**不是播放
  输入**；播放 stream MUST 使用 URL 或 Item 的 `ref`。
- 解析规则见 [`extending.md`](extending.md)。
- Audius 的公开 identity MUST 为 `audius:<kind>:<provider-id>`，ref 同形；`kind` 不可
  省略，因而 song/playlist identity 无歧义。

## 4. PlaybackState 与 PlaybackStatus

helper State 的公开归一化投影，外加 server 级字段。

```jsonc
{
  "sequence": 42,
  "source": "apple-music",
  "track": { /* Item */ },
  "position": 12.4,
  "duration": 240.0,
  "status": "stopped|playing|paused|buffering|error",
  "audioVariant": null,
  "format": "System-selected",
  "availableFormats": [],
  "shuffle": true,
  "repeatMode": "off|all|one",
  "isLive": false,
  "mode": "none|preview|full|stream",
  "playbackError": null,
  "queueRevision": 7,
  "queueSource": "apple-music",
  "streamTitle": null,
  "streamArtist": null,
  "queue": [ /* Item */ ],
  "queueIndex": 0
}
```

- `sequence` MUST 在一个 server 生命周期内单调递增，即使 helper 重生也不得
  回退。放在 watch event 中时 MUST 等于 event 顶层 `sequence`；命令 response
  中的值是该命令提交时的 server sequence。
- watch client MUST 忽略 `sequence` 小于或等于最后已应用值的 event。
- `queueRevision` 只在**队列构成变化**时递增（add/remove/move/clear/replace）；
  自动切歌或 seek 不递增。用于 [`commands.md`](commands.md) 的乐观并发。它与
  `AppState.revision`（持久化版本）是不同概念。
- **canonical 队列序号空间（唯一）**：`queue` 数组的顺序是**提交顺序**，
  `queueIndex` 与 `queue.jump`/`queue.remove`/`queue.move` 的 index 都指这个数组。
  `shuffle` 只是播放推进策略（随机选择尚未播放的曲目，一轮内不重复，耗尽后 no-op，
  `repeatMode=all` 时重洗一轮继续）；它 **MUST NOT** 改变、重排或替换 reported `queue`，
  也不得让任何 index 在不同顺序坐标系之间解释。播放推进导致的 `queueIndex` 移动是
  正常的，但数组本身保持提交顺序。
- `streamTitle` / `streamArtist` 是 ICY 电台元数据（server 读取流内的
  `StreamTitle`，并将 `"Artist - Title"` 拆分），仅 stream 播放且流已公告时有值；
  没有公告时为 `null`。client 不得假设其存在。（ICY 并非正式缩写，源自
  SHOUTcast/Icecast 的 `ICY 200 OK` 与 `icy-*` 头，通常理解为 Icecast。）
- `PlaybackStatus` 是 `PlaybackState` 的命名投影：包含全部非队列字段，但**同时省略**
  `queue`、`queueIndex` 和 `queueSource`，避免无上下文索引。`session.status` 在 `includeQueue=false`
  （默认）时返回它；`includeQueue=true`、watch 初始快照和 `playback.changed` 返回完整
  `PlaybackState`。`api.describe` MUST 以两个可 `$ref` 的 JSON Schema 表达它们。
- `mode`：`preview` 是受限试听；`full` 是**source-agnostic 的完整、非 preview 播放**，可由
  Apple Music catalog 或 Audius direct URL 实现；它单独不承诺有限队列。`stream` 是广播
  （`isLive=true`）。client MUST 用 `source`、`isLive`、`duration`、`queueSource` 与 capabilities
  判断来源和队列，MUST NOT 把 `full` 解释为仅 Apple Music 或必有队列。helper 的内部 URL mode
  不成为公开枚举值，server 将其投影为 `full`。来源授权由 authorization 命令和
  `sources.list` capability availability 表达，不属于 `PlaybackState`。

## 5. AppState

持久状态的归一化公开投影（`state.get` 的返回）。

```jsonc
{
  "revision": 12,
  "theme": "gruvbox",
  "lastSource": "apple-music",
  "favorites": [ /* Item */ ],
  "recent": [ /* {item: Item, playedAt: string} */ ],
  "recentContainers": [ /* {item: Item, playedAt: string} */ ]
}
```

- `revision` 在每次成功持久化后单调递增。
- AppState **不是** `state.json` 的原始 JSON；server 负责在公开 Item 模型和
  [`../internals/state.md`](../internals/state.md) 的持久格式之间转换。
- 运行期间，最后一次成功持久化后的 server 内存快照是权威状态；`state.json`
  是它的耐久表示和下次启动输入。server 不监视也不合并运行期间的外部编辑。

## 6. 其他结果模型

| 模型 | 必填字段 |
|---|---|
| `SourceAuthorization` | `source`、稳定 `status: not_required|not_determined|pending|authorized|denied|expired|error`；可选 `accountLabel` / `expiresAt` / `details`。`details` 是 namespaced source-specific 信息，generic control flow 不得依赖它。 |
| `AuthorizationFlow` | `flowId`、`source`、`status: pending|authorized|denied|expired|cancelled|error`、`interaction`；可选稳定 `error: {code,message}`。`interaction` 含 `type: system_dialog|browser|device_code|none`，可选 `url` / `userCode` / `expiresAt`。绝不包含 token 或 secret。 |
| `QueueState` | `source: SourceId\|null`、`items: [Item]`、`index`、`queueRevision`；空队列时 `source = null`、`index = -1` |
| `WatchSnapshot` | `sequence`、`playback: PlaybackState`；请求 `includeState` 时含 `state: AppState`；订阅对应 topic 时含 `sources: [SourceDescriptor]` / `authorizations: [SourceAuthorization]` |
| `RadioProbeResult` | `status: "healthy" \| "failed"`；可选 `latencyMs` / `errorCode` / `message` |
| `SearchResult` | `source`、`term`、`groups`（见 [`commands.md`](commands.md)） |
| `RadioSearchResult` | `items`、`query`；可选 `degradedOrigins: [{origin,code,message}]` |
| `RadioOptionsResult` | `options: [{value,count}]`；可选同形 `degradedOrigins` |

## 7. JSON 约定

- nullable 字段 MUST 输出 JSON `null`（不以省略表示“未知”）。例如 `format` 的线值是
  `System-selected`，UI 可以显示为 `Auto`。
- optional 字段 MAY 省略。
- 枚举值一律小写、稳定的字符串，不使用数字。
- `api.describe` 的 schema 使用 JSON Schema Draft 2020-12，并通过 `$ref` 引用
  以上模型。
- Apple Music 的 `canPlayCatalogContent`、`hasCloudLibraryEnabled` 和账户字段如需公开，
   MUST 放在 `SourceAuthorization.details`，不得重建通用顶层字段。helper 的订阅读取是异步的：
   `details.accountStatus` 为 `checking` 时表示 `authorized` 但订阅状态未定；此时
   `canPlayCatalogContent` 等未知字段必须省略，不得报 `false`。

### 有限队列不变量

`QueueState.source` 为 `null`（空）或唯一 finite-queue Source，且每个 Item 的 `source` MUST
等于它。完整 `PlaybackState.queueSource` 与 `QueueState.source` 同义；`PlaybackState.source` 与
非空 `queue.source` MUST 相同。Apple Music 与 Audius 支持有限队列；radio 没有有限队列。
Audius 的 URL 队列支持 queue list/jump、播放控制与 `queue.add/remove/move/clear`（server 侧实现）。
完整分层见
[`../internals/providers.md`](../internals/providers.md)。
