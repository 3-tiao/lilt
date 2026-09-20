set shell := ["zsh", "-cu"]

# lilt 的本地开发/测试入口。
#
# 绝大多数 recipe 只是 `lilt` CLI（唯一接口）的薄封装，外加构建/测试/发布辅助。
# CLI 本身见 `lilt help`（人类可读）与 `lilt api --json`（机器可读权威目录）。

root := justfile_directory()
binary := root / "lilt"
player_app := root / "player/Build/Products/Release/lilt-player.app"
audio_app := root / "player/Build/Products/Release/lilt-audio.app"
version := `git describe --tags --always 2>/dev/null || echo 0.1.0`
# lilt CLI + 本机签名 helper 路径，供本地运行使用。
lilt := "env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH=\"" + player_app + "\" LILT_AUDIO_PATH=\"" + audio_app + "\" \"" + binary + "\""

default:
    @just --list

# --- build -------------------------------------------------------------------

# Build the Go CLI/TUI.
build-go:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)" -o "{{binary}}" ./cmd/lilt

# Regenerate the Xcode project from player/project.yml.
player-project:
    sh "{{root}}/player/scripts/generate-project.sh"

# Build and automatically sign both macOS helpers.
build-player: player-project
    cd "{{root}}/player" && env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD ./scripts/build-app.sh

# Build lilt and both signed helpers.
build: build-go build-player

# Request Apple Music authorization through the signed app.
auth: build-player
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD open -n -W "{{player_app}}" --args --authorize

# Sign both helpers with Developer ID and notarize them (for distribution).
# Requires DEVELOPER_ID_APPLICATION and NOTARY_PROFILE; see docs/product/release.md.
notarize: build-player
    sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"
    sh "{{root}}/player/scripts/notarize-app.sh" "{{audio_app}}"

# --- run / debug -------------------------------------------------------------

# Build and open the TUI on a freshly restarted server.
run: build
    -"{{binary}}" quit --json
    -pkill -f "{{binary}} serve"
    {{lilt}} tui

# Open the TUI only (no rebuild) and attach to the running server.
# It does NOT stop the server, so current playback and the queue stay visible.
tui:
    {{lilt}} tui

# Stop the running server and helper (stops playback). Use after changing
# server-side code so the next launch uses the rebuilt binary.
restart:
    -"{{binary}}" quit --json
    -pkill -f "{{binary}} serve"

# Run the TUI with deterministic fake data and no Apple services.
fake: build-go
    LILT_FAKE_PLAYER=1 "{{binary}}" tui

# Diagnose native MusicKit tokens without printing token contents.
doctor: build
    {{lilt}} doctor --json

# --- content shortcuts (thin `lilt` CLI wrappers) ----------------------------

# Search for a song and start full or preview playback.
search term: build
    {{lilt}} search "{{term}}" --play

# Search the catalog once and print stable JSON.
find term: build
    {{lilt}} search "{{term}}" --json

# List recently played songs as JSON.
recent: build
    {{lilt}} recent --json

# List playlists from the authorized user's cloud library.
library: build
    {{lilt}} library --json

# Play a canonical ref or Apple Music URL in the running server.
play reference: build-go
    "{{binary}}" play "{{reference}}" --json

# --- quality -----------------------------------------------------------------

# Run unit tests and static checks.
test:
    go test ./...
    go vet ./...
    cd "{{root}}/player" && swift build
    cd "{{root}}/player" && swift test

# Run the provider admission gate: Go tests, race detector, and vet.
provider-gate:
    go test -race ./...
    go vet ./...

# Verify repository-local Markdown links under docs/.
docs-check:
    python3 "{{root}}/scripts/check-doc-links.py"

# Run credential-free checks suitable for local review and CI.
verify: docs-check
    go test ./...
    go test -race ./...
    go vet ./...
    cd "{{root}}/player" && swift build
    cd "{{root}}/player" && swift test
    git diff --check

# Run verification plus the automatic-signing Xcode app build.
verify-app: verify build

# --- release -----------------------------------------------------------------

# Build signed release artifacts into dist/ (see docs/product/release.md).
release: build
    @if [ -n "${DEVELOPER_ID_APPLICATION:-}" ] && [ -n "${NOTARY_PROFILE:-}" ]; then \
        sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"; \
        sh "{{root}}/player/scripts/notarize-app.sh" "{{audio_app}}"; \
    else \
        echo "warning: DEVELOPER_ID_APPLICATION/NOTARY_PROFILE unset; artifact is development-signed and NOT distributable"; \
    fi
    rm -rf dist/stage "dist/lilt-{{version}}-darwin-arm64.tar.gz" "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"
    mkdir -p dist/stage
    cp "{{binary}}" dist/stage/lilt
    cp -R "{{player_app}}" dist/stage/lilt-player.app
    cp -R "{{audio_app}}" dist/stage/lilt-audio.app
    tar -czf "dist/lilt-{{version}}-darwin-arm64.tar.gz" -C dist/stage lilt lilt-player.app lilt-audio.app
    shasum -a 256 "dist/lilt-{{version}}-darwin-arm64.tar.gz" | tee "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"

# --- agent -------------------------------------------------------------------

# Copy the lilt skill into opencode's global skills directory.
agent-install:
    mkdir -p "$HOME/.config/opencode/skills/music-control" && cp "{{root}}/skills/music-control/SKILL.md" "$HOME/.config/opencode/skills/music-control/SKILL.md"
