---
name: music-control
description: 音乐与电台播放控制。当用户说"播放音乐 / 播放X的歌 / 来点pop / 放个电台 / 适合写代码的歌 / 暂停 / 下一首 / 停止音乐"等时使用。通过 lilt CLI 控制 Apple Music、Audius 与网络电台（macOS）。
---

# 音乐与电台播放控制（lilt CLI）

lilt 是本机的 Apple Music / Audius / 网络电台控制器。你（agent）**只通过 `lilt` CLI** 完成播放与
控制；不碰 TUI、server 或 `state.json`。

**本 skill 只写三件事：触发、策略、配方。**

- 命令名、参数、返回模型、稳定错误码 → `lilt api --json`（程序自身生成，无需 server）。
- 人类可读用法 → `lilt help`。
- 来源与能力（capability 是唯一真值）→ `lilt sources --json`。

这里**不重复接口清单**：重复会漂移，接口以命令输出为准。若某条策略其实是在绕开 CLI 的毛病，
那就该修 CLI，而不是写进 skill。

若 `lilt` 不在 `PATH`（例如在仓库里工作）：仓库根的同名二进制就是 CLI（`just build-go` 产物），
直接 `./lilt …`；签名 helper 会在二进制旁边、或仓库的 `player/Build/Products/Release/` 下自动找到，
无需设置环境变量。

## 原则（策略）

1. 所有命令加 `--json`，只解析 `{"ok":true,"data":…}` / `{"ok":false,"error":{"code","message"}}`。
2. **永远不要运行 `lilt tui`** —— 那是给人用的全屏界面。
3. **不用手动起 server**：需要 server 的命令在无 server 时会自动启动并重试一次。只有自动启动失败
   （`session_unavailable`）才需要如实报给用户。
4. 每次改变播放状态后用 `lilt status --json` 确认，并向用户**一句话汇报**（播了什么 + 为什么选它）。
   只报一个决定，不罗列候选。
5. **自动来源选择**：用户没有点名来源时，先读 `lilt sources --json`，在**具备所需 capability 且
   `available:true`** 的来源中按 `priority` 选：`playback.full` 的 Apple Music → `playback.full` 的
   Audius → radio stream。Apple 未授权/无订阅或不可用时才降级；radio 内部先 `origin=builtin` 再
   `directory`。只把**完整播放**当可播放，`preview` 不算。用户点名的来源永远优先。
6. **来源优先（provider-first）**：先定来源再搜索，只请求该来源声明了的能力。**API 原语不做隐式跨
   来源 fallback**——换源是你的决定，而且必须显式。

## 来源选择（用户未指定时）

按顺序决策，每步都用 `lilt sources --json` 的事实（capability 的 `available`），不要凭记忆：

1. **用户点名来源** → 直接用该来源；不可用就如实报错，不偷偷换源。
2. **具体歌名/艺人**：
   - 选优先级最高、`playback.full` 可用的来源（Apple Music → Audius）。
   - 在该来源搜歌曲；若无 title 精确/最接近匹配，或匹配项的艺人明显不符，**显式换到下一个来源**
     再搜一次（如 `--source audius`）。
   - 命中后播 `item.ref`；汇报要包含**最终选了哪个来源、为什么**。
3. **氛围/背景音乐**：先 Apple Music 的歌单，其次 Audius，再次电台。
4. **只想随便放点东西**（"放点音乐"）：Apple 资料库歌单，或 Audius trending，挑一个直接播。
5. **显式换源永远是 skill 的决定**：API 不会替你 fallback；换源前先确认目标来源的 capability。

Phrasing（用户这样说时）：
- "用 Audius 播放 X" / "苹果音乐放 X" / "用收音机放 X" → 固定 `--source`。
- "播放 X"（不点来源）→ 走上面的自动选择。

## 需要解释的事实（schema 里读不出来的）

- `lilt recent` 是**跨 source 的 lilt 本地播放历史**，不是某个 provider 的 recent。
- `lilt library` 是 provider 云端资料库歌单，需要 `library` capability（Audius 还需先连账号）。
- `lilt play-songs` 编的是**当前会话的临时队列**，不会在 Apple Music 等 provider 里建永久歌单。
- `lilt play <ref>` 的 `ref` 是 canonical `source:kind:id`；`shuffle`/`repeat` 与启动是**一个逻辑
  命令**（一次调用），但只在来源声明了对应 capability 时才能传——未声明会返回
  `unsupported_command`，不会静默忽略。
- 队列类操作（`queue` / `shuffle` / `repeat` / `next`）只对有限队列的 Apple Music / Audius 有意义；
  live stream 没有队列。

## Recipes（skill 层 preset）

这些 preset 是本 skill 的命名编排配方，不是 TUI、server 或 Client API 对象。它们通过
搜索和 `play-songs` 生成当前会话的临时队列，不创建 provider 中的永久歌单，也不要求 server
保存 usage。

**播放〈艺人〉的歌**
1. 按「来源选择」定来源；搜该艺人（Audius 加 `--source audius`，`--type all`）。
2. 优先歌单：`playlists` 里 `title` 或 `artist` 含该艺人名的（如"张信哲精选"）→ 播 `item.ref`；
   仅当该来源声明 `shuffle` 时再加 `--shuffle`。
