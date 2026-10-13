#!/usr/bin/env bash
# 构建 Picsky: 先构建前端产物, 再拷贝到 internal/web/build 并由 go:embed 打进二进制.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OUTPUT="${1:-picsky}"
VERSION="${PICSKY_VERSION:-}"

echo "==> 构建前端产物 (web/)"
npm --prefix web install
npm --prefix web run build

echo "==> 暂存前端产物到 internal/web/build"
rm -rf internal/web/build
mkdir -p internal/web/build
cp -a web/build/. internal/web/build/
: > internal/web/build/.keep

echo "==> 构建后端"
LDFLAGS=""
if [ -n "$VERSION" ]; then
  LDFLAGS="-X github.com/yeqingky/Picsky/internal/config.Version=${VERSION}"
fi
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/picsky

echo "==> 完成: $OUTPUT"
