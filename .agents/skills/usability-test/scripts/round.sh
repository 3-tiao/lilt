#!/bin/sh
# lilt agent 可用性走查装置：为单个 round 起一个完全隔离的 tmux 会话。
#
# 隔离的东西：socket、state.json、log.jsonl、config、Radio cache、发键记录。
# 不隔离的东西：真实 MusicKit / Radio / 音频（仅 opt-in real 模式）。
#
# 本脚本只碰自己的 session 与自己的 /tmp 目录；绝不列出、attach 或 kill 别的东西。
set -eu
umask 077

script_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$script_dir/../../../.." && pwd)
binary="$repo/lilt"
player_app="$repo/player/Build/Products/Release/lilt-player.app/Contents/MacOS/lilt-player"
audio_app="$repo/player/Build/Products/Release/lilt-audio.app/Contents/MacOS/lilt-audio"
report_root=/tmp/lilt-usability

usage() {
	cat <<'USAGE'
usage:
  round.sh preflight <batch> [--probe] [--fake-only|--real-enabled]
  round.sh start <name> --batch <batch> [--fake|--real] [--cols N] [--rows N]
  round.sh send <name> <key>...
  round.sh capture <name> [--ansi]
  round.sh resize <name> <cols> <rows>
  round.sh status <name>
  round.sh probe <name> <status|sources|auth>   # read-only RPC; never starts a server
  round.sh guard <name>                          # real-only safety stop; run concurrently
  round.sh finish-readonly <name>                # real-only, after stopped status; wait for guard before stop
  round.sh evidence <name>                      # copy full keys into a private report
  round.sh report-check <name> <report.md>      # check prompt, facts and complete keys
  round.sh wait-steady <name> [--stable N] [--tries N] [key...]
  round.sh wait-frame-change <name> [--tries N] [key...]
  round.sh stop <name>
  round.sh paths <name>

  preflight writes an immutable build manifest to /tmp/lilt-usability/<batch>/manifest.txt.
  Default: `just verify && just build-go`, preflight and start in fake mode.
  --real-enabled requires signed helpers on macOS; start --real additionally requires
  LILT_TEST_AUDIO=1 and an idle daily server. --probe marks one isolated task.
  LILT_APPLE_ENGINE is passed through to the isolated server when set in the
  orchestrator environment (e.g. browser mode drives the Apple web player).
  start rejects a binary or helper that differs from its batch manifest, and clears stale data
  for the same round name before creating a fresh private directory. It writes ready only after a first frame.
  A missing ready marker means start was interrupted or failed: stop that round and start a new name.

  --fake (default): fake playback with no audio; live content is NOT deterministic.
  --real: may play audio; requires explicit user approval, --real-enabled manifest and LILT_TEST_AUDIO=1.
  send keys use tmux names (Enter/Escape/Space/Tab/BSpace/Up/Down/Left/Right/C-c…); other keys are literal.
  wait-* poll every 0.5s; they print the last frame and exit 0 when their stated frame condition is met.
  wait-frame-change only proves that the captured frame changed; it does not prove that a key was handled.
  Trailing keys are sent after the baseline is captured and written to keys.log.
  probe uses the round's Unix socket directly, without invoking the auto-starting lilt CLI.
  It only permits session.status, sources.list and authorization.list; never send a mutation.
USAGE
	exit 2
}

valid_name() {
	case "$1" in
	"" | *[!a-zA-Z0-9_-]*) return 1 ;;
	*) return 0 ;;
	esac
}

positive_integer() {
	case "$1" in
	*[!0-9]* | "") return 1 ;;
	*) [ "$1" -gt 0 ] ;;
	esac
}

sha256() {
	shasum -a 256 "$1" | awk '{print $1}'
}

worktree_fingerprint() {
	git -C "$repo" status --porcelain | shasum -a 256 | awk '{print $1}'
}

