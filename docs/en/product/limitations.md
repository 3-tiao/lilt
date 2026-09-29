# Known limitations (English overview)

[English overview](limitations.md) | [完整中文规范](../../product/limitations.md)

This page explains **accepted product and platform limitations**, not a claim that every possible failure is resolved. The [Chinese specification](../../product/limitations.md) contains the investigation evidence and the exact fallback contracts. For unresolved work that still needs verification, see [open questions (Chinese)](../../product/open-questions.md). Capability availability for your own session comes from `lilt sources --json`.

## Apple Music on macOS

- In the tested signed macOS environment, explicit Music User Token requests, personal recommendations, and Apple's cloud recently-played requests fail with MusicKit's opaque `.unknown` error. Library playlists, their tracks, catalog search, and full playback **do work**. This finding does **not** prove that Music User Tokens are unavailable to all apps or all machines. `lilt recent` deliberately shows **lilt's local playback history**, not Apple cloud recent playback; For You is not offered.
- Public MusicKit does not expose actual playback bitrate, arbitrary seek, or per-app volume. lilt does not promise lossless/Hi-Res/Atmos selection, seek, volume control, or a true PCM-based spectrum. When the variant is unavailable, the format is shown as `System-selected`, not guessed.
- `f` and `lilt favorite add` maintain a **local lilt list**. They do not read or write Apple Music favorites. Apple's localized “Favorite Songs” playlist can be listed and played read-only by its name, but may stop matching if Apple renames it. lilt does not use AppleScript to mutate the Music app or your account.

## Apple Music browser engine (Linux; opt-in on macOS)

Native MusicKit is limited to Apple platforms. Linux drives Apple's own web player through a Widevine-capable Chromium; macOS uses it only with `LILT_APPLE_ENGINE=browser`. It supports catalog discovery, recommendations, previews, and eligible full playback, **not** library access, personal playlists, catalog radio, shuffle, or repeat. The browser does not integrate with macOS Now Playing or media keys. Mobile clients are not scheduled.

Signed-out playback is a provider-defined preview (observed at around 90 seconds). Signed-in playback first reports `unverified`: only a comparison of the current item's catalog and actual media durations can establish `full` or `preview`. A missing catalog duration keeps it `unverified`. The engine tries to align the page storefront to the signed-in account; failure may leave a signed-in listener with only a preview. The **real mismatched-duration negative path is still awaiting verification**—see [OQ36 (Chinese)](../../product/open-questions.md). Do not infer authorization or full-play eligibility from a login screen alone.

`lilt auth apple-music` uses Apple's own sign-in page, not a lilt password form. Sign-in requires exclusive access to the browser profile, stops current Apple playback, and does not resume it automatically. A Widevine-capable browser is an external dependency; finding Chromium alone does not prove full-play capability. Apple's web-player changes can break this engine.

## Radio and external services

- Radio Browser is a community directory without an uptime guarantee. If its mirrors fail, lilt shows an error but can still play built-in stations. A successful HTTP reachability probe does **not** guarantee macOS AVPlayer can play a stream: some stations fail when AVPlayer requests a byte range. lilt reports the playback error instead of claiming a network outage or buffering forever. It does not automatically reconnect every failing stream.
- Radio is one live stream, not a seekable finite queue. Pausing local audio does not freeze the station's ICY `streamTitle`/`streamArtist` announcements: the displayed title may change during pause.
- Audius anonymous discovery/playback does not need an account. Optional account linking needs the deployer's Audius developer app and `LILT_AUDIUS_API_KEY` for OAuth; without configuration it returns an authorization error. Connecting an account enables access to that account's playlists; disconnect removes local credentials. On macOS, the system `security` command temporarily receives a secret in its process arguments.
- Jamendo requires **your own** free developer `client_id` (`lilt jamendo setup`), which lilt cannot bundle or share. Its free API use is non-commercial only; a commercial use needs an appropriate Jamendo license. The upstream API can intermittently return empty results or reset TLS, so bounded retries may slow a genuinely empty search. An already-open TUI may need reconnection after setup in another shell to refresh availability.

## Queues, source transitions, and terminal input

- For Apple Music, the main finite-queue path assigns a full queue at once. MusicKit may reject a particular batch; fallback appends items one by one, which may take 10–40 seconds and cannot always jump to a chosen row in place. A jump may attempt a one-shot rebuild, but a failed MusicKit assignment is **not transactional**: inspect the returned authoritative state rather than assuming the old playback survived. Preview mode does not advertise or accept native queue editing.
- After append fallback, MusicKit can reject the final playback start. lilt retries **one specific transient Code=1 refusal once**; it cannot guarantee playback. When the queue is ready but not playing, it returns `partial_failure` with `details.queueReady:true` and state rather than treating it as success. The real post-fix negative path has not been reproduced; see [OQ17 (Chinese)](../../product/open-questions.md).
- During a source switch, late notifications from the previous engine can continue briefly. The server filters them for a roughly three-second settling window and publishes its own committed state; a same-source empty notification may also be filtered during that window.
- Pressing Escape and another character immediately can be interpreted by the terminal as `Alt+<character>`. In Help, send Escape on its own or `?` to close; `q` exits the TUI. This is a terminal-input ambiguity, not a reason to replay a playback command.

For technical evidence, historical probes, and exact API links, consult the [full Chinese limitations specification](../../product/limitations.md). For safe next steps in a live session, start with [troubleshooting](../guides/troubleshooting.md).
