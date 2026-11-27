#!/bin/sh
set -eu
SCRIPTS=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$SCRIPTS/build-macos.sh" -target macos_universal -installers "$@"