[ $# -ge 2 ] || usage
command=$1
name=$2
shift 2

if ! valid_name "$name"; then
	echo "round.sh: name/batch 只能是字母/数字/下划线/连字符" >&2
	exit 2
fi

session="lilt-round-$name"
dir="/tmp/lilt-round-$name"
batch_dir="$report_root/$name"
mode=fake
cols=110
rows=30
ansi=0
batch=""
run_kind=batch
fake_only=1

# fake 模式的 TUI 在专用 window 里（serve 占着主 window）；real 模式只有一个 window。
# 按实际存在的 window 解析，调用方不必记得自己用的是哪种模式。
target() {
	if tmux list-windows -t "$session" -F '#{window_name}' 2>/dev/null | grep -qx tui; then
		echo "$session:tui"
	else
		echo "$session"
	fi
}

# 按键按 tmux 键名解释；不在名单里的按字面字符发送（避免被当成键名解析）。
send_keys() {
	for key in "$@"; do
		case "$key" in
		Enter | Escape | Space | Tab | BSpace | Up | Down | Left | Right | Home | End | PageUp | PageDown | C-c | C-d | C-u | C-b | C-f)
			tmux send-keys -t "$(target)" "$key"
			;;
		*)
			tmux send-keys -t "$(target)" -l "$key"
			;;
		esac
	done
}

record_keys() {
	[ $# -gt 0 ] || return 0
	printf '%s' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >>"$dir/keys.log"
	for key in "$@"; do
		printf ' %s' "$key" >>"$dir/keys.log"
	done
	printf '\n' >>"$dir/keys.log"
}

manifest_value() {
	awk -v key="$1" '$1 == key ":" {print $2; exit}' "$2"
}

# Probe a Unix listener without issuing a Client API command. `lilt status`
# auto-starts a missing server, so it must never be used as a liveness probe.
socket_reachable() {
	python3 - "$1" <<'PY'
import socket
import sys

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(0.2)
try:
    sock.connect(sys.argv[1])
except OSError:
    sys.exit(1)
finally:
    sock.close()
PY
}

require_manifest() {
	[ -n "$batch" ] || {
		echo "round.sh: start 必须带 --batch <batch>；先运行 preflight" >&2
		exit 2
	}
	batch_dir="$report_root/$batch"
	manifest="$batch_dir/manifest.txt"
	[ -f "$manifest" ] || {
		echo "round.sh: 缺少 $manifest；先运行 preflight" >&2
		exit 1
	}
	expected_worktree=$(manifest_value worktree_sha256 "$manifest")
	actual_worktree=$(worktree_fingerprint)
	[ "$expected_worktree" = "$actual_worktree" ] || {
		echo "round.sh: worktree 在 preflight 后变化；结束此 run，重新 verify/build/preflight" >&2
		exit 1
	}
	expected=$(manifest_value lilt_sha256 "$manifest")
	actual=$(sha256 "$binary")
	[ "$expected" = "$actual" ] || {
		echo "round.sh: ./lilt 与 batch manifest 不一致；重新 preflight 一个新 batch" >&2
		exit 1
	}
	if [ "$mode" = real ]; then
		[ "$(manifest_value batch_mode "$manifest")" != fake-only ] || {
			echo "round.sh: fake-only batch 不能启动 real round" >&2
			exit 1
		}
		if [ "$(uname -s)" = Darwin ]; then
			for helper in "$player_app" "$audio_app"; do
				[ -x "$helper" ] || {
					echo "round.sh: 缺少 $helper；先跑 just build" >&2
					exit 1
				}
			done
			[ "$(manifest_value player_sha256 "$manifest")" = "$(sha256 "$player_app")" ] || {
				echo "round.sh: lilt-player 与 batch manifest 不一致；重新 preflight 一个新 batch" >&2
				exit 1
			}
			[ "$(manifest_value audio_sha256 "$manifest")" = "$(sha256 "$audio_app")" ] || {
				echo "round.sh: lilt-audio 与 batch manifest 不一致；重新 preflight 一个新 batch" >&2
				exit 1
			}
		fi
	fi
}

while [ $# -gt 0 ]; do
	case "$1" in
	--fake)
		mode=fake
		shift
		;;
	--fake-only)
		fake_only=1
		shift
		;;
	--real-enabled)
		fake_only=0
		shift
		;;
	--real)
		mode=real
		shift
		;;
	--probe)
		run_kind=probe
		shift
		;;
	--batch)
		[ $# -ge 2 ] || usage
		batch=$2
		valid_name "$batch" || {
			echo "round.sh: batch 只能是字母/数字/下划线/连字符" >&2
			exit 2
		}
		shift 2
		;;
	--cols)
		[ $# -ge 2 ] && positive_integer "$2" || usage
		cols=$2
		shift 2
		;;
	--rows)
		[ $# -ge 2 ] && positive_integer "$2" || usage
		rows=$2
		shift 2
		;;
	--ansi)
		ansi=1
		shift
		;;
	*)
		break
		;;
	esac
