# Spec: State（跨端数据 schema）

本地优先。没有 server 运行时，文件是耐久事实来源；server 启动后读取文件，并以
“成功持久化后的内存快照”作为运行期权威状态。Client API client 不得直接写文件，
server 也不监视运行期间的外部编辑。云同步以后再加（此处只定义持久 schema 与路径）。

## 路径

| 用途 | 路径 | 覆盖变量 |
|---|---|---|
| 配置 | macOS `~/Library/Application Support/lilt/`（`themes/*.toml` 等） | `LILT_CONFIG` |
| 状态 | macOS `~/Library/Application Support/lilt/state.json` | `LILT_STATE` |
| cache/socket | macOS `~/Library/Caches/lilt/`（`radio-cache.json`、`session.sock`） | `LILT_RADIO_CACHE` / `LILT_SOCKET` |
| server 生命周期锁 | macOS `~/Library/Application Support/lilt/server.lock` | 随 state root 派生，不独立覆盖 |

显式 `XDG_CONFIG_HOME`、`XDG_STATE_HOME`、`XDG_CACHE_HOME` 优先于平台默认；未设置时
macOS 使用上表，其他平台使用各自 native user directory。显式 `LILT_*` 路径覆盖对应
派生路径。首次启动在目标目录不存在状态时，MUST 一次性迁移既有 XDG-style
`~/.config/lilt` / `~/.local/state/lilt` / cache 路径（或明确报告冲突），原子完成并留下
migration marker；不得静默分裂读写两套状态。主题随配置迁移。

## `state.json` v2（Audius Phase 1 已迁移）

```jsonc
{
  "version": 2,
  "theme": "gruvbox",                 // themes/ 下的内置名或自定义文件名；空/`default` 在加载时解析为 gruvbox
  "lastSource": "apple-music",        // UI 下次启动选择的 source
  "lastPlaybackSource": "apple-music", // stopped PlaybackState.source 的恢复值
  "favorites": {
    "apple-music": [
      { "id": "am:1440845629", "source": "apple-music", "kind": "song",
        "title": "Aruarian Dance",
        "artist": "Nujabes", "addedAt": "2026-09-13T12:00:00Z" }
    ],
    "audius": [
      { "id": "audius:song:abc123", "source": "audius", "kind": "song",
        "title": "Example Track", "artist": "Example Artist",
        "addedAt": "2026-09-13T12:00:00Z" }
    ],
    "radio": [
      { "id": "radio:https://radio.cliamp.stream/lofi/stream",
        "source": "radio", "kind": "stream",
        "title": "lofi", "url": "https://radio.cliamp.stream/lofi/stream",
        "addedAt": "2026-09-13T12:00:00Z" }
    ]
  },
  "recent": [
    { "id": "am:1440845629", "source": "apple-music", "kind": "song",
      "title": "Aruarian Dance", "artist": "Nujabes",
      "playedAt": "2026-09-13T12:05:00Z" }
  ],
  "recentContainers": [
    { "id": "am:p-library", "source": "apple-music", "kind": "playlist",
      "title": "Morning Mix", "playedAt": "2026-09-13T12:06:00Z" }
  ]
}
```

## 规则

- **稳定 id**：见 [`sources.md`](sources.md) 的 id 方案。收藏/最近以 id 判定
  identity；可以保存 title/artist 等显示快照，但不得以显示文本去重。
- **可离线还原**：favorites、recent 和 recentContainers MUST 保存 `source`、`kind`、
  `id` 及已有显示快照；radio 还必须保存 URL。server 由这些字段确定性生成公开
  Item 的 `ref` 与 `providerId`（从 canonical `id` 派生），不能依赖在线 provider lookup。
  Audius 离线 Item 使用 `audius:<kind>:<provider-id>` identity；recentContainers 可属于 Apple
  Music 或 Audius，分别使用 `am:<provider-id>` 或 `audius:playlist:<provider-id>`。v1 的
  container id 形如
  `playlist:<provider-id>`，迁移为 `am:<provider-id>`；旧记录缺 kind 时按 song 迁移。
- **时间戳**：RFC3339（UTC）。
- **去重**：同 id 幂等；同一 recording 的不同 provider id（MusicKit 队列内 id vs 目录 id）在
  同一 source 内按 `title+artist` 视为同一条，写入时合并并保留最新 id；`recent` 按 id/
  同轨合并去重后按 `playedAt` 倒序，保留最近 N（建议 100）。
