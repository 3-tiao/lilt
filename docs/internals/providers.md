# Spec: Provider 与播放传输

> **状态**：Audius Phase 1、Phase 2（含 2a/2b）、Phase 2.5、Phase 3（账号 OAuth）与
> Phase 4（TUI/skill/产品文档）**已完成**；Phase 3 通过
> 真实账号验收；Audius 使用 server-owned URL queue 与 `lilt-audio` 的 `url` mode。Apple Music 与 Radio 的现有
> 实现是此设计的过渡形态。公开 Client API 仍以
> [`../client-api/README.md`](../client-api/README.md) 为准。
> Apple Music 与 Radio 的现有实现是此设计的过渡形态。公开 Client API 仍以
> [`../client-api/README.md`](../client-api/README.md) 为准。

## 1. 目的与边界

一个 public Source 同时涉及两类不同的职责：

1. **资源与播放准备（provider）**：搜索、歌单、稳定 identity、canonical ref，并把稳定 ref
   准备为某个播放传输可执行的私有 plan。资源 runtime 可在另一 source 播放时运行，但不得输出音频。
2. **播放传输（playback transport）**：理解自己的私有 plan，把它变成声音、维护播放位置和有限队列。

旧的 `Engine` 同时承担了资源读取与播放控制，导致 Apple 的 discovery 跟随 MusicKit playback helper
被销毁。现在 `Engine` 仅代表独占的 MusicKit playback backend；Apple provider 通过独立
`AppleResourceClient` 做 catalog/library/resolve。macOS 的实际音频仍只有两个 backend：
`lilt-player`（MusicKit，Apple）与 `lilt-audio`（AVPlayer，Audius/Radio）；server 按 transport
二选一，同一时刻只有一个实际播放。详见 [`audio-helper.md`](audio-helper.md)。

本设计不引入运行期 provider 插件。provider MUST 随 Go server 编译、由 server 启动时
注册；外部脚本只能调用 Client API，不能注册新 source。

## 2. 分层与所有权

```text
 Client API client (TUI / CLI / skill)
                 |
                 v
 lilt serve -- source registry, activeSource, queue/state/recent/auth owner
       |                                      |
       | resource / plan preparation          | exclusive playback routing
       v                                      v
 ContentProvider registry                 active PlaybackBackend
 AppleProvider -> AppleResourceClient     lilt-player: Apple MusicKit queue
 RadioProvider -> Go Radio client          lilt-audio: Radio stream / Audius URL
 AudiusProvider -> Go Audius REST
```

| 关注点 | owner | 约束 |
|---|---|---|
| Source descriptor、discovery、ref/resource 解析、playback plan 准备 | 对应 `ContentProvider` / resource runtime | 公开 Item 与私有 plan 分离；资源 runtime 不输出音频、不写 server state |
| 活动 source、公开播放状态、队列 revision、recent、favorites | `lilt serve` | 唯一 owner；不得从 helper 状态推断 source |
| 音频输出、位置、内部播放队列 | 当前 `PlaybackBackend` | MusicKit 或 AVPlayer 的实际出声 backend；不暴露给 CLI/TUI/skill |
| auth flow 生命周期 | `lilt serve` + `AuthProvider` | provider 处理具体 OAuth/系统交互；token 不离开 secure storage。flow 预算单一 owner：provider 可选实现 `AuthFlowBudget` 声明自己的交互预算（Apple 声明 10 分钟），server 用声明值建 flow context；未声明的 provider 用 server 默认 2 分钟，行为不变 |

`activeSource` 是 server 明确提交的 source。它取代“live/stream 则 radio、其他则 Apple”的
推断：helper 内部 mode 不足以在多个有限来源间确定公开 source。它的完整生命周期见第 5 节。

## 3. Discovery provider 契约

实现层以一个编译期注册的窄接口表达。最终 Go 名称可随实现调整，但行为 MUST 保持：

```go
type ContentProvider interface {
    Source() api.SourceID
    Descriptor(context.Context) api.SourceDescriptor
    Search(ctx context.Context, term, kind string, limit int) ([]api.Item, error)
}

type PlaybackPreparer interface {
    PreparePlayback(ctx context.Context, request PlaybackRequest) (PreparedPlayback, error)
}

type PlaylistProvider interface {
    PlaylistTracks(ctx context.Context, id string) (api.Item, []api.Item, error)
}
```

