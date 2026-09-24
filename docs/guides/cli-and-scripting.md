# 用 CLI 与 JSON 做脚本

`lilt` 是唯一入口：TUI、agent skill 与脚本都走同一个 CLI 和它的稳定 JSON 信封。人类可读用法是
`lilt help`；机器可读的权威目录（命令、参数 schema、返回模型、稳定错误码）是 `lilt api --json`，
**无需 server 即可离线运行**。命令逐条规格见 [`../client-api/commands.md`](../client-api/commands.md)。

## JSON 信封

所有 `--json` 命令输出两种形状之一：

```json
{"ok":true,"requestId":"...","data":{}}
{"ok":false,"requestId":"...","error":{"code":"no_active_session","message":"no active lilt server"}}
```

脚本只依据 `ok` 与 `error.code` 分支；`message` 面向用户、可能变化。稳定 code 的含义与建议动作见
[`../client-api/errors.md`](../client-api/errors.md)。

## 自动启动与生命周期

- 需要 server 的命令在无 server 时会自动启动 `lilt serve` 并重试一次；只有自动启动失败
  （`session_unavailable`）才需要处理。
- `lilt tui` 无 server 时启动、有则附着；`lilt stop` 只停播放；`lilt quit` 关掉 server。
- 非幂等命令不重放：结果未知（`operation_outcome_unknown`）时先读状态，不要换 `requestId` 重放。

## 常见脚本片段

```sh
# 一次性搜索（不启动 TUI），拿 JSON
lilt search "Nujabes" --json

# 搜索并播放
lilt search "Nujabes" --play

# 播一个 canonical ref（source:kind:id）
lilt play apple-music:song:1440845629 --json

# 播多首、组成当前会话的临时队列
lilt play-songs audius:song:1,audius:song:2 --shuffle --repeat all --json

# 播放控制
lilt pause --json; lilt resume --json; lilt next --json; lilt stop --json

# 队列（按当前队列 index 操作；带 ifQueueRevision 更安全）
lilt queue --json
lilt queue add audius:song:3 --next --json
lilt queue remove 2 --json

# 状态、收藏与历史
lilt status --queue --json
lilt favorites --json
lilt favorite add apple-music:song:1440845629
lilt history --json

# 来源与能力（capability 是唯一真值）
lilt sources --json
```

只有来源声明了对应 capability，`shuffle`/`repeat` 等形态参数才可传；未声明会返回
`unsupported_command`，不会被静默忽略。队列类操作只对有限队列（Apple Music / Audius / Jamendo）
有意义，直播流没有队列。

## 输出与日志

- 日志是 JSON lines，默认写到 `~/.local/state/lilt/log/lilt.jsonl`（`LILT_LOG` 覆盖）；
  `lilt log [n]` 打印最近 n 条（默认 50），文件 5 MB 轮转。
- 原始搜索词与元数据标题不写日志；URL 日志去掉 userinfo、query 与 fragment，只保留安全的 host/path。
- 报障时用 `lilt log` 分享会话，见 [`troubleshooting.md`](troubleshooting.md)。
