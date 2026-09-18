# lilt

A macOS Apple Music and internet-radio terminal controller. It is usable by hand
through the TUI, and programmable through the CLI and an AI agent skill.

> **Implementation status.** `lilt serve` is the single headless server: it owns
> the signed `lilt-player` helper, playback, the queue, and `state.json`. The TUI,
> the CLI, and the agent skill are equal clients over the Client API v0.1 Unix
> socket. Three sources are implemented: Apple Music (signed MusicKit helper),
> Audius (official REST discovery, finite URL-queue playback, optional account
> OAuth), and a unified Radio source (builtin + Radio Browser, AVPlayer streams).
> The TUI uses a source-independent Home model (`s` switches source, `:` opens a
> command palette, `1`-`9` select surfaces). Linux playback is not implemented.
> The contract is specified in [`docs/`](docs/README.md); see
> [`docs/architecture.md`](docs/architecture.md) for the architecture and
> [`docs/client-api/README.md`](docs/client-api/README.md) for the interface
> contract. macOS 14+ only for now.

## Documentation

Start at [`docs/README.md`](docs/README.md). Highlights:

- [`docs/architecture.md`](docs/architecture.md) — components, ownership, data flow.
- [`docs/client-api/README.md`](docs/client-api/README.md) — the Client API v0.1 contract.
- [`docs/integrations/agent-skill.md`](docs/integrations/agent-skill.md) — AI agent integration.
- [`docs/product/roadmap.md`](docs/product/roadmap.md) — product scope and platform plan.

## Install

Testers use the Homebrew tap once published:

```sh
brew tap Older-Youth-HZ/lilt
brew install lilt
lilt version
```

Contributors build from source (macOS 14+, Xcode with the Apple Developer team):

```sh
just build          # Go CLI/TUI + signed lilt-player.app
./lilt version
```

`just build-player` uses Xcode automatic signing for Team `9Y6KG228YM` and App
ID `com.caiguo.lilt-player`; Apple Music login is never performed by Fastlane or
by lilt (native MusicKit uses the Apple Music account already configured in
macOS). Testers need macOS 14+, an Apple Music subscription for **full** playback
(otherwise search/playback fall back to ~30s previews), and no Apple ID/password
is ever entered into lilt. Audius works anonymously; account linking is optional.

## Quick start

The repository includes a `Justfile` so normal development does not require
setting helper paths manually:

```sh
just auth                 # first-time Apple Music authorization
just run                  # build and open the TUI (restarts the server)
just tui                  # attach to the running server (keeps current playback)
just restart              # stop the server/helper after changing server code
just search "Nujabes"     # search and play full audio or a preview
just find "Nujabes"       # one-shot catalog search as JSON
just recent               # recently played songs as JSON
just library              # list personal Apple Music playlists as JSON
just play apple-music:song:1440845629 # play a ref in the running server
just doctor               # diagnose MusicKit token validity (helper app)
just fake                 # UI development without Apple services
just test                 # unit tests and static checks
just provider-gate        # provider admission gate: Go race tests and vet
just verify               # credential-free Go/Swift tests, race, vet, build
just verify-app           # verify plus signed Xcode app build
```

Audius login (once `LILT_AUDIUS_API_KEY` is configured for the server):

```sh
./lilt auth audius        # opens the browser; completes automatically
./lilt auth status audius # authorized + account label
./lilt auth disconnect audius
```

## Go development

```sh
go test ./...
go vet ./...
go build ./cmd/lilt
LILT_FAKE_PLAYER=1 ./lilt tui
# In another terminal while the session is running:
./lilt status --json
./lilt pause --json
```

Before adding a content source (provider), read
[`docs/testing/provider-admission.md`](docs/testing/provider-admission.md) and run
`just provider-gate`. Providers are registered in source; there is no runtime
plugin mechanism.

