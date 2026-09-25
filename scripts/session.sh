#!/usr/bin/env bash
# One session entry point for `just run` (plain TUI) and `just test-drive`
# (Herdr tab with an agent). It selects the build and state root from --env:
#
#   --env pre-release (default) + plain + real  -> the daily server (pinned
#       build, daily socket/state); attaches or starts it on demand.
#   --env dev, or --fake, or the Herdr front    -> a private session under
#       /tmp/lilt-<label>-<stamp>/ that never touches the daily instance.
#
# Private real sessions give up an idle daily server automatically (a playing
# server refuses); invoking the command is itself the audible consent, so no
# extra environment approval is required. Unattended harness entries (round.sh
# --real, probes) still gate on LILT_TEST_AUDIO=1/LILT_PROBE_AUDIO=1. Fake
# sessions need no account, no helpers, and no idle daily server.
# `just stop-daily` stops the daily server explicitly.
set -eu

front=plain
env_name=pre-release
fake=0
browser=0
while [ "$#" -gt 0 ]; do
	case "$1" in
	--front)
		front=${2:?--front needs a value}
		shift 2
		;;
	--env)
		env_name=${2:?--env needs a value}
		shift 2
		;;
	--fake)
		fake=1
		shift
		;;
	--browser)
		browser=1
		shift
		;;
	-h | --help)
		echo "usage: just run|test-drive [--env pre-release|dev] [--fake] [--browser]" >&2
		exit 0
		;;
	*)
		echo "session: unknown argument $1" >&2
		exit 2
		;;
	esac
done
case "$env_name" in
pre-release | dev) ;;
*)
	echo "session: --env must be pre-release or dev" >&2
	exit 2
	;;
esac
case "$front" in
plain | herdr) ;;
*)
	echo "session: --front must be plain or herdr" >&2
	exit 2
	;;
esac

script_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$script_dir/.." && pwd)
local_workflow="$script_dir/local-workflow.py"

# --- daily path: the only combination that owns the daily instance -----------
if [ "$env_name" = pre-release ] && [ "$fake" = 0 ] && [ "$front" = plain ]; then
	if [ "$browser" = 1 ]; then
		exec python3 "$local_workflow" run browser
	fi
	exec python3 "$local_workflow" run
fi

# --- private path ------------------------------------------------------------
if [ "$env_name" = dev ]; then
	if [ "$fake" = 1 ]; then
		(cd "$repo" && just build-go)
	else
		(cd "$repo" && just build)
	fi
	binary="$repo/lilt"
	helper_dir="$repo/player/Build/Products/Release"
else
	binary="$repo/.lilt-prerelease/current/lilt"
	helper_dir="$repo/.lilt-prerelease/current"
	[ -f "$binary" ] || {
		echo "session: no prerelease build; run just promote first" >&2
		exit 1
	}
fi
[ -f "$binary" ] || {
	echo "session: $binary is missing; run just build first" >&2
	exit 1
}

with_helpers=0
if [ "$fake" = 0 ] && [ "$(uname -s)" = "Darwin" ]; then
	with_helpers=1
	[ -d "$helper_dir/lilt-player.app" ] && [ -d "$helper_dir/lilt-audio.app" ] || {
		echo "session: signed helpers are missing under $helper_dir; run just build (or promote) first" >&2
		exit 1
	}
fi

# Herdr needs its environment and tools before it creates any tab, and before
# anything touches the daily server: a `just test-drive` run outside Herdr must
# fail without stopping it.
if [ "$front" = herdr ]; then
	if [ "${HERDR_ENV:-}" != 1 ]; then
		echo "session: run this inside Herdr (HERDR_ENV must be 1)" >&2
		exit 1
	fi
	if [ -z "${HERDR_WORKSPACE_ID:-}" ]; then
		echo "session: HERDR_WORKSPACE_ID is unset; cannot tell which workspace to open the tab in" >&2
		exit 1
	fi
	for tool in herdr jq python3; do
		command -v "$tool" >/dev/null 2>&1 || {
			echo "session: $tool is required" >&2
			exit 1
		}
	done
fi

# Real private sessions give up an idle daily server automatically; invoking the
# command is itself the audible consent. A playing server refuses, so this can
# never cut off the user's audio.
if [ "$fake" = 0 ]; then
	python3 "$local_workflow" yield || exit 3
fi

label=$([ "$front" = herdr ] && echo test-drive || echo run)
stamp=$(date +%Y%m%d-%H%M%S)
session="$label-$stamp"
agent_name=$session
dir="/tmp/lilt-$session"
umask 077
mkdir -p "$dir"

