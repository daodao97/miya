#!/bin/bash
# NSGo packages a purego desktop binary. Pass -target macos_universal and
# -installers for a universal DMG, or -sign/-notarize for release signing.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
ARCH=$(go env GOARCH)
[ "$ARCH" != amd64 ] || ARCH=x64
SIGNING_IDENTITY=${MIYA_SIGNING_IDENTITY:-}
if [ -z "$SIGNING_IDENTITY" ] && [ -r build/nsgo-cache/macos-signing-identity ]; then
    IFS= read -r SIGNING_IDENTITY < build/nsgo-cache/macos-signing-identity
fi
if [ -n "$SIGNING_IDENTITY" ]; then set -- -sign "$SIGNING_IDENTITY" "$@"; fi
exec scripts/build-nsgo.sh -target "macos_$ARCH" "$@"
