#!/bin/bash
# NSGo's arm64 APK needs only Go; x64 additionally needs the NDK. -run installs
# on an explicitly selected device (-device SERIAL), when requested.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
exec "$ROOT/scripts/build-nsgo.sh" -target android_arm64 "$@"
