#!/usr/bin/env bash
# Installs FFSWallet.app on a booted iPhone simulator, launches it, and checks
# it is still running some seconds later. Leaves a screenshot and the
# simulator log in <out_dir>.
#
# Usage: smoke-ios-simulator.sh <app_path> <out_dir>
set -euo pipefail

app="${1:?app path required}"
out_dir="${2:?output directory required}"
bundle_id="${BUNDLE_ID:-com.ffswallet.mobile}"
mkdir -p "$out_dir"

udid="$(xcrun simctl list devices available -j |
  jq -r '[.devices | to_entries[] | select(.key | test("iOS")) | .value[] | select(.name | test("^iPhone"))][0].udid')"
if [ -z "$udid" ] || [ "$udid" = "null" ]; then
  echo "no available iPhone simulator" >&2
  xcrun simctl list devices available >&2
  exit 1
fi
echo "simulator: $(xcrun simctl list devices | grep "$udid")"

xcrun simctl boot "$udid" || true
xcrun simctl bootstatus "$udid" -b
xcrun simctl install "$udid" "$app"
xcrun simctl launch "$udid" "$bundle_id"
sleep 20

xcrun simctl io "$udid" screenshot "$out_dir/ios-launch.png"
xcrun simctl spawn "$udid" log show --last 2m --style compact \
  --predicate "process == \"main\" OR eventMessage CONTAINS \"$bundle_id\"" > "$out_dir/ios-log.txt" 2>&1 || true

# launchctl lists a running app with its PID in the first column, and "-"
# once it has exited.
line="$(xcrun simctl spawn "$udid" launchctl list | grep "$bundle_id" || true)"
echo "launchctl: ${line:-<not listed>}"
pid="$(printf '%s' "$line" | awk '{print $1}')"
if [ -z "$pid" ] || [ "$pid" = "-" ]; then
  echo "FFSWallet is not running 20s after launch" >&2
  tail -80 "$out_dir/ios-log.txt" >&2 || true
  exit 1
fi
echo "FFSWallet running, pid $pid"
