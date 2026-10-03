# lilt documentation

[English](README.md) | [简体中文](../README.md)

This is the **English entry point for users**. The [project overview](../../README.md) explains what lilt does. The product roadmap is in English; detailed Client API, implementation, UI design, and test specifications remain in Chinese and are linked below. `lilt api --json` is the offline, machine-readable command/model/error catalog.

| I want to… | Start here |
|---|---|
| Install lilt or play my first song | [Getting started](getting-started/README.md) |
| Use the terminal interface | [TUI guide](guides/tui.md) |
| Write CLI/JSON scripts | [CLI and scripting](guides/cli-and-scripting.md) |
| Control playback from an AI agent | [Agent guide](guides/agent.md) |
| Find and play internet radio | [Radio guide](guides/radio.md) |
| Investigate an error | [Troubleshooting](guides/troubleshooting.md) |
| Understand product boundaries | [Known limitations](product/limitations.md) |

## Authoritative product and technical documentation

| Topic | Source |
|---|---|
| Supported services and platform/engine matrix (English) | [Product roadmap](../product/roadmap.md) |
| Commands, wire models, stable errors, and watch events | [Client API](../client-api/README.md) |
| Architecture and ownership | [Architecture](../architecture.md) |
| Playback engines and persistence | [Implementation contracts](../internals/README.md) |
| TUI behavior and design | [UI specifications](../ui/README.md) |
| Testing layers and provider admission | [Testing](../testing/README.md) |
| Release process and unresolved work | [Product documentation](../product/README.md) |

The public Client API version is **`v0.1`**, regardless of the published product release. API details can change rapidly without backward compatibility; query the installed CLI instead of guessing from an example.
