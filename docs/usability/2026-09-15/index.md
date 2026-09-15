# 真实用户可用性测试 · 2026-09-15 · 第一批（5 轮）

## 流程说明

- 方法：每轮一个独立 sub-agent，扮演不同背景的真实用户，通过 tmux PTY 驱动**真实** lilt
  （真实 MusicKit helper、真实 Radio Browser API、真实音频输出），只隔离 socket/state/log，
  不碰用户正在运行的实例与真实数据。禁止 agent 读源码/文档/日志，只允许看屏幕和应用内帮助。
- 目的：发现真实使用中的问题与改进点；全部 prompt 与报告留档于本目录，供后续批次复用与对比。
- 轮次：5（新手 / 听朋友介绍 / 老用户 / 目标搜索者 / 小窗口探索者）。Round 3 首跑因
  编排者种子写入失败作废后重跑，v2 有效。
- 构建：63392ce 工作区快照（第一批全部基于同一构建）。

## 评分

| 轮 | 人设 | 评分 |
|---|---|---|
| 1 | 完全新手 | 6/10 |
| 2 | 听朋友介绍过 | 6/10 |
| 3 | 老用户（seeded） | 8/10 |
| 4 | 目标搜索者 | 6/10 |
| 5 | 小窗口探索者（84x24） | 8/10 |

## 问题汇总（按严重度）

### 高（已确认 + 已修复）
1. **收藏后 Favorites 列表显示旧缓存**（R1、R2、R4 三轮独立复现）
   - 现象：在 Browse/搜索结果里 `f` 收藏，星标与 toast 正常；按 `1` 进 Favorites 却是
     `Favorites (0)`。会话启动时 Radio 默认进入 Favorites 并缓存了当时的空列表。
   - 根因：`toggleFavorite` 没有失效 `cache["radio/Favorites"]`。
   - 处置：已修复（toggle 后删除该缓存键）+ 回归测试
     `TestFavoriteOutsideFavoritesInvalidatesCache`；`just verify` 通过。未提交。

### 中（跨轮一致，待定夺）
2. **`p` 不是播放/暂停切换**（R3、R4、R5 三轮出现）
   - 用户在播放中按 `p` 期待暂停；footer 只显示 `p play`，`Space` 暂停不易发现。
   - 选项 A：footer 增加 `space pause`；选项 B：让 `p` 对正在播放的当前项切换暂停。
     B 更符合直觉但改变键位语义（Apple Music 侧 `p` 在歌单里是整单播放）。
3. **弹窗/主题选择器的选中态不够醒目**（R1、R2、R3、R5 四轮出现）
   - 仅靠加粗/颜色区分当前行；主题选择器在低对比下几乎不可辨。
   - 建议：统一给选中行加 `>` 文本标记（非颜色依赖），主题选择器标注当前已保存主题（`✓`）。

### 低
4. **启动缺一句产品定位**（R1）：首页直接是 Apple Music 内容，新手需要按 `?` 才知道还能听 Radio。
5. **窄窗口长元数据截断**（R5）：84 列下国家/标签后半段不可见；可考虑优先展示国家。
6. **搜索确认流程偏长**（R3）：`/ → 输入 → Tab×5 → Enter`；`Enter` 在 Text 行不能直接提交
   （会打开编辑器），可考虑 Text 已有内容时 Enter 直接确认。

## 做得好的（多轮验证）

- `Tab` 切换、`j/k` 导航、`g/G`、`?` 帮助全部被自然发现并使用。
- `/` 搜索与筛选（含组合查询 jazz+France）结果符合直觉，`Showing: ...` 标题清晰。
- `Esc` 从过滤结果回 `Popular Worldwide` 被两轮用户自然发现并成功使用。
- 播放状态 `buffering/playing/paused`、`LIVE` 标记、信息弹层 `i` 反馈明确。
- 老用户核心流（启动即 Radio Favorites → p 播放）3 步内完成；lastSource 恢复有效。
- 主题保存与跨重启持久化正常；84x24 小窗口布局无崩坏。
- `q` 退出在所有轮次干净退出，无残留进程。

## 留档索引

- [round-1-newbie.md](round-1-newbie.md) · 完全新手
- [round-2-introduced.md](round-2-introduced.md) · 听朋友介绍过
- [round-3-veteran.md](round-3-veteran.md) · 老用户（含 v1 装置失败说明）
- [round-4-filter-user.md](round-4-filter-user.md) · 目标搜索者
- [round-5-explorer.md](round-5-explorer.md) · 小窗口探索者

## 下一批建议

- 在修复收藏缓存后的新构建上重跑 R1/R2/R4 的核心路径，确认高严重度问题消失。
- 新增人设：a) 想删除收藏的用户（unfavorite 流）；b) 用 `a` 添加自定义流 URL 的用户；
  c) Apple Music preview 模式用户（无订阅）；d) 60x12 极小窗口/慢网络（Radio Browser 超时）用户。
- 决定 p/space 语义与选中态标记方案后再跑对比轮。