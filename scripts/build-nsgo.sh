#!/bin/bash
# Shared NSGo packaging entry. Never writes to a dependency checkout or app install.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
mkdir -p build/.tmp build/nsgo-cache
export TMPDIR="$ROOT/build/.tmp"
export CLANG_MODULE_CACHE_PATH="$ROOT/build/nsgo-cache/clang-modules"
# NSGo's per-project files include Android's persistent debug key. Keep them in
# build/ without a private Go cache (GOCACHE stays shared).
if [ ! -e .nsgo ]; then ln -s build/nsgo-cache .nsgo; fi
if [ ! -L .nsgo ] || [ "$(readlink .nsgo)" != build/nsgo-cache ]; then
    echo '.nsgo must point to build/nsgo-cache; move existing local files there first' >&2
    exit 1
fi
VERSION=$(python3 -c 'import json; print(json.load(open("nsgo.yaml"))["version"])')
exec go tool nsgo build -ldflags "-X miya/internal/selfupdate.Build=miya-release:$VERSION:end" "$@"
