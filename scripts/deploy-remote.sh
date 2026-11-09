#!/usr/bin/env bash
# Copy the built manager to a remote Linux server and run the installer there.
# This script is intentionally thin: it does not store credentials and does not
# modify the remote server beyond the installer's documented behavior.
set -euo pipefail

HOST=""
USER_NAME=""
PORT="22"
DOMAIN=""
PUBLIC_URL=""
ADMIN_USERNAME=""
BINARY="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/dist/pbmanager"
SKIP_CADDY="0"

usage() {
  cat <<'EOF'
Usage: ./scripts/deploy-remote.sh --host HOST [options]

  --host HOST            SSH host or IP (required)
  --user USER            SSH user (default: current user)
  --port PORT            SSH port (default: 22)
  --domain DOMAIN        Dashboard domain for Caddy
  --public-url URL       Public dashboard URL
  --admin-username NAME  First owner (installer prompts for password)
  --binary PATH          Local binary (default: dist/pbmanager)
  --skip-caddy           Do not configure a Caddy route
  -h, --help             Show help

The remote user must be able to sudo. The installer prompts for the first
password on the remote terminal; this script does not send secrets over SSH
arguments.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --user) USER_NAME="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --public-url) PUBLIC_URL="$2"; shift 2 ;;
    --admin-username) ADMIN_USERNAME="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --skip-caddy) SKIP_CADDY="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ -z "$HOST" ]]; then echo "error: --host is required" >&2; usage; exit 2; fi
if [[ ! -x "$BINARY" ]]; then echo "error: binary not found: $BINARY" >&2; exit 1; fi
if [[ -z "$USER_NAME" ]]; then USER_NAME="$(id -un)"; fi
TARGET="$USER_NAME@$HOST"
SSH=(ssh -p "$PORT" -o BatchMode=no -o ServerAliveInterval=30)
SCP=(scp -P "$PORT" -o BatchMode=no)

echo "== Copying files to $TARGET =="
"${SSH[@]}" "$TARGET" "rm -rf /tmp/pbmanager-deploy && mkdir -p /tmp/pbmanager-deploy"
"${SCP[@]}" "$BINARY" "$TARGET:/tmp/pbmanager-deploy/pbmanager"
"${SCP[@]}" "$(dirname "${BASH_SOURCE[0]}")/install.sh" "$TARGET:/tmp/pbmanager-deploy/install.sh"

args=(--binary /tmp/pbmanager-deploy/pbmanager)
[[ -n "$DOMAIN" ]] && args+=(--domain "$DOMAIN")
[[ -n "$PUBLIC_URL" ]] && args+=(--public-url "$PUBLIC_URL")
[[ -n "$ADMIN_USERNAME" ]] && args+=(--admin-username "$ADMIN_USERNAME")
[[ "$SKIP_CADDY" == "1" ]] && args+=(--skip-caddy)

echo "== Running installer (sudo may prompt) =="
remote_args=""
for arg in "${args[@]}"; do
  printf -v quoted '%q' "$arg"
  remote_args+=" $quoted"
done
# -t keeps the installer's password prompt interactive.
"${SSH[@]}" -t "$TARGET" "chmod +x /tmp/pbmanager-deploy/install.sh && sudo /tmp/pbmanager-deploy/install.sh$remote_args"
