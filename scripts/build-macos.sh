#!/bin/sh
# All macOS native capabilities use purego; no native compiler is needed.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
export CGO_ENABLED=0
RELEASE_VERSION=$(python3 -c 'import json; print(json.load(open("mygo.json"))["version"])')
export MYGO_LDFLAGS="${MYGO_LDFLAGS:-} -X miya/internal/selfupdate.Build=miya-release:$RELEASE_VERSION:end"
# Keep a local signing identity outside version control. Ad-hoc signatures
# change their designated requirement on every build and invalidate TCC grants.
SIGNING_IDENTITY=${MIYA_SIGNING_IDENTITY:-}
if [ -z "$SIGNING_IDENTITY" ] && [ -r .mygo/macos-signing-identity ]; then
    IFS= read -r SIGNING_IDENTITY < .mygo/macos-signing-identity
fi
if [ -n "$SIGNING_IDENTITY" ]; then
    set -- -sign "$SIGNING_IDENTITY" "$@"
fi
exec go tool mygo build -platform "darwin/$(go env GOARCH)" -skip-dmg "$@"
