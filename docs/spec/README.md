# lilt 跨端规范（Spec）

这些文档是 lilt 的**跨端契约**：定义来源模型、状态数据、播放协议、交互与主题。
各平台用各自语言实现（不共享代码），但必须遵守这里的模型与字段，才能保证
「数据与操作一致」。

## 为什么是文档而不是共享代码

- 桌面是 Go + Bubble Tea；macOS 播放由签名 Swift helper（MusicKit）承担；手机是原生 UI。
- 没有跨语言可复用的代码段，共享的是**行为、schema、协议**。

## 引擎（Engine）

| 引擎 | 平台 | Apple Music 能力 | Radio |
|---|---|---|---|
| `musickit` | macOS / iOS | 原生 MusicKit，队列/资料库/无损/Atmos | AVPlayer |
| `web` | Linux / Windows（v2 不实现，仅预留） | MusicKit JS + Widevine，AAC 256、需网页登录 | 原生或内嵌浏览器 |
| `native-mobile` | iOS / Android（以后） | 平台 MusicKit / 官方 App | 原生播放器 |

> 「无缝」承诺限定为**数据（收藏/预设/最近）与操作**，不含音质。
> Linux 端 Apple Music 只能是 `web` 引擎，质量与稳定性都弱于 Apple 平台。

## 文档

- [`sources.md`](sources.md)：来源（Provider）与浏览树、可播放项与 id 方案。
- [`state.md`](state.md)：本地优先的状态 schema（收藏/最近/预设/主题/上次来源）。
- [`rpc.md`](rpc.md)：helper JSON-RPC 协议与状态形状。
- [`ux.md`](ux.md)：布局、导航、键位、加载/错误/toast/弹层。
- [`theme.md`](theme.md)：主题格式（沿用 cliamp 的 TOML schema）。
- [`limitations.md`](limitations.md)：已接受的已知限制（含 Music User Token 不可用）。

## 范围（v2）

- 做：Apple Music（macOS 原生）+ Radio（AVPlayer）+ 主题 + 跨端数据 schema。
- 不做：本地文件、播客、EQ、歌词、真频谱、Linux 实现、手机 UI。
