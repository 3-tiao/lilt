# Tech Design: Radio Discovery and Health Probing

**Status: health probing, typed directory metadata, local probe persistence, and TUI sorting are implemented. Mirror discovery beyond the static `de1`/`de2` fallback and click counting remain future work.**

> **所有权**：`radio-cache.json` 由 `lilt serve` 单一写入（探测成功与失败都会持久化），
> probe 执行经由 `radio.probe` 落在 server 的 engine 上。TUI 只发起探测并读取
> `radio.cache` 快照用于显示与离线 fallback，不再自己写盘。下文描述的行为契约不变；
> “当前进程”内的探测状态对 client 而言是 server 进程的运行期状态。

> 范围：本文件包含 2026-09 的只读实测证据，用于支撑结论；**契约以各节结论为准**，
> 日期性观测可能随目录服务变化而失效。

## Implemented

- Radio views are Favorites, Recent, Browse; Favorites is the default whenever
  Radio is entered.
- Browse defaults to `Popular Worldwide` via `topclick`.
- `/` from any Radio page opens Search & Filters: optional name text plus
  pending Language, Genre/tag, Country selectors plus Sort (`Recommended`, `Popular`, `Fastest`, `Name`). Option
  lists are locally type-filterable; `Any` clears one facet, Reset filters clears
  pending facets only, and Esc/Cancel discard edits. Confirm applies the query
  directly to Browse in place (title `Showing: ...`); reopening `/` from Browse
  pre-fills the current query. An empty Confirm restores `Popular Worldwide`.
  Conditions are session-only. `F` is inert in Radio so lowercase `f` remains
  favorite/unfavorite.
- Text and filled facets use advanced search AND semantics with `offset`/`limit`,
  `hidebroken=true`, `order=clickcount`, and `reverse=true`. Browse loads the
  query results directly; no temporary result page exists.
- Directory requests fall back from `de1` to `de2` with a 7s per-request timeout.
- Health probing: the TUI queues the visible `stream`/`station` rows (selected
 row first, then top to bottom), runs at most two probes at a time, and asks
  the helper's `radioProbe` for HTTP time to first byte (TTFB), including TLS
  and redirects. Probes never play audio, never touch the playback players or
  state, and are answered from a detached task so they cannot block
  `play`/`pause`/`radioPlay`/`shutdown`. The helper's own 10s timeout matches
  the playback-start guard and returns a normal `timeout` result; the 12s RPC
  deadline leaves room for that result without invalidating the transport.
- Rows render color, symbol, and text: `○ unchecked`/`○ queued` (dim),
  `◌ checking…` (yellow), `● <latency>` (green), `× TLS error`/`timeout`/
  `HTTP error`/`unsupported`/`network error`/`probe unavailable`/`probe failed`
  (red). No-color terminals still distinguish every state.
- Playing a station whose cached probe failed is allowed and announces the retry
  (`Retrying <name> — earlier probe failed (<reason>)`); a live stream that never
  reaches `playing` within 10s of a start or resume surfaces an error instead of
  buffering forever, and `Space` during `buffering…` pauses rather than being
  ignored. That 10s guard covers one start attempt: it is disarmed as soon as
  audio flows, and a later stall re-arms a fresh window, so pausing and resuming
  an already-playing stream must never report a start failure.
- Terminal probe states are cached by endpoint hash in the disposable Radio cache. Healthy and structural failures live for 24 hours; transient failures live for 10 minutes. Playing a failed station clears its cached failure for a manual retry. Cross-start results display their age; a failed probe never blocks `Enter`/`p` playback.
- Radio Browser records retain typed `stationuuid`, tags, languages, country, codec/bitrate/HLS, votes, click count/trend, and directory check fields. Station identity and endpoint health remain separate so a changed `url_resolved` is re-probed automatically.
- Favorites, Recent, playback, and custom stream URLs remain local and unaffected.

## Decision

Implemented: Radio Browse displays Radio Browser popular stations while probing visible stations in the background:

1. 目录请求使用 `hidebroken=true`，但不把目录检查结果视为本机可播放保证。
2. 列表先显示，探测后异步更新；探测不能阻塞 Radio Browse 首屏。
3. 只探测当前可见的电台，最多同时运行两个探测。
4. 探测使用带 player-like User-Agent、`Accept: */*`、禁用缓存的 HTTP GET；收到首个响应数据字节即成功并取消请求，不调用 `play()`，不得产生声音或改变当前播放状态。
   这是音频 endpoint probe；目录 JSON 请求仍使用 JSON `Accept` header。二者刻意不同，
   不得合并或移除。
5. 延迟定义为 `resume()` 前到首字节的耗时（TTFB），包含 DNS/连接、TLS 和重定向；不是 ICMP ping、普通 HTTP 往返时间或播放器就绪时间。
6. probe 验证可达性、TLS 和 HTTP 状态，不验证 codec 支持；codec 和 bitrate 第一版使用 Radio Browser 声明值，不宣称已经通过解码验证。
7. 探测状态同时使用颜色、符号和文字表达，不能只依赖颜色。
8. Radio Browser endpoint 必须可替换；第一版不把动态镜像发现作为健康探测的阻塞条件。
9. Radio Browser 仍按 `clickcount` 提供候选集；TUI 可在已加载候选内选择 Recommended、Popular、Fastest 或 Name。Recommended 优先本机健康，再使用 clickcount/clicktrend，最后使用延迟与名称稳定打破平局。

## Motivation

Radio Browser 是社区维护的互联网电台目录。它提供名称、直播 URL、国家、标签、codec、bitrate 和服务器侧健康检查，但不托管音频，也不能保证某个流能在当前用户的 macOS AVPlayer 上播放。

`hidebroken=true` 只能减少明显失效的条目，仍可能遇到：

- 目录检查结果过期；
- TLS 证书链被 AVPlayer 拒绝；
- 直播 URL 根据地区、User-Agent 或 IP 返回不同内容；
- codec 或内容类型标记不准确；
- 电台暂时离线或启动速度很慢。

因此目录排序不引入人工精选系统，而是在 Radio Browser 目录上增加本机、低并发、渐进式的可播放性反馈。

`clickcount` 带来一个关键的概率筛选层。热门电台通常有更稳定的运营、更多活跃用户和更快暴露坏链接的机会，因此比名称顺序、随机顺序或历史 votes 更可能可用。它不是健康证明，但能显著提高进入本机 probe 队列的候选质量。

第一版选择管线固定为：

```text
language/countrycode/tag（可选）
  -> hidebroken=true
  -> clickcount descending
   -> 100 candidates per page
  -> probe visible rows only
   -> HTTP TTFB healthy/failed
```

这三层分别表达不同事实：`hidebroken` 是目录硬门槛，`clickcount` 是群体使用形成的可靠性先验，HTTP TTFB probe 是当前 Mac 的可达性验证，而非解码验证。

## Goals

- 用户打开 Radio Browse 后无需搜索即可看到可播放候选项。
- 优先展示更可能可用的高 clickcount 电台，减少无效 probe 和用户试错。
- 列表内容立即可浏览，后台检查不阻塞输入和播放操作。
- 给出本机 HTTP 可达性、首字节延迟和目录声明的音频信息。
- 严格限制并发、超时和探测范围，避免对公共电台产生过多连接。
- 探测失败不永久禁用电台，用户始终可以手动重试播放。
- 不影响正在播放的 Apple Music、preview 或 Radio 流。

## Non-goals

- 在**目录排序**中引入人工维护的 Featured/Curated 清单（内置精选台是独立的
  `origin=builtin`，见 [`../client-api/extending.md`](../client-api/extending.md)）。
- 对全部 Radio Browser 目录进行扫描。
- 自动播放、自动切换到下一个电台或产生可听音频。
- 测量完整音频质量、响度、丢包率、长期稳定性或实际听感。
- 跨设备同步目录 cache 或本机健康状态。
- 用探测结果替代 Radio Browser 的服务器侧健康检查。

## Radio Browse

### Paging

Radio Browse fetches 100 stations per page for both `Popular Worldwide` and
Search & Filters queries. When the cursor reaches within three rows of the end,
the next page loads automatically. Only an empty page ends paging: the client
hides broken and duplicate entries, so a short page can still have more. An
append failure keeps the loaded list visible and requires `G` to retry, so the
directory is not hammered.

