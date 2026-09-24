#!/usr/bin/env bash
# Open a Herdr tab for a manual test session: pi on the left, an isolated lilt
# TUI on the right, both pointed at the same private server. Fake by default;
# --real requires an explicit audible opt-in and an idle daily server.
# The session gets its
# own socket/state/config/radio cache/log under /tmp/lilt-manual-<stamp>/, and
# the build identity it runs is recorded next to them. Only one manual session
# is live at a time: a fresh run closes the previous session's TUI and private
# server first (see the replacement block below). Closing the tab stops the TUI
# and the server it started when the session ends normally; a replaced session
# is stopped explicitly here.
set -eu

mode=fake
if [ "${1:-}" = "--real" ] && [ "$#" -eq 1 ]; then
	mode=real
elif [ "$#" -ne 0 ]; then
	echo "manual-test: usage: just manual-test | LILT_TEST_AUDIO=1 just manual-test-real" >&2
	exit 2
fi

if [ "$mode" = real ]; then
	if [ "${LILT_TEST_AUDIO:-}" != 1 ]; then
		echo "manual-test: real playback needs LILT_TEST_AUDIO=1 and explicit user approval" >&2
		exit 3
	fi
	python3 "$(dirname "$0")/local-workflow.py" check-idle || exit 3
fi

if [ "${HERDR_ENV:-}" != "1" ]; then
	echo "manual-test: run this inside Herdr (HERDR_ENV must be 1)" >&2
	exit 1
fi
if [ -z "${HERDR_WORKSPACE_ID:-}" ]; then
	echo "manual-test: HERDR_WORKSPACE_ID is unset; cannot tell which workspace to open the tab in" >&2
	exit 1
fi

for tool in herdr jq python3; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "manual-test: $tool is required" >&2
		exit 1
	}
done

script_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$script_dir/.." && pwd)
player_bin="$repo/player/Build/Products/Release/lilt-player.app/Contents/MacOS/lilt-player"
audio_bin="$repo/player/Build/Products/Release/lilt-audio.app/Contents/MacOS/lilt-audio"

# Only the approved real mode needs signed helpers. Fake mode never starts one.
with_helpers=0
[ "$mode" = real ] && [ "$(uname -s)" = "Darwin" ] && with_helpers=1

[ -f "$repo/lilt" ] || {
	echo "manual-test: $repo/lilt is missing; run 'just build' first" >&2
	exit 1
}
if [ "$with_helpers" = 1 ]; then
	for artifact in "$player_bin" "$audio_bin"; do
		[ -f "$artifact" ] || {
			echo "manual-test: $artifact is missing; run 'just build' first" >&2
			exit 1
		}
	done
fi

# --- replace the previous manual session -------------------------------------

# Only one manual session is live at a time. The pointer records the previous
# session's tab and TUI pane; the label sweep below also covers sessions from
# before the pointer existed, and the socket sweep stops every leftover manual
# server. `lilt quit` on a dead socket is a successful no-op (it never
# auto-starts), so stale entries are harmless.
pointer=/tmp/lilt-manual-session
if [ -f "$pointer" ]; then
	prev_tab=$(sed -n 's/^tab=//p' "$pointer" | tail -1)
	prev_tui=$(sed -n 's/^tui=//p' "$pointer" | tail -1)
	prev_dir=$(sed -n 's/^dir=//p' "$pointer" | tail -1)
	if [ -n "$prev_tab" ] && [ "$prev_tab" != "${HERDR_TAB_ID:-}" ]; then
		echo "manual-test: closing previous session tab $prev_tab"
		herdr tab close "$prev_tab" >/dev/null 2>&1 || true
	elif [ -n "$prev_tui" ]; then
		# The previous tab is the one this script runs in (its agent was asked
		# to restart the session): closing that tab would kill this process, so
		# only the old TUI pane goes away.
		echo "manual-test: closing previous session TUI pane $prev_tui"
		herdr pane close "$prev_tui" >/dev/null 2>&1 || true
	fi
	[ -n "$prev_dir" ] && echo "manual-test: stopping previous server ($prev_dir)"
	rm -f "$pointer"
fi
# Fallback for sessions that predate the pointer (or lost it): close every tab
# this script created, except the one this script runs in.
for leftover in $(herdr tab list | jq -r '.result.tabs[] | select(.label == "lilt manual") | .tab_id'); do
	[ "$leftover" != "${HERDR_TAB_ID:-}" ] || continue
	echo "manual-test: closing leftover manual tab $leftover"
	herdr tab close "$leftover" >/dev/null 2>&1 || true
