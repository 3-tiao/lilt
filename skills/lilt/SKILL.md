---
name: lilt
description: 音乐与电台播放控制。当用户说"播放音乐 / 播放X的歌 / 来点pop / 放个电台 / 适合写代码的歌 / 暂停 / 下一首 / 停止音乐"等时使用。通过 lilt CLI 控制 Apple Music、Audius 与网络电台（macOS）。
---

# lilt 音乐控制

> 本文是自包含的操作速查。**权威、机器可读的命令目录用 `lilt api --json`**：它由
> 程序自身生成（不需要 server 运行），列出每个命令的参数 schema、返回模型与稳定错误码。
> 命令行为变化时以 `lilt api --json` 为准。

lilt 是本机的 Apple Music / Audius / 网络电台控制器。你（agent）通过 `lilt` CLI 的
稳定 JSON 输出完成播放与控制。智能在训练里：API 只提供事实与原语，由你
组合出最合适的做法。

## 原则

1. 所有命令一律加 `--json`；只解析 `{"ok":true,"data":…}` / `{"ok":false,"error":{"code","message"}}` 信封。
2. **永远不要运行 `lilt tui`** —— 那是给人用的全屏界面。
3. 遇到 `no_active_session`：先运行 `lilt serve --detach --json`，然后**重试一次**原命令；重试前用 `lilt status --json` 确认会话存在。
4. 每次改变播放状态后，用 `lilt status --json` 确认，并向用户**一句话汇报**（播了什么 + 为什么选它）。只报一个决定，不要把多个候选都列出来。
5. **自动来源选择**：用户没有明确指定来源时，先 `lilt sources --json`，在**具备所需 capability
   且 `available:true`** 的来源中按 `priority` 选择：`playback.full` 的 Apple Music → `playback.full`
   的 Audius → radio stream。Apple 未授权/无订阅或不可用时才降级；radio 内部先 `origin=builtin`
   再 `directory`。只把**完整播放**当可播放，`preview` 不算。用户明确指定的来源永远优先。
6. **来源优先（provider-first）**：`lilt search` 先定来源。默认 `apple-music`，Audius 用 `--source audius`；电台发现用 `lilt radio search`（`lilt search --source radio` 会报错）。只请求该来源声明的能力；来源及其能力的说明见 `lilt sources --json` 与 `lilt api --json` 的 `description` 字段（仅供线索，不做分支依据）。**API 原语不做隐式跨来源 fallback**——换源必须由你在 skill 层显式决定（见下）。

## 来源选择（用户未指定时）

按顺序决策，每步都用 `lilt sources --json` 的事实（capability 的 `available`），不要凭记忆：

1. **用户点名来源** → 直接用该来源；不可用就如实报错，不偷偷换源。
2. **具体歌名/艺人**：
   - 选优先级最高、`playback.full` 可用的来源（Apple Music → Audius）。
   - 在该来源 `search --type song`；若无 title 精确/最接近匹配，或匹配项的艺人明显不符，
     **显式换到下一个来源**再搜一次（如 `--source audius`）。
   - 命中后 `play <ref>`；汇报要包含**最终选了哪个来源、为什么**。
3. **氛围/背景音乐**：先 Apple Music `--type playlist`，其次 Audius，再次电台（`lilt radio search`）。
4. **只想随便放点东西**（"放点音乐"）：`lilt library`（Apple 有资料库时）或
   `lilt trending --source audius`，挑一个直接播。
5. **显式换源永远是 skill 的决定**：API 不会替你 fallback；换源前先确认目标来源的 capability。

Phrasing（用户这样说时）：
- "用 Audius 播放 X" / "苹果音乐放 X" / "用收音机放 X" → 固定 `--source`。
- "播放 X"（不点来源）→ 走上面的自动选择。

## API 速查

发现类（无会话可用）：

| 命令 | 输出 |
|---|---|
| `lilt search <term> [--source apple-music\|audius] --json` | 歌曲（每首含 `kind:"song"`、`id`、`title`、`artist`） |
| `lilt search <term> [--source S] --type playlist --json` | 歌单（`kind:"playlist"`、`id`、`title`、`artist`＝策展方） |
| `lilt search <term> --type station --json` | Apple Music 目录电台（Audius 不支持 station，会报 `unsupported_command`） |
| `lilt search <term> --source S --type all --json` | 分组对象；只含该来源支持的分组（Audius 只有 `songs`/`playlists`），空组省略 |
| `lilt radio search [--name 文本] [--tag 流派] [--language 语言] [--country 国家码] [--limit n] --json` | 电台（`kind:"stream"`、`url`、`radio.tags`、`radio.bitrate`、`radio.lastCheckOK`） |
| `lilt sources --json` | 各来源及其 capability（`available`、`reason`、可选 `description`） |
| `lilt trending [--source audius] [--type song\|playlist] --json` | 官方 trending（当前为 Audius） |
| `lilt recent [n] --json` / `lilt library [--source S] --json` | 最近播放 / 云端资料库歌单 |

播放控制类（需会话）：

