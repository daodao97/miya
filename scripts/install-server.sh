#!/bin/sh
# Install as the current user's service: agents and projects share that user's
# normal CLI login, filesystem permissions and environment.
set -eu
PACKAGE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BINARY=${MIYA_SERVER_BINARY:-"$PACKAGE/miya"}
BIN_DIR=${MIYA_SERVER_BIN_DIR:-"$HOME/.local/bin"}
DATA_DIR=${MIYA_DIR:-"${XDG_CONFIG_HOME:-$HOME/.config}/Miya"}
UNIT_DIR=${XDG_CONFIG_HOME:-"$HOME/.config"}/systemd/user
WORKSPACE=${1:-"$HOME"}
if [ "$(uname -s)" != Linux ]; then echo 'Server installation requires Linux and systemd.' >&2; exit 1; fi
if [ ! -f "$BINARY" ] || [ ! -d "$WORKSPACE" ]; then echo 'usage: install-server.sh [existing-project-directory]' >&2; exit 2; fi
WORKSPACE=$(CDPATH= cd -- "$WORKSPACE" && pwd)
mkdir -p "$BIN_DIR" "$DATA_DIR" "$UNIT_DIR"
chmod 700 "$DATA_DIR"
# Keep the running inode alive until its tasks finish.
if [ -f "$BIN_DIR/miya" ]; then cp -p "$BIN_DIR/miya" "$BIN_DIR/miya.previous"; fi
install -m 755 "$BINARY" "$BIN_DIR/.miya-new"
mv -f "$BIN_DIR/.miya-new" "$BIN_DIR/miya"
# Quote systemd specifiers, dollar signs and argument delimiters. Reject
# line breaks rather than allowing values to inject extra unit directives.
quote_unit() {
    case "$1" in *'
'*|*''*) echo 'Newlines are not supported in service paths.' >&2; exit 2 ;; esac
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/%/%%/g; s/\$/$$/g'
}
EXE_ESC=$(quote_unit "$BIN_DIR/miya")
DATA_ESC=$(quote_unit "$DATA_DIR")
WORK_ESC=$(quote_unit "$WORKSPACE")
PATH_ESC=$(quote_unit "$PATH:$BIN_DIR")
umask 077
cat > "$UNIT_DIR/miya.service" <<EOF
[Unit]
Description=Miya Tailcat server
After=network-online.target

[Service]
Type=simple
ExecStart="$EXE_ESC" -serve -data-dir "$DATA_ESC" -workspace "$WORK_ESC"
Environment="PATH=$PATH_ESC"
Restart=always
RestartSec=5
UMask=0077
TimeoutStopSec=30
KillMode=control-group

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
RESTART=false
# Also migrate an existing window-managed -server using this data directory.
# An idle exit is requested before enable, so first installation never
# restarts the new service merely because it has just opened its socket.
if MIYA_DIR="$DATA_DIR" "$BIN_DIR/miya" -restart-when-idle 2>/dev/null; then RESTART=true; fi
systemctl --user enable --now miya.service
if [ "$RESTART" = true ]; then
    echo 'Update installed. The server will restart after running tasks and terminals finish.'
fi
echo "Service installed. Check: systemctl --user status miya.service"
echo "Pairing link (once ready): MIYA_DIR='$DATA_DIR' '$BIN_DIR/miya' -connection"
echo 'To keep it running after logout: loginctl enable-linger "$(id -un)"'
