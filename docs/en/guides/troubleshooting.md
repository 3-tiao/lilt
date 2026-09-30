# Troubleshooting

[English](troubleshooting.md) | [简体中文](../../guides/troubleshooting.md)

Start with observable state and logs before deciding whether the failure is in the client, server, helper, or upstream service. For already-occurring real-session triage, see [integration testing (Chinese)](../../testing/integration.md); do not restart or replay audible operations just to investigate.

```sh
lilt status --queue --json
lilt auth status apple-music --json
lilt sources --json
lilt log 100
lilt doctor                     # macOS native MusicKit token diagnosis; no token contents
```

Logs default to `~/.local/state/lilt/log/lilt.jsonl` (`LILT_LOG` overrides), one JSON event per line with 5 MB rotation. Standard logs omit raw searches and metadata titles and strip URL credentials and query parameters. Explicit development diagnostics (`LILT_LOG_LEVEL=debug` or `LILT_DEV_LOG=1`) preserve more reproduction detail, including request parameters and TUI keys; **review debug logs before sharing them**. Credentials are never supposed to be logged, even at debug level. See the [journal contract (Chinese)](../../internals/troubleshooting/journal.md).

| `error.code` | Meaning / next step |
|---|---|
| `session_unavailable` | The server or socket is unavailable; reconnect or explicitly restart the server. |
| `active_session` | A server already owns this socket; use it instead of starting another. |
| `authorization_required` | Ask the user to authorize that source. |
| `unsupported_command` | The source lacks the capability; change the command or source, **do not retry**. |
| `source_mismatch` | References belong to different sources; use one source. |
| `operation_outcome_unknown` | A timed-out mutation may have taken effect; **inspect state, do not replay**. |
| `engine_restarting` | The operation definitely did not run; retry after the engine settles. |
| `state_save_failed` | Persistent state was not saved; check the explicit response. |
| `storage_unavailable` | Activity storage failed but playback continues; investigate before considering `lilt data reset --confirm`. |

For the complete list and semantics, use `lilt api --json` or [errors (Chinese)](../../client-api/errors.md). Messages are human-facing and can change.

**Common symptoms:** `mode:preview` may mean no authorization, no subscription, or a browser storefront mismatch; check authorization separately. `working… 9/16` can mean a real queue fill is progressing. `lilt recent` is local history, not Apple's cloud recent-playback list. If the Radio Browser directory fails, built-in stations still work; station reachability does not guarantee AVPlayer compatibility. After a source switch, the previous engine may briefly report old state; see [known limitations](../product/limitations.md).

**Stop/status waits during automatic track changes:** finite URL-queue advances and media-failure retries share the serialized control channel; controls do not interrupt them immediately. At the [execution and cleanup budget (Chinese)](../../client-api/protocol.md#4-超时预算), the server clears the queue, publishes stopped, and records `source_unavailable` in the journal and watch feed. Audio cleanup is best-effort. If controls remain unresponsive beyond that budget, collect existing logs rather than repeating play commands.

For reproducible, credential-free checks use `just test` or `just verify`. Real playback is opt-in, audible, and must not run alongside daily playback; see [integration testing (Chinese)](../../testing/integration.md). Unresolved engineering questions are tracked in [open questions (Chinese)](../../product/open-questions.md).