Re-entering Browse with the same query paints the last in-session first page
immediately and refreshes it in the background (stale-while-revalidate). The cache
stores the directory candidate snapshot in directory order and is scoped to exact
directory query plus pagination, never local sort. Display applies the active local
sort to that candidate set. A background refresh updates membership, rows, and health
markers without automatically re-sorting the visible list; the selected station is
preserved by identity when it still exists. A failed refresh leaves the usable cached
page in place instead of showing an error. Changing query, pressing `Esc`, or explicit
reload starts a clean first page; changing sort only reorders loaded candidates and
never fetches.

Popular Stations 使用 Radio Browser 的 clickcount 排序。无筛选条件时使用官方 top-click 列表：

```text
/json/stations/topclick/100?hidebroken=true
```

Search & Filters 查询带语言、国家或标签条件时使用 advanced search，并保持相同排序。例如日语电台：

```text
/json/stations/search?language=japanese&languageExact=true&hidebroken=true&order=clickcount&reverse=true&offset=0&limit=100
```

支持的第一版筛选条件：

- `language` + `languageExact=true`；
- ISO 3166-1 `countrycode`；
- `tag` + 可选 `tagExact=true`。

Radio Browser 网站 URL 中的 `page=1` 属于网页 UI；JSON API 使用 `offset` 和 `limit`。TUI 的第一页是 `offset=0`，后续分页按 `offset += limit`。

`clickcount` 表示最近 24 小时的点击次数，不是历史累计值。第一版同时把它视为当前流行度和可用性概率信号，但不能用它替代 `hidebroken` 或本机 probe。

Sort 只作用于当前已经加载的候选集：Recommended 优先用户播放/收藏过的电台，其次 fresh healthy，再按 clickcount/clicktrend；Popular 纯按 clickcount/clicktrend；Fastest 按 fresh TTFB，未知项随后、失败项最后；Name 按名称。

probe 或后台 stale-while-revalidate 更新时不自动重排序当前列表，避免光标和内容跳动；
用户改变 Sort、加载下一页、显式 reload 完成或按 `S` 时才按当前 sort 重排，并按 identity
保留选中电台。因为启动排序时大多数行尚未测量，Fastest 标题在覆盖不完整时追加
`· N/M measured`，footer 提供 `S re-sort`，让用户能在后台探测完成后主动重排，而不是
面对一个名不副实的排序。

目录请求失败时，Favorites、Recent 和当前播放不受影响。Browse 显示错误提示，并允许刷新；目录失败不能让 Radio 进入不可用状态。

错误处理必须区分“自己的超时”与“目录确实不可用”：目录镜像常有数秒延迟，而每个镜像的 HTTP 预算只有 7 秒，所以超时通常不意味着用户网络坏。文案不得默认归因用户连接。重试必须真的可用：报错时再次选择当前 view 或按 `r` 都会绕过 session cache 重新请求（`r` 在任何列表页均可用）；有缓存 profile 时先用缓存的站点填充列表并标注来自缓存，无缓存才显示空错误页。

## API Compatibility and Runtime Reality

Radio Browser 官方建议客户端通过 DNS/SRV 发现镜像、随机选择节点并在失败后 failover。2026-09-14 的实际只读验证结果与该描述存在差距：

- `de1.api.radio-browser.info` 和 `de2.api.radio-browser.info` 均可用，top-click 结果及 UUID 数据一致。
- `_api._tcp.radio-browser.info` SRV 只公布 `de1`。
- `/json/servers` 只返回 `de1`，未公布可用的 `de2`。
- `all.api.radio-browser.info` 能解析，但 HTTP/HTTPS 连接被重置。
- 因此完整实现官方 discovery 当前仍可能只得到一个可用节点，不能把 discovery 成功等同于具备 failover。

第一版按实际价值划分：

