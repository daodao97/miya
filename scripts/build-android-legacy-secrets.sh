#!/bin/bash
# Regenerate the small DEX library in resources/android with the pinned NSGo wire.
set -euo pipefail
cd "$(dirname "$0")/.."
android_jar=${ANDROID_PLATFORM_JAR:-${ANDROID_HOME:-}/platforms/android-35/android.jar}
d8=${ANDROID_D8:-${ANDROID_HOME:-}/build-tools/35.0.0/d8}
nsgo_dir=$(go list -m -f '{{.Dir}}' github.com/nsgo-dev/nsgo)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
source_path="$nsgo_dir/internal/platform/android/java"
javac --release 17 -classpath "$android_jar" -sourcepath "$source_path" -d "$tmp" resources/android/MiyaLegacySecrets.java
"$d8" --min-api 28 --lib "$android_jar" --classpath "$tmp" --output "$tmp/legacy-secrets.jar" "$tmp"/dev/oneo/nsgo/MiyaLegacySecrets*.class
mv "$tmp/legacy-secrets.jar" resources/android/legacy-secrets.jar