- `PlaylistProvider` 是可选扩展；某个 source 未声明的能力不调用对应方法，并通过
  `SourceDescriptor.capabilities` 说明原因。
- `PlaybackPreparer` 是 `playback.*` capability 的必备扩展。它接收稳定 ref，不直接向 Client API
  返回 media URL；它返回 `PreparedPlayback`，其中只有对应 transport 能理解私有 payload。仅 discovery
  source 可以不实现它，也不得声明 playback capability。Audius 已实现 preparer 并声明 playback capability。
- `Descriptor` 是 `sources.list` 的唯一实现来源；同一 source 的 `AuthProvider` 必须同时
  出现在 `authorization.list`，由 provider gate 验证。
- Apple provider 是现有 MusicKit discovery RPC 的薄适配器，不在 Audius 工作中重写。
- Radio provider 包装现有 Go Radio client；builtin/directory 是 radio 的 origin，不是 provider。
- Audius provider 使用官方 `https://api.audius.co/v1` REST API。公开搜索、歌单与播放无需
  账号；OAuth 是后续账号能力，不得阻塞匿名播放。
- Jamendo provider 的 J1/J2 已实现：使用官方 `https://api.jamendo.com/v3.0` REST API，公开读取
  只需用户自带的 `client_id`（应用级配置，不是用户授权）；播放走与 Audius 相同的 URL 队列。
  凭据、公开授权语义、错误映射与额度限制见 [`jamendo.md`](jamendo.md)。

discovery 路由以 provider 的 `Descriptor.capabilities` 为**唯一真值**：`discovery.search`
对未声明的 search capability 返回 `unsupported_command`，`type:"all"` 只运行该 source 已声明的
分组。provider MUST NOT 用并行的“支持列表”再声明一次能力。descriptor/capability 可以带可选
`description` 供 agent 参考，但它不参与路由与判定。

### 3.1 PreparedPlayback 与 transport

`PreparedPlayback` 不是一个会随 provider 数量膨胀的全局 `MediaTarget` union。它是 provider 与
transport 的编译期交接对象：

```go
type PreparedPlayback interface {
    Source() api.SourceID
    Transport() TransportID
    PublicQueue() []api.Item // 稳定、可投影、无短期 media URL
    StartIndex() int
}

type PlaybackTransport interface {
    ID() TransportID
    Start(ctx context.Context, plan PreparedPlayback) (core.PlaybackState, error)
    // control / queue operations / state subscription
}
```

provider 返回完整的公开 `api.Item`，并负责它的 source、kind、stable `id`、providerId、canonical ref 和
短期 URL 剥离；Apple 薄适配器把 helper `core.Item` 映射为该形状。server 只读取 `PreparedPlayback` 的
公开队列、source、transport 和起点；它不检查、序列化或持久化 transport 私有 payload。transport 按
`TransportID` 选择，并只接受它认识的 plan 类型。

| plan / transport | 例子 | 私有 payload |
|---|---|---|
| `AppleMusicPlan` / MusicKit transport | Apple Music | MusicKit catalog IDs |
| `URLQueuePlan` / direct-URL transport | Audius、Jamendo、未来公开直链 source | source-owned、按需 URL resolver |
| provider-specific plan / provider-specific transport | 未来 DRM/SDK source | 该 SDK 的 session/opaque target |

因此二十个提供 direct URL 的 source 可共用一个 URL transport；只有出现新的**播放机制**才需要新增
plan/transport 类型。声明 playback capability 的 provider MUST 能把 ref 准备成可执行 plan。

`core.Item.URL` 只可携带稳定 public URL 或稳定 radio stream URL。尤其 Audius 的签名 media URL
MUST 在播放启动或切曲时重新解析，MUST NOT 写入 `state.json`、recent、favorites、watch event、
Client API response、错误或日志。

## 4. 播放传输与 mode

public `PlaybackStatus.mode` 描述**用户可见的播放语义**，而不是 helper 的传输细节：

| 公开 mode | source 示例 | 语义 |
|---|---|---|
| `none` | 任意 | 无播放 |
| `preview` | Apple Music | 受限试听 |
| `full` | Apple Music、Audius、Jamendo | 完整、非 preview 的 source 播放；是否 live、时长与队列由其他字段决定 |
| `stream` | Radio | live 流；`isLive=true`，没有有限队列 |

因此 Audius MUST 对 client 投影 `mode:"full"`、`isLive:false`、其自身的 `source:"audius"`
和有限 `duration`。client 使用 `source` 区分 Apple 与 Audius，使用 `isLive`、`duration`、
`queueSource` 与 capability 判断直播和队列；不得从 `full` 单独推导有限队列。

