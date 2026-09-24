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
                 "LILT_AUDIUS_E2E")
    for key in forbidden:
        env.pop(key, None)
    env.pop("FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD", None)
    env["LILT_PLAYER_PATH"] = str(dest / "current/lilt-player.app")
    env["LILT_AUDIO_PATH"] = str(dest / "current/lilt-audio.app")
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
                raise ValueError("prerelease server uses another build or Apple engine; stop it explicitly before switching")
            try:
                os.kill(active["pid"], 0)
            except (OSError, KeyError):
                raise ValueError("prerelease server identity is stale; refusing to attach") from None
        else:
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


def stop_prerelease(dest, socket_path):
    binary = pinned_binary(dest)
    with workflow_lock(dest):
        if not reachable(socket_path):
            return
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
        subprocess.run([str(binary), "quit", "--json"], env=prerelease_env(dest), check=True)
        marker.unlink(missing_ok=True)


def fake_dev(root):
    binary = root / "lilt"
    if not binary.is_file():
        raise ValueError("missing development binary; run just build-go first")
    work = tempfile.mkdtemp(prefix="lilt-dev-fake-")
    print(f"fake session: {work} (socket: {work}/sock)", flush=True)
    env = os.environ.copy()
    for key in ("LILT_APPLE_ENGINE", "LILT_APPLE_PROFILE", "LILT_APPLE_E2E",
                "LILT_MPV_E2E", "LILT_AUDIUS_E2E"):
        env.pop(key, None)
    env.update({"LILT_SOCKET": f"{work}/sock", "LILT_STATE": f"{work}/state.json",
                "LILT_CONFIG": f"{work}/config", "LILT_RADIO_CACHE": f"{work}/radio.json",
                "LILT_LOG": f"{work}/log.jsonl", "LILT_FAKE_PLAYER": "1"})
    try:
        return subprocess.run([str(binary), "tui", "--fake"], env=env, check=False).returncode
    finally:
        subprocess.run([str(binary), "quit", "--json"], env=env, check=False,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if reachable(env["LILT_SOCKET"]):
            print(f"fake server did not stop; keeping private directory {work}", file=sys.stderr)
        else:
            shutil.rmtree(work)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("promote", "run", "run-browser", "tui", "stop", "check-idle", "fake", "cli", "reserve-real", "release-real"))
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
        elif args.action in ("run", "run-browser"):
            return run_prerelease(dest, path, "browser" if args.action == "run-browser" else "helper")
        elif args.action == "tui":
            if not reachable(path):
                raise ValueError("no prerelease server; run just run first")
            marker = json.loads((dest / "active.json").read_text())
            return run_prerelease(dest, path, marker["mode"])
        elif args.action == "cli":
            if not args.command:
                raise ValueError("cli needs a command")
            marker = dest / "active.json"
            mode = json.loads(marker.read_text())["mode"] if reachable(path) and marker.is_file() else "helper"
            binary, env = ensure_server(dest, path, mode)
            return subprocess.run([str(binary), *args.command], env=env, check=False).returncode
        elif args.action == "stop":
            stop_prerelease(dest, path)
        elif args.action == "check-idle":
            require_idle(path)
        elif args.action == "fake":
            return fake_dev(ROOT)
    except (OSError, ValueError, KeyError, json.JSONDecodeError, subprocess.CalledProcessError) as exc:
        print(f"local-workflow: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
