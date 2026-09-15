# 真实用户可用性测试 · 2026-09-15 · 第三批（Round 11-14）

## 背景

第二批后提交了三处修复（收藏缓存、p 切换、actionMsg 竞态）。本批在 4c8bf4d 上探索
**前两批完全未覆盖的领域**：Apple Music 侧（试听模式人设、歌单/Up Next 队列）、极小终端
（60x12），并跑第三次新手数据点（验证搜索提交成功率）。

## 评分

| 轮 | 人设 | 评分 | 一句话 |
|---|---|---|---|
| 11 | Apple Music 试听（人设前提失实） | 5 | 机器实际有订阅，试听困惑不成立；暴露 AM 收藏无列表视图 |
| 12 | 歌单与 Up Next 队列 | 6 | 发现「从所选歌曲播放」自始失效的高严重度 bug |
| 13 | 60x12 小窗口电台 | 7 | 核心流程全通过；帮助弹层截断 |
| 14 | 完全新手（第三数据点） | 7 | 搜索提交成功（对比 R6 失败）；焦点不可见第四次出现 |

## 本批修复（R12 发现，已修复并验证）

**歌单内「从所选歌曲播放」从未生效过（高严重度，原罪级 bug）**
- 现象：歌单详情选中中间某首按 Enter，永远从第一首播放（`From: Queue · 1/48`）。
- 分层定位：TUI 参数正确 → helper 起点解析正确算出 `startIndex=11` → 真正根因是
  `queue = .init(for: songs)` 后立即读 `queue.entries` 得 **0 条**（MusicKit 异步填充，
  3 秒轮询仍为 0，`prepare()` 不存在于 ApplicationMusicPlayer），`startingAt` 重设被跳过。
  单元测试只测纯函数，E2E 从未验证，直到真实用户操作暴露。`playSongs` 同病。
- 修复：直接从 Song 构造 `ApplicationMusicPlayer.Queue.Entry` 数组并
  `.init(entries, startingAt: entries[startIndex])`，完全绕开填充竞态。
  PTY 验证：`My Way — Limp Bizkit · From: Queue · 12/48` ✓。`just verify` 全绿。
- 附带记录：play RPC 耗时 ~1.5s → ~4.3s（48 首队列构造），可接受。

## 问题汇总（跨轮收敛后）

### 高优先级（强烈建议下一批前修复）
1. **弹窗/标签选中态不可见**（R1/R2/R6/R13/R14 五轮出现）：选中行仅加粗/变色，用户
   「靠底部提示变化和猜测导航」。R14 完整描述了从输入文字到提交的犹豫链。
   建议：统一非颜色 `>` 标记（Search & Filters 菜单、facet 列表、Theme、SOURCE/VIEW 标签）。

### 中优先级
2. **文本搜索两步确认心智模型**（R14）：Enter 提交文本后回到菜单无「尚未搜索」提示。
   建议：菜单内 Text 行右侧加 `(not submitted)` 状态，或空 facet 时 Enter 直接提交搜索。
3. **AM 收藏无列表视图**（R11 发现的设计缺口）：Apple Music 来源 `f` 收藏后无处浏览。
   建议：加 AM Favorites 视图或帮助中说明 f 的本地性。
4. **帮助弹层小窗口截断**（R13）：低高度下纵向裁剪无分页提示。
5. **队列重排/删除当前曲立即切歌**（R12）：提示预告即可，不建议确认弹窗。

### 低优先级
6. 首屏产品定位一句话（R6/R14 三次）。
7. 自定义 URL/流标题为原始地址（R10/R11 语境）；`a` 帮助未标注仅限 Radio（R10）。
8. buffering 中暂停无反馈瞬态（R6/R9）；MusicKit 启动 `working…/paused` 并存瞬态（R11）。
9. 中文本地化（R6/R14 两次，产品方向性）。

## 环境与装置教训（沉淀）

- R11：涉及账号状态的人设必须先核实机器现实（本机实际有订阅），否则产生误报。
- zsh glob 无匹配会中止 `&&` 链——已两次造成编排事故（R3 种子、R13 前的探针构建），
  后续脚本避免在链中用裸 glob。
- LaunchServices 启动的 app stderr 不进 `open` 管道，journal 永远收不到 helper stderr 行——
  helper 调试需要文件探针（已验证该方式有效）。

## 留档索引

- [round-11-am-preview.md](round-11-am-preview.md) · AM 试听（人设修正）
- [round-12-am-queue.md](round-12-am-queue.md) · 歌单与队列（原罪 bug 发现轮）
- [round-13-tiny-window.md](round-13-tiny-window.md) · 60x12 小窗口
- [round-14-newbie-recheck.md](round-14-newbie-recheck.md) · 新手第三数据点

## 下一批建议

1. 修复选中态 `>` 标记后，第四次复跑新手场景，验证提交犹豫是否消除。
2. AM Favorites 视图落地后再跑 AM 收藏场景。
3. 未覆盖人设：慢网络/断网（需网络限速装置）、AM 播放中断恢复、preset 用户。
4. 提交当前修复（歌单起点修复 + 档案）后再开新批。