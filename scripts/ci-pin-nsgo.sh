#!/bin/bash
# The unpublished local NSGo replacement is intentionally not guessed in CI.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${NSGO_FORK_MODULE:?Set repository variable NSGO_FORK_MODULE to the published fork module path}"
: "${NSGO_FORK_VERSION:?Set repository variable NSGO_FORK_VERSION to an immutable tag or pseudo-version containing the Miya platform services}"
case "$NSGO_FORK_VERSION" in
    v[0-9]*) ;;
    *) echo 'NSGO_FORK_VERSION must be a Go module version, not a branch or latest' >&2; exit 2 ;;
esac
case "$NSGO_FORK_MODULE" in
    github.com/*/*) ;;
    *) echo 'NSGO_FORK_MODULE must be the published GitHub Go module path' >&2; exit 2 ;;
esac
if [ "$NSGO_FORK_MODULE" = github.com/nsgo-dev/nsgo ]; then
    go mod edit -dropreplace=github.com/nsgo-dev/nsgo
else
    go mod edit "-replace=github.com/nsgo-dev/nsgo=$NSGO_FORK_MODULE@$NSGO_FORK_VERSION"
fi
go mod edit "-require=github.com/nsgo-dev/nsgo@$NSGO_FORK_VERSION"
go mod download