| 能力 | 优先级 | 决策 |
|---|---|---|
| `hidebroken=true` | Required | 所有电台列表请求启用 |
| `url_resolved` | Required | 播放和探测使用目录已解析地址 |
| `stationuuid` | Required | 保留稳定目录身份，用于去重和后续 API 操作 |
| 描述性 User-Agent | Required | 保留 `lilt/<version>`；当前服务虽不强制，仍遵守官方礼仪 |
| 本机 HTTP TTFB probe | Required | 目录检查有 30 到 93 小时延迟，不能代替本机可达性验证 |
| `countrycode` | Recommended | 新代码优先使用，显示时允许 `country` fallback |
| `/json/stations/topclick` | Recommended | 与高级 search 排序实测等价，但语义更直接 |
| API mirror failover | Recommended | endpoint 保持可替换；第一版可使用 `de1` 并 best-effort fallback 到 `de2` |
| click counter | Recommended | 后台 best-effort 上报，不影响播放 |
| 重复过滤 `lastcheckok`/`ssl_error` | Not required | `hidebroken=true` 的热门 100 条实测均为 `lastcheckok=1`、`ssl_error=0` |

不使用旧 API 的数字 `id`。跨镜像身份使用 `stationuuid`；国家筛选优先使用 ISO 3166-1 `countrycode`。

用户从 Radio Browser 条目开始播放时，客户端应 best-effort 调用官方点击计数 endpoint：

```text
GET /json/url/{stationuuid}
```

该请求用于改善 Radio Browser 的流行度数据，同一 IP 对同一电台每天最多计数一次。它是生态协作要求，不是播放正确性的前置条件。若实现，必须在后台 best-effort 调用；计数失败不得阻塞或中断播放。自定义 URL 没有 `stationuuid`，不调用该 endpoint。

`url_resolved` 是 Radio Browser 已处理 playlist 和 HTTP redirect 后的直接地址，播放和探测优先使用它；原始 `url` 仅作为诊断和重新解析信息保留。

## Client Strategy

第一版继续维护 lilt 自己的小型、typed Go client，不引入 `gitlab.com/AgentNemo/goradios` 依赖。该库可以参考 endpoint 和字段命名，但当前版本不满足 lilt 的运行边界：

- 固定使用 `de1.api.radio-browser.info`，没有官方要求的镜像发现和 failover；
- 使用全局无 timeout HTTP client；
- 忽略 request、network、status 和 JSON decode 错误；
- 没有发送描述性 User-Agent；
- 部分字段类型和参数处理与当前 API 不一致；
- 项目自身将测试和 CI 列为待办。

lilt 现有 client 已具备 timeout、context、HTTP status 和 decode error 处理，并已实现静态镜像
failover（`de1` 主、`de2` 回退、7s per-request timeout）。实现本设计的其余部分时应在其上增加
动态镜像发现、完整字段和 click counter，而不是更换为功能及可靠性更弱的封装。

## Directory Data

电台候选项至少保留以下 Radio Browser 字段：

```text
Station {
  stationUUID
  name
  url
  urlResolved
  countryCode
  tags
  languageCodes
  codec
  bitrate
  hls
  lastCheckOK
  lastCheckTime
  sslError
  votes
  clickCount
  clickTrend
}
```

`stationUUID` 是 Radio Browser 网络内稳定的目录身份。第一版必须随 `core.Item` 保留该值，用于镜像切换、点击计数和去重。自定义 URL 继续使用规范化 URL 身份；是否将已有 Favorites 从 URL 身份迁移到 UUID 由 state schema 的独立迁移决定，不能在加载时静默改变用户数据。

`lastCheckOK` 是多个 Radio Browser 检查节点的多数结果，不表示最后一个请求的单点结果。`codec`、`bitrate` 和 `hls` 来自最近一次目录检查，其中 bitrate 的展示单位为 kbit/s。`votes` 是节点数据，`clickCount` 是最近 24 小时数据，两者都只用于目录排序参考，不能代替本机探测。

排序优先使用 `clickCount`，不使用 `votes` 作为第一版主要排序字段。votes 更接近历史认可度；clickcount 更能反映当前仍有人实际使用该电台。

进入列表前执行基础校验和去重：

- `urlResolved` 不是 HTTP/HTTPS 时丢弃；
- 优先按 `stationUUID` 去重，再按规范化 `urlResolved` 去重；
- `codec` 或 `bitrate` 缺失不丢弃，只显示 `Unknown`。

`hidebroken=true` 仍必须保留在请求中。第一版无需再次以 `lastCheckOK` 或 `sslError` 硬过滤返回结果；保留字段用于展示和诊断即可。必须继续过滤无效 URL，并执行 UUID/URL 去重。

## Probe State

每个规范化 Radio URL 在当前进程内对应一个探测状态：

