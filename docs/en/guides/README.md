# Guides by task

[English](README.md) | [简体中文](../../guides/README.md)

These are user workflows, not a second command specification. For exact commands, parameters, response models, and errors, run `lilt api --json` or consult the [Client API (Chinese)](../../client-api/README.md).

| Task | Guide |
|---|---|
| Daily playback in the TUI | [TUI](tui.md) |
| CLI and JSON automation | [CLI and scripting](cli-and-scripting.md) |
| AI-agent integration | [Agent](agent.md) |
| Internet radio | [Radio](radio.md) |
| Errors and logs | [Troubleshooting](troubleshooting.md) |
| Installation and first playback | [Getting started](../getting-started/README.md) |

API operations do not silently switch sources or fall back to another one. A client or agent must make that decision explicitly after checking each source's capabilities.
