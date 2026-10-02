"""Hermetic safety-stop tests; no actual audio or lilt server."""

import importlib.util
import itertools
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
    # Mirrors the server: every response carries the epoch, and a command with
    # side effects is rejected without it. A fake that accepts anything would
    # let the guard lose its only safety net unnoticed.
    EPOCH = "epoch-under-test"

    def run_guard(self, statuses, fail_status=False, fail_stop=False, finish_readonly=False):
        with tempfile.TemporaryDirectory(prefix="lilt-guard-test-") as temp:
            directory = Path(temp)
            if finish_readonly:
                (directory / "guard-finish-readonly").touch()
            calls = []
            with socket.socket(socket.AF_UNIX) as listener:
                listener.bind(str(directory / "session.sock"))
                listener.listen(5)
                # The fake's lifetime follows the guard's: it answers until the
                # guard stops probing. Guessing when to disconnect from the
                # status list would cut the guard off before it could confirm a
                # sustained stop, which is the behavior under test.
                listener.settimeout(0.3)

                def serve():
                    while True:
                        try:
                            connection, _ = listener.accept()
                        except (socket.timeout, TimeoutError):
                            return
                        with connection:
                            with connection.makefile("rb") as stream:
                                req = json.loads(stream.readline())
                            calls.append(req)
                            if req["command"] == "playback.stop" and fail_stop:
                                connection.sendall(b"invalid response\n")
                                continue
                            if req["command"] != "session.status" and req.get("ifServerInstanceId") != self.EPOCH:
                                connection.sendall((json.dumps(
                                    {"ok": False, "requestId": req["requestId"],
                                     "serverInstanceId": self.EPOCH,
                                     "error": {"code": "invalid_request"}}
                                ) + "\n").encode())
                                continue
                            if req["command"] == "playback.stop":
                                status = "stopped"
                            elif fail_status:
                                connection.sendall(b"invalid response\n")
                                continue
                            else:
                                status = statuses[min(len(calls)-1, len(statuses)-1)]
                            # A status entry may be a full state object with queue
                            # context, exactly what includeQueue returns.
                            if isinstance(status, dict):
                                data = status
                            else:
                                data = {"status": status}
                            response = {"ok": True, "requestId": req["requestId"],
                                        "serverInstanceId": self.EPOCH,
                                        "data": data}
                            connection.sendall((json.dumps(response) + "\n").encode())

                thread = threading.Thread(target=serve, daemon=True)
                thread.start()
                elapsed = itertools.count()
                result = guard.run(directory, playing_limit=3, wait_limit=30,
                                   clock=lambda: next(elapsed), pause=lambda _: None)
                thread.join(timeout=3)
                self.assertFalse(thread.is_alive())
            events = [json.loads(line) for line in (directory / "audio-guard.jsonl").read_text().splitlines()]
            return result, calls, events

    def test_playing_limit_stops_directly_without_tui_key(self):
        result, calls, events = self.run_guard(["stopped", "playing", "playing", "playing"])
        self.assertEqual(result, 0)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(len({call["requestId"] for call in calls}), len(calls))
        self.assertEqual(events[-1]["reason"], "audio_limit")

    def test_stop_carries_the_server_instance_epoch(self):
        # The server rejects a side-effecting command without ifServerInstanceId,
        # so an epoch-less stop would leave audio running while the guard
        # reported a failure nobody hears about.
        result, calls, events = self.run_guard(["stopped", "playing", "playing", "playing"])
        self.assertEqual(result, 0)
        self.assertEqual(calls[-1]["ifServerInstanceId"], self.EPOCH)
        self.assertEqual(events[-1]["event"], "stopped")

    def test_every_probe_response_refreshes_the_epoch(self):
        _, calls, _ = self.run_guard(["stopped", "playing", "playing", "playing"])
        # The first probe cannot carry an epoch it has not seen yet; every later
        # call reuses the one the server reported.
        self.assertNotIn("ifServerInstanceId", calls[0])
        self.assertTrue(all(call.get("ifServerInstanceId") == self.EPOCH for call in calls[1:]))

    def test_a_momentary_stop_does_not_end_the_watch(self):
        # A source switch or a retry passes through `stopped` and then plays
        # again. The guard used to exit on that one sample, leaving the music
        # that followed completely unwatched while it reported success.
        # Only one playing sample fits before the audio limit trips, so the
        # transient stop has to come immediately after it.
        result, calls, events = self.run_guard(
            ["stopped", "playing", "stopped", "playing", "playing", "playing"]
        )
        self.assertNotIn("participant_stopped", [event["event"] for event in events])
        self.assertEqual(events[-1]["event"], "stopped")
        self.assertEqual(events[-1]["reason"], "audio_limit")
        self.assertEqual(calls[-1]["command"], "playback.stop")

    def test_a_track_gap_with_queue_remaining_keeps_watching(self):
        # A stopped sample whose queue still has items after the playing index
        # is a track boundary or a retry gap, however long it lingers. Timing
        # the stopped state cannot tell it from a queue that ran out, so the
        # guard must read the queue (session.status includeQueue) instead.
        gap = {"status": "stopped", "queue": [{"ref": "a"}, {"ref": "b"}, {"ref": "c"}],
               "queueIndex": 0}
        result, calls, events = self.run_guard(
            ["playing"] + [gap] * 4 + ["playing", "playing", "playing"]
        )
        self.assertNotIn("participant_stopped", [event["event"] for event in events])
        self.assertEqual(events[-1]["event"], "stopped")
        self.assertEqual(events[-1]["reason"], "audio_limit")
        self.assertEqual(calls[-1]["command"], "playback.stop")

    def test_a_drained_queue_stop_finishes_the_round(self):
        # The queue that actually ran out is empty at the stopped index: that is
        # a real ending, confirmed twice before the round closes.
        drained = {"status": "stopped", "queue": [], "queueIndex": -1}
        result, calls, events = self.run_guard(["playing", drained, drained])
        self.assertEqual(result, 0)
        self.assertEqual(events[-1]["event"], "participant_stopped")

    def test_probes_request_queue_context(self):
        # The gap/ending decision reads queue state, so every status probe must
        # ask for it: without includeQueue the server omits the queue and every
        # stopped sample would look drained.
        _, calls, _ = self.run_guard(["playing", "stopped", "stopped"])
        self.assertTrue(all(call.get("params") == {"includeQueue": True}
                            for call in calls if call["command"] == "session.status"))

    def test_a_sustained_stop_still_finishes_the_round(self):
        # One playing sample, then a stop that holds: two consecutive stopped
        # samples end the round, with no extra mutation.
        result, calls, events = self.run_guard(["stopped", "playing", "stopped", "stopped"])
        self.assertEqual(result, 0)
        self.assertEqual([call["command"] for call in calls],
                         ["session.status"] * 4)
        self.assertEqual(events[-1]["event"], "participant_stopped")

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
        # A sustained stop now takes two consecutive samples before the round is
        # considered over, so the command list has one more read.
        result, calls, events = self.run_guard(["playing", "stopped"])
        self.assertEqual(result, 0)
        self.assertEqual([call["command"] for call in calls],
                         ["session.status"] * 3)
        self.assertEqual(events[-1]["event"], "participant_stopped")

    def test_read_only_finish_requires_stopped_and_never_mutates(self):
        result, calls, events = self.run_guard(["stopped"], finish_readonly=True)
        self.assertEqual(result, 0)
        self.assertEqual([call["command"] for call in calls], ["session.status"])
        self.assertEqual(events[-1]["event"], "read_only_finished")

    def test_read_only_marker_does_not_bypass_playing_safety(self):
        result, calls, events = self.run_guard(["playing", "playing", "playing"], finish_readonly=True)
        self.assertEqual(result, 0)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(events[-1]["reason"], "audio_limit")

    def test_unconfirmed_stop_is_reported_as_failure(self):
        result, calls, events = self.run_guard([], fail_status=True, fail_stop=True)
        self.assertEqual(result, 1)
        self.assertEqual(calls[-1]["command"], "playback.stop")
        self.assertEqual(events[-1]["event"], "stop_unconfirmed")


if __name__ == "__main__":
    unittest.main()