```text
RadioProbe {
  status: unchecked | queued | checking | healthy | failed
  latencyMs?: integer
  errorCode?: string
  errorMessage?: string
  checkedAt?: timestamp
}
```

状态转换：

```text
unchecked -> queued -> checking -> healthy
                              \-> failed
```

终态结果会同时保留在当前进程与独立的 `radio-cache.json`。Endpoint health 以规范化 `url_resolved` 的 SHA-256 为 key，不保存 URL（包括 query/token）：健康结果保留 24 小时；`network` / `timeout` / `transport` 等暂态失败保留 10 分钟；HTTP、TLS、unsupported 等结构性失败保留 24 小时。每个 endpoint 最多保留最近 5 个健康样本的滚动平均，最多 500 条。Radio Browser station profile 另以 `stationuuid` 保存公开目录元数据和当前 endpoint key，保留 6 小时、最多 1000 条；自定义 URL 不写 station profile。过期结果重新进入低优先级探测队列。

## Probe Semantics

Swift helper 新增只读的 `radioProbe` 操作：

```json
{
  "jsonrpc": "2.0",
  "id": 42,
  "method": "radioProbe",
  "params": {
    "url": "https://example.test/live.m3u8",
    "timeoutMs": 10000
  }
}
```

成功结果：

```json
{
  "status": "healthy",
  "latencyMs": 382
}
```

失败结果：

```json
{
  "status": "failed",
  "errorCode": "tls",
  "message": "certificate rejected"
}
```

实现要求：

- 使用独立的 `URLSession` GET，使用 player-like `User-Agent` 和 `Accept: */*`，并禁用缓存。
- 从 `resume()` 前开始计时；首个 `didReceive data` 字节为健康成功标准，随后立即取消 task。
- HTTP `>=400` 为 `http`；2xx 完成但零字节为 `network`（`stream closed before sending audio`）；传输错误使用稳定 URL error 映射。`unsupported` 只表示非 HTTP(S) URL。
- 不调用 `play()`，不创建可听输出，也不修改 `streamPlayer`、`previewPlayer`、`ApplicationMusicPlayer`、`currentTrack`、`mode` 或 playback state sequence。
- 成功、失败、超时或取消后必须释放 session、task 和 delegate 引用；自身取消产生的 `NSURLErrorCancelled` 不得覆盖成功。
- helper 内部超时固定为 10 秒，与播放启动 guard 完全一致。公共 `radio.probe` 不接受
  `timeoutMs`；server 固定传 10000。internal helper RPC 的 12 秒 deadline 为正常 probe
  结果留出传输余量，不能使用常规 RPC deadline。
- 探测任务必须与播放命令隔离。正在探测的慢电台不能阻塞 `play`、`pause`、`radioPlay` 或 `shutdown`。
- RPC response 可以按完成顺序返回，但必须保留 request ID，并继续通过现有串行 writer 写入 socket。

第一版错误码建议：

| Code | 含义 |
|---|---|
| `timeout` | 10 秒内未收到首字节（与播放启动 guard 相同） |
| `tls` | TLS 或证书校验失败 |
| `http` | HTTP 状态或重定向失败 |
| `unsupported` | URL 不是 HTTP(S) scheme |
| `network` | DNS、连接或网络不可用 |
| `unknown` | 无法分类的传输错误 |

错误消息在进入终端前必须经过外部文本清理，日志不得记录 URL query、fragment 或 userinfo。

## Scheduling and Concurrency

TUI 负责调度待探测条目：

- 全局 worker 上限固定为 2，不按页面额外创建 worker pool。
- 只把当前列表窗口内可见的 `stream`/`station` 项加入队列。
- 当前选中项优先，其余按屏幕从上到下排列。
- 滚动后为新出现且状态为 `unchecked` 的条目排队。
- 离开 Radio source 或更换页面后，不再启动旧页面的 queued 项；**被丢弃的 queued 项必须同时从进程缓存中移除
  （恢复为 `unchecked`）**，否则再次进入该页面时会因「已知」而永远停留在 `queued`。
- 已经 checking 的任务允许在 10 秒内结束；返回结果通过页面 generation 校验，不能覆盖无关页面。
- 退出应用时 helper 释放全部探测任务。

