#!/usr/bin/env bash
# Real-playback probe: records the MusicKit timeline of one isolated lilt session
# and prints the status transitions the helper actually observed. It exists for
# the questions in docs/product/open-questions.md that no hermetic test can
# answer (OQ11 natural end, OQ16 helper arbitration pause).
#
# Usage: scripts/playback-probe.sh <ref> [seconds]
#   scripts/playback-probe.sh apple-music:song:471749203 25
#
# Requirements: `just build` (signed helper) and a real Apple Music session.
# It plays audio on this machine; nothing else is touched. The isolated socket,
# state and activity database live in a temp directory.
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/probe-audio.sh"

ref="${1:-apple-music:song:471749203}"
seconds="${2:-25}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cli="$root/lilt"
timeline="${LILT_PLAYER_TIMELINE_PATH:-/tmp/lilt-player-timeline.log}"

if [ ! -x "$cli" ]; then
  echo "build the CLI first: just build-go" >&2
  exit 1
fi

work="$(mktemp -d /tmp/lilt-probe-XXXXXX)"
export LILT_SOCKET="$work/session.sock"
export LILT_STATE="$work/state.json"
export LILT_ACTIVITY_DB="$work/activity.sqlite3"
export LILT_PLAYER_TIMELINE=1
# Optional: disable the helper's playback activity assertion (OQ16 control).
if [ "${LILT_PROBE_ASSERT:-1}" = "0" ]; then
  launchctl setenv LILT_PLAYER_ACTIVITY_ASSERT 0
  export LILT_PLAYER_ACTIVITY_ASSERT=0
fi

# Optional: finite-queue append gap in ms (OQ4 pacing probe).
if [ -n "${LILT_PROBE_PACING_MS:-}" ]; then
  export LILT_QUEUE_PACING_MS="$LILT_PROBE_PACING_MS"
fi

# The helper is launched through LaunchServices, which does not reliably inherit
# the shell environment, so publish the switch for launched apps too.
launchctl setenv LILT_PLAYER_TIMELINE 1
cleanup() {
  launchctl unsetenv LILT_PLAYER_TIMELINE 2>/dev/null || true
  launchctl unsetenv LILT_PLAYER_ACTIVITY_ASSERT 2>/dev/null || true
  if [ -n "${server_pid:-}" ]; then kill "$server_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# Append mode keeps earlier runs so two consecutive sessions can be correlated.
if [ "${LILT_PROBE_APPEND:-0}" != "1" ]; then
  : > "$timeline"
fi
"$cli" serve >"$work/serve.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 100); do
  [ -S "$LILT_SOCKET" ] && break
  sleep 0.1
done
if [ ! -S "$LILT_SOCKET" ]; then
  echo "server did not come up; see $work/serve.log" >&2
  cat "$work/serve.log" >&2
  exit 1
fi

probe_audio_require_consent || exit 3
echo "== play $ref (pacing ${LILT_QUEUE_PACING_MS:-default}ms) =="
play_started=$(python3 -c 'import time; print(time.time())')
"$cli" play "$ref" --json > "$work/play.json" || true
play_elapsed=$(python3 -c "import time; print(round(time.time() - $play_started, 2))")
python3 - "$work/play.json" "$play_elapsed" <<'PYJSON' || cat "$work/play.json"
import json, sys
payload = json.load(open(sys.argv[1]))
elapsed = sys.argv[2]
if not payload.get("ok"):
    print(f"  play FAILED after {elapsed}s: {payload.get('error')}")
    raise SystemExit(0)
data = payload["data"]
queue = data.get("queue") or []
print(f"  fill took {elapsed}s; queue={len(queue)} status={data.get('status')} "
      f"index={data.get('queueIndex')} track={data.get('track', {}).get('title')!r}")
PYJSON

echo "== sampling ${seconds}s =="
samples=0
for _ in $(seq 1 "$seconds"); do
  sleep 1
  samples=$((samples + 1))
  if [ $((samples % 5)) -eq 0 ]; then
    status=$("$cli" status --json | grep -o '"status":"[a-z]*"' | head -1 || true)
    echo "  t+${samples}s ${status:-status=unavailable}"
  fi
done

"$cli" stop --json >/dev/null 2>&1 || true
"$cli" session shutdown --json >/dev/null 2>&1 || true
sleep 1

echo
echo "== timeline (one line per state change, plus 1s samples) =="
if [ -s "$timeline" ]; then
  cat "$timeline"
  echo
  echo "== status transitions per helper pid =="
  awk '
    {
      pid=""; event=""; raw=""; mapped=""; pos="";
      for (i = 1; i <= NF; i++) {
        split($i, kv, "=");
        if (kv[1] == "pid") pid = kv[2];
        if (kv[1] == "event") event = kv[2];
        if (kv[1] == "raw") raw = kv[2];
        if (kv[1] == "mapped") mapped = kv[2];
      }
      if (event != "change" && event != "sample") next;
      key = pid " " mapped;
      if (key != last) { printf "  pid=%s mapped=%s raw=%s (t=%s)\n", pid, mapped, raw, $1; last = key }
    }
  ' "$timeline"
else
  echo "  (empty: the helper did not see LILT_PLAYER_TIMELINE=1)"
fi
