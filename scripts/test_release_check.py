"""Hermetic release preflight and artifact-verification checks (no signing)."""

import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest

CHECK = Path(__file__).with_name("release-check.sh")


class ReleaseCheckTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lilt-release-check-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        DEVELOPER_ID_APPLICATION="Developer ID Application: Tester",
                        NOTARY_PROFILE="test-profile")
        self.shim("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo arm64; fi')
        self.git("init", "-q")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release@example.invalid")
        (self.repo / "tracked").write_text("clean")
        (self.repo / "cmd/lilt").mkdir(parents=True)
        (self.repo / "cmd/lilt/main.go").write_text('var version = "0.1.1"\n')
        self.git("add", "tracked", "cmd/lilt/main.go")
        self.git("commit", "-qm", "fixture")
        self.git("tag", "v0.1.1")

    def shim(self, name, body):
        executable = self.bin / name
        executable.write_text("#!/bin/sh\n" + body + "\n")
        executable.chmod(0o755)

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.repo, check=True, capture_output=True)

    def check(self, action="preflight"):
        return subprocess.run(["sh", str(CHECK), action, str(self.repo)], env=self.env,
                              capture_output=True, text=True)

    def test_preflight_accepts_only_a_clean_tagged_signed_arm64_build(self):
        self.assertEqual(self.check().returncode, 0)
        self.env["NOTARY_PROFILE"] = ""
        self.assertIn("NOTARY_PROFILE", self.check().stderr)
        self.env["NOTARY_PROFILE"] = "test-profile"
        self.env["LILT_BUILD_UNSIGNED"] = "1"
        self.assertIn("LILT_BUILD_UNSIGNED", self.check().stderr)
        self.env.pop("LILT_BUILD_UNSIGNED")
        self.shim("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo x86_64; fi')
        self.assertIn("arm64", self.check().stderr)
        self.shim("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo arm64; fi')
        (self.repo / "tracked").write_text("dirty")
        self.assertIn("clean", self.check().stderr)
        self.git("checkout", "--", "tracked")
        self.git("tag", "-d", "v0.1.1")
        self.git("tag", "v0.1.2")
        self.assertIn("version must match", self.check().stderr)
        self.git("tag", "-d", "v0.1.2")
        self.git("tag", "v0.1.1")
        self.git("commit", "--allow-empty", "-qm", "after tag")
        self.assertIn("release tag", self.check().stderr)

    def test_verification_rejects_wrong_architecture_and_unsigned_helpers(self):
        for name in ("lilt", "lilt-player", "lilt-audio"):
            executable = self.repo / name if name == "lilt" else (
                self.repo / f"player/Build/Products/Release/{name}.app/Contents/MacOS/{name}")
            executable.parent.mkdir(parents=True, exist_ok=True)
            executable.write_bytes(b"fixture")
        self.shim("lipo", "echo x86_64")
        self.assertIn("not arm64", self.check("verify").stderr)
        self.shim("lipo", "echo arm64")
        self.shim("codesign", 'if [ "$1" = -dv ]; then echo "Authority=Apple Development: Tester" >&2; fi')
        self.assertIn("not Developer ID signed", self.check("verify").stderr)
        self.shim("codesign", 'if [ "$1" = -dv ]; then echo "Authority=Developer ID Application: Tester" >&2; fi')
        self.shim("xcrun", "exit 1")
        self.assertIn("notary ticket missing", self.check("verify").stderr)
        self.shim("xcrun", "exit 0")
        self.shim("spctl", "exit 1")
        self.assertIn("Gatekeeper rejected", self.check("verify").stderr)
        self.shim("spctl", "exit 0")
        self.assertEqual(self.check("verify").returncode, 0)

    def test_player_bundle_is_declared_as_an_application(self):
        info = Path(__file__).parent.parent / "player/Resources/Info.plist"
        with info.open("rb") as source:
            metadata = plistlib.load(source)
        self.assertEqual(metadata["CFBundlePackageType"], "APPL")


if __name__ == "__main__":
    unittest.main()
