# Spec: Jamendo provider

> **状态**：Phase J0（设计）、J1（凭据 + discovery）与 J2（有限 URL 队列播放）已完成；J4
> TUI/skill 可见集成尚未开始。分层沿用
> [`providers.md`](providers.md)，准入沿用 [`../testing/provider-admission.md`](../testing/provider-admission.md)。
> **范围限非商业使用**，见第 8 节。

## 1. 决策记录

| # | 决策 | 结论 |
|---|---|---|
| D1 | 凭据模型 | 用户自建免费 Jamendo 开发者 app（默认 read-only plan）；**不内置、不共享 client_id** |
| D2 | 凭据配置方式 | 新增 `lilt jamendo setup`，CLI 进程内引导并直写 Keychain；不新增配置文件，公开 Client API 零改动 |
| D3 | 未配置时的公开状态 | `availability: unavailable` + `reason` 指向 setup；`authorization.list` 永远 `not_required` |
| D4 | 媒体 URL 生命周期 | 播放启动时懒解析，媒体 URL 只进私有 plan，不进 Item/state/fixture/日志 |
| D5 | 本次范围 | 只做有限队列（song/playlist）；`/radios` + `radios/stream` 延后 |
| D6 | SoundCloud | **否决**，理由见第 2 节 |

## 2. 为什么是 Jamendo，以及为什么否决 SoundCloud

| 维度 | SoundCloud | Jamendo |
|---|---|---|
| 注册门槛 | **Artist Pro 订阅**才可注册 app | 免费开发者账号，read-only plan 默认生效 |
| 公开读取凭据 | 所有 client 均视为 confidential，**必须有 client_secret**；每个请求需 OAuth token | 仅需 `client_id` query 参数，无 secret、无 token |
| 播放资源 | `/tracks/{urn}/streams` 只给 HLS，文档注明 **"needs to keep using authentication"**；AVPlayer 需要私有 header API 或服务端代理 | `audio` 是普通 MP3 直链（无签名、无过期），AVPlayer 直接可播 |
| 条款 | 明文禁止"与其它来源聚合的按需播放体验" | 非商业免费；要求署名与回链，未禁止聚合 |
| 额度 | 15,000 play/24h per client_id | 35,000 请求/月（non-commercial） |

SoundCloud 的三条硬约束（Artist Pro、confidential client、HLS 鉴权）叠加起来使
"个人可用的 CLI" 不成立，故否决；结论记录在此，不再重复评估。

## 3. 分层与所有权

Jamendo 是 discovery + plan preparation provider，沿用
[`providers.md`](providers.md) 第 2 节：它产出公开 `api.Item` 与 canonical ref，并把 ref 准备为
transport-specific 私有 plan。播放复用既有 `URLQueuePlan` 与 `lilt-audio` 的私有 `url` mode：
**不新增 transport，MUST NOT 修改 Swift helper**。server 仍是 active source、队列与状态的唯一 owner。

## 4. 凭据

- `client_id` 是**应用级**标识（出现在每个请求 URL 中），不是用户授权，也不是 secret。
- 获取：`devportal.jamendo.com` 注册开发者账号 → 创建 app（填写准确名称、描述与项目链接，
  Jamendo 要求 description 含应用链接）→ 默认 `read only` plan 立即生效。
- 存储：`internal/securestore`，service `lilt`、account `jamendo.client_id`。**不写配置文件**，
  MUST NOT 进入 `state.json`、Activity、日志、watch event、错误或 Client API response。
- 命令：`lilt jamendo setup [--client-id <ID>]`
  - 无参数时引导：打开 devportal 注册页、提示粘贴 client_id（client_id 不是 secret，无需隐藏回显）
  - 写盘前 MUST 调用一次 `GET /tracks?limit=1` 校验：`code 0` 通过；`code 5` 报凭据无效；
    `code 11` 报 app 已被停用；其他错误按第 7 节映射。校验失败 MUST NOT 写入
  - 已存在有效凭据时提示覆盖；写入后回显非敏感确认（例如 client_id 前 8 位）
  - `--client-id` 供脚本/CI 使用；仍然执行同一次校验
  - agent/skill 只有在用户明确要求配置 Jamendo 时才能执行 setup，MUST NOT 自行创建或替换凭据
  - 校验需要网络：这是用户显式发起的命令，不属于 hermetic 测试路径
