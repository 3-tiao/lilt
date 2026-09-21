#!/bin/sh
# Open a Herdr tab for a manual test session: pi on the left, an isolated lilt
# TUI on the right, both pointed at the same private server.
#
# Run it through `just manual-test`, which first stops the normal server and
# playback, then rebuilds the CLI and both signed helpers. The session gets its
# own socket/state/config/radio cache/log under /tmp/lilt-manual-<stamp>/, and
# the build identity it runs is recorded next to them. Closing the tab stops the
# TUI and the server it started (the pane runs `lilt quit` after the TUI exits).
set -eu

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
for artifact in "$repo/lilt" "$player_bin" "$audio_bin"; do
	[ -f "$artifact" ] || {
		echo "manual-test: $artifact is missing; run 'just build' first" >&2
		exit 1
	}
done

stamp=$(date +%Y%m%d-%H%M%S)
session="manual-$stamp"
# Herdr agent names must be unique among live agents, so a second session does
# not collide with the first one still running.
agent_name=$session
dir="/tmp/lilt-$session"
umask 077
mkdir -p "$dir"

# Record what this session runs. A manual session is exploratory, not a
# replayable round, but "which build was that?" must stay answerable.
{
	echo "session: $session"
	echo "created_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "commit: $(git -C "$repo" rev-parse HEAD)"
	echo "worktree_dirty_files: $(git -C "$repo" status --porcelain | wc -l | tr -d ' ')"
	echo "worktree_sha256: $(git -C "$repo" status --porcelain | shasum -a 256 | awk '{print $1}')"
	echo "lilt_sha256: $(shasum -a 256 "$repo/lilt" | awk '{print $1}')"
	echo "player_sha256: $(shasum -a 256 "$player_bin" | awk '{print $1}')"
	echo "audio_sha256: $(shasum -a 256 "$audio_bin" | awk '{print $1}')"
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
herdr agent start "$agent_name" --kind pi --pane "$agent_pane" >/dev/null

# Right pane: the TUI on the same private server. `lilt quit` after the TUI
# exits keeps the session from leaving a detached server behind.
split_json=$(herdr pane split --pane "$agent_pane" --direction right --cwd "$repo" --ratio 0.5 --no-focus "${env_flags[@]}")
tui_pane=$(printf '%s' "$split_json" | jq -r '.result.pane.pane_id')
[ "$tui_pane" != "null" ] || {
	echo "manual-test: could not split the Herdr pane: $split_json" >&2
	exit 1
}
herdr pane run "$tui_pane" "./lilt tui; ./lilt quit --json" >/dev/null

# The first frame carries the source name; a timeout is reported, not fatal.
if herdr pane wait-output "$tui_pane" --match "Apple Music" --timeout 30000 >/dev/null 2>&1; then
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
	herdr tab close "$tab_id" >/dev/null 2>&1 || true
	exit 1
fi

herdr agent prompt "$agent_name" "只回一句 ok，不要执行任何命令。背景：这是 lilt 的手动测试会话，你在左侧 pane；右侧 pane 是同一个 server 上的 TUI，你执行的 CLI 操作会立刻反映在它上面。用仓库根的 ./lilt 调 CLI（LILT_SOCKET/LILT_STATE/LILT_CONFIG/LILT_RADIO_CACHE/LILT_LOG 已指向 ${dir}）。日志：${dir}/log.jsonl。等用户指令。" --wait --timeout 120000 >/dev/null || {
	echo "manual-test: the agent did not settle on the bootstrap prompt; check the pane" >&2
}

herdr tab focus "$tab_id" >/dev/null
echo "  tab       $tab_id (focused)"
echo "  agent     $agent_name (left pane $agent_pane)"
echo "  read tui  herdr pane read $tui_pane --source recent-unwrapped --lines 60"
echo "  read log  jq -c . $dir/log.jsonl | tail -50"
echo "  cleanup   env LILT_SOCKET=$dir/sock \"$repo/lilt\" quit --json"