# --- replace the previous test-drive session (Herdr front only) --------------
pointer=/tmp/lilt-test-drive-session
if [ "$front" = herdr ]; then
	if [ -f "$pointer" ]; then
		prev_tab=$(sed -n 's/^tab=//p' "$pointer" | tail -1)
		prev_tui=$(sed -n 's/^tui=//p' "$pointer" | tail -1)
		prev_dir=$(sed -n 's/^dir=//p' "$pointer" | tail -1)
		if [ -n "$prev_tab" ] && [ "$prev_tab" != "${HERDR_TAB_ID:-}" ]; then
			echo "session: closing previous session tab $prev_tab"
			herdr tab close "$prev_tab" >/dev/null 2>&1 || true
		elif [ -n "$prev_tui" ]; then
			# The previous tab is the one this script runs in: closing that tab
			# would kill this process, so only the old TUI pane goes away.
			echo "session: closing previous session TUI pane $prev_tui"
			herdr pane close "$prev_tui" >/dev/null 2>&1 || true
		fi
		[ -n "$prev_dir" ] && echo "session: stopping previous server ($prev_dir)"
		rm -f "$pointer"
	fi
	for leftover in $(herdr tab list | jq -r '.result.tabs[] | select(.label == "lilt test drive") | .tab_id'); do
		[ "$leftover" != "${HERDR_TAB_ID:-}" ] || continue
		echo "session: closing leftover test-drive tab $leftover"
		herdr tab close "$leftover" >/dev/null 2>&1 || true
	done
	for sock in /tmp/lilt-test-drive-*/sock; do
		[ -S "$sock" ] || continue
		env LILT_SOCKET="$sock" "$binary" quit --json >/dev/null 2>&1 || true
		python3 "$local_workflow" release-real "$sock"
	done
	if [ -n "${prev_dir:-}" ]; then
		python3 "$local_workflow" release-real "$prev_dir/sock"
	fi
fi

# Real private sessions take the exclusive reservation so no approved real
# session (this one or a usability round) can run at the same time.
if [ "$fake" = 0 ]; then
	python3 "$local_workflow" reserve-real "$dir/sock" || exit 3
	trap 'python3 "$local_workflow" release-real "$dir/sock" 2>/dev/null || true' EXIT
fi

# Record what this session runs: a private session is exploratory, but "which
# build was that?" must stay answerable.
{
	echo "session: $session"
	echo "front: $front"
	echo "env: $env_name"
	echo "fake: $fake"
	echo "created_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "commit: $(git -C "$repo" rev-parse HEAD)"
	echo "worktree_dirty_files: $(git -C "$repo" status --porcelain | wc -l | tr -d ' ')"
	echo "lilt_sha256: $(shasum -a 256 "$binary" | awk '{print $1}')"
	if [ "$with_helpers" = 1 ]; then
		echo "player_sha256: $(shasum -a 256 "$helper_dir/lilt-player.app/Contents/MacOS/lilt-player" | awk '{print $1}')"
		echo "audio_sha256: $(shasum -a 256 "$helper_dir/lilt-audio.app/Contents/MacOS/lilt-audio" | awk '{print $1}')"
	fi
} >"$dir/manifest.txt"

session_env=(
	"LILT_SOCKET=$dir/sock"
	"LILT_STATE=$dir/state.json"
	"LILT_CONFIG=$dir/config"
	"LILT_RADIO_CACHE=$dir/radio.json"
	"LILT_LOG=$dir/log.jsonl"
	"LILT_FAKE_PLAYER=$fake"
	# Machine-level settings are carried over so the session matches daily use.
	"LILT_CHROMIUM_PATH=${LILT_CHROMIUM_PATH:-}"
	"LILT_APPLE_PROFILE=${LILT_APPLE_PROFILE:-}"
)
if [ "$fake" = 0 ]; then
	session_env+=("LILT_APPLE_ENGINE=$([ "$browser" = 1 ] && echo browser || echo helper)")
	if [ "$with_helpers" = 1 ]; then
		session_env+=("LILT_PLAYER_PATH=$helper_dir/lilt-player.app" "LILT_AUDIO_PATH=$helper_dir/lilt-audio.app")
	fi
fi

# Smoke checks: the CLI answers offline and the API description parses.
smoke="$dir/smoke.txt"
env "${session_env[@]}" "$binary" version >"$smoke" 2>&1
env "${session_env[@]}" "$binary" api --json >/dev/null 2>>"$smoke" || {
	echo "session: 'lilt api --json' failed; see $smoke" >&2
	exit 1
}

echo "session: $session"
echo "  manifest  $dir/manifest.txt"
echo "  log       $dir/log.jsonl"

