#!/usr/bin/env bash
# One command for the real-session checks the open-questions ledger is waiting
# on. Each check prints PASS/FAIL and keeps its raw evidence under $work.
#
# Usage:
#   LILT_PROBE_AUDIO=1 scripts/check-open-questions.sh            # everything
#   LILT_PROBE_AUDIO=1 scripts/check-open-questions.sh OQ18 OQ17  # a subset
#   scripts/check-open-questions.sh --list                        # what exists
#
# Audibility: MusicKit playback has no per-playback volume, so Apple Music
# checks need LILT_PROBE_AUDIO=1 and are audible. The script never changes the
# system volume. OQ16 additionally needs a second helper process, which the
# script starts on the default socket if none is running.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cli="$root/lilt"
song="${LILT_PROBE_SONG:-apple-music:song:471749203}"
album="${LILT_PROBE_ALBUM:-apple-music:album:471749193}"

if [ "${1:-}" = "--list" ]; then
  cat <<'MSG'
OQ18  shuffle off actually sticks (wire, idle)       fast, no audio
OQ18P shuffle sticks while a queue is playing        ~20s, audible
OQ17  a stop→play album keeps its queue              fast, audible
OQ16  two helpers do not pause playback by themselves medium, audible
OQ11  a finished finite queue reports "ended"        long (~6 min), audible
OQ14  shuffle state is reported on the wire          fast, audible
MSG
  exit 0
fi

wanted=("$@")
if [ ${#wanted[@]} -eq 0 ]; then
  wanted=(OQ18 OQ17 OQ16 OQ11 OQ14)
fi
want() {
  local name="$1"
  for entry in "${wanted[@]}"; do [ "$entry" = "$name" ] && return 0; done
  return 1
}

source "$(cd "$(dirname "$0")" && pwd)/probe-audio.sh"
probe_audio_require_consent || exit 3

work="$(mktemp -d /tmp/lilt-oq-XXXXXX)"
python3 "$root/scripts/local-workflow.py" reserve-real "$work/session.sock" || exit 3
export LILT_SOCKET="$work/session.sock"
export LILT_STATE="$work/state.json"
export LILT_ACTIVITY_DB="$work/activity.sqlite3"
server_pid=""
results=()

cleanup() {
  launchctl unsetenv LILT_PLAYER_TIMELINE 2>/dev/null || true
  if [ -n "${peer_dir:-}" ]; then
    env LILT_SOCKET="$peer_dir/session.sock" LILT_STATE="$peer_dir/state.json" "$cli" quit >/dev/null 2>&1 || true
  fi
  if [ -n "$server_pid" ]; then "$cli" quit >/dev/null 2>&1 || true; kill "$server_pid" 2>/dev/null || true; fi
  python3 "$root/scripts/local-workflow.py" release-real "$work/session.sock" || true
  echo "-- evidence kept in $work"
}
trap cleanup EXIT

start_session() {
  "$cli" serve >"$work/serve.log" 2>&1 &
  server_pid=$!
  for _ in $(seq 1 100); do [ -S "$LILT_SOCKET" ] && return 0; sleep 0.1; done
  echo "server did not start; see $work/serve.log" >&2
  exit 1
}

stop_session() {
  "$cli" stop >/dev/null 2>&1 || true
  "$cli" quit >/dev/null 2>&1 || true
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  server_pid=""
  sleep 2
}

record() { results+=("$1  $2"); }

# status_field reads one field from `lilt status --json`.
status_field() {
  "$cli" status --queue --json 2>/dev/null | python3 -c "import json,sys
try: data = json.load(sys.stdin)['data']
except Exception: print(''); raise SystemExit
print(data.get('$1'))"
}

start_session

if want OQ18; then
  echo "== OQ18: does shuffle off stick? =="
  "$cli" shuffle on --json > "$work/oq18-on.json" 2>&1 || true
  sleep 1
  on="$(status_field shuffle)"
  "$cli" shuffle off --json > "$work/oq18-off.json" 2>&1 || true
  sleep 1
  off="$(status_field shuffle)"
  if [ "$on" = "True" ] && [ "$off" = "False" ]; then
    record OQ18 PASS
  else
    record OQ18 "FAIL (after on: $on, after off: $off)"
  fi
fi

# OQ18P plays first: an idle player may simply ignore shuffleMode, so the
# interesting question is whether it sticks with a queue loaded.
if want OQ18P; then
  echo "== OQ18P: does shuffle stick while playing? =="
  : > /tmp/lilt-player-timeline.log
  start_session
  "$cli" play "$album" --json > "$work/oq18p-play.json" 2>&1 || true
  sleep 3
  before="$(status_field shuffle)"
  "$cli" shuffle on --json > "$work/oq18p-on.json" 2>&1 || true
  sleep 2
  after_on="$(status_field shuffle)"
  "$cli" shuffle off --json > "$work/oq18p-off.json" 2>&1 || true
  sleep 2
  after_off="$(status_field shuffle)"
  stop_session
  cp /tmp/lilt-player-timeline.log "$work/oq18p-timeline.log" 2>/dev/null || true
  if [ "$after_on" = "True" ] && [ "$after_off" = "False" ]; then
    record OQ18P "PASS (before=$before, after on=$after_on, after off=$after_off)"
  else
    record OQ18P "FAIL (before=$before, after on=$after_on, after off=$after_off)"
  fi
fi

if want OQ14; then
  echo "== OQ14: is the shuffle state on the wire? =="
  "$cli" shuffle on --json >/dev/null 2>&1 || true
  sleep 1
  wire="$(status_field shuffle)"
  "$cli" shuffle off --json >/dev/null 2>&1 || true
  if [ "$wire" = "True" ]; then record OQ14 PASS; else record OQ14 "FAIL (shuffle=$wire)"; fi
fi

if want OQ17; then
  echo "== OQ17: stop then play an album =="
  # The helper timeline is the only record of what MusicKit was doing when the
  # re-pin fails, so keep it for this check.
  launchctl setenv LILT_PLAYER_TIMELINE 1
  export LILT_PLAYER_TIMELINE=1
  : > /tmp/lilt-player-timeline.log
  "$cli" play "$song" --json >/dev/null 2>&1 || true
  sleep 2
  "$cli" stop --json >/dev/null 2>&1 || true
  sleep 1
  "$cli" play "$album" --json > "$work/oq17-play.json" 2>&1 || true
  verdict="$(python3 "$(cd "$(dirname "$0")" && pwd)/oq17_verdict.py" "$work/oq17-play.json")"
  case "$verdict" in
    ok|queueReady:*)
      # The queue survived. If the re-pin failed, can a plain resume recover it?
      # That decides whether a bounded server-side retry is the right fix.
      recovered="n/a"
      if [ "${verdict%%:*}" = "queueReady" ]; then
        # Which recovery actually works: a plain resume, or starting the play
        # again (which rebuilds the queue)?
        "$cli" resume --json > "$work/oq17-resume.json" 2>&1 || true
        sleep 3
        recovered="$(status_field status)"
        "$cli" play "$album" --json > "$work/oq17-replay.json" 2>&1 || true
        sleep 3
        rebuilt="$(status_field status)"
        recovered="$recovered replay=$rebuilt"
      fi
      if [ "${verdict%%:*}" = "queueReady" ]; then
        cp /tmp/lilt-player-timeline.log "$work/oq17-timeline.log" 2>/dev/null || true
      fi
      record OQ17 "PASS ($verdict, resume=$recovered)"
      ;;
    *)
      record OQ17 "FAIL ($verdict, see oq17-play.json)"
      ;;
  esac
