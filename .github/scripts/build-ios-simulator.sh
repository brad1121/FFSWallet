#!/usr/bin/env bash
# Builds FFSWallet.app for the iOS Simulator, ad-hoc signed.
#
# `fyne package -os iossimulator` cannot run in CI: it looks up an Apple
# development certificate in the keychain to find a team ID even for a
# simulator build, and a runner has none. What it does with that team ID is
# run xcodebuild over a project whose only content is the prebuilt Go
# executable, so this script does the same work directly: cross-compile with
# the simulator SDK, write the bundle, sign it ad hoc (all the simulator
# needs).
#
# A build for a physical device needs a real certificate and provisioning
# profile from an Apple Developer account; see README.
#
# Usage: build-ios-simulator.sh <out_dir>
# Env:   APP_VERSION (default 0.0.0), APP_BUILD (default 1),
#        BUNDLE_ID (default com.ffswallet.mobile)
set -euo pipefail

out_dir="${1:?output directory required}"
version="${APP_VERSION:-0.0.0}"
build="${APP_BUILD:-1}"
bundle_id="${BUNDLE_ID:-com.ffswallet.mobile}"
min_ios="15.0"

# The simulator runs the host's architecture.
goarch="$(go env GOHOSTARCH)"
case "$goarch" in
  arm64) clang_arch=arm64 ;;
  amd64) clang_arch=x86_64 ;;
  *) echo "unsupported host arch $goarch" >&2; exit 1 ;;
esac

sdk="$(xcrun --sdk iphonesimulator --show-sdk-path)"
clang="$(xcrun --sdk iphonesimulator --find clang)"
flags="-isysroot $sdk -mios-simulator-version-min=$min_ios -arch $clang_arch"

app="$out_dir/FFSWallet.app"
rm -rf "$app"
mkdir -p "$app"

GOOS=ios GOARCH="$goarch" CGO_ENABLED=1 \
  CC="$clang" CXX="$clang++" \
  CGO_CFLAGS="$flags" CGO_CXXFLAGS="$flags" CGO_LDFLAGS="$flags" \
  go build -ldflags=-w -o "$app/main" ./cmd/ffswallet

sips -Z 120 assets/icon.png --out "$app/AppIcon60x60@2x.png" >/dev/null
sips -Z 180 assets/icon.png --out "$app/AppIcon60x60@3x.png" >/dev/null

cat > "$app/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>main</string>
  <key>CFBundleIdentifier</key>
  <string>${bundle_id}</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>FFSWallet</string>
  <key>CFBundleDisplayName</key>
  <string>FFSWallet</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${version}</string>
  <key>CFBundleVersion</key>
  <string>${build}</string>
  <key>CFBundleSupportedPlatforms</key>
  <array>
    <string>iPhoneSimulator</string>
  </array>
  <key>CFBundleIcons</key>
  <dict>
    <key>CFBundlePrimaryIcon</key>
    <dict>
      <key>CFBundleIconFiles</key>
      <array>
        <string>AppIcon60x60</string>
      </array>
    </dict>
  </dict>
  <key>LSRequiresIPhoneOS</key>
  <true/>
  <key>MinimumOSVersion</key>
  <string>${min_ios}</string>
  <key>UIDeviceFamily</key>
  <array>
    <integer>1</integer>
    <integer>2</integer>
  </array>
  <key>UILaunchScreen</key>
  <dict/>
  <key>UIRequiresFullScreen</key>
  <true/>
  <key>UISupportedInterfaceOrientations</key>
  <array>
    <string>UIInterfaceOrientationPortrait</string>
  </array>
</dict>
</plist>
PLIST
plutil -lint "$app/Info.plist"

codesign --force --sign - "$app"
codesign --verify --verbose "$app"
