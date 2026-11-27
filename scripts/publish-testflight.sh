#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 4 || ! -f $4 ]]; then
    echo 'Expected one signed IPA: publish-testflight.sh TARGET VERSION BUILD_NUMBER IPA' >&2
    exit 1
fi

target=$1
version=$2
build_number=$3
ipa=$4
app=6839456228
case "$target" in
    internal) group=9ab0c4c9-cab0-4708-bfe3-f62f7d93a25e ;;
    public) group=b49b37aa-a686-47f6-96b3-0be9b71218db ;;
    *) echo "Unknown TestFlight target: $target" >&2; exit 1 ;;
esac

# Confirm that the uploaded artifact belongs to this version and workflow run.
python3 - "$ipa" "$version" "$build_number" <<'PY'
import plistlib
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as ipa:
    info = plistlib.loads(ipa.read('Payload/Miya.app/Info.plist'))
expected = ('com.daodao.miya', sys.argv[2], sys.argv[3])
actual = tuple(info[key] for key in ('CFBundleIdentifier', 'CFBundleShortVersionString', 'CFBundleVersion'))
if actual != expected:
    raise SystemExit(f'IPA identity {actual} does not match {expected}')
PY

# A rerun may be resuming an upload that Apple already received.
uploads=$(asc builds uploads list --app "$app" --platform IOS \
    --cf-bundle-short-version "$version" --cf-bundle-version "$build_number" --limit 1)
if jq -e '.data | length > 0' <<< "$uploads" > /dev/null; then
    receipt=$(asc builds wait --app "$app" --platform IOS --version "$version" \
        --build-number "$build_number" --timeout 20m --fail-on-invalid)
else
    receipt=$(asc publish testflight --app "$app" --ipa "$ipa" --upload-only --wait --timeout 20m)
fi
build=$(jq -er '.buildId' <<< "$receipt")

publish=(asc publish testflight --app "$app" --build-id "$build" --group "$group")
if [[ $target == public ]]; then
    state=$(asc testflight distribution view --build-id "$build" | jq -er '.data[0].attributes.externalBuildState')
    case "$state" in
        WAITING_FOR_BETA_REVIEW|IN_BETA_REVIEW)
            echo "TestFlight $version ($build_number) is already in the review queue: $state"
            exit 0
            ;;
        READY_FOR_BETA_SUBMISSION)
            pending=$(asc builds list --app "$app" --platform IOS --version "$version" \
                --beta-review-state WAITING_FOR_REVIEW,IN_REVIEW --limit 1 | jq -r '.data[0].attributes.version // empty')
            if [[ -n $pending ]]; then
                echo "TestFlight $version ($pending) is in review. Build $build_number was uploaded. Rerun this job once that review is complete." >&2
                exit 1
            fi
            publish+=(--submit --confirm)
            ;;
        READY_FOR_BETA_TESTING|IN_BETA_TESTING) ;;
        *) echo "Build $build_number is unavailable for external testing: $state" >&2; exit 1 ;;
    esac
fi

"${publish[@]}"
distribution=$(asc testflight distribution view --build-id "$build")
jq '.data[0].attributes' <<< "$distribution"
if [[ $target == public && -n ${TESTFLIGHT_UPDATE_RECORD:-} ]] &&
   jq -e '.data[0].attributes.externalBuildState == "IN_BETA_TESTING"' <<< "$distribution" >/dev/null; then
    expires_at=$(asc builds info --build-id "$build" | jq -er '.data.attributes.expirationDate | select(length > 0)')
    jq -n --arg version "$version" --arg build "$build_number" --arg expiresAt "$expires_at" \
        '{version: $version, build: $build, expiresAt: $expiresAt}' > "$TESTFLIGHT_UPDATE_RECORD"
fi
