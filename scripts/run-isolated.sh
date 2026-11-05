#!/bin/bash
# Run this checkout's Miya beside the installed app, without touching it.
#
#   scripts/run-isolated.sh [run] [--no-build]   build, then open an isolated window
#   scripts/run-isolated.sh status               the isolated service's hello and state
#   scripts/run-isolated.sh stop                 ask the isolated service to quit
#
# MIYA_ISOLATED_DIR gives the window and the service it starts a directory of
# their own (see internal/appdir): settings, conversation caches, phone
# pairing, memories and the service socket live there, and the installed
# app's login item is left alone. Agents' own state (~/.codex, ~/.claude)
# stays shared, so Codex and Claude Code keep their sign-in; avoid opening
# the same conversation in both apps at once.
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"
export MIYA_ISOLATED_DIR="${MIYA_ISOLATED_DIR:-$HOME/Library/Application Support/Miya-dev}"
APP=build/darwin-arm64/Miya.app
BIN="$PWD/$APP/Contents/MacOS/Miya"
SOCK="$MIYA_ISOLATED_DIR/service.sock"

service() {
	python3 - "$SOCK" "$@" <<'PY'
import json, socket, sys
path, ops = sys.argv[1], sys.argv[2:]
try:
    conn = socket.socket(socket.AF_UNIX)
    conn.settimeout(10)
    conn.connect(path)
except OSError:
    print('isolated service not running:', path)
    sys.exit(0)
stream = conn.makefile('rb')
for rid, op in enumerate(ops, 1):
    conn.sendall((json.dumps({'id': rid, 'op': op}) + '\n').encode())
    while True:
        line = stream.readline()
        if not line:
            print(op + ': service closed the connection')
            sys.exit(0)
        m = json.loads(line)
        if m.get('id') == rid:
            break
    r = m.get('result') or {}
    if m.get('error'):
        print(op, 'error:', m['error'])
    elif op == 'hello':
        print('hello:', json.dumps({k: r.get(k) for k in ('version', 'pid', 'busy', 'exe', 'exeTime')}, ensure_ascii=False))
    elif op == 'state':
        print('state:', json.dumps({'threads': len(r.get('threads', [])), 'background_projects': len(r.get('projectThreads', {})), 'busy_projects': r.get('projectBusy', {})}, ensure_ascii=False))
PY
}

case "${1:-run}" in
run)
	shift || true
	if [ "${1:-}" != "--no-build" ]; then
		./scripts/build-macos.sh -platform darwin/arm64
	fi
	mkdir -p "$MIYA_ISOLATED_DIR"
	# Run the binary itself: open(1) would hand the bundle ID to the installed app.
	nohup "$BIN" >>"$MIYA_ISOLATED_DIR/window.log" 2>&1 &
	echo "isolated window: pid $!"
	echo "data: $MIYA_ISOLATED_DIR"
	echo "status: $0 status    stop: $0 stop"
	;;
status)
	service hello state
	;;
stop)
	service quit
	;;
*)
	sed -n '2,7p' "$0"
	exit 2
	;;
esac