done

# Socket 目录必须是私有目录：server 锁是 <socket 目录>/server.lock。
env_prefix="LILT_SOCKET=$dir/session.sock LILT_STATE=$dir/state.json LILT_LOG=$dir/log.jsonl LILT_CONFIG=$dir/config LILT_RADIO_CACHE=$dir/radio-cache.json"
# Apple 引擎模式从编排者环境透传（helper 默认；browser 走浏览器引擎）。隔离 server
# 与编排者共享同一个机器级浏览器 profile，这是该 profile 的设计语义，不是泄漏。
if [ -n "${LILT_APPLE_ENGINE:-}" ]; then
	env_prefix="$env_prefix LILT_APPLE_ENGINE=$LILT_APPLE_ENGINE"
fi
if [ "$mode" = fake ]; then
	env_prefix="$env_prefix LILT_FAKE_PLAYER=1"
fi

case "$command" in
preflight)
	[ $# -eq 0 ] || usage
	[ -x "$binary" ] || {
		echo "round.sh: 缺少 $binary；先跑 just verify && just build" >&2
		exit 1
	}
	if [ "$fake_only" -ne 1 ] && [ "$(uname -s)" = Darwin ]; then
		for helper in "$player_app" "$audio_app"; do
			[ -x "$helper" ] || {
				echo "round.sh: 缺少 $helper；先跑 just verify && just build" >&2
				exit 1
			}
		done
	fi
	mkdir -p "$batch_dir"
	chmod 700 "$batch_dir"
	manifest="$batch_dir/manifest.txt"
	[ ! -e "$manifest" ] || {
		echo "round.sh: $manifest 已存在；每次构建使用新的 batch 名" >&2
		exit 1
	}
	{
		echo "batch: $name"
		echo "run_kind: $run_kind"
		echo "created_at: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
		echo "commit: $(git -C "$repo" rev-parse HEAD)"
		echo "worktree_sha256: $(worktree_fingerprint)"
		echo "batch_mode: $([ "$fake_only" -eq 1 ] && echo fake-only || echo mixed)"
		echo "lilt_sha256: $(sha256 "$binary")"
		if [ "$fake_only" -eq 1 ] || [ "$(uname -s)" != Darwin ]; then
			echo "player_sha256: unavailable"
			echo "audio_sha256: unavailable"
		else
			echo "player_sha256: $(sha256 "$player_app")"
			echo "audio_sha256: $(sha256 "$audio_app")"
		fi
		echo "worktree:"
		git -C "$repo" status --porcelain
	} >"$manifest"
	echo "$run_kind $name 已就绪；manifest: $manifest"
	;;
start)
	[ $# -eq 0 ] || usage
	require_manifest
	if tmux has-session -t "$session" 2>/dev/null; then
		echo "round.sh: session $session 已存在；先 stop 或换名字" >&2
		exit 1
	fi
	if [ -S "$dir/session.sock" ] && socket_reachable "$dir/session.sock"; then
		echo "round.sh: $dir/session.sock 仍有 server；先 stop，不能 unlink 活 server 的 socket" >&2
		exit 1
	fi
	if [ "$mode" = real ]; then
		[ "${LILT_TEST_AUDIO:-}" = 1 ] || {
			echo "round.sh: real playback needs LILT_TEST_AUDIO=1 and explicit user approval" >&2
			exit 3
		}
		python3 "$repo/scripts/local-workflow.py" reserve-real "$dir/session.sock" || exit 3
	fi
	# 同名 round 代表新一轮：清掉旧 state/log/keys，避免测试相互污染。
	rm -rf "$dir"
	mkdir -m 700 "$dir"
	: >"$dir/keys.log"
	printf '%s\n' "$mode" >"$dir/mode"
	if [ "$mode" = fake ]; then
		tmux new-session -d -s "$session" -x "$cols" -y "$rows" \
			"cd '$repo' && $env_prefix ./lilt serve --fake"
		# The TUI must not race server startup: wait for the socket before
		# opening the TUI window. A server that never binds fails the start.
		i=0
		while [ "$i" -lt 40 ]; do
			if [ -S "$dir/session.sock" ] && socket_reachable "$dir/session.sock"; then
				break
			fi
			i=$((i + 1))
			sleep 0.5
		done
		if [ ! -S "$dir/session.sock" ] || ! socket_reachable "$dir/session.sock"; then
			echo "round.sh: 隔离 server 20 秒内没有就绪；此 round 无效，运行 stop 后换新名字重开" >&2
			exit 1
		fi
		tmux new-window -t "$session" -n tui \
			"cd '$repo' && $env_prefix ./lilt tui"
	else
		tmux new-session -d -s "$session" -x "$cols" -y "$rows" \
			"cd '$repo' && $env_prefix ./lilt tui"
	fi
	# A usable round needs a rendered first frame. Until ready exists, an interrupted
	# start is invalid and must never be handed to a participant.
	first_frame=""
	i=0
	while [ "$i" -lt 40 ]; do
		current=$(tmux capture-pane -t "$(target)" -p 2>/dev/null || true)
		if [ -n "$(printf '%s' "$current" | tr -d '[:space:]')" ]; then
			first_frame=$current
			break
		fi
		i=$((i + 1))
		sleep 0.5
	done
	if [ -z "$first_frame" ]; then
		echo "round.sh: 20 秒内没有首帧；此 round 无效，运行 stop 后换新名字重开" >&2
		exit 1
	fi
	: >"$dir/ready"
	echo "round $name 已启动（batch=$batch mode=$mode ${cols}x${rows}）；socket 目录 $dir"
	printf '%s\n' "$first_frame"
	;;
send)
	[ $# -gt 0 ] || usage
	record_keys "$@"
	send_keys "$@"
	;;
capture)
	[ $# -eq 0 ] || usage
	if [ "$ansi" = 1 ]; then
		tmux capture-pane -t "$(target)" -p -e
	else
		tmux capture-pane -t "$(target)" -p
	fi
	;;
resize)
	[ $# -eq 2 ] && positive_integer "$1" && positive_integer "$2" || usage
	tmux resize-window -t "$session" -x "$1" -y "$2"
	echo "round $name 已调整为 $1x$2"
	;;
status)
	[ $# -eq 0 ] || usage
	[ -f "$dir/ready" ] || {
		echo "round.sh: 缺少 $dir/ready；start 被中断或未完成，此 round 无效，先 stop 后换新名字重开" >&2
		exit 1
	}
	tmux has-session -t "$session" 2>/dev/null || {
		echo "tmux: session 不存在" >&2
		exit 1
	}
	echo "tmux: running ($(target))"
	if socket_reachable "$dir/session.sock"; then
		echo "server: reachable"
	else
		echo "server: unreachable" >&2
		exit 1
	fi
	;;
probe)
	[ $# -eq 1 ] || usage
	[ -f "$dir/ready" ] || {
		echo "round.sh: $name 未 ready；只读探针拒绝访问，也不会启动 server" >&2
		exit 1
	}
	case "$1" in
	status) method=session.status ;;
	sources) method=sources.list ;;
	auth) method=authorization.list ;;
	*) echo "round.sh: 只读探针仅支持 status|sources|auth" >&2; exit 2 ;;
	esac
	# Do not use `lilt status/sources`: on a missing socket the CLI can start a
	# server with the default state root. Connect only to this round's socket.
	python3 - "$dir/session.sock" "$method" <<'PY'