if [ "$front" = plain ]; then
	env "${session_env[@]}" "$binary" tui
	env "${session_env[@]}" "$binary" quit --json >/dev/null 2>&1 || true
	[ "$fake" = 0 ] && python3 "$local_workflow" release-real "$dir/sock" >/dev/null 2>&1 || true
	if [ ! -S "$dir/sock" ]; then
		rm -rf "$dir"
	else
		echo "session: private server did not stop; keeping $dir" >&2
	fi
	exit 0
fi

# --- Herdr front: agent on the left, TUI on the right ------------------------
env_flags=()
for pair in "${session_env[@]}"; do
	env_flags+=(--env "$pair")
done

tab_json=$(herdr tab create --workspace "$HERDR_WORKSPACE_ID" --label "lilt test drive" --cwd "$repo" --focus "${env_flags[@]}")
tab_id=$(printf '%s' "$tab_json" | jq -r '.result.tab.tab_id')
agent_pane=$(printf '%s' "$tab_json" | jq -r '.result.root_pane.pane_id')
[ "$tab_id" != "null" ] && [ "$agent_pane" != "null" ] || {
	echo "session: could not create the Herdr tab: $tab_json" >&2
	exit 1
}

# `agent start` requires an interactive shell prompt. A fresh tab's shell is not
# up yet and the precheck fails with agent_pane_busy; retry that specific error
# for a bounded time instead of guessing a fixed sleep.
tries=0
while :; do
	if err=$(herdr agent start "$agent_name" --kind pi --pane "$agent_pane" 2>&1 >/dev/null); then
		break
	fi
	tries=$((tries + 1))
	printf '%s\n' "$err" | grep -q '"code":"agent_pane_busy"' || {
		printf 'session: %s\n' "$err" >&2
		exit 1
	}
	if [ "$tries" -ge 60 ]; then
		echo "session: pane $agent_pane never reached an interactive shell prompt" >&2
		exit 1
	fi
	sleep 0.5
done

split_json=$(herdr pane split --pane "$agent_pane" --direction right --cwd "$repo" --ratio 0.5 --no-focus "${env_flags[@]}")
tui_pane=$(printf '%s' "$split_json" | jq -r '.result.pane.pane_id')
[ "$tui_pane" != "null" ] || {
	echo "session: could not split the Herdr pane: $split_json" >&2
	exit 1
}
quit_cmd="\"$binary\" quit --json"
release_cmd=""
[ "$fake" = 0 ] && release_cmd="; python3 \"$local_workflow\" release-real '$dir/sock'"
herdr pane run "$tui_pane" "\"$binary\" tui; $quit_cmd$release_cmd" >/dev/null

ready_match="Home"
[ "$fake" = 0 ] && ready_match="Apple Music"
if herdr pane wait-output "$tui_pane" --match "$ready_match" --timeout 30000 >/dev/null 2>&1; then
	echo "  tui       ready in pane $tui_pane"
else
	echo "  tui       still starting in pane $tui_pane (check with: herdr pane read $tui_pane)"
fi

# Isolation is the point: the private socket exists only once a server bound it,
# and the journal file only once a process opened it.
if [ ! -S "$dir/sock" ] || [ ! -f "$dir/log.jsonl" ]; then
	echo "session: the session is NOT isolated (socket or log missing under $dir)" >&2
	echo "  the TUI is probably on the default socket; closing the tab" >&2
	herdr pane read "$tui_pane" >&2 2>&1 || true
	herdr tab close "$tab_id" >/dev/null 2>&1 || true
	exit 1
fi

printf 'dir=%s\ntab=%s\ntui=%s\n' "$dir" "$tab_id" "$tui_pane" >"$pointer"

herdr agent prompt "$agent_name" "只回一句 ok，不要执行任何命令。背景：这是 lilt 的试驾会话（front=$front env=$env_name fake=${fake}），你在左侧 pane；右侧 pane 是同一个私有 server 上的 TUI。用 $binary 调 CLI（LILT_SOCKET/LILT_STATE/LILT_CONFIG/LILT_RADIO_CACHE/LILT_LOG 已指向 ${dir}）。默认不做账号写操作，也不要触碰日常 server。日志：${dir}/log.jsonl。等用户指令。" --wait --timeout 120000 >/dev/null || {
	echo "session: the agent did not settle on the bootstrap prompt; check the pane" >&2
}

herdr tab focus "$tab_id" >/dev/null
echo "  tab       $tab_id (focused)"
echo "  agent     $agent_name (left pane $agent_pane)"
echo "  read tui  herdr pane read $tui_pane --source recent-unwrapped --lines 60"
echo "  read log  jq -c . $dir/log.jsonl | tail -50"
echo "  cleanup   env LILT_SOCKET=$dir/sock \"$binary\" quit --json"
[ "$fake" = 0 ] && echo "  release   python3 scripts/local-workflow.py release-real $dir/sock (after cleanup)"
