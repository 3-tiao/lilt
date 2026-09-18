#!/bin/sh
set -eu

# Xcode manages the development certificate and provisioning profile. Override
# these values only when building under another Apple Developer team.
bundle_id=${PRODUCT_BUNDLE_IDENTIFIER:-com.caiguo.lilt-player}
team=${DEVELOPMENT_TEAM:-9Y6KG228YM}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

common="-quiet -project LiltPlayer.xcodeproj -scheme LiltPlayer -configuration Release \
  -destination platform=macOS -derivedDataPath .build/xcode \
  DEVELOPMENT_TEAM=$team PRODUCT_BUNDLE_IDENTIFIER=$bundle_id"

if [ "${LILT_BUILD_UNSIGNED:-}" = "1" ]; then
  # CI/release: build unsigned and let player/scripts/notarize-app.sh apply the
  # Developer ID signature. Keychain-free, so it does not need an Apple ID session.
  # shellcheck disable=SC2086
  xcodebuild $common CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY=- \
    CODE_SIGNING_REQUIRED=NO CODE_SIGNING_ALLOWED=NO build
else
  # shellcheck disable=SC2086
  xcodebuild $common -allowProvisioningUpdates -allowProvisioningDeviceRegistration \
    CODE_SIGN_STYLE=Automatic build
fi

printf '%s\n' "Build/Products/Release/lilt-player.app"
