# lilt

A first vertical slice of a macOS Apple Music terminal controller. A foreground
`lilt tui` owns both playback and the signed `lilt-player` helper; quitting the
TUI terminates both. It is not a daemon. macOS only for now; Radio playback on
Linux via an in-process mpv backend is a proposed design
(`docs/spec/linux-mpv-engine.md`) pending Linux hardware.

## Quick start

The repository includes a `Justfile` so normal development does not require
setting helper paths manually:

```sh
just auth                 # first-time Apple Music authorization
just run                  # open the TUI
just focus                # open the TUI in preset mode
just search "Nujabes"     # search and play full audio or a preview
just find "Nujabes"       # one-shot catalog search as JSON
just recent               # recently played songs as JSON
just library              # list personal Apple Music playlists as JSON
just play song:1440845629 # play a URL or kind:id in the running TUI session
just doctor               # safely diagnose MusicKit token validity
just fake                 # UI development without Apple services
just test                 # unit tests and static checks
just verify               # credential-free Go/Swift tests, race, vet, build
just verify-app           # verify plus signed Xcode app build
```

`just build-player` uses Xcode automatic signing for Team `9Y6KG228YM` and App
ID `com.caiguo.lilt-player`. Apple Music login is never performed by Fastlane
or by lilt: native MusicKit uses the Apple Music account already configured in
macOS. Never enter an Apple ID or password into lilt.

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

The TUI's private socket defaults to `$XDG_CACHE_HOME/lilt/session.sock` (the
Go user cache directory) and is mode `0600`; set `LILT_SOCKET` only for tests.
`status`, `pause`, `resume`, `next`, `previous`, and `play` are secondary
commands. `play` accepts an Apple Music URL or `kind:id` (for example
`song:1440845629` or `playlist:pl.u-abc`) and starts it in the running session.
With no TUI they return the stable JSON error code `no_active_session`.
The current implementation has one local macOS target; `--target` and remote
target selection remain future work and are not accepted CLI options.

`lilt tui` opens a fullscreen, lazygit-style UI. The top bar has two labelled
rows: `SOURCE` (Apple Music / Radio, switched with `Tab`) and `VIEW` (the
sub-views, selected with `1`-`9`; Apple Music remembers its last view and Radio enters Favorites). There
is a single list cursor.

- Apple Music sub-views: `Home`, `Playlists`, `Recent`, and `Presets`. `Enter` on a playlist opens its tracks; in a
  playlist detail, `Enter` plays the whole playlist starting at that track.
- Radio sub-views: `Favorites`, `Recent`, and `Browse`, with `Favorites` as the
  default whenever Radio is entered. Browse is the single discovery surface: it
  shows up to 20 Popular Worldwide stations by default, and `/` Search &
  Filters (optional station-name text plus guided Language / Genre / Country
  selectors, combined with AND) changes what Browse shows in place. Confirm
  reloads Browse with the query and its title reflects it
  (`Showing: city pop · Japanese`); reopening `/` from Browse prefills the
  current query, an empty Confirm restores Popular Worldwide, `Esc` on a
  queried Browse jumps straight back to the default list (footer shows
  `esc popular`), and queries are session-only — never persisted across
  restarts. `Reset filters` clears
  pending facets without changing text or executing a query.
  `a` adds a stream URL to Favorites and plays it.

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

Keys: `Tab` switches source (Apple Music / Radio), `1`-`9` selects a sub-view
(Apple Music remembers its last view; Radio enters Favorites), `0` opens the Now Playing queue,
`[`/`]` cycle sub-views, `j`/`k`/`g`/`G` and `Ctrl+d`/`u`/`f`/`b` navigate. In an Apple playlist detail,
`p` plays all in order, `s` shuffles the playlist, and `Enter` plays from the selected track;
elsewhere `Enter`/`p` play the selected item — `p` toggles pause/resume when the cursor sits on
the item already playing. `x` is inert outside focused Up Next. `Space`/`c` pause, `n`/`b` next/previous (Apple Music), `v` stop,
`R` repeat, `e`/`E` queue next/append, `f` favorite, `a` add a stream URL to Favorites (and play it),
`/` search (or Radio Search & Filters), `F` local filter in Apple Music only, `t` theme picker,
`i` info, `?` help, `Esc`/`Backspace` back, `q` quit.

