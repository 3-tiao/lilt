# Tech Design: Playback State Synchronization

**Status: server-side state subscriptions, Client API watch, and server-owned URL queue natural-end handling are implemented. Full end-to-end lifecycle fencing remains a proposal.**

The current architecture is **backend → server → Client API client**. TUI, CLI and
agent clients do not connect to playback helpers or consume their private sequence
numbers. See [architecture](../../architecture.md), the
[helper protocol](helper-rpc.md) and [Client API watch](../../client-api/watch.md).

The [end-to-end concurrency proposal](../concurrency.md) covers command admission,
per-start ownership, backend observation ordering, multi-client conditions and
reconnection epochs. It is not implemented and does not replace the current wire
contracts.

## Decision

Playback state uses a mixed model:

1. A backend command returns a state observation to the server. The server
   projects its committed playback state into the command response.
2. Backend subscriptions deliver subsequent observations to the server, including
   progress, track transitions, natural ends and external media controls.
3. The server owns the public source, queue and playback projection, assigns its
   public sequence, and broadcasts committed snapshots through `session.watch`.
4. Clients merge public command results and watch snapshots. Finite-track progress
   may be interpolated with a local monotonic clock; track, queue and status may not.

This avoids coupling TUI redraw frequency to helper IPC latency. A `play` response
alone is insufficient: playback can change after acknowledgement without another
client command.

## Ownership

| Concern | Owner |
|---|---|
| Native playback observation | The selected backend: MusicKit helper, audio helper, mpv or browser adapter |
| Public playback state and active source | `lilt serve`, using backend observations and transport ownership |
| Finite URL queue and automatic advancement | Server-owned URL transport |
| Native MusicKit queue execution / natural advancement | MusicKit backend; server projects its observed queue |
| Command acknowledgement | Backend RPC internally; Client API response publicly |
| Backend sampling / notification publication | The corresponding backend or adapter |
| Public state broadcast | Server watch hub |
| Display-only progress interpolation | Client local monotonic clock |

There are two sequence domains: backend-local subscription sequence and
server-wide Client API sequence. They are not interchangeable. The proposed
unified ordering of backend command responses and notifications is described in
[the concurrency design](../concurrency.md), not assumed to exist today.

## Current implementation boundary

Finite URL queues bind generation/session before starting their driver. The pair
identifies the queue session, not each individual start: changing item or retrying
within the session reuses it. The audio helper therefore also checks the captured
AVPlayer instance before applying end, failure, time or artwork callbacks.
Cancellation or observer removal alone does not establish ownership.

Native MusicKit has a serial RPC path, MainActor execution and bounded start
confirmation. These do not establish the same generation/session correlation for
all native requests and observations. The existing protocol requirements and
implementation boundary are stated in [helper RPC](helper-rpc.md); do not infer
complete lifecycle isolation from the presence of a subscription.

The browser and mpv adapters likewise have their own local protections. Their
contracts are in [Apple web engine](apple-web-engine.md) and
[Linux mpv engine](linux-mpv-engine.md). The concurrency proposal specifies the
remaining common ownership and observation rules without claiming that each
adapter already satisfies them.

## Failure and display behavior

A helper transport EOF is a connection failure, not a track's natural end.
A timed-out serial helper transport is invalidated; late messages from that
transport must not restore it, and the server must not replay the unknown command
automatically. Rebuild and public failure semantics are defined by
[Client API protocol](../../client-api/protocol.md).

Client disconnection stops progress extrapolation. Loading, failure feedback,
mutation admission and stale query/action results follow the
[UI asynchronous-state contract](../../ui/async-state.md). Clients subscribe through
Client API watch, not `subscribeState` on a helper.

The UI never modifies canonical `position`. For accepted `playing`, finite,
non-live snapshots it may display:

```text
displayPosition = min(snapshot.position + monotonicNow - receivedAt, duration)
```

Paused, stopped, buffering, live or disconnected states do not extrapolate.
Backend sampling corrects displayed facts; it is not the UI frame clock and does
not prove what was audibly heard. Final-build real-audio verification remains
separate from hermetic subscription and ordering tests.
