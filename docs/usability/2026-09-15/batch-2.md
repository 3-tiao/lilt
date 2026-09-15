# 真实用户可用性测试 · 2026-09-15 · 第二批（Round 6-10）

## 背景

第一批（见 [index.md](index.md)）发现 1 个高严重度 bug（收藏缓存）与 2 个跨轮一致的中优先级问题
（p 键语义、弹窗焦点可见性）。本批在**包含修复的工作区构建**上：
1. 复跑 R1/R2/R4 三个人设，验证修复；
2. 新增两个人设：取消收藏（R9）、朋友分享 URL（R10）；
3. 全部真实运行（真实 helper + Radio Browser + 音频），隔离 state/socket/log。

## 评分对比

| 轮 | 人设 | 第一批 | 第二批 |
|---|---|---|---|
| 1→6 | 完全新手 | 6 | **7** |
| 2→7 | 听朋友介绍 | 6 | 5*（上游 API 大面积超时） |
| 4→8 | 目标搜索者 | 6 | **9** |
| 9 | 整理收藏（新） | — | 8 |
| 10 | 自定义 URL（新） | — | 8 |

*R7 的 5 分主要被 Radio Browser 瞬时故障拖累（见下），其直接暴露的新竞态 bug 是本批最有价值的发现。

## 修复验证结论

1. **收藏缓存 bug：修复确认。** R6/R8/R10 三轮全部验证「收藏 → Favorites 列表可见」；
   R8 的过滤场景、R10 的 `a` 自动收藏场景均端到端通过。
2. **`p` 上下文感知切换：无回归且被自然使用。** 无任何轮次再误用 p（R6 在播放项上按 p 直接暂停成功，
   footer `space pause/resume` 提示被多轮读到）。
3. **新发现并修复：actionMsg 元数据竞态（高严重度，R7 发现）。**
   - 现象：`a` 添加 URL 播放成功但收藏/最近播放丢失；第一批 R1 的 recent 缺失同源。
   - 根因：helper 在 RPC 响应后立即发布 `stateChanged` 通知，通知经不同 goroutine 常先于响应到达
     （sequence+1），`afterSequence < m.sequence` 守卫把已成功完成的动作连同元数据一起丢弃。
   - 修复：通知较新只跳过状态快照应用；已完成动作的 recent/favorite/preset/note/refresh 无条件落盘。
     回归测试 `TestNewerNotificationSkipsStaleStateButKeepsCompletedMetadata`；PTY 复现验证通过；
     playback-state-sync.md 已更新契约。

## 环境性发现

- **Radio Browser de1 在 04:34-04:36Z 大面积超时**（languages/browse 均超时，R7 全程受影响；R8/R9/R10 未复现）。
  这直接验证了 radio-discovery-health.md 中「镜像发现 + failover」设计的必要性，建议提高其排期优先级。

## 问题汇总（本批新发现，除已修复项）

### 中
1. **弹窗焦点可见性**（R6 再现，第一批 R1/R2/R3/R5 同）：新手在 Search & Filters 里用 Tab 循环时
   误入 facet 行打开选择器，最终未能提交查询（Browse 仍是 Popular Worldwide）。
   建议：选中行加 `>` 等非颜色文本标记——这是剩余问题里优先级最高的一项。

### 低
2. **缓冲中暂停无反馈**（R6/R9 两次）：直播 buffering 状态下按 Space，状态仍显示 buffering，
   用户无法确认暂停生效。helper 侧快照时序优化项。
3. **`a` 帮助未标注仅限 Radio**（R10）：Apple Music 下按 `a` 静默无反馈。帮助行加范围标注或 toast 提示。
4. **自定义 URL 收藏显示原始 URL 为标题**（R10）：可结合未来 ICY 元数据或手动重命名改善。
5. **首屏缺产品定位**（R6 再现）：一句「Apple Music 与网络电台的终端播放器」即可。
6. **中文界面**（R6）：产品方向性建议，暂不排期。

## 做得好的（本批验证）

- 取消收藏链路完整：帮助可发现、列表即时更新、跨视图星标同步、删除后可播（R9）。
- `a` 全链路：自动收藏 + 播放、Enter 重播、重启持久化、暂停恢复（R10）。
- 过滤组合查询 `Showing: jazz · The United States Of America` 直觉正确（R8）。
- Reset filters / Esc 恢复默认列表可靠（R8/R9）。
- 暂停反馈（正常播放中）`playing → paused + space resume` 被多轮明确称赞。

## 留档索引

- [round-6-newbie-recheck.md](round-6-newbie-recheck.md) · 完全新手复跑
- [round-7-introduced-recheck.md](round-7-introduced-recheck.md) · 介绍复跑（竞态 bug 发现轮）
- [round-8-filter-recheck.md](round-8-filter-recheck.md) · 过滤搜索复跑
- [round-9-unfavorite.md](round-9-unfavorite.md) · 整理收藏（新）
- [round-10-custom-url.md](round-10-custom-url.md) · 自定义 URL（新）

## 下一批建议

1. 修弹窗焦点标记（`>` 前缀）后复跑 R6 同款新手场景，验证查询提交成功率。
2. 提高镜像 failover 优先级后可加「慢网络/断网」人设（Radio Browser 不可用时的降级体验）。
3. Apple Music preview 模式人设（无订阅）与 60x12 极小窗口人设仍未覆盖。
4. 代码提交建议：三处修复（收藏缓存、p 切换、actionMsg 竞态）均有测试，可一并提交后再开下一批。