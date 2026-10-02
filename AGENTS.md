# AGENTS.md

本文件是 agent / 协作者在本仓库工作的持久约定。**权威规范在 `docs/`**；本文件只做
导航与工作方式约定，不重复会漂移的细节。若有冲突，以对应规范为准。

## 项目

lilt 是 macOS / Linux 上 Apple Music、Audius、Jamendo 与网络电台的 CLI + TUI。`lilt serve`
是每个隔离 state root 的唯一常驻 server（Client API v0.1 over Unix socket），持有播放路由、队列与
`state.json`；macOS 的 `lilt-player` 与 `lilt-audio` 是私有签名 Swift helper。技术规范默认中文；
公开使用指南有英文入口，`docs/product/roadmap.md` 与对外 `music-control` skill 使用英文。
代码标识/注释用英文。

## 迭代节奏与兼容

- 现在处于**快速迭代期**，Client API（版本号固定写作 **`v0.1`**，长期保持）会快速变化：
  可以直接修改 wire 模型、命令、错误语义与持久 schema，**不做向后兼容**、不保留
  deprecated 别名或兼容分支。
- 但迭代 **MUST NOT 破坏现有功能**：完成标准是**单元测试保持绿色**
  （`go test ./...` / `just test`；提交前 `just verify`）。删除或改变行为时同步更新测试与文档，
  不要留下只在文档里存在的旧路径。
- 发现“旧式/兼容”之类的历史包袱时，直接删除并修正确的一方，而不是叠加兼容层。

## 命令

```text
just build          # Go + 签名 helper（Linux 上仅 Go）
just build-go       # 仅 Go
just test           # go test/vet + swift build/test（Swift 仅 macOS）
just verify         # fmt + go test/race/vet + swift build/test + workflow-check + git diff --check（Swift 仅 macOS）
just provider-gate  # provider 准入：go test -race ./... + go vet ./...
just fmt-check      # 已跟踪 Go 文件的 gofmt 一致性
just skill-check    # skill 命令/错误码与 in-process catalog 的一致性
just audit-check    # 公共 kind 枚举与 client surface 的结构一致性
just promote        # verify + build，固定一份预发布 CLI/helper（日常 server 活跃时拒绝）
just run            # 纯 TUI：默认 pre-release 日常 server；--env dev 为私有会话
just test-drive     # Herdr + agent 的私有试驾会话（同 --env/--fake/--browser）
just stop-daily     # 显式停止记录在案的日常预发布 server
just usage          # just 命令使用统计（user/ai 各用了哪些，来自 gitignored .just-usage.tsv）
```

`just verify` 是提交前门禁。CI 目前单独跑 Go/Swift 检查；不要以“CI 没跑”为理由跳过本地门禁。
Swift 相关 recipe 在 Linux 上自动跳过（helper 是 macOS-only）；NixOS 用 `nix develop` 进入 flake
devShell（go/just/zsh/sqlite/mpv），`nix run .#` 直接构建并运行 Go 二进制。
要跑 Linux 的 Apple Music 引擎用 `nix develop .#apple`：它额外提供 Widevine
Chromium 并导出 `LILT_CHROMIUM_PATH`（CDM 是专有组件，故与默认 shell 分开）。

## 工作约定（硬性）

- **提交时机**：**不随任务完成就提交**。一次任务（或一组相关改动）做完后先停下，改动留在工作树里，
  在下一次明确的新任务开始前**询问用户是否提交**，或等用户主动要求提交。
- **`git push` 永远只在用户明确要求时执行。**
- **提交粒度**：按主题分组，一条消息说清“做了什么 + 为什么 + 删掉了什么旧路径”；提交前跑 `just verify`，
  文档改动还须由文档测试工程师审阅。
- **每个 Phase 的 done = 代码 + hermetic 测试 + 文档工程师同步文档 + 文档测试工程师审阅 + `just verify`。**
  任一项缺失只能标为 in progress。
- **行为变更先写四行「行为卡」**（在对话中，不另建模板文件）：用户要完成什么 / 绝不能暗中做什么 /
  等待与失败时显示什么 / 用什么场景验收。意图不明确时先问清楚，再改代码；不得用隐藏回退把失败
  伪装成另一种成功。适用于 TUI、CLI、server 等用户可见行为，不要求纯重构重复填写。
- **测试阶梯**：先跑相关单测，再跑受影响包，改动完成后跑 `just verify`；文档核验按文档测试工程师
  的 `review changed` 进行。不必每次小改都重跑全套。必要的隔离真机/可用性复测须用户授权，不能代替 hermetic 测试。
  交付时写清成功与失败路径各由什么证据覆盖、实机是否验证最终构建；未验证的路径不要写成通过。
