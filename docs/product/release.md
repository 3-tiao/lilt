# Release 流程

本文说明如何发布一个 lilt 版本并通过 Homebrew tap 分发。lilt 只支持 macOS 14+；
播放依赖**已签名**的 `lilt-player.app`，因此发布制品的核心是“签名 + 打包 + tap 更新”。

## 版本

- 版本号格式 `0.1.0`、`0.1.1`（不带 `beta`/`v` 前缀作为**代码版本**）。
- git tag 用 `v` 前缀：`v0.1.0`。
- `lilt version` 输出 `lilt <version>`；`lilt version --json` 返回 `{"version":…}`。
  构建时由 `just build-go` 通过 ldflags 注入 `git describe` 的结果（无 tag 时为 commit）。
- 提升版本：改 `cmd/lilt/main.go` 的 `var version = "0.1.0"`（作为无 git 环境的兜底），
  打 tag `vX.Y.Z`，再构建。

## 制品

`just release` 产出：

```text
dist/lilt-vX.Y.Z-darwin-arm64.tar.gz   # 内含 lilt 二进制 + lilt-player.app
dist/lilt-vX.Y.Z-darwin-arm64.tar.gz.sha256
```

要求：

- 主机为 Apple Silicon，且已配置开发者账户。`just build` 用 Xcode 自动签名构建 helper；
  正式分发还必须用 **Developer ID Application** 证书重签并**公证**（见下）。
- `.app` 必须公证，否则测试者首次运行 helper 会被 Gatekeeper 拦截。Homebrew 通过 curl
  下载不会打 quarantine 标记，但公证仍是分发前提。
- 目前只发 `darwin-arm64`；Intel/通用二进制在需要时再加。

### 签名与公证

一次性准备（需要你的 Apple 开发者账户）：

```sh
xcrun notarytool store-credentials lilt-notary \
  --apple-id "<你的 Apple ID>" --team-id 9Y6KG228YM \
  --password "<app-specific password>"
```

在 keychain 里放好 Developer ID Application 证书后，运行 release 时带环境变量：

```sh
DEVELOPER_ID_APPLICATION="Developer ID Application: <Name> (9Y6KG228YM)" \
NOTARY_PROFILE=lilt-notary \
just release
```

当这两个变量都存在时，`just release` 会先调用
`player/scripts/notarize-app.sh`（inside-out 重签 `LiltPlayerLogic.framework` 与 app，
hardened runtime + timestamp，然后 `notarytool submit --wait` + `stapler staple` + `spctl` 验证）。
未设置时脚本只做开发签名，并在日志里警告该制品**不可分发**（仅供本机测试）。

## Homebrew tap（独立仓库）

tap 是**独立仓库** `Older-Youth-HZ/homebrew-lilt`（Homebrew 规定仓库名必须为
`homebrew-<name>`），主仓库只保留模板 `packaging/homebrew/Formula/lilt.rb`。
初始化一次：

```sh
git clone git@github.com:Older-Youth-HZ/homebrew-lilt.git
mkdir -p homebrew-lilt/Formula
cp packaging/homebrew/Formula/lilt.rb homebrew-lilt/Formula/lilt.rb
# 填 version/url/sha256 后提交推送
```

发布流程（手动或由 `.github/workflows/release.yml` 自动完成）：

1. 打 tag 并构建制品：`git tag vX.Y.Z && just release`。
2. 在 GitHub 建 Release `vX.Y.Z`，上传 `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz`。
3. 更新 tap 仓库 `Formula/lilt.rb`：填 `version`、`url`、`sha256`（模板里有占位符）。
4. 提交 tap 仓库。测试者：

   ```sh
   brew tap Older-Youth-HZ/lilt
   brew install lilt
   lilt version
   ```

`Formula/lilt.rb` 把真实二进制与 `.app` 装到 `libexec/`，再用 `write_env_script`
生成 `bin/lilt` 包装器，注入 `LILT_PLAYER_PATH`，因此无需测试者设置任何路径。

### 私有 → 公开两阶段

两个仓库都可以先 **Private**，验证通过后在 GitHub 改成 **Public**（随时可改，无需重建）。

- **Private 阶段（推荐不走 brew）**：GitHub 私有仓库的 Release 资产无法匿名下载，
  而 Homebrew 的 `curl` 默认不带 token。让两位测试者直接 clone 主仓库（他们有权限）：

  ```sh
  git clone git@github.com:Older-Youth-HZ/lilt.git
  cd lilt && just build && ./lilt version
  just run
  ```

  等 `brew tap`（需匿名下载）转 Public 后再用。
- **Public 阶段**：`brew tap Older-Youth-HZ/lilt && brew install lilt` 正常工作。
  注意公开后 git 历史可见——仓库中不得存在密钥（Audius OAuth key 只放环境变量）。

## 自动化发布（GitHub Actions）

`.github/workflows/release.yml` 在推送 `v*` tag 时：构建并（有密钥时）签名公证 helper →
`just release` 产出制品 → 创建/更新 GitHub Release 并上传 → 用 PAT 更新 tap 仓库 formula。
需要的仓库 secrets：

| secret | 用途 |
|---|---|
| `DEVELOPER_ID_P12_BASE64` | Developer ID Application 证书（base64 的 .p12） |
| `DEVELOPER_ID_P12_PASSWORD` | 上述 .p12 密码 |
| `NOTARY_APPLE_ID` / `NOTARY_TEAM_ID` / `NOTARY_PASSWORD` | notarytool 凭据（app-specific password） |
| `TAP_GITHUB_TOKEN` | 对 `homebrew-lilt` 有写权限的 PAT，用于推送 formula |

缺签名密钥时 workflow 仍会出包，但只做开发签名，**不可分发**（日志会警告）。
CI 里 helper 以 `LILT_BUILD_UNSIGNED=1` 构建（无 Apple ID 会话也能编译），再由
`notarize-app.sh` 用导入的 Developer ID 证书签名，因此本地 `just build`（自动签名）
与 CI 路径互不影响。

## 发布前检查清单

- [ ] `just verify` 与 `just provider-gate` 全绿（提交前门禁）。
- [ ] `just docs-check` 通过（文档链接/锚点）。
- [ ] 根 `README.md` 的状态、TUI 导航、安装与已知限制与实现一致。
- [ ] `lilt version` 显示预期版本；`lilt api --json` 可离线运行。
- [ ] `.app` 已 Developer ID 签名并公证；在两台干净机器上 `brew install` 冒烟：
      `lilt version`、`lilt sources --json`、`lilt run` 播放一首、Radio 一个台。
- [ ] `LICENSE`（MIT）与各制品一致。
- [ ] 已知限制在 [`limitations.md`](limitations.md) 中准确，且不含未实现承诺。

## 内部测试（不走 brew）

开发者在自己机器上仍可用仓库构建：

```sh
just run                 # 构建并前台打开 TUI（会重启旧 server）
./lilt auth audius       # 配置了 LILT_AUDIUS_API_KEY 时
```
