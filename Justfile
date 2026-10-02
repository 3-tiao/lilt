# 所有 recipe 经由 scripts/just-tracker 执行：它把“谁（user/ai）何时运行了哪个
# just 命令”追加进 gitignored 的 .just-usage.tsv，再原样 exec zsh -cu。统计用
# `just usage` 查看，长期没人用的 recipe 据此裁剪。相对路径依赖 recipe 默认在
# justfile 目录执行（just 的默认行为），`just -d/--working-dir` 不在本约定内。
set shell := ["scripts/just-tracker", "zsh", "-cu"]

# lilt 的本地开发/测试入口。
#
# 绝大多数 recipe 只是 `lilt` CLI（唯一接口）的薄封装，外加构建/测试/发布辅助。
# CLI 本身见 `lilt help`（人类可读）与 `lilt api --json`（机器可读权威目录）。

root := justfile_directory()
binary := root / "lilt"
player_app := root / "player/Build/Products/Release/lilt-player.app"
audio_app := root / "player/Build/Products/Release/lilt-audio.app"
version := `git describe --tags --always 2>/dev/null || echo 1.0.0`
# Daily prerelease entry point; development binaries live at different paths.
pre := "python3 \"" + root / "scripts/local-workflow.py" + "\""
safe_go := "env -u LILT_APPLE_E2E -u LILT_AUDIUS_E2E -u LILT_MPV_E2E -u LILT_LIVE_PLAYBACK -u LILT_LIVE_RADIO -u LILT_PROBE_AUDIO -u LILT_TEST_AUDIO"

default:
    @just --list

# --- build -------------------------------------------------------------------

# Build the Go CLI/TUI.
build-go:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 1.0.0)" -o "{{binary}}" ./cmd/lilt

# Regenerate the Xcode project from player/project.yml.
[macos]
player-project:
    sh "{{root}}/player/scripts/generate-project.sh"

# Build and automatically sign both macOS helpers. Linux has no Swift helpers,
# so the recipe degrades to a notice and `build` stays one entry point.
[macos]
build-player: player-project
    cd "{{root}}/player" && env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD ./scripts/build-app.sh

[linux]
build-player:
    @echo "swift helpers skipped: they are macOS-only"

# Build lilt and both signed helpers.
build: build-go build-player

# Request Apple Music authorization through the pinned signed app.
[macos]
auth:
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD open -n -W "{{root}}/.lilt-prerelease/current/lilt-player.app" --args --authorize

# Sign both helpers with Developer ID and notarize them (for distribution).
# Requires DEVELOPER_ID_APPLICATION and NOTARY_PROFILE; see docs/product/release.md.
[macos]
notarize: build-player
    sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"
    sh "{{root}}/player/scripts/notarize-app.sh" "{{audio_app}}"

# --- run / debug -------------------------------------------------------------

# Verify/build a candidate and pin immutable CLI + signed helper bundles.
# Refuse to switch while a daily server is active; never overwrite running apps.
promote: verify build
    {{pre}} promote

# Plain TUI: default pre-release on the daily server; --env dev/--fake is private.
run *args:
    bash "{{root}}/scripts/session.sh" --front plain {{args}}

# Herdr tab with an agent on the same private session; same --env/--fake/--browser.
test-drive *args:
    bash "{{root}}/scripts/session.sh" --front herdr {{args}}

# Explicitly stop the recorded daily server (and playback).
stop-daily:
    {{pre}} stop

# Diagnose native MusicKit tokens without printing token contents.
[macos]
doctor:
    {{pre}} cli doctor --json

# --- content shortcuts (thin `lilt` CLI wrappers) ----------------------------

# Search for a song and start full or preview playback.
search term:
    {{pre}} cli search "{{term}}" --play

# Search the catalog once and print stable JSON.
find term:
    {{pre}} cli search "{{term}}" --json

# List recently played songs as JSON.
recent:
    {{pre}} cli recent --json

# List playlists from the authorized user's cloud library.
library:
    {{pre}} cli library --json

# Play a canonical ref or Apple Music URL in the running server.
play reference:
    {{pre}} cli play "{{reference}}" --json

# --- quality -----------------------------------------------------------------

# Run unit tests and static checks: Go everywhere, Swift on macOS only.
test: go-test test-native

