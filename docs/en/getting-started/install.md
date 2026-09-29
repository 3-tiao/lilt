# Installation and platform requirements

[English](install.md) | [简体中文](../../getting-started/install.md)

**`v1.0.0` is being prepared for public use, but the signed/notarized GitHub Release and a Homebrew formula with its final SHA-256 have not been published yet. The Homebrew and shell-install commands below will not work anonymously until publication.** The repository is public; until the release is available, build from source. Playback paths not yet verified on a real account are tracked in [open questions (Chinese)](../../product/open-questions.md).

The authoritative platform/source matrix is in the [product roadmap](../../product/roadmap.md); the release gate is in the [release process (Chinese)](../../product/release.md).

## macOS 14+ on Apple Silicon (after public release)

Homebrew, from the organization's separate tap:

```sh
brew tap 3-tiao/lilt
brew install lilt
lilt version
```

Or the independent shell installer, without Homebrew or shell-configuration edits:

```sh
curl -fsSLo install-lilt.sh https://raw.githubusercontent.com/3-tiao/lilt/main/scripts/install.sh && sh install-lilt.sh
~/.local/bin/lilt version
```

Review `install-lilt.sh` before running it if you prefer. `&&` prevents executing an old local file if the download fails. The script chooses the latest stable GitHub Release (or `LILT_VERSION=1.0.0 sh install-lilt.sh`), downloads the archive and checksum, verifies SHA-256 and the helpers' signature/Gatekeeper status, then installs to `~/.local/share/lilt/vX.Y.Z/` with a `~/.local/bin/lilt` launcher. It refuses to replace a launcher it does not manage and reports if `~/.local/bin` is not on your `PATH`.

The Homebrew package installs the CLI, `lilt-player.app`, `lilt-audio.app`, and the agent skill together. It prints a command to link the skill into an agent harness; for example:

```sh
mkdir -p ~/.agents/skills && ln -sfn "$(brew --prefix lilt)/share/lilt/music-control" ~/.agents/skills/music-control
```

Native Apple Music uses your macOS system account; full playback requires a subscription. Without one, playback may be a provider-defined preview (about 30 seconds for native MusicKit). lilt never asks for your Apple ID password.

## Build from source (macOS or Linux)

Clone this repository first. You need Go (see `go.mod`) and `just`; on macOS the **signed** helpers additionally require Xcode access to Apple Developer Team `9Y6KG228YM` and `xcodegen` (`brew install xcodegen`).

```sh
git clone https://github.com/3-tiao/lilt.git
cd lilt
just build
./lilt version
```

`just build-go` builds only Go. On macOS `just doctor` diagnoses native MusicKit token status without printing tokens. Source builds are not substitutes for a signed/notarized public release.

### Linux

The Swift helpers are macOS-only; the Linux build contains the Go CLI/TUI. With Nix:

```sh
nix develop
nix run .# -- tui
```

Without Nix, supply Go and the external playback dependencies yourself. Radio, Audius, and Jamendo need `mpv` on `PATH` (or `LILT_MPV_PATH`). Apple Music needs a Widevine-capable Chromium (or `LILT_CHROMIUM_PATH`); sign in with `lilt auth apple-music`. On NixOS the separate `NIXPKGS_ALLOW_UNFREE=1 nix develop .#apple` shell provides that browser. Linux's Apple browser engine supports catalog search, recommendations, previews and full playback, but not your library, personal playlists, catalog radio, shuffle, or repeat. Details: [browser engine (Chinese)](../../internals/playback/apple-web-engine.md) and [known limitations](../product/limitations.md).

## Optional credentials

| Source | Requirement |
|---|---|
| Radio | None; built-in stations still work if the community directory is down |
| Audius | Anonymous use needs none; optional account linking requires the deployer's `LILT_AUDIUS_API_KEY` |
| Jamendo | Your own free `client_id`, for non-commercial use only; run `lilt jamendo setup` |
| Apple Music on macOS | System Apple Music account; subscription for full playback |
| Apple Music in a browser | Widevine-capable Chromium and `lilt auth apple-music` |

Storage paths, overrides, and migration rules are in [state persistence (Chinese)](../../internals/persistence/state.md). Use isolated state roots for tests; see [integration testing (Chinese)](../../testing/integration.md). Continue with [first playback](first-playback.md).
