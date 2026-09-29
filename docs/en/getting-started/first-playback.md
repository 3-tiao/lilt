# First playback

[English](first-playback.md) | [简体中文](../../getting-started/first-playback.md)

## 1. Start the session

`lilt tui` starts a server if none is running, or attaches to the existing one. CLI commands that need the server also start it once when there is no active session. To run it explicitly:

```sh
lilt serve --json        # Foreground; --detach waits for the listener and returns its PID
```

There is one server per isolated state root, owning playback, the queue, and `state.json`. Exiting the TUI does **not** stop playback. Use `lilt stop` to stop audio or `lilt quit` to shut down the server.

## 2. Authorize Apple Music (optional)

On macOS, the default signed MusicKit helper presents the system authorization dialog the first time playback needs it. It does not ask lilt to collect your Apple ID password. On Linux or when you opt into `LILT_APPLE_ENGINE=browser`, use Apple's own sign-in page:

```sh
lilt auth apple-music
lilt auth status apple-music
lilt auth disconnect apple-music  # Only when you intend to disconnect
```

The sign-in window and playback browser exclusively use the same profile. Starting sign-in stops current Apple playback; completion does **not** resume it automatically. The page storefront follows the signed-in account.

## 3. Play something

```sh
lilt tui                                  # / search → Enter play; Space pause; ? help; q exit
lilt search "Nujabes" --play              # Opens the interactive TUI
lilt play apple-music:song:1440845629     # Canonical source:kind:id reference
lilt play "https://station.example/stream" # Radio accepts a stream URL
```

Other examples of canonical references: `apple-music:playlist:pl.u-abc`, `audius:song:<id>`, `jamendo:song:<id>`. The public kind enum is `song|playlist|album|station|stream`. Radio playback takes a URL; `radio:<normalized-url>` is a persistent identity, not a playback reference.

## 4. Check what happened

```sh
lilt status --queue --json
lilt auth status apple-music --json
lilt sources --json
```

Playback `mode` can be `full` or `preview`; browser playback may remain `unverified` until its catalog and media durations agree. Preview lengths depend on Apple (native around 30 seconds; browser observed around 90 seconds) and may be missing. **Do not infer authorization from playback mode**: query `auth status` separately. Scripts should branch on JSON `ok` and stable `error.code`, not `error.message`; see [errors (Chinese)](../../client-api/errors.md).

## 5. Configure other sources if needed

Audius discovery and playback work anonymously; account linking is optional. Jamendo requires your own free `client_id`, for non-commercial use:

```sh
lilt jamendo setup
lilt search "lofi" --source jamendo
lilt trending --source jamendo --type song
```

Radio needs no setup. Continue with the [TUI guide](../guides/tui.md), [CLI guide](../guides/cli-and-scripting.md), or [radio guide](../guides/radio.md).
