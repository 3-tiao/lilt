# 排查故障

[English](../en/guides/troubleshooting.md) | 简体中文

先看日志，再用 `status` / `sources` / `lilt log` 的证据判断是哪一层（client / server / helper /
上游 provider）出问题。真实会话的只读取证流程见
[`../testing/integration.md`](../testing/integration.md) 的“已发生的真实会话排障”。

## 先收集证据

```sh
lilt status --queue --json   # mode、当前 track、队列、错误状态
lilt auth status apple-music --json  # Apple Music 授权状态
lilt sources --json          # 每个 source 的 capability 与 available:false 的原因
lilt log 100                 # 最近 100 条 JSON 日志（默认 50）
lilt doctor                  # macOS：诊断 MusicKit token 有效性，不打印 token 内容
```

日志默认在 `~/.local/state/lilt/log/lilt.jsonl`（`LILT_LOG` 覆盖），每行是带 `ts` 和 `kind` 的
JSON，5 MB 轮转；报障时用 `lilt log` 分享会话。默认级别 `info` 不写原始搜索词与元数据标题，URL
只保留 scheme/host/path。

需要完整复现一次操作时显式开启开发诊断日志：`LILT_LOG_LEVEL=debug`（或 `LILT_DEV_LOG=1`）会记录
完整请求参数、watch 事件与 TUI 按键；**凭据/密钥在任何级别都不会写入**。级别、事件清单与脱敏
边界见 [`../internals/troubleshooting/journal.md`](../internals/troubleshooting/journal.md)。

## 按错误码判断

`error.code` 是稳定契约，`message` 面向用户、可能变化。完整列表与建议动作见
[`../client-api/errors.md`](../client-api/errors.md)。常见几类：

| code | 含义 | 怎么办 |
|---|---|---|
| `session_unavailable` | socket 或 server 内部不可用 | 重新连接；必要时重启 `lilt serve` |
| `active_session` | 已有 server 占用 socket | 直接用现有会话，不要重复启动 |
| `authorization_required` | 来源需要授权 | 运行 `lilt auth <source> --json` |
| `unsupported_command` | 当前来源不具备该 capability | 换命令或换来源，**不要重试** |
| `source_mismatch` | 混了不同 Source 的 ref | 只用同一 Source 的 canonical ref |
| `operation_outcome_unknown` | 命令超时且可能已产生副作用 | **不要重放**；先 `status` |
| `engine_restarting` | engine 正在重建，命令确定未执行 | 稍后重试一次 |
| `state_save_failed` | `state.json` 未持久化，内存权威状态未变 | 显式命令会提示用户 |
| `storage_unavailable` | Activity 存储失败 | 播放继续；检查存储后必要时显式运行 `lilt data reset --confirm` |

## 常见现象

- **Apple Music 只播试听（`mode:preview`）**：可能是未授权、无订阅、或浏览器模式下 storefront
  与订阅区域不一致（原生试听通常约 30 秒，浏览器引擎实测约 90 秒）。先用
  `lilt auth status apple-music --json` 查看授权状态，不要用 `mode` 反推授权。
  macOS 用 `just doctor` 查 token；浏览器模式登录见
  [`../getting-started/first-playback.md`](../getting-started/first-playback.md)。
- **TUI 显示 `working…` 很久**：有限队列在逐首填充时会显示 `working… 9/16` 进度；单个起播会显示
  目标曲名。这是真实进度，不是卡死。
- **自动换曲时 stop/status 暂时无响应**：有限 URL 队列的换曲与失败重试和控制命令串行执行，
  控制不会立即抢占。达到[执行与清理预算](../client-api/protocol.md#4-超时预算)后，server
  清空队列、发布 stopped，并在 `lilt log` 与 watch 记录 `source_unavailable`；音频清理是尽力操作。
  若超过预算仍无响应，收集现有日志，不要重复发送起播命令。
- **歌曲没进 Apple 云端的“最近播放”**：`lilt recent` 是 **lilt 本地播放历史**，不读 Apple 的
  `lastPlayedDate`；Apple 个性化接口在本机失败是已接受限制，见
  [`../product/limitations.md`](../product/limitations.md)。
- **Radio 目录不可用**：Browse 显示目录不可用并记日志；仍可用内置精选台，或稍后 `r` 重试。
  探测失败**不阻止**播放，直接按 `Enter`/`p` 仍会尝试。
- **切来源后短暂状态错位**：provider 切换瞬间的旧状态尾巴是已接受限制（约 3 秒），见
  [`../product/limitations.md`](../product/limitations.md)。
- **Esc 后按键无反应**：终端把 `Esc` 与紧跟的字符解析成 `alt+<char>`；单发 `Escape` 正常。帮助层
  只由 `Esc`/`?` 关闭；`q` 直接退出 TUI，其他键保持惰性。

## 复现与验证

- hermetic 测试不访问网络、不依赖真实账号：`just test`、`just verify`、`just provider-gate`。
- 真实播放需要显式 opt-in 与批准的可听窗口，且不能与日常 server 并发；边界见
  [`../testing/integration.md`](../testing/integration.md) 与
  [`../../AGENTS.md`](../../AGENTS.md)。
- 待决的工程问题（含证据与下一步）在
  [`../product/open-questions.md`](../product/open-questions.md)。
