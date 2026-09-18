# Tech Design: Playback State Synchronization

**Status: state subscription, generation/session correlation, and URL natural-end handling are implemented.**

> `lilt serve` 持有 helper，把它归一化为 Client API 的 PlaybackState 并经 watch
> 广播给各 client。见 [`../architecture.md`](../architecture.md) 与
> [`../client-api/watch.md`](../client-api/watch.md)。

## Decision

Playback state uses a mixed model:

1. A command RPC returns an immediate authoritative `State` snapshot.
2. The playback helper publishes subsequent `stateChanged` notifications while it
   owns an active player.
3. The UI renders finite-track progress from the latest snapshot plus its local
   monotonic clock. Its redraw timer never polls the helper.
4. EOF closes the update stream. The UI clears its interpolation clock, marks
   an in-flight playing/buffering snapshot `disconnected`, and displays an
   actionable quit/restart message; stale progress must not continue moving.
5. An RPC deadline invalidates the whole serial helper transport. The host
   closes the socket, rejects every late response/notification, and terminates
   that private helper instance. `lilt serve` rebuilds a fresh helper with
   bounded backoff, publishes `server.warning` then `engine.restarted`, and must
   never replay the timed-out command automatically (see
   [`../client-api/README.md`](../client-api/README.md)).

This replaces UI-driven periodic `state` RPCs. It is not sufficient to return
only the state from `play`: progress, track transitions, and external media
controls can change after that response.

## Motivation

`ApplicationMusicPlayer.playbackTime` is read on demand; it does not provide a
reliable public high-frequency callback suitable for the terminal UI. Repeated
TUI RPCs couple frame rate to IPC latency, can queue behind slow playback
commands, and make progress appear frozen.

The playback helpers are the only processes that can read `ApplicationMusicPlayer`
(`lilt-player`) and `AVPlayer` (`lilt-audio`), so they own sampling and publish one normalized contract to every
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
{"jsonrpc":"2.0","id":42,"result":{"status":"playing", "playbackGeneration":17, "transportSessionID":"s-42", "...":"State"}}
{"jsonrpc":"2.0","method":"stateChanged","params":{"sequence":18,"playbackGeneration":17,"transportSessionID":"s-42","origin":"client","state":{"status":"playing", "position":13.3, "...":"State"}}}
```

New methods:

| Method | params | result |
|---|---|---|
| `subscribeState` | — | current `{sequence,state}` snapshot |
| `unsubscribeState` | — | `{}` |

`stateChanged.params` includes a strictly increasing helper-local `sequence` and causal
`playbackGeneration`/`transportSessionID`/`origin`.
The UI discards an older sequence so a delayed command response cannot overwrite
a newer notification. `State` keeps the shape defined in [`helper-rpc.md`](helper-rpc.md).

A command response whose starting sequence is older than a notification that
already arrived skips only its `State` snapshot and queue context. Its completed
   side effects — recents, favorites, notes, and view refreshes — are
still applied: for live streams the helper always publishes a `stateChanged`
notification right after the response, and that notification frequently reaches
the UI before the response itself, so tying metadata to response order would
silently lose favorites and recents for plays that succeeded. Superseded actions
are handled separately by the action-id guard.

## Helper Behavior

- A successful state-changing command (`play`, `pause`, queue edit, radio
  action, and so on) returns its immediate `State`, then publishes a
  `stateChanged` notification when the observable state differs.
- Server playback sessions assign one `playbackGeneration` and immutable `transportSessionID` before
  starting helper playback; the helper buffers observer notifications until the start response is serialized, and
  response/notification carry both. Server drops a stale helper instance, generation,
  or session. Every observer closes over its generation/session, so a delayed callback from replaced MusicKit or
   AVFoundation mode cannot be relabeled as current. A media-key/system notification uses that captured pair with
   `origin:"external"` and remains observable.
- A `url` AVPlayer natural end emits private `State.ended=true` with its captured generation/session. The server
  accepts only the active pair, advances its URL queue, and strips `ended` before public projection.
- Playback sources are mutually exclusive. MusicKit's `stop()` can keep
  reporting — and sounding — `playing` for up to ~3s, so when Radio starts while
  Apple Music was playing, the new stream starts muted and is unmuted only once
  MusicKit reports non-playing; the Apple Music preview fallback waits (bounded)
  instead, since it cannot buffer quietly. Resuming a stream always clears the
  muted flag.
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
