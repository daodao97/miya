#!/bin/bash
# Build Miya for Android (arm64) with MyGo's Android host, which the CLI
# embeds. Without android.keystore in mygo.json the APK is debug-signed.
# Usage: scripts/build-android.sh [--install] [--identifier ID]
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$ROOT"
INSTALL=0
APP_ID=""
while [ $# -gt 0 ]; do
    case "$1" in
        --install) INSTALL=1; shift ;;
        --identifier) APP_ID="$2"; shift 2 ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done
export PATH="/opt/homebrew/bin:$PATH"
if [ -z "${JAVA_HOME:-}" ] && [ "$(uname -s)" = Darwin ]; then
    export JAVA_HOME=$(/usr/libexec/java_home -v 17)
fi
# MyGo runs go list where the config is, so it must stay inside the module.
mkdir -p "$ROOT/.mygo"
STAGE=$(mktemp -d "$ROOT/.mygo/android-config-XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
python3 - "$ROOT" "$STAGE" "$APP_ID" <<'PY'
import json, re, sys
from pathlib import Path
root, stage, override = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
config = json.loads((root / 'mygo.json').read_text())
if override:
    if not re.fullmatch(r'[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+', override):
        raise SystemExit('Invalid Android application identifier')
    config['identifier'] = override
config['main'] = str(root / config.get('main', '.'))
config['out'] = str(root / config.get('out', 'build'))
config['icon'] = str(root / 'resources/icon.png')
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
(stage / 'identifier').write_text(config['identifier'])
PY
IDENTIFIER=$(cat "$STAGE/identifier")
go tool mygo build -platform android/arm64 "$STAGE"
APK=$(ls -t "$ROOT"/build/android*/*.apk 2>/dev/null | head -1)
test -n "$APK" || { echo "No APK found under build/" >&2; exit 1; }
echo "Built: $APK"
if [ "$INSTALL" -eq 1 ]; then
    SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
    "$SDK/platform-tools/adb" install -r "$APK"
    "$SDK/platform-tools/adb" shell monkey -p "$IDENTIFIER" -c android.intent.category.LAUNCHER 1 >/dev/null
fi
