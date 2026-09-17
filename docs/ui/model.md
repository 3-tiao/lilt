# Spec: UI 模型与导航（Source / Surface / Home）

> **状态**：设计完成、**尚未实现**。当前 TUI 的具体渲染见 [`ux.md`](ux.md)，是过渡实现。
> 本文件与渲染方式无关，供 TUI、向导式 UI 与未来客户端共用；实现时以本模型为准。

## 1. 目标与约束

- **好理解、好操作**：常规操作在首页即可完成；其余操作一步可达。
- **适配不同 provider**：切换 provider 时**骨架不变**，只有可用的 surface 增减（宽容度）。
- **避免**
  - 把 `browse`/大列表放进首页——比例会失控、挤占首页。
  - 用大量开关、反复展开/折叠来承载内容——把操作变多。
- **允许**：直接按键或点击“进入一个 surface / 推入一页 / 返回”，以及覆盖常规动作的全局快捷键。

## 2. 与 UI 无关的模型

| 概念 | 说明 |
|---|---|
| **Source** | provider；声明 capabilities 与 availability |
| **Surface** | 可寻址的“面”，有稳定 id、可用性、内容与动作（见 §2.1） |
| **Item** | surface 内的条目（可播放/可进入） |
| **Action** | 与渲染无关的动作：play/pause/next/previous/stop、favorite、queue-add、search、switch-source、jump |
| **Navigation state** | `currentSource` + `currentSurface` + 推入栈（详情页）+ overlay |
| **Overlay** | `search`、`help`、`panel` 等临时层，不改变 surface 归属 |

### 2.1 规范 Surface 集合

| id | 典型 label | 内容 | 可用性来源 | 备注 |
|---|---|---|---|---|
| `home` | Home | 见 §3 | 恒有 | 每个 source 的默认面 |
| `discover` | Browse / Discover | source 自带发现（Radio 目录+筛选、Audius trending） | provider capability/内置 | 外部面；**不进 Home** |
| `favorites` | Favorites | 本地收藏 | 本地 store | 恒有 |
| `recent` | Recent | 本地最近播放 | 本地 store | 外部面；**不进 Home** |
| `playlists` | Your playlists | 资料库歌单 | `library` capability | provider 可选 |
| `queue` | Up Next | 当前有限队列 | 有 finite-queue 会话 | player/queue |
| `auth` | Account | 授权/账号状态 | 需要/可选授权 | `authorization.*` |

`search` 是 **overlay**（`/`），不是 surface；结果是可返回的临时列表。

## 3. Home（方案 C：混合）

`Home` 由有序的行组成，行有两类：**预览（preview，≤5 条）** 与 **入口（entry，单行）**。
只在“可用且非空”时渲染；无折叠/开关。默认顺序（易改）：

1. `continue_playing` — 该 source 有活动会话时：当前曲目 + `queue` 入口。
2. `recent`（preview ≤5）— 本地最近播放，通用；`Recent` surface 仍可看全量。
3. `trending`（preview ≤5）— 声明 `search.trending`（如 Audius）。
4. `playlists`（preview ≤5 或 entry）— 声明 `library`（如 Apple）。
5. `favorites`（preview ≤5）— 本地，通用。
6. 入口行：`Search`(`/`)、`Discover`/`Browse`、`Recent`、`Queue`、`Account`（按需）。

规则：`browse` 的**内容**不进 Home（只保留入口）；`recent` 可进 Home，但必须截断（≤5），
全量在 `Recent` surface。

## 4. 导航

- **全局快捷键**（不随 surface 变化）：`space` 播放/暂停、`n`/`b` 上/下一首、`/` 搜索、`:` 命令面板、
  `f` 收藏、`Esc` 返回、`?` 帮助、`1..n` 直达 surface。
- **Surface 导航（已定）**：`1..n` 数字直达常用 surface，长尾与动作走 **`:` 命令面板**；
  **不渲染 surface/source tab 行**，也不使用 `Ctrl-P` 之类的第二入口。
- **`:` 命令面板**：弹出输入 + 实时筛选列表，既可选 surface/最近条目，也可输入带参数命令
  （`:home`、`:discover`、`:recent`、`:queue`、`:auth`、`:source radio`、`:play <ref>`）。
  **先实现一个基本命令集与 `Tab` 补全**（匹配命令名与参数），后续按实际使用再增补；
  `?` 列当前支持的命令。与 `/` 分工：`/` 搜内容（provider），`:` 走 UI/命令（同 vim 的 search / ex）。
- **推入栈**：歌单详情、目录筛选等是“进入/返回”，不占 Home 比例。

## 5. 切换 Source（明确、较重，不用 tab）

不采用 tab 切换 Source，原因：
1. provider 会越来越多，来回切太麻烦；
2. 任一时刻只有一个 provider 播放，切换应是**明确事件**，并可顺带做清理。

设计：
- 入口：`s` 打开 **Source switcher overlay**：列出所有 source + `availability` +
  关键 capability 摘要，当前 source 高亮。
- 切换语义（一次原子提交）：
  1. 若正在播放，先停止当前 source 的播放/清空临时队列（或先请求确认）；
  2. 清空推入栈与搜索态；
  3. 载入新 source 的 `home`；
  4. 更新 `lastSource`。
  取消或失败不改变当前 source 与播放。
- 与 Client API 的 `activeSource`/播放互斥语义一致。

## 6. Provider 适配

- **P1（当前采用）**：UI 只认规范 surface 集合；provider 通过 capabilities 声明能力。
  未声明的 surface 隐藏，或显示“不可用 + `reason`”。
- capability → surface 映射：

| capability | 影响的 surface/行 |
|---|---|
| `search.songs` / `search.playlists` | `search` overlay、`discover` |
| `search.trending`（新增） | Home `trending` 预览 |
| `library` | `playlists` |
| `recommendations` | Home 推荐行（如启用） |
| `playback.*` / `queue` | `queue`、全局播放动作 |
| （本地，无 capability） | `favorites`、`recent`、`home` 恒有 |

## 7. 多 UI 渲染

同一模型可由不同 UI 渲染：
- **当前 TUI**（键位与布局见 [`ux.md`](ux.md)）：主列表 + 右侧 Up Next + Now Playing 带 + overlay。
- **向导式 UI**（类似 archinstall）：把 surface 顺序化，逐步“选择 source → 选择内容 → 确认播放”，
  顶部面包屑 + 底部操作提示；不改变本模型。

## 8. 未决问题

1. Home 各 preview 的截断数量与顺序（默认 ≤5，见 §3，易改）。
2. `discover` 的 label（Radio=“Browse”、Audius=“Discover”）是否按来源定制。
3. 空 Home / 空 section 的呈现（隐藏 vs 空态提示）。
4. `:` 命令集：先实现基本集合 + `Tab` 补全（命令名/参数），后续按使用情况增补，不预先设计大而全的语法。

## 9. Links

- [`ux.md`](ux.md) — 当前 TUI 布局与键位（过渡实现）
- [`../internals/sources.md`](../internals/sources.md) — Source、identity、BrowseNode
- [`../client-api/models.md`](../client-api/models.md) — SourceDescriptor 与 capability
- [`../internals/providers.md`](../internals/providers.md) — provider 与播放传输