| 命令 | 语义 |
|---|---|
| `lilt play <ref> --json` | `ref` 必须是 canonical `source:kind:id`（如 `apple-music:song:<id>`、`apple-music:playlist:<id>`、`audius:song:<id>`）、Apple Music URL，或电台流 `https://…`（可加 `--name "台名"`） |
| `lilt play-songs <ref,ref,...> [--start n] --json` | 把同一 finite-queue Source 的 canonical refs 编成队列播放（"生成播放列表"） |
| `lilt shuffle on\|off --json` | 队列随机 |
| `lilt repeat off\|all\|one --json` | `one`＝单曲循环，`all`＝队列循环 |
| `lilt status --json` | 当前播放；默认不含队列，需队列时用 `--queue` |
| `lilt auth status [SOURCE] --json` | 所有来源或指定来源的授权状态 |
| `lilt auth <SOURCE> --json` | 用户明确要求时开始并等待该来源授权的终态 |
| `lilt auth cancel <FLOW_ID> --json` | 取消指定授权流程 |
| `lilt pause --json` / `lilt resume --json` / `lilt next --json` / `lilt previous --json` | 控制 |
| `lilt serve --detach --json` | 后台起无界面服务；成功仅表示 API 已可接受请求，返回 `data.pid` |
| `lilt stop --json` | 停止播放、保留服务（总是幂等）；`lilt quit` 才结束服务 |

## Recipes（skill 层 preset）

这些 preset 是本 skill 的命名编排配方，不是 TUI、server 或 Client API 对象。它们通过
搜索和 `play-songs` 生成当前会话的临时队列，不创建 Apple Music 等 provider 中的永久
歌单，也不要求 server 保存 usage。

**播放〈艺人〉的歌**
1. 按「来源选择」定来源；`lilt search <艺人名> --type all --limit 10 --json`（Audius 加 `--source audius`）。
2. 优先歌单：`playlists` 里 `title` 或 `artist` 含该艺人名的（如"张信哲精选"）→ `lilt play <item.ref> --json` → `lilt shuffle on --json`
3. 没有专属歌单 → 从 `songs` 里取 `artist` 字段包含该艺人名的前 10 首 → `lilt play-songs <ref1,ref2,...> --json` → `lilt shuffle on --json` → `lilt repeat all --json`
4. `lilt status --json` 汇报（播了什么 + 来源 + 为什么）
5. 排除规则：艺人名只出现在歌曲 `title` 里的翻唱/合辑不要选。
6. Apple Music 没有该艺人或不可播放 → 显式 `--source audius` 重搜一次再决定。

**播放〈歌名〉**
1. 用户点名来源就用它；否则按「来源选择」在 Apple Music → Audius 中选第一个 `playback.full` 可用的。
2. 首选来源：`lilt search "<歌名>" --type song --limit 10 --json`，取 `title` 精确或最接近、`artist` 合理者。
3. 无合适匹配或该来源不可播放 → **显式**换下一来源：`lilt search "<歌名>" --source audius --type song --limit 10 --json` 再选。
4. `lilt play <item.ref> --json` → `lilt status --json`；汇报播了什么 + 最终来源 + 为什么（含是否发生了回退）。

**播放 Audius**：用户指定 Audius、要独立音乐或公开发现时，运行
`lilt search "<term>" --source audius --type all --json`，从 `songs` 或 `playlists` 选择项目，随后
`lilt play <item.ref> --json`（例如 `audius:song:<id>`）。也可以先 `lilt trending --source audius --json`
拿现成 trending。Audius 匿名搜索和播放可用；账号连接是可选的，不要为了播放主动授权。
命令与参数始终以 `lilt api --json` 为准。

**单曲循环**：`play <item.ref>` → `lilt repeat one --json`。**多首循环**：`play-songs <refs>` → `lilt repeat all --json`（可加 shuffle）。

**播放〈流派/氛围〉（pop / lofi / jazz / 适合写代码的歌 / 安静一点的歌）**
1. 先 Apple Music full（能订阅播放就走它）：`lilt search "<氛围词>" --type playlist --limit 8 --json` → 选标题/策展贴合的 → 播放。
2. Apple Music full 不可用，或用户明确要电台 → `lilt radio search --tag <tag> --limit 5 --json`。
3. 选 `radio.lastCheckOK == true` 且 `radio.bitrate` 较高者 → `lilt play <radio.url> --name "<title>" --json` → `lilt status --json` 确认 `isLive`。
4. 写代码/学习/专注 → 优先 tag：`lofi`、`jazz`、`instrumental`、`classical`；避免 `news`、`talk`、`pop 派对`类。把选择理由一并汇报。

**控制**：暂停/继续/下一首/停止 → 直接对应命令；`pause` 已暂停、`resume` 已播放和
`stop` 都是成功 no-op。live/stream 没有下一首；不要猜测恢复或换台。

## 错误处理

| error.code | 含义 | 动作 |
|---|---|---|
| `no_active_session` | 没有播放服务 | `lilt serve --detach --json` 后重试一次 |
| `active_session` | 另一启动者已建立 server | 直接使用已有会话并重试原命令，不停止用户播放 |
| `invalid_state` / `finite_queue_required` | 当前状态或播放模式不支持该控制 | 读取 status；不要对 live/空队列执行队列控制 |
| `source_mismatch` | 队列或 ID 混了 Source | 不混队；只使用同一 Source 的 ID/ref |
| `playback_error` | 播放失败（未授权/资源不可播） | 如实转述 error.message，尝试下一个候选或换来源 |
| `authorization_required` | 来源需要授权 | 告诉用户运行确切的 `lilt auth <source> --json`；未获用户明确要求时绝不执行或发起交互式授权 |
| `search_failed` | 请求的发现来源均不可用 | 按来源选择规则回退；有 `degradedOrigins` 的成功响应仍可使用其结果 |
| `duplicate_result_unavailable` | 同 requestId 的结果已逐出 | 不重放；先 `status` 后再决定 |

授权与播放分开：用 `lilt auth status <source> --json` 和 `sources` capability 判断来源
可用性；`status.mode:"preview"|"full"` 只表达当前播放模式，不推断授权状态。
只有用户明确要求退出、断开或切换账号时才执行 `lilt auth disconnect <source> --json`；
该命令会停止该 source 的当前播放并删除本地凭据。