- server 侧**惰性读取**（与 Audius `auth_audius.go` 的 `load()` 同款），因此 setup 之后新的 API
  请求可立即使用；已连接的 `session.watch` client 不会自动收到 `sources.changed`，必须重新连接。
  本次不为单个 keyed source 新增配置事件命令；通用化见第 9 节。
- Terms 规定 credential 个人持有、不得向第三方披露：lilt MUST NOT 内置任何 client_id
  （官方文档中给出的测试 client_id 已实测失效，返回 `code 11`，不能作为公开默认值）。

### 4.1 公开授权语义

Jamendo 没有需要用户授权的 capability（用户 OAuth2 见第 9 节 J3），因此：

| 命令 | 行为 |
|---|---|
| `authorization.list` / `authorization.status` | 永远 `not_required`（已配置与未配置相同） |
| 未配置时的 descriptor | `availability: unavailable`，`reason` 给出 `lilt jamendo setup` 指引；每个 capability `Available: false` 且带同一 reason |
| 已配置 | `availability: ready`，capability 按第 5 节声明 |
| `authorization.begin jamendo` | `unsupported_command`（没有 flow） |
| `authorization.disconnect jamendo` | 删除本地 client_id、幂等，之后回 `not_required`；远端撤销＝在 devportal 删除 app，由文档指引 |

状态语义 MUST 如实：缺配置不是欠授权，因此不使用 `authorization_required` / `not_determined`。

## 5. Discovery 契约

Base URL `https://api.jamendo.com/v3.0`，`format=json`，`client_id` 为 query 参数。

| lilt capability | 端点与参数 |
|---|---|
| `search.songs` | `GET /tracks?search=<term>&limit=<n>&offset=<o>` |
| `search.playlists` | `GET /playlists?namesearch=<term>&limit=&offset=` |
| `playlist.tracks` | `GET /playlists/tracks?id=<playlist-id>&limit=200&offset=`，循环到完整结果 |
| song 详情 / ref 解析 | `GET /tracks?id=<track-id>`（播放时另加 `audioformat=mp32`，见第 6 节） |

`search` 参数覆盖 track/album/artist 名、tags 与相似艺人，是唯一的自由文本入口；
`fuzzytags` / `tags` / `ccnc` 等标签与许可过滤暂不暴露为公共参数，只在需要时由 provider 内部使用。

J1 **不声明 `search.trending`**：公共 capability 目前不能表达"只支持 song、不支持 playlist"，而
Jamendo playlist 也没有 popularity/featured 排序。不得声明一个 generic client 无法正确路由的
半能力；若以后扩展 kind-specific capability，再接 `/tracks?featured=1&order=popularity_month`。

`playlist.tracks` MUST 自动分页到完整结果；不得把 Jamendo 单页最大 200 条静默当成完整歌单。
若上游忽略 offset 并重复返回同一页，返回 `search_failed`，避免无限请求与配额耗尽。

### 5.1 字段与投影

- `id`、`duration`、`position`、`artist_id`、`album_id` 在 JSON 中是**字符串**，Go 结构体 MUST 按
  string 解析后再转换；不得假设为数字。
- 只有可播放项才投影为 `api.Item`；无法确定可播放性的项 MUST 被跳过，不得返回"能搜到但播不了"的 Item。
- `Item.url` MUST 为 `shareurl`（canonical 页面 URL，如 `https://www.jamendo.com/track/<id>`）；
  MUST NOT 携带 `audio` / `audiodownload`。
- identity：`jamendo:song:<track-id>`、`jamendo:playlist:<playlist-id>`；ref 同形，`kind` 不可省略。
  稳定 id 与 ref 只能由 `internal/api` 的 `Identity` 产生。
- 归属（Terms 4.1）：Item 的 artist MUST 来自 `artist_name`；TUI 与 `lilt api` 输出 MUST 让
  Jamendo 作为来源可见，并保留回链 `shareurl`。`audiodownload_allowed=false` 时 MUST NOT 提供任何
  下载入口（lilt 不实现下载，因此只需忽略 `audiodownload`）。

## 6. 播放（Phase J2）

```text
jamendo:song:<id> / jamendo:playlist:<id>
  -> JamendoProvider 产出 URLQueuePlan（公开队列 + 私有惰性 resolver）
  -> server 提交 activeSource=jamendo，建立有限队列
  -> resolver: GET /tracks?id=<id>&audioformat=mp32 -> audio 直链
  -> lilt-audio urlPlay（单曲短期 target）
```

