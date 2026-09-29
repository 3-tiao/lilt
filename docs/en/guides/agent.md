# AI-agent integration

[English](agent.md) | [简体中文](../../guides/agent.md)

An agent controls lilt **only through the CLI**: it does not manipulate the TUI, call the server directly, or edit local state. It discovers commands, parameters, models, and stable errors with `lilt api --json` and checks live source capabilities with `lilt sources --json`.

The self-contained [music-control skill](../../../skills/music-control/SKILL.md) ships with the product. `just agent-install` installs it into a harness. The skill describes triggers and decision strategy, not a duplicate API specification.

## Rules for agents

- Pass shuffle/repeat only when the source advertises those capabilities; unsupported operations fail **before** playback rather than being ignored.
- Do not initiate an interactive authorization flow on the user's behalf. On `authorization_required`, ask the user to run `lilt auth <source> --json`; disconnect only on an explicit request.
- On `operation_outcome_unknown`, inspect state instead of replaying a mutation with a new request ID. Read the latest queue before any index-based edit; CLI has no `ifQueueRevision` option.
- Do not launch `lilt tui`; it is a full-screen human interface.
- Do not silently switch sources. Check the proposed source's capabilities and make the change explicit.
- Do not retry an unsupported shuffle/repeat request without that mode, or modify the queue to remove duplicates unless asked. A failed or ambiguous play requires a state check before another candidate; a ready queue is not proof of playback.
- Confirm each playback mutation with `lilt status --json`, then briefly tell the user what played and why it was selected. Report buffering or `unverified` honestly rather than claiming full playback.

When the user has not chosen a source, start from available sources with the required capability, in priority order. For full playback, the strategy prefers Apple Music → Audius → Jamendo → radio; a preview is **not** full playback. Within radio, prefer built-in stations before directory stations. Respect an explicitly requested source. The [Client API overview (Chinese)](../../client-api/README.md) owns the full source-selection rules.

`just skill-check` checks the published skill against the CLI's in-process catalog and key safety rules without contacting providers.
