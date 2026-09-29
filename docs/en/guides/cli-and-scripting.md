# CLI and JSON scripting

[English](cli-and-scripting.md) | [简体中文](../../guides/cli-and-scripting.md)

The CLI is the common entry point for humans, scripts, and the bundled agent skill. Run `lilt help` for readable usage or **`lilt api --json` for the authoritative offline command catalog**, including parameters, response models, and stable error codes. The detailed [command specification (Chinese)](../../client-api/commands.md) is not translated here.

## JSON responses

```json
{"ok":true,"requestId":"...","data":{}}
{"ok":false,"requestId":"...","error":{"code":"no_active_session","message":"no active lilt server"}}
```

Branch on `ok` and `error.code`; the human-readable `message` can change. See the [stable error specification (Chinese)](../../client-api/errors.md).

Commands requiring a server start one when none is active and retry once. `lilt stop` only stops playback; `lilt quit` shuts down the server. If a non-idempotent operation returns `operation_outcome_unknown`, **read state before doing anything else; do not replay it with a new request ID**.

```sh
lilt search "Nujabes" --json
lilt search "Nujabes" --play                  # Opens the TUI, not a headless JSON operation
lilt play apple-music:song:1440845629 --json
lilt play-songs audius:song:1,audius:song:2 --shuffle --repeat all --json
lilt pause --json; lilt resume --json; lilt next --json; lilt stop --json
lilt queue --json
lilt queue add audius:song:3 --next --json
lilt queue remove 2 --json
lilt status --queue --json
lilt favorites --json
lilt favorite add apple-music:song:1440845629
lilt history --json
lilt sources --json
```

For headless search-and-play, get a reference from `search --json`, then explicitly call `play <ref> --json`. CLI queue edits use the current queue index and do not accept `ifQueueRevision`: read `queue --json` immediately before editing, but concurrent clients can still change it. The Client API's `queue.*` operations support optional atomic `ifQueueRevision` checks. Do not pass shuffle/repeat to a source that does not advertise those capabilities; it returns `unsupported_command`. Live radio has no finite queue.

Logs are JSON lines in `~/.local/state/lilt/log/lilt.jsonl` by default (`LILT_LOG` overrides), rotating at 5 MB. `lilt log [n]` shows the most recent entries (50 by default). Standard logs omit raw searches and metadata titles; see [troubleshooting](troubleshooting.md) before sharing diagnostic logs.
