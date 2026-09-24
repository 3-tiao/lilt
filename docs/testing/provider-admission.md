# Provider 接入准入 / Provider Admission

> **状态**：已落地自动注册完整性测试与本地准入命令。真实账号验收仍为人工流程；
> 在仓库转为 public 并配置受保护分支之前，本准入不阻塞 merge。Audius Phase 1–4 已实现；
> Jamendo Phase J0/J1/J2 已完成；凭据与错误语义见
> [`../internals/providers/jamendo.md`](../internals/providers/jamendo.md)。

本文件定义“新增一个内容来源（provider/source）”的准入条件。它是
[`integration.md`](integration.md) 中测试分层的执行细则，也是
[`../client-api/extending.md`](../client-api/extending.md) 第 1 节的验收清单。

## 1. 前提：只允许源码接入

provider MUST 以 Go 源码形式实现并随 server 编译，由 `lilt serve` 在启动时注册
（见 [`../internals/providers/providers.md`](../internals/providers/providers.md)）。lilt **不提供**运行期
provider 插件协议：外部脚本不能把新 source 注册进运行中的 server。

CLI、TUI 与 AI skill 只能通过 Client API v0.1 使用已注册的 source，不能成为 provider
的注册入口。因此“接入 provider”等价于“提交一个实现 source 契约的代码变更”。

## 2. 门禁由两部分组成

| 部分 | 位置 | 是否自动 | 作用 |
|---|---|---|---|
| 结构完整性 | `internal/server/provider_gate_test.go` | 是 | 阻止半注册、自相矛盾的 source |
| 语义与真实行为 | provider 自己的 fixture / 集成测试 + 人工验收 | 部分 | 阻止错误的 ref、形状、错误映射、鉴权行为 |

### 2.1 自动部分：注册完整性

`provider_gate_test.go` 通过公开 API（`sources.list`、`authorization.list`）反向校验，
不依赖 provider 是否实现。它要求：

- `sources.list` 与 `authorization.list` 的 source 集合完全一致；每个 source 只出现一次。
- 非保留 source：`label` 非空、`priority > 0`、`availability` 属于已知枚举。
- 至少声明一个 capability；capability 名必须是已登记标准名称，或以该 source id 开头的
  namespaced 名称；不可用的 capability 必须带 `reason`。
- `descriptor.available` 必须与 capabilities 的推导结果一致。
- `authorization.status` 属于已知枚举。
- 契约保留但未实现的 source **不得**出现在任一列表；一旦注册，测试失败，
  作者必须把它从 `reservedButUnimplemented` 白名单移除，随后上述全部检查对它生效。

半接入（只加常量、只加 descriptor 不加 auth、capability 写错名字）无法通过该测试。

### 2.2 语义部分：provider fixture 必测项

以下 MUST 由 provider 自己的确定性 fixture 覆盖，落在 `go test ./...` 覆盖范围内：

- **identity/ref**：每个 item 有 canonical `ref`，前缀等于 `source`；非法 ref 返回
  `invalid_reference`，跨 source queue ref 返回 `source_mismatch`。
  Audius fixture 还必须断言 `audius:<kind>:<provider-id>`，防止 song/playlist provider ID collision；
  Jamendo fixture 必须断言 `jamendo:<kind>:<numeric-id>` 及同一原因。
- **search/library 形状**：分页、空结果、limit、第三方未知或缺失字段被安全忽略。
- **错误映射**：401 / 403 / 404 / 429 / 5xx / 超时 / context 取消映射为稳定 `api.Error` code，
  不 panic、不泄露 provider 原始 body。任何上游错误文本必须在跨入公开 response、watch、状态或日志边界前
  脱敏；上游把错误码放在 HTTP 200 body 内时（如 Jamendo），fixture 必须覆盖 body code 与传输层错误的区分。
- **auth**（需要授权时）：begin → completed/failed/cancel、credential removal（disconnect）幂等、token 失效
  可理解、断线后 flow 状态可恢复；凭据与 token 不得进入 `state.json`、日志、watch event、错误或 response。
  交互预算由 provider 通过 `AuthFlowBudget` 声明，未声明时使用 server 默认 2 分钟；context deadline 必须
  落为 `expired`，不能混成用户 `cancelled` 或 provider `error`。
- **播放与状态隔离**（仅声明 `playback.*` 的 source）：source 互斥、有限队列不变量、失败不污染
  recent / favorites / state。
