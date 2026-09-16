# Round 26 · Mouse-first path and list hierarchy

- 日期：2026-09-16
- 人设：优先使用鼠标、只根据屏幕可见标签行动的终端用户
- 终端：120×30，隔离 fake player + 真实 Radio directory
- 状态：完成；无操作 blocker，完成 1 项视觉层级修复

## 任务

1. 点击 `Radio` source 和 `Browse` view。
2. 在 Browse 中对同一电台连续点击两次播放。
3. 用滚轮继续浏览，收藏当前项，再打开 Favorites 确认。

## 结果

- 点击可见的 `Radio` 和 `[3 Browse]` 标签成功切换页面；不依赖快捷键。
- 对 `Classic Vinyl HD` 同一终端单元格连续点击两次，Now Playing 播放的仍是 `Classic Vinyl HD`；列表没有重排到另一项。
- 滚轮移动可见选择，且主列表不跳屏。
- `f favorite` 的 footer 指引、星标和 Favorites (1) 提供了连续的收藏确认。

## 视觉发现与修复

**中：未选中列表行的电台名与格式/国家/标签同等醒目，长行难扫读。**

- 修复：未选中行的标题保持主文本对比度；探测状态保留成功/失败色；格式、国家、标签改为低对比的次级文本。
- 真实 SGR 终端输出验证：`France Info` 为主亮色，探测图标仍为绿色，`· MP3 128k · France` 在图标之后恢复 faint 样式。
- 选中行与当前播放行保持完整高对比，避免降低操作可见性。
