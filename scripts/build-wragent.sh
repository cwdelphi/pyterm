#!/bin/bash
# wragent 发布构建：版本第三位自动+1（第一/二位仅人工修改本文件）
# 用法: scripts/build-wragent.sh [--no-bump]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION_FILE="wragent/VERSION"
ENV_FILE=".env"
BUMP=1
[ "${1:-}" = "--no-bump" ] && BUMP=0

GO_BIN="${GO_BIN:-/usr/local/go1.27/bin/go}"
[ -x "$GO_BIN" ] || GO_BIN="$(command -v go)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILT="$(date -u +%Y%m%d%H%M%S)"

OLD_VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
IFS=. read -r MAJOR MINOR PATCH <<< "$OLD_VERSION"
if [ "$BUMP" = "1" ]; then
  NEW_VERSION="${MAJOR}.${MINOR}.$((PATCH + 1))"
  echo "$NEW_VERSION" > "$VERSION_FILE"
else
  NEW_VERSION="$OLD_VERSION"
fi

rollback() {
  echo "$OLD_VERSION" > "$VERSION_FILE"
  [ -n "${OLD_ENV_VALUE:-}" ] && sed -i "s/^LATEST_AGENT_VERSION=.*/LATEST_AGENT_VERSION=${OLD_ENV_VALUE}/" "$ENV_FILE" || true
  echo "build failed, rolled back VERSION to $OLD_VERSION" >&2
}
trap rollback ERR

LDFLAGS="-X main.gitCommit=${COMMIT} -X main.buildTime=${BUILT}"
echo "building wragent ${NEW_VERSION} (commit=${COMMIT})"

cd wragent
CGO_ENABLED=0 "$GO_BIN" build -ldflags "$LDFLAGS" -o wragent .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$GO_BIN" build -ldflags "$LDFLAGS" -o wragent-windows-amd64.exe .
cd ..

OLD_ENV_VALUE="$(grep '^LATEST_AGENT_VERSION=' "$ENV_FILE" | cut -d= -f2 || true)"
sed -i "s/^LATEST_AGENT_VERSION=.*/LATEST_AGENT_VERSION=${NEW_VERSION}/" "$ENV_FILE"

trap - ERR
echo "built wragent ${NEW_VERSION}"
./wragent/wragent -v
echo "NOTE: restart containers to pick up new binary + .env:"
echo "  docker compose up -d md wragent"
