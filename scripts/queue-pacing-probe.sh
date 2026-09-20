#!/usr/bin/env bash
# Queue-fill pacing probe (docs/product/open-questions.md OQ4).
#
# One isolated server per pacing value, one warm-up play, then N album plays
# back-to-back. Reusing one helper removes the launch/timing noise that
# dominated the first version of this probe: the interesting signal is the fill
# itself, not MusicKit's first-seconds readiness.
#
# Usage: scripts/queue-pacing-probe.sh [ref] [runs-per-pacing]
#   scripts/queue-pacing-probe.sh apple-music:album:471749193 3
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/probe-audio.sh"

ref="${1:-apple-music:album:471749193}"
runs="${2:-3}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cli="$root/lilt"
warmup_ref="${LILT_PROBE_WARMUP_REF:-apple-music:song:471749203}"

if [ ! -x "$cli" ]; then
  echo "build the CLI first: just build-go" >&2
  exit 1
fi

launchctl setenv LILT_PLAYER_TIMELINE 1
work="$(mktemp -d /tmp/lilt-pacing-XXXXXX)"
export LILT_SOCKET="$work/session.sock"
export LILT_STATE="$work/state.json"
export LILT_ACTIVITY_DB="$work/activity.sqlite3"
export LILT_PLAYER_TIMELINE=1
server_pid=""

cleanup() {
  launchctl unsetenv LILT_PLAYER_TIMELINE 2>/dev/null || true
  if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

start_server() {
  local pacing="$1"
  LILT_QUEUE_PACING_MS="$pacing" "$cli" serve >"$work/serve.log" 2>&1 &
  server_pid=$!
  for _ in $(seq 1 100); do
    [ -S "$LILT_SOCKET" ] && return 0
    sleep 0.1
  done
  echo "server did not come up for pacing $pacing" >&2
  return 1
}

stop_server() {
  "$cli" stop >/dev/null 2>&1 || true
  "$cli" quit >/dev/null 2>&1 || true
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  server_pid=""
  sleep 2
}

# one_play measures a single play: fill wall time, queue size, status, and the
# status three seconds later (a fill that wedges lands stopped/paused).
one_play() {
  local label="$1"
  local started finished elapsed
  started=$(python3 -c 'import time; print(time.time())')
  "$cli" play "$ref" --json > "$work/play.json" || true
  finished=$(python3 -c 'import time; print(time.time())')
  elapsed=$(python3 -c "print(round($finished - $started, 2))")
  sleep 3
  "$cli" status --queue --json > "$work/status.json" 2>/dev/null || echo '{}' > "$work/status.json"
  python3 - "$work/play.json" "$work/status.json" "$elapsed" "$label" <<'PYJSON'
import json, sys
play = json.load(open(sys.argv[1]))
status = json.load(open(sys.argv[2]))
elapsed, label = sys.argv[3], sys.argv[4]
after = (status.get("data") or {}) if status else {}
if not play.get("ok"):
    code = (play.get("error") or {}).get("code")
    print(f"  {label}: FAILED after {elapsed}s code={code} status={after.get('status')}")
    raise SystemExit(0)
data = play["data"]
queue = data.get("queue") or []
print(f"  {label}: fill={elapsed}s queue={len(queue)} playStatus={data.get('status')} "
      f"+3s={after.get('status')} index={after.get('queueIndex')}")
PYJSON
}

probe_audio_require_consent || exit 3
echo "ref=$ref runs=$runs"
for pacing in ${LILT_PROBE_PACINGS:-700 500 400 300}; do
  echo "== pacing ${pacing}ms =="
  start_server "$pacing"
  # Warm-up: the first play after a helper launch is not representative.
  "$cli" play "$warmup_ref" --json >/dev/null 2>&1 || true
  sleep 2
  "$cli" stop --json >/dev/null 2>&1 || true
  sleep 1
  for run in $(seq 1 "$runs"); do
    # No stop between runs: a stop immediately before a fill makes the re-pin
    # fail (see open-questions), which is a different question than pacing.
    one_play "run$run"
    sleep 1
  done
  stop_server
done
