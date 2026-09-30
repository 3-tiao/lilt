# Client API —— 错误

client 的分支逻辑 MUST 只依赖下表稳定 code；`message` 面向用户，可能变化。provider 的
原始错误不进 `message`：machine 侧原始 code 在 `details.providerCode`，原始文本在
`details.detail`（例如 helper 的 NSError 描述）。`playback_error` 的 `message` 是稳定文案
（当前为 “Playback could not be started”）。

| code | 含义 | client 建议动作 |
|---|---|---|
| `no_active_session` | 没有 server | CLI 可启动 serve 后重试一次 |
| `active_session` | 已有 server 占用 socket | 提示用户，不重复启动 |
| `invalid_request` | params 缺失、类型错误或值域错误 | 修正请求 |
| `invalid_state` | 命令合法，但当前播放状态没有可操作对象或不允许该转换 | 读取最新状态后再决定 |
| `duplicate_result_unavailable` | 相同 requestId 已执行，但缓存 body 已逐出 | **不得重执行**；读状态后再决定 |
| `invalid_reference` | ref 语法或资源类型错误 | 修正 ref |
| `unknown_command` | command 未注册 | 检查 `api.describe` |
| `unsupported_command` | 当前 source/engine 不具备该 capability | 换命令或换来源，不要重试 |
| `source_unavailable` | source 动态不可用 | 按 [`README.md`](README.md#来源选择规则) 回退 |
| `source_mismatch` | refs 的 Source 彼此不同，或与活动有限队列 Source 不同 | 不混队；使用同一 Source 的 canonical refs，或先显式开始另一 Source |
| `authorization_required` | source 需要授权 | 引导用户完成系统授权 |
| `authorization_in_progress` | 该 source 已有 active flow | 使用 `details.flowId` 查询或取消既有 flow，不要重复 begin |
| `authorization_flow_not_found` | flowId 未知或终态记录已过保留期 | 重新读取 source 授权状态；需要时发起新 flow |
| `authorization_failed` | flow 无法启动，或本地凭据读取/删除失败 | 保持原授权状态；显示错误，不自动重试交互 |
| `subscription_required` | source 需要有效订阅 | 告知用户；可回退到 radio |
| `queue_unavailable` | 当前 mode/source 无队列 | 不要执行队列操作 |
| `preview_unavailable` | 没有试听资源 | 换结果 |
| `preview_unsupported` | 试听模式不支持该播放控制（队列编辑统一返回 `queue_unavailable`） | 告知用户 |
| `queue_not_jumpable` | append 队列无法原地跳转；server 的一次性重建未成功，已核对队列与当前项仍和操作前一致（位置可能自然推进）。不保证未发生过重启；`details.state` 给出当前快照 | 从列表/专辑页该行重新起播；不要自动重试跳转 |
| `undo_unavailable` | 最近一次删除已过 5 秒、被后续删除取代，或播放/session 已变化，无法精确恢复；`details.reason` 为 `superseded\|expired\|playback_changed\|session_changed\|not_restorable` | 清除 Undo 提示；不得改用 add/move 猜测恢复 |
| `partial_failure` | 操作已产生副作用，但目标未完整达成；`queue.jump` 在重建失败或落点错误且播放/队列已变时返回，`queue.move` 在无法保住当前曲目或其进度发生重置时返回 | 读 `details.state` 并展示真实状态；不要自动重播或回滚音频 |
| `partial_failure`（`details.queueReady:true`） | 有限队列**已建好**但起播失败（re-pin 被 MusicKit 拒绝） | 队列保留在 `details.state` 且已提交；提示用户重按播放，不要重建队列 |
| `conflict` | `ifQueueRevision` 前置条件不满足 | 读 `details` 的最新队列后重新决定 |
| `playback_error` | provider/engine 播放失败 | 读 `message`；source 切换失败时读 `details.state`（最终 stopped 状态），不要假设旧源恢复 |
| `playback_stalled` | URL 队列媒体停滞/失败，正在重新解析当前项一次 | 仅出现在 journal（`lilt log`），不发布到 watch；等结果：恢复则无事发生，死项见下一行 |
| `playback_skipped` | 某队列项重试后仍死链，已自动跳到下一项（连续上限 2） | 无需处理，播放继续；读 `playback.changed`；连续第 3 个死项或最后一项死链会另发 `playback_error` |
| `search_failed` | 内容发现失败 | 可回退其他来源或重试 |
| `internal_error` | 命令处理器发生意外内部错误（已 recover，服务继续运行） | 重试一次；持续出现时把 `lilt log` 里的 `server.panic` 记录（含 stack）报上来 |
| `state_save_failed` | state 未持久化，权威内存状态未改变 | 显式命令提示用户；自动 mutation 另发 `server.warning`，不自动重试 |
| `storage_unavailable` | Activity 存储操作失败或无法打开；播放继续；自动历史写入失败还会发布 watch 警告 | 提示用户检查历史与存储状态；不可用时可显式 `activity.reset`（需确认），不要盲目重试写入 |
| `engine_restarting` | engine 正在重建，命令确定未执行 | 稍后重试一次 |
| `operation_outcome_unknown` | 命令超时且可能已产生副作用 | **禁止自动重放**；先查询状态 |
| `session_unavailable` | socket 或 server 内部不可用 | 重新连接；必要时重启 serve |

终止性播放错误带 `details.cleanupFailed:true` 时，`details.state` 的 stopped 只表示 server 已结束
该会话，不能当作实际音频已停止的证明；显示清理未确认，不自动重新起播。预算与所有权见
[`失败清理规则`](protocol.md#4-超时预算)。

错误响应结构见 [`protocol.md`](protocol.md#13-response)。

## 迁移说明

旧扁平 wire 格式与旧 CLI 已移除。provider 侧原始信息只出现在 `details.providerCode` 与
`details.detail`；client 的分支逻辑只依赖上表。
