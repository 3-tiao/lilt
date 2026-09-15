# 真实用户可用性测试 · 2026-09-15 · 第四批（Round 15-17）· 修复验证批

## 背景

第三批后制定了修正计划：Phase A（A1-A5 选中态/确认心智/文案/定位）、Phase B（B1 镜像 failover、
B2 自定义 URL 命名）、Phase C（C1 流暂停反馈、C2 MusicKit 启动瞬态、C3 队列操作提示）。
本批在**包含全部 A/B/C 改动**的构建上复跑关键人设，验证修复效果。

## 修复清单与验证结果

| 项 | 内容 | 验证 |
|---|---|---|
| A1 | 选中态非颜色标记（菜单/facet/主题/顶栏 `[Radio]`、`[2 Recent]`） | R15/R17 均读到「一眼可辨」（R17 三处逐项确认） |
| A2 | 确认按钮动态命名（`Show all`/`Search "…"`/`Apply filters`/`Search + filters`） | R15 读到 `[ Search "jazz" ]` 并理解 |
| A2+ | 文字提交后焦点自动落到确认按钮（R15 发现→即时修复） | 补测试 + PTY |
| A3 | `a (Radio)` 帮助标注；`f` 无选中提示；播放中 footer `v stop` | R16 帮助读到范围；footer `v stop` 真机可见 |
| A4 | 首屏定位句 `Apple Music & radio — Tab switches source, / searches` | R15 启动即读到 |
| A5 | 选项加载失败短文案（不再露原始 URL） | 单测 + 覆盖 |
| A5+ | Radio 目录错误友好文案（不再露原始 URL；R15 发现→即时修复） | 补测试 `TestRadioDirectoryFailureUsesFriendlyCopy` |
| B1 | Radio Browser 静态 failover（de1→de2，7s per-request） | 单测 `TestDirectoryFailsOverToMirror` |
| B2 | 自定义 URL ICY 名称解析 | **R16 生产流实测**：`Indie Pop Rocks! [SomaFM]` 三处一致 |
| C1 | 流暂停立即反映 `paused` | **R16 实测**：`LIVE · paused` 即时，未再出现 buffering 残留 |
| C2 | MusicKit 启动瞬态收敛为 `State: starting` | 真机 PTY 观察 3 帧 + 单测 |
| C3 | 队列删除当前曲/重排提示 | 单测（`Removed current track — playback advanced`、`Queue reordered`） |

## 第四批评分

| 轮 | 人设 | 评分 | 关键结论 |
|---|---|---|---|
| 15 | 完全新手 | 7 | A1/A2/A4 生效；又发现 2 个小项，已即时修复 |
| 16 | 朋友分享 URL | 9（R10=8） | B2/C1 全链路通过 |
| 17 | 60x12 小窗口 | 6（R13=7） | A1 收口；2 个布局项，1 项已修 1 项属 Phase D |

## 遗留（进入 Phase D 决策）

1. **帮助弹层滚动/分页（D2）**：60x12 下截断；已加 `(more — resize for the full list)` 提示，滚动未实现。
2. **Apple Music Favorites 视图（D1）**：AM 收藏仍无列表视图（R11 缺口，本批未处理）。
3. 低优先级 backlog：自定义 URL 无法识别 ICY 时的手动改名；窄窗口紧凑条目格式；
   中文本地化；`Mode: full/preview` 术语面向用户的可读化。

## 稳定结论（多批一致）

- 收藏、取消收藏、搜索+过滤、Browse 重置、Up Next 全功能、主题持久化、流命名与暂停反馈均已通过真实用户验证。
- 三个高严重度 bug（收藏缓存、actionMsg 元数据竞态、歌单起点）均有回归测试且不再复现。
- 选中态可见性（跨五轮的头号问题）经 A1 收口。

## 留档索引

- [round-15-newbie-recheck.md](round-15-newbie-recheck.md)
- [round-16-custom-url-recheck.md](round-16-custom-url-recheck.md)
- [round-17-tiny-window-recheck.md](round-17-tiny-window-recheck.md)

## 下一批建议

1. 就 Phase D1/D2 做决策；D2 落地后复跑 60x12 人设。
2. 新增人设：Apple Music 播放中断恢复、preset 用户、慢网络（需限速装置）。
3. 提交本批全部改动 + 档案。