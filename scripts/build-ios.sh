#!/bin/bash
# Build the iOS app with its full-bleed icon (resources/ios/app-icon.png,
# from `go run ./scripts/ios-icon`). MyGo shares one icon setting across
# platforms; an isolated config keeps the desktop's rounded icon as it is.
#
#   ./scripts/build-ios.sh -ios-team TEAM -ios-device UDID
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
# MyGo runs go list where the config is, so it must stay inside the module.
mkdir -p "$ROOT/.mygo"
STAGE=$(mktemp -d "$ROOT/.mygo/ios-config-XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
python3 - "$ROOT" "$STAGE" <<'PY'
import json, sys
from pathlib import Path
root, stage = map(Path, sys.argv[1:])
config = json.loads((root / 'mygo.json').read_text())
config['main'] = str(root / config.get('main', '.'))
config['out'] = str(root / config.get('out', 'build'))
config['icon'] = str(root / 'resources/ios/app-icon.png')
for key in ('bindings', 'frontendDist'):
    if config.get(key):
        config[key] = str(root / config[key])
config['resources'] = [str(root / p) for p in config.get('resources', [])]
(stage / 'mygo.json').write_text(json.dumps(config, indent=2) + '\n')
(stage / 'resources').symlink_to(root / 'resources', target_is_directory=True)
PY
go tool mygo build -platform ios/arm64 "$@" "$STAGE"