import json
import socket
import sys
import uuid

path, method = sys.argv[1:]
try:
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
        conn.settimeout(5)
        conn.connect(path)
        # The server deduplicates requestIds across commands. Each invocation
        # must have its own ID, even when all probes are read-only.
        request_id = "usability-probe-" + uuid.uuid4().hex
        request = {"requestId": request_id, "command": method}
        conn.sendall((json.dumps(request) + "\n").encode())
        with conn.makefile("rb") as stream:
            line = stream.readline(4 * 1024 * 1024 + 1)
    if not line or len(line) > 4 * 1024 * 1024 or not line.endswith(b"\n"):
        raise ValueError("missing or oversized response")
    response = json.loads(line)
    if not isinstance(response, dict) or response.get("requestId") != request_id:
        raise ValueError("mismatched requestId")
except (OSError, ValueError, json.JSONDecodeError) as exc:
    print(f"round.sh: isolated probe failed ({exc}); no server was started", file=sys.stderr)
    sys.exit(1)
print(json.dumps(response, ensure_ascii=False))
sys.exit(0 if response.get("ok") else 1)
PY
	;;
guard)
	[ $# -eq 0 ] || usage
	[ -f "$dir/ready" ] && [ "$(cat "$dir/mode")" = real ] || {
		echo "round.sh: guard 只允许已 ready 的 real round" >&2
		exit 1
	}
	python3 "$script_dir/audio-guard.py" "$dir"
	;;
evidence)
	[ $# -eq 0 ] || usage
	[ -f "$dir/keys.log" ] || { echo "round.sh: 缺少 $dir/keys.log" >&2; exit 1; }
	printf '## 完整键序\n\n```text\n'
	cat "$dir/keys.log"
	printf '```\n'
	;;
report-check)
	[ $# -eq 1 ] || usage
	[ -f "$dir/keys.log" ] && [ -f "$1" ] || {
		echo "round.sh: 报告或 $dir/keys.log 不存在" >&2
		exit 1
	}
	python3 - "$dir/keys.log" "$1" <<'PY'
from pathlib import Path
import sys

keys = Path(sys.argv[1]).read_text()
report = Path(sys.argv[2]).read_text()
headers = ("## 参与者 prompt", "## 探针目标与命令")

def section(header):
    if header not in report:
        return ""
    return report.split(header, 1)[1].split("\n## ", 1)[0].strip()

if not any(section(header) for header in headers) or not section("## 屏幕事实与复核"):
    sys.exit("round.sh: 报告缺 prompt/探针目标或屏幕事实与复核章节")
marker = "## 完整键序\n\n```text\n"
if marker not in report or "```" not in report.split(marker, 1)[1]:
    sys.exit("round.sh: 报告缺 `round.sh evidence` 生成的完整键序块")
recorded = report.split(marker, 1)[1].split("```", 1)[0]
if recorded != keys:
    sys.exit("round.sh: 报告键序与本轮 keys.log 不一致；先核对再清理")
print("round.sh: 报告结构和完整键序已核对；屏幕事实与结论仍须人工复核")
PY
	;;
wait-steady)
	stable=2
	tries=40
	while [ $# -gt 0 ]; do
		case "$1" in
		--stable)
			[ $# -ge 2 ] && positive_integer "$2" || usage
			stable=$2
			shift 2
			;;
		--tries)
			[ $# -ge 2 ] && positive_integer "$2" || usage
			tries=$2
			shift 2
			;;
		*) break ;;
		esac
	done
	frame=""
	same=0
	i=0
	record_keys "$@"
	send_keys "$@"
	while [ "$i" -lt "$tries" ]; do
		sleep 0.5
		current=$(tmux capture-pane -t "$(target)" -p 2>/dev/null || true)
		if [ "$current" = "$frame" ]; then
			same=$((same + 1))
		else
			same=0
		fi
		if [ "$same" -ge "$stable" ]; then
			echo "$current"
			exit 0
		fi
		frame=$current
		i=$((i + 1))
	done
	echo "$frame"
	echo "round.sh: 画面在 $tries 次轮询内未静止（播放中会持续变化）" >&2
	exit 1
	;;
