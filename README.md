# lilt

English | [简体中文](README.zh-CN.md)

Play **Apple Music, Audius, Jamendo, and internet radio** from your terminal on macOS or Linux. Use the full-screen TUI, script the CLI's JSON output, or let an AI agent control playback through the same CLI.

```sh
lilt play apple-music:song:1440845629   # Play a catalog song
lilt search "Nujabes" --play            # Search and play in the TUI
lilt radio search --tag lofi            # Find internet radio
lilt status --json                      # Inspect playback from a script
```

## Why lilt?

Apple Music has no official CLI or Linux client. lilt brings it into the terminal alongside other online sources, without scanning local files. It treats scripting and AI-agent control as first-class use cases rather than automating a graphical app. It does **not** promise identical capabilities for every source or platform: inspect `lilt sources --json` before relying on an operation.

## Use it

### Full-screen TUI

```sh
lilt tui
```

Press `s` to switch sources, `/` to search, `Enter` to play, `e` to queue next, `0` to edit Up Next, `f` to save a local favorite, `?` for help, and `q` to leave. **Leaving the TUI does not stop playback**: the server owns the active session. See the [TUI guide](docs/en/guides/tui.md) for context-dependent key behavior.

### CLI and scripts

```sh
lilt search "Nujabes" --type song --json
lilt play apple-music:song:1440845629 --json
lilt queue add audius:song:123 --next --json
lilt pause --json && lilt next --json
```

Scripts should branch on `ok` and `error.code`, not human-readable messages. `lilt search --play` opens the interactive TUI; use `search --json` followed by `play <ref> --json` for headless automation. Run `lilt help` for human-readable commands or `lilt api --json` for the authoritative machine-readable catalog, even when no server is running. See the [CLI guide](docs/en/guides/cli-and-scripting.md).

### AI agents

The bundled [music-control skill](skills/music-control/SKILL.md) lets an agent respond to requests such as “play something for coding,” “play jazz radio,” or “pause.” The agent uses `lilt sources --json` to check capabilities, issues CLI commands, and confirms the result with `lilt status --json`. See the [agent guide](docs/en/guides/agent.md).

### Common tasks

| Task | TUI | CLI |
|---|---|---|
| Search and play | `/`, then `Enter` | `lilt search "<term>" --play` (opens TUI) |
| Play a reference | `Enter` or `p` | `lilt play <source:kind:id>` |
| Find radio | `s` → Radio → Browse | `lilt radio search --tag lofi` |
| Edit the queue | `0` | `lilt queue`; `lilt queue add <ref> --next` |
| Save a local favorite | `f` | `lilt favorite add <ref>` |
| Pause / next / stop | `Space` / `n` / `v` | `lilt pause` / `lilt next` / `lilt stop` |
| Inspect state | Now Playing bar | `lilt status --queue --json` |

## Sources and platforms

- **macOS:** native Apple Music playback through a signed MusicKit helper; Audius, Jamendo, and radio use the other signed audio helper. Apple Music's browser engine is opt-in.
- **Linux:** Apple Music uses Apple's web player in a Widevine-capable Chromium; Audius, Jamendo, and radio use `mpv`. Browser-based Apple Music cannot access your library or personal playlists, catalog radio, shuffle, or repeat.
- **Across sources:** capabilities determine which actions are available. Unsupported operations report an error rather than silently changing source. Jamendo requires your own `client_id` for non-commercial use.

For the authoritative source/platform matrix, see the [product roadmap](docs/product/roadmap.md) and check your session with `lilt sources --json`.

## Install and first playback

**`v1.0.0` is being prepared for public installation, but there is no signed and notarized public GitHub Release or finalized Homebrew formula yet. The commands below are for source builds until publication.** The [installation guide](docs/en/getting-started/install.md) documents the future Homebrew and shell installer paths and their requirements.

```sh
git clone https://github.com/3-tiao/lilt.git
cd lilt
# On macOS, building the helpers needs Xcode signing access.
just build
./lilt version
```

Continue with [first playback](docs/en/getting-started/first-playback.md), then use the [documentation home](docs/en/README.md) to find guides. Linux/Nix requirements and optional accounts are in the [installation guide](docs/en/getting-started/install.md).

## Privacy and limitations

lilt is a local client, not a cloud service: it has no telemetry. Preferences, favorites, and playback history stay on your machine. Source-specific network requests occur when you use those services. On macOS, the native Apple Music helper uses system authorization rather than asking lilt for your Apple ID password. Review the [bundled agent skill](skills/music-control/SKILL.md) before installing it into an agent environment.

Some important limits: previews depend on the provider and your subscription; favorites are **local to lilt**, not written to Apple Music; Apple cloud recommendations and cloud recent-playback APIs fail in the tested native setup, while `lilt recent` shows lilt's local history. There is no seek, volume control, live bitrate, lyrics, podcasts, or local-file library. See [known limitations](docs/en/product/limitations.md) and the [unresolved engineering questions (Chinese)](docs/product/open-questions.md). An open question is not a verified fix.

## Documentation and development

Start with the [English documentation home](docs/en/README.md). The detailed [Client API](docs/client-api/README.md), [implementation](docs/internals/README.md), and [test contracts](docs/testing/README.md) are currently maintained in Chinese; the wire models, commands, and errors are discoverable offline with `lilt api --json`.

The server, CLI, TUI, and source adapters are written in Go. On macOS, two signed Swift helpers handle MusicKit and stream playback. `lilt serve` is the sole server per isolated state root; clients communicate through Client API `v0.1` over a Unix socket. Product `v1.0.0` and Client API `v0.1` are different version numbers.

```sh
just build
just test
just verify
```

## License

MIT. See [LICENSE](LICENSE).
