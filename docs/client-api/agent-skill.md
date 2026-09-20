# AI Agent 接入指南

lilt 可以被编码 agent（opencode、pi 等）以自然语言控制。本文件说明接入方式、
编排原则与扩展点。Authored skill 见仓库 `skills/lilt/SKILL.md`
（`just agent-install` 安装到 opencode 全局 skills 目录）。

> **分发注意**：安装到用户机器的 skill 是**自包含**的，不引用仓库内 `docs/**`
> 路径（外部用户没有这些文件）。skill 只写操作的简明说明；权威、机器可读的命令
> 目录由程序运行时提供：`lilt api --json`（命令名、参数 schema、返回模型、稳定
> 错误码，无需 server 运行）。本页的 `docs/client-api/**` 链接只面向仓库读者。

## 1. 接入形态

> **实现状态**：本指南与已实现的 Client API v0.1 一致。`lilt stop` 只停止播放，
> `lilt quit` 才结束服务；`lilt status` 默认只返回无队列的 `PlaybackStatus`，
> 需要队列时用 `lilt status --queue --json`。

lilt 只要求 agent 能执行 shell 命令并解析 JSON：

```sh
lilt serve --detach --json     # 无界面后台服务；已有服务返回 active_session
lilt play <ref> --json         # 播放；命令会在无服务时自动启动 serve 并重试一次
lilt status --json             # 读取当前状态（默认不含队列）
lilt stop --json               # 停止播放，服务保留
lilt quit --json               # 结束服务
```

若 `lilt` 不在 `PATH`（在仓库里工作时）：仓库根的同名二进制就是 CLI（`just build-go` 产物），
直接 `./lilt …`。签名 helper 默认在二进制旁边、或仓库的 `player/Build/Products/Release/` 下
自动找到，`LILT_PLAYER_PATH` / `LILT_AUDIO_PATH` 只在非默认位置时才需要设置。

不需要 MCP 或常驻插件；Client API 的 JSON 输出即接入面。所有命令的权威清单由
`lilt api --json` 给出（见 [`README.md`](README.md)）。

## 2. 编排原则

1. 一律使用 `--json`，只解析 `ok` 与稳定错误码（见
   [`errors.md`](errors.md)）。
2. 遇到 `no_active_session`：`lilt serve --detach --json` 后**重试一次**原命令。
3. 每次改变播放状态后 `lilt status --json` 确认，并向用户一句话汇报（播了什么 +
   为什么）。只报一个决定，不罗列候选。
4. 不要启动 `lilt tui`（那是给人用的全屏界面）。
5. 不要在未被要求时 `lilt quit`（会停止用户正在听的音乐）。
6. **provider-first**：先确定 source 再操作。`discovery.search` 的 wire 请求必填
   `source`（CLI 默认 `apple-music`）；radio 发现用 `radio.search`，不是 `--source radio`。
   来源能力与可选 `description` 读 `lilt sources --json`，命令级 `description` 读
   `lilt api --json`。这些文字只作线索，分支逻辑只看稳定 code / capability。

## 3. 来源选择

默认按“最优来源”选择，规则与优先级见
[`README.md`](README.md#来源选择规则)：

- Apple Music full 可用时优先；否则 Audius full；再否则 radio stream；
- radio 内部先用内置精选台（确定性高），再用 Radio Browser；
- 用户明确指定来源时不跨来源 fallback。

## 4. 常见意图 → API 组合

| 用户说 | 建议做法 |
|---|---|
| 播放〈艺人〉的歌 | `search --source apple-music --type all` → 命中该艺人的歌单则 `play <item.ref> --shuffle`（Apple 声明 shuffle）；否则用同一 Source 的歌曲 `play-songs <ref,...> --shuffle --repeat all`；Audius 等未声明形态能力的来源不要传这两个参数 |
| 播放〈歌名〉 | `search --source apple-music <歌名>` → 匹配后 `play <item.ref>`；Apple 无匹配/不可用则显式 `--source audius` 重搜 |
| 播放 Audius / 独立音乐 | `search "<term>" --source audius --type all --json` → 选 song/playlist → `play <item.ref> --json`；trending 用 `trending --source audius`；匿名可用，账号连接可选 |
| 播放〈流派/氛围〉 | Apple Music 歌单优先；再 `--source audius` public 歌单/歌曲；再 `radio search --tag <tag> --origin builtin` 后播放流 |
| 放个电台 | `radio search`（内置优先）或 `search --source apple-music --type station`（Apple 目录） |
| 暂停 / 切一下 / 下一首 | `pause` / `toggle` / `next` |
| 循环播放 | 单曲 `play <item.ref> --repeat one`；多首 `play-songs <refs> --shuffle --repeat all`。**两者都只在来源声明 `shuffle`/`repeat` 时传**（当前仅 Apple Music；Audius 与 radio 不声明，传了会报 `unsupported_command`，不要重试） |
| 查看/编辑队列 | `queue`（查看）、`queue add <ref> --next\|--append`、`queue remove <index>`、`queue move <from> <to>`、`queue jump <index>`、`queue clear`（仅 Apple/Audius 有限队列） |
| 歌单里有什么 | `playlist <ref>` 取曲目，再 `play` 或 `play-songs` |
| 专辑里有什么 / 放整张专辑 | `albums [--source S]` 列出资料库专辑，`album <ref>` 取曲目；播放用 `play <album-ref>`（整张）或 `play-songs <ref,...>` |
| 收藏/最近 | `favorites [--source S]`、`recent`（后者是**跨 source 的 lilt 本地历史**） |
| 停止音乐 | `stop`（只停播，服务保留）；只有用户要“退出服务”时才 `quit` |

不要把歌单和歌曲同时推荐给用户；选一个最合适的执行。

## 5. 并发与安全

- skill 与 TUI 可能同时在操作。skill 的顺序操作通常无需乐观并发控制；带 index
  的队列操作应读取最新队列后再执行（见
  [`commands.md`](commands.md) 的 `ifQueueRevision`）。
- 幂等命令（`pause`/`stop`/`queue clear` 等）可安全重试；播放启动等非幂等命令
  在结果未知时**不要**用新 requestId 重放，先查询状态
  （`operation_outcome_unknown`）。同 requestId 的 `duplicate_result_unavailable` 也不
  得重放：结果已不可取，先读状态再决定。
- source 报 `authorization_required` 或 authorization status 需要操作时，MUST NOT 自行
  发起交互式授权；告知用户运行确切的 `lilt auth <source> --json` 命令。只有用户明确
  请求授权时才可执行它。
- `lilt auth disconnect <source>` 会停止该 source 的当前播放并删除本地凭据；skill 只有
  在用户明确要求退出、断开或切换账号时才能执行。

## 6. 扩展

- Audius 已完成 discovery、播放、可选账号与 TUI/skill 集成；agent 用 `--source audius` 搜索，随后播放
  item canonical ref。账号连接不应阻塞匿名使用；见
  [`extending.md`](extending.md)。
- 需要机器可读的命令/错误目录时读取 `lilt api --json`，不要硬编码命令表。
