# 本地 Activity 存储计划（SQLite）

> **状态：Phase 0–3 已实现（存储、API/CLI、TUI 闭环）；Phase 4 的真实验收（usability-test）待跑。** 本文定义 Favorites、完整 Playback History 与 Recent
> 的目标存储和分阶段实施门禁。当前实现仍以 [`state.md`](state.md) 的 `state.json` v2 为准；每个
> Phase 只有在代码、hermetic 测试、对应权威文档、`just verify` 与 `just docs-check` 全部完成后才算
> done。
>
> **Phase 0 结果**（Apple M2 Pro，Go 1.26，`modernc.org/sqlite v1.59.0`，BSD-3-Clause，
> fixture：1,000,000 次播放 / 100,000 Item / 10,000 Favorites，全量生成后 VACUUM，81.9 MB）：
>
> | 门禁 | 目标 | 实测 |
> |---|---|---|
> | Open + Recent 100 + Favorites 全量 | < 250ms | ~17.5ms |
> | `RecentItems(100)` | < 50ms | ~0.24ms |
> | `StatsForRefs(500)` | < 100ms | ~1.8ms |
> | `HistoryPage(200)`（中部 cursor） | < 50ms | ~0.44ms |
> | 单次达标播放事务 | p95 < 25ms | ~0.18ms/op |
> | DB + indexes | < 250MB | 81.9 MB |
>
> 复现：`LILT_ACTIVITY_BENCH=1 go test ./internal/activity -bench . -benchtime 30x -run '^$'`
> （fixture 缓存在 `os.TempDir()/lilt-activity-bench/`，可删除重建）。二进制体积：链接 driver
> +6.5MB（hello-world 探针 2.5MB → 9.0MB）。cursor 语义固化为 SQLite 行值比较
> `(played_at, id) < (?, ?)`，命中 `playback_history(played_at DESC, id DESC)` 索引。

## 1. 目标与边界

目标：

1. 在本机永久保存每一次达到“听过”阈值的播放，不再把 Recent 当作完整历史。
2. Favorites 与 Playback History 共用同一份持久 Item；Recent 完全从历史聚合结果派生。
3. 为 CLI/agent 提供确定性的列表、批量统计和清理原语；搜索、筛选和多轮推理由 skill 编排。
4. 数据量增长到百万次播放时，启动、Recent、批量“听过没有”查询仍只走有界索引路径。
5. SQLite 故障不能阻止播放，也不能静默丢弃或覆盖用户 Activity 数据。

初版明确不做：

- 不记录未达到阈值的试听、失败的播放请求、精确收听时长、结束原因或播放上下文。
- 不把 playlist/album/station 另存为 recent container；删除 `recentContainers`。
- 不按 `title+artist` 猜测同曲，不跨 source 合并 Item；身份严格使用现有稳定 Item identity。
- 不做自动过期、容量上限、云同步、JSONL 双写或 JSONL 导出。
- 不在 skill 中复制命令参数，也不让 skill、TUI 或 CLI 直接打开 SQLite。

“没听过”只表示：**lilt 本地历史中没有该稳定 Item identity 的达标播放记录**，不声称知道用户在
使用 lilt 前或其他播放器中的收听情况。

## 2. 已定决策

| 主题 | 决策 |
|---|---|
| 写入条件 | 沿用当前阈值：累计 monotonic `status=playing >= min(30s, duration*50%)`；未知/live 为 30s |
| 历史粒度 | 每次达标 INSERT 一条不可变记录；同一 Item 重播会有多条 |
| 保留策略 | 默认永久保留；初版不自动删除 |
| Item identity | 初版严格按稳定 identity；不做 alias 或模糊合并 |
| 上下文 | 初版不保存 playlist/album/station context |
| Recent | 从每个 Item 的 `lastPlayedAt` 派生；无独立 Recent / recentContainers 持久数据 |
| Favorites | 与 History 同阶段迁入 SQLite；共用 Item，关系本身只保存 `itemID + addedAt` |
| 旧数据 | APP 尚在开发期，旧 Favorites/Recent 均视为测试数据；不迁移、不保留 legacy 分支 |
| 存储 | 一个 SQLite 数据库是 Activity 的唯一事实来源；不持久化第二份 JSONL |
| driver | 使用纯 Go SQLite driver，通过 `database/sql` 隔离；允许这一项经门禁验证的存储依赖 |
| skill | 初版不新增智能策略；skill 只经 CLI/API 使用确定性原语，永远不知道磁盘格式 |
| TUI | Home 预览最新 5 条；新增 `All Favorites` 临时页，不新增顶层 surface |
| 故障 | Activity DB 不可用时播放继续；Activity 进入只读故障态，绝不自动创建空库替代 |