`lilt-audio` 的 `url` mode 是实现细节：它用 AVFoundation 播放当前有限 direct-URL item，并由
server 映射为公开 `full`。它不会成为 Client API 枚举值。Radio 流同样由 `lilt-audio` 承载。

### 4.1 有限 URL 队列

Audius track 是有限时长的匿名可播放资源，而不是 Radio live stream。播放流程为：

```text
audius:song:<id> / audius:playlist:<id>
  -> AudiusProvider 产生 URLQueuePlan（公开队列 + 私有 lazy resolver）
  -> server 建立同一 source 的有限队列并提交 activeSource=audius
  -> URLQueueTransport 在当前曲启动时取得新签名 URL
  -> lilt-audio urlPlay（单个短期 target）
  -> lilt-audio State(mode=url) -> server public State(mode=full, source=audius)
```

URLQueueTransport 由 server 编排队列：`lilt-audio` 只持有正在播放的一项短期 target。曲目结束、jump 或
next 时 transport 再向 provider 解析目标曲目，可短距离预取下一首；它不得在歌单启动时永久保存整队列
的签名 URL。URL 过期/403 或流停滞时 MUST 重新解析一次（已实现：helper 媒体失败或 20s stall 看门狗触发
`RetryCurrent` 重取一次，触发点在 journal 记 `playback_stalled`，仅日志不发布）。二次失败时：队列还有
后续项且连续跳过未达上限（2）MUST 跳到下一项并发布 `playback_skipped`（队列推进、播放继续，新项获得
自己的重试预算）；连续第 3 个死项或最后一项死链按第 9 节的播放错误语义结束会话（`playback_error`）。
跳转目标本身的解析失败视作系统性故障，同样结束会话并映射 `source_unavailable`。

Phase 2 的 URL 队列 v1 支持 play、pause、resume、next、previous、stop、位置、queue list 与
jump。Phase 2.5 起 `queue.remove`、`queue.move`、可编辑 `queue.add` 与 `queue.clear` 由 server 侧
URL 队列实现（`lilt-audio` 只播放当前项），并遵循 `ifQueueRevision` 乐观并发。

