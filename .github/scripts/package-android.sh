#!/usr/bin/env bash
# Packages FFSWallet for Android as release/FFSWallet-android.apk.
#
# With a signing key in the environment this is a release build: fyne makes
# a non-debuggable app bundle (.aab), and bundletool turns it into one
# universal APK signed with the key, which any device will sideload and which
# every later build signed the same way can update. Without a key it is a
# test build: fyne's own debug-signed, debuggable APK. A tagged release
# refuses to fall back to that.
#
# Env: APP_ID, FYNE_TOOLS, GITHUB_REF_NAME
#      ANDROID_KEYSTORE_BASE64, ANDROID_KEYSTORE_PASSWORD, ANDROID_KEY_ALIAS
#      ANDROID_HOME, ANDROID_NDK_HOME (runner image)
set -euo pipefail

: "${APP_ID:?}" "${FYNE_TOOLS:?}"
root="$PWD"
tmp="${RUNNER_TEMP:-$(mktemp -d)}"

app_version="0.0.0"
tagged=false
if [[ "${GITHUB_REF_NAME:-}" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  app_version="${GITHUB_REF_NAME#v}"
  tagged=true
  # Android only installs an update with a higher version code, so it is
  # derived from the version rather than a run counter.
  version_code=$(( BASH_REMATCH[1] * 1000000 + BASH_REMATCH[2] * 1000 + BASH_REMATCH[3] ))
else
  version_code=1
fi
echo "version $app_version, version code $version_code"

mkdir -p "$root/release"
cd "$root/cmd/ffswallet" # mobile builds do not take --src

if [ -z "${ANDROID_KEYSTORE_BASE64:-}" ]; then
  if [ "$tagged" = true ]; then
    echo "ANDROID_KEYSTORE_BASE64 is not set: a tagged release must be signed with the release key" >&2
    exit 1
  fi
  echo "No signing key: building a debug-signed test APK"
  go run "$FYNE_TOOLS" package \
    --target android \
    --name FFSWallet \
    --icon "$root/assets/icon.png" \
    --app-id "$APP_ID" \
    --app-version "$app_version" \
    --app-build "$version_code"
  mv FFSWallet.apk "$root/release/FFSWallet-android.apk"
  exit 0
fi

: "${ANDROID_KEYSTORE_PASSWORD:?}" "${ANDROID_KEY_ALIAS:?}" "${ANDROID_HOME:?}"
keystore="$tmp/release.p12"
printf '%s' "$ANDROID_KEYSTORE_BASE64" | base64 -d > "$keystore"

# fyne's release packaging and the APK step both need bundletool.
mkdir -p "$tmp/bundletool" "$tmp/bin"
gh release download --repo google/bundletool --pattern 'bundletool-all-*.jar' --dir "$tmp/bundletool" --clobber
jar="$(ls "$tmp"/bundletool/bundletool-all-*.jar)"
echo "bundletool: $(basename "$jar")"
printf '#!/bin/sh\nexec java -jar "%s" "$@"\n' "$jar" > "$tmp/bin/bundletool"
chmod +x "$tmp/bin/bundletool"
export PATH="$tmp/bin:$PATH"

go run "$FYNE_TOOLS" package \
  --release \
  --target android \
  --name FFSWallet \
  --icon "$root/assets/icon.png" \
  --app-id "$APP_ID" \
  --app-version "$app_version" \
  --app-build "$version_code"

bundletool build-apks \
  --bundle FFSWallet.aab \
  --output "$tmp/FFSWallet.apks" \
  --mode universal \
  --ks "$keystore" \
  --ks-pass "pass:$ANDROID_KEYSTORE_PASSWORD" \
  --ks-key-alias "$ANDROID_KEY_ALIAS" \
  --key-pass "pass:$ANDROID_KEYSTORE_PASSWORD" \
  --overwrite
unzip -p "$tmp/FFSWallet.apks" universal.apk > "$root/release/FFSWallet-android.apk"
rm -f "$keystore" FFSWallet.aab

# Prove what shipped: signed with the release key, and not debuggable.
build_tools="$(find "$ANDROID_HOME/build-tools" -mindepth 1 -maxdepth 1 -type d | sort -V | tail -1)"
"$build_tools/apksigner" verify --print-certs "$root/release/FFSWallet-android.apk"
if "$build_tools/aapt2" dump badging "$root/release/FFSWallet-android.apk" | grep -q "application-debuggable"; then
  echo "release APK is debuggable" >&2
  exit 1
fi
"$build_tools/aapt2" dump badging "$root/release/FFSWallet-android.apk" | grep -E "^package:"