wait-frame-change)
	tries=10
	while [ $# -gt 0 ]; do
		case "$1" in
		--tries)
			[ $# -ge 2 ] && positive_integer "$2" || usage
			tries=$2
			shift 2
			;;
		*) break ;;
		esac
	done
	base=$(tmux capture-pane -t "$(target)" -p 2>/dev/null || true)
	record_keys "$@"
	send_keys "$@"
	i=0
	while [ "$i" -lt "$tries" ]; do
		sleep 0.5
		current=$(tmux capture-pane -t "$(target)" -p 2>/dev/null || true)
		if [ "$current" != "$base" ]; then
			echo "$current"
			exit 0
		fi
		i=$((i + 1))
	done
	echo "$base"
	echo "round.sh: 画面在 $tries 次轮询内没有变化；这不能单独证明应用或按键失效" >&2
	exit 1
	;;
finish-readonly)
	[ $# -eq 0 ] || usage
	[ -f "$dir/ready" ] && [ -f "$dir/mode" ] && [ "$(cat "$dir/mode")" = real ] || {
		echo "round.sh: finish-readonly requires a ready real round" >&2
		exit 1
	}
	# The guard confirms stopped on the next direct status probe before honoring
	# this marker. Never signal a read-only finish for a round that has played.
	: >"$dir/guard-finish-readonly"
	;;
stop)
	[ $# -eq 0 ] || usage
	tmux send-keys -t "$(target)" -l q 2>/dev/null || true
	sleep 1
	tmux kill-session -t "$session" 2>/dev/null || true
	# In real mode the server outlives the TUI and must receive an explicit shutdown.
	# Shutdown can return before helper cleanup finishes; wait for socket removal instead of unlinking a live server socket.
	quit_output=$(LILT_SOCKET="$dir/session.sock" LILT_LOG="$dir/log.jsonl" "$binary" quit --json 2>&1 || true)
	# A killed fake tmux session leaves a stale filesystem entry. quit explicitly
	# identifies that case, so removing it cannot disconnect a running server.
	case "$quit_output" in
	*'"reason":"no_active_session"'*) rm -f "$dir/session.sock" ;;
	esac
	i=0
	while [ -S "$dir/session.sock" ] && [ "$i" -lt 20 ]; do
		i=$((i + 1))
		sleep 0.5
	done
	if [ -S "$dir/session.sock" ]; then
		echo "round.sh: server 10 秒后仍在运行；保留 $dir/session.sock，勿复用该 round 名" >&2
		exit 1
	fi
	rm -f "$dir/ready"
	python3 "$repo/scripts/local-workflow.py" release-real "$dir/session.sock"
	echo "round $name 已停止；state/log/config/cache/keys 保留在 $dir 供本批复核"
	;;
paths)
	[ $# -eq 0 ] || usage
	cat <<EOF
session:     $session
target:      $(target)
socket:      $dir/session.sock
state:       $dir/state.json
log:         $dir/log.jsonl
config:      $dir/config
radio_cache: $dir/radio-cache.json
keys:        $dir/keys.log
ready:       $dir/ready
EOF
	;;
*)
	usage
	;;
esac