## 3. 所有权与文件

```text
<state.json 所在目录>/
├── state.json            # theme、lastSource、lastPlaybackSource 等轻量偏好
└── activity.sqlite3      # Item、Favorites、Playback History、派生 stats
```

当前实现下 state.json 在 `~/.local/state/lilt/`（平台默认路径修正见 roadmap）；Activity
数据库与其同目录，socket 隔离测试自动落在 socket 目录，`LILT_ACTIVITY_DB` 可显式覆盖。

SQLite 的 `-wal` / `-shm` 文件与数据库同目录。目录权限 MUST 为 `0700`，数据库与旁文件 MUST 不向
其他用户开放。只有 `lilt serve` 可以打开数据库和写入；TUI、CLI、skill 仍是 Client API client。数据库不得保存
OAuth token、authorization material、Audius 短期签名 URL、Apple 临时媒体 URL 或其他 secret。
Radio 只保存稳定、规范化后的公开 stream URL。

`state.json` 的目标 schema 删除 `favorites`、`recent` 与 `recentContainers`。本次不读取旧字段并导入；
保存新状态时旧字段自然消失，不增加格式嗅探或兼容分支。

## 4. 目标 schema

时间在 SQLite 中使用 UTC Unix milliseconds；Client API 投影为 RFC3339。相同毫秒内的稳定顺序使用
单调 `id` 打破平局。实际 DDL 在 Phase 1 固化，并由 migration/round-trip fixture 锁定。

```sql
CREATE TABLE items (
    id           INTEGER PRIMARY KEY,
    source       TEXT NOT NULL,
    kind         TEXT NOT NULL,
    stable_id    TEXT NOT NULL,
    provider_id  TEXT,
    ref          TEXT NOT NULL,
    title        TEXT NOT NULL,
    artist       TEXT,
    public_url   TEXT,
    metadata_json TEXT,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    UNIQUE (source, stable_id),
    UNIQUE (ref)
);

CREATE TABLE favorites (
    item_id      INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    added_at     INTEGER NOT NULL
);

CREATE TABLE playback_history (
    id           INTEGER PRIMARY KEY,
    item_id      INTEGER NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    played_at    INTEGER NOT NULL
);

CREATE TABLE item_play_stats (
    item_id          INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    play_count       INTEGER NOT NULL CHECK (play_count > 0),
    first_played_at  INTEGER NOT NULL,
    last_played_at   INTEGER NOT NULL
);

CREATE INDEX playback_history_item_time
    ON playback_history(item_id, played_at DESC, id DESC);
CREATE INDEX playback_history_time
    ON playback_history(played_at DESC, id DESC);
CREATE INDEX item_play_stats_recent
    ON item_play_stats(last_played_at DESC, item_id);
```

`item_play_stats` 是可重建索引，不是第二事实来源。一次达标播放 MUST 在同一事务中：

1. upsert 最新的非空 Item 展示快照；identity 不变；
2. INSERT `playback_history`；
3. INSERT/UPDATE `item_play_stats` 的 count/first/last；
4. COMMIT 后才更新公开内存投影并发布 watch event。

事务失败时四步全部不可见。提供测试专用的 stats rebuild，并验证重建前后结果一致；初版不公开用户命令。