- `audioformat=mp32`（VBR 好音质）；不使用默认 `mp31`（96kbps），不请求 `flac`。
- `audio` 直链虽然无签名、无过期，仍 MUST 按短期资源对待：只出现在私有 plan 与
  transport payload 中，MUST NOT 进入 Item、公开队列、`state.json`、recent/favorites、watch event、
  日志或 fixture。
- 公开投影与 Audius 一致：`mode:"full"`、`isLive:false`、`source:"jamendo"`、有限 `duration`；
  queue 编辑（`add/remove/move/clear`）与 `ifQueueRevision` 语义复用现有 URL 队列实现。
- **封面 MUST NOT 阻塞起播**：Jamendo 封面托管对 22KB 图片实测需 1.2–3.2s，helper 早期实现先
  `await` 封面再 `startPlayer`，导致每曲开头静音数秒。现为：先起播，封面在后台拉取，到达后重新
  发布 now-playing（stop / 换曲通过 generation+session 作废该任务）。
- 空队列、启动失败、队列中的非 song 项、跨 source ref MUST 返回 `invalid_reference`，且不恢复旧
  source/队列。

### 6.1 上游不稳定：空结果与传输重置

Jamendo 的读接口会**对一个完全有效的请求返回空结果**（HTTP 200、`headers.status=success`、
`code=0`、`results_count=0`），且间歇性在响应中途重置 TLS。实测（2026-09-21）：

| 测量 | 结果 |
|---|---|
| 同一 `id` 查询重复 30 次 | 8–10 次空结果（约 30%） |
| 同一 `search` 查询重复 10 次 | 3–7 次空结果，且回传集合顺序不稳定 |
| 用 HTTP/1.1 与 HTTP/2 各重复 30 次 | 8/30 与 9/30——**与协议、连接复用无关** |
| 请求间隔 0.1s / 1s / 3s | 空结果比例无变化——**不是限流** |

空响应与“真的没有匹配”在 wire 上**完全无法区分**，所以唯一手段是**有界重试**：

- `readAttempts = 5`，退避 120/250/450/700ms。空结果只在重试时多花请求；最后一个尝试仍为空则
  按“真的为空”接受（搜索）或 `invalid_reference`（按 id 查询）。
- 传输错误、5xx、不可解析 body 同样重试；**body code 错误（5xx 之外）不重试**——被拒绝的
  client_id 不是抖动。
- `playlist.tracks` 的每页都过同一层：一个抖动的空页会被重试，因此不会静默截断歌单。

代价：真正无匹配的查询最多花 5 次请求（配额上限内）；收益：起播与搜索的用户可见失败率从约
30–50% 降到 1% 以下。

媒体侧同样不稳定：`prod-1.storage.jamendo.com` 对同一首 mp32 的实测吞吐在 20–90KB/s 之间波动，
而 128kbps 需要约 16KB/s，因此有时在开头就拉不动。此时 AVPlayer 不会报错，而是落到“停止推进”，
helper 曾把它报成 `paused`，于是 server 的 stall 看门狗把它当成“用户在休息”，结果是：位置永远
停在 0、无错误、无重试、无声音（用户实测中遇到的就是这个）。现在的处理：

- helper 把未暂停却不再推进的会话报成 `buffering`（`paused` 只表示真暂停）。
- server 的 stall 看门狗把“用户没暂停却报 paused、且位置仍是 0”的会话也当作卡死，20s 预算到点后
  `RetryCurrent` 重新解析媒体 URL 并重播；再失败则结束会话并发 `server.warning`，不再无声冻结。
- 位置已推进后的暂停（包括系统媒体键）仍视为真暂停，不会被重试打断。

## 7. 错误映射

Jamendo 的错误模型是 **HTTP 200 + body**：所有响应都带
`headers{status,code,error_message,warnings,results_count}`，因此 MUST 先解析 HTTP 层（传输/超时/
5xx），再解析 body code。原始信息只可脱敏放入 `details.providerCode`。

| 条件 | code |
|---|---|
| malformed ref、未知 kind、队列内非 song 项 | `invalid_reference` |
| 缺失/无效 client_id（body `code 5`）或 app 被停用（`code 11`） | `authorization_failed`，message 指向 `lilt jamendo setup` |
| body `code 3`（type）/ `code 4`（required parameter）/ `code 8`（needed parameter）/ malformed body | `search_failed` |
| body `code 6`（rate limit exceeded） | discovery: `search_failed`；播放中: `source_unavailable` |
| HTTP 4xx/5xx、连接失败、超时 | discovery: `search_failed`；起播: `playback_error`；播放中: `source_unavailable` |
| context 取消 / deadline | 保留为 server 的取消语义，不映射成 provider 成功 |

