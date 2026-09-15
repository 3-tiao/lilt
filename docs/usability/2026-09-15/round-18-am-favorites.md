# Round 18 · 想收藏 Apple Music 歌曲的用户（D1 验证）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r18*）
- 构建：工作区（D1 Apple Music Favorites 视图 + 两个潜在 bug 修复）
- 评分：7/10
- 目的：验证 D1（AM 收藏列表）全链路
- 结果：核心全部通过；报告指出「本地收藏 vs 官方喜爱歌曲」概念说明不足

## Agent 报告（原文）

# Round 18 报告：想收藏 Apple Music 歌曲的用户

## 时间线
1. 启动首页底部提示：`f favorite · 3 Favorites · / search`；`?` 确认 `f favorite / unfavorite`。
2. `/` → `Yellow Coldplay` → 搜索首条 `Yellow — Coldplay`。
3. `f` → 出现 `★`，提示 `★ Favorited: Yellow`。
4. `/` → `Numb Linkin Park` → 首条 `Numb — LINKIN PARK` 上 `f`。
5. `3` → `Favorites (2)`：`★ Yellow — Coldplay`、`★ Numb — LINKIN PARK`。
6. `p` → `State: playing`；`Space` → `State: paused`。
7. `Down`、`f` → 列表即时 `Favorites (1)`，提示 `Unfavorited: Numb`。
8. `v` 停止，`2` Playlists，在 `2001 年摇滚金曲` 上 `f` → `★ Favorited: 2001 年摇滚金曲`。
9. `3` → `Favorites (2)`（含歌单）；`Down`、`Enter` → 打开 `2001 年摇滚金曲 (48)` 曲目列表。
10. `q` 退出、重启 → `Favorites (2)` 仍在，持久化成功。

## 收藏流程评价
- 入口：底部提示与帮助均有说明，发现后操作快；仅 `f` 字母需理解 favorite 语义。
- 列表：`3 Favorites` 好找，歌曲与歌单统一展示。
- 取消收藏：`f` 即时更新列表并有 `Unfavorited` 提示。
- **本地收藏 vs 官方「喜爱歌曲」：存在困惑**——首页有 Apple Music 歌单「喜爱歌曲」，应用又有 `Favorites`，界面未说明是否同步。

## 发现的问题
1. **中：本地 Favorites 与 Apple Music「喜爱歌曲」概念冲突，缺少说明。**
   最小复现：`1` 看到歌单 `喜爱歌曲`；`3` 进入 `Favorites`。
2. **低：收藏操作仅依赖英文快捷键语义。**

## 好的方面
- 搜索中收藏立即显示 `★`；收藏列表即时增删。
- 支持歌曲与歌单统一回找；从收藏播放、暂停状态清晰。
- 重启后收藏仍在，满足长期保存预期。

## 改进建议
- 明确标注「本地收藏」，说明是否与官方喜爱歌曲同步。
- 快捷键提示本地化；歌单/歌曲增加类型标识。

## 总体可用性评分（1-10）与一句话结论
**7/10。** 收藏、回找、播放和持久化都顺畅，但 Favorites 与 Apple Music「喜爱歌曲」的关系不清楚，削弱了对收藏位置的信心。

## 编排者复核与即时修复

- **D1 全链路验证通过**：收藏歌曲/歌单 → 列表可见（含 `★`）→ 播放/暂停 → 取消收藏即时更新 →
  打开歌单详情 → 重启持久化。
- 验证过程中发现并修复两个潜在 bug（详见 batch-5）：
  1. `ItemID` 对已带前缀的收藏项二次加前缀（`am:am:…`），导致 AM 收藏/最近的星标匹配与取消收藏失配
     （预存 bug，radio 因 URL 分支未暴露）；
  2. `playlistTracks` 需要来源原始 ID，而收藏项带 `am:` 前缀导致打开歌单失败。
- **问题 #1 即时修复**：AM 收藏视图标题改为 `Favorites · local`，帮助中 `f` 标注 `(lilt-local list)`；
  README/sources/ux 已说明与官方「喜爱歌曲」不同步。
- 问题 #2（中文化）维持不修（产品方向）。