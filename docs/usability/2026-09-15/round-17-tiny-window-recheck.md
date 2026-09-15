# Round 17 · 60x12 小窗口电台听众（第四批 · 验证 A1/小窗口）

- 日期：2026-09-15
- 终端：60x12，隔离 state/socket/log（/tmp/usability-r17*）
- 构建：工作区（Phase A/B/C 全部改动）
- 评分：6/10（R13 为 7/10；评分下降因该轮把帮助截断列为「高」）
- 目的：验证 A1 标记在小窗口的可辨识性、A3 footer、小窗口布局
- 结果：A1 三处标记全部一眼可辨；暴露两个布局项（其一为 Phase D，其一已即时修复）

## Agent 报告（原文）

# Round 17 报告：60x12 小窗口电台听众

## 时间线
1. 启动 → SOURCE `[Apple Music]`，VIEW `[1 Home]`，底部提示可读。
2. `Tab` → `Apple Music   [Radio]`，VIEW `[1 Favorites]`。
3. `/` → `Search & Filters` 占满窗口。
4. `Enter` → 文本输入；输入 `SomaFM`，`Enter` 返回筛选弹层。
5. `Up` → 选中态 `› Reset filters`，清楚。
6. `Up Up Up Enter` → Language facet；等待后列表可见。
7. `Down` → `› english · 11952 stations`，清楚；`Enter` 选中。
8. `Down Down Down Down Enter` → 执行搜索，显示 20 个 SomaFM 电台。
9. `Enter` → 播放 `SomaFM Groove Salad (128k MP3)`；`LIVE · playing`。
10. `Space` → `LIVE · paused`；再 `Space` → `playing`；再 `Space` 暂停。
11. `?` → 帮助弹层只显示前 10 条，底部截断。
12. `Escape`、`q` → 退出。

## 选中态可辨识性
- **SOURCE/VIEW：清楚。**`Apple Music   [Radio]`、`1 Favorites   2 Recent  [3 Browse]`，方括号醒目。
- **搜索筛选菜单：清楚。**`› Reset filters`、`› Language       english`。
- **facet 选项列表：清楚。**`› english · 11952 stations`，未选项无箭头。

## 小窗口布局评估
- 主界面基本可用；列表长文本 `…` 截断但可辨认。
- Now Playing 好：电台名、`LIVE`、playing/paused 完整可读。
- 底部提示可读，播放后 `space pause` / `space resume` 正确。
- **搜索筛选：**初次完整；选定 facet 返回后，底部按钮与提示被截断，只能盲操作。
- **帮助：**被硬截断，无滚动或提示。

## 发现的问题
1. **高：帮助弹层在 60x12 下被硬截断，无法阅读完整帮助。** 最小复现：`?`
2. **中：筛选项选定后，筛选弹层底部关键按钮被截断。** 最小复现：`/ → Enter → SomaFM → Enter → Up×3 → Enter → Down → Enter`
3. **低：主列表长文本过早省略。**

## 好的方面
- 主界面在 60×12 仍保留核心操作路径。
- SOURCE、VIEW、列表和筛选的选中态都一眼可辨。
- Now Playing 对直播、播放、暂停反馈明确。

## 改进建议
- 帮助弹层改为可滚动分页，并显示导航提示。
- 小窗口筛选弹层优先保留确认/取消按钮。
- 窄窗口提供更紧凑的电台条目格式。

## 总体可用性评分（1-10）与一句话结论
**6/10：主界面与播放流程可用，但帮助和筛选确认区在 60×12 下被截断，限制了独立探索与可靠操作。**

## 编排者复核与即时修复

- **A1 验证通过**：三类标记（顶栏方括号、菜单 `›`、facet `›`）在 60x12 全部「一眼可辨」，
  这是第一批以来最高频问题（五轮命中）的收口证据。
- **问题 #2 即时修复**：Search & Filters 在高度不足时切换紧凑布局（去掉空行与分隔线），
  保证 `[ Show all ]`/`[ Cancel ]` 与提示始终可见；真机 60x12 复验通过，补测试
  `TestSmallOverlayKeepsActionsVisible`。
- **问题 #1 部分缓解**：帮助弹层被裁剪时末行显示 `(more — resize for the full list)`（不再静默截断）；
  真正的滚动/分页仍属 Phase D2，待与产品一起决定。真机复验通过。
- **问题 #3** 维持设计内行为。