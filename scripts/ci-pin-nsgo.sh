#!/bin/bash
# Release builds resolve the immutable upstream version in go.mod.
set -euo pipefail
cd "$(dirname "$0")/.."
go mod edit -dropreplace=github.com/nsgo-dev/nsgo
go mod download
