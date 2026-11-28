#!/bin/bash
# Build the iOS app with its full-bleed icon (resources/ios/app-icon.png,
# from `go run ./scripts/ios-icon`). MyGo shares one icon setting across
# platforms; an isolated config keeps the desktop's rounded icon as it is.
#
#   ./scripts/build-ios.sh            # a signed .ipa (team: mygo.json ios.team)
# To install on a device during development: go tool mygo dev -platform ios -device UDID
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
# MyGo runs go list where the config is, so it must stay inside the module.
mkdir -p "$ROOT/.mygo"
STAGE=$(mktemp -d "$ROOT/.mygo/ios-config-XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
# Apply the iOS host fix locally until it is included in the pinned MyGo version.
mygo_dir=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)
if [[ -z "$mygo_dir" ]]; then
    go mod download github.com/egoist/mygo
    mygo_dir=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)
fi
cp -R "$mygo_dir" "$STAGE/mygo"
chmod -R u+w "$STAGE/mygo"
surface="$STAGE/mygo/internal/mobile/ios/Sources/MyGoHost/SurfaceWindow.swift"
patch -s -f -F 0 "$surface" < "$ROOT/scripts/mygo-ios-keyboard-paste.patch"
(cd "$STAGE" && go work init "$ROOT")
go work edit -replace "github.com/egoist/mygo=$STAGE/mygo" "$STAGE/go.work"
python3 - "$ROOT" "$STAGE" <<'PY'
import json, sys
from pathlib import Path
root, stage = map(Path, sys.argv[1:])
config = json.loads((root / 'mygo.json').read_text())
config['main'] = str(root / config.get('main', '.'))
config['out'] = str(root / config.get('out', 'build'))
config['icon'] = str(root / 'resources/ios/app-icon.png')
# MIYA_IOS_DISTRIBUTION=development signs for the team's devices, to install
# on a phone (xcrun devicectl device install app).
import os
if os.environ.get('MIYA_IOS_DISTRIBUTION'):
    config.setdefault('ios', {})['distribution'] = os.environ['MIYA_IOS_DISTRIBUTION']
for key in ('bindings', 'frontendDist'):
    if config.get(key):
        config[key] = str(root / config[key])
config['resources'] = [str(root / p) for p in config.get('resources', [])]
(stage / 'mygo.json').write_text(json.dumps(config, indent=2) + '\n')
(stage / 'resources').symlink_to(root / 'resources', target_is_directory=True)
if config.get('i18n'):
    locale_dir = Path(config['i18n'].get('dir', 'locales'))
    locale_stage = stage / locale_dir
    if not locale_stage.exists():
        locale_stage.parent.mkdir(parents=True, exist_ok=True)
        locale_stage.symlink_to(root / locale_dir, target_is_directory=True)
PY
build_number=$(python3 -c 'import json; print(json.load(open("mygo.json")).get("ios", {}).get("buildNumber", "1"))')
export MYGO_LDFLAGS="${MYGO_LDFLAGS:-} -X miya/internal/mobile.mobileBuildNumber=$build_number"
GOWORK="$STAGE/go.work" go tool mygo build -platform "${MIYA_IOS_PLATFORM:-ios/arm64}" "$@" "$STAGE"
