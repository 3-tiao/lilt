"""Hermetic check: the install docs announce exactly the latest tagged release."""

import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def latest_tag() -> str:
    out = subprocess.run(
        ["git", "-C", str(REPO), "describe", "--tags", "--abbrev=0"],
        capture_output=True, text=True, check=True,
    )
    return out.stdout.strip()


class ReleaseRefsTest(unittest.TestCase):
    """docs/getting-started/install.md is the single authoritative place that
    names the current published release; every other doc must stay
    version-less (history lives in docs/product/release.md)."""

    def setUp(self):
        self.tag = latest_tag()
        self.version = self.tag.removeprefix("v")

    def test_latest_tag_exists(self):
        self.assertRegex(self.tag, r"^v\d+\.\d+\.\d+$")

    def test_install_docs_announce_the_latest_release(self):
        for rel in ("docs/getting-started/install.md", "docs/en/getting-started/install.md"):
            with self.subTest(doc=rel):
                text = (REPO / rel).read_text(encoding="utf-8")
                self.assertIn(
                    self.version, text,
                    f"{rel} must name the current release {self.version} "
                    "(update it together with the release tag)",
                )

    def test_incidental_docs_stay_version_less(self):
        for rel in ("README.md", "README.zh-CN.md", "docs/README.md",
                    "docs/en/README.md", "docs/client-api/README.md",
                    "docs/product/roadmap.md"):
            with self.subTest(doc=rel):
                text = (REPO / rel).read_text(encoding="utf-8")
                self.assertNotRegex(
                    text, r"\bv\d+\.\d+\.\d+\b",
                    f"{rel} hardcodes a release version; point at "
                    "docs/getting-started/install.md instead so a release "
                    "edits one authoritative place",
                )


if __name__ == "__main__":
    unittest.main()