The server socket path follows the platform/XDG rules in
[`docs/internals/state.md`](docs/internals/state.md#路径), is mode `0600`, and
may be explicitly overridden with `LILT_SOCKET` for tests.
`status`, `pause`, `toggle`, `resume`, `next`, `previous`, and `play` are
secondary commands. `play` accepts a canonical ref or Apple Music URL (for
example `apple-music:song:1440845629`, `apple-music:playlist:pl.u-abc`, or a stream URL) and
starts it in the running server. Commands that need a server auto-start
`lilt serve` once when none is running, then retry; `lilt tui` starts one when
absent and attaches otherwise. `lilt stop` stops playback only; `lilt quit`
shuts the server down. The current implementation has one local macOS target;
`--target` and remote target selection remain future work and are not accepted
CLI options.

`lilt tui` opens a fullscreen UI. A breadcrumb shows the current source and
surface (`SOURCE: Audius · HOME`); `1`-`9` select a surface, `s` opens the source
switcher, and `:` opens a command palette. There is a single list cursor.

- Every source has `Home` and `Recent`. Home is an aggregated, dynamic page:
  Continue Playing, Recently Played, Trending (Audius, `search.trending`), Your
  Playlists (Apple, `library`), Favorites, then a `Go to` block (Search, Browse
  or Discover, Recent, Queue, Account). Each preview is capped at five and nothing
  is collapsible.
- Apple Music adds no extra surface; Favorites and playlists are Home sections.
  Collections you mark with `f` are lilt-local, separate from Apple Music's own
  "Favorite Songs" smart playlist.
- Audius adds `Discover` (trending songs/playlists) beside `Home` and `Recent`.
- Radio adds `Browse` beside `Home` and `Recent`. Browse is the single discovery
  surface: it highlights the playing station, paints the last result immediately
  (refreshed in the background), loads 100 Popular Worldwide stations per page,
  and `/` opens Search & Filters (optional name text plus guided Language / Genre
  / Country selectors, ANDed, with Recommended / Popular / Fastest / Name
  sorting). Because sorting runs while rows are still being probed, `Fastest`
  shows coverage (`· 14/100 measured`) and `S` re-sorts with the results collected
  so far. `r` reloads (the retry after a directory error, falling back to cached
  stations when the directory is down). `a` adds a stream URL to Favorites and
  plays it, using the ICY name when the stream announces one. Rows show a local
  playability probe (`○ unchecked`, `◌ checking…`, `● 0.4s`, `× TLS error`) run
  two at a time, cached by endpoint hash, and never blocking playback; retrying a
  failed station clears the cached failure. A stream that never starts fails after
  10s instead of buffering forever.

The `Up Next` queue is editable: `0` opens it, `Enter`/`p` jump to a track, `x`
removes the selected queue item, `J`/`K` reorder, and `c` clears. Apple Music playlists are read-only:
macOS does not allow creating or editing library playlists, and lilt keeps no
separate playlists.

Search is global, not a tab: `/` opens a query from anywhere. Results appear as a
temporary list (`Esc`/`Backspace` returns); Apple Music results are grouped into
`Songs` / `Playlists`, while Radio `/` opens station search and discovery filters.

`Now Playing` is a read-only band with the current track, progress (or `LIVE`
for streams), format, shuffle/repeat flags, and the live Apple Music queue. It
redraws at 250 ms, interpolating finite-track progress between non-overlapping
helper state snapshots. Playback is strictly exclusive: starting Radio stops
Apple Music and vice versa. The helper also publishes Now Playing metadata to
macOS media controls (Control Center, lock screen, and media keys); play/pause/
stop work everywhere, and next/previous are honored for Apple Music playback.

Keys: `1`-`9` select a surface, `[`/`]` cycle surfaces, `s` opens the source
switcher (atomic: stops current playback and clears session caches on commit;
Enter applies, Esc cancels), `:` opens the command palette (`Tab`/`↑↓` move the
highlight, `Enter` runs, `Esc` cancels), `0` opens the Now Playing queue,
`[`/`]` cycle, `j`/`k`/`g`/`G` and `Ctrl+d`/`u`/`f`/`b` navigate. In a list,
`Enter` on a song plays it and the rest of its section (play from here); on a
playlist it opens the track detail, where `Enter` plays the whole playlist from
the selected track, `p` plays all in order, and `S` shuffles. Elsewhere `p`
plays/toggles the selected item. `x` is inert outside focused Up Next. `Space`/`c`
pause, `n`/`b` next/previous (finite queues), `v` stop, `S` shuffle, `R` repeat,
`e`/`E` queue next/append, `f` favorite, `a` add a stream URL (Radio), `/` search
(or Radio Search & Filters), `F` local filter in Apple lists, `t` theme picker,
`i` info, `?` help, `Esc`/`Backspace` back, `q` quit. The top row is a breadcrumb,
not a tab; clicking it opens the source switcher.

The footer is context-sensitive: root views show surface navigation, while playlist and queue
detail pages show their primary actions. `?` always shows the complete key reference. When a visible
control accepts text, every printable key (including `j`/`k`/`h`/`l`, `q`, `?`, and `/`) is text;
use arrow keys to move choices. Elsewhere lists use `j`/`k` vertically, `h` leaves a context where
available, and `h`/`l` select visible horizontal actions.

Queueing follows cmus (`e` play next, `E` append). Transient messages
auto-dismiss; `?`, `i`, and `t` open centered overlays. Themes use the cliamp
TOML schema in `~/.config/lilt/themes/`.

The format line reports what public MusicKit exposes: the current audio variant
(for example `AAC 256 kbps`, `ALAC Lossless`, `Dolby Atmos`) and the track's
available variants. MusicKit does not expose a live bitrate, and it may report
no current variant on some setups, in which case `Format:` shows `Auto` while
`Available:` still lists the track's encodings.

`lilt search TERM --play` starts the UI, searches, selects the first song, and
begins the available mode.
One-shot content commands print stable JSON without starting a TUI:
`lilt search TERM --json` (catalog songs), `lilt recent [limit] --json`
(recently played), and `lilt library [--source S] --json` (personal playlists).

The UI and `status --json` always expose `authorization` and `mode`:

- `authorized` uses MusicKit `ApplicationMusicPlayer` for full playback.
- Any other authorization state uses an AVFoundation `AVPlayer` preview only
  when that search result supplies a catalog preview asset. Previews are
  typically about 30 seconds, may be absent, and are never presented as full
  playback. In preview mode next/previous return `preview_unsupported`.

## Logging

lilt records every operation as JSON lines to
`~/.local/state/lilt/log/lilt.jsonl` (override with `LILT_LOG`). Each entry has
`ts` and `kind`:

- `cli` / `cli.exit` — command kind/count, cwd, and exit code.
- `tui.start` / `tui.run` / `tui.quit` — session lifecycle.
- `key` — every TUI key with source/view and non-content selection metadata.
- `navigate` / `play` / `control` / `favorite` / `submit` / `theme` — semantic actions.
- `rpc` — helper method, duration, and ok/error.
- `helper` — helper stderr lines.

Print the last N entries with `lilt log [n]` (default 50). The file rotates at
5 MB. This is the intended way to share a session when reporting a problem.
Raw searches/submissions and metadata titles are not logged. URL logs remove
userinfo, query strings, and fragments, retaining only a safe host/path.

## lilt-player (macOS 14+)

The Swift package supports fast compiler checks; the Xcode project produces the
signed app bundle. It uses public MusicKit's independent
`ApplicationMusicPlayer`, not Music.app automation. An Apple Developer account
with the MusicKit capability enabled for the chosen App ID is required.

When MusicKit authorization is unavailable, the helper uses Apple's public
iTunes Search API and AVFoundation to play an official preview where available.
This fallback needs no developer token and is clearly reported as `preview`,
not full Apple Music playback.

If the native MusicKit service temporarily rejects its system-issued token
(for example while a newly enabled App ID service propagates), search and play
also fall back to preview instead of failing the TUI. An App Store Connect app
record is not required for local native MusicKit development.

1. In Certificates, Identifiers & Profiles, create/configure the explicit App
   ID `com.caiguo.lilt-player` and enable MusicKit.
2. Add Team `9Y6KG228YM` to Xcode under Settings > Accounts.
3. Build with automatic signing and provisioning:

```sh
just build-player
```

`build-player` first runs `xcodegen generate`, then the script builds
`LiltPlayer.xcodeproj` with `-allowProvisioningUpdates`, so
Xcode creates or downloads signing assets as needed. MusicKit is enabled as an
App ID service; macOS does not use the `com.apple.developer.music-kit`
entitlement. Override
`DEVELOPMENT_TEAM` and `PRODUCT_BUNDLE_IDENTIFIER` only for another account.
For local development, point the Go host at the resulting **app bundle** (not
its `Contents/MacOS` executable):

```sh
LILT_PLAYER_PATH="$PWD/Build/Products/Release/lilt-player.app" ../lilt tui
```

Authorize the app once through LaunchServices so macOS can present the system
permission UI:

```sh
open -n -W Build/Products/Release/lilt-player.app --args --authorize
```

`just run` and `just search` launch a fresh signed app instance through
LaunchServices for the entire foreground session. They first read the stored
authorization status. If it is `not_determined`, lilt explains that macOS is
about to show the system dialog and requests access from that same app
identity. Denied or restricted access does not exit the TUI; preview search
remains usable. `just auth` remains an optional explicit first-time flow.
MusicKit and macOS manage authorization for the user's system Apple Music
account; lilt neither collects credentials nor reads, stores, or refreshes a
Music User Token. `audioVariant` is deliberately nullable: SDK availability
differs and the helper does not claim a precise bitrate or force quality.

On startup, lilt also checks `MusicSubscription.current`. The TUI reports when
the system Apple Music account is unavailable, an active subscription is
missing, or Sync Library is disabled. These checks do not request credentials;
account sign-in and Sync Library remain managed by Music.app and macOS.
Authorization/account guidance is refreshed from helper state snapshots.

Catalog playlists returned by search or URL resolution and personal-library
playlists share the same playback path. Playlist starts prefer a stable track
ID, then an explicit index, with title matching retained only for compatibility;
the complete supported-song queue is preserved before and after the selected
track. Music-video and unavailable/non-song playlist entries cannot enter an
`ApplicationMusicPlayer` song queue, so they are omitted consistently from both
browsing and playback. A temporary UI filter maps its cursor back to the full
displayed ordering before sending `startAt`. If the helper transport closes,
`lilt serve` automatically rebuilds the helper with bounded backoff (publishing
`server.warning` then `engine.restarted`), the current command that timed out
reports `operation_outcome_unknown` and is never replayed, and playback returns
to `stopped`. The TUI shows the reconnecting state; quit and restart only if the
rebuild keeps failing. AVPlayer item failures are shown as actionable playback
errors rather than as a silent pause.

All Apple/iTunes and Radio Browser text crosses a terminal-sanitization boundary
that removes ESC and all C0/C1 controls, replacing tabs/newlines with one space
to keep row widths deterministic while preserving normal Unicode. Network/helper
operations have bounded deadlines. Because MusicKit calls are serial and not
reliably cancellable, a timed-out RPC permanently closes that transport and
terminates its private helper; the server then rebuilds it. Live radio streams
expose the announced `StreamTitle` as `streamTitle`/`streamArtist`. Stale
asynchronous page loads and action-owned metadata are rejected by generation.

Current implementation status: the TUI uses a source-independent Home model with a
breadcrumb, `1`-`9` surfaces, `s` source switching, and a `:` command palette;
playlist/detail pages keep back navigation; Radio supports the directory plus
probes/filters; Audius adds trending discovery and URL-queue playback. Playback
covers song/playlist/station full playback, live radio streams (strictly exclusive
with finite queues), preview fallback, shuffle/repeat, `e`/`E` queueing,
pause/resume/stop, favorites, themes, and toasts/overlays. Signed runtime checks
verified MusicKit catalog and personal-library access, full song and station
playback, and live radio; Audius discovery/playback and an account OAuth round trip
were verified against the real service.

Known limitations are documented in [`docs/product/limitations.md`](docs/product/limitations.md).
Notably, the explicit Music User Token request returns `MusicTokenRequestError.unknown`
on this macOS setup, so cloud-personalized APIs (For You, cloud recently played)
are unavailable; `recent` is lilt-local playback history. The project retains
automatic Xcode signing/provisioning.

An opt-in live playback check plays a real catalog song through the signed
helper and asserts `mode: full`:

```sh
LILT_LIVE_PLAYBACK=1 LILT_PLAYER_PATH="$PWD/player/Build/Products/Release/lilt-player.app" \
  go test ./internal/player -run TestLivePlayback -v
```

GitHub Actions runs unsigned, credential-free `go test`, race tests, `go vet`,
and Swift package build/tests on macOS. Signed app and live MusicKit tests remain
explicit local checks and require no CI secrets.

## Protocols

Go-to-helper is newline-delimited JSON-RPC 2.0 over a short, private,
per-session Unix socket. Go creates its `0700` directory, LaunchServices starts
the signed app with `--rpc-socket`, and the app creates the socket as `0600`.
The server owns this helper for its whole lifetime; quitting the TUI does not
stop playback or the helper. This is separate from the persistent-path Client
API socket used by clients. JSON CLI output is always either:

```json
{"ok":true,"requestId":"...","data":{}}
{"ok":false,"requestId":"...","error":{"code":"no_active_session","message":"no active lilt server"}}
```

## License

[MIT](LICENSE).