切换 source MUST 先停止旧传输、清空/替换旧有限队列，再开始新 source。启动失败后公开状态
MUST 为 stopped，MUST NOT 恢复旧队列或做 mid-queue fallback。切换后约 3 秒内，若 engine 通知的
状态形状推导出的 source 与已提交的 `activeSource` 不符，server 丢弃它（已知限制见
[`../product/limitations.md`](../product/limitations.md#8-provider-切换瞬间的旧状态尾巴已接受)）。

## 5. activeSource、generation 与状态机

server 在 `s.mu` 下维护 `activeSource`、`playbackGeneration` 与公开队列。`activeSource` 是最近一次
播放会话的 source；停止后仍保留为公开 stopped state 的 `source`，而 `queueSource` 变为 `null`。
启动失败同样保留**请求的新 source**并发布 stopped，不恢复旧 source 或旧队列。

| 转换 | server 行为 |
|---|---|
| fresh server | `activeSource` 取持久 `lastPlaybackSource`（旧 state 从 `lastSource` 迁移），无队列，状态 stopped |
| start source S | generation 递增，停止旧 transport，清空旧队列，写 `activeSource=S`，进入 starting |
| start success | 相同 generation 的 helper state 提交为 playing/paused；写 queue 与 recent tracker |
| start failure | 相同 generation 提交 stopped，queue 为空；不恢复旧会话 |
| stop / queue end | 停止/清空传输；保留 `activeSource`，公开状态 stopped |
| helper restart | generation 递增，旧通知失效；发布 stopped，保留最后 active source |

Phase 2 在每个 state-changing helper RPC 中传入 server 生成且不可复用的 `playbackGeneration` 与
`transportSessionID`。server 在发送 start 前已绑定该二元组，并只接受当前 helper instance、generation、
session 三元组完全匹配的 notification/response。每个 MusicKit/AVFoundation observer 在注册时捕获不可变的
generation/session，旧 observer 即使延迟回调也不得重新标记为当前 session。外部媒体键
事件使用其所属的捕获 session 并标记内部 `origin:"external"`，不能改变 source 归属。

`lastPlaybackSource` 只在一个 start 成功取得当前 generation/session 的 helper response 时更新并持久化；
stop、queue end 和 helper restart 保留它。启动失败仍以请求 source 发布 stopped，但不覆盖
`lastPlaybackSource`，因此重启恢复最近一次成功会话的 source。该持久化是播放成功后的附加 state mutation：
保存失败返回 `partial_failure`，播放本身继续，且下次成功 state mutation 才重试写入。

## 6. Helper 私有协议

`lilt-player` 有两个逻辑角色：独立的 Apple resource client 只执行 `authorize`、catalog、library 和
resolve RPC；当前 Apple playback backend 执行播放与队列 RPC。resource client 可与 `lilt-audio`
共存，但绝不调用播放/队列 RPC。Audius 的 `url` mode 属于 `lilt-audio`：

```text
urlPlay {
  url, title, artist?, duration?, providerID?,
  playbackGeneration: uint64, transportSessionID: string
}
urlStop { playbackGeneration: uint64, transportSessionID: string }
```

`URLQueueTransport` 在 server 侧处理 next/previous/jump、公开 queue 和 lazy resolution；`lilt-audio`
的 `url` mode 只处理当前 item 的音频输出、位置和结束通知。helper 的 URL、队列和状态是运行期数据；
server 不持久化签名 URL。完整 wire 细节见 [`helper-rpc.md`](helper-rpc.md)。

## 7. Audius auth（Phase 3）

匿名 discovery/playback 不需要授权；Audius `AuthProvider` 在未连接账号时报告 `not_determined`
（不是 `not_required`，因为可选账号能力仍可发起）。账号功能按官方 OAuth 2 Authorization Code +
PKCE 实现 browser/loopback flow（端点见 [`../client-api/extending.md`](../client-api/extending.md#14-audius正式可选-source)）：

- 用户创建 Audius developer app、注册 `http://localhost:<port>/callback` redirect URI；server 可读取
  `LILT_AUDIUS_API_KEY`、`LILT_AUDIUS_REDIRECT_URI`（默认 `http://localhost:8765/callback`）、
  `LILT_AUDIUS_OAUTH_SCOPE`（默认 `read`）。read scope 也可用 `app_name=lilt`。
- server 生成 state 与 PKCE verifier/challenge（S256），**先绑定 pending flow 再把浏览器 URL 交给 client**；
  callback MUST 校验 state，否则拒绝。
- access/refresh token 只放 `internal/securestore`（macOS Keychain；测试用内存实现），绝不进入 state、
  日志、watch event、error 或 Client API response。
- access token 过期时用 refresh token 刷新；disconnect MUST 先删除本地 token，远端 revoke 尽力执行，
  失败不恢复本地 token。
- 未配置 developer app（无 `LILT_AUDIUS_API_KEY`）时 `authorization.begin audius` MUST 返回
  `authorization_failed` 并给出配置指引，不得打开一个必然失败的授权页；匿名功能不受影响。

OAuth 不改变匿名曲目的可播放性。账号型 capabilities 需要独立表达 `authorization_required`，
不得把整个 `audius` source 标为不可用。当前未声明账号型 capability，因此 `authorization.list`
连接后为 `authorized`（带 account label），未连接为 `not_determined`。

## 8. 当前 state / identity 归属

- 稳定 Item identity 由 `internal/api` 统一实现：Apple Music 使用 `am:<provider-id>`，Audius
  使用 `audius:<kind>:<provider-id>`，Radio 使用 `radio:<normalized-url>`；短期媒体 URL 不写入
  公开投影或持久数据。
- Favorites、完整 Playback History 与派生 Recent 由 Activity SQLite store 持有，按 source 和
  稳定 identity 区分 Item；不再使用 `RecentContainer` 或 state.json 中的收藏列表。当前 schema 与
  测试门禁见 [`local-activity.md`](local-activity.md)。
- `state.json` v3 只保存偏好；读取旧 v2 Activity 字段时忽略，不将开发期测试数据迁入 SQLite。
  版本与持久化规则以 [`state.md`](state.md) 为准。新增来源不应重复 Phase 1 的旧迁移。

## 9. 错误映射

provider MUST 只向 Client API 暴露下列稳定错误，原始 HTTP/status 只可脱敏放入
`details.providerCode`：

| 操作 | 条件 | code |
|---|---|---|
| discovery / playlist | malformed ref、未知 kind | `invalid_reference` |
| discovery / playlist | upstream 4xx/5xx、rate limit、timeout、malformed body | `search_failed` |
| `discovery.search` | 未传 `source` | `invalid_request` |
| `discovery.search` | 该 source 未声明所请求 search capability（含 `source:"radio"`） | `unsupported_command` |
| PreparePlayback | source 尚未声明 playback capability | `unsupported_command` |
| PreparePlayback | ref 跨 source 或不可播放 track | `invalid_reference` |
| initial playback | plan/transport 启动或首次 URL resolution 失败 | `playback_error`，`details.state` 为 stopped |
| mid-queue resolution | URL 过期/403 后重取仍失败 | `source_unavailable`，当前会话转 stopped |

context cancellation 保留为 server deadline/cancellation，不映射为一个伪造的 provider 成功。错误路径不得
恢复旧 source 或旧 queue。

## 10. Audius HTTP 资源契约

Phase 1/2 只使用官方 `https://api.audius.co/v1`。搜索、单曲、歌单和 resolve 的具体 schema MUST
由 fixture 固定。播放资源使用 `GET /v1/tracks/{track_id}/stream`：请求 `no_redirect=true` 时以
`{"data":"<media-url>"}` 读取短期 URL；普通请求的 redirect 行为不得作为 JSON schema 假设。
provider MUST 验证 track 可播放性，且为 URL 过期/403 与 malformed response 提供稳定映射。

## 11. 实施与验收

| Phase | 范围 | 完成条件 |
|---|---|---|
| 0 | 本设计及关联规范 | 文档、链接检查通过；不改实现 |
| 1 | Audius REST discovery、provider registry、fixture、gate | **已完成**（门禁通过）：`--source audius` search/playlist 可用；`just provider-gate` 通过 |
| 2a | server foundation：activeSource/generation、PreparedPlayback、URLQueueTransport、Audius lazy stream URL | **已完成**：内部 seam 与 hermetic test 可用 |
| 2b | `lilt-audio` `urlPlay`、session/generation wire 校验与公开路由 | **已完成**：`play audius:*` 可播放且公开为 `full`；有限队列 v1 可用 |
| 2.5 | URL 队列编辑 | **已完成**：remove/move/add/clear 与 `ifQueueRevision` 语义完整 |
| 3 | OAuth/Keychain/账号能力 | **已完成**：真实账号验收通过（授权、`/v1/me` account label、disconnect 删除本地凭据）；refresh/revoke/错误路径由 hermetic 覆盖 |
| 4 | TUI、skill、产品文档 | **已完成**：TUI 有 Audius Search/Recent/Favorites 与歌单详情；skill 可选择并播放 Audius；UI 与文档完成 |
| J0 | Jamendo 设计与决策记录（[`jamendo.md`](jamendo.md)） | **已完成**：文档与链接检查通过；不改实现 |
| J1 | Jamendo 凭据（securestore + `lilt jamendo setup`）与 REST discovery | **已完成**：provider 注册、gate、完整歌单分页、setup 与 body-code 错误映射均有 hermetic 覆盖 |
| J2 | Jamendo 播放：`PreparePlayback` + URLQueuePlan + `mp32` 惰性解析 | **已完成**：媒体 URL 惰性解析且不公开，有限队列、source 互斥和错误映射有 hermetic 覆盖 |
| — | SoundCloud（**否决**） | 注册 app 需 Artist Pro、所有 client 为 confidential、播放仅 HLS 且需鉴权、Terms 禁止跨来源聚合；理由见 [`jamendo.md`](jamendo.md) §2 |
| — | Jamendo radios（`/radios`、`radios/stream`，**未排期**） | 连续流语义，见 [`jamendo.md`](jamendo.md) §9 |

每个 Phase 的 done MUST 同时包含：代码、hermetic 测试、对应文档更新、`just verify` 与文档
链接检查。任何不满足这些条件的 Phase 只能标为 in progress。

## 12. Links

- [`../architecture.md`](../architecture.md) — 组件与 server 所有权
- [`sources.md`](sources.md) — Browse、identity 与队列公开语义
- [`helper-rpc.md`](helper-rpc.md) — helper wire 协议
- [`../client-api/models.md`](../client-api/models.md) — 公开 Source / PlaybackState 模型
- [`jamendo.md`](jamendo.md) — Jamendo 凭据、discovery、播放与错误映射
- [`../client-api/extending.md`](../client-api/extending.md) — 新增 source 的实现步骤
- [`../testing/provider-admission.md`](../testing/provider-admission.md) — provider 门禁