fi

stop_session

if want OQ16; then
  echo "== OQ16: 10 single-song plays with a second helper present =="
  # A second private server keeps a read-only Apple helper alive. Never start
  # it on the daily socket or count an unrelated user's helper as the peer.
  peer_dir="$work/peer"
  mkdir -p "$peer_dir"
  env LILT_SOCKET="$peer_dir/session.sock" LILT_STATE="$peer_dir/state.json" \
    "$cli" serve --detach --json > "$work/oq16-peer.json"
  env LILT_SOCKET="$peer_dir/session.sock" LILT_STATE="$peer_dir/state.json" \
    "$cli" auth status apple-music --json > "$work/oq16-peer-auth.json" || true
  sleep 2
  peers="$(pgrep -f 'lilt-player --rpc-socket' | wc -l | tr -d ' ')"
  : > /tmp/lilt-player-timeline.log
  for run in $(seq 1 10); do
    start_session
    "$cli" play "$song" --json > "$work/oq16-$run.log" 2>&1 || true
    sleep 10
    stop_session
  done
  pauses="$(awk '{pid="";raw="";for(i=1;i<=NF;i++){split($i,kv,"=");if(kv[1]=="pid")pid=kv[2];if(kv[1]=="raw")raw=kv[2]} if(prev[pid]=="playing" && raw=="paused") n++; prev[pid]=raw} END{print n+0}' /tmp/lilt-player-timeline.log)"
  cp /tmp/lilt-player-timeline.log "$work/oq16-timeline.log" 2>/dev/null || true
  if [ "$peers" -ge 2 ] && [ "$pauses" -eq 0 ]; then
    record OQ16 "PASS (peers=$peers, spontaneous pauses=$pauses)"
  else
    record OQ16 "FAIL (peers=$peers, spontaneous pauses=$pauses)"
  fi
fi

if want OQ11; then
  echo "== OQ11: play a single song to its end (~6 min) =="
  # Same session throughout: a new session has nothing playing, so the end can
  # only be observed where the song was started.
  start_session
  "$cli" play "$song" --json > "$work/oq11-play.json" 2>&1 || true
  final=""
  for _ in $(seq 1 80); do
    sleep 6
    final="$(status_field status)"
    case "$final" in
      playing|buffering|"") ;;
      *) break ;;
    esac
  done
  sleep 2
  final="$(status_field status)"
  "$cli" status --queue --json > "$work/oq11-final.json" 2>&1 || true
  stop_session
  if [ "$final" = "ended" ]; then record OQ11 PASS; else record OQ11 "FAIL (status=$final)"; fi
fi

echo
echo "== summary =="
for line in "${results[@]}"; do echo "  $line"; done
echo
echo "Update docs/product/open-questions.md for every PASS: move the entry to"
echo "'archived' by deleting it and landing the conclusion in the authoritative doc."