3. 没有专属歌单 → 从 `songs` 里取 `artist` 字段包含该艺人名的前 10 首 → `play-songs`；
   仅当该来源声明 `shuffle`/`repeat` 时再加 `--shuffle --repeat all`。
4. `lilt status --json` 汇报（播了什么 + 来源 + 为什么）。
5. 排除规则：艺人名只出现在歌曲 `title` 里的翻唱/合辑不要选。
6. Apple Music 没有该艺人或不可播放 → 显式 `--source audius` 重搜一次再决定。

**播放〈歌名〉**
1. 用户点名来源就用它；否则按「来源选择」在 Apple Music → Audius 中选第一个 `playback.full` 可用的。
2. 首选来源：搜 `<歌名> --type song`，取 `title` 精确或最接近、`artist` 合理者。
3. 无合适匹配或该来源不可播放 → **显式**换下一来源（`--source audius`）再搜一次。
4. 播 `item.ref` → `lilt status --json`；汇报播了什么 + 最终来源 + 为什么（含是否发生了回退）。

**播放 Audius**：用户指定 Audius、要独立音乐或公开发现时，搜 `--source audius --type all`，从
`songs` 或 `playlists` 选择项目，随后播 `item.ref`（例如 `audius:song:<id>`）。也可用
`lilt trending --source audius` 拿现成 trending（想连续播就取前 N 首 `play-songs`，或直接播一个
trending 歌单）。Audius 匿名搜索和播放可用；账号连接是可选的，不要为了播放主动授权。
中文内容用英文关键词更准（`mandarin`、`cpop` 等）；直接搜 `中文` 常返回播客与 DJ 混音。
Audius 歌单可能自带重复曲目：播放后用 `lilt queue` 核对，必要时 `queue remove` 去重。

**循环 / shuffle**：只在来源声明 `shuffle` / `repeat` 时传参数（当前仅 Apple Music；Audius 与
radio 不声明）。先 `lilt sources --json` 确认，再在启动时给参数——单曲循环 `play --repeat one`；
多首 `play-songs --shuffle --repeat all`。来源不支持时命令报 `unsupported_command`（不会静默忽略），
此时**不要重试**，改用不带形态参数的调用，并告知用户该来源不支持。播放后再 `lilt shuffle` /
`lilt repeat` 也可，但启动参数是原子的，优先用参数。

**编辑队列**：先 `lilt queue` 查看；`queue add` / `queue remove` / `queue move` / `queue jump` /
`queue clear` 都按**当前**队列的 index 操作，操作前后都可再 `queue list` 确认。队列只在
Apple Music / Audius 的有限队列存在。

**播放〈流派/氛围〉（pop / lofi / jazz / 适合写代码的歌 / 安静一点的歌）**
1. 先 Apple Music full（能订阅播放就走它）：搜氛围词的歌单 → 选标题/策展贴合的 → 播放。
2. Apple Music full 不可用，或用户明确要电台 → `lilt radio search --tag <tag>`。
3. 选 `radio.lastCheckOK == true` 且 `radio.bitrate` 较高者 → 播它的 `url` 并带 `--name` →
   `lilt status --json` 确认 `isLive`。
4. 写代码/学习/专注 → 优先 tag：`lofi`、`jazz`、`instrumental`、`classical`；避免 `news`、`talk`、
   派对 pop 类。把选择理由一并汇报。

**控制**：暂停/继续/下一首/停止 → 直接对应命令；`pause` 已暂停、`resume` 已播放和 `stop` 都是成功
no-op。live stream 没有下一首；不要猜测恢复或换台。

## 错误处理

| error.code | 含义 | 动作 |
|---|---|---|
| `session_unavailable` | server 无法启动 | 如实告知用户；不要重试或自行清理进程 |
| `active_session` | 已有 server 在运行（`serve` 的返回） | 直接使用已有会话并重试原命令，不停止用户播放 |
| `invalid_state` / `finite_queue_required` | 当前状态或播放模式不支持该控制 | 读取 status；不要对 live/空队列执行队列控制 |
| `unsupported_command` | 该来源不支持这个操作或形态参数 | 不重试；去掉不支持的参数或换来源，并如实告知 |
| `source_mismatch` | 队列或 ID 混了 Source | 不混队；只使用同一 Source 的 ID/ref |
| `playback_error` | 播放失败（未授权/资源不可播） | 如实转述 error.message，尝试下一个候选或换来源 |
| `authorization_required` | 来源需要授权 | 告诉用户运行确切的 `lilt auth <source> --json`；未获用户明确要求时绝不执行或发起交互式授权 |
| `search_failed` | 请求的发现来源均不可用 | 按来源选择规则回退；有 `degradedOrigins` 的成功响应仍可使用其结果 |
| `duplicate_result_unavailable` | 同 requestId 的结果已逐出 | 不重放；先 `status` 后再决定 |

授权与播放分开：用 `lilt auth status <source> --json` 和 `sources` capability 判断来源可用性；
`status.mode:"preview"|"full"` 只表达当前播放模式，不推断授权状态。只有用户明确要求退出、断开或
切换账号时才执行 `lilt auth disconnect <source> --json`；该命令会停止该 source 的当前播放并删除
本地凭据。
