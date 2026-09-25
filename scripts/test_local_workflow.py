"""Hermetic checks for daily-build pinning and fail-closed session routing."""

import importlib.util
import json
import os
from pathlib import Path
import socket
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("local_workflow", Path(__file__).with_name("local-workflow.py"))
workflow = importlib.util.module_from_spec(spec)
spec.loader.exec_module(workflow)

apple_spec = importlib.util.spec_from_file_location("check_apple_start", Path(__file__).with_name("check-apple-start.py"))
apple_check = importlib.util.module_from_spec(apple_spec)
apple_spec.loader.exec_module(apple_check)


class AppleStartCheckTest(unittest.TestCase):
    def test_real_check_requires_explicit_audio_consent(self):
        with patch.dict(os.environ, {"LILT_TEST_AUDIO": "0"}):
            with self.assertRaisesRegex(ValueError, "LILT_TEST_AUDIO=1"):
                apple_check.require_session()

    def test_real_check_tests_selected_song_and_stops_owned_playback(self):
        for title, succeeds in (("Selected", True), ("Different", False)):
            with self.subTest(title=title):
                states = iter(({"status": "stopped"}, {"status": "stopped"},
                               {"status": "playing", "queueIndex": 0, "track": {"title": title, "artist": "Artist"}},
                               {"status": "playing"}))
                called = []

                def fake_call(command, params=None, timeout=70):
                    called.append(command)
                    if command == "session.status":
                        return {"ok": True, "data": next(states)}
                    if command == "library.playlists":
                        return {"ok": True, "data": [{"ref": "apple-music:playlist:p"}]}
                    if command == "playlist.tracks":
                        return {"ok": True, "data": {"items": [{"providerId": "s", "kind": "song", "title": "Selected", "artist": "Artist"}]}}
                    if command == "playback.play":
                        self.assertEqual(params["startTrackID"], "s")
                        self.assertTrue(params["fromHere"])
                        return {"ok": True, "data": {}}
                    if command == "playback.stop":
                        return {"ok": True, "data": {"status": "stopped"}}
                    raise AssertionError(command)

                with patch.object(apple_check, "require_session"), patch.object(apple_check, "call", side_effect=fake_call):
                    if succeeds:
                        apple_check.check(0, 0, 1)
                    else:
                        with self.assertRaisesRegex(ValueError, "start check failed"):
                            apple_check.check(0, 0, 1)
                self.assertEqual(called.count("playback.play"), 1)
                self.assertEqual(called.count("playback.stop"), 1)


class WorkflowTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lilt-workflow-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        (self.repo / "lilt").write_bytes(b"candidate-A")
        self.dest = self.root / "prerelease"
        self.sock = self.root / "sock"

    def test_promotion_pins_binaries_and_helper_bundles(self):
        for name in ("lilt-player", "lilt-audio"):
            executable = self.repo / "player/Build/Products/Release" / f"{name}.app/Contents/MacOS/{name}"
            executable.parent.mkdir(parents=True, exist_ok=True)
            executable.write_bytes(name.encode())
        with patch.object(workflow.subprocess, "run"):
            pinned = workflow.promote(self.repo, self.dest, self.sock, True)
        self.assertEqual((pinned / "lilt").read_bytes(), b"candidate-A")
        self.assertEqual((pinned / "lilt-player.app/Contents/MacOS/lilt-player").read_bytes(), b"lilt-player")
        (self.repo / "lilt").write_bytes(b"candidate-B")
        self.assertEqual((self.dest / "current/lilt").read_bytes(), b"candidate-A")
        with patch.object(workflow.subprocess, "run"):
            newer = workflow.promote(self.repo, self.dest, self.sock, True)
        self.assertNotEqual(pinned, newer)
        self.assertEqual((pinned / "lilt").read_bytes(), b"candidate-A")
        self.assertEqual((self.dest / "current/lilt").read_bytes(), b"candidate-B")

    def test_promotion_never_switches_an_active_daily_server(self):
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with self.assertRaisesRegex(ValueError, "active"):
                workflow.promote(self.repo, self.dest, self.sock, False)
        self.assertFalse((self.dest / "current").exists())

    def test_engine_switch_gives_up_an_idle_helper_session(self):
        pinned = workflow.promote(self.repo, self.dest, self.sock, False)
        (self.dest / "active.json").write_text(json.dumps({
            "mode": "helper", "binary": str((pinned / "lilt").resolve()), "pid": 1,
        }))
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with patch.object(workflow, "daily_is_idle", return_value=True), \
                    patch.object(workflow, "_stop_prerelease") as stopped, \
                    patch.object(workflow.subprocess, "run", return_value=SimpleNamespace(
                        stdout=json.dumps({"data": {"pid": os.getpid()}}))):
                binary, env = workflow.ensure_server(self.dest, self.sock, "browser")
        stopped.assert_called_once()
        self.assertEqual(binary.resolve(), (pinned / "lilt").resolve())
        self.assertEqual(env["LILT_APPLE_ENGINE"], "browser")

    def test_engine_switch_refuses_while_daily_is_playing(self):
        workflow.promote(self.repo, self.dest, self.sock, False)
        (self.dest / "active.json").write_text(json.dumps({
            "mode": "helper", "binary": str((self.dest / "current/lilt").resolve()), "pid": 1,
        }))
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with patch.object(workflow, "daily_is_idle", return_value=False):
                with self.assertRaisesRegex(ValueError, "playing"):
                    workflow.ensure_server(self.dest, self.sock, "browser")

    def test_engine_switch_refuses_unconfirmable_daily_state(self):
        workflow.promote(self.repo, self.dest, self.sock, False)
        (self.dest / "active.json").write_text(json.dumps({
            "mode": "helper", "binary": str((self.dest / "current/lilt").resolve()), "pid": 1,
        }))
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with patch.object(workflow, "daily_is_idle", return_value=None):
                with self.assertRaisesRegex(ValueError, "cannot confirm"):
                    workflow.ensure_server(self.dest, self.sock, "browser")

    def test_daily_run_uses_pinned_binary_and_never_inherits_test_paths(self):
        pinned = workflow.promote(self.repo, self.dest, self.sock, False)
        with patch.dict(os.environ, {"LILT_SOCKET": "/tmp/test.sock", "LILT_FAKE_PLAYER": "1",
                                    "LILT_APPLE_PROFILE": "/tmp/test-profile", "LILT_APPLE_E2E": "1",
                                    "LILT_LOCAL_DEBUG": "0"}):
            with patch.object(workflow, "reachable", return_value=False), patch.object(
                    workflow.subprocess, "run", return_value=SimpleNamespace(
                        stdout=json.dumps({"data": {"pid": os.getpid()}}))) as runner:
                binary, env = workflow.ensure_server(self.dest, self.sock, "helper")
        self.assertEqual(binary.resolve(), (pinned / "lilt").resolve())
        self.assertEqual(env["LILT_PLAYER_PATH"], str(self.dest / "current/lilt-player.app"))
        self.assertEqual(env["LILT_LOCAL_DEBUG"], "1")
        for key in ("LILT_SOCKET", "LILT_FAKE_PLAYER", "LILT_APPLE_PROFILE", "LILT_APPLE_E2E"):
            self.assertNotIn(key, env)
        self.assertEqual(runner.call_args.args[0][1:], ["serve", "--detach", "--json"])
        self.assertEqual(json.loads((self.dest / "active.json").read_text())["pid"], os.getpid())

    def test_unknown_daily_server_is_not_stopped(self):
        workflow.promote(self.repo, self.dest, self.sock, False)
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with self.assertRaisesRegex(ValueError, "untracked"):
                workflow.stop_prerelease(self.dest, self.sock)

    def test_real_reservation_excludes_prerelease_and_other_real_tests(self):
        first = self.root / "real.sock"
        second = self.root / "other.sock"
        workflow.reserve_real(self.dest, self.sock, first)
        with self.assertRaisesRegex(ValueError, "reserved"):
            workflow.promote(self.repo, self.dest, self.sock, False)
        with self.assertRaisesRegex(ValueError, "reserved"):
            workflow.reserve_real(self.dest, self.sock, second)
        (self.dest / "current").symlink_to("candidate")
        (self.dest / "candidate").mkdir()
        (self.dest / "candidate/lilt").write_bytes(b"pinned")
        with self.assertRaisesRegex(ValueError, "reserved"):
            workflow.ensure_server(self.dest, self.sock, "helper")
        workflow.release_real(self.dest, second)  # Cannot release someone else's reservation.
        self.assertTrue((self.dest / "real-session.json").exists())
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(first))
            server.listen()
            with self.assertRaisesRegex(ValueError, "still active"):
                workflow.release_real(self.dest, first)
        workflow.release_real(self.dest, first)
        self.assertFalse((self.dest / "real-session.json").exists())

    def test_yield_daily_stops_only_a_confirmed_idle_server(self):
        workflow.promote(self.repo, self.dest, self.sock, False)
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(self.sock))
            server.listen()
            with patch.object(workflow, "daily_is_idle", return_value=False):
                with self.assertRaisesRegex(ValueError, "playing"):
                    workflow.yield_daily(self.dest, self.sock)
            with patch.object(workflow, "daily_is_idle", return_value=None):
                with self.assertRaisesRegex(ValueError, "cannot confirm"):
                    workflow.yield_daily(self.dest, self.sock)
            with patch.object(workflow, "daily_is_idle", return_value=True), \
                    patch.object(workflow, "_stop_prerelease") as stopped:
                self.assertTrue(workflow.yield_daily(self.dest, self.sock))
                stopped.assert_called_once()

    def test_yield_daily_is_a_noop_without_a_daily_server(self):
        self.assertFalse(workflow.yield_daily(self.dest, self.sock))


if __name__ == "__main__":
    unittest.main()
