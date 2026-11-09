#!/usr/bin/env bash
# Upgrade PocketBase Manager safely. Config, data, inventory, jobs, and audit
# history are preserved. The previous binary is retained for rollback.
set -euo pipefail

BIN="/usr/local/bin/pbmanager"
CONFIG="/etc/pbmanager/config.json"
LISTEN="http://127.0.0.1:8787"
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NEW_BINARY="${SOURCE_DIR}/dist/pbmanager"

usage() {
  cat <<'EOF'
Usage: sudo ./scripts/upgrade.sh [--binary PATH] [--listen URL]

  --binary PATH   new pbmanager binary (default: dist/pbmanager)
  --listen URL    local health URL (default: http://127.0.0.1:8787)
  -h, --help      show help

Rollback: sudo ./scripts/upgrade.sh --rollback
EOF
}

ROLLBACK="0"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) NEW_BINARY="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --rollback) ROLLBACK="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "error: run as root" >&2
  exit 1
fi
if [[ ! -f "$CONFIG" || ! -f "$BIN" ]]; then
  echo "error: manager is not installed" >&2
  exit 1
fi
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="/var/backups/pbmanager/upgrade-$stamp"
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

if [[ "$ROLLBACK" == "1" ]]; then
  previous="$(ls -1t /var/backups/pbmanager/upgrade-*/pbmanager.binary 2>/dev/null | head -1 || true)"
  if [[ -z "$previous" ]]; then
    echo "error: no previous binary backup found" >&2
    exit 1
  fi
  echo "Rolling back to $previous"
  systemctl stop pbmanager.service || true
  install -m 0755 "$previous" "$BIN"
  systemctl start pbmanager.service
  sleep 2
  systemctl is-active pbmanager.service
  curl -fsS "$LISTEN/healthz"
  echo
  echo "Rollback complete."
  exit 0
fi

if [[ ! -x "$NEW_BINARY" ]]; then
  echo "error: new binary not found: $NEW_BINARY" >&2
  exit 1
fi

echo "== Backing up current deployment =="
cp -a "$BIN" "$backup_dir/pbmanager.binary"
cp -a "$CONFIG" "$backup_dir/config.json"
if [[ -f /etc/systemd/system/pbmanager.service ]]; then
  cp -a /etc/systemd/system/pbmanager.service "$backup_dir/pbmanager.service"
fi
"$BIN" --config "$CONFIG" db snapshot "$backup_dir/manager.db" || {
  echo "error: could not take a consistent database snapshot" >&2
  exit 1
}
chmod 600 "$backup_dir/manager.db"
echo "backup: $backup_dir"

echo "== Stopping service =="
systemctl stop pbmanager.service

echo "== Installing new binary =="
install -m 0755 "$NEW_BINARY" "$BIN"

echo "== Starting service =="
if ! systemctl start pbmanager.service; then
  echo "error: service failed to start; rolling back" >&2
  install -m 0755 "$backup_dir/pbmanager.binary" "$BIN"
  systemctl start pbmanager.service || true
  exit 1
fi

healthy="0"
for i in $(seq 1 30); do
  if curl -fsS "$LISTEN/healthz" >/dev/null 2>&1; then healthy="1"; break; fi
  sleep 1
done
if [[ "$healthy" != "1" ]]; then
  echo "error: new version is unhealthy; rolling back" >&2
  journalctl -u pbmanager -n 60 --no-pager >&2 || true
  systemctl stop pbmanager.service || true
  install -m 0755 "$backup_dir/pbmanager.binary" "$BIN"
  systemctl start pbmanager.service || true
  exit 1
fi

echo "Upgrade complete. Previous version retained at $backup_dir/pbmanager.binary"
echo "Rollback: sudo $SOURCE_DIR/scripts/upgrade.sh --rollback"