- **长会话交接**：阶段切换、委托子 agent 或结束未完任务时，在对话中简记已定决定、当前改动、
  已验证、未验证和下一个动作；委托时再写文件范围与验收条件。不要为此新增文档体系。
- 只改与任务相关的文件；匹配现有风格；**不新增第三方依赖**（Go 优先 stdlib）。唯一例外：
  Activity 存储（`internal/activity`）允许 `modernc.org/sqlite`（纯 Go、BSD-3-Clause），
  见 [`docs/internals/persistence/local-activity.md`](docs/internals/persistence/local-activity.md)；其他用途仍需先修改本约定。
- **代码、文档、设计三者必须一致，且只实现“当前最佳做法”。**
  - **不写 legacy / 兼容 / 猜测性历史处理**：不保留 deprecated 别名、不推测旧格式、不为“万一”加分支。
    发现旧包袱时直接删掉，并修正确的一方（文档或代码）。
  - 需要的迁移只是**设计里明确声明的 schema 转换**（例如 `state.json` v1→v2），按声明确定性改写，
    不做格式嗅探或兜底猜测。
- 测试必须 hermetic：不访问网络、不依赖真实账户/Keychain/系统弹窗；用 `httptest` / mock
  transport / fixture。真实 E2E 只能 opt-in，并以带原因的 skip 表示。本机默认走静音测试；
  预发布/开发/共享账号真实播放的隔离入口与验证边界见 `docs/testing/integration.md` §5a。
  `just verify` 和默认走查不得出声；获批 real 也不得与日常播放并发。
- **skill 只写触发、策略与配方**：命令/参数/返回/错误码以 `lilt api --json` 与 `docs/client-api/`
  为准，不在 skill 里重复（重复会漂移）。需要 workaround 才能用 CLI 时，先修 CLI/API，再删掉
  那段说明。
- 发现文档与实现矛盾时，修正确的一方，并在交付说明里明确指出矛盾的双方。
- 实现或产品行为变化时，实施者把行为与证据交给文档工程师（`write`）同步权威文档与使用者入口；
  若无需更新，由文档工程师说明原因。随后由文档测试工程师（`review changed`）核对事实、示例、链接和
  可读性。两种职责见 `.agents/skills/docs-maintenance/SKILL.md`；`just verify` 不替代文档审阅。
- 新增/修改 provider 前先读 `docs/testing/provider-admission.md` 并跑 `just provider-gate`。

## 权威文档

| 主题 | 文档 |
|---|---|
| 架构与所有权 | `docs/architecture.md` |
| 安装与第一次播放 | `docs/getting-started/` |
| 按任务指南（TUI/CLI/agent/电台/排障） | `docs/guides/` |
| Provider / 播放传输 / 分阶段实施 | `docs/internals/providers/providers.md` |
| Provider 准入与门禁 | `docs/testing/provider-admission.md` |
| 测试分层与真实 E2E | `docs/testing/integration.md` |
| Source、identity、BrowseNode | `docs/internals/providers/sources.md` |
| `state.json` schema 与迁移 | `docs/internals/persistence/state.md` |
| Client API（模型/命令/错误/watch） | `docs/client-api/` |
| TUI 产品、设计与异步状态 | `docs/ui/model.md`、`docs/ui/design-system.md`、`docs/ui/async-state.md` |
| helper 私有协议 | `docs/internals/playback/helper-rpc.md` |
| 产品路线与已知限制 | `docs/product/roadmap.md`、`docs/product/limitations.md` |
| 未解决的工程问题台账 | `docs/product/open-questions.md` |
| 文档地图 | `docs/README.md` |

实现状态以 `docs/product/roadmap.md` 与 `docs/internals/providers/providers.md` 的 Phase 表为准，不要在
本文件维护状态副本。

## Provider 约定

- provider 是**编译期组件**，随 server 启动注册；**没有运行期插件**，CLI/TUI/skill 只能调 Client API。
- 只实现并声明自己真正支持的 capability；不支持的返回 `unsupported_command`，不静默降级。
- **capability 是唯一真值**：discovery 路由只读 `Descriptor.capabilities`，不另维护并行“支持列表”。
  descriptor/capability 可带可选 `description` 供 agent 参考，不参与路由或判定。
- `discovery.search` 的 wire `source` 必填；radio 发现用 `radio.search`，不是 `--source radio`。
- 稳定 ID / ref：`apple-music` → `am:<provider-id>`、`audius:<kind>:<provider-id>`、
  `radio:<normalized-url>`；canonical ref 前缀等于 source id。kind 属于公共枚举
  `song|playlist|album|station|stream`。
