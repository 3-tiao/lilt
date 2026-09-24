# Release 流程

本流程的公开 beta 包仅面向 **macOS 14+ arm64**，默认 MusicKit 播放依赖经 Developer ID 签名、
公证的 `lilt-player.app` 与 `lilt-audio.app`。此 beta 也包含 macOS opt-in 的 Apple browser 模式
（`LILT_APPLE_ENGINE=browser`）；用户需自行安装带 Widevine 的 Chromium/Chrome，包不内置浏览器。
Linux 可从源码构建，但不在此 Homebrew 包的发布范围。
发布分两个阶段；
**私有测试阶段不需要任何打包、公证或 CI**。

## 版本

- 代码版本 `0.1.0`、`0.1.1`……；git tag 用 `v` 前缀（`v0.1.0`）。
- `lilt version` 输出 `lilt <version>`；`lilt version --json` 返回 `{"version":…}`。
  构建时由 `just build-go` 经 ldflags 注入 `git describe`（无 tag 时为 commit）。
- 提升版本：改 `cmd/lilt/main.go` 的 `var version`，再打 tag。

## 阶段一：私有测试（当前）

两位测试者 clone 主仓库后直接构建，不涉及 brew / 公证 / secrets。**前置条件**：

- macOS 14+，Xcode 已登录并被加入 Apple Developer Team `9Y6KG228YM`（否则无法签名 helper）。
- Go（版本见 `go.mod`）与 `xcodegen`：`brew install xcodegen`。
- 各自的 Apple Music 账号（完整播放需订阅；否则走 preview）。

```sh
git clone git@github.com:Older-Youth-HZ/lilt.git
cd lilt
just promote      # just verify + build，将签名 CLI/helper 固定为预发布构建
./.lilt-prerelease/current/lilt version
just run          # 使用固定构建，不重启正在运行的日常 server
```

Audius 登录需要在 server 环境里有 `LILT_AUDIUS_API_KEY`（见 limitations §9）。

## 阶段二：本地发布（公开 beta）

在开发者自己的 arm64 Mac 上完成；Xcode 自动开发签名**不能**作为公开分发签名。
需准备 Developer ID Application 身份与 `notarytool` keychain profile；证书不进入 CI 或仓库。

1. **完成门禁并打 tag**：运行 `just verify && just provider-gate && just docs-check`，确认无未提交文件，
   然后 `git tag vX.Y.Z`（必须指向当前 HEAD，且 `cmd/lilt/main.go` 的版本与 tag 一致）。

2. **构建并验证制品**（签名与公证**必需**）：
   ```sh
   xcrun notarytool store-credentials lilt-notary \
     --apple-id "<Apple ID>" --team-id 9Y6KG228YM --password "<app-specific password>"

   DEVELOPER_ID_APPLICATION="$(security find-identity -v -p codesigning \
     | awk -F'"' '/Developer ID Application/{print $2; exit}')" \
   NOTARY_PROFILE=lilt-notary just release
   ```
   `just release` 会先拒绝非 arm64 主机、缺少签名凭据、非干净 tag 或无签名构建；随后用 Developer ID
   重签两个 helper 并公证，验证三个可执行文件的架构、两个 app 的签名、公证票据与 Gatekeeper 判定，
   最后才生成 `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz` 和 `.sha256`（内含 `lilt`、两个 app 与 `skills/`）。
   此处验证的是本机产物；仍需在干净机器上验证最终安装包。

3. **上传 Release**：
   ```sh
   gh release create vX.Y.Z \
     dist/lilt-vX.Y.Z-darwin-arm64.tar.gz \
     dist/lilt-vX.Y.Z-darwin-arm64.tar.gz.sha256 \
     --title vX.Y.Z --generate-notes
   ```

4. **更新 tap**（独立仓库 `Older-Youth-HZ/homebrew-lilt`）：
   ```sh
   git clone git@github.com:Older-Youth-HZ/homebrew-lilt.git
   cp packaging/homebrew/Formula/lilt.rb homebrew-lilt/Formula/lilt.rb
   # 填 version 与 sha256（sha256 见上一步的 .sha256 文件）
   cd homebrew-lilt && git add -A && git commit -m "lilt vX.Y.Z" && git push
   ```

5. 测试者：
   ```sh
   brew tap Older-Youth-HZ/lilt
   brew install lilt
   lilt version
   # 让 agent 用上随包发布的 skill（brew 会打印同样的提示）
   mkdir -p ~/.agents/skills && ln -sfn "$(brew --prefix lilt)/share/lilt/music-control" ~/.agents/skills/music-control
   ```

`Formula/lilt.rb` 把二进制与两个 `.app` 装到 `libexec/`，再用 `write_env_script` 生成
`bin/lilt` 包装器注入 `LILT_PLAYER_PATH`，测试者无需设置任何路径；agent skill 装到
`share/lilt/music-control/`，由 `caveats` 打印把它链进 harness skills 目录的命令（formula 不直接
写用户 home）。

### 私有 → 公开

两个仓库都可以先 **Private**，验证后随时改 **Public**，无需重建。注意：
**私有阶段不要用 brew**——GitHub 私有仓库的 Release 资产不能匿名下载，而 Homebrew 的
`curl` 默认不带 token；此时用上面的「阶段一」源码构建即可。转 Public 后 brew 正常。

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

## 发布前检查清单

- [ ] `just verify` 与 `just provider-gate` 全绿。
- [ ] `just docs-check` 通过。
- [ ] 根 `README.md` 与实际实现一致。
- [ ] `lilt version` 显示预期版本；`lilt api --json` 可离线运行。
- [ ] 两个 helper 均已 Developer ID 签名（建议公证）；在干净机器上冒烟 `brew install`：
      `lilt version`、`lilt sources --json`、`lilt play <apple-music-song-ref>` 播放一首、
      `lilt play <radio-stream-url>` 播放一个台；browser 模式还须在有 Chrome 的干净机器核对
      `unverified → full|preview`，不能仅用登录态或 fake 时长代替（有声测试须获批）。
- [ ] `LICENSE`（MIT）与制品一致。
- [ ] tarball 含 `skills/music-control/SKILL.md`；formula 安装后 `caveats` 能打印 skill 路径与链接命令。
- [ ] 已知限制在 [`limitations.md`](limitations.md) 中准确，不含未实现承诺。
