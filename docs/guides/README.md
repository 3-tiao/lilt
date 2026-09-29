# 任务指南

[English](../en/guides/README.md) | 简体中文

这一层按“我要做什么”组织，聚焦操作流程；命令与数据结构以
[`../client-api/`](../client-api/README.md) 为准，实现原理以 [`../internals/`](../internals/README.md)
为准。本层不重复会漂移的接口细节。

| 我想 | 去哪 |
|---|---|
| 用 TUI 做日常播放与控制 | [`tui.md`](tui.md) |
| 用 CLI / JSON 写脚本或接自动化 | [`cli-and-scripting.md`](cli-and-scripting.md) |
| 让 AI agent 用自然语言控制 | [`agent.md`](agent.md) |
| 听网络电台、筛选电台 | [`radio.md`](radio.md) |
| 排查故障、看日志、理解错误码 | [`troubleshooting.md`](troubleshooting.md) |
| 第一次安装与播放 | [`../getting-started/README.md`](../getting-started/README.md) |

跨来源的共同规则见 [`../client-api/README.md`](../client-api/README.md) 的“来源选择规则”与所有权摘要：
API 原语不做隐式跨来源 fallback，换源是调用方的显式决定。
