#!/usr/bin/env bash
# Build the production frontend and a single self-contained Linux binary.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
GO_BIN="${GO_BIN:-go}"
NPM_BIN="${NPM_BIN:-npm}"
OUT="${OUT:-$ROOT/dist/pbmanager}"
export CGO_ENABLED=0

echo "== Frontend =="
if [[ -f web/package-lock.json ]]; then
  "$NPM_BIN" --prefix web ci --no-audit --no-fund
else
  "$NPM_BIN" --prefix web install --no-audit --no-fund
fi
"$NPM_BIN" --prefix web run build

echo "== Tests =="
"$GO_BIN" test ./...

echo "== Binary =="
mkdir -p "$(dirname "$OUT")"
"$GO_BIN" build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" ./cmd/pbmanager
chmod 0755 "$OUT"
sha256sum "$OUT" > "$OUT.sha256"
echo "version: $VERSION"
cat "$OUT.sha256"
