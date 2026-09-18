# Client API —— 扩展来源

本文件说明如何在不破坏现有 client 的前提下新增内容来源（以 Audius 为具体例），以及
内置电台这一特殊 radio 候选来源。

数据模型见 [`models.md`](models.md)，命令见 [`commands.md`](commands.md)。

## 1. 新增 Source

新增 source 时 MUST：

1. 注册唯一 `id`、`label`、`priority` 和动态 `availability`。
2. 声明 capabilities；不具备的命令返回 `unsupported_command`。
3. 定义稳定的 Item `id` 和规范 `ref`，不得与已有 source 冲突；公共 `kind` 只能为
   `song|playlist|station|stream`（Audius track 是 `song`），原生类型进 metadata。
4. 实现并注册编译期 `ContentProvider`；它负责 discovery、identity/ref，不直接拥有公开播放状态或
   持久化短期资源。完整分层见 [`../internals/providers.md`](../internals/providers.md)。
5. 仅当声明 `playback.*` 时，实现 `PlaybackPreparer` 以产生 transport-specific 私有 plan，并映射到
   现有或新增 playback transport；server 在开始会话时显式写入 active source/generation，而不是从
   内部 mode 推断或检查私有 plan。discovery-only source 可以延后这一步。
6. 注册 auth provider：不需授权的 source 也 MUST 报告 `not_required`，使
   `authorization.list` 与 `sources.list` 一致；需要授权时按 `AuthorizationFlow` 异步启动、查询与
   取消，实现幂等 disconnect，并把授权映射为 `SourceAuthorization`。系统不允许程序化撤权时
   disconnect 返回 `unsupported_command` 与人工指引。
7. 把播放状态映射为公共 `PlaybackState`，不把 provider 专有字段塞进顶层。
8. 遵守 source 互斥与有限队列不变量：新 source 开始播放前停止当前 source、替换/清空旧
   队列；启动失败保持 stopped，不恢复旧 source/queue；不得跨 source 混队或 mid-queue fallback。
9. 增加 contract tests：search 形状、play 的 resulting state、auth flow/watch 顺序、错误
   映射、source 不可用时的 fallback metadata。准入条件、自动注册完整性门禁与人工验收
   清单见 [`../testing/provider-admission.md`](../testing/provider-admission.md)。

### 1.1 授权与凭据安全

- provider 独占交互以及凭据的 refresh/revocation；凭据和 token 只能写入 OS secure
  storage（Keychain 或平台等价物），绝不写入 `state.json`、日志、watch event、错误或
  Client API response。
- browser OAuth MUST 使用 loopback callback，并校验 state 与 PKCE。provider 支持动态
  redirect port 时优先绑定 `localhost` 随机端口；要求精确预注册 redirect URI 时允许使用
  已注册的固定 loopback port。端口策略属于 provider 配置，不得削弱 state/PKCE 校验。
  不适用 browser flow 时可使用 device code；具体流程必须映射到公共 flow 模型。
- disconnect MUST 先删除 lilt 持有的本地凭据；远端 revoke 可尽力执行，失败只能产生
  warning，不能把已删除的本地凭据写回。

### 1.2 命名与引用

- capability 名使用 `域.能力` 小写形式（`search.songs`、`playback.full`）。已登记的标准名称
  必须保持语义；新增名称必须以 source id 命名空间开头（例如 `audius.reposts`），并在该 provider
  的 descriptor/fixture 中定义语义。
- descriptor 与每个 capability 可带可选 `description` 供 agent/skill 参考；它是非规范性文字，
  不参与路由或判定。discovery 路由只读 descriptor：未声明的 search capability 返回
  `unsupported_command`，MUST NOT 另维护一份并行“支持列表”。
- Item 的 `source` 是 source id；`ref` 前缀与之相同（`audius:song:<provider-id>`；原生 track
  映射为公共 kind `song`）。
- `discovery.search` 的 `source` 在 wire 上必填（CLI 可默认 `apple-music`）；radio 发现走
  `radio.search`，不属于 `discovery.search`。播放 ref 一律是 `source:kind:id`，不接受裸
  `kind:id`。

### 1.3 客户端影响

新增 source 需要同时：

- 在 server 注册 ContentProvider、播放路由与 auth provider；
- 在 TUI 增加对应 tab 与 BrowseNode 组合（见
  [`../internals/sources.md`](../internals/sources.md)）。

本接口不承诺“安装一个 provider 插件后 TUI 无代码自动生成界面”。公共 Item、search、
playlist、queue、playback、watch 模型保持不变，因此现有 skill 与 CLI 无需改动
即可通过 `--source` 使用新来源。

### 1.4 Audius（正式可选 Source）

Audius 不是 test-only provider，而是正式、用户可见且可选的 Source，也是参考真实
integration/E2E provider。使用官方 `https://api.audius.co/v1` REST API 提供 discovery（search、
tracks、playlists），并在 item 上暴露 `https://audius.co<permalink>` 规范公开 URL；播放由
server-owned URL 队列 + `lilt-audio` 的 `url` mode 完成；可选账户能力以官方 OAuth 2 Authorization Code + PKCE
实现（见下）。不得对 Audius 使用 yt-dlp、cookies 或废弃的 `discoveryprovider.audius.co`。

