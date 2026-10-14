#!/usr/bin/env bash
# Builds the macOS agenda widget extension into widget/build/RavenWidget.appex,
# which tauri.conf.json copies into Raven.app/Contents/PlugIns. Run it before
# `tauri build` on macOS. Signed ad-hoc: WidgetKit accepts that for a locally
# installed app, and the extension must carry its sandbox entitlement.
set -euo pipefail
cd "$(dirname "$0")"

# Cargo.toml is the app's single version source (see build-desktop.yml).
version="$(sed -n 's/^version = "\(.*\)"/\1/p' ../src-tauri/Cargo.toml | head -n1)"
appex=build/RavenWidget.appex
rm -rf "$appex"
mkdir -p "$appex/Contents/MacOS"
sed "s/__VERSION__/${version}/g" Info.plist > "$appex/Contents/Info.plist"
xcrun swiftc -O -parse-as-library -application-extension \
  -target arm64-apple-macos14.0 -sdk "$(xcrun --sdk macosx --show-sdk-path)" \
  RavenWidget.swift -o "$appex/Contents/MacOS/RavenWidget" \
  -Xlinker -e -Xlinker _NSExtensionMain -framework WidgetKit -framework SwiftUI
codesign --force --sign - --entitlements RavenWidget.entitlements "$appex"
echo "built $appex ($version)"
