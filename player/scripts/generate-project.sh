#!/bin/sh
set -eu

# Generate the Xcode project and normalize its object version. Recent xcodegen
# emits the newest project format, which older CI Xcode versions refuse to open
# ("future Xcode project file format (77)"); pin a widely compatible format.
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

xcodegen generate

project="LiltPlayer.xcodeproj/project.pbxproj"
if [ -f "$project" ]; then
  sed -i '' -E 's/objectVersion = [0-9]+;/objectVersion = 56;/' "$project"
fi
