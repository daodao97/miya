#!/bin/bash
# Shared NSGo packaging entry. Never writes to a dependency checkout or app install.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
mkdir -p build/.tmp build/nsgo-cache
export TMPDIR="$ROOT/build/.tmp"
export MIYA_NSGO_BUILD_CACHE="$ROOT/build/nsgo-cache/compiled"
export CLANG_MODULE_CACHE_PATH="$ROOT/build/nsgo-cache/clang-modules"
# NSGo's per-project files include Android's persistent debug key. Keep them in
# build/ without a private Go cache (GOCACHE stays shared).
if [ ! -e .nsgo ]; then ln -s build/nsgo-cache .nsgo; fi
if [ ! -L .nsgo ] || [ "$(readlink .nsgo)" != build/nsgo-cache ]; then
    echo '.nsgo must point to build/nsgo-cache; move existing local files there first' >&2
    exit 1
fi
OVERLAY=$(python3 scripts/packaging/nsgo-overlay.py)
export GOFLAGS="${GOFLAGS:-} -overlay=$OVERLAY"
exec go tool nsgo build "$@"
