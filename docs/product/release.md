# Release 流程

首个公开版是 `v1.0.0`；当前推荐的 `v1.0.1` 补齐了安装包中的 MIT `LICENSE`，取代前者。
此发布系列尚非 production-ready，仅面向 **macOS 14+ arm64**，
默认 MusicKit 播放依赖经 Developer ID 签名、公证的 `lilt-player.app` 与 `lilt-audio.app`。
此版本也包含 macOS opt-in 的 Apple browser 模式
（`LILT_APPLE_ENGINE=browser`）；用户需自行安装带 Widevine 的 Chromium/Chrome，包不内置浏览器。
Linux 可从源码构建，但不在此 Homebrew 包的发布范围。
发布分两个阶段；**从源码做隔离测试不需要打包或公证**。主仓库、`v1.0.1` Release 与 Homebrew tap
已经公开；本机安装验收不等于另一台干净机器上的播放验收。

## 版本

- 首个公开版用 `1.0.0`；git tag 用 `v` 前缀（`v1.0.0`）。版本号表示可公开安装，
  不宣称所有上游播放失败路径已消除；已知限制与真机缺口分别见
  [`limitations.md`](limitations.md) 与 [`open-questions.md`](open-questions.md)。
- `lilt version` 输出 `lilt <version>`；`lilt version --json` 的 `data.version` 给出版本。
  构建时由 `just build-go` 经 ldflags 注入 `git describe`（无任何可达 tag 时为 commit；
  有历史 tag 时包含 tag、提交距离与 commit）。
- 提升版本：改 `cmd/lilt/main.go` 的 `var version`，再打 tag。运行时版本的唯一真源是 tag（`just build` 经 ldflags 注入 `git describe`），`release-check.sh` 在发布时强制 `main.go` 与 tag 一致。
- 文档中的版本号只写在 [`getting-started/install.md`](../getting-started/install.md)（中英两个镜像），随 tag 一起更新；其余文档一律不带发布版本号，`workflow-check` 的 `test_release_refs` 断言这两处与最新 tag 一致，并断言固定的入口文件清单（根 README 中英、docs/README、client-api/README、roadmap）不出现发布版本号。

## 阶段一：从源码测试（贡献者与预发布）

两位测试者 clone 主仓库后直接构建，不涉及 brew / 公证 / secrets。**前置条件**：

- macOS 14+，Xcode 已登录并被加入 Apple Developer Team `9Y6KG228YM`（否则无法签名 helper）。
- Go（版本见 `go.mod`）与 `xcodegen`：`brew install xcodegen`。
- 各自的 Apple Music 账号（完整播放需订阅；否则走 preview）。

```sh
git clone git@github.com:3-tiao/lilt.git
cd lilt
just promote      # just verify + build，将签名 CLI/helper 固定为预发布构建
./.lilt-prerelease/current/lilt version
just run          # 使用固定构建，不重启正在运行的日常 server
```

Audius 登录需要在 server 环境里有 `LILT_AUDIUS_API_KEY`（见 limitations §9）。

## 阶段二：本地发布

在开发者自己的 arm64 Mac 上完成；Xcode 自动开发签名**不能**作为公开分发签名。
需准备 Developer ID Application 身份与 `notarytool` keychain profile；证书不进入 CI 或仓库。

1. **完成门禁并打 tag**：运行 `just verify && just provider-gate`，由文档测试工程师按
   [docs-maintenance](../../.agents/skills/docs-maintenance/SKILL.md) 审阅本次发布文档，确认无未提交文件，
   然后 `git tag vX.Y.Z`（必须指向当前 HEAD，且 `cmd/lilt/main.go` 的版本与 tag 一致）。
   完成构建与本机产物验证后再推送 tag，确保公开 tag 指向已验收的源码。

2. **构建并验证制品**（签名与公证**必需**）：
   ```sh
   # 首次发布才需运行；密码由 notarytool 安全提示输入，不放进命令行历史。
   xcrun notarytool store-credentials lilt-notary \
     --apple-id "<Apple ID>" --team-id 9Y6KG228YM

   DEVELOPER_ID_APPLICATION="$(security find-identity -v -p codesigning \
     | awk -F'"' '/Developer ID Application/{print $2; exit}')" \
   NOTARY_PROFILE=lilt-notary just release
   ```
   `just release` 会先拒绝非 arm64 主机、缺少签名凭据、非干净 tag 或无签名构建；随后用 Developer ID
   重签两个 helper 并公证，验证三个可执行文件的架构、两个 app 的签名、公证票据与 Gatekeeper 判定，
   最后才生成 `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz` 和 `.sha256`（内含 `lilt`、两个 app、`skills/` 与 `LICENSE`）。
   此处验证的是本机产物；仍需在干净机器上验证最终安装包。

3. **推送 tag 并上传 Release**：
   ```sh
   git push origin vX.Y.Z
   gh release create vX.Y.Z \
     dist/lilt-vX.Y.Z-darwin-arm64.tar.gz \
     dist/lilt-vX.Y.Z-darwin-arm64.tar.gz.sha256 \
     --title vX.Y.Z --generate-notes
   ```

