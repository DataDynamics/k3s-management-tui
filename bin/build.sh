#!/usr/bin/env bash
# src → 실행 파일 빌드.
#   사용법: bin/build.sh [--test] [--arch amd64|arm64] [--out 경로]
#   --test  빌드 전에 go vet, go test를 실행합니다
#   --arch  대상 아키텍처 (기본: 현재 Go의 GOARCH). 정적 바이너리라 교차 컴파일 도구가 필요 없습니다
#   --out   출력 경로 (기본: build/k3stui, --arch를 주면 build/k3stui-linux-<arch>)
set -euo pipefail

BASE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
TEST=0
ARCH=""
OUT=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --test) TEST=1; shift ;;
    --arch) ARCH="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    *) echo "build: 알 수 없는 옵션: $1" >&2; exit 1 ;;
  esac
done

command -v go >/dev/null || { echo "build: go가 필요합니다 (1.27 이상)" >&2; exit 1; }
VERSION="$(git -C "$BASE" describe --tags --always --dirty 2>/dev/null || echo dev)"
if [[ -z "$OUT" ]]; then
  if [[ -n "$ARCH" ]]; then OUT="$BASE/build/k3stui-linux-$ARCH"; else OUT="$BASE/build/k3stui"; fi
fi
ARCH="${ARCH:-$(go env GOARCH)}"

cd "$BASE/src"
if [[ $TEST -eq 1 ]]; then
  echo "▶ go vet / go test"
  go vet ./...
  go test ./...
fi

echo "▶ build $VERSION linux/$ARCH → $OUT"
mkdir -p "$(dirname "$OUT")"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" ./cmd/k3stui
echo "✓ $(du -h "$OUT" | cut -f1)  $OUT"
