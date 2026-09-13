# Spec: State（跨端数据 schema）

本地优先。文件是**唯一事实来源**，云同步以后再加（此处只定义 schema 与路径）。

## 路径

| 用途 | 路径 | 覆盖变量 |
|---|---|---|
| 配置 | `~/.config/lilt/`（`presets.toml`、`themes/*.toml`） | `LILT_CONFIG` |
| 状态 | `~/.local/state/lilt/state.json` | `LILT_STATE` |

Windows/macOS 遵循同构约定（`XDG_*` 未设时用平台默认目录）。

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
  "presets": {
    "focus": { "uses": 3, "last": "am:123", "chosen": { "am:123": 3 } }
  },
  "playlists": [
    { "id": "list-1789300000000000000", "name": "Road",
      "items": [ { "id": "am:1440845629", "title": "Aruarian Dance", "artist": "Nujabes" } ],
      "createdAt": "2026-09-13T12:00:00Z", "updatedAt": "2026-09-13T12:00:00Z" }
  ]
}
```

`playlists` 是 lilt 本地可编辑歌单（macOS 不能创建/编辑 Apple 资料库歌单）。
`items` 存稳定的 Apple Music id（`am:<id>`），播放时解析 id 逐个入队。

## 规则

- **稳定 id**：见 [`sources.md`](sources.md) 的 id 方案。收藏/最近/预设都存 id，不存显示文本。
- **时间戳**：RFC3339（UTC）。
- **去重**：同 id 幂等；`recent` 按 id 去重后按 `playedAt` 倒序，保留最近 N（建议 100）。
- **版本**：`version` 递增；读取时对未知字段宽容，缺失字段用默认。
- **主题**：`theme` 存主题名（文件名去 `.toml`）。
- **预设**：格式与 `presets.toml`（`label/query/kind`）不变；`presets` 段只存统计。

## 同步（留口，不在 v1）

- 文件是权威；将来同步层只负责合并文件。
- 合并策略（待定）：按 id 取并集；`favorites` 加时间戳，`recent` 按 `playedAt`；`presets.uses` 取较大值。
- 跨设备身份由上述 id 方案保证：Apple Music 用 catalog/library id；电台用规范化 URL。
