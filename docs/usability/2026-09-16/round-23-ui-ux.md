# Round 23 · UI/UX baseline and recheck

- 日期：2026-09-16
- 重点：直觉性、信息层级、视觉密度与反馈；功能 bug 仅在影响体验时处理
- 终端：真实 Apple Music 120×32；隔离 fake Apple Music 120×32 / 90×20；真实 Radio directory 110×30
- 状态：完成；2 个 UI/UX 问题已修复，无 blocker
- 评分：8/10

## 任务结果

| 路径 | 结果 | UX 观察 |
|---|---|---|
| 首次打开真实 Apple Music Home | 成功 | Source / View、分组列表、footer 的 `Apple Music & radio — Tab switches source, / searches` 足以说明 app 用途与下一步。真实中英文长歌名安全显示。 |
| Apple Music：Playlists → detail → Enter 播放 → `0` 队列 | 成功 | 固定 dock 不移动主列表；Now Playing 与 Up Next 的层级清楚。90×20 仍能显示并可聚焦队列。 |
| Radio：Tab → Browse → `/` → 输入 `jazz` → Enter → Enter | 成功 | 输入后焦点自动落在 `[ Search "jazz" ]`，第二次 Enter 得到 92 个结果；流程符合预期。 |
| Theme picker | 成功 | 当前项有 `›`，预览/保存/取消指令可见。 |
| `?` Help | 改善后成功 | 关键操作按 Navigation、Playback、Up Next、Library 分组；110×30 内完整可读。 |

## 发现与修复

1. **中：Radio 空播放 dock 出现 Apple Music “access denied”提示。**
   - 影响：Radio 可正常使用却呈现错误感，干扰首屏视觉。
   - 修复：Apple 授权提示只在 Apple Music source 显示；Radio 保持干净的 `Nothing playing` dock。

2. **中：Help 是无分组的长列表，难以快速扫描。**
   - 影响：用户知道 `?` 后仍需在 23 条平铺快捷键中寻找操作。
   - 修复：保留全部操作但按情境分组，并把 Up Next 专属操作移入独立区；30 行终端无需滚动。

## 视觉审查结论

- **保留**固定高度的 Now Playing dock：播放前后没有布局跳动，留白与前一轮的舒适度目标一致。
- **保留**Radio 单行元数据：格式、可达性、国家与标签支持快速比较；长标签已安全省略，不造成边框溢出。
- 当前无须继续调整字体大小、边框或左右间距。下一次应只在真实长队列、真实主题颜色下复查主观视觉。

## 验证

```text
go test ./...
go test -race ./...
go vet ./...
git diff --check
```