Favorite mutation MUST 在一个事务中 upsert Item 并幂等 set/unset `favorites`。重复 add 不改变原
`added_at`；remove 不存在的 Favorite 仍成功。Item 后续再次出现时，只用最新非空 title/artist/
公开 URL 更新展示快照，不修改 identity，也不持久化短期媒体 URL。

## 5. 查询、性能与增长

### 5.1 有界查询

- Recent：查询 `item_play_stats`，按 `(last_played_at DESC, item_id DESC)` 取默认窗口，再 join Item；
  不扫描 `playback_history`。
- “听过没有”：`history.stats` 将一批 refs 映射为 Item/stats，一次查询；未知 ref 返回
  `playCount: 0`，结果保持请求顺序。
- 完整历史：按 `(played_at, id)` 做 keyset cursor 分页；禁止随页数退化的 `OFFSET`。
- 启动：只打开数据库并加载 Favorites + Recent 窗口；不得把完整历史读进内存。
- watch/AppState：可以携带 Favorites 与有界 Recent，但 MUST NOT 携带完整历史。

初始参数边界：

| 查询 | 默认 | 上限 |
|---|---:|---:|
| `history.list` | 50 | 200 / page |
| `history.stats.refs` | — | 500 / request |
| `recent.list` | 25 | 200 |
| TUI Home Favorites preview | 5 | 固定 |

### 5.2 百万记录门禁

纯 Go driver 在进入实现前必须通过一个可复现 benchmark fixture：1,000,000 条历史、100,000 个 Item、
10,000 个 Favorites。记录机器信息、driver/version、冷/热查询与 DB 大小，不把易波动的 wall-clock
阈值写成 CI 单测。

准入目标（开发机本地 gate）：

| 操作 | 目标 |
|---|---:|
| 打开数据库并读取 Recent 100 + Favorites | < 250ms |
| `history.stats` 查询 500 refs（冷热各测） | < 100ms |
| `history.list` 读取任意 cursor 的 200 条 | < 50ms |
| Recent 100 | < 50ms |
| 单次达标播放事务 p95 | < 25ms |
| 主 DB + indexes（fixture metadata 不含大 blob） | < 250MB |

若 driver 不能满足门禁，先换 driver 或修 query plan，不增加内存全量 cache。每个关键查询要有
`EXPLAIN QUERY PLAN` 测试/断言，确认命中预期索引。SQLite 使用 WAL、`foreign_keys=ON`、有限
`busy_timeout`；具体 `synchronous` 必须通过断电语义和写入 benchmark 决定，不能为跑分快而静默降低
用户数据耐久性。

普通用户按 100 次达标播放/天计算，10 年约 365,000 条，低于上述 fixture。永久保留不会成为正常
使用的性能瓶颈；未来保留策略只能显式配置，不能默认改变“听过没有”的长期语义。

## 6. Client API 与 CLI

接口保持基础、确定性；不增加 genre、recommendation 或“智能 unheard”命令。

### 6.1 History

| command | params | result | 语义 |
|---|---|---|---|
| `history.list` | `{source?, before?, limit?}` | `{items:[HistoryEntry], nextCursor?}` | 每次达标播放一条，可重复 Item |
| `history.stats` | `{refs:[string]}` | `[HistoryStats]` | 保持输入顺序；未知 identity 的 count 为 0 |
| `history.clear` | `{confirm:true}` | `{cleared:int}` | 幂等清空 history/stats；保留 Favorites |

CLI：

```text
lilt history [--source S] [--before CURSOR] [--limit N] --json
lilt history stats <ref>... --json
lilt history clear --confirm --json
```

`HistoryEntry` 至少包含完整 `Item` 与 `playedAt`；`HistoryStats` 至少包含 `ref`、`playCount`、可空的
`firstPlayedAt` / `lastPlayedAt`。缺少记录不是错误。

### 6.2 Recent

现有 `recent.list` 名称保留，但改为从 `item_play_stats` 派生。返回仍是最近的不同 Item；删除
`recentContainers` wire 字段、模型、TUI 合并逻辑与文档，不留 deprecated alias。

