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
version := `git describe --tags --always 2>/dev/null || echo 0.1.0`
# Daily prerelease entry point; development binaries live at different paths.
pre := "python3 \"" + root / "scripts/local-workflow.py" + "\""
safe_go := "env -u LILT_APPLE_E2E -u LILT_AUDIUS_E2E -u LILT_MPV_E2E -u LILT_LIVE_PLAYBACK -u LILT_LIVE_RADIO -u LILT_PROBE_AUDIO -u LILT_TEST_AUDIO"

default:
    @just --list

# --- build -------------------------------------------------------------------

# Build the Go CLI/TUI.
build-go:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)" -o "{{binary}}" ./cmd/lilt

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

# Daily prerelease: attach or start only the pinned build, never rebuild/kill.
# Engine changes require an explicit `just stop-pre` first.
run:
    {{pre}} run

run-browser:
    {{pre}} run-browser

tui:
    {{pre}} tui

# Deliberately stop just the recorded daily server (and playback).
stop-pre:
    {{pre}} stop

# Development fake TUI: private state/socket/log, in-memory credentials, no audio.
fake: build-go
    {{pre}} fake

# Real Apple Music Herdr test-drive session (idle daily server + LILT_TEST_AUDIO=1); fake dev sessions use `just fake`.
test-drive: build
    bash "{{root}}/scripts/test-drive.sh"

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

# Show which just commands were actually used and by whom (user/ai), from the
# gitignored .just-usage.tsv written by scripts/just-tracker. Never-used
# recipes are deletion candidates.
usage:
    @just --summary | python3 scripts/just-usage.py

# Run credential-free checks suitable for local review and CI.
verify: fmt-check verify-native workflow-check
    {{safe_go}} go test ./...
    {{safe_go}} go test -race ./...
    go vet ./...
    git diff --check

# Hermetic guards for promotion, no-audio default and run isolation.
workflow-check:
    PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_local_workflow.py'

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
    # Finder writes .DS_Store into the skill tree; it must not reach the artifact.
    tar --exclude '.DS_Store' -czf "dist/lilt-{{version}}-darwin-arm64.tar.gz" -C dist/stage lilt lilt-player.app lilt-audio.app skills
    shasum -a 256 "dist/lilt-{{version}}-darwin-arm64.tar.gz" | tee "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"

# --- agent -------------------------------------------------------------------

# Copy the lilt skill into opencode's global skills directory.
agent-install:
    mkdir -p "$HOME/.config/opencode/skills/music-control" && cp "{{root}}/skills/music-control/SKILL.md" "$HOME/.config/opencode/skills/music-control/SKILL.md"
