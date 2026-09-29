"""Hermetic safety-stop tests; no actual audio or lilt server."""

import importlib.util
import json
import socket
import tempfile
import threading
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "audio_guard", ROOT / ".agents/skills/usability-test/scripts/audio-guard.py"
)
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


class AudioGuardTest(unittest.TestCase):
    def run_guard(self, statuses, fail_status=False, fail_stop=False):
        with tempfile.TemporaryDirectory(prefix="lilt-guard-test-") as temp:
            directory = Path(temp)
            calls = []
            with socket.socket(socket.AF_UNIX) as listener:
                listener.bind(str(directory / "session.sock"))
                listener.listen(5)

                def serve():
                    saw_playing = False
                    while True:
                        connection, _ = listener.accept()
                        with connection:
                            with connection.makefile("rb") as stream:
                                req = json.loads(stream.readline())
                            calls.append(req)
                            if req["command"] == "playback.stop" and fail_stop:
                                connection.sendall(b"invalid response\n")
                                break
                            if req["command"] == "playback.stop":
                                status = "stopped"
                            elif fail_status:
                                connection.sendall(b"invalid response\n")
                                continue
                            else:
                                status = statuses[min(len(calls)-1, len(statuses)-1)]
                                saw_playing |= status == "playing"
                            response = {"ok": True, "requestId": req["requestId"],
                                        "data": {"status": status}}
                            connection.sendall((json.dumps(response) + "\n").encode())
                        if req["command"] == "playback.stop" or (
                            not fail_status and status == "stopped" and saw_playing
                        ):
                            break

                thread = threading.Thread(target=serve, daemon=True)
                thread.start()
                elapsed = iter(range(100))
                result = guard.run(directory, playing_limit=3, wait_limit=30,
                                   clock=lambda: next(elapsed), pause=lambda _: None)
                thread.join(timeout=2)
                self.assertFalse(thread.is_alive())
            events = [json.loads(line) for line in (directory / "audio-guard.jsonl").read_text().splitlines()]
            return result, calls, events

    def test_playing_limit_stops_directly_without_tui_key(self):
        result, calls, events = self.run_guard(["stopped", "playing", "playing", "playing"])
        self.assertEqual(result, 0)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(len({call["requestId"] for call in calls}), len(calls))
        self.assertEqual(events[-1]["reason"], "audio_limit")

    def test_probe_failure_stops_and_invalidates_round(self):
        result, calls, events = self.run_guard([], fail_status=True)
        self.assertEqual(result, 1)
        self.assertEqual([call["command"] for call in calls],
                         ["session.status", "playback.stop"])
        self.assertEqual(events[-1]["reason"], "probe_failed")

    def test_no_playback_watch_expiry_stops_and_invalidates_round(self):
        result, calls, events = self.run_guard(["stopped"])
        self.assertEqual(result, 1)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(events[-1]["reason"], "watch_expired")

    def test_participant_stop_finishes_without_extra_mutation(self):
        result, calls, events = self.run_guard(["playing", "stopped"])
        self.assertEqual(result, 0)
        self.assertEqual([call["command"] for call in calls],
                         ["session.status", "session.status"])
        self.assertEqual(events[-1]["event"], "participant_stopped")

    def test_unconfirmed_stop_is_reported_as_failure(self):
        result, calls, events = self.run_guard([], fail_status=True, fail_stop=True)
        self.assertEqual(result, 1)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(events[-1]["event"], "stop_unconfirmed")


if __name__ == "__main__":
    unittest.main()
