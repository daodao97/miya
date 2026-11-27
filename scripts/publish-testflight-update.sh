#!/usr/bin/env bash
set -euo pipefail

record=$1
[[ -s $record ]] || exit 0
tag=miya-testflight
if ! gh release view "$tag" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; then
    gh release create "$tag" "$record" --repo "$GITHUB_REPOSITORY" --target "$GITHUB_SHA" \
        --prerelease --latest=false --title "Miya TestFlight availability" \
        --notes "Update information for the public iOS beta."
    exit 0
fi

previous=$(mktemp -d)
trap 'rm -f "$previous/update-ios.json"; rmdir "$previous"' EXIT
has_record=$(gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json assets --jq 'any(.assets[]; .name == "update-ios.json")')
if [[ $has_record == true ]]; then
    gh release download "$tag" --repo "$GITHUB_REPOSITORY" --pattern update-ios.json --dir "$previous"
    replace=$(python3 - "$previous/update-ios.json" "$record" <<'PY'
import json
import sys
from pathlib import Path

def version(path):
    release = json.loads(Path(path).read_text())
    return tuple(map(int, release["version"].split("."))), int(release["build"])

print('yes' if version(sys.argv[1]) < version(sys.argv[2]) else 'no')
PY
    )
    [[ $replace == yes ]] || exit 0
fi
gh release upload "$tag" "$record" --repo "$GITHUB_REPOSITORY" --clobber