# Run the Go half of `test`/`verify`.
[private]
go-test:
    {{safe_go}} go test ./...
    go vet ./...

[macos]
[private]
test-native:
    cd "{{root}}/player" && swift build
    cd "{{root}}/player" && swift test

[linux]
[private]
test-native:
    @echo "swift checks skipped: the helpers are macOS-only"

# Run the provider admission gate: Go tests, race detector, and vet.
provider-gate:
    {{safe_go}} go test -race ./...
    go vet ./...

# Fail when a tracked Go file is not gofmt-formatted.
fmt-check:
    @files="$(gofmt -l $(git ls-files '*.go'))"; if [ -n "$files" ]; then echo "gofmt needed:"; echo "$files"; exit 1; fi
    @echo "gofmt clean"

# Check the published agent skill against the shipped Client API catalog.
skill-check:
    go test ./internal/skillcheck

# Check that client surfaces use the api constants for the public kind enum.
audit-check:
    go test ./internal/auditcheck

# Show which just commands were actually used and by whom (user/ai), from the
# gitignored .just-usage.tsv written by scripts/just-tracker. Never-used
# recipes are deletion candidates.
usage:
    @just --summary | python3 scripts/just-usage.py

# Run credential-free checks suitable for local review and CI.
# recipe 里的 git 命令一律 --no-pager：git 在 stdout 是终端时会对 diff 启动分页器，
# 即使是空 diff 也会打开 less 并停在结尾等按键，让 verify/promote 看起来"卡住"。
verify: fmt-check verify-native workflow-check
    {{safe_go}} go test ./...
    {{safe_go}} go test -race ./...
    go vet ./...
    git --no-pager diff --check

# Hermetic guards for release installation, promotion, and real-round isolation.
workflow-check:
    PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'

[macos]
[private]
verify-native:
    cd "{{root}}/player" && swift build
    cd "{{root}}/player" && swift test

[linux]
[private]
verify-native:
    @echo "swift checks skipped: the helpers are macOS-only"

# Run verification plus the automatic-signing Xcode app build.
[macos]
verify-app: verify build

# --- release -----------------------------------------------------------------

# Fail before building if this cannot become a distributable arm64 package.
[macos]
[private]
release-preflight:
    sh "{{root}}/scripts/release-check.sh" preflight "{{root}}"

# Build Developer ID signed, notarized release artifacts into dist/.
[macos]
release: release-preflight build
    sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"
    sh "{{root}}/player/scripts/notarize-app.sh" "{{audio_app}}"
    sh "{{root}}/scripts/release-check.sh" verify "{{root}}"
    rm -rf dist/stage "dist/lilt-{{version}}-darwin-arm64.tar.gz" "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"
    mkdir -p dist/stage
    cp "{{binary}}" dist/stage/lilt
    cp -R "{{player_app}}" dist/stage/lilt-player.app
    cp -R "{{audio_app}}" dist/stage/lilt-audio.app
    cp -R "{{root}}/skills" dist/stage/skills
    cp "{{root}}/LICENSE" dist/stage/LICENSE
    # Finder writes .DS_Store into the skill tree; it must not reach the artifact.
    tar --exclude '.DS_Store' -czf "dist/lilt-{{version}}-darwin-arm64.tar.gz" -C dist/stage lilt lilt-player.app lilt-audio.app skills LICENSE
    cd dist && shasum -a 256 "lilt-{{version}}-darwin-arm64.tar.gz" | tee "lilt-{{version}}-darwin-arm64.tar.gz.sha256"

# --- agent -------------------------------------------------------------------

# Copy the lilt skill into the harness-global skill directories. ~/.agents/skills
# is the cross-harness user scope (ZCode reads it natively) and shadows the
# repo's .agents/skills copy, so rerun after skill updates; the opencode path
# stays for OpenCode sessions.
agent-install:
    mkdir -p "$HOME/.agents/skills/music-control" && cp "{{root}}/skills/music-control/SKILL.md" "$HOME/.agents/skills/music-control/SKILL.md"
    mkdir -p "$HOME/.config/opencode/skills/music-control" && cp "{{root}}/skills/music-control/SKILL.md" "$HOME/.config/opencode/skills/music-control/SKILL.md"
