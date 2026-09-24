#!/bin/sh
set -eu

# Public macOS beta packages are arm64, built from one clean, tagged commit.
# Run preflight before building and verify after notarizing both helpers.
action=${1:?usage: release-check.sh preflight|verify REPO}
root=${2:?usage: release-check.sh preflight|verify REPO}
cd "$root"

fail() { echo "release: $*" >&2; exit 1; }

case "$action" in
preflight)
    [ "$(uname -s)" = Darwin ] || fail "public beta packaging requires macOS"
    [ "$(uname -m)" = arm64 ] || fail "darwin-arm64 package requires an arm64 build host"
    [ -n "${DEVELOPER_ID_APPLICATION:-}" ] || fail "DEVELOPER_ID_APPLICATION is required for public distribution"
    [ -n "${NOTARY_PROFILE:-}" ] || fail "NOTARY_PROFILE is required for public distribution"
    [ "${LILT_BUILD_UNSIGNED:-}" != 1 ] || fail "LILT_BUILD_UNSIGNED cannot be used for a release"
    tag=$(git describe --exact-match --tags HEAD 2>/dev/null) || fail "HEAD must have a release tag"
    printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail "release tag must be vX.Y.Z"
    grep -Fqx "var version = \"${tag#v}\"" cmd/lilt/main.go || fail "cmd/lilt/main.go version must match $tag"
    [ -z "$(git status --porcelain)" ] || fail "working tree must be clean before building a release"
    ;;
verify)
    for executable in lilt \
        player/Build/Products/Release/lilt-player.app/Contents/MacOS/lilt-player \
        player/Build/Products/Release/lilt-audio.app/Contents/MacOS/lilt-audio; do
        [ -f "$executable" ] || fail "missing $executable"
        lipo -archs "$executable" | grep -Eq '(^| )arm64( |$)' || fail "$executable is not arm64"
    done
    for app in player/Build/Products/Release/lilt-player.app player/Build/Products/Release/lilt-audio.app; do
        codesign --verify --strict "$app" || fail "invalid signature: $app"
        codesign -dv --verbose=4 "$app" 2>&1 | grep -Fq 'Authority=Developer ID Application:' || fail "not Developer ID signed: $app"
        xcrun stapler validate "$app" || fail "notary ticket missing: $app"
        spctl -a --type execute "$app" || fail "Gatekeeper rejected: $app"
    done
    ;;
*) fail "unknown check $action" ;;
esac
