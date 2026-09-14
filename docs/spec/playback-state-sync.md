# Tech Design: Playback State Synchronization

**Status: implemented.**

## Decision

Playback state uses a mixed model:

1. A command RPC returns an immediate authoritative `State` snapshot.
2. The playback helper publishes subsequent `stateChanged` notifications while it
   owns an active player.
3. The UI renders finite-track progress from the latest snapshot plus its local
   monotonic clock. Its redraw timer never polls the helper.

This replaces UI-driven periodic `state` RPCs. It is not sufficient to return
only the state from `play`: progress, track transitions, and external media
controls can change after that response.

## Motivation

`ApplicationMusicPlayer.playbackTime` is read on demand; it does not provide a
reliable public high-frequency callback suitable for the terminal UI. Repeated
TUI RPCs couple frame rate to IPC latency, can queue behind slow playback
commands, and make progress appear frozen.

The helper is the only process that can read `ApplicationMusicPlayer` and
`AVPlayer`, so it owns sampling and publishes one normalized contract to every
UI.

## Ownership

| Concern | Owner |
|---|---|
| MusicKit / AVPlayer reads | `lilt-player` helper |
| Authoritative playback state | helper snapshots |
| Command acknowledgement | JSON-RPC response |
| Periodic sampling and state-change detection | helper |
| Smooth progress display | UI local monotonic clock |
| Queue, track, status correction | next helper snapshot |

The UI must never advance or otherwise mutate the canonical `State.position`.
It derives a display-only position from the most recently received snapshot.

## Wire Protocol

The existing private Unix socket remains JSON-RPC 2.0, but becomes a
bidirectional multiplexed stream. Responses retain their request `id`.
Helper-originated notifications have no `id`:

```json
{"jsonrpc":"2.0","id":42,"result":{"status":"playing", "position":12.3, "...":"State"}}
{"jsonrpc":"2.0","method":"stateChanged","params":{"sequence":18,"state":{"status":"playing", "position":13.3, "...":"State"}}}
```

New methods:

| Method | params | result |
|---|---|---|
| `subscribeState` | — | current `{sequence,state}` snapshot |
| `unsubscribeState` | — | `{}` |

`stateChanged.params` includes a strictly increasing helper-local `sequence`.
The UI discards an older sequence so a delayed command response cannot overwrite
a newer notification. `State` keeps the shape defined in [`rpc.md`](rpc.md).

## Helper Behavior

- A successful state-changing command (`play`, `pause`, queue edit, radio
  action, and so on) returns its immediate `State`, then publishes a
  `stateChanged` notification when the observable state differs.
- While playback status is `playing` or `buffering`, sample Apple Music at a
  modest cadence (target: once per second). This detects track transitions,
  paused state, queue changes, and corrects elapsed time.
- MusicKit keeps `playbackStatus == .playing` while audio is stalled. When a
  sample shows less than half the positional progress expected for the elapsed
  wall time (and the position did not jump backward from a seek or track
  change), the helper reports `status=buffering`; the first advancing sample
  restores `playing`. Detection latency stays below the 1s sample interval.
- Stop periodic MusicKit sampling while stopped or paused. Publish immediately
  for a command result or a known player transition.
- For Radio/preview, use `AVPlayer`'s periodic time observer for timely state
  changes. Normalize the emitted data to the same `State` contract.
- Do not write responses and notifications concurrently without serialization:
  the socket writer needs one ordered write lock. Request execution may be
  concurrent only when command ordering is explicitly preserved.

The helper's sampling cadence is a correction channel, not the progress frame
rate. The UI remains smooth even if a sample is delayed.

## UI Behavior

- Subscribe once after the helper connection is established; unsubscribe during
  orderly shutdown.
- Record the local monotonic receipt time for every accepted snapshot.
- For `status=playing`, finite, non-live media:

  ```text
  displayPosition = min(snapshot.position + monotonicNow - receivedAt, duration)
  ```

- Render at 250ms (or the host UI's equivalent) without a `state` RPC.
- For `paused`, `stopped`, `buffering`, or `isLive=true`, display the snapshot
  position without interpolation.
- Command responses update UI immediately. Notifications remain the source for
  subsequent reconciliation and external media-control changes.

## Migration

1. Extend the Go stream client to classify a JSON-RPC response versus a
   notification and dispatch `stateChanged` to the TUI model.
2. Add a serialized notification writer and subscription lifecycle to the Swift
   helper.
3. Add `subscribeState` and emit initial/current snapshots.
4. Remove periodic TUI `state` RPCs. Retain `state` temporarily as a diagnostic
   and one-shot CLI method, not as the interactive progress transport.
5. Test delayed commands, notification ordering, pause/resume, track changes,
   queue edits, Radio, shutdown, and an external media-key transition.

## Non-goals

- Sub-250ms authoritative MusicKit sampling.
- A cross-device real-time playback synchronization protocol.
- Changing the `State` schema solely to encode UI animation state.
