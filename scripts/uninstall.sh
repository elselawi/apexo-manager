#!/usr/bin/env bash
# Uninstall the manager. Application data and configuration are preserved by
# default. Existing PocketBase installations are never touched.
set -euo pipefail

BIN="/usr/local/bin/pbmanager"
CONFIG_DIR="/etc/pbmanager"
DATA_DIR="/var/lib/pbmanager"
UNIT="/etc/systemd/system/pbmanager.service"
PURGE="0"
YES="0"

usage() {
  cat <<'EOF'
Usage: sudo ./scripts/uninstall.sh [--purge] [--yes]

  --purge   also remove manager config and data (NOT PocketBase data)
  --yes     required with --purge; skip interactive confirmation
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge) PURGE="1"; shift ;;
    --yes) YES="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
if [[ "$(id -u)" -ne 0 ]]; then
  echo "error: run as root" >&2
  exit 1
fi
if [[ "$PURGE" == "1" && "$YES" != "1" ]]; then
  echo "WARNING: --purge permanently removes manager config, database, backups, and job history."
  echo "PocketBase instance data under other paths is not touched."
  read -r -p "Type PURGE to continue: " answer
  [[ "$answer" == "PURGE" ]] || { echo "canceled"; exit 1; }
fi

systemctl disable --now pbmanager.service 2>/dev/null || true
if [[ -f "$UNIT" ]] && grep -q "managed by pbmanager installer" "$UNIT"; then
  rm -f "$UNIT"
else
  echo "warning: unit was not created by this installer; leaving it in place" >&2
fi
systemctl daemon-reload || true
rm -f "$BIN"

if [[ "$PURGE" == "1" ]]; then
  # Guard against accidental root deletion: only known manager paths.
  if [[ "$CONFIG_DIR" == "/etc/pbmanager" ]]; then rm -rf "$CONFIG_DIR"; fi
  if [[ "$DATA_DIR" == "/var/lib/pbmanager" ]]; then rm -rf "$DATA_DIR"; fi
  echo "Manager configuration and data removed."
else
  echo "Manager binaries and service removed. Config and data were preserved:"
  echo "  $CONFIG_DIR"
  echo "  $DATA_DIR"
fi
