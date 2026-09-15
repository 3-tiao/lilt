# D1 · Apple Music Favorites 视图（Round 18）

## 决策与实现

**问题（R11 发现）**：`f` 可收藏 Apple Music 歌曲/歌单，但 Apple Music 来源没有任何收藏列表可浏览，
收藏后无处回找（用户会误以为与官方「喜爱歌曲」同步）。

**推荐方案（已实施）**：复用现有对称通路，最小侵入。

1. `amViews` 插入 `Favorites`：`Home, Playlists, Favorites, Recent, Presets`
   （与 Radio 一致把收藏靠前；代价：Recent/Presets 数字键后移一位，应用未发布可接受）。
2. 视图内容直接复用 `store.FavoritesFor("apple-music")`；歌曲用现有 `playItem` 播放，
   歌单走现有 `activate` → 详情 push。无需新 RPC。
3. 补缓存失效：`f` 切换后删除对应来源的 `*/Favorites` 缓存并在该视图内即时刷新
   （复用第一批修过的同类模式，避免在 AM 侧重演）。
4. footer 的 `f favorite/unfavorite` 提示推广到所有来源。
5. 命名澄清：AM 收藏标题 `Favorites · local`，帮助标注 `(lilt-local list)`，文档说明与
   官方「喜爱歌曲」不同步。

## 修复的两个潜在 bug

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| 1 | AM 收藏/最近的 `★` 不显示、`f` 取消收藏变成再添加（state 出现 `am:am:…` 重复项） | `ItemID` 对已带来源前缀的条目二次加前缀（radio 因走 URL 分支未暴露，AM 侧长期潜伏） | `ItemID` 幂等化：已带前缀原样返回；radio 分支剥离前缀后再规范化。补测试 `TestItemIDIsIdempotentForStoredEntries` |
| 2 | 打开收藏的歌单报 `play requires a catalog song reference` | `playlistTracks` 需要来源原始 ID，而收藏项带 `am:` 前缀 | state 返回列表项时剥离来源前缀（`rawSourceID`），前缀只由 `ItemID` 负责。补 AM 收藏视图测试 |

## 验证

- 单测：`TestAppleMusicFavoritesView`、`TestAppleMusicUnfavoriteRefreshesAndInvalidates`、
  `TestItemIDIsIdempotentForStoredEntries`；`amViews`/数字键/鼠标标签测试同步更新。
- 真机 PTY：搜索收藏歌曲 → `3` 显示 `Favorites · local (1)` 带 `★` → `Enter` 播放
  （`State: playing · Mode: full`）→ `f` 取消即时 `(0)` 且空态正确；
  收藏歌单 → `Enter` 打开 `2001 年摇滚金曲 (48)`（`playlistTracks ok`）→ 返回 `f` 取消。
- 真机 PTY：`Favorites · local (0)` 标题与帮助行 `favorite / unfavorite (lilt-local list)` 可见。
- `just verify` 全绿。

## 用户验证（Round 18）：7/10

核心全通过。唯一中优先级反馈「本地收藏与官方喜爱歌曲关系不清」已通过标题/帮助/文档三处澄清收口。

## 后续（Phase D 剩余）

- **D2 帮助弹层滚动/分页**：60x12 下仍截断（已有 `(more — resize for the full list)` 提示），待决策。