探测是低优先级后台工作。播放命令永远优先于 queued probe；必要时可以暂停调度新 probe，直到播放命令完成。

## UI Contract

状态必须使用颜色加稳定文案：

```text
○ Power POP - unchecked - HLS - Türkiye
◌ Power POP - checking… - HLS - Türkiye
● RFM POP-ROCK - 0.4s · checked 3h ago - AAC 128k - France
× Dark City Signal - TLS error - MP3 128k
```

视觉语义：

| 状态 | 颜色 | 符号 | 必须显示的文字 |
|---|---|---|---|
| `unchecked` / `queued` | dim/gray | `○` | `unchecked` 或 `queued` |
| `checking` | yellow | `◌` | `checking…` |
| `healthy` | green | `●` | `<latency>`：`<100ms` 显示毫秒（`87ms`），否则显示秒（`0.4s`、`2.0s`）；跨启动复用的结果追加 `checked <age> ago` |
| `failed` | red | `×` | 简短错误，例如 `TLS error` |

终端不支持颜色时，符号和文字仍必须完整表达状态。

codec/bitrate 使用 Radio Browser 声明值，展示规则：

```text
AAC 128k
MP3 320k
HLS
Unknown
```

第一版不显示 `fast`、`slow` 或质量评分，避免引入未经验证的判断。延迟仅作为信息，不参与隐藏或禁用。

## Interaction

- `Enter` / `p` 始终可以播放任意健康状态的电台。
- 对 `failed` 项播放相当于用户主动重试，不被自动探测结果阻止。
- `f` 收藏行为与健康状态无关。
- Favorites 和 Recent 可以显示当前进程已有的探测结果，但不因失败自动删除。
- 第一版不增加新的必需快捷键；刷新可以复用页面 reload 行为。

## Resource and Privacy Limits