The footer is context-sensitive: root views show source/view navigation, while playlist and queue
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
(recently played), and `lilt library --json` (personal playlists).

## Presets

`lilt focus` opens the TUI in preset mode. Presets live in
`~/.config/lilt/presets.toml`. `LILT_CONFIG` names a configuration **directory**
(containing `presets.toml` and `themes/`), and `LILT_PRESETS` is the preferred
explicit preset-file override. For migration compatibility only, when
`LILT_CONFIG` points to an existing regular file it retains its deprecated
preset-file meaning; move that value to `LILT_PRESETS` before creating a config
directory at the same path:

```toml
[focus]
label = "Focus Lofi"
query = "lofi focus"
kind  = "station"   # station | playlist | song

[jazz]
label = "Jazz"
query = "jazz classics"
kind  = "playlist"
```

`label` defaults to the table key and `kind` defaults to `song`; entries with an
empty `query` are skipped. Playing a preset resolves the query live through
MusicKit (`Stations`, `SearchPlaylists`, or `Search` by kind), ranks candidates
by prior local choices, then plays the top result. A use is recorded only after
the helper acknowledges successful playback. Usage counts are stored in
`~/.local/state/lilt/state.json` (override with `LILT_STATE`); a missing file is
fine.

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
displayed ordering before sending `startAt`. If the
helper stream closes, progress interpolation stops and the TUI asks the user to
quit and restart lilt. AVPlayer item failures are shown as actionable playback
errors rather than as a silent pause.

All Apple/iTunes and Radio Browser text crosses a terminal-sanitization boundary
that removes ESC and all C0/C1 controls, replacing tabs/newlines with one space
to keep row widths deterministic while preserving normal Unicode. Network/helper
operations have bounded deadlines. Because MusicKit calls are serial and not
reliably cancellable, a timed-out RPC permanently closes that transport and
terminates its private helper; quit and restart lilt to reconnect. Stale
asynchronous page loads and action-owned metadata are rejected by generation.

Current implementation status: the TUI has labelled `SOURCE` (Apple Music /
Radio, `Tab`) and `VIEW` (`1`-`9`) rows with a single list; Apple Music
playlists open a track detail with back navigation; Radio supports favorites,
Radio Browser discovery filters and popular stations. Playback covers song/playlist
/station full playback, live radio streams (strictly exclusive with Apple
Music), preview fallback, shuffle/repeat, `e`/`E` queueing, pause/resume/stop,
favorites, themes, and toasts/overlays. Presets (`lilt focus`) resolve live
queries and rank candidates by local history. Signed runtime checks verified
MusicKit catalog and personal-library access, song and station playback reaching
`mode: full` / `status: playing`, and live radio (`mode: stream`, `isLive`).

Known limitations are documented in [`docs/spec/limitations.md`](docs/spec/limitations.md).
Notably, the explicit Music User Token request returns `MusicTokenRequestError.unknown`
on this macOS setup, so cloud-personalized APIs (For You, cloud recently played)
are unavailable; recently played falls back to the local library sorted by
`lastPlayedDate`. The project retains automatic Xcode signing/provisioning.

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
This is separate from the persistent-path Go host socket used by secondary CLI
commands. TUI exit sends `shutdown`; the exact app instance stops both players,
unlinks its helper socket, and terminates. JSON CLI output is always either:

```json
{"ok":true,"data":{}}
{"ok":false,"error":{"code":"no_active_session","message":"no active lilt TUI session"}}
```