done
for sock in /tmp/lilt-manual-*/sock; do
	[ -S "$sock" ] || continue
	env LILT_SOCKET="$sock" "$repo/lilt" quit --json >/dev/null 2>&1 || true
	python3 "$repo/scripts/local-workflow.py" release-real "$sock"
done
if [ -n "${prev_dir:-}" ]; then
	python3 "$repo/scripts/local-workflow.py" release-real "$prev_dir/sock"
fi

stamp=$(date +%Y%m%d-%H%M%S)
session="manual-$stamp"
# Herdr agent names must be unique among live agents, so a second session does
# not collide with the first one still running.
agent_name=$session
dir="/tmp/lilt-$session"
umask 077
mkdir -p "$dir"
if [ "$mode" = real ]; then
	python3 "$repo/scripts/local-workflow.py" reserve-real "$dir/sock" || exit 3
	# On an interrupted startup, release only if no test server remains alive.
	trap 'python3 "$repo/scripts/local-workflow.py" release-real "$dir/sock" 2>/dev/null || true' EXIT
fi

# Record what this session runs. A manual session is exploratory, not a
# replayable round, but "which build was that?" must stay answerable.
{
	echo "session: $session"
	echo "mode: $mode"
	echo "created_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "commit: $(git -C "$repo" rev-parse HEAD)"
	echo "worktree_dirty_files: $(git -C "$repo" status --porcelain | wc -l | tr -d ' ')"
	echo "worktree_sha256: $(git -C "$repo" status --porcelain | shasum -a 256 | awk '{print $1}')"
	echo "lilt_sha256: $(shasum -a 256 "$repo/lilt" | awk '{print $1}')"
	if [ "$with_helpers" = 1 ]; then
		echo "player_sha256: $(shasum -a 256 "$player_bin" | awk '{print $1}')"
		echo "audio_sha256: $(shasum -a 256 "$audio_bin" | awk '{print $1}')"
	fi
} >"$dir/manifest.txt"

# Everything this session runs, including the offline smoke checks, journals to
# the session log instead of the default one.
# `tab create --env` reaches the tab's own process only: a split pane does not
# inherit it, so the TUI pane is given the same variables on `pane split`.
# macOS ships bash as /bin/sh, which is what this script runs under.
session_env=(
	"LILT_SOCKET=$dir/sock"
	"LILT_STATE=$dir/state.json"
	"LILT_CONFIG=$dir/config"
	"LILT_RADIO_CACHE=$dir/radio.json"
	"LILT_LOG=$dir/log.jsonl"
	"LILT_FAKE_PLAYER=$([ "$mode" = fake ] && echo 1 || echo 0)"
	# Machine-level settings, carried over so the session matches daily use: which
	# browser to drive and which Apple profile to reuse. They are not session
	# state, so a fresh state root must not silently drop them.
	"LILT_CHROMIUM_PATH=${LILT_CHROMIUM_PATH:-}"
	"LILT_APPLE_PROFILE=${LILT_APPLE_PROFILE:-}"
)
env_flags=()
for pair in "${session_env[@]}"; do
	env_flags+=(--env "$pair")
done

# Smoke checks: the CLI answers offline, the API description parses.
smoke="$dir/smoke.txt"
env "${session_env[@]}" "$repo/lilt" version >"$smoke" 2>&1
env "${session_env[@]}" "$repo/lilt" api --json >/dev/null 2>>"$smoke" || {
	echo "manual-test: 'lilt api --json' failed; see $smoke" >&2
	exit 1
}

echo "manual-test: session $session"
echo "  manifest  $dir/manifest.txt"
echo "  log       $dir/log.jsonl"
echo "  state     $dir/state.json"

# --workspace keeps the tab in the caller's workspace: without it Herdr uses the
# UI-focused workspace, which can be someone else's.
tab_json=$(herdr tab create --workspace "$HERDR_WORKSPACE_ID" --label "lilt manual" --cwd "$repo" --focus "${env_flags[@]}")
tab_id=$(printf '%s' "$tab_json" | jq -r '.result.tab.tab_id')
agent_pane=$(printf '%s' "$tab_json" | jq -r '.result.root_pane.pane_id')
[ "$tab_id" != "null" ] && [ "$agent_pane" != "null" ] || {
	echo "manual-test: could not create the Herdr tab: $tab_json" >&2
	exit 1
}

