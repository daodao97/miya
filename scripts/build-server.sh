#!/bin/sh
# The server never starts a window backend; no GTK, display or cgo is needed.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
ARCH=${1:-amd64}
case "$ARCH" in amd64|arm64) ;; *) echo 'usage: build-server.sh [amd64|arm64] [output-directory]' >&2; exit 2 ;; esac
OUT=${2:-"$ROOT/build/server/linux-$ARCH"}
mkdir -p "$OUT" "$ROOT/build/.tmp"
export TMPDIR="$ROOT/build/.tmp"
OUT=$(CDPATH= cd -- "$OUT" && pwd)
RELEASE_LDFLAGS=$(python3 -c 'import json; c=json.load(open("nsgo.yaml")); print(c["miya"]["ldflags"].replace("{version}", c["version"]))')
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -tags nsgo -trimpath -ldflags="-s -w $RELEASE_LDFLAGS" -o "$OUT/miya" .
cp scripts/install-server.sh "$OUT/install-server.sh"
cp docs/server.md "$OUT/README.md"
chmod 755 "$OUT/miya" "$OUT/install-server.sh"
(cd "$OUT" && if command -v sha256sum >/dev/null 2>&1; then sha256sum miya; else shasum -a 256 miya; fi > SHA256SUMS)
tar -czf "$OUT.tar.gz" -C "$OUT" .
echo "Server package: $OUT"
echo "Archive: $OUT.tar.gz"