- 最大活跃 probe：2。
- 单 probe helper 超时：10 秒（与播放启动 guard 相同）；RPC deadline：12 秒。
- 每页候选列表上限：100；空页结束分页（目录会隐藏 broken/重复项，短页不代表结束）；offset 按固定 100 步进。
- 自动探测范围：仅当前可见项。
- 自动重试：同一 scope 内 0 次；过期的持久化记录在下次 view entry 重试，手动播放失败项会清除缓存。
- 持久化 cache：健康与结构性失败 24 小时；暂态失败 10 分钟；最多 500 条 endpoint hash。目录 profile 保留 6 小时、最多 1000 个 station UUID。
- probe 不发送 Apple Music token、用户身份或 lilt state。
- helper 是本地 `LSUIElement` radio client，必须接受、探测并尝试播放任意用户提供的 HTTP/HTTPS 电台 URL；单个公开流是否兼容 AVFoundation 仍取决于其媒体与 HTTP 行为（见 [`limitations.md`](../product/limitations.md#6-部分公开连续流不兼容-avplayer已接受)）。因此 ATS 只启用 `NSAllowsArbitraryLoads`（同时覆盖 URLSession probe 与 AVFoundation 媒体播放）。不能与 `NSAllowsArbitraryLoadsForMedia` 等更窄的键并存：并存时全局键会被系统忽略，http 电台会被 ATS 拒绝。请求仍仅使用 GET、无凭据、禁用缓存，并且不发送 Apple Music token 或 lilt state。
- 日志仅记录结果类别、延迟和 URL 的安全 host/path 表示，不记录 query、fragment、userinfo 或完整搜索输入。
  探测调度/启动/完成会记录 `probe` 日志（`event=schedule|start|done|paused`，含 queue/active 计数），
  用于诊断队列停滞；URL 经安全化处理。

## Failure Isolation

- Radio Browser 请求失败不影响 Favorites、Recent、自定义 URL 或当前播放。
- 单个 probe 超时不关闭主 RPC transport。
- 单个 probe 崩溃或异常不得改变当前播放状态。
- helper 断连时，所有 checking 项回到 `unchecked` 或显示 `probe unavailable`；播放状态仍按 playback-state-sync 的断连规则处理。
- TUI 页面切换后到达的旧 probe response 可以写入进程级 URL cache，但不得修改当前页面 selection、loading、message 或 navigation history。

## Testing

Go 测试至少覆盖：

- Popular Stations 使用 `/json/stations/topclick/100?hidebroken=true`，后续页增加 offset。
- 带 language/countrycode/tag 条件时使用 advanced search，并保持 `order=clickcount&reverse=true`。
- 同一 query 重进 Browse 先显示上次结果并后台静默刷新；刷新失败保留旧列表；换 query、`Esc` 或显式 reload 后不复用旧页。换 sort 不 fetch、不改变 cache identity。
- Popular 保持 clickcount/clicktrend 降序；Recommended 优先用户历史再 health；Fastest 按 fresh TTFB 并在覆盖不完整时显示 `N/M measured`；Name 使用去空白后的名称；`S` 重排保留选中项；probe 完成后不自动重排当前页面。
- API 分页使用 offset/limit，不向 JSON API 发送网页的 page 参数。
- 所有目录请求包含描述性 User-Agent 和 JSON Accept header。
- endpoint 可配置，节点失败能返回明确错误而不影响本地 Radio 内容。
- 目录字段解析、URL 校验和 UUID/URL 去重。
- 列表先于 probe 结果渲染。
- worker pool 活跃任务不超过 2。
- 只调度可见项，滚动后调度新增项。
- 页面切换后旧结果不覆盖当前页面。
- healthy/checking/failed/unchecked 的颜色、符号和文字。
- 同一 URL 在同一进程内不会被自动重复探测。

实现推荐项时追加覆盖：

- 主节点失败后切换 fallback，全部节点失败后才返回目录错误。
- Radio Browser 条目播放时 best-effort 调用 click counter，自定义 URL 不调用。
- `countrycode` 优先且 `country` fallback 正常。

Swift 测试至少覆盖：

- HTTP status/zero-data 分类，以及 success、failed、timeout 和自身取消后的 URLSession 清理。
- probe 不修改任何 playback-owned state。
- 两个 probe 可以并行，但第三个等待。
- 播放命令不会排在慢 probe 后面。
- 错误到稳定 error code 的映射。

依赖真实网络的 live probe 测试必须显式 opt-in；默认 CI 使用可控 URL protocol/mock asset，不依赖公共电台稳定性。

## Acceptance Criteria

1. Radio Browse 获取目录后立即显示 100 个 Popular Stations，不等待探测；空页结束分页。
2. 候选项经过 `hidebroken=true` 并按 clickcount 降序返回；有筛选条件时顺序语义不变。
3. 当前可见电台从 unchecked/queued 进入 checking，并最终进入 healthy 或 failed。
4. 任意时刻最多有两个实际探测任务。
5. healthy 项显示 HTTP 首字节延迟和目录声明的 codec/bitrate。
6. failed 项显示可理解的错误分类，并仍允许 Enter/p 播放。
7. probe 状态更新不改变当前页面条目顺序或光标位置。
8. 探测期间播放、暂停、切换 source、搜索和退出保持响应。
9. 探测不发声，不改变 Now Playing，不写入 Recent。
10. 离开页面后旧结果不破坏当前 UI。
11. 无颜色终端仍能通过符号和文字区分全部状态。
12. 默认测试和 CI 不连接公共 Radio 服务。

## Future Skill Contract（仅设计，未实现）

未来 Skill 不模拟 TUI 按键，而调用 typed Radio selection service。输入至少包含 `text`、`tags[]`、`languages[]`、`countryCodes[]`、`sort`、`limit`；返回所选 station UUID、resolved URL、匹配理由、health age 和备用候选。自然语言如“播放 city pop 电台”由 Skill 转成结构化 query；本轮不实现 CLI、自然语言解析、自动选择或失败后切换。

## Future Work

- 用实际播放后的 `AVPlayerItemAccessLog` 显示 observed bitrate。
- 对 HLS manifest 提取声明的 bandwidth/codecs。
- 基于本机成功率和启动延迟进行排序。
- `n` 尝试下一个健康结果或失败后的可选自动 fallback。
- 使用 station UUID 的小型人工精选清单。
- 当官方 discovery 能稳定暴露多个节点时，替换静态 fallback 为 DNS/SRV 随机镜像池。

## References

- Radio Browser API usage: <https://api.radio-browser.info/>
- Radio Browser API reference: <https://docs.radio-browser.info/>
- Evaluated Go wrapper: <https://gitlab.com/AgentNemo/goradios>
