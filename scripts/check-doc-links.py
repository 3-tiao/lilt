#!/usr/bin/env python3
"""Check repository-local Markdown paths and headings in project documentation."""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
LINK = re.compile(r"(?<!!)\[[^]]*\]\(([^)]+)\)")
HEADING = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$", re.MULTILINE)


def local_target(raw: str) -> tuple[str, str] | None:
    target = raw.strip().split(maxsplit=1)[0].strip("<>")
    parsed = urlsplit(target)
    if parsed.scheme or target.startswith("//"):
        return None
    return unquote(parsed.path), unquote(parsed.fragment)


def anchors(markdown: Path) -> set[str]:
    found: set[str] = set()
    counts: dict[str, int] = {}
    for heading in HEADING.findall(markdown.read_text(encoding="utf-8")):
        text = re.sub(r"`([^`]*)`", r"\1", heading).strip().lower()
        slug = re.sub(r"[^\w\- ]", "", text).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        found.add(slug if count == 0 else f"{slug}-{count}")
    return found


def markdown_files() -> list[Path]:
    files = list(DOCS.rglob("*.md"))
    files.extend(sorted(ROOT.glob("README*.md")))
    return files


def main() -> int:
    failures: list[str] = []
    for markdown in markdown_files():
        text = markdown.read_text(encoding="utf-8")
        for match in LINK.finditer(text):
            parsed = local_target(match.group(1))
            if parsed is None:
                continue
            target, fragment = parsed
            destination = (markdown.parent / target).resolve() if target else markdown
            if not destination.is_relative_to(ROOT) or not destination.exists():
                failures.append(f"{markdown.relative_to(ROOT)}: missing link {match.group(1)}")
            elif fragment and destination.suffix == ".md" and fragment not in anchors(destination):
                failures.append(f"{markdown.relative_to(ROOT)}: missing anchor {match.group(1)}")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
