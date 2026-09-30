# Spec: TUI 设计参考（references）

本页记录 lilt 终端界面的**设计参考对象**：我们观察它们的布局、密度与交互，把结论转化为
lilt 自己的 [UI 模型](model.md) 与 [UX 规范](ux.md)。参考不等于照搬：不引入新依赖（仍是
Bubbletea/Lipgloss），不照抄与 lilt 产品形态冲突的功能；每个参考都写明**借鉴**与**不借鉴**。

## 参考对象

| 项目 | 形态 | 借鉴 | 明确不借鉴 |
|---|---|---|---|
| [orbit](https://github.com/sihooleebd/orbit)（Rust, ratatui） | 本地音乐库三栏 TUI：曲目列表 / 播放列表（buckets）/ 队列，底部横跨全宽的播放状态区 | **简洁现代的视觉基调**：细边框、低密度状态区、"底部状态只保留可操作事实"的信息取舍；把 queue 放在工作区而非播放区的纵向分区结构 | 本地文件夹浏览模型（lilt 的来源是 catalog/cloud provider）、10-band EQ、zen mode、频谱可视化、离线音频指纹电台（均超出路线，见 [`../product/roadmap.md`](../product/roadmap.md)） |
| [cliamp](https://github.com/bjarneo/cliamp)（Go, Bubbletea/Lipgloss） | Winamp 风格多来源聚合播放器，与 lilt 同技术栈 | Bubbletea/Lipgloss 大规模布局与 overlay 的实现可行性（同栈先例）；聚合多来源时的**状态精简**呈现 | Winamp 复古皮肤美学、Lua 插件系统、客户端直连各家流媒体 API（lilt 的来源必须经 provider 编译期准入，见 [`../internals/providers/providers.md`](../internals/providers/providers.md)） |
| [cmus](https://github.com/cmus/cmus) | 经典 vi 键位终端播放器：左 library / 右 queue，底部 `:command` | vi 式高效导航；`:command` 命令面板形态（lilt 用 `:` overlay）；双栏队列结构的鼻祖 | 本地库与目录树模型、`~/.config` 手工配置心智 |

## 布局结论（对 lilt 的直接影响）

1. **工作区与播放区分离**：lilt 采用主浏览区（左）+ 有限队列 Up Next（右）的两栏工作区；
   Now Playing 横跨全宽、位于工作区下方。orbit 的 queue 不在底部播放区，这一点必须保留。
2. **底部状态要精简**：orbit 只保留"曲目 — 艺人、进度、可操作状态"。lilt 的 Now Playing 遵循
   同一取舍（见 [`design-system.md`](design-system.md)）：进度、时间、实际编码和已启用播放模式
   同行呈现，不重复 Source、queue count 或页面上下文。
3. **密度即现代感**：orbit 的"好看"主要来自低密度 + 细节克制（空行留白、暗色边框、单 accent），
   而非更多组件。lilt 的固定 shell band、紧凑播放带与受限 Header 文法遵循同一原则。
4. **键位不叠加**：cmus 的效率来自几十年的键位沉淀，但 lilt 的操作面（多来源、队列编辑、
   授权）与本地播放器不同；只吸收结构（`:` 面板、数字键聚焦固定面板），不为相似而相似地映射按键。

> 新增参考对象时：先在真实终端里用一遍，确认其**布局决策**（不是功能清单）对 lilt 有可迁移
> 的结论，再补充到本表，并在 [ux.md](ux.md) 落地对应规范。
