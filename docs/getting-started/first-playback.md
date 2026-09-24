# 第一次播放

## 1. 起 server

`lilt tui` 会在没有 server 时自动起一个，有则直接附着；需要 server 的 CLI 命令同理，会在
`no_active_session` 时自动启动并重试一次。也可以显式：

```sh
lilt serve --json          # 前台；--detach 后台并在 listener ready 后返回 {pid}
```

server 是每个隔离 state root 的唯一常驻进程，持有播放、队列与 `state.json`。TUI 退出**不会**停止
播放；要停 server 用 `lilt quit`，只停播放用 `lilt stop`。

## 2. 授权 Apple Music（可选）

- macOS 默认走签名 MusicKit helper：首次请求 Apple Music 播放时，`not_determined` 会触发系统授权
  对话框，无需输入 Apple ID 密码（`just auth` 是显式的首次授权流程）。
- Linux（或 `LILT_APPLE_ENGINE=browser`）走浏览器引擎：

```sh
lilt auth apple-music           # 打开 Apple 自己的登录页；lilt 看不到凭据
lilt auth status apple-music    # 查看授权状态
lilt auth disconnect apple-music
```

浏览器模式中，登录窗口与播放浏览器必须独占同一个 profile，因此开始登录会先停止当前 Apple 播放；
登录后不自动重播。storefront 会跟随登录账号。

## 3. 播第一首

TUI：

```sh
lilt tui
# / 搜索 → Enter 播放选中项；Space 暂停；n/b 下一首/上一首；v 停止；? 帮助；q 退出
```

CLI：

```sh
lilt search "Nujabes" --play            # 搜索并播放
lilt play apple-music:song:1440845629   # canonical ref：source:kind:id
lilt play "https://station.example/stream"  # 也可直接给 stream URL
```

canonical ref 形态是 `source:kind:id`，例如 `apple-music:playlist:pl.u-abc`、`audius:song:<id>`、
`jamendo:song:<id>`、`radio:<normalized-url>`；kind 属于 `song|playlist|station|stream`。

## 4. 确认结果

```sh
lilt status --json              # authorization、mode、当前 track、进度、队列
lilt sources --json             # 每个 source 的 capability 与 available
```

- `mode` 只表达当前播放模式：`full` 是完整播放，`preview` 是试听（时长由上游决定：macOS 原生
  约 30 秒，浏览器引擎实测约 90 秒，且可能缺失）；浏览器模式下还有过渡态 `unverified`
  （登录后、目录时长与媒体时长核对前）。
- 授权状态看 `authorization`，不要用 `mode` 反推授权。
- agent / 脚本只解析 `{"ok":true,"data":…}` 与 `{"ok":false,"error":{"code","message"}}`；
  稳定错误码见 [`../client-api/errors.md`](../client-api/errors.md)。

## 5. 按来源补充配置（可选）

- **Audius**：匿名即可发现与播放；账号关联是可选的。
- **Jamendo**：需要免费 `client_id`，且仅限非商业使用：

```sh
lilt jamendo setup        # 打开 devportal，校验并保存到 Keychain
lilt search "lofi" --source jamendo
lilt trending --source jamendo --type song
```

- **Radio**：默认就能用，无需配置；见 [`../guides/radio.md`](../guides/radio.md)。

下一步建议读 [`../guides/tui.md`](../guides/tui.md) 或
[`../guides/cli-and-scripting.md`](../guides/cli-and-scripting.md)。