- **recent 阈值**：一次 playback occurrence 累计 monotonic `status=playing` 达到
   `min(30s, 已知有限 duration 的 50%)` 才写一次；live/未知 duration 是 30s。
   paused/buffering/stopped 不计时，seek/position jump 不增加计数。合格重播刷新
   `playedAt`；显式 play、队列切换和 repeat wrap 开始新 occurrence，pause/resume、
   buffering 恢复和 seek 不会。`recentContainers` 不受阈值影响，容器成功启动即记录。
- **最近容器**：`recentContainers` 记录由 lilt 启动、可恢复详情的 Apple/Audius 歌单，按
  `source+kind+id` 去重、按时间倒序并保留最近 100；不会伪造历史队列。
- **版本**：`version` 递增；同版本读取时对未知字段宽容（**忽略**，下次保存不保留），缺失字段用默认。高于当前实现的 future version 以只读方式拒绝，原文件不重写；语法错误或字段类型错误必须先完整解码失败，再用不覆盖已有备份的 `.corrupt-*` 名称隔离后以空状态启动并显示警告。若隔离失败则阻止所有保存，绝不覆盖原文件。
- **v1 -> v2**：当前 v1 的 `favorites.appleMusic`/`favorites.radio` 迁移为 canonical SourceID-keyed
  map（例如 `apple-music`、`audius`、`radio`）；每个 favorite/recent/container 的 `source` 以其
  collection key（favorites）或 Apple Music（旧无 source 记录）为准，并规范化为对应 stable
  identity；旧 `playlist:<id>` container 规范化为 `am:<id>`。新增 `lastPlaybackSource`，初值取旧
  `lastSource`。持久 Item 补齐 `kind` 与 source；`providerId` 由 canonical `id` 派生，不单独持久化。迁移必须幂等；未知字段读取时忽略、
  保存时不保留。以 Apple Music、Radio、Audius round-trip fixtures 验证，签名 media URL 在迁移前后
  都必须被剥离。
- **主题**：`theme` 存主题名（文件名去 `.toml`）。
- **广播发现查询**：Radio Search & Filters 的文本与 facet 只存在于当前 TUI 会话，直接驱动 Browse 的内容与标题；不会写入状态、不会在重启后恢复，也不影响收藏、最近播放、当前播放或自定义 URL。

## 同步（留口，不在 v1）

- 文件是权威；将来同步层只负责合并文件。
- 合并策略（待定）：按 id 取并集；`favorites` 加时间戳，`recent` 按 `playedAt`。
- 跨设备身份由上述 id 方案保证：Apple Music 用 catalog/library id；电台用规范化 URL。

状态是可选本地数据：解析失败不得阻止 TUI 启动，必须显示启动警告。所有 mutation
先写临时文件并原子替换，成功后才替换权威内存状态。自动 mutation（如 recent 达阈值）
保存失败 MUST 丢弃，不改变权威内存、不自动重试，并发 `server.warning`
`code:"state_save_failed"`；后续无关保存不得复活该 mutation。

## Server 启动

server 启动前 MUST 获取每用户 advisory/process `server.lock` 并持有至退出；只有 lock
holder 能创建/删除 socket。取得锁后才可检查并回收 stale socket。`serve --detach` 仅在
Client API listener 已接受请求后成功，返回 `{pid}`；engine/source 初始化可异步，
通过 `sources.list` availability 与 watch 观察。竞争者不得删除 socket 或猜测启动结果：
锁已被持有时先等待现有 listener 可连接，确认 ready 后才返回 `active_session`；若持锁进程
在 ready 前退出则重新竞争锁，等待超过 10 秒启动预算才返回 `serve_failed`。因此
`active_session` 与 detach 成功都保证 transport 已 ready，不给 agent 留启动 race。

Client API 对外返回归一化 AppState（favorites/recent 使用完整 Item），它不是本文件
磁盘 JSON 的逐字段镜像；server 负责二者转换。见
[`../client-api/models.md`](../client-api/models.md)。

授权凭据、OAuth state、token 和 refresh material 不属于 `state.json`；它们只能由对应
provider 保存到 OS secure storage（`internal/securestore`：macOS Keychain，测试用内存实现）。
Audius 的 token bundle 以 service `lilt`、account `audius` 存储。

`radio-cache.json` 不是用户状态或同步事实来源，可以随时删除。它按 `stationuuid` 保存最多 1000 个短期 Radio Browser profile，按规范化 `url_resolved` 的 SHA-256 保存最多 500 个本机 endpoint health；自定义 URL 只保存 hash，不把 URL/query/token 写进 cache。目录 URL 变化会产生新的 endpoint key 并触发重新探测。
