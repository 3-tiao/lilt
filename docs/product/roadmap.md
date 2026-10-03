# Product roadmap

This is the authoritative document for lilt's product scope, supported services, and platform/engine matrix. For wire contracts see the [Client API](../client-api/README.md); for ownership and data flow see [architecture](../architecture.md). Those technical specifications are currently maintained in Chinese.

> **Current state:** The client/server migration is complete. `lilt serve` owns Apple Music and unified Radio (built-in stations plus Radio Browser); the TUI, CLI, and agent skill use Client API `v0.1`. The server rebuilds a failed helper transport and exposes ICY `streamTitle`/`streamArtist` for live streams. Server-owned authorization flows, Audius discovery/URL-queue playback/account OAuth (Authorization Code + PKCE), Jamendo discovery and finite playback (phases J0/J1/J2/J4), and in-process Linux mpv playback are implemented. Apple Music's browser engine, `lilt auth apple-music`, and startup warmup for an existing browser profile are implemented on Linux; macOS can opt into the same engine with `LILT_APPLE_ENGINE=browser`.

## 1. Positioning

lilt is for people who live in a terminal: play Apple Music, optionally Audius and Jamendo, and internet radio, with a TUI, scriptable CLI/JSON, and natural-language control through an AI agent.

```sh
lilt tui           # Full-screen human interface; a client of the server
lilt play <ref>    # Canonical reference, Apple Music URL, or stream URL
lilt status --json # Machine-readable playback state
```

An agent can translate a request such as “play something for focus” into a source and playback choice, using the actual Client API capabilities rather than guessing.

### Supported services

This table is the **single authoritative list of supported content services**. Providers are compiled in, not installed as runtime plugins. For the capabilities actually available in this session, use `lilt sources --json`; for implementation phases see [providers](../internals/providers/providers.md).

| Service | Source ID | Content | Requirements and limits |
|---|---|---|---|
| **Apple Music** | `apple-music` | Catalog song/album/playlist search, library playlists and albums, recommendations, playback; native macOS MusicKit also supports catalog radio and shuffle/repeat | macOS uses the system account (subscription for full playback). The Linux browser engine has no library/personal playlists, catalog radio, shuffle, or repeat. |
| **Audius** | `audius` | Public discovery, trending, playlists, finite-queue playback | Anonymous by default; optional account-playlist OAuth needs a deployer's `LILT_AUDIUS_API_KEY`. |
| **Jamendo** | `jamendo` | Public discovery and trending, finite-queue playback | Each user supplies a free `client_id`; use is non-commercial unless separately licensed. |
| **Internet radio** | `radio` | Built-in stations, Radio Browser search/filter/probe, live streams | No setup required. |

## 2. Product decisions

1. **One server owns playback.** `lilt serve` owns state and the queue; the TUI, CLI, and skill are clients. Closing the TUI does not stop playback.
2. **Capability-driven source preference.** When full playback is available: Apple Music → Audius → Jamendo → radio stream (including built-in stations). An explicitly requested source takes precedence. Do not represent a preview as full playback.
3. **Deterministic API, orchestration in the skill.** The server does not silently fall back across sources. The CLI's `--source` default is deterministic, not a runtime selection algorithm: `lilt search` defaults to `apple-music`, while `lilt trending` defaults to `audius`. Other sources must be chosen explicitly.
4. **Native MusicKit without hand-managed user tokens.** On macOS, a signed Swift helper uses system authorization; lilt neither collects Apple ID credentials nor issues a developer token itself.
5. **Compile-time extensibility.** The supported services above are the scope. See [provider admission](../testing/provider-admission.md), [extension rules](../client-api/extending.md), and Jamendo's [credential and licensing details](../internals/providers/jamendo.md).
6. **No local music library.** lilt does not scan files, manage playlist files, or download/export content.
7. **Honor the requested operation.** Show progress for slow work; a dependent step waits for completion or fails explicitly. Do not add an automatic fallback that changes playback or queue semantics without stating its user-visible cost and deciding on a contract. The existing append fallback is documented under [limitations](limitations.md), section 7b; it is not a default template for new operations.

## 3. Platforms and engines

| Platform | Apple Music | Audius | Jamendo | Radio | Notes |
|---|---|---|---|---|---|
| **macOS** | Signed native MusicKit helper by default; optional Apple web player via `LILT_APPLE_ENGINE=browser` (no library access) | Official REST discovery and signed helper finite URL queue | Official REST discovery and signed helper finite URL queue; own `client_id`, non-commercial | AVPlayer live stream | Browser mode is implemented; streams still use `lilt-audio`. See [audio helper](../internals/playback/audio-helper.md). |
| **Linux** | Apple's web player in a Widevine browser: catalog, previews, **full playback where eligible**, and `lilt auth apple-music` | Official REST and in-process mpv | Official REST and in-process mpv; own `client_id` | In-process mpv | Implemented. See [Apple browser](../internals/playback/apple-web-engine.md), [Linux mpv](../internals/playback/linux-mpv-engine.md), and Nix `nix develop` / `nix run`. |
| **Other platforms** | Not scheduled | Not scheduled | Not scheduled | Not scheduled | A possible web-engine design does not imply a committed release. |

