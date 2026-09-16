# Round 24 · Playback format clarity

- 日期：2026-09-16
- 重点：Now Playing 是否把“曲目支持的格式”误导为“当前播放格式”
- 状态：完成；误导性信息层级已修复

## 观察

真实播放截图显示：

```text
Playing · Apple Music · System-selected
Track offers: Dolby Atmos, ALAC Lossless · up to 24/48, AAC 256 kbps
```

用户自然地把行末 `AAC 256 kbps` 理解成当前播放 AAC。实际含义是：MusicKit 没有回报当前 `audioVariant`；曲目目录同时提供 Atmos、ALAC 与 AAC，当前 codec 不可确认。

## 修复

1. Now Playing 只保留播放状态与 MusicKit 回报的格式：`AAC 256 kbps`、`ALAC Lossless` 或 `System-selected`。
2. 曲目可用格式移至 `i` → Track Info，以 `Offer` 字段列出。
3. 当格式为 `System-selected` 时，UI 不再暗示 AAC 或 Lossless；这是“系统选择但 API 未回报实际格式”。

## 回归覆盖

- 主 dock 有 `System-selected`，但不含 `Track offers:` / `Available:`。
- Track Info 含 ALAC 与 AAC 的可用格式。
- `docs/spec/rpc.md` 与 `docs/spec/limitations.md` 同步 `Auto` → `System-selected` 语义。
