# Listen to internet radio

[English](radio.md) | [简体中文](../../guides/radio.md)

Radio combines built-in stations (`origin=builtin`) with the community Radio Browser directory (`origin=directory`). It plays **one live stream**, requires no credentials, and has no finite queue. The full discovery and caching contract is in [radio discovery (Chinese)](../../internals/providers/radio-discovery.md).

## In the TUI

Radio has Home, Browse, and Recent surfaces. Browse is its discovery view. It shows up to 100 Popular Worldwide stations per page, displaying cached results before a background refresh. `/` opens **Search & Filters**: name, Language, Genre, and Country combine with AND; sorting offers Recommended, Popular, Fastest, and Name. Filters last for the session, not in persistent state.

Press `r` to reload after a directory failure, or `a` to save a stream URL as a local favorite and play it. A local reachability probe annotates rows (`unchecked`, `checking`, measured delay, or error), with at most two probes at once. **A probe never blocks playback**. If a stream fails to start within ten seconds, playback fails rather than buffering forever.

## From the CLI

```sh
lilt radio search --tag lofi --json
lilt radio search --origin builtin --json
lilt radio search --name jazz --language English --json
lilt radio options --json
lilt radio probe --url <url> --json
lilt radio cache --json
lilt play "https://station.example/stream" --name "My Station"
```

Use `lilt api --json` for exact arguments. Playback takes a stream URL; `radio:<normalized-url>` is an identity used in persistent state, **not** the value to pass to `lilt play`.

Starting Radio stops the other active source and vice versa. ICY-enabled streams may expose `streamTitle`/`streamArtist`, while the TUI displays `LIVE`. Next/previous and finite-queue editing do not apply. Some publicly reachable streams fail when macOS AVPlayer requests an HTTP Range; lilt reports the playback error rather than treating a successful reachability probe as guaranteed playback. See [known limitations](../product/limitations.md).

Radio Browser is a community service with no uptime guarantee. If both configured mirrors fail, Browse reports the unavailable directory, but built-in stations remain available.