4. **更新 tap**（独立仓库 `3-tiao/homebrew-lilt`）：
   ```sh
   git clone git@github.com:3-tiao/homebrew-lilt.git
   mkdir -p homebrew-lilt/Formula
   cp packaging/homebrew/Formula/lilt.rb homebrew-lilt/Formula/lilt.rb
   # 更新 URL 与 sha256（sha256 见上一步的 .sha256 文件）；不要提交占位 SHA。
   # 首次发布时需显式创建 main 分支；发布 formula 时将 tap 改为 Public。
   cd homebrew-lilt && git branch -M main && git add Formula/lilt.rb && git commit -m "lilt vX.Y.Z" && git push origin main
   ```

5. **从无仓库权限的干净 macOS 14+ arm64 环境验证两种安装入口**：
   ```sh
   brew tap 3-tiao/lilt
   brew install lilt
   lilt version
   lilt api --json
   curl -fsSLo install-lilt.sh https://raw.githubusercontent.com/3-tiao/lilt/vX.Y.Z/scripts/install.sh
   sh install-lilt.sh
   ~/.local/bin/lilt version
   # 让 agent 用上随包发布的 skill（brew 会打印同样的提示）
   mkdir -p ~/.agents/skills && ln -sfn "$(brew --prefix lilt)/share/lilt/music-control" ~/.agents/skills/music-control
   ```

`Formula/lilt.rb` 把二进制与两个 `.app` 装到 `libexec/`，再用 `write_env_script` 生成
`bin/lilt` 包装器注入 `LILT_PLAYER_PATH` 与 `LILT_AUDIO_PATH`，测试者无需设置任何路径；agent skill 装到
`share/lilt/music-control/`，由 `caveats` 打印把它链进 harness skills 目录的命令（formula 不直接
写用户 home）。

### 仓库可见性与匿名安装

主仓库和 tap 均已 **Public**；`v1.0.1` Release 与真实 SHA-256 formula 已可匿名下载。
2026-09-30 在开发机验证了匿名 Release 下载与 SHA、Homebrew 安装（`brew test`、严格 formula audit）、
独立 `sh` 安装到隔离 HOME，以及两种安装的离线 CLI 与 helper 签名、公证票据、Gatekeeper 判定。
**另一台无仓库权限的干净 macOS 14+ arm64 机器与真实播放仍待验收**；不能用同机隔离目录代替。
`sh` 安装器只消费正式版
GitHub Release，先校验 SHA-256，再安装到 `~/.local/share/lilt/`；不会覆盖其他软件的
`~/.local/bin/lilt` 或修改用户的 shell 配置。静音假 Release 回归在 `scripts/test_install.py`。

## CI 自动化（可选，未来）

`packaging/ci/release.yml` 是一个**未启用**的 workflow 模板：推 `v*` tag 时自动
签名公证、出包、建 Release 并更新 tap formula。发版频繁时，把它复制到
`.github/workflows/release.yml` 并配置以下 secrets（主仓库）：

| secret | 用途 |
|---|---|
| `DEVELOPER_ID_P12_BASE64` / `DEVELOPER_ID_P12_PASSWORD` | Developer ID 证书 |
| `NOTARY_APPLE_ID` / `NOTARY_TEAM_ID` / `NOTARY_PASSWORD` | notarytool 凭据 |
| `TAP_GITHUB_TOKEN` | 对 `homebrew-lilt` 有写权限的 PAT |

在启用前，发布一律走上面的本地流程。

## 发布与后续验收清单

`v1.0.1` 已公开、但未宣称 production-ready。发布时完成了签名、公证、匿名下载以及本机两种
安装路径的离线验收；下列干净机器与真实播放项**尚未完成**，不应被公开 Release 或同机隔离 HOME
的成功结果掩盖。下一版发布前应先完成适用的验收；完成前不能声称这些路径通过。

- [x] `just verify` 与 `just provider-gate` 全绿。
- [x] 文档测试工程师已独立复核发布事实、示例、入口与链接（包括 `README*.md`）。
- [x] 英文 `README.md` 与中文 `README.zh-CN.md` 的安装入口已同步（指向最新 Release，不含硬编码版本号）。
- [x] `lilt version` 显示预期版本；`lilt api --json` 可离线运行。
- [x] 两个 helper 均已 Developer ID 签名并公证；Homebrew 安装后再次验证签名、公证票据与 Gatekeeper。
- [ ] 在干净机器上冒烟 `brew install`：
      `lilt version`、`lilt sources --json`、`lilt play <apple-music-song-ref>` 播放一首、
      `lilt play <radio-stream-url>` 播放一个台；browser 模式还须在有 Chrome 的干净机器核对
      `unverified → full|preview`，不能仅用登录态或 fake 时长代替（有声测试须获批）。
- [x] `v1.0.1` tarball 含 MIT `LICENSE`，两种安装均可读到；旧 `v1.0.0` 资产保持不变并标记被取代。
- [x] tarball 含 `skills/music-control/SKILL.md`；formula 安装后 `caveats` 能打印 skill 路径与链接命令。
- [ ] 从无权限干净机器验证 `brew install` **与** `sh scripts/install.sh` 均使用同一 Release，
      安装后 wrapper 都能找到两个签名 helper；真实播放需单独获批，不能用静音假包替代。
- [x] 已知限制在 [`limitations.md`](limitations.md) 中准确，不含未实现承诺。
