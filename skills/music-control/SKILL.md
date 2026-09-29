---
name: music-control
description: Control music and internet radio with the lilt CLI on macOS or Linux. Use for requests in any language to play a song, artist, playlist, genre, or station; choose background music; or pause, resume, skip, and stop playback.
---

# Music and radio control

Use **only the `lilt` CLI**. Do not drive `lilt tui`, start `lilt serve` yourself, call the server directly, or read or edit lilt's local storage. The CLI starts its server when needed.

This skill supplies **triggers, decisions, and recipes**, not an API copy. Before using unfamiliar options, consult `lilt api --json` (works without a server); `lilt help` is the human-readable reference. Read `lilt sources --json` for current availability and capabilities. Use `--json` for actions and branch on `ok` and `error.code`, not on the wording of `message`.

Use the installed `lilt` on `PATH`. Only when working inside the lilt repository and no installed CLI is available, use its pinned `.lilt-prerelease/current/lilt`; use the repository's `./lilt` only for an explicitly requested development task. Do not replace a daily user's build with an unverified development binary.

## Non-negotiable decisions

- **Honor an explicit source.** If the user chose Apple Music, Audius, Jamendo, or Radio, do not substitute another source when it fails or lacks a capability. Explain the limitation and ask before changing course.
- **Check capabilities before choosing.** First read `lilt sources --json`; consider only sources that are available and advertise the action you need. For an unspecified source and ordinary “play music,” require full playback: Apple Music → Audius → Jamendo → radio stream, subject to the live descriptor's priority and capabilities. A preview is not full playback. If the user specifically requests a preview, it may be used where advertised and must be called a preview.
- **Search one source at a time.** When a request names a song or artist, search the highest-priority suitable source first. If it has no credible match, explicitly check the next source's capability and search there; do not silently switch after a playback failure whose outcome might be uncertain. Explain the final source choice.
- **Do not authorize or configure accounts on your own.** On `authorization_required`, tell the user to run `lilt auth <source> --json`. Do not run an interactive authorization, Jamendo setup, or `lilt auth disconnect` without an explicit request. Jamendo requires the user's own client ID and is for non-commercial use.
- **Do not silently change the requested operation.** If shuffle/repeat is unsupported, do not retry without that option and claim success. Explain the unsupported mode and ask whether a different mode or source is acceptable. A live radio stream has no finite queue or next track.
- **Treat uncertain outcomes as uncertain.** On `operation_outcome_unknown` or `duplicate_result_unavailable`, read current status and queue; never replay the mutation with a new request ID. On `partial_failure` with `queueReady`, the queue may already exist: report it and do not rebuild or replay it automatically. For any other playback error, inspect authoritative state before considering another candidate.
- **Verify and report.** After each playback or queue mutation, read `lilt status --json` (and the queue when relevant). Report the selected item, source, and reason briefly. If playback is still buffering or unverified, say so; do not assert that audio started, that a preview is full-length, or that a failed action succeeded.

## Pick the content the user meant

- **Named song:** search songs in the chosen source, compare both title and artist, then play a suitable `item.ref`. If several catalog IDs share display text, keep them distinct; do not deduplicate by title. With no credible match, try the next available full-playback source only if the user did not specify one.
- **Named artist:** prefer an artist-specific playlist whose title or artist matches the request; otherwise select up to ten songs whose **artist field** matches and create a temporary session queue. A name mentioned only in a cover's title is not an artist match. Do not add shuffle unless requested.
- **Mood or background music:** prefer a matching Apple Music playlist if full playback is available, then suitable Audius or Jamendo content. Choose radio for an explicit station request or when finite-queue music is unavailable. For focus, prefer lofi, jazz, instrumental, or classical over news and talk; treat this as a preference, not a guarantee that a station fits.
- **Something to play without specifics:** choose a suitable library playlist, trending selection, or featured songs from an available source. Do not assume personal-library access on the browser-based Apple Music engine; check the descriptor first.
- **Radio:** search by the requested name or tag. Built-in stations are the more predictable first choice; directory results may be stale, and a successful reachability probe does not guarantee playable audio. Play the selected stream URL, not a `radio:` identity, and confirm the resulting live state.
- **Shuffle, repeat, and queue:** check the required capability before setting a mode or editing. Apply a requested playback mode at start when supported. Read the latest queue before index-based edits; concurrent clients can still change it because CLI index operations do not accept `ifQueueRevision`. Do not remove duplicates, clear, reorder, or otherwise “fix” the user's queue unless asked.
- **Pause, resume, skip, or stop:** perform only the requested control. When no track or finite queue supports it, report the actual state instead of guessing another action or station.

## Facts worth explaining

`lilt recent` is lilt's **local cross-source history**, not Apple cloud recent playback. Favorites are also local to lilt. `lilt library` needs a source that advertises library access (Audius needs an account connection); `lilt play-songs` makes a **temporary session queue**, not a permanent provider playlist. Playback `mode` describes the current media, **not authorization**: query authorization separately when needed. For browser Apple Music, `unverified` is not a promise of full playback.

For exact command syntax, response fields, and the current stable error-code catalog, always use `lilt api --json` rather than memorizing a table in this skill.
