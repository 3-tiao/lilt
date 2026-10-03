# Skill: release

发布一个新 lilt 版本(tag、GitHub Release、Homebrew tap、安装入口验证)。**权威流程是
[`docs/product/release.md`](../../../docs/product/release.md)**;本 skill 是编排层,负责前置检查、
分步执行、门禁与对外动作的批准关口。与 release.md 冲突时以 release.md 为准,并回报矛盾。

## 触发

用户说"发布 vX.Y.Z / cut a release / 更新 brew tap / 走发布流程"等。用户只说"发个新版"
时,先从 git log 归纳变更面、**提议**版本号(patch/minor)并等确认,不自作主张打 tag。

## 铁律

1. **对外动作逐项获批**:`git push`(分支与 tag)、`gh release create`、tap 仓库 push,每一次
   执行前都要用户当次明确同意;上一项的批准不覆盖下一项。
2. **签名凭据只在用户侧**:Developer ID 身份与 `notarytool` profile 来自用户的环境与 keychain;
   不把任何凭据写进命令行参数、日志或仓库。CI/无凭据环境里只做到"构建与门禁",公证停下等用户。
3. **版本真源是 git tag**:运行时版本由 `just build` 经 ldflags 注入 `git describe`;
   `scripts/release-check.sh` 在发布时强制 `cmd/lilt/main.go` 的 `var version` 与 tag 全等。
   文档版本号只出现在 `docs/getting-started/install.md`(中英);`just workflow-check` 里的
   `test_release_refs` 会断言这两处与最新 tag 一致、入口文件不带版本号。
4. **未验证就是未验证**:干净机器安装、真实播放是发布声明的边界项。没做就在 Release notes 与
   install.md 里如实写"未验证",不宣称完成。
5. **发布是可中断的长流程**:每完成一步,在对话里记录已做/未做/下一步(不新建状态文件)。
   中断后按记录续跑,不重做已完成的对外动作。

## 流程

### 0. 前置检查(只读)

- 工作树干净;`origin/main..HEAD` 为空(tag 必须指向已公开源码;有未推送提交先请用户推)。
- 目标 tag 不存在;提议版本号并说明理由(自上一 tag 以来的变更面)。
- `just verify && just provider-gate` 全绿(发布规程点名的两道门禁)。

### 1. 版本落地

- `cmd/lilt/main.go` 的 `var version` → 新版本。
- `docs/getting-started/install.md` 中英:版本号、取代关系、证据边界措辞按事实更新
  (这是**唯一**要写版本号的文档)。
- `docs/product/release.md` 只在流程本身变化时同步;发布历史由该文件自身记录。
- 按文档约定过一遍 docs-maintenance(`review changed`),然后**向用户展示 diff 并确认提交**。

### 2. 打 tag

`git tag vX.Y.Z`(指向当前 HEAD)。按 release.md:**先构建验证产物,后推送 tag**。

### 3. 构建与公证

- 凭据就绪时执行 release.md 阶段二的 `just release`(环境变量由用户提供);产物落在
  `dist/lilt-vX.Y.Z-darwin-arm64.tar.gz` + `.sha256`。
- 留存构建/公证/验证输出的关键行,进交付说明。

### 4. 对外发布(逐项获批)

1. `git push origin main` 与 `git push origin vX.Y.Z`。
2. 起草 Release notes(见下),用户过目后 `gh release create` 上传 tar.gz 与 sha256。
3. 更新 tap `3-tiao/homebrew-lilt`:`packaging/homebrew/Formula/lilt.rb` → tap 仓库,
   更新 URL 与真实 sha256(取自 `.sha256` 文件;**绝不提交占位 SHA**)。

### 5. Nix 与安装入口

- **Nix 无需随版本更新**:`flake.nix` 从源码构建(`self.shortRev`),没有版本 pin/hash 要改;
  main/tag 推送后 flake 自然指向新源码。有 Linux/Nix 环境就跑 `nix build .#`(或 `.#lilt-apple`
  按 docs 验证),没有就如实标注"未在本机验证 Nix 构建"。
- 干净机器验证两个安装入口(brew tap/install.sh,命令见 release.md 阶段二第 5 步),并按
  brew caveats 确认 skill 链接提示。无干净机器则保留"未验证"声明。

### 6. 收尾

- Release notes 与 install.md 的证据边界三问:干净机安装验证了吗?真实播放验证了吗?Nix 构建验证了吗?
- `limitations.md` / `open-questions.md` 是否需要随本版更新;向用户交付完成/未完成总清单。

## Release notes 起草策略

从 `git log <上一tag>..vX.Y.Z --oneline` 归纳,分四节,全部事实性:

- **行为变更**(最重要,点名 Client API 语义变化,agent/skill 用户依赖它);
- **修复**;**新增**;**已知未验证边界**。

不写营销话术;引用的每个行为变更都能对应到提交。

## 反模式

- 跳过 `provider-gate` 或以"CI 会跑"为由跳过本地门禁。
- tag 先推后验,或指向未推送的 commit。
- 在命令行、issue、日志里出现 notarytool 密码或签名身份私料。
- tap 里提交占位 SHA;formula 与 tar.gz 的 sha256 不一致。
- 版本号散写进本 skill 未列出的文档(守卫会红)。
- 没做干净机/真实验证却在文案里暗示"已全面验证"。
- 中断后从第 0 步重跑并重复已推送的对外动作。
