"""Hermetic macOS installer tests; curl serves only local fixture bytes."""

import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


INSTALL = Path(__file__).with_name("install.sh")


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lilt-install-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home"
        self.home.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.archive = self.root / "release.tar.gz"
        self.make_archive()
        self.env = dict(os.environ, HOME=str(self.home), TMPDIR=str(self.root),
                        PATH=f"{self.bin}:{os.environ['PATH']}",
                        FIXTURE_TAR=str(self.archive))
        self.shim("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo arm64; fi')
        self.shim("sw_vers", 'echo 14.5')
        self.shim("codesign", '[ -z "${FAIL_CODESIGN:-}" ]')
        self.shim("spctl", '[ -z "${FAIL_SPCTL:-}" ]')
        self.shim("curl", '''#!/usr/bin/env python3
import hashlib, json, os, pathlib, sys
args = sys.argv[1:]
url = args[-1]
if 'api.github.com' in url:
    if os.environ.get('NO_RELEASE'):
        sys.exit(22)
    print(json.dumps({'tag_name': 'v1.0.1'}))
elif 'lilt-v1.0.1-darwin-arm64.tar.gz' in url:
    archive = pathlib.Path(os.environ['FIXTURE_TAR'])
    if url.endswith('.sha256'):
        checksum = '0' * 64 if os.environ.get('BAD_SHA') else hashlib.sha256(archive.read_bytes()).hexdigest()
        pathlib.Path(args[args.index('-o') + 1]).write_text(checksum + '  lilt-v1.0.1-darwin-arm64.tar.gz\\n')
    else:
        pathlib.Path(args[args.index('-o') + 1]).write_bytes(archive.read_bytes())
else:
    sys.exit(22)
''')

    def shim(self, name, body):
        path = self.bin / name
        path.write_text(body if body.startswith("#!") else "#!/bin/sh\n" + body + "\n")
        path.chmod(0o755)

    def make_archive(self, malicious=False, missing_helper=False, missing_license=False):
        entries = {
            "lilt": b'#!/bin/sh\nprintf "lilt 1.0.1\\n"\nprintf "%s|%s\\n" "$LILT_PLAYER_PATH" "$LILT_AUDIO_PATH"\n',
            "lilt-player.app/Contents/MacOS/lilt-player": b"player",
            "skills/music-control/SKILL.md": b"fixture",
        }
        if not missing_license:
            entries["LICENSE"] = b"MIT License\n"
        if not missing_helper:
            entries["lilt-audio.app/Contents/MacOS/lilt-audio"] = b"audio"
        if malicious:
            entries["../outside"] = b"must not escape"
        with tarfile.open(self.archive, "w:gz") as archive:
            for name, data in entries.items():
                info = tarfile.TarInfo(name)
                info.size = len(data)
                info.mode = 0o755 if name == "lilt" else 0o644
                archive.addfile(info, io.BytesIO(data))

    def install(self, **extra_env):
        return subprocess.run(["sh", str(INSTALL)], env=dict(self.env, **extra_env),
                              capture_output=True, text=True)

    def test_installs_and_launches_with_both_helper_paths(self):
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        launcher = self.home / ".local/bin/lilt"
        self.assertTrue(launcher.is_symlink())
        run = subprocess.run([str(launcher), "version"], capture_output=True, text=True)
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertIn("lilt 1.0.1", run.stdout)
        self.assertIn("v1.0.1/lilt-player.app|", run.stdout)
        self.assertIn("v1.0.1/lilt-audio.app", run.stdout)
        self.assertEqual((self.home / ".local/share/lilt/v1.0.1/LICENSE").read_text(), "MIT License\n")
        self.assertIn("Add", result.stdout)
        self.assertIn("already installed", self.install().stderr)

    def test_checksum_failure_never_installs(self):
        result = self.install(BAD_SHA="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SHA-256 mismatch", result.stderr)
        self.assertFalse((self.home / ".local/bin/lilt").exists())

    def test_invalid_signature_or_gatekeeper_rejection_never_installs(self):
        self.assertIn("signature is invalid", self.install(FAIL_CODESIGN="1").stderr)
        self.assertIn("Gatekeeper rejected", self.install(FAIL_SPCTL="1").stderr)
        self.assertFalse((self.home / ".local/bin/lilt").exists())

    def test_refuses_existing_binary(self):
        path = self.home / ".local/bin/lilt"
        path.parent.mkdir(parents=True)
        path.write_text("existing binary")
        result = self.install()
        self.assertIn("not managed", result.stderr)
        self.assertEqual(path.read_text(), "existing binary")

    def test_rejects_unsupported_platform_and_version(self):
        self.shim("uname", 'if [ "$1" = -s ]; then echo Linux; else echo arm64; fi')
        self.assertIn("requires macOS arm64", self.install().stderr)
        self.shim("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo arm64; fi')
        self.shim("sw_vers", 'echo 13.7')
        self.assertIn("macOS 14", self.install().stderr)
        self.shim("sw_vers", 'echo 14.5')
        self.assertIn("no public stable release", self.install(LILT_VERSION="../bad").stderr)
        self.assertIn("no public stable release", self.install(NO_RELEASE="1").stderr)

    def test_rejects_archive_traversal_and_missing_helper(self):
        self.make_archive(malicious=True)
        self.assertIn("unexpected archive contents", self.install().stderr)
        self.assertFalse((self.root / "outside").exists())
        self.make_archive(missing_helper=True)
        self.assertIn("missing the CLI, license, or signed helpers", self.install().stderr)
        self.assertFalse((self.home / ".local/bin/lilt").exists())
        self.make_archive(missing_license=True)
        self.assertIn("missing the CLI, license, or signed helpers", self.install().stderr)
        self.assertFalse((self.home / ".local/bin/lilt").exists())


if __name__ == "__main__":
    unittest.main()
