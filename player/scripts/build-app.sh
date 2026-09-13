#!/bin/sh
set -eu

# Xcode manages the development certificate and provisioning profile. Override
# these values only when building under another Apple Developer team.
bundle_id=${PRODUCT_BUNDLE_IDENTIFIER:-com.caiguo.lilt-player}
team=${DEVELOPMENT_TEAM:-9Y6KG228YM}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
xcodebuild \
  -quiet \
  -project LiltPlayer.xcodeproj \
  -scheme LiltPlayer \
  -configuration Release \
  -destination 'platform=macOS' \
  -derivedDataPath .build/xcode \
  -allowProvisioningUpdates \
  -allowProvisioningDeviceRegistration \
  DEVELOPMENT_TEAM="$team" \
  PRODUCT_BUNDLE_IDENTIFIER="$bundle_id" \
  CODE_SIGN_STYLE=Automatic \
  build
app="Build/Products/Release/lilt-player.app"
printf '%s\n' "$app"
