"""Hermetic guards for usability-test's private, read-only probes and reports."""

import json
import shutil
import socket
import subprocess
import tempfile
import threading
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ROUND = ROOT / ".agents/skills/usability-test/scripts/round.sh"


class UsabilityRoundTest(unittest.TestCase):
    def setUp(self):
        # round.sh deliberately owns /tmp/lilt-round-<name>; use a unique name
        # and remove only this test's private directory.
        self.directory = Path(tempfile.mkdtemp(prefix="lilt-round-check_", dir="/tmp"))
        self.name = self.directory.name.removeprefix("lilt-round-")

    def tearDown(self):
        shutil.rmtree(self.directory)

    def run_round(self, *args):
        return subprocess.run(
            [str(ROUND), *args], cwd=ROOT, capture_output=True, text=True, timeout=10
        )

    def test_probe_never_starts_missing_server(self):
        result = self.run_round("probe", self.name, "status")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("未 ready", result.stderr)
        (self.directory / "ready").touch()
        result = self.run_round("probe", self.name, "status")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no server was started", result.stderr)
        self.assertFalse((self.directory / "session.sock").exists())

    def test_probe_allows_only_read_only_commands_on_own_socket(self):
        (self.directory / "ready").touch()
        requests = []
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(str(self.directory / "session.sock"))
            listener.listen(3)

            def answer():
                for _ in range(3):
                    conn, _ = listener.accept()
                    with conn:
                        with conn.makefile("rb") as stream:
                            request = json.loads(stream.readline())
                        requests.append(request)
                        response = {"ok": True, "requestId": request["requestId"], "data": []}
                        conn.sendall((json.dumps(response) + "\n").encode())

            thread = threading.Thread(target=answer, daemon=True)
            thread.start()
            refused = self.run_round("probe", self.name, "session.shutdown")
            self.assertEqual(refused.returncode, 2)
            self.assertIn("仅支持", refused.stderr)
            self.assertEqual(requests, [])
            for method in ("status", "sources", "auth"):
                result = self.run_round("probe", self.name, method)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)["data"], [])
            thread.join(timeout=5)
        self.assertEqual([r["command"] for r in requests],
                         ["session.status", "sources.list", "authorization.list"])
        ids = [r["requestId"] for r in requests]
        self.assertEqual(len(set(ids)), 3, "different probes must not reuse a requestId")
        self.assertTrue(all(value.startswith("usability-probe-") for value in ids))

    def test_report_requires_prompt_facts_and_exact_keys(self):
        keys = "2026-09-29T00:00:00Z s Enter\n2026-09-29T00:00:01Z Escape\n"
        (self.directory / "keys.log").write_text(keys)
        evidence = self.run_round("evidence", self.name)
        self.assertEqual(evidence.returncode, 0, evidence.stderr)
        report = self.directory / "report.md"
        report.write_text(
            "# Round\n## 参与者 prompt\nFind a source.\n\n"
            "## 屏幕事实与复核\nSource chooser opened; no real audio.\n\n"
            + evidence.stdout
        )
        self.assertEqual(self.run_round("report-check", self.name, str(report)).returncode, 0)
        report.write_text(report.read_text().replace("s Enter", "s"))
        failed = self.run_round("report-check", self.name, str(report))
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn("键序", failed.stderr)
        report.write_text(evidence.stdout)
        self.assertIn("缺 prompt", self.run_round("report-check", self.name, str(report)).stderr)
        report.write_text(
            "## 参与者 prompt\nFind a source.\n## 屏幕事实与复核\n\n" + evidence.stdout
        )
        self.assertIn("缺 prompt", self.run_round("report-check", self.name, str(report)).stderr)

    def test_probe_report_can_have_no_keys(self):
        (self.directory / "keys.log").touch()
        evidence = self.run_round("evidence", self.name)
        report = self.directory / "probe.md"
        report.write_text(
            "## 探针目标与命令\nRead status via round.sh probe.\n"
            "## 屏幕事实与复核\nSocket unavailable; no claim.\n" + evidence.stdout
        )
        result = self.run_round("report-check", self.name, str(report))
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
