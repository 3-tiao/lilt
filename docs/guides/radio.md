# 听网络电台

Radio 是统一来源：内置精选台（`origin=builtin`）+ Radio Browser 目录（`origin=directory`），都是直播
单流，不需要凭据，也没有队列。设计、探测语义与缓存规则见
[`../internals/providers/radio-discovery.md`](../internals/providers/radio-discovery.md)。

## 在 TUI 里

Radio 有 **Home / Browse / Recent** 三个 surface，进入默认是 Home；Browse 是唯一的发现面。

- Browse 默认加载每页 100 个 `Popular Worldwide` 电台，先画出上次结果再后台刷新；正在播的台会被高亮。
- `/` 打开 **Search & Filters**：可选名称文本 + Language / Genre / Country 选择器（多条件 AND），
  排序有 Recommended / Popular / Fastest / Name。条件是会话级的，不写入状态。
- `r` 重新加载（目录出错后的重试，目录不可用时回退到缓存电台）。
- `a` 把流地址加进 Favorites 并播放，流带 `icy-name` 时用它的台名。
- 行的可播放性由本机探测给出：`○ 未检查`、`◌ 检查中…`、`● 0.4s`、`× TLS 错误`。探测最多同时两个、
  按 endpoint hash 缓存，**永不阻塞播放**；重试失败的台会清掉缓存失败。流在 10s 内没有开始播放会失败，
  而不是无限缓冲。
- `Fastest` 在行仍被探测时会显示覆盖度（如 `· 14/100 measured`）；`S` 用当前已收集的结果重排。

## 用 CLI

```sh
lilt radio search --tag lofi --json
lilt radio search --origin builtin --json          # 只看内置台
lilt radio search --name jazz --language English --json
lilt radio options --json                          # facet 可选值
lilt radio probe --url <url> --json                # 探测单个 endpoint
lilt radio cache --json                            # server 探测缓存快照
```

具体参数、返回与错误码以 `lilt api --json` 与
[`../client-api/commands.md`](../client-api/commands.md) 为准。电台播放接受 canonical `radio:<url>`
ref 或直接给 stream URL：

```sh
lilt play "https://station.example/stream" --name "My Station"
```

## 播放与元数据

- 直播是单流：没有 `next`/`previous`，队列编辑只对有限队列（Apple Music / Audius / Jamendo）有效。
- 播放严格互斥：开始 Radio 会停止 Apple Music，反之亦然。
- 支持 ICY 的流会把 `StreamTitle` 暴露为 `streamTitle`/`streamArtist`，TUI 的 Now Playing 显示 `LIVE`。
- 个别公开连续流不兼容 macOS `AVPlayer`（Range 请求失败等），会显示具体错误而不是假装断网；
  这是已接受的限制，见 [`../product/limitations.md`](../product/limitations.md)。

## 依赖与边界

Radio Browser 是无可用性保证的社区服务；当前以 `de1` 为主、`de2` 为静态回退（每请求 7s 超时），
全部失败时 Browse 显示目录不可用并记日志，此时仍可用内置精选台。内置台数据源自 cliamp /
cliamp.stream，provenance 由 `internal/builtin` 提供并被测试断言，见
[`../client-api/extending.md`](../client-api/extending.md)。
