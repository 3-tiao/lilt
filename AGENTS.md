# AGENTS.md

本文件是 agent / 协作者在本仓库工作的持久约定。**权威规范在 `docs/`**；本文件只做
导航与工作方式约定，不重复会漂移的细节。若有冲突，以对应规范为准。

## 项目

lilt 是 macOS 上 Apple Music / Audius / 网络电台的 CLI + TUI。`lilt serve` 是唯一常驻
server（Client API v0.1 over Unix socket），持有播放路由、队列与 `state.json`；`lilt-player`
是唯一的私有签名 Swift 播放 helper。文档用中文，代码标识/注释用英文。

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
just verify         # docs-check + go test + race + vet + swift build/test + git diff --check（Swift 仅 macOS）
just provider-gate  # provider 准入：go test -race ./... + go vet ./...
just fmt-check      # 已跟踪 Go 文件的 gofmt 一致性
just skill-check    # skill 命令/错误码与 in-process catalog 的一致性
just docs-check     # docs/ 与根 README 的 Markdown 链接与锚点
just run            # 前台 TUI
just run-browser    # 前台 TUI，browser 引擎跑 Apple Music（macOS opt-in；Linux 即默认）
just manual-test    # 重建 + 开 Herdr tab：左 pi、右 TUI，共用私有 server 与日志
just usage          # just 命令使用统计（user/ai 各用了哪些，来自 gitignored .just-usage.tsv）
```

`just verify` 是提交前门禁。CI 目前单独跑 Go/Swift 检查；不要以“CI 没跑”为理由跳过本地门禁。
Swift 相关 recipe 在 Linux 上自动跳过（helper 是 macOS-only）；NixOS 用 `nix develop` 进入 flake
devShell（go/just/zsh/sqlite/mpv），`nix run .#` 直接构建并运行 Go 二进制。
要跑 Linux 的 Apple Music 引擎用 `NIXPKGS_ALLOW_UNFREE=1 nix develop .#apple`：它额外提供 Widevine
Chromium 并导出 `LILT_CHROMIUM_PATH`（CDM 是专有组件，故与默认 shell 分开）。

## 工作约定（硬性）

- **提交时机**：**不随任务完成就提交**。一次任务（或一组相关改动）做完后先停下，改动留在工作树里，
  在下一次明确的新任务开始前**询问用户是否提交**，或等用户主动要求提交。
- **`git push` 永远只在用户明确要求时执行。**
- **提交粒度**：按主题分组，一条消息说清“做了什么 + 为什么 + 删掉了什么旧路径”；提交前跑 `just verify`
  与 `just docs-check`。
- **每个 Phase 的 done = 代码 + hermetic 测试 + 对应文档更新 + `just verify` + `just docs-check`。**
  任一项缺失只能标为 in progress。
- 只改与任务相关的文件；匹配现有风格；**不新增第三方依赖**（Go 优先 stdlib）。唯一例外：
  Activity 存储（`internal/activity`）允许 `modernc.org/sqlite`（纯 Go、BSD-3-Clause），
  见 [`docs/internals/local-activity.md`](docs/internals/local-activity.md)；其他用途仍需先修改本约定。
- **代码、文档、设计三者必须一致，且只实现“当前最佳做法”。**
  - **不写 legacy / 兼容 / 猜测性历史处理**：不保留 deprecated 别名、不推测旧格式、不为“万一”加分支。
    发现旧包袱时直接删掉，并修正确的一方（文档或代码）。
  - 需要的迁移只是**设计里明确声明的 schema 转换**（例如 `state.json` v1→v2），按声明确定性改写，
    不做格式嗅探或兜底猜测。
- 测试必须 hermetic：不访问网络、不依赖真实账户/Keychain/系统弹窗；用 `httptest` / mock
  transport / fixture。真实 E2E 只能 opt-in，并以带原因的 skip 表示。
- **skill 只写触发、策略与配方**：命令/参数/返回/错误码以 `lilt api --json` 与 `docs/client-api/`
  为准，不在 skill 里重复（重复会漂移）。需要 workaround 才能用 CLI 时，先修 CLI/API，再删掉
  那段说明。
- 发现文档与实现矛盾时，修正确的一方，并在交付说明里明确指出矛盾的双方。
- 新增/修改 provider 前先读 `docs/testing/provider-admission.md` 并跑 `just provider-gate`。

## 权威文档

