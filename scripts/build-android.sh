#!/bin/bash
# Build the actual Miya entry point with MyGo's Android host. This creates
# a locally debug-signed APK; distribution signing is a separate release step.
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
NDK_VERSION="${ANDROID_NDK_VERSION:-28.2.13676358}"
case "$(uname -s)" in
    Darwin) HOST_TAG=darwin-x86_64; SDK_DEFAULT="$HOME/Library/Android/sdk" ;;
    Linux) HOST_TAG=linux-x86_64; SDK_DEFAULT="$HOME/Android/Sdk" ;;
    *) echo "Android build script currently supports macOS/Linux hosts" >&2; exit 2 ;;
esac
SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$SDK_DEFAULT}}"
MYGO_ROOT=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)
HB_ROOT="${MYGO_HB_ANDROID:-$MYGO_ROOT/third_party/hb-build/src/lib/android}"
export CC="${MYGO_ANDROID_CC:-$SDK/ndk/$NDK_VERSION/toolchains/llvm/prebuilt/$HOST_TAG/bin/aarch64-linux-android29-clang}"
test -x "$CC"
test -f "$HB_ROOT/lib/libharfbuzz.a"
export CGO_CFLAGS="${CGO_CFLAGS:-} -I$HB_ROOT/include/harfbuzz -I$HB_ROOT/include/freetype2"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$HB_ROOT/lib -lharfbuzz -lfreetype -lm -llog"
if [ -z "${JAVA_HOME:-}" ]; then
    if [ "$(uname -s)" = Darwin ]; then
        export JAVA_HOME=$(/usr/libexec/java_home -v 17)
    fi
fi
STAGE="$ROOT/.mygo/android-build"
OUT="$ROOT/build/android-arm64"
mkdir -p "$STAGE" "$OUT"
python3 - "$MYGO_ROOT" "$STAGE" "$APP_ID" <<'PY'
import json, re, shutil, sys, xml.etree.ElementTree as ET
from pathlib import Path
framework, stage, override = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
config = json.loads(Path('mygo.json').read_text())
identifier = override or config['identifier']
if not re.fullmatch(r'[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+', identifier):
    raise SystemExit('Invalid Android application identifier')
version = config['version']
if not re.fullmatch(r'[0-9][A-Za-z0-9.+-]*', version):
    raise SystemExit('Invalid Android application version')
host = stage / 'host'
shutil.copytree(framework / 'android', host, dirs_exist_ok=True,
                ignore=shutil.ignore_patterns('build', '.gradle', '.kotlin', 'jniLibs', 'local.properties'))
p = host / 'app/build.gradle'
text = p.read_text().replace("applicationId 'org.mygo.host'", f"applicationId '{identifier}'")
text = text.replace("versionName '1.0'", f"versionName '{version}'")
# MyGo's NDK font matcher requires API 29. Do not advertise API 24 while
# linking Android 29 symbols into the native library.
text = text.replace('minSdk 24', 'minSdk 29')
p.write_text(text)
ET.register_namespace('android', 'http://schemas.android.com/apk/res/android')
android = '{http://schemas.android.com/apk/res/android}'
p = host / 'app/src/main/AndroidManifest.xml'
tree = ET.parse(p)
app = tree.getroot().find('application')
app.set(android + 'label', config['name'])
app.set(android + 'icon', '@drawable/miya_icon')
activity = app.find('activity')
for scheme in config.get('urlSchemes', []):
    if not re.fullmatch(r'[a-z][a-z0-9+.-]*', scheme):
        raise SystemExit('Invalid URL scheme')
    intent = ET.SubElement(activity, 'intent-filter')
    ET.SubElement(intent, 'action', {android + 'name': 'android.intent.action.VIEW'})
    for category in ['DEFAULT', 'BROWSABLE']:
        ET.SubElement(intent, 'category', {android + 'name': 'android.intent.category.' + category})
    ET.SubElement(intent, 'data', {android + 'scheme': scheme})
tree.write(p, encoding='utf-8', xml_declaration=True)
icons = host / 'app/src/main/res/drawable'
icons.mkdir(exist_ok=True)
shutil.copyfile('resources/icon.png', icons / 'miya_icon.png')
(stage / 'identifier').write_text(identifier)
PY
printf 'sdk.dir=%s\n' "$SDK" > "$STAGE/host/local.properties"
mkdir -p "$STAGE/host/app/src/main/jniLibs/arm64-v8a"
GOOS=android GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildmode=c-shared \
    -o "$STAGE/host/app/src/main/jniLibs/arm64-v8a/libmygo.so" .
"$STAGE/host/gradlew" -p "$STAGE/host" :app:assembleDebug --console=plain
cp "$STAGE/host/app/build/outputs/apk/debug/app-debug.apk" "$OUT/Miya.apk"
echo "Built: $OUT/Miya.apk"
if [ "$INSTALL" -eq 1 ]; then
    "$SDK/platform-tools/adb" install -r "$OUT/Miya.apk"
    IDENTIFIER=$(cat "$STAGE/identifier")
    "$SDK/platform-tools/adb" shell am start -W -n "$IDENTIFIER/org.mygo.MygoActivity"
fi
