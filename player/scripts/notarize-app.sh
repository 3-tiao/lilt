#!/bin/sh
set -eu

# Sign the helper with a Developer ID Application identity and notarize it, so
# the artifact can be distributed (Homebrew/Gatekeeper). Requires:
#   DEVELOPER_ID_APPLICATION  e.g. "Developer ID Application: Name (TEAMID)"
#   NOTARY_PROFILE            a `xcrun notarytool store-credentials` profile
# See docs/product/release.md.
app=${1:-Build/Products/Release/lilt-player.app}
identity=${DEVELOPER_ID_APPLICATION:?set DEVELOPER_ID_APPLICATION to a Developer ID Application identity}
profile=${NOTARY_PROFILE:?set NOTARY_PROFILE to a notarytool keychain profile}

if [ ! -d "$app" ]; then
  echo "no app bundle at $app (run just build-player first)" >&2
  exit 1
fi

echo "signing $app with $identity"
# Sign nested code inside-out; --deep is deprecated and does not seal properly.
if [ -d "$app/Contents/Frameworks" ]; then
  find "$app/Contents/Frameworks" -maxdepth 1 -name '*.framework' -print | while read -r framework; do
    codesign --force --timestamp --options runtime --sign "$identity" "$framework"
  done
fi
codesign --force --timestamp --options runtime --sign "$identity" "$app"
codesign --verify --strict --verbose=2 "$app"

echo "notarizing with profile $profile"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
ditto -c -k --keepParent "$app" "$tmp/lilt-player.zip"
xcrun notarytool submit "$tmp/lilt-player.zip" --keychain-profile "$profile" --wait
xcrun stapler staple "$app"
xcrun stapler validate "$app"
spctl -a -vv --type execute "$app" || true
echo "notarized $app"
