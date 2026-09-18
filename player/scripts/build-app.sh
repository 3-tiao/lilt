#!/bin/sh
set -eu

# Xcode manages the development certificate and provisioning profile. Override
# these values only when building under another Apple Developer team.
team=${DEVELOPMENT_TEAM:-9Y6KG228YM}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

common="-quiet -project LiltPlayer.xcodeproj -configuration Release \
  -destination platform=macOS -derivedDataPath .build/xcode \
  DEVELOPMENT_TEAM=$team"

build_scheme() {
  scheme=$1
  bundle_id=$2
  if [ "${LILT_BUILD_UNSIGNED:-}" = "1" ]; then
    # shellcheck disable=SC2086
    xcodebuild $common -scheme "$scheme" PRODUCT_BUNDLE_IDENTIFIER="$bundle_id" \
      CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY=- CODE_SIGNING_REQUIRED=NO CODE_SIGNING_ALLOWED=NO build
  else
    # shellcheck disable=SC2086
    xcodebuild $common -scheme "$scheme" PRODUCT_BUNDLE_IDENTIFIER="$bundle_id" \
      -allowProvisioningUpdates -allowProvisioningDeviceRegistration CODE_SIGN_STYLE=Automatic build
  fi
}

build_scheme LiltPlayer "${PRODUCT_BUNDLE_IDENTIFIER:-com.caiguo.lilt-player}"
build_scheme LiltAudio "${AUDIO_PRODUCT_BUNDLE_IDENTIFIER:-com.caiguo.lilt-audio}"

printf '%s\n' "Build/Products/Release/lilt-player.app"
printf '%s\n' "Build/Products/Release/lilt-audio.app"