- **短期资源**（仅声明 `playback.*` 的 source）：签名播放 URL 等短期资源在播放启动时重新解析，
  不进入持久 Item、state、日志或 fixture；URL 过期/拒绝必须有稳定错误映射。
- **capability 一致性**：不支持的 capability 返回 `unsupported_command`，不静默降级；声明不得早于可验证
  证据，例如只有页面 EME/Widevine 探测成功后才能把 DRM full playback 当作已确认能力，负向证据必须撤销声明。
- **注册路径**：fixture 必须通过真正的 provider registry 取得 descriptor 和 discovery，不能只以
  手工构造的 API response 通过 gate；声明 `playback.*` 后还必须取得 `PreparePlayback`。
- **private plan 边界**（仅声明 `playback.*` 的 source）：transport fixture 必须证明 server/state/
  watch/log 投影不读取或输出 plan 内的短期 target；只允许 transport 解析其私有 payload。
- **playback 身份**（仅声明 `playback.*` 的 source）：所有会改变状态或推进队列的通知必须同时携带
  playback generation 与不可复用的 transport session 身份；零值、缺失或旧身份不得推进当前队列。
  fixture 必须覆盖 source switch、启动失败、helper restart、外部媒体键与阻塞后迟到通知。
- **机器级资源**：浏览器 profile 等跨 state root 共享的资源必须定义所有权（谁创建、谁可删除）与跨进程
  互斥；fixture 必须证明外部资源不会被接管或删除，且并发进程不能同时使用同一资源。

真实的 happy path 与主要降级按 [`integration.md`](integration.md) 覆盖。

### 2.3 不进入阻塞 CI 的部分

真实 OAuth / 真实播放 E2E 依赖账号、网络与系统弹窗，会 flaky，MUST 以带原因的 **skip**
或人工验收记录存在，不得伪装成 pass，也不得作为阻塞性 CI 条件。

## 3. 凭证与数据安全

- 无真实账号、token、authorization code、PKCE verifier、用户数据或真实 API 响应内容进入
  仓库、fixture、日志或用例名。
- hermetic 测试 MUST 不访问网络；上游用 mock transport。
- provider 暂标 **experimental**；完成一次个人真实账号验收（授权、搜索、播放、disconnect）并
  记录脱敏结果、日期与 provider API 版本后，才可标 **stable**。

## 4. 本地运行

```text
just provider-gate
```

等价于 `go test -race ./...` 与 `go vet ./...`，与 CI 的 Go 检查一致，便于提交前自检。
文档工程师同步 provider 文档后，由文档测试工程师检查事实与链接；`just verify` 验证代码门禁，
不替代文档审阅。流程见 [docs-maintenance](../../.agents/skills/docs-maintenance/SKILL.md)。

## 5. 边界

- 本门禁保证 provider **不破坏 lilt 自身的结构、API、状态与安全边界**；它**不能**保证第三方
  服务的真实可用性。真实可用性只能由真实账号验收确认。
- 在仓库为 private 且未配置受保护分支前，本门禁是“自动检测 + 人工自觉”，CI 会变红但不阻止 merge。
  转为 public 并配置 required check 后才是硬性合并门禁。
- 当出现第三、四个账号型 provider、测试逻辑明显重复时，再把重复部分提炼为通用 conformance suite；
  在此之前不为单个 provider 预先抽象。

## 6. Phase 完成标准

一个 provider 实施 Phase 只有同时完成下列事项才可标为 done：

1. 对应代码已实现，且不留仅文档声明的 capability。
2. hermetic fixture/contract 测试覆盖该 Phase 的成功与失败语义。
3. `architecture`、provider/source、helper RPC、公开 API 模型和测试规范中受影响的文档已同步更新。
4. `just verify` 与文档链接检查通过。

缺少任一项时状态 MUST 为 in progress。该规则适用于 Audius 及后续所有 provider。

## 7. Links

- [`integration.md`](integration.md) — 测试分层与真实 E2E 设计
- [`../client-api/extending.md`](../client-api/extending.md) — 新增 Source 的契约步骤
- [`../client-api/models.md`](../client-api/models.md) — SourceDescriptor / Capability 模型
- [`../internals/providers/providers.md`](../internals/providers/providers.md) — provider/discovery 与播放传输分层
- [`../internals/providers/sources.md`](../internals/providers/sources.md) — Browse / identity / queue
- [`../internals/persistence/state.md`](../internals/persistence/state.md) — 本地状态与凭据边界