### 6.3 Favorites

保留幂等 `favorites.set {item, favorited}` 与 `favorites.list`。新增 CLI：

```text
lilt favorite add <ref> --json
lilt favorite remove <ref> --json
```

`add` 先从 Activity Item 查找；没有时通过只读 Item resolve 原语调用对应 provider，再执行
`favorites.set`。无法得到至少包含 source/kind/stable identity/ref/title 的完整 Item 时返回明确错误，
不得写入裸 ref。Radio URL 可以规范化 URL 并以 URL 作为初始 title。`remove` 优先从 Favorites 本地
解析，目标不存在时幂等成功，不要求 provider 在线。

Item resolve 的 API 形状在 Phase 2 与 provider capability 一起固化；它必须是确定性的基础能力，
不得播放资源或隐式跨 source fallback。

### 6.4 故障与 reset

新增稳定错误 `storage_unavailable`：Activity DB 无法打开、schema 版本过新、migration/transaction
失败且存储不能继续服务时使用。server 仍启动 playback/discovery，并发出持久可见的
`server.warning`；Favorites/History mutation 不得假装成功。

```text
lilt data reset --confirm --json
```

reset 必须在 server 命令锁下关闭 Activity store，把数据库及同组 WAL/SHM 文件一起改名到带时间戳的
归档路径，然后创建并验证空库。不得删除归档。`history.clear` 只处理健康库；`data reset` 是用户明确
放弃当前 Activity 数据后的故障恢复入口。

## 7. TUI

TUI 仍只通过 AppState/watch 和 Client API 工作：

- Home 的 Favorites 按 `addedAt DESC` 预览最新 5 条。
- `Go to → All Favorites` push 一个临时 Page；它不是新的顶层 surface。
- All Favorites 支持播放 song/stream/station、打开 playlist/album、按 `f` 幂等删除。
- 删除后保持最近的有效 cursor；最后一条删除后显示明确 empty state。
- Recent surface 只显示 History 派生 Item，不再混入最近歌单。
- History 全事件流初版只由 CLI/API 提供，不新增 TUI surface。
- loading、save failure、`storage_unavailable` 和 reset 后空状态都要有 hermetic Model 测试。

可见帧改动实施前按 TUI skill 先更新 [`../ui/model.md`](../ui/model.md) / [`../ui/ux.md`](../ui/ux.md)，
给出宽屏与 80x24 mockup，再实现。

## 8. Skill 影响

初版原则上**不修改** `skills/music-control/SKILL.md`：

- skill 不需要知道 SQLite、表、cursor 或 schema。
- 命令参数、返回模型和错误码只写入 `docs/client-api/` 与 `lilt api --json`。
- 存储切换后，已有 `lilt recent` 的意义仍是 lilt 本地 Recent，只是来源从 JSON 列表改为派生查询。

未来实现“播放我没听过的英伦摇滚”时，skill 只增加策略：多轮 discovery 收集候选 → 一次或分批调用
`history.stats` → 由 skill 按 `playCount == 0` 筛选 → 使用已有播放原语。服务端不增加 genre 推理、
跨 source fallback 或 recommendation 编排。该后续阶段应单独做 usability-test，不能与存储切换混测。

## 9. 分阶段实施

### Phase 0 — 规范与 driver 准入（已完成）

- 选择并锁定一个纯 Go SQLite driver/version，检查 license、维护状态与 transitive dependency。
  **已选 `modernc.org/sqlite v1.59.0`（纯 Go，BSD-3-Clause）。**
- 增加独立 benchmark fixture，跑第 5.2 节百万记录门禁及 `go test -race`/release build。
  **已完成：`internal/activity` 含 schema、最小操作与 opt-in benchmark，门禁全过（结果见文件头）。**
- 在 `AGENTS.md` 把“无第三方依赖”收窄为：仅允许该 SQLite driver 作为 Activity store 的已审计例外；
  其他依赖仍禁止。**已完成。**