# Left pane: the agent, named so Herdr reports its lifecycle state.
# `agent start` requires a pane already sitting at an interactive shell prompt,
# and its `--timeout` only waits for the agent, not for the shell to boot. The
# shell of a freshly created tab is not up yet at this point, and that precheck
# fails fast with `agent_pane_busy`; retry that specific error for a bounded
# time instead of guessing a fixed sleep. Any other error is real and fatal.
agent_started=
tries=0
while :; do
	if err=$(herdr agent start "$agent_name" --kind pi --pane "$agent_pane" 2>&1 >/dev/null); then
		agent_started=1
		break
	fi
	tries=$((tries + 1))
	printf '%s\n' "$err" | grep -q '"code":"agent_pane_busy"' || {
		printf 'manual-test: %s\n' "$err" >&2
		exit 1
	}
	if [ "$tries" -ge 60 ]; then
		echo "manual-test: pane $agent_pane never reached an interactive shell prompt" >&2
		exit 1
	fi
	sleep 0.5
done

# Right pane: the TUI on the same private server. `lilt quit` after the TUI
# exits keeps the session from leaving a detached server behind.
split_json=$(herdr pane split --pane "$agent_pane" --direction right --cwd "$repo" --ratio 0.5 --no-focus "${env_flags[@]}")
tui_pane=$(printf '%s' "$split_json" | jq -r '.result.pane.pane_id')
[ "$tui_pane" != "null" ] || {
	echo "manual-test: could not split the Herdr pane: $split_json" >&2
	exit 1
}
if [ "$mode" = real ]; then
	herdr pane run "$tui_pane" "./lilt tui; ./lilt quit --json; python3 scripts/local-workflow.py release-real '$dir/sock'" >/dev/null
else
	herdr pane run "$tui_pane" "./lilt tui; ./lilt quit --json" >/dev/null
fi

# The first frame carries the source name on macOS and the always-present Home
# surface off it; a timeout is reported, not fatal.
ready_match="Home"
[ "$with_helpers" = 1 ] && ready_match="Apple Music"
if herdr pane wait-output "$tui_pane" --match "$ready_match" --timeout 30000 >/dev/null 2>&1; then
	echo "  tui       ready in pane $tui_pane"
else
	echo "  tui       still starting in pane $tui_pane (check with: herdr pane read $tui_pane)"
fi

# Isolation is the point of this session: the private socket exists only once a
# server bound it, and the journal file only once a process opened it. Check
# before reporting success so a mis-wired session cannot look healthy.
if [ ! -S "$dir/sock" ] || [ ! -f "$dir/log.jsonl" ]; then
	echo "manual-test: the session is NOT isolated (socket or log missing under $dir)" >&2
	echo "  the TUI is probably on the default socket; closing the tab" >&2
	# The pane is about to disappear, so its contents are the only evidence of
	# why the TUI never connected.
	herdr pane read "$tui_pane" >&2 2>&1 || true
	herdr tab close "$tab_id" >/dev/null 2>&1 || true
	exit 1
fi

# Record this session as the live one so the next run replaces it.
printf 'dir=%s\ntab=%s\ntui=%s\n' "$dir" "$tab_id" "$tui_pane" >"$pointer"

herdr agent prompt "$agent_name" "只回一句 ok，不要执行任何命令。背景：这是 lilt 的 ${mode} 手动测试会话，你在左侧 pane；右侧 pane 是同一个私有 server 上的 TUI。用仓库根的 ./lilt 调 CLI（LILT_SOCKET/LILT_STATE/LILT_CONFIG/LILT_RADIO_CACHE/LILT_LOG 已指向 ${dir}）。${mode} 轮默认不做账号写操作，也不要触碰日常 server。日志：${dir}/log.jsonl。等用户指令。" --wait --timeout 120000 >/dev/null || {
	echo "manual-test: the agent did not settle on the bootstrap prompt; check the pane" >&2
}

herdr tab focus "$tab_id" >/dev/null
echo "  tab       $tab_id (focused)"
echo "  agent     $agent_name (left pane $agent_pane)"
echo "  read tui  herdr pane read $tui_pane --source recent-unwrapped --lines 60"
echo "  read log  jq -c . $dir/log.jsonl | tail -50"
echo "  cleanup   env LILT_SOCKET=$dir/sock \"$repo/lilt\" quit --json"
if [ "$mode" = real ]; then
	echo "  release   python3 scripts/local-workflow.py release-real $dir/sock (after cleanup)"
fi
