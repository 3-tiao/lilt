# 集成测试设计 / Integration Testing

> **状态**：分层已落地。Apple Music 与 Radio（builtin + Radio Browser）有确定性
> contract/集成覆盖；真实 provider E2E 为 opt-in。Audius 的 discovery、播放与账号 OAuth 的 hermetic
> 覆盖已完成（另含 opt-in `LILT_AUDIUS_E2E=1` 真实 discovery/stream，以及一次人工真实 OAuth 验收）。

## 1. Purpose / scope

测试分两层：真实 provider E2E 验证公开协议与主要降级；确定性 contract suite 验证所有
Source 共享的 Client API 契约。完整接口见 [`../client-api/README.md`](../client-api/README.md)，
Source/queue 规则见 [`../client-api/models.md`](../client-api/models.md#有限队列不变量) 与
[`../client-api/commands.md`](../client-api/commands.md)。

## 2. Coverage matrix

| Provider / fixture | Real E2E 覆盖 |
|---|---|
| Apple Music | `system_dialog`、subscription/capabilities、native finite queue、full/preview |
| Audius | mock REST search/playlists/error mapping、URLQueueTransport、helper URL playback、OAuth（PKCE/refresh/revoke/secure store）；opt-in 真实 discovery/stream 与人工真实 OAuth 验收 |
| Radio Browser | no auth、directory/filter/paging、health probe、live stream/metadata、partial outage |
| builtin radio | vendored deterministic fallback，独立于 directory |
| user URL | direct stream identity/error |
| fixture provider + mock upstream | OAuth reject/state mismatch/callback timeout/device code/token expiry/refresh fail/malformed payload/rate limit/timeout/disconnect/concurrency/source switch failure |

Audius real path 只使用官方 REST APIs 与 OAuth 2 Authorization Code + PKCE；不用 yt-dlp/cookies。
真实账户能力仅在官方 API capability 可用且已授权时测试，不能假定未确认 endpoint。

## 3. Tiers / secrets

1. **Hermetic CI contract suite（默认）**：fixture provider 与 mock upstream，无网络、无
   真实 Keychain/凭据；使用进程内 fake secure store 验证凭据生命周期契约。
2. **Real integrations（opt-in）**：显式环境开关，并满足 provider 环境与 Keychain 前置条件。
   不存储 secrets、tokens、authorization code 或 PKCE verifier；已注册 callback URL 是配置而非
   secret。前置条件不满足时以带原因的 **skip** 标记，绝不伪造 pass。
3. 真实测试避免生产账户 mutation；能读则不写，必须创建的本地测试状态在 cleanup 删除。

## 4. Shared contract suite

每个 Source 都必须覆盖：`api.describe`/Source capabilities、identity/ref、search shape、play/result
state、single-source queue invariant、watch ordering、auth state、error mapping、disconnect。fixture 必须
从公开 API 验证 provider discovery 与播放传输的路由边界；不可把短期签名 URL 当作持久 Item
字段，并扫描 state、recent、favorites、public response、watch event 与测试日志。有限队列
还验证 `QueueState.source` 与每项/`PlaybackState.source` 一致、错误 source 的 `queue.add/ref`
返回 `source_mismatch`、source switch 不恢复旧队列。并发 discovery/auth 可以跨 Source；播放不可以。

## 5. Real E2E acceptance / cleanup

真实 E2E 至少验证一次可观察 happy path 与表中主要 degradation。结束时 MUST stop playback，删除
测试创建的本地 state/cache/fixture 资源；不得自动撤销用户的 Apple system permission。Audius
disconnect/revoke 仅在隔离的测试账户且测试明确要求时执行。所有 skip、环境要求与 cleanup 结果必须
可审计。

## 6. Boundary

真实 provider 覆盖 happy path 和 major degradation；mock 覆盖需要确定性重现的全部分支。两者互补，
真实成功不能替代 mock 的错误断言。

## 7. Links

- [`provider-admission.md`](provider-admission.md) — provider 接入准入条件与门禁边界
- [`../internals/providers.md`](../internals/providers.md) — provider/discovery 与播放传输分层
- [`../client-api/README.md`](../client-api/README.md) — Source priority 与 API 目录
- [`../client-api/extending.md`](../client-api/extending.md) — Audius auth、cliamp builtin provenance
- [`../internals/sources.md`](../internals/sources.md) — Browse/identity/queue
- [`../internals/state.md`](../internals/state.md) — local state 与 Keychain 边界
- [`../internals/radio-discovery.md`](../internals/radio-discovery.md) — Radio Browser health behavior
