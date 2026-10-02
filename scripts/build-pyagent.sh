#!/bin/bash
# pyagent 单二进制（agent+gateway 双模式）发布构建：版本第三位自动+1（第一/二位仅人工修改本文件）
# 产出三平台版本到 releases/（文件名含版本号+CPU类型，下划线分隔）:
#   releases/pyagent_{VERSION}_linux_amd64
#   releases/pyagent_{VERSION}_linux_arm64
#   releases/pyagent_{VERSION}_windows_amd64.exe
# 同时刷新 pyagent/ 内 canonical 副本供容器运行时挂载。
# 版本线已合并：主写 LATEST_PYAGENT_VERSION，并把 LATEST_WR_VERSION /
# LATEST_AGENT_VERSION / LATEST_GATEWAY_VERSION 对齐到同一版本（兼容旧读取方）。
# 用法: scripts/build-pyagent.sh [--no-bump]
set -euo pipefail
cd "$(dirname "$0")/.."
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

VERSION_FILE="pyagent/VERSION"
ENV_FILE=".env"
BUMP=1
[ "${1:-}" = "--no-bump" ] && BUMP=0

GO_BIN="${GO_BIN:-}"
if [ -z "$GO_BIN" ] || [ ! -x "$GO_BIN" ]; then
  GO_BIN="$(command -v go 2>/dev/null || true)"
fi
if [ -z "$GO_BIN" ] || [ ! -x "$GO_BIN" ]; then
  GO_BIN="/usr/local/go/bin/go"
fi
[ -x "$GO_BIN" ] || { echo "ERROR: go not found, set GO_BIN=<path>" >&2; exit 1; }
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

OLD_ENV_PY="$(grep '^LATEST_PYAGENT_VERSION=' "$ENV_FILE" 2>/dev/null | cut -d= -f2 || true)"
OLD_ENV_WR="$(grep '^LATEST_WR_VERSION=' "$ENV_FILE" 2>/dev/null | cut -d= -f2 || true)"
OLD_ENV_AGENT="$(grep '^LATEST_AGENT_VERSION=' "$ENV_FILE" 2>/dev/null | cut -d= -f2 || true)"
OLD_ENV_GW="$(grep '^LATEST_GATEWAY_VERSION=' "$ENV_FILE" 2>/dev/null | cut -d= -f2 || true)"

sync_env() {
  local val="$1"
  if grep -q '^LATEST_PYAGENT_VERSION=' "$ENV_FILE" 2>/dev/null; then
    sed -i "s/^LATEST_PYAGENT_VERSION=.*/LATEST_PYAGENT_VERSION=${val}/" "$ENV_FILE"
  else
    printf 'LATEST_PYAGENT_VERSION=%s\n' "$val" >> "$ENV_FILE"
  fi
  if grep -q '^LATEST_WR_VERSION=' "$ENV_FILE" 2>/dev/null; then
    sed -i "s/^LATEST_WR_VERSION=.*/LATEST_WR_VERSION=${val}/" "$ENV_FILE"
  fi
  sed -i "s/^LATEST_AGENT_VERSION=.*/LATEST_AGENT_VERSION=${val}/" "$ENV_FILE"
  sed -i "s/^LATEST_GATEWAY_VERSION=.*/LATEST_GATEWAY_VERSION=${val}/" "$ENV_FILE"
}

rollback() {
  echo "$OLD_VERSION" > "$VERSION_FILE"
  [ -n "${OLD_ENV_PY:-}" ] || sed -i '/^LATEST_PYAGENT_VERSION=/d' "$ENV_FILE" 2>/dev/null || true
  [ -n "${OLD_ENV_PY:-}" ] && sed -i "s/^LATEST_PYAGENT_VERSION=.*/LATEST_PYAGENT_VERSION=${OLD_ENV_PY}/" "$ENV_FILE" || true
  [ -n "${OLD_ENV_WR:-}" ] && sed -i "s/^LATEST_WR_VERSION=.*/LATEST_WR_VERSION=${OLD_ENV_WR}/" "$ENV_FILE" || true
  [ -n "${OLD_ENV_AGENT:-}" ] && sed -i "s/^LATEST_AGENT_VERSION=.*/LATEST_AGENT_VERSION=${OLD_ENV_AGENT}/" "$ENV_FILE" || true
  [ -n "${OLD_ENV_GW:-}" ] && sed -i "s/^LATEST_GATEWAY_VERSION=.*/LATEST_GATEWAY_VERSION=${OLD_ENV_GW}/" "$ENV_FILE" || true
  echo "build failed, rolled back VERSION to $OLD_VERSION" >&2
}
trap rollback ERR

# -s -w 去符号表/DWARF（发布件不需要，实测剥离率约 32%）
LDFLAGS="-s -w -X main.gitCommit=${COMMIT} -X main.buildTime=${BUILT}"
RELEASES="releases"
mkdir -p "$RELEASES"
echo "building pyagent ${NEW_VERSION} (commit=${COMMIT})"

cd pyagent
CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "../${RELEASES}/pyagent_${NEW_VERSION}_linux_amd64" .
cp "../${RELEASES}/pyagent_${NEW_VERSION}_linux_amd64" pyagent
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "../${RELEASES}/pyagent_${NEW_VERSION}_linux_arm64" .
cp "../${RELEASES}/pyagent_${NEW_VERSION}_linux_arm64" pyagent-arm64
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "../${RELEASES}/pyagent_${NEW_VERSION}_windows_amd64.exe" .
cp "../${RELEASES}/pyagent_${NEW_VERSION}_windows_amd64.exe" pyagent-windows-amd64.exe
cd ..

sync_env "$NEW_VERSION"

trap - ERR
echo "built pyagent ${NEW_VERSION}"
ls -la "$RELEASES"/pyagent_${NEW_VERSION}_*
./pyagent/pyagent -v
echo "NOTE: restart containers to pick up new binary + .env:"
echo "  docker compose up -d md wragent wragent2 wrgateway"