`code 7`（method not found）与 `code 1`（generic exception）按 `search_failed` 处理。
错误路径 MUST NOT 泄漏 jamendo 原始 body、client_id 或媒体 URL。

## 8. 额度、许可与限制

- 配额：non-commercial plan 为 **35,000 请求/月**，按 client_id 计算；超出返回 `code 6`。
  provider MUST NOT 轮询上游，MUST 只在用户操作（搜索、翻页、起播、跳曲）时请求；每曲起播最多
  一次解析请求。
- 分页：`offset` / `limit`（max 200）；不请求 `results_fullcount`（文档注明其性能代价）。
- 非商业：Jamendo API 仅对非商业用途免费；广告、付费、affiliate 或其它以商业利益/金钱补偿为
  目的的使用在开始前 MUST 先取得 Jamendo 商业许可（`licensing@jamendo.com`）。公开分发本身不等于
  商业使用；lilt 的接入定位是**非商业使用**，
  该限制 MUST 同时写在 roadmap 与对外 skill 文档中。
- 每首曲目可能带 `license_ccurl`（如 `by-nc-nd`）；使用 MUST 遵守该 CC 许可。lilt 只做流式播放、
  不做缓存、不做离线、不做下载，符合 Terms 3.3。

## 9. Phase 与延后项

| Phase | 范围 | 完成条件 |
|---|---|---|
| J0 | 本设计与决策记录 | **已完成**：文档 + 链接检查通过；不改实现 |
| J1 | 凭据（`securestore` + `lilt jamendo setup`）与 discovery（provider 注册、search/playlist） | **已完成**：provider gate、完整歌单分页、注册路径、body-code 错误映射与 CLI setup 均有 hermetic 覆盖；CLI/API 可搜不可播 |
| J2 | 播放：`PreparePlayback` + URLQueuePlan + `mp32` 惰性解析 | **已完成**：惰性 `mp32`、公开队列不泄漏媒体 URL、有限队列、source 互斥、rate-limit 错误语义均有 hermetic 覆盖 |
| J3 | 用户 OAuth2（读自己的歌单 / favorites） | **未排期**；接入前确认 scope 与凭据存储 |
| J4 | TUI（source tab/BrowseNode）、skill、文档 | 与 Audius Phase 4 同标准 |

记录在案、明确不在本次范围：

1. **Jamendo radios**（`/radios`、`radios/stream`）：连续流语义，需要 radio 式播放路径，不是有限队列。
2. **setup 的通用化**：等第二个需要用户自备凭据的 source 出现时，再把 `lilt <source> setup`
   提升为公开 `interaction.type=input` + server-owned flow（届时 TUI 也能引导）。本次刻意不为
   单个 provider 预先抽象公开模型。
3. **Jamendo trending**：等公共 capability 能表达 kind-specific 支持后，再接 song-only featured/popular。
4. **Jamendo 用户 OAuth2**（J3）。
5. **SoundCloud**：已否决，理由见第 2 节。

## 10. 测试与安全边界

- hermetic：`httptest` 固定 body，覆盖成功、空结果、body-code 错误（0/3/4/5/6/7/11）、HTTP 5xx、
  超时与 context 取消；不得访问网络，fixture MUST NOT 含真实 client_id 或媒体 URL。
- 凭据测试使用 `securestore.NewMemory()`；`lilt jamendo setup` 的校验用假 HTTP transport。
- 播放测试沿用既有 URL 队列 fixture 形状，断言媒体 URL 不进入公开投影与持久状态。
- 真实账号验收是 opt-in，MUST 以带原因的 skip 表示，不进入阻塞 CI。

## 11. Links

- [`providers.md`](providers.md) — provider/discovery 与播放传输分层
- [`sources.md`](sources.md) — Browse、identity 与队列公开语义
- [`state.md`](state.md) — 本地状态与凭据边界
- [`../client-api/extending.md`](../client-api/extending.md) — 新增 source 的契约步骤
- [`../testing/provider-admission.md`](../testing/provider-admission.md) — provider 准入门禁
- [`../product/roadmap.md`](../product/roadmap.md) — 来源规划与非商业限制
