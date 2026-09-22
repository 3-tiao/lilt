#!/usr/bin/env python3
"""Summarize the just-command usage log written by scripts/just-tracker."""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOG = ROOT / ".just-usage.tsv"


def invocations(log: Path) -> dict[tuple[str, str], tuple[str, str]]:
    """Read (just-pid, argv) -> (who, last ts), one entry per real invocation.

    A just process runs one command but writes one line per executed recipe
    (plus backtick evaluations) with the same pid, so the pid+argv pair is the
    invocation. Pid reuse across distinct invocations only ever merges two runs
    of the same command, which is harmless for a usage tally.
    """
    runs: dict[tuple[str, str], tuple[str, str]] = {}
    if not log.is_file():
        return runs
    for raw in log.read_text(encoding="utf-8", errors="replace").splitlines():
        fields = raw.split("\t")
        if len(fields) != 4:
            continue  # tolerate a torn interleaved write
        ts, pid, who, argv = fields
        if not (ts and pid and who and argv):
            continue
        runs[(pid, argv)] = (who, ts)
    return runs


def tally(runs: dict[tuple[str, str], tuple[str, str]]) -> dict[str, dict[str, object]]:
    """Aggregate per-command user/ai invocation counts and last-used ts."""
    per: dict[str, dict[str, object]] = {}
    for (_pid, argv), (who, ts) in runs.items():
        command = "default" if argv == "(default)" else argv.split(" ")[0]
        row = per.setdefault(command, {"user": 0, "ai": 0, "last": ""})
        if who not in row:
            row[who] = 0
        row[who] = int(row[who]) + 1
        if ts > str(row["last"]):
            row["last"] = ts
    return per


def main() -> int:
    log = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else LOG
    # The usage recipe pipes `just --summary`, which prints the recipe names
    # space-separated on one line.
    recipes = sys.stdin.read().split()
    per = tally(invocations(log))

    print(f"just command usage ({log})")
    if per:
        width = max([len(c) for c in per] + [len("command")])
        print(f"{'command':{width}}  user   ai  last used")
        for command in sorted(per, key=lambda c: (-(per[c]["user"] + per[c]["ai"]), c)):
            row = per[command]
            gone = "" if command in recipes else "  (not a current recipe)"
            print(
                f"{command:{width}}  {row['user']:>4}  {row['ai']:>3}"
                f"  {str(row['last'])[:10]}{gone}"
            )
    else:
        print("nothing recorded yet")

    never = sorted(r for r in recipes if r not in per)
    if never:
        print(f"never used ({len(never)}): {' '.join(never)}")
    elif recipes:
        print("every recipe has been used")
    return 0


if __name__ == "__main__":
    sys.exit(main())