- `authorization.list` 对未连接账号的 Audius 报告 `not_determined`（匿名 discovery/playback 不受影响）；
  连接后为 `authorized` 并带 account label。OAuth 使用官方端点：授权
  `GET /v1/oauth/authorize`（`response_type=code`、`scope=read|write`、`api_key` 或 `app_name`、
  `redirect_uri`、`state`、`code_challenge`、`code_challenge_method=S256`），token/refresh
  `POST /v1/oauth/token`，revoke `POST /v1/oauth/revoke`，资料 `GET /v1/me`（Bearer）。
  配置来自 `LILT_AUDIUS_API_KEY`、`LILT_AUDIUS_REDIRECT_URI`、`LILT_AUDIUS_OAUTH_SCOPE`。token refresh/revoke
  与凭据存储遵守本文件的安全规则及 [`commands.md`](commands.md#6-会话授权与生命周期)：只写
  `internal/securestore`（macOS Keychain），绝不进入 state、日志、watch event、error 或 response。
- 公共 discovery 无账户可用，查询带 `app_name=lilt`；账户 capability 独立以
  `authorization_required` 报告。不要臆造未确认的账户 endpoint。
- Item identity/ref 都为 `audius:<kind>:<provider-id>`；公共初始 kinds 仅 `song`、`playlist`。
- **Phase 2 已实现**：`PreparePlayback`、server-owned URL queue 与官方
  `/v1/tracks/{id}/stream?no_redirect=true` 的 lazy resolution；`lilt-audio` 私有 `url` mode 与公开路由均已接入，
  Audius 声明 `playback.full`/`queue`。播放启动时 MUST 使用该 endpoint 重新取得短期 media URL，并拒绝
  不可播放（`is_streamable=false`）track 或 malformed response；不得持久化该 URL。URL 过期/403 重取一次，
  仍失败：初始失败返回 `playback_error`，mid-queue 失败返回 `source_unavailable`。完整有限播放对 client
  投影为 `mode:"full"`、`source:"audius"`、`isLive:false`；`lilt-audio` 的内部 URL queue mode 不泄露到 Client API。
- 它的 Browse、queue、auth 与错误映射必须覆盖 shared contract，详见
  [`../testing/integration.md`](../testing/integration.md)。

## 2. 内置电台

内置电台**不是**新 source：它与 directory 共用 radio 播放路径，区别只在候选
来源。

- 它被表示为 `radio` source 下 `origin=builtin` 的 Item；builtin/directory 是 radio
  的 origin/provider，不是公共 Source。
- 数据来源：vendored m3u snapshot（实现为 `internal/builtin/streams.m3u`，随二进制
  内嵌）。每条含 `name`、`url`；tags/language 匹配与配置文件覆盖是未来扩展。
- station snapshot/list 派生自 [cliamp](https://github.com/bjarneo/cliamp) /
  [cliamp.stream](https://cliamp.stream/)，当前上游列表地址为
  `https://radio.cliamp.stream/streams.m3u`，不是 lilt 创建或拥有。
- provenance（upstream、list URL、retrieval timestamp、SHA-256、免责声明）由
  `internal/builtin.ProvenanceInfo()` 提供，并有测试断言 snapshot 的 SHA-256 与之
  一致；不得虚构 commit/hash。刷新必须显式、经 review，验证规范化 URL 唯一，并绝不
  形成对 cliamp directory service 的运行时依赖。
- 归属与免责：文档、TUI 信息与 `lilt api` MUST 归属 cliamp/cliamp.stream，并声明无 affiliation
  或 endorsement、无可用性保证，station audio rights 属于第三方；cliamp code license 不授予
  station content 权利。
- 可用性：`radio.search` 的 builtin 查询不依赖 Radio Browser；只有流本身需要
  网络。目录不可达不会影响 builtin。
- identity 与 ref：与任何 radio 流一致，identity 是 `radio:<normalized-url>`，
  ref 是流 URL，因此收藏、recent、favorites 天然复用现有规则。

内置电台的价值：在网络目录不可达或 Apple Music 不可用时，仍提供确定性较高的
精选台，是 skill fallback 链（见 [`README.md`](README.md#来源选择规则)）的稳定
一环。

## 3. 内置电台数据文件（未来 schema 草案）

当前实现使用内嵌 m3u snapshot；以下 TOML 覆盖 schema 是未来扩展，尚未实现。

```toml
# builtin-stations.toml
[[station]]
name = "Lofi"
url  = "https://radio.cliamp.stream/lofi/stream"
tags = ["lofi", "chill"]
language = ""

[[station]]
name = "Synthwave"
url  = "https://radio.cliamp.stream/synthwave/stream"
tags = ["synthwave", "electronic"]
```

字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | 是 | 显示名；同时用于名称匹配 |
| `url` | 是 | 流地址（HTTP/HTTPS） |
| `tags` | 否 | 匹配与展示用标签 |
| `language` | 否 | ISO 语言码 |

同一 URL MUST NOT 重复；identity 由规范化 URL 决定，与目录台一致。

成熟度与覆盖规则（目标契约，尚未实现）：配置目录中的同名文件**整体替换**内嵌
默认列表，不做逐条合并，避免隐式冲突；用户文件解析失败时回退内嵌列表并显示
warning。
