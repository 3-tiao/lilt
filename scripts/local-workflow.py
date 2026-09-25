#!/usr/bin/env python3
"""Local prerelease and silent-development entry points. Never kill unrelated servers."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def default_socket():
    if sys.platform == "darwin":
        cache = Path.home() / "Library/Caches"
    else:
        cache = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
    return cache / "lilt/session.sock"


def reachable(path):
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(0.3)
        try:
            conn.connect(str(path))
            return True
        except OSError:
            return False


def daily_is_idle(dest, socket_path):
    """Report whether the daily prerelease server is safe to stop.

    True is idle (stopped/none), False is actively playing or paused, and None
    means the state could not be confirmed. Callers must treat None as "do not
    stop": a real session must never interrupt playback it cannot rule out.
    """
    binary = pinned_binary(dest)
    try:
        result = subprocess.run([str(binary), "status", "--json"], env=prerelease_env(dest),
                                capture_output=True, text=True, timeout=8, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout).get("data") or {}
    except (json.JSONDecodeError, AttributeError, TypeError):
        return None
    return data.get("status", "") in ("", "stopped", "none")


def yield_daily(dest, socket_path):
    """Give up the daily server to a private real session or an engine switch.

    Stops it only when it is confirmed idle; a playing/paused server or an
    unconfirmable state refuses, so this can never cut off the user's audio.
    """
    if not reachable(socket_path):
        return False
    state = daily_is_idle(dest, socket_path)
    if state is None:
        raise ValueError(f"cannot confirm the prerelease server at {socket_path} is idle; run just stop-daily first")
    if not state:
        raise ValueError(f"prerelease server at {socket_path} is playing; run just stop-daily before a real session")
    _stop_prerelease(dest, socket_path)
    return True


def require_idle(path):
    if reachable(path):
        raise ValueError(f"prerelease server is active at {path}; do not interrupt it")


@contextmanager
def workflow_lock(dest):
    dest.mkdir(parents=True, exist_ok=True)
    with (dest / "workflow.lock").open("a+") as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(stream, fcntl.LOCK_UN)


def require_no_real_session(dest):
    if (dest / "real-session.json").exists():
        raise ValueError("an approved real-test session is reserved; do not start prerelease playback")


def reserve_real(dest, daily_socket, test_socket):
    if Path(test_socket).resolve() == Path(daily_socket).resolve():
        raise ValueError("real test cannot use the daily socket")
    with workflow_lock(dest):
        require_idle(daily_socket)
        require_no_real_session(dest)
        (dest / "real-session.json").write_text(json.dumps({"socket": str(Path(test_socket).resolve())}) + "\n")


def release_real(dest, test_socket):
    with workflow_lock(dest):
        marker = dest / "real-session.json"
        if marker.exists():
            if json.loads(marker.read_text())["socket"] != str(Path(test_socket).resolve()):
                return  # Stopping an unrelated fake session cannot release a real one.
            if reachable(test_socket):
                raise ValueError("real test server is still active; stop it before releasing the reservation")
            marker.unlink()


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def promote(root, dest, socket_path, mac):
    require_idle(socket_path)
    binary = root / "lilt"
    if not binary.is_file():
        raise ValueError("missing development binary; run just build first")
    apps = ("lilt-player.app", "lilt-audio.app") if mac else ()
    source = root / "player/Build/Products/Release"
    for name in apps:
        if not (source / name).is_dir():
            raise ValueError(f"missing signed helper {name}; run just build first")
    dest.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".staging-", dir=dest))
    try:
        shutil.copy2(binary, staging / "lilt")
        for name in apps:
            shutil.copytree(source / name, staging / name, symlinks=True)
            subprocess.run(["codesign", "--verify", "--deep", str(staging / name)], check=True,
                           stdout=subprocess.DEVNULL)
        hashes = {"lilt": digest(staging / "lilt")}
        for name in apps:
            hashes[name] = digest(staging / name / "Contents/MacOS" / name.removesuffix(".app"))
        (staging / "manifest.json").write_text(json.dumps(hashes, indent=2) + "\n")
        # Keep every published version immutable. Never overwrite a helper bundle
        # that LaunchServices may still be using.
        key = hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()[:16]
        pinned = dest / key
        if pinned.exists():
            shutil.rmtree(staging)
        else:
            staging.rename(pinned)
        with workflow_lock(dest):
            require_idle(socket_path)  # A server may have started while copying.
            require_no_real_session(dest)
            link = dest / "current.next"
            if link.exists() or link.is_symlink():
                link.unlink()
            link.symlink_to(key)
            link.replace(dest / "current")
        return pinned
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def pinned_binary(dest):
    binary = dest / "current/lilt"
    if not binary.is_file():
        raise ValueError("no prerelease build; run just promote first")
    return binary


def prerelease_env(dest):
    env = os.environ.copy()
    # A test-pane environment must never redirect the daily instance to its
    # private socket, state, fake engine, or a provider E2E setting.
    forbidden = ("LILT_SOCKET", "LILT_STATE", "LILT_ACTIVITY_DB", "LILT_CONFIG",
                 "LILT_RADIO_CACHE", "LILT_LOG", "LILT_FAKE_PLAYER",
                 "LILT_APPLE_PROFILE", "LILT_APPLE_E2E", "LILT_MPV_E2E",
                 "LILT_AUDIUS_E2E", "LILT_LOCAL_DEBUG")
    for key in forbidden:
        env.pop(key, None)
    env.pop("FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD", None)
    env["LILT_PLAYER_PATH"] = str(dest / "current/lilt-player.app")
    env["LILT_AUDIO_PATH"] = str(dest / "current/lilt-audio.app")
    # Daily prerelease is the local debug entry point, not a public release.
    # The server journals private start identity fields only in this mode.
    env["LILT_LOCAL_DEBUG"] = "1"
    return env


def ensure_server(dest, socket_path, requested):
    binary = pinned_binary(dest)
    mode = requested if sys.platform == "darwin" else "browser"
    marker = dest / "active.json"
    env = prerelease_env(dest)
    env["LILT_APPLE_ENGINE"] = mode
    with workflow_lock(dest):
        require_no_real_session(dest)
        if reachable(socket_path):
            if not marker.is_file():
                raise ValueError("an untracked server owns the daily socket; refusing to attach")
            active = json.loads(marker.read_text())
            if active.get("mode") != mode or active.get("binary") != str(binary.resolve()):
                # Switching build/engine is allowed only by giving up an idle
                # daily server; yield_daily refuses while it is playing.
                yield_daily(dest, socket_path)
            else:
                try:
                    os.kill(active["pid"], 0)
                except (OSError, KeyError):
                    raise ValueError("prerelease server identity is stale; refusing to attach") from None
                return binary, env
        # Start explicitly: a TUI attaching to an unknown server must not
        # silently select its mode or launch a development build instead.
        out = subprocess.run([str(binary), "serve", "--detach", "--json"], env=env,
                             check=True, capture_output=True, text=True)
        pid = json.loads(out.stdout)["data"]["pid"]
        marker.write_text(json.dumps({"mode": mode, "binary": str(binary.resolve()), "pid": pid}) + "\n")
    return binary, env


def run_prerelease(dest, socket_path, requested):
    binary, env = ensure_server(dest, socket_path, requested)
    return subprocess.run([str(binary), "tui"], env=env, check=False).returncode


def _stop_prerelease(dest, socket_path):
    if not reachable(socket_path):
        return
    binary = pinned_binary(dest)
    marker = dest / "active.json"
    if not marker.is_file():
        raise ValueError("untracked server on daily socket; refusing to stop it")
    active = json.loads(marker.read_text())
    if active.get("binary") != str(binary.resolve()):
        raise ValueError("prerelease server identity does not match pinned build; refusing to stop it")
    try:
        os.kill(active["pid"], 0)
    except (OSError, KeyError):
        raise ValueError("prerelease server identity is stale; refusing to stop it") from None
    result = subprocess.run([str(binary), "quit", "--json"], env=prerelease_env(dest),
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    marker.unlink(missing_ok=True)


def stop_prerelease(dest, socket_path):
    pinned_binary(dest)
    with workflow_lock(dest):
        _stop_prerelease(dest, socket_path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("promote", "run", "stop", "yield", "cli", "reserve-real", "release-real"))
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    dest = ROOT / ".lilt-prerelease"
    path = default_socket()
    try:
        if args.action in ("reserve-real", "release-real"):
            if len(args.command) != 1:
                raise ValueError(f"{args.action} needs one private socket path")
            if args.action == "reserve-real":
                reserve_real(dest, path, args.command[0])
            else:
                release_real(dest, args.command[0])
        elif args.action == "promote":
            print(f"prerelease pinned at {promote(ROOT, dest, path, sys.platform == 'darwin')}")
        elif args.action == "run":
            mode = "browser" if args.command[:1] == ["browser"] else "helper"
            return run_prerelease(dest, path, mode)
        elif args.action == "cli":
            if not args.command:
                raise ValueError("cli needs a command")
            marker = dest / "active.json"
            mode = json.loads(marker.read_text())["mode"] if reachable(path) and marker.is_file() else "helper"
            binary, env = ensure_server(dest, path, mode)
            return subprocess.run([str(binary), *args.command], env=env, check=False).returncode
        elif args.action == "stop":
            stop_prerelease(dest, path)
        elif args.action == "yield":
            yield_daily(dest, path)
    except (OSError, ValueError, KeyError, json.JSONDecodeError, subprocess.CalledProcessError) as exc:
        print(f"local-workflow: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
