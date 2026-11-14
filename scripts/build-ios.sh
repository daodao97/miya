#!/bin/bash
# Device-less IPA by default; -target ios_simulator builds a simulator app.
# The full-bleed icon comes from go run ./scripts/ios-icon. Distribution needs
# sign.profile AND sign.widgets_profile granting the configured App Group/APNs.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
exec "$ROOT/scripts/build-nsgo.sh" -target ios "$@"
