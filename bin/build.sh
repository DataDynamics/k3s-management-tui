#!/usr/bin/env bash
# src → build/k3stui 빌드.  사용법: bin/build.sh [--test]
set -euo pipefail

BASE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
OUT="$BASE/build/k3stui"
VERSION="$(git -C "$BASE" describe --tags --always --dirty 2>/dev/null || echo dev)"

command -v go >/dev/null || { echo "build: go가 필요합니다 (1.27 이상)" >&2; exit 1; }

cd "$BASE/src"
if [[ "${1:-}" == "--test" ]]; then
  echo "▶ go vet / go test"
  go vet ./...
  go test ./...
fi

echo "▶ build $VERSION → $OUT"
mkdir -p "$BASE/build"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" ./cmd/k3stui
echo "✓ $(du -h "$OUT" | cut -f1)  $OUT"
