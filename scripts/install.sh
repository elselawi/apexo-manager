#!/usr/bin/env bash
# Install PocketBase Infrastructure Manager on a Linux server with systemd.
# This script never touches existing PocketBase installations.
set -euo pipefail

PREFIX="/usr/local"
BIN="/usr/local/bin/pbmanager"
CONFIG_DIR="/etc/pbmanager"
CONFIG="$CONFIG_DIR/config.json"
DATA_DIR="/var/lib/pbmanager"
UNIT="/etc/systemd/system/pbmanager.service"
LISTEN="127.0.0.1:8787"
PUBLIC_URL=""
DOMAIN=""
ADMIN_USERNAME=""
SKIP_CADDY="0"
BINARY_SRC=""
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'EOF'
Usage: sudo ./scripts/install.sh [options]

  --binary PATH           pbmanager binary (default: dist/pbmanager)
  --listen HOST:PORT      local listen address (default: 127.0.0.1:8787)
  --public-url URL        public dashboard URL (recommended with HTTPS)
  --domain DOMAIN         add a managed Caddy route for the dashboard
  --admin-username NAME   bootstrap the first owner (password prompted or env)
  --skip-caddy            do not configure the dashboard Caddy route
  -h, --help              show this help

The first administrator password can be supplied through
PBMANAGER_ADMIN_PASSWORD for unattended installs. There are no default
credentials. Existing config, data, and systemd units are never silently
overwritten.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY_SRC="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --public-url) PUBLIC_URL="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --admin-username) ADMIN_USERNAME="$2"; shift 2 ;;
    --skip-caddy) SKIP_CADDY="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "error: run this installer as root" >&2
  exit 1
fi

if [[ -z "$BINARY_SRC" ]]; then
  BINARY_SRC="$SOURCE_DIR/dist/pbmanager"
fi
if [[ ! -x "$BINARY_SRC" ]]; then
  echo "error: binary not found or not executable: $BINARY_SRC" >&2
  echo "build it first with: ./scripts/build.sh" >&2
  exit 1
fi
if [[ -n "$DOMAIN" && -z "$PUBLIC_URL" ]]; then
  PUBLIC_URL="https://$DOMAIN"
fi
if [[ -z "$PUBLIC_URL" ]]; then
  PUBLIC_URL="http://$LISTEN"
fi

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="/var/backups/pbmanager/install-$stamp"
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

echo "== Existing installation inspection =="
if systemctl list-unit-files pbmanager.service >/dev/null 2>&1; then
  echo "systemd: pbmanager.service already known (will be preserved/backed up)"
fi
if [[ -f "$UNIT" ]]; then
  if ! grep -q "managed by pbmanager installer" "$UNIT"; then
    echo "error: $UNIT exists but is not managed by this installer; refusing to overwrite" >&2
    exit 1
  fi
  cp -a "$UNIT" "$backup_dir/pbmanager.service"
fi
if [[ -f "$BIN" ]]; then
  cp -a "$BIN" "$backup_dir/pbmanager.binary"
fi
if [[ -f "$CONFIG" ]]; then
  cp -a "$CONFIG" "$backup_dir/config.json"
  echo "config: preserving existing $CONFIG"
fi

echo "== Installing binary =="
install -d -m 0755 "$(dirname "$BIN")"
install -m 0755 "$BINARY_SRC" "$BIN"

echo "== Preparing persistent data directory =="
install -d -m 0700 "$DATA_DIR"
chmod 700 "$DATA_DIR"

echo "== Writing bootstrap configuration =="
if [[ ! -f "$CONFIG" ]]; then
  "$BIN" config init --path "$CONFIG" --data-dir "$DATA_DIR" --listen "$LISTEN" --public-url "$PUBLIC_URL"
else
  echo "Existing configuration retained. Review it with: pbmanager --config $CONFIG config show"
fi

