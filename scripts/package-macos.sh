#!/bin/sh
set -eu
SCRIPTS=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$SCRIPTS/build-macos.sh" -platform darwin/universal -skip-dmg=false "$@"
