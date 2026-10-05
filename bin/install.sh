#!/usr/bin/env bash
# /opt/k3stui 설치.  사용법: sudo bin/install.sh [--prefix DIR] [--link PATH] [--uninstall]
#   PREFIX/libexec/k3stui   바이너리
#   PREFIX/bin/k3stui       런처
#   PREFIX/conf/            설정 (이미 있으면 덮어쓰지 않고 *.new로 저장)
#   /usr/local/bin/k3stui → PREFIX/bin/k3stui
set -euo pipefail

BASE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
PREFIX=/opt/k3stui
LINK=/usr/local/bin/k3stui
UNINSTALL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift 2 ;;
    --link) LINK="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    *) echo "알 수 없는 옵션: $1" >&2; exit 1 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "install: root 권한이 필요합니다 (sudo $0)" >&2; exit 1; }

if [[ $UNINSTALL -eq 1 ]]; then
  rm -f "$LINK" "$PREFIX/bin/k3stui" "$PREFIX/libexec/k3stui"
  rmdir "$PREFIX/bin" "$PREFIX/libexec" 2>/dev/null || true
  echo "✓ 제거 완료 (설정 $PREFIX/conf, 로그 /var/log/k3stui 는 남겨 두었습니다)"
  exit 0
fi

# 실행 파일: 배포용 압축본(libexec/) → 저장소 빌드본(build/) → 없으면 빌드
SRC_BIN=""
for c in "$BASE/libexec/k3stui" "$BASE/build/k3stui"; do
  if [[ -x "$c" ]]; then SRC_BIN="$c"; break; fi
done
if [[ -z "$SRC_BIN" ]]; then
  "$BASE/bin/build.sh"
  SRC_BIN="$BASE/build/k3stui"
fi

install -d -m 0755 "$PREFIX/bin" "$PREFIX/libexec" "$PREFIX/conf" "$PREFIX/conf/views.d"
install -m 0755 "$SRC_BIN" "$PREFIX/libexec/k3stui"
install -m 0755 "$BASE/bin/k3stui" "$PREFIX/bin/k3stui"

# 설정: 사용자가 고친 파일을 보존합니다.
while IFS= read -r -d '' f; do
  rel="${f#"$BASE/conf/"}"
  dst="$PREFIX/conf/$rel"
  install -d -m 0755 "$(dirname "$dst")"
  if [[ -e "$dst" ]] && ! cmp -s "$f" "$dst"; then
    install -m 0644 "$f" "$dst.new"
    echo "  보존: $dst (새 기본값은 $dst.new)"
  else
    install -m 0644 "$f" "$dst"
  fi
done < <(find "$BASE/conf" -type f -print0)

ln -sfn "$PREFIX/bin/k3stui" "$LINK"
install -d -m 0750 /var/log/k3stui

echo "✓ 설치 완료: $PREFIX  (실행: sudo k3stui)"
"$PREFIX/libexec/k3stui" --version