- 固化 DDL、PRAGMA、数据库路径、错误码、History wire model 与 cursor 编码。**DDL、PRAGMA
  （`foreign_keys=ON`、`busy_timeout=5000`、WAL、`synchronous=FULL`）与行值 cursor 已固化；
  数据库路径/wire model 随 Phase 1–2 落地。**

**Done**：benchmark 报告可复现，driver 通过门禁，文档/API schema tests 先行。

### Phase 1 — Activity Store 与播放历史（已完成）

- 新建独立 `internal/activity`，实现 open/schema/version、Item upsert、history insert、stats rebuild/query、
  health/degraded mode；server 是唯一 owner。
- 把现有 recentTracker 的达标回调改为一个 SQLite 事务；阈值算法本身不改。
- `recent.list` 与 AppState Recent 改读 stats；删除 state Recent/RecentContainers 及所有旧合并路径。
- 覆盖事务回滚、并发串行、重启、重复播放、Radio、未来 schema 拒绝、corruption/degraded tests。

**Done**：完整历史每次达标新增一条，Recent 可由 stats 重建，state.json 不再拥有历史。

### Phase 2 — Favorites、API 与 CLI（已完成）

- Favorites 迁入 Activity store，统一 Item upsert，修复 `addedAt` 与 exact-identity 幂等语义。
- 实现 `history.list/stats/clear`、cursor、CLI JSON 渲染与 500 refs 批量边界。
- 实现 provider Item resolve 与 `favorite add/remove <ref>`；删除 server/state 中重复的 Favorite mutation。
- 实现 `storage_unavailable`、Activity warning 与 `data reset --confirm`，覆盖 WAL/SHM 归档。

**Done**：CLI 可以完整读写 Favorite、查询/清除 History；失败不会产生半事务或空 Item。

### Phase 3 — TUI Favorites 闭环（已完成，视觉对照待复测）

- 先更新 UI 规范与 mockup，再实现 Home 最新 5 条和 All Favorites 临时页。
- 删除 recentContainers UI 路径；验证 Favorites/Recent 在 watch 更新、清空和故障态下同步。
- 覆盖宽屏、80x24、too-small、空列表、保存失败、删除最后一项与 cursor 稳定。

**Done**：超过 5 条 Favorites 仍可完全浏览和管理，Recent 只反映达标历史。

### Phase 4 — 门禁与真实验收

- 更新 `state.md`、architecture、Client API models/commands/errors/watch、README、roadmap；删除旧契约，
  不保留 deprecated 字段。
- 运行 `just verify`、`just docs-check`、百万记录 benchmark 与 DB secret scanner。
- 使用真实构建跑 usability-test：Favorite 超过 5 条、重启保持、CLI/TUI 交叉修改、重复达标播放、
  Recent 派生、history clear、模拟 storage unavailable 和显式 reset。
- 复测通过后归档发现；台账只保留仍未解决项。

**Done**：代码、hermetic 测试、真实复测、性能证据与权威文档一致。

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 纯 Go driver 增大构建时间/二进制 | Phase 0 实测；不过门不进入实现，不以猜测接受 |
| stats 与 immutable history 漂移 | 同事务更新 + deterministic rebuild test；history 是唯一事实 |
| DB 增长导致 Recent/skill 变慢 | stats 表、覆盖索引、keyset cursor、批量 refs、百万记录门禁 |
| DB 损坏让播放器不可用 | Activity 独立 degraded mode；播放/discovery 不依赖数据库健康 |
| 自动恢复造成用户数据消失 | 禁止自动空库；只有 `data reset --confirm` 可归档后重建 |
| SQLite 泄漏短期 URL/secret | Item upsert allowlist + fixture/log/DB 扫描；Audius media URL 永不入库 |
| CLI 增加“智能”导致职责漂移 | CLI 只返回事实和执行幂等操作；候选与 fallback 始终由 skill 编排 |
| exact identity 出现同曲不同 ID | 初版接受；未来可加显式 alias 层，原始历史无需改写 |