echo "== Installing systemd unit =="
cat > "$UNIT" <<EOF
# managed by pbmanager installer
[Unit]
Description=PocketBase Infrastructure Manager
Documentation=file://$CONFIG_DIR
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=10

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=$DATA_DIR
ExecStart=$BIN serve --config $CONFIG
Restart=on-failure
RestartSec=3
KillMode=mixed
TimeoutStopSec=30
UMask=0077
LimitNOFILE=65536
NoNewPrivileges=false
PrivateTmp=true
ProtectClock=true
ProtectHostname=true
ProtectKernelLogs=true
ProtectKernelModules=true
ProtectKernelTunables=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "$UNIT"
systemctl daemon-reload
systemctl enable pbmanager.service >/dev/null
systemctl restart pbmanager.service

echo "== Waiting for manager health =="
for i in $(seq 1 40); do
  if curl -fsS "http://$LISTEN/healthz" >/dev/null 2>&1; then
    break
  fi
  if [[ "$i" -eq 40 ]]; then
    echo "error: manager did not become healthy; journal follows" >&2
    journalctl -u pbmanager -n 80 --no-pager >&2 || true
    exit 1
  fi
  sleep 1
done
echo "manager is healthy at http://$LISTEN"

echo "== Administrator bootstrap =="
initialized="$(curl -fsS "http://$LISTEN/api/v1/auth/status" | tr -d ' ' || true)"
if echo "$initialized" | grep -q '"initialized":false'; then
  if [[ -z "$ADMIN_USERNAME" ]]; then
    if [[ -t 0 ]]; then
      read -r -p "First owner username [admin]: " ADMIN_USERNAME
      ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
    else
      echo "No administrator exists. Run: sudo pbmanager --config $CONFIG admin create"
    fi
  fi
  if [[ -n "$ADMIN_USERNAME" ]]; then
    password="${PBMANAGER_ADMIN_PASSWORD:-}"
    if [[ -z "$password" ]]; then
      if [[ ! -t 0 ]]; then
        echo "error: PBMANAGER_ADMIN_PASSWORD is required for non-interactive bootstrap" >&2
        exit 1
      fi
      read -r -s -p "Password for $ADMIN_USERNAME: " password
      echo
      read -r -s -p "Confirm password: " password2
      echo
      if [[ "$password" != "$password2" ]]; then
        echo "error: passwords do not match" >&2
        exit 1
      fi
    fi
    printf '%s\n' "$password" | "$BIN" --config "$CONFIG" admin create --username "$ADMIN_USERNAME" --role owner --password-stdin
    unset password password2
  fi
else
  echo "Administrator account(s) already exist; no credentials were changed."
fi

if [[ -n "$DOMAIN" && "$SKIP_CADDY" == "0" ]]; then
  echo "== Configuring Caddy route for $DOMAIN =="
  if command -v caddy >/dev/null 2>&1 && [[ -f /etc/caddy/Caddyfile ]]; then
    if "$BIN" --config "$CONFIG" caddy ensure-import && "$BIN" --config "$CONFIG" caddy route --domain "$DOMAIN" --upstream "$LISTEN" --name manager; then
      echo "Caddy route applied for https://$DOMAIN"
    else
      echo "warning: Caddy route was not applied; see the Caddy page in the dashboard" >&2
    fi
  else
    echo "warning: Caddy is not installed/configured; dashboard route skipped" >&2
  fi
fi

echo
echo "PocketBase Manager installed."
echo "  Dashboard: $PUBLIC_URL (local: http://$LISTEN)"
echo "  Config:    $CONFIG"
echo "  Data:      $DATA_DIR"
echo "  Unit:      $UNIT"
echo "  Backup of prior installation: $backup_dir"
echo
echo "Recovery (works even if the service is stopped):"
echo "  sudo pbmanager --config $CONFIG admin reset-password"
echo "  sudo pbmanager --config $CONFIG recover"
