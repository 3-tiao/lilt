# 快速上手

这一层面向**使用者**：把 lilt 装上、完成第一次播放、知道日常从哪里进。想了解为什么这样设计、
系统由哪些部分组成，去 [`../architecture.md`](../architecture.md)；想按任务查操作，去
[`../guides/README.md`](../guides/README.md)。

| 我想 | 去哪 |
|---|---|
| 安装并确认能跑起来 | [`install.md`](install.md) |
| 授权 Apple Music、播第一首歌 | [`first-playback.md`](first-playback.md) |
| 日常用 TUI 操作 | [`../guides/tui.md`](../guides/tui.md) |
| 用 CLI / JSON 写脚本 | [`../guides/cli-and-scripting.md`](../guides/cli-and-scripting.md) |
| 让 AI agent 用自然语言控制 | [`../guides/agent.md`](../guides/agent.md) |
| 听网络电台 | [`../guides/radio.md`](../guides/radio.md) |
| 出问题了 | [`../guides/troubleshooting.md`](../guides/troubleshooting.md) |

## 一分钟版本

```sh
lilt tui                       # 全屏手工界面（首次会自动起 server）
lilt search "Nujabes" --play   # 打开 TUI，搜索并播放
lilt status --json             # 看当前播放状态
lilt play apple-music:song:1440845629   # 播一个 canonical ref
lilt quit                      # 停止 server
```

TUI 退出不停止播放；server 才是播放与状态的唯一持有者。人类可读用法是 `lilt help`，机器可读的
权威命令目录（参数、返回模型、稳定错误码，无需 server）是 `lilt api --json`。