| 主题 | 文档 |
|---|---|
| 架构与所有权 | `docs/architecture.md` |
| Provider / 播放传输 / 分阶段实施 | `docs/internals/providers.md` |
| Provider 准入与门禁 | `docs/testing/provider-admission.md` |
| 测试分层与真实 E2E | `docs/testing/integration.md` |
| Source、identity、BrowseNode | `docs/internals/sources.md` |
| `state.json` schema 与迁移 | `docs/internals/state.md` |
| Client API（模型/命令/错误/watch） | `docs/client-api/` |
| TUI 产品、设计与异步状态 | `docs/ui/model.md`、`docs/ui/design-system.md`、`docs/ui/async-state.md` |
| helper 私有协议 | `docs/internals/helper-rpc.md` |
| 产品路线与已知限制 | `docs/product/roadmap.md`、`docs/product/limitations.md` |
| 未解决的工程问题台账 | `docs/product/open-questions.md` |
| 文档地图 | `docs/README.md` |

实现状态以 `docs/product/roadmap.md` 与 `docs/internals/providers.md` 的 Phase 表为准，不要在
本文件维护状态副本。

## Provider 约定

- provider 是**编译期组件**，随 server 启动注册；**没有运行期插件**，CLI/TUI/skill 只能调 Client API。
- 只实现并声明自己真正支持的 capability；不支持的返回 `unsupported_command`，不静默降级。
- **capability 是唯一真值**：discovery 路由只读 `Descriptor.capabilities`，不另维护并行“支持列表”。
  descriptor/capability 可带可选 `description` 供 agent 参考，不参与路由或判定。
- `discovery.search` 的 wire `source` 必填；radio 发现用 `radio.search`，不是 `--source radio`。
- 稳定 ID / ref：`apple-music` → `am:<provider-id>`、`audius:<kind>:<provider-id>`、
  `radio:<normalized-url>`；canonical ref 前缀等于 source id。kind 属于公共枚举
  `song|playlist|station|stream`。
- **短期/签名媒体 URL 绝不进入**持久状态、公开 response、watch event、日志或 fixture；
  只在播放启动时解析。`core.Item.URL`/`api.Item.URL` 只放稳定公开 URL。
- 错误只用 `docs/client-api/errors.md` 的稳定 code；上游原始信息只可脱敏放入
  `details.providerCode`。

## 代码地图

| 位置 | 作用 |
|---|---|
| `internal/server/` | 唯一 server、Client API handlers、provider registry、state 投影 |
| `internal/audius/`、`internal/radio/`、`internal/builtin/` | 来源实现 |
| `internal/mpvplayer/` | Linux 播放后端（进程内 mpv JSON IPC；无 build tag，hermetic 测试用假 mpv）；契约见 `docs/internals/linux-mpv-engine.md` |
| `internal/appleweb/` | Linux 与 macOS opt-in 的 Apple 浏览器引擎：CDP over pipe 驱动 Apple 自家 web player（macOS Chrome 自带 Widevine；纯 stdlib）；契约见 `docs/internals/apple-web-engine.md` |
| `internal/playrouter/` | 跨平台播放路由：把 streams 后端（Linux mpv / macOS lilt-audio）与 Apple 浏览器合成 server 的 `AudioEngine` + `URLPlaybackDriver`，负责互斥与状态流合并 |
| `internal/state/` | `state.json` schema、迁移与持久化 |
| `internal/client/`、`internal/tui/`、`cmd/lilt/` | client 侧（TUI/CLI/skill 入口） |
| `skills/music-control/` | **对外**发布的 agent skill（音乐/电台播放控制，自包含）；`just agent-install` 安装到 harness 全局 skills |
| `.agents/skills/tui/` | 修改 TUI 时的 agent skill：规范加载顺序、骨架不变量、验证清单 |
| `.agents/skills/usability-test/` | 基于真实构建的 agent 可用性走查 skill：轮次/prompt/隔离装置/汇总格式（运行产物不入库） |

skill 分两类，**同一个文件不存两份**：

- **对外**（用户/外部 agent 加载）：`skills/music-control/`，由 `just agent-install` 安装到 harness 全局
  skills。它是产品制品，与 `player/` 同级看待。
- **对内**（开发/测试本仓库时加载）：`.agents/skills/tui/`、`.agents/skills/usability-test/`。
- `.agents/skills/music-control` 是指向 `skills/music-control` 的**软链**，`.opencode/skills` 是指向
  `.agents/skills` 的软链；因此在仓库里测的就是用户安装的那一份，不存在仓库副本。
| `player/` | Swift helper（`LiltPlayer`）；内部协议见 `docs/internals/helper-rpc.md` |
| `scripts/check-doc-links.py` | 文档链接/锚点检查（`just docs-check`） |

## 测试与本地状态

- 测试用 `LILT_SOCKET` / `LILT_STATE` / `LILT_CONFIG` / `LILT_RADIO_CACHE` 隔离路径，
  `LILT_FAKE_PLAYER=1` 可绕开真实 helper。
- 状态相关改动必须覆盖 v1 → 当前 version 的迁移、幂等重载与 round-trip fixture。