Cross-platform principle: **share the data and operation contract, not implementation code**. No cross-platform audio-quality guarantee is implied. The current public installer/Homebrew package is for macOS 14+ arm64 only; it is **not a production-readiness claim**. Linux currently builds from source or Nix (see [installation](../getting-started/install.md)).

## 4. Scope

**Included:**

- Apple Music catalog search and playback, native library playlists, lilt-local playback history and favorites, finite queue editing and shuffle/repeat where the engine advertises them.
- Radio Browser discovery and filters, built-in stations, local favorites, reachability probes, and cache.
- Audius public search/discovery/playlists, server-owned finite URL queue playback, optional account OAuth, and TUI/skill integration; see [provider phases](../internals/providers/providers.md).
- Jamendo public search/discovery/playlists, finite URL queue playback and TUI/skill integration (J0/J1/J2/J4). Its credential is personal: `lilt jamendo setup` takes the user's own `client_id`. Ads, paid use, affiliate use, or other commercial benefit require a Jamendo commercial license first; see [Jamendo](../internals/providers/jamendo.md).
- Manual TUI, scriptable CLI/JSON, published AI-agent skill, cliamp-compatible TOML themes, and local-first state.

**Excluded:**

- Local files, podcasts, lyrics, EQ, spectrum visualization, application volume and seek. Public MusicKit does not provide reliable live bitrate/seek/volume controls.
- Creating or editing Apple Music cloud-library playlists; lilt does not maintain provider playlist files.
- Downloads, exports, DRM circumvention, or forced lossless/Hi-Res/Atmos quality.
- A user-installable plugin system (sources are registered at build time), or a mobile UI.

## 5. Next steps (not scheduled)

- Local Activity SQLite storage, `history.*`/`favorites.add|remove`/`data reset`, and the TUI's All Favorites view are implemented; real-account acceptance remains outstanding. See [local activity](../internals/persistence/local-activity.md).
- Move the default macOS config/state root into native Application Support with a deterministic migration from the current XDG-style location (see [state persistence](../internals/persistence/state.md)). Activity and the lifecycle lock already use the durable state root instead of cache/socket paths.
- Gracefully exit the Apple Chromium engine after ten uninterrupted idle minutes; see the [current browser lifecycle](../internals/playback/apple-web-engine.md).
- Cloud synchronization is **outside the current product scope**. State remains owned by one local server; background continuation and automatic launch at login are not scheduled.

**Evaluated source candidates (recorded 2026-09-20 onward, not a release promise):**

- **SoundCloud — rejected (2026-09-21).** Its API app registration requires Artist Pro; clients need a `client_secret`; streaming is HLS with continuing authorization, and its API terms prohibit an on-demand experience aggregated with other services. See the [Jamendo comparison](../internals/providers/jamendo.md).
- **Spotify — evaluated, not integrated (2026-09-29).** Music playback through its Web Playback SDK/Connect requires Premium. Development mode requires the app owner to have Premium and allows at most five allowlisted users; the former 30-second `preview_url` path was removed for new and unextended development apps as of 2024-11-27. Extended quota was restricted to eligible organizations (at least 250k MAU) as of 2025-05-15. Developer Policy §III.5 conflicts with lilt's multi-source aggregation. OAuth supports PKCE, so a `client_secret` is not the blocker. Sources checked 2026-09-29: [quota modes](https://developer.spotify.com/documentation/web-api/concepts/quota-modes), [Web Playback SDK](https://developer.spotify.com/documentation/web-playback-sdk), [Developer Policy](https://developer.spotify.com/policy), and [2024 API changes](https://developer.spotify.com/blog/2024-11-27-changes-to-the-web-api). These external policies can change; recheck before making a new decision.
- **Jamendo — selected and implemented (J0/J1/J2/J4).** Free read-only developer plan, user-provided `client_id`, ordinary MP3 media. A commercial license is required for monetization or usage beyond the free 35,000 requests/month; see [Jamendo](../internals/providers/jamendo.md).
- **Jamendo radio streams — deferred.** `/radios` and `radios/stream` are continuous-stream semantics, outside the finite-queue implementation.
- **Generic setup for keyed sources — deferred.** Wait until a second source requires user-provided credentials before exposing a generic `interaction.type=input` and server-owned setup flow.

Any future source must pass [provider admission](../testing/provider-admission.md): compile-time registration, honest capabilities, stable IDs, and resolution of short-lived media URLs only when playback starts (never in persisted state or normal logs). No new third-party dependency is assumed. The Audius shared contract suite and opt-in real E2E are tracked in [integration testing](../testing/integration.md).

## 6. Resolved decisions and open work

ICY now exposes live `streamTitle`/`streamArtist` (see [models](../client-api/models.md)); seek and volume remain system/provider concerns rather than lilt controls. Cloud sync is out of scope. Unresolved engineering behavior and verification gaps, with evidence and next steps, live only in [open questions](open-questions.md), not in a duplicate list here.
