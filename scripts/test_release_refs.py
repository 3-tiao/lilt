"""Hermetic check: the install docs announce exactly the latest tagged release."""

import re
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


def main_go_version() -> str:
    text = (REPO / "cmd/lilt/main.go").read_text(encoding="utf-8")
    match = re.search(r'var version = "(\d+\.\d+\.\d+)"', text)
    if not match:
        raise AssertionError("cmd/lilt/main.go var version not found")
    return match.group(1)


def bump_rank(version: str) -> tuple:
    return tuple(int(part) for part in version.split("."))


def expected_release() -> str:
    """The version the docs must announce: the newer of the latest tag and
    main.go. A release first bumps main.go and the docs together, then tags;
    between those steps main.go leads. Once tagged they agree again."""
    tag = latest_tag().removeprefix("v")
    main = main_go_version()
    return max(tag, main, key=bump_rank)


class ReleaseRefsTest(unittest.TestCase):
    """docs/getting-started/install.md is the single authoritative place that
    names the current published release; every other doc must stay
    version-less (history lives in docs/product/release.md)."""

    def setUp(self):
        self.tag = latest_tag()
        self.version = expected_release()

    def test_latest_tag_exists(self):
        self.assertRegex(self.tag, r"^v\d+\.\d+\.\d+$")

    def test_install_docs_announce_the_latest_release(self):
        import re
        for rel in ("docs/getting-started/install.md", "docs/en/getting-started/install.md"):
            with self.subTest(doc=rel):
                text = (REPO / rel).read_text(encoding="utf-8")
                announced = re.search(r"v(\d+\.\d+\.\d+)", text)
                self.assertIsNotNone(
                    announced, f"{rel} must announce a release version")
                self.assertEqual(
                    announced.group(1), self.version,
                    f"{rel} announces {announced.group(1)} but the latest tag is "
                    f"{self.version}; bump the docs together with the tag",
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
