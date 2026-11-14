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
VERSION=$(python3 -c 'import json; print(json.load(open("nsgo.yaml"))["version"])')
ARCH=$(go env GOARCH)
[ "$ARCH" != amd64 ] || ARCH=x64
APP=${MIYA_ISOLATED_APP:-build/Miya-$VERSION-macos_$ARCH/Miya.app}
case "$APP" in /*) ;; *) APP="$PWD/$APP" ;; esac
BIN="$APP/Contents/MacOS/Miya"
SOCK=$(python3 - "$MIYA_ISOLATED_DIR" <<'PY_SOCKET'
import os, sys, tempfile
path = os.path.join(sys.argv[1], 'service.sock')
if len(path.encode()) >= 100:
    h = 2166136261
    for b in path.encode():
        h = ((h ^ b) * 16777619) & 0xffffffff
    path = os.path.join(tempfile.gettempdir(), f'miya-{os.getuid()}-{h:x}.sock')
print(path)
PY_SOCKET
)

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
        print('hello:', json.dumps({k: r.get(k) for k in ('version', 'release', 'pid', 'busy', 'exe', 'exeTime')}, ensure_ascii=False))
    elif op == 'state':
        print('state:', json.dumps({'threads': len(r.get('threads', [])), 'background_projects': len(r.get('projectThreads', {})), 'busy_projects': r.get('projectBusy', {})}, ensure_ascii=False))
PY
}

case "${1:-run}" in
run)
	shift || true
	if [ "${1:-}" != "--no-build" ]; then
		./scripts/build-macos.sh
	fi
	mkdir -p "$MIYA_ISOLATED_DIR"
	# Run the binary itself: open(1) would hand the bundle ID to the installed app.
	python3 - "$BIN" "$MIYA_ISOLATED_DIR" <<'PY_LAUNCH'
import os, pathlib, subprocess, sys
binary, directory = sys.argv[1:]
with open(pathlib.Path(directory) / 'window.log', 'ab') as log:
    child = subprocess.Popen([binary], stdin=subprocess.DEVNULL, stdout=log,
                             stderr=log, start_new_session=True, env=os.environ.copy())
(pathlib.Path(directory) / 'window.pid').write_text(str(child.pid) + '\n')
print('isolated window: pid', child.pid)
PY_LAUNCH
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
