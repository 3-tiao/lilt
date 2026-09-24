# Release 流程

lilt 只支持 macOS 14+，播放依赖**已签名**的 `lilt-player.app` 与 `lilt-audio.app`。发布分两个阶段；
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

在开发者自己的 Mac 上完成——证书已在 login keychain 里，`just build` 的自动签名即可用。
不需要把证书搬进 CI，也不需要任何 repository secret。

1. **打 tag**：`git tag vX.Y.Z`

2. **构建制品**：`just release`
   - 产出 `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz` 与其 `.sha256`（内含 `lilt`、
     `lilt-player.app`、`lilt-audio.app` 与 `skills/`——对外发布的 agent skill）。
   - 默认是开发签名。**可选**公证（brew 下载不打 quarantine，不公证也能安装，但建议公证）：
     ```sh
     xcrun notarytool store-credentials lilt-notary \
       --apple-id "<Apple ID>" --team-id 9Y6KG228YM --password "<app-specific password>"

     DEVELOPER_ID_APPLICATION="$(security find-identity -v -p codesigning \
       | awk -F'"' '/Developer ID Application/{print $2; exit}')" \
     NOTARY_PROFILE=lilt-notary just release
     ```
     设置这两个 env 时 `just release` 会重签（hardened runtime）并公证 + staple。

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
      `lilt version`、`lilt sources --json`、`lilt run` 播放一首、Radio 一个台。
- [ ] `LICENSE`（MIT）与制品一致。
- [ ] tarball 含 `skills/music-control/SKILL.md`；formula 安装后 `caveats` 能打印 skill 路径与链接命令。
- [ ] 已知限制在 [`limitations.md`](limitations.md) 中准确，不含未实现承诺。
