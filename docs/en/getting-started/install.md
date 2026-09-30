# Installation and platform requirements

[English](install.md) | [简体中文](../../getting-started/install.md)

**`v1.0.1` is publicly installable on macOS 14+ arm64, but is not production-ready.** The signed/notarized [Release](https://github.com/3-tiao/lilt/releases/tag/v1.0.1) and public Homebrew tap are available. This release includes the MIT license and supersedes `v1.0.0`, whose archive omitted it. Both installer paths passed offline checks on a developer Mac; installation on a separate clean Mac and real playback have not been verified. Remaining playback gaps are tracked in [open questions (Chinese)](../../product/open-questions.md).

The authoritative platform/source matrix is in the [product roadmap](../../product/roadmap.md); the release gate is in the [release process (Chinese)](../../product/release.md).

## macOS 14+ on Apple Silicon

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

Review `install-lilt.sh` before running it if you prefer. `&&` prevents executing an old local file if the download fails. The script chooses the latest stable GitHub Release (or `LILT_VERSION=1.0.1 sh install-lilt.sh`), downloads the archive and checksum, verifies SHA-256, the bundled license, and the helpers' signature/Gatekeeper status, then installs to `~/.local/share/lilt/vX.Y.Z/` with a `~/.local/bin/lilt` launcher. It refuses to replace a launcher it does not manage and reports if `~/.local/bin` is not on your `PATH`.

The Homebrew package installs the CLI, `lilt-player.app`, `lilt-audio.app`, and the agent skill together. It prints a command to link the skill into an agent harness; for example:

```sh
mkdir -p ~/.agents/skills && ln -sfn "$(brew --prefix lilt)/share/lilt/music-control" ~/.agents/skills/music-control
```

Native Apple Music uses your macOS system account; full playback requires a subscription. Without one, playback may be a provider-defined preview (about 30 seconds for native MusicKit). lilt never asks for your Apple ID password.

## NixOS installation

The normal package includes `mpv` and installs into the current profile:

```sh
nix profile install github:3-tiao/lilt#lilt
lilt version
```

When trying the checked-out repository, use `.#lilt` instead. Remove it with
`nix profile remove lilt`.

Apple Music needs the proprietary Widevine CDM and is therefore an opt-in package. Install `lilt-apple`
**instead of** `lilt` when it is needed: it also includes `mpv`, and both packages provide the same `lilt`
binary:

```sh
nix profile install github:3-tiao/lilt#lilt-apple
lilt auth apple-music
```

`lilt-apple` sets `LILT_CHROMIUM_PATH` itself. It remains subject to the Linux Apple Music [limitations](../product/limitations.md).

If the host cannot reach the default `proxy.golang.org`, an external NixOS flake can specify a reachable
Go module proxy for the package, for example:

```nix
(inputs.lilt.packages.${pkgs.stdenv.hostPlatform.system}.lilt-apple.override {
  goProxy = "https://goproxy.cn,direct";
})
```

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

Without Nix, supply Go and the external playback dependencies yourself. Radio, Audius, and Jamendo need `mpv` on `PATH` (or `LILT_MPV_PATH`). Apple Music needs a Widevine-capable Chromium (or `LILT_CHROMIUM_PATH`); sign in with `lilt auth apple-music`. On NixOS the separate `nix develop .#apple` shell provides that browser. Linux's Apple browser engine supports catalog search, recommendations, previews and full playback, but not your library, personal playlists, catalog radio, shuffle, or repeat. Details: [browser engine (Chinese)](../../internals/playback/apple-web-engine.md) and [known limitations](../product/limitations.md).

## Optional credentials

| Source | Requirement |
|---|---|
| Radio | None; built-in stations still work if the community directory is down |
| Audius | Anonymous use needs none; optional account linking requires the deployer's `LILT_AUDIUS_API_KEY` |
| Jamendo | Your own free `client_id`, for non-commercial use only; run `lilt jamendo setup` |
| Apple Music on macOS | System Apple Music account; subscription for full playback |
| Apple Music in a browser | Widevine-capable Chromium and `lilt auth apple-music` |

Storage paths, overrides, and migration rules are in [state persistence (Chinese)](../../internals/persistence/state.md). Use isolated state roots for tests; see [integration testing (Chinese)](../../testing/integration.md). Continue with [first playback](first-playback.md).
