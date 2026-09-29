# Spec: State（偏好 UI 状态）

本地优先。`state.json` 只保存**轻量 UI 偏好**；用户数据（Favorites、完整 Playback History、派生
Recent）属于 Activity SQLite store（`internal/activity`），schema 与门禁见
[`local-activity.md`](local-activity.md)。没有 server 运行时，文件是偏好的耐久事实来源；server
启动后读取文件，并以“成功持久化后的内存快照”作为运行期权威状态。Client API client 不得直接写
文件，server 也不监视运行期间的外部编辑。当前产品范围不包含云同步。

## 路径

| 用途 | 路径 | 覆盖变量 |
|---|---|---|
| 配置 | 当前实现：`~/.config/lilt/`（`themes/*.toml` 等；平台默认路径修正见 roadmap） | `LILT_CONFIG` |
| 状态 | 当前实现：`~/.local/state/lilt/state.json`（平台默认路径修正见 roadmap） | `LILT_STATE` |
| cache/socket | macOS `~/Library/Caches/lilt/`（`radio-cache.json`、`session.sock`） | `LILT_RADIO_CACHE` / `LILT_SOCKET` |
| server 生命周期锁 | 与 state.json 同目录 `server.lock` | 随 state root 派生，不独立覆盖 |
| Activity store | 与 `state.json` 同目录 `activity.sqlite3`（含 `-wal`/`-shm`） | `LILT_ACTIVITY_DB` |
| Apple 会话（浏览器 profile） | Linux：`~/.local/share/lilt/apple-browser/`（`XDG_DATA_HOME/lilt/apple-browser`）；macOS：`~/Library/Application Support/lilt/apple-browser/` | `LILT_APPLE_PROFILE` |

Apple 会话是**机器级凭据**（一次登录服务同一台机器上的每个 lilt server），因此**不随 state root 派生**：
手册会话与测试用的是各自的 `LILT_STATE`，若 profile 跟着它走，那些会话就会找不到已登录的 profile 并静默退成试听。
profile 只有在 lilt 原子创建目录时才写入所有权 marker；既有外部目录可复用但永不补写 marker、也永不由
disconnect 删除。浏览器存活期间持有同级 `<profile>.lock` 的非阻塞独占锁，防止不同 state root 的 server
同时使用该机器级资源；disconnect 对不存在的目录成功，对无 marker 的目录返回 `invalid_state`。

当前实现中（Apple browser profile 的 macOS native 路径除外），显式 `XDG_CONFIG_HOME`、`XDG_STATE_HOME`、`XDG_CACHE_HOME`、`XDG_DATA_HOME` 优先于上述默认，
显式 `LILT_*` 路径覆盖对应派生路径。Activity 与 lifecycle lock 已统一从 state root 派生，
`LILT_SOCKET` 只改变 disposable socket，不再分裂耐久数据。

平台 native user directory 修正属于 roadmap 中的独立迁移：实施时首次启动 MUST 一次性迁移既有
XDG-style `~/.config/lilt` / `~/.local/state/lilt` / cache 路径（或明确报告冲突），原子完成并留下
migration marker；不得静默分裂读写两套状态。主题随配置迁移。在该 Phase 完成前，不把目标路径写成
当前行为。

## `state.json` v3（Activity 迁移后）

```jsonc
{
  "version": 3,
  "theme": "gruvbox",                 // themes/ 下的内置名或自定义文件名；空/`default` 在加载时解析为 gruvbox
  "lastSource": "apple-music",        // UI 下次启动选择的 source
  "lastPlaybackSource": "apple-music" // stopped PlaybackState.source 的恢复值
}
```

v2 的 `favorites` / `recent` / `recentContainers` 字段在读取时**忽略**、下次保存时消失；不迁移、
不保留兼容分支——那是开发期测试数据，Activity store 从空开始（见
[`local-activity.md`](local-activity.md) §2）。

## 规则

- **版本**：`version` 递增；同版本读取时对未知字段宽容（**忽略**，下次保存不保留），缺失字段用默认。
  高于当前实现的 future version 以只读方式拒绝，原文件不重写；语法错误或字段类型错误必须先完整解码
  失败，再用不覆盖已有备份的 `.corrupt-*` 名称隔离后以空状态启动并显示警告。若隔离失败则阻止所有
  保存，绝不覆盖原文件。
- **主题**：`theme` 存主题名（文件名去 `.toml`）。
- **持久化**：所有 mutation 先写临时文件并原子替换，成功后才替换权威内存状态。保存失败 MUST 返回
  错误，不改变权威内存状态、不自动重试。
- **广播发现查询**：Radio Search & Filters 的文本与 facet 只存在于当前 TUI 会话，直接驱动 Browse 的
  内容与标题；不会写入状态、不会在重启后恢复，也不影响收藏、最近播放、当前播放或自定义 URL。

## Server 启动

server 启动前 MUST 获取每用户 advisory/process `server.lock` 并持有至退出；只有 lock holder 能
创建/删除 socket。取得锁后才可检查并回收 stale socket。`serve --detach` 仅在 Client API listener
已接受请求后成功，返回 `{pid}`；engine/source 初始化可异步，通过 `sources.list` availability 与
watch 观察。竞争者不得删除 socket 或猜测启动结果：锁已被持有时先等待现有 listener 可连接，确认
ready 后才返回 `active_session`；若持锁进程在 ready 前退出则重新竞争锁，等待超过 10 秒启动预算才
返回 `serve_failed`。因此 `active_session` 与 detach 成功都保证 transport 已 ready，不给 agent
留启动 race。

Client API 对外返回归一化 AppState（Favorites/Recent 来自 Activity store），它不是本文件磁盘
JSON 的逐字段镜像；server 负责二者转换。见 [`../client-api/models.md`](../../client-api/models.md)。

授权凭据、OAuth state、token 和 refresh material 不属于 `state.json` 或 Activity store；它们只能
由对应 provider 保存到 OS secure storage（`internal/securestore`：macOS Keychain，测试用内存
实现）。Audius 的 token bundle 以 service `lilt`、account `audius` 存储；Jamendo J1 把
`client_id` 以 service `lilt`、account `jamendo.client_id` 存储（应用级凭据，不是用户授权，见
[`jamendo.md`](../providers/jamendo.md)）。

`radio-cache.json` 不是用户状态或同步事实来源，可以随时删除。它按 `stationuuid` 保存最多 1000 个
短期 Radio Browser profile，按规范化 `url_resolved` 的 SHA-256 保存最多 500 个本机 endpoint
health；自定义 URL 只保存 hash，不把 URL/query/token 写进 cache。目录 URL 变化会产生新的
endpoint key 并触发重新探测。
