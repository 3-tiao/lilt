#!/usr/bin/env python3
"""Fail-closed audio limit for one isolated real usability round."""

import json
import socket
import sys
import time
import uuid
from pathlib import Path


def request(path, command):
    request_id = "usability-guard-" + uuid.uuid4().hex
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(2)
        conn.connect(str(path))
        conn.sendall((json.dumps({"requestId": request_id, "command": command}) + "\n").encode())
        with conn.makefile("rb") as stream:
            line = stream.readline(4 * 1024 * 1024 + 1)
    if not line or len(line) > 4 * 1024 * 1024 or not line.endswith(b"\n"):
        raise ValueError("missing or oversized response")
    response = json.loads(line)
    if not isinstance(response, dict) or response.get("requestId") != request_id:
        raise ValueError("mismatched requestId")
    if response.get("ok") is not True:
        raise ValueError("RPC rejected: " + str((response.get("error") or {}).get("code")))
    return response.get("data") or {}


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
            state = request(path, "session.status")
            status = state.get("status")
            if status not in ("playing", "paused", "buffering", "stopped", "ended", "error"):
                raise ValueError("unknown playback status")
        except (OSError, ValueError) as exc:
            record("probe_failed", error=type(exc).__name__)
            reason = "probe_failed"
            break
        now = clock()
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
        state = request(path, "playback.stop")
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
