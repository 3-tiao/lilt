#!/usr/bin/env python3
"""Fail-closed audio limit for one isolated real usability round."""

import json
import socket
import sys
import time
import uuid
from pathlib import Path


def request(path, command, epoch=None):
    request_id = "usability-guard-" + uuid.uuid4().hex
    payload = {"requestId": request_id, "command": command}
    # A command with side effects must carry the server instance epoch: the
    # server rejects `playback.stop` without it, so a guard that omits it can
    # never stop audio and silently degrades to stop_unconfirmed. Every response
    # carries serverInstanceId, so learn it from the first probe and reuse it.
    if epoch:
        payload["ifServerInstanceId"] = epoch
    with socket.socket(socket.AF_UNIX) as conn:
        # A foreground MusicKit start serializes Client API commands. Allow a
        # bounded start to finish before treating the probe as unavailable.
        # A longer append/hung start is still an invalid round, not a
        # confirmed stop; keep the per-attempt wait below the audio window.
        conn.settimeout(8)
        conn.connect(str(path))
        conn.sendall((json.dumps(payload) + "\n").encode())
        with conn.makefile("rb") as stream:
            line = stream.readline(4 * 1024 * 1024 + 1)
    if not line or len(line) > 4 * 1024 * 1024 or not line.endswith(b"\n"):
        raise ValueError("missing or oversized response")
    response = json.loads(line)
    if not isinstance(response, dict) or response.get("requestId") != request_id:
        raise ValueError("mismatched requestId")
    if response.get("ok") is not True:
        raise ValueError("RPC rejected: " + str((response.get("error") or {}).get("code")))
    return response.get("data") or {}, response.get("serverInstanceId") or epoch


def run(directory, playing_limit=40, wait_limit=300, clock=time.monotonic, pause=time.sleep):
    path = directory / "session.sock"
    log = directory / "audio-guard.jsonl"

    def record(event, **fields):
        with log.open("a") as stream:
            stream.write(json.dumps({"event": event, **fields}) + "\n")

    start = clock()
    first_play = None
    last = start
    playing_total = 0.0
    epoch = None
    record("armed", playing_limit=playing_limit, wait_limit=wait_limit)
    while True:
        now = clock()
        if first_play is not None and (playing_total >= playing_limit or now-first_play >= 55):
            reason = "audio_limit"
            break
        if now-start >= wait_limit:
            reason = "watch_expired"
            break
        try:
            state, epoch = request(path, "session.status", epoch)
            status = state.get("status")
            if status not in ("playing", "paused", "buffering", "stopped", "ended", "error"):
                raise ValueError("unknown playback status")
        except (OSError, ValueError) as exc:
            record("probe_failed", error=type(exc).__name__)
            reason = "probe_failed"
            break
        now = clock()
        if status == "stopped" and first_play is None and (directory / "guard-finish-readonly").exists():
            record("read_only_finished")
            return 0
        if status == "stopped" and first_play is not None:
            record("participant_stopped", observed_playing_seconds=round(playing_total, 1))
            return 0
        if status == "playing":
            if first_play is None:
                first_play = now
                record("playing_observed")
            playing_total += max(0, now-last)
        last = now
        pause(0.5)

    # Even on probe failure, never inject a key into the participant's TUI.
    # A direct stop on this round's socket is idempotent and cannot start a server.
    try:
        state, _ = request(path, "playback.stop", epoch)
        if state.get("status") != "stopped":
            raise ValueError("stop not confirmed")
    except (OSError, ValueError) as exc:
        record("stop_unconfirmed", reason=reason, error=type(exc).__name__)
        return 1
    record("stopped", reason=reason, observed_playing_seconds=round(playing_total, 1))
    return 0 if reason == "audio_limit" else 1


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: audio-guard.py <private-round-dir>")
    sys.exit(run(Path(sys.argv[1])))
