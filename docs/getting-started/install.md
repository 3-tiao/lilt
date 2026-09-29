# 安装与平台要求

[English](../en/getting-started/install.md) | 简体中文

目前 `v1.0.0` 作为**公开可用的首版**准备发布，不宣称所有播放场景都已完成真机验收。
**在签名公证的 GitHub Release 和带正确 SHA-256 的 tap formula 发布之前，以下安装命令尚不可用**；
当前私有测试仍从源码构建。安装后核对：`lilt version` 能打印版本，`lilt api --json` 能在没有
server 时运行。未验证的真实播放边界见 [`../product/open-questions.md`](../product/open-questions.md)。

支持矩阵与各来源的实现状态见 [`../product/roadmap.md`](../product/roadmap.md) 与
[`../architecture.md`](../architecture.md)；发布流程本身见 [`../product/release.md`](../product/release.md)。

## macOS arm64（公开 Release 发布后）

```sh
brew tap 3-tiao/lilt
brew install lilt
lilt version
```

或使用独立 `sh` 安装器（无需 Homebrew，不修改 shell 配置）：

```sh
curl -fsSLo install-lilt.sh https://raw.githubusercontent.com/3-tiao/lilt/main/scripts/install.sh && sh install-lilt.sh
~/.local/bin/lilt version
```

建议先查看下载的 `install-lilt.sh` 再执行；`&&` 确保下载失败时不会运行本地旧文件。

安装器从 GitHub Releases 获取最新正式版本及 SHA-256，校验后安装到
`~/.local/share/lilt/vX.Y.Z/`，入口为 `~/.local/bin/lilt`；升级时安装新版本并切换入口。
可用 `LILT_VERSION=1.0.0 sh install-lilt.sh` 固定版本。若已有非本安装器管理的
`~/.local/bin/lilt`，安装器会拒绝覆盖；需要将该目录加入 `PATH` 时会提示，不自动修改配置。
要求 macOS 14+ arm64；Linux 目前仍按下方源码/Nix 路径安装。

Homebrew 包把 CLI、两个签名 helper（`lilt-player.app`、`lilt-audio.app`）与 agent skill 一起装上，
并用 wrapper 注入 helper 路径，测试者无需手动设置。`brew install` 会打印把 skill 链进 harness
skills 目录的命令，例如：

```sh
mkdir -p ~/.agents/skills && ln -sfn "$(brew --prefix lilt)/share/lilt/music-control" ~/.agents/skills/music-control
```

要求：

- macOS 14+。
- 完整 Apple Music 播放需要有效的 Apple Music 订阅；没有订阅时搜索与播放回退为试听
  （时长由上游决定：macOS 原生约 30 秒，浏览器引擎实测约 90 秒），且始终标记为 `preview`，
  不会伪装成完整播放。
- lilt 从不收集 Apple ID 或密码；原生 MusicKit 使用 macOS 上已配置的 Apple Music 账号。

## macOS / Linux（从源码构建）

私有测试与贡献者路径。macOS 上构建**签名** helper 需要 Xcode 与 Apple Developer Team `9Y6KG228YM`；
Go 版本见 `go.mod`，另需 `xcodegen`（`brew install xcodegen`）。

```sh
just build          # Go CLI/TUI + 签名 lilt-player.app 与 lilt-audio.app
./lilt version
```

辅助命令：`just build-go` 只构建 Go；`just player-project` 重新生成 Xcode 工程；
`just doctor` 在不打印 token 内容的前提下诊断原生 MusicKit token。

### Linux

Swift helper 是 macOS-only，Linux 只构建 Go：

```sh
nix develop          # go、just、zsh、sqlite、mpv
nix run .# -- tui    # 直接构建并运行
```

或不上 Nix，自行提供 Go 与外部依赖：

- Radio、Audius、Jamendo 的有限队列播放需要 `mpv` 在 `PATH`（或用 `LILT_MPV_PATH` 指定）。
- Apple Music 在 Linux 走“Apple 自家 web player + Widevine 的浏览器引擎”：需要带 Widevine 的
  Chromium（或用 `LILT_CHROMIUM_PATH` 指定），登录用 `lilt auth apple-music`。
  NixOS 上 `NIXPKGS_ALLOW_UNFREE=1 nix develop .#apple` 会额外提供 Widevine Chromium
  并导出 `LILT_CHROMIUM_PATH`（CDM 是专有组件，故与默认 shell 分开）。
- Linux 上的 Apple Music 提供 catalog 搜索、**推荐**、试听与全曲；**资料库、个人歌单、目录电台与
  shuffle/repeat 不可用**（web player 的 catalog API 不暴露前者，服务端队列不提供后者）。详见
  [`../internals/playback/apple-web-engine.md`](../internals/playback/apple-web-engine.md) 与
  [`../product/limitations.md`](../product/limitations.md)。

## 外部依赖与可选凭据

各来源的完整能力与前置条件见 [`../product/roadmap.md`](../product/roadmap.md) 的「支持的来源（服务）」；
下表只列安装/配置时实际需要处理的项。

| 来源 / 功能 | 需要什么 | 说明 |
|---|---|---|
| Radio（builtin + Radio Browser） | 无 | 目录是社区服务，无可用性保证；不可达时仍可用内置精选台 |
| Audius | 无（匿名可用） | 账号 OAuth 可选，需要部署者配置 `LILT_AUDIUS_API_KEY` |
| Jamendo | 用户自备免费 `client_id` | 仅限非商业用途；`lilt jamendo setup` 保存到系统 Keychain |
| Apple Music（macOS） | 系统 Apple Music 账号 + 订阅（完整播放） | 通过签名 helper 使用系统授权 |
| Apple Music（Linux/browser） | 带 Widevine 的 Chromium | `lilt auth apple-music` 登录 |

## 数据与配置位置

默认路径、覆盖变量（`LILT_STATE`、`LILT_CONFIG`、`LILT_SOCKET`、`LILT_RADIO_CACHE`、
`LILT_ACTIVITY_DB`、`LILT_APPLE_PROFILE`、`LILT_LOG` 等）与迁移规则以
[`../internals/persistence/state.md`](../internals/persistence/state.md) 为权威来源。
`LILT_FAKE_PLAYER=1` 可绕开真实 helper，用于测试。

测试与本地隔离用独立 state root，见 [`../testing/integration.md`](../testing/integration.md)。
