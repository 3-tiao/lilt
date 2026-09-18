set shell := ["zsh", "-cu"]

root := justfile_directory()
binary := root / "lilt"
player_app := root / "player/Build/Products/Release/lilt-player.app"
version := `git describe --tags --always 2>/dev/null || echo 0.1.0`

default:
    @just --list

# Build the Go CLI/TUI.
build-go:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)" -o "{{binary}}" ./cmd/lilt

# Regenerate the Xcode project from player/project.yml.
player-project:
    cd "{{root}}/player" && xcodegen generate

# Sign the helper with Developer ID and notarize it (for distribution).
# Requires DEVELOPER_ID_APPLICATION and NOTARY_PROFILE; see docs/product/release.md.
notarize: build-player
    sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"

# Build and automatically sign the macOS MusicKit helper.
build-player: player-project
    cd "{{root}}/player" && env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD ./scripts/build-app.sh

# Build both lilt and its signed helper.
build: build-go build-player

# Request Apple Music authorization through the signed app.
auth: build-player
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD open -n -W "{{player_app}}" --args --authorize

# Open the foreground TUI on a freshly restarted server.
# The server is long-lived across TUI sessions, so a stale server from an older
# build would reject newer commands (unknown_command); shut it down first.
run: build
    -"{{binary}}" quit --json
    -pkill -f "{{binary}} serve"
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" tui

# Open the foreground TUI only (no rebuild), on a freshly restarted server.
# Assumes ./lilt is already built; use `just build-go` first if it is stale.
tui:
    -"{{binary}}" quit --json
    -pkill -f "{{binary}} serve"
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" tui

# Diagnose native MusicKit tokens without printing token contents.
doctor: build
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" doctor --json

# List playlists from the authorized user's cloud library.
library: build
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" library --json

# Search for a song and start full or preview playback.
search term: build
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" search "{{term}}" --play

# Search the catalog once and print stable JSON.
find term: build
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" search "{{term}}" --json

# List recently played songs as JSON.
recent: build
    env -u FASTLANE_APPLE_APPLICATION_SPECIFIC_PASSWORD LILT_PLAYER_PATH="{{player_app}}" "{{binary}}" recent --json

# Play a canonical ref or Apple Music URL in the running TUI session.
play reference: build-go
    "{{binary}}" play "{{reference}}" --json

# Run the TUI with deterministic fake data and no Apple services.
fake: build-go
    LILT_FAKE_PLAYER=1 "{{binary}}" tui

# Copy the lilt skill into opencode's global skills directory.
agent-install:
    mkdir -p "$HOME/.config/opencode/skills/lilt" && cp "{{root}}/skills/lilt/SKILL.md" "$HOME/.config/opencode/skills/lilt/SKILL.md"

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

# Build signed release artifacts into dist/ (see docs/product/release.md).
release: build
    @if [ -n "${DEVELOPER_ID_APPLICATION:-}" ] && [ -n "${NOTARY_PROFILE:-}" ]; then \
        sh "{{root}}/player/scripts/notarize-app.sh" "{{player_app}}"; \
    else \
        echo "warning: DEVELOPER_ID_APPLICATION/NOTARY_PROFILE unset; artifact is development-signed and NOT distributable"; \
    fi
    rm -rf dist/stage "dist/lilt-{{version}}-darwin-arm64.tar.gz" "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"
    mkdir -p dist/stage
    cp "{{binary}}" dist/stage/lilt
    cp -R "{{player_app}}" dist/stage/lilt-player.app
    tar -czf "dist/lilt-{{version}}-darwin-arm64.tar.gz" -C dist/stage lilt lilt-player.app
    shasum -a 256 "dist/lilt-{{version}}-darwin-arm64.tar.gz" | tee "dist/lilt-{{version}}-darwin-arm64.tar.gz.sha256"
