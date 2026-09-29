# Getting started

[English](README.md) | [简体中文](../../getting-started/README.md)

This section covers installation, the first playback, and the next task to try. For the implementation model see [architecture (Chinese)](../../architecture.md); for daily tasks use the [English guides](../guides/README.md).

| Goal | Guide |
|---|---|
| Check platform requirements and install | [Installation](install.md) |
| Authorize Apple Music and start playback | [First playback](first-playback.md) |
| Learn TUI keys | [TUI](../guides/tui.md) |
| Automate with JSON | [CLI and scripting](../guides/cli-and-scripting.md) |
| Use an AI agent | [Agent](../guides/agent.md) |
| Listen to radio | [Radio](../guides/radio.md) |
| Diagnose a failure | [Troubleshooting](../guides/troubleshooting.md) |

```sh
lilt tui
lilt search "Nujabes" --play
lilt status --json
lilt quit
```

The TUI starts or attaches to the server. Closing the TUI does **not** stop playback; `lilt quit` shuts down the server, while `lilt stop` only stops playback. Run `lilt help` or `lilt api --json` for the actual installed command catalog.
