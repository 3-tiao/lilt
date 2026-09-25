# 实现契约：诊断日志（journal）

`internal/journal` 是 lilt 唯一的本地操作日志：JSON lines，默认 `info`，操作者可显式开启 `debug`
用于本地排障。本文件是日志格式与脱敏规则的权威；CLI/watch 的事件清单也在这里，其他文档只链接。

## 1. 级别与开关

| 环境变量 | 取值 | 效果 |
|---|---|---|
| `LILT_LOG_LEVEL` | `info`（默认）/ `debug` | 选择级别；非法或未设置按 `info` |
| `LILT_DEV_LOG` | `1` | `debug` 的别名 |

- 关闭 `info` 或 `debug` 之外的级别模型；将来新增级别只扩展这一个开关。
- 路径由 `LILT_LOG` 决定；默认 `~/.local/state/lilt/log/lilt.jsonl`（`XDG_STATE_HOME` 生效）。
- 5 MB 轮转为 `.1` 一份。复测/长会话场景请把 `LILT_LOG` 指向该轮的独立文件。

## 2. 事件与关联

每行是 `{"ts": …, "kind": …, …}`。用 Client API 的 `requestId` 与状态 `sequence` 跨层关联，不另造 id。

| kind | 级别 | 写入者 | 关键字段 |
|---|---|---|---|
| `cli` / `cli.exit` | info | CLI | `args`（仅 count+command）、`cwd`、退出 `code` |
| `cli.rpc` / `cli.response` | debug | CLI | `command`、`requestId`、`ok`、`errorCode`、`ms` |
| `rpc` / `rpc.start` | info | server→helper | `method`、`ms`、`ok`、`error` |
| `server.request` | debug | server | `requestId`、`command`、`ok`、`errorCode`、`ms`、`params`、`stream?` |
| `watch.publish` | debug | server | `event`、`sequence` |
| `key` / `mouse` | info | TUI | `key`/`event`、`source`、`view`、`overlay`、`queueFocus`、`selectedKind` |
| `tui.run` / `tui.quit` / `tui.panic` | info | TUI | 生命周期与崩溃栈 |
| `server.warning` / `server.panic` / `engine.*` / `activity.*` / `recent.*` / `radio.cache*` | info（panic 含栈） | server | 失败面 |
| `serve.start` / `serve.quit` / `tui.start` | info | CLI | 进程生命周期 |
| `helper` / `audio-helper` / `apple-resource` | info | CLI | helper stdout 行 |

一轮完整链路：`cli`（命令）→ `cli.response`（`requestId`）→ `server.request`（同 `requestId`）→
`rpc`（helper）→ `watch.publish`（`sequence`）；TUI 侧另有 `key`。

## 3. 脱敏边界

- **`info`（默认）**：用户内容保持私有——`term`/`query`/`value`/`reference`/`title`/`line` 记成
  `{kind,length}`，URL 只留 scheme+host+path；`args` 只留 count+command。
- **`debug`**：操作者显式选择全保真，记录完整 params（凭据字段仍脱敏）、结果大小、搜索词、标题与短期/签名媒体 URL。
- **任何级别**：凭据/密钥都不得写入——字段名命中 `secret`/`token`/`password`/`authorization`/
  `keychain`/`credential`/`api_key`（大小写不敏感的子串，含嵌套 map）以及 HTTP `Authorization`
  头，一律写成 `[redacted]`。这条不受级别影响。

## 4. 复现一轮

```sh
LILT_LOG_LEVEL=debug LILT_LOG=/tmp/lilt-round-<name>/lilt.jsonl just run
lilt log 200            # 读默认路径；指定路径时直接读文件
```

真实 MusicKit 的时间线探针（`LILT_PLAYER_TIMELINE=1` → `/tmp/lilt-player-timeline.log`）与
usability 装置的 `manifest.txt`/`keys.log` 见
[`../../testing/integration.md`](../../testing/integration.md)。
