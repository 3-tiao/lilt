#!/usr/bin/env python3
"""Opt-in audible playlist-from check against the already running daily prerelease.

Requires LILT_TEST_AUDIO=1 and an exclusive playback window. Never starts or
restarts a server. Prints no track metadata; the private just-run journal has
startDiagnostic and (on failure) the expected/actual identity fields.
"""

import argparse
import json
import os
from pathlib import Path
import socket
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
SOCKET = Path.home() / "Library/Caches/lilt/session.sock" if sys.platform == "darwin" else Path.home() / ".cache/lilt/session.sock"


def require_session():
    if os.getenv("LILT_TEST_AUDIO") != "1":
        raise ValueError("audible Apple Music check requires LILT_TEST_AUDIO=1 and an exclusive window")
    if sys.platform != "darwin":
        raise ValueError("the signed Apple Music helper is macOS-only")
    dest = ROOT / ".lilt-prerelease"
    active = json.loads((dest / "active.json").read_text())
    binary = str((dest / "current/lilt").resolve())
    if active.get("mode") != "helper" or active.get("binary") != binary:
        raise ValueError("daily server is not the currently pinned Apple helper build")
    try:
        os.kill(active["pid"], 0)
    except OSError as exc:
        raise ValueError("recorded daily server is no longer running") from exc
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(2)
        conn.connect(str(SOCKET))
    # No implicit server startup even when the socket disappears afterwards.
    return active


def call(command, params=None, timeout=70):
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(timeout)
        conn.connect(str(SOCKET))
        conn.sendall((json.dumps({"requestId": str(uuid.uuid4()), "command": command,
                                  "params": params or {}}) + "\n").encode())
        line = conn.makefile("rb").readline()
        if not line:
            raise OSError("daily server closed without a response; outcome unknown, do not retry")
        return json.loads(line)


def data(response, command):
    if not response.get("ok"):
        raise ValueError(f"{command} failed: {response.get('error', {}).get('code', 'unknown')}")
    return response["data"]


def check(playlist_index, song_index, runs):
    require_session()
    before = data(call("session.status", {"includeQueue": True}, 8), "session.status")
    if before.get("status") != "stopped":
        raise ValueError("daily playback is not stopped; abort without interrupting it")
    playlists = data(call("library.playlists", {"source": "apple-music"}), "library.playlists")
    if not 0 <= playlist_index < len(playlists):
        raise ValueError("playlist index is outside the current library")
    playlist = playlists[playlist_index]
    items = data(call("playlist.tracks", {"ref": playlist["ref"]}), "playlist.tracks")["items"]
    if not 0 <= song_index < len(items) or items[song_index].get("kind") != "song":
        raise ValueError("song index is not a playable song in this playlist")
    selected = items[song_index]
    for run in range(1, runs + 1):
        current = data(call("session.status", {"includeQueue": True}, 8), "session.status")
        if current.get("status") != "stopped":
            raise ValueError("another client started playback; abort without replacing it")
        started = False
        try:
            started = True
            began = time.monotonic()
            response = call("playback.play", {"ref": playlist["ref"], "startAt": song_index,
                                              "startTrackID": selected["providerId"], "fromHere": True,
                                              "shuffle": False, "repeat": "off"}, 80)
            elapsed = round(time.monotonic() - began, 2)
            state = data(call("session.status", {"includeQueue": True}, 8), "session.status")
            if response.get("ok"):
                # A successful RPC must have landed on the requested song.
                track = state.get("track") or {}
                correct = track.get("title") == selected.get("title") and track.get("artist") == selected.get("artist")
                good = state.get("status") == "playing" and state.get("queueIndex") == 0 and correct
                print(f"run {run}: ok={good} rpc=success elapsed={elapsed}s status={state.get('status')} queue={len(state.get('queue') or [])} expectedMetadata={correct}", flush=True)
            else:
                code = response.get("error", {}).get("code", "unknown")
                good = False
                print(f"run {run}: ok=False rpc={code} elapsed={elapsed}s status={state.get('status')} queue={len(state.get('queue') or [])}", flush=True)
            if not good:
                raise ValueError("start check failed; inspect this run's private rpc journal before changing the confirmation rule")
        finally:
            if started:
                # Test owns this exclusive playback window. Stop even if the
                # response was lost; never auto-retry an unknown play result.
                try:
                    state = data(call("session.status", {"includeQueue": True}, 8), "session.status")
                    if state.get("status") in ("playing", "buffering", "paused"):
                        data(call("playback.stop", {}, 12), "playback.stop")
                except (OSError, ValueError) as exc:
                    print(f"cleanup requires attention: {exc}", file=sys.stderr)
                    raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--playlist-index", type=int, required=True)
    parser.add_argument("--song-index", type=int, required=True)
    parser.add_argument("--runs", type=int, default=1, choices=(1, 2))
    args = parser.parse_args()
    try:
        check(args.playlist_index, args.song_index, args.runs)
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as exc:
        print(f"apple start check: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
