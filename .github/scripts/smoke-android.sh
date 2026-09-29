#!/usr/bin/env bash
# Installs the APK on the running emulator, launches it, and checks it is
# still running some seconds later. Leaves a screenshot and logcat in
# <out_dir>.
#
# Usage: smoke-android.sh <apk> <out_dir>
set -euo pipefail

apk="${1:?apk required}"
out_dir="${2:?output directory required}"
package="${APP_ID:-com.ffswallet.mobile}"
mkdir -p "$out_dir"

adb install -r "$apk"
adb logcat -c
adb shell monkey -p "$package" -c android.intent.category.LAUNCHER 1
sleep 30

adb exec-out screencap -p > "$out_dir/android-launch.png"
adb logcat -d > "$out_dir/android-logcat.txt"

pid="$(adb shell pidof "$package" | tr -d '\r' || true)"
if [ -z "$pid" ]; then
  echo "FFSWallet is not running 30s after launch" >&2
  grep -iE "fatal|panic|$package|GoLog|Fyne" "$out_dir/android-logcat.txt" | tail -80 >&2 || true
  exit 1
fi
echo "FFSWallet running, pid $pid"
