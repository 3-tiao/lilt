# Round 16 · 朋友发来电台链接的用户（第四批 · 验证 Phase B2/C1）

- 日期：2026-09-15
- 终端：110x30，隔离 state/socket/log（/tmp/usability-r16*）
- 构建：工作区（含 ICY 名称解析 + 流暂停标志）
- 评分：9/10（R10 同人设为 8/10）
- 目的：验证 `a` 自定义 URL 的 ICY 命名与暂停反馈
- 结果：全链路通过——自动命名、收藏、重播、重启持久化、暂停/恢复

## Agent 报告（原文）

# Round 16 报告：朋友发来电台链接的用户

## 时间线
1. 启动 → `?` 帮助显示 `a add a stream URL to Favorites and play it`。
2. `Escape`、`Tab` → Radio Favorites，显示 `(empty) — press a to add a stream URL`。
3. `a` → 出现 `Stream URL: https://stream.example/live` 输入框。
4. 输入 `http://ice2.somafm.com/indiepop-128-aac`、`Enter` → 收藏列表和 Now Playing 均显示
   `Indie Pop Rocks! [SomaFM]`；提示 `Added to Favorites and playing: Indie Pop Rocks! [SomaFM]`，
   状态 `LIVE · playing · live stream`。**非常好，URL 被识别成可读台名。**
5. `Space` → `LIVE · paused · live stream`，底部提示 `space resume`；再 `Space` → `LIVE · playing`。
6. `v` → `Stopped` / `Nothing playing`；`Enter` 从 Favorites 再次播放 → `LIVE · playing`；`Space` 暂停。
7. `q` 退出，重启 → Favorites 仍是 `Indie Pop Rocks! [SomaFM]`，持久化正常。
8. `q` 退出并确认会话结束。

## 名称可读性评价
- Now Playing：`★ Indie Pop Rocks! [SomaFM]`
- 收藏列表：`Indie Pop Rocks! [SomaFM]`
- 添加提示：`Added to Favorites and playing: Indie Pop Rocks! [SomaFM]`
- 全部可读，一眼可认出，不再是一长串 URL。

## 发现的问题
1. **低**：添加流地址时未提供手动自定义名称入口；本次自动识别成功，不影响核心体验，
   但对无法识别元数据的电台可能需要兜底。

## 好的方面
- `a` 的「添加并播放」流程直接，符合收到链接立即试听的预期。
- 自动解析电台名并同步用于收藏、播放页与提示。
- 自动收藏成功，无需额外步骤。
- 从收藏 `Enter` 稳定重播；暂停/恢复状态与 footer 提示明确。
- 重启后收藏仍在，且默认回到 Radio Favorites。

## 改进建议
- 自动识别失败时提供可选「名称」编辑兜底。
- 成功提示可附带更短的操作提示（如「可在 Favorites 再次播放」）。

## 总体可用性评分（1-10）与一句话结论
**9/10**：朋友发来链接后，lilt 快速播放、自动命名并持久收藏，整体非常顺畅。

## 编排者复核

- **B2 验证通过**：ICY `icy-name` 解析在生产流上生效（SomaFM 实际返回可读台名），
  Now Playing / 收藏 / 提示三处一致，较 R10 的「只有 URL」显著改善。
- **C1 验证通过**：暂停立即显示 `LIVE · paused`，footer 切换为 `space resume`，此前 R6/R9 的
  「按了暂停仍显示 buffering」未复现。
- 存留低项：无法识别 ICY 时无手动改名入口，记入 backlog（与未来 ICY/元数据设计一起）。