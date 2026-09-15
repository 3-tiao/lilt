# Spec: State（跨端数据 schema）

本地优先。文件是**唯一事实来源**，云同步以后再加（此处只定义 schema 与路径）。

## 路径

| 用途 | 路径 | 覆盖变量 |
|---|---|---|
| 配置 | `~/.config/lilt/`（`presets.toml`、`themes/*.toml`） | `LILT_CONFIG` |
| 仅预设文件 | `<config>/presets.toml` | `LILT_PRESETS` |
| 状态 | `~/.local/state/lilt/state.json` | `LILT_STATE` |

Windows/macOS 遵循同构约定（`XDG_*` 未设时用平台默认目录）。
`LILT_PRESETS` 是首选的预设文件覆盖。迁移兼容：仅当 `LILT_CONFIG` 指向
**已存在的普通文件**时，继续按旧版“预设文件”语义读取；否则它是配置目录。

## `state.json` v1

```jsonc
{
  "version": 1,
  "theme": "gruvbox",                 // themes/ 下文件名，空=终端默认
  "lastSource": "apple-music",        // 下次启动恢复
  "favorites": {
    "appleMusic": [
      { "id": "am:1440845629", "title": "Aruarian Dance",
        "artist": "Nujabes", "addedAt": "2026-09-13T12:00:00Z" }
    ],
    "radio": [
      { "id": "radio:https://radio.cliamp.stream/lofi/stream",
        "title": "lofi", "url": "https://radio.cliamp.stream/lofi/stream",
        "addedAt": "2026-09-13T12:00:00Z" }
    ]
  },
  "recent": [
    { "id": "am:1440845629", "source": "apple-music",
      "title": "Aruarian Dance", "artist": "Nujabes",
      "playedAt": "2026-09-13T12:05:00Z" }
  ],
  "recentContainers": [
    { "id": "playlist:p-library", "kind": "playlist", "title": "Morning Mix",
      "playedAt": "2026-09-13T12:06:00Z" }
  ],
  "presets": {
    "focus": { "uses": 3, "last": "am:123", "chosen": { "am:123": 3 } }
  }
}
```

## 规则

- **稳定 id**：见 [`sources.md`](sources.md) 的 id 方案。收藏/最近/预设都存 id，不存显示文本。
- **时间戳**：RFC3339（UTC）。
- **去重**：同 id 幂等；`recent` 按 id 去重后按 `playedAt` 倒序，保留最近 N（建议 100）。
- **最近容器**：`recentContainers` 只记录由 lilt 启动的 Apple 歌单，按 `kind:id` 去重、按时间倒序并保留最近 100。它用于打开详情；不会伪造无法恢复的历史 Apple 队列。
- **版本**：`version` 递增；同版本读取时对未知字段宽容，缺失字段用默认。高于当前实现的 future version 以只读方式拒绝，原文件不重写；语法错误或字段类型错误必须先完整解码失败，再用不覆盖已有备份的 `.corrupt-*` 名称隔离后以空状态启动并显示警告。若隔离失败则阻止所有保存，绝不覆盖原文件。
- **主题**：`theme` 存主题名（文件名去 `.toml`）。
- **预设**：格式与 `presets.toml`（`label/query/kind`）不变；`presets` 段只存统计。
- **广播发现查询**：Radio Search & Filters 的文本与 facet 只存在于当前 TUI 会话，直接驱动 Browse 的内容与标题；不会写入状态、不会在重启后恢复，也不影响收藏、最近播放、当前播放或自定义 URL。

## 同步（留口，不在 v1）

- 文件是权威；将来同步层只负责合并文件。
- 合并策略（待定）：按 id 取并集；`favorites` 加时间戳，`recent` 按 `playedAt`；`presets.uses` 取较大值。
- 跨设备身份由上述 id 方案保证：Apple Music 用 catalog/library id；电台用规范化 URL。

状态与预设是可选本地数据：解析失败不得阻止 TUI 启动，必须显示启动警告。收藏、最近播放、歌单容器和预设统计仅在播放成功确认后写入；所有更新先修改内存快照，保存成功后才替换权威内存状态。保存失败显示错误，不得显示虚假的成功提示，也不得让失败修改混入以后无关的保存。