- **短期/签名媒体 URL 绝不进入**持久状态、公开 response、watch event、**默认日志**或 fixture；
  只在播放启动时解析。`core.Item.URL`/`api.Item.URL` 只放稳定公开 URL。唯一例外：操作者显式开启的
  开发诊断日志（`LILT_LOG_LEVEL=debug` / `LILT_DEV_LOG=1`）为本地排障可写全保真内容。
- **凭据/密钥永不进日志**：任何级别的日志（含 debug）都必须拒绝 `token`/`secret`/`password`/
  `authorization`/`keychain`/`credential`/`api_key` 等字段与 Authorization 头。
- 错误只用 `docs/client-api/errors.md` 的稳定 code；上游原始信息只可脱敏放入
  `details.providerCode`。

## 代码地图

| 位置 | 作用 |
|---|---|
| `internal/server/` | 唯一 server、Client API handlers、provider registry、state 投影 |
| `internal/audius/`、`internal/radio/`、`internal/builtin/` | 来源实现 |
| `internal/mpvplayer/` | Linux 播放后端（进程内 mpv JSON IPC；无 build tag，hermetic 测试用假 mpv）；契约见 `docs/internals/playback/linux-mpv-engine.md` |
| `internal/appleweb/` | Linux 与 macOS opt-in 的 Apple 浏览器引擎：CDP over pipe 驱动 Apple 自家 web player（macOS Chrome 自带 Widevine；纯 stdlib）；契约见 `docs/internals/playback/apple-web-engine.md` |
| `internal/playrouter/` | 跨平台播放路由：把 streams 后端（Linux mpv / macOS lilt-audio）与 Apple 浏览器合成 server 的 `AudioEngine` + `URLPlaybackDriver`，负责互斥与状态流合并 |
| `internal/state/` | `state.json` schema、迁移与持久化 |
| `internal/client/`、`internal/tui/`、`cmd/lilt/` | client 侧（TUI/CLI/skill 入口） |
| `skills/music-control/` | **对外**发布的 agent skill（音乐/电台播放控制，自包含）；`just agent-install` 安装到 harness 全局 skills |
| `.agents/skills/tui/` | 修改 TUI 时的 agent skill：规范加载顺序、骨架不变量、验证清单 |
| `.agents/skills/docs-maintenance/` | 文档工程师 `write` 与文档测试工程师 `review`：同步实现、独立核验事实与可读性 |
| `.agents/skills/usability-test/` | 基于真实构建的 agent 可用性走查 skill：轮次/prompt/隔离装置/汇总格式（运行产物不入库） |
| `.agents/skills/session-triage/` | 已发生的真实使用故障：保留现场、只读取证、跨 helper/server/TUI 定位 |
| `player/` | Swift helper（`LiltPlayer`）；内部协议见 `docs/internals/playback/helper-rpc.md` |
| `scripts/check-doc-links.py` | 文档测试工程师使用的本地 Markdown 链接/锚点检查脚本 |

skill 分两类，**同一个文件不存两份**：

- **对外**（用户/外部 agent 加载）：`skills/music-control/`，由 `just agent-install` 安装到 harness 全局
  skills（`~/.agents/skills/`，ZCode 等原生读取；及 OpenCode 的 `~/.config/opencode/skills/`）。
  它是产品制品，与 `player/` 同级看待。
- **对内**（开发/测试本仓库时加载）：`.agents/skills/tui/`、`.agents/skills/docs-maintenance/`、
  `.agents/skills/usability-test/`、`.agents/skills/session-triage/`、`.agents/skills/architecture-audit/`。
- `.agents/skills/music-control` 是指向 `skills/music-control` 的**软链**，`.opencode/skills` 是指向
  `.agents/skills` 的软链；仓库内的 music-control 只有 `skills/music-control` 一份真身。但 harness
  全局的 `~/.agents/skills/music-control`（ZCode 原生读取）与 `~/.config/opencode/skills/music-control`
  是 `just agent-install` 复制出的**物理副本**，会 shadow 仓库侧软链路径，只在安装那一刻与仓库一致；
  skill 更新后必须重跑 `just agent-install`，否则外部 harness 加载到的是旧副本。

## 测试与本地状态

- 测试用 `LILT_SOCKET` / `LILT_STATE` / `LILT_CONFIG` / `LILT_RADIO_CACHE` 隔离路径，
  `LILT_FAKE_PLAYER=1` 可绕开真实 helper。
- 排查用户刚发生的真实操作时，使用 `.agents/skills/session-triage/SKILL.md`，按
  `docs/testing/integration.md` 的「已发生的真实会话排障」只读取证；不自动开新会话或有声复测，
  不与主动走查的 `usability-test` 混用。
- 状态相关改动必须覆盖 v1 → 当前 version 的迁移、幂等重载与 round-trip fixture。
