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

- 主机为 Apple Silicon，且已配置签名（Team `9Y6KG228YM`）。`just release` 先用
  `just build` 构建 Go CLI 与**签名**的 helper app。
- `.app` 必须是用 **Developer ID** 签名并完成 **notarization**，否则测试者首次运行
  helper 会被 Gatekeeper 拦截。Homebrew 通过 curl 下载不会打 quarantine 标记，
  但公证仍是分发的前提。
- 目前只发 `darwin-arm64`；Intel/通用二进制在需要时再加。

## Homebrew tap

tap 仓库约定为 `Older-Youth-HZ/homebrew-tap`，formula 见仓库内模板
`packaging/homebrew/Formula/lilt.rb`。

发布步骤：

1. 打 tag 并构建制品：`git tag vX.Y.Z && just release`。
2. 在 GitHub 建 Release `vX.Y.Z`，上传 `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz`。
3. 更新 tap 仓库 `Formula/lilt.rb`：填 `version`、`url`、`sha256`（模板里有占位符）。
4. 提交 tap 仓库。测试者：

   ```sh
   brew tap Older-Youth-HZ/tap
   brew install lilt
   lilt version
   ```

`Formula/lilt.rb` 把真实二进制与 `.app` 装到 `libexec/`，再用 `write_env_script`
生成 `bin/lilt` 包装器，注入 `LILT_PLAYER_PATH`，因此无需测试者设置任何路径。

## 发布前检查清单

- [ ] `just verify` 与 `just provider-gate` 全绿（提交前门禁）。
- [ ] `just docs-check` 通过（文档链接/锚点）。
- [ ] 根 `README.md` 的状态、TUI 导航、安装与已知限制与实现一致。
- [ ] `lilt version` 显示预期版本；`lilt api --json` 可离线运行。
- [ ] `.app` 已 Developer ID 签名并公证；在两台干净机器上 `brew install` 冒烟：
      `lilt version`、`lilt sources --json`、`lilt run` 播放一首、Radio 一个台。
- [ ] 仓库含 `LICENSE`（当前缺失；公开发布前必须补）。
- [ ] 已知限制在 [`limitations.md`](limitations.md) 中准确，且不含未实现承诺。

## 内部测试（不走 brew）

开发者在自己机器上仍可用仓库构建：

```sh
just run                 # 构建并前台打开 TUI（会重启旧 server）
./lilt auth audius       # 配置了 LILT_AUDIUS_API_KEY 时
```
