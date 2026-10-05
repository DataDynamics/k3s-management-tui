#!/usr/bin/env bash
# 배포용 압축본(tar.gz)과 패키지(.deb, .rpm)를 만듭니다. CI(GitHub Actions)와 로컬에서 같은 명령을 씁니다.
#   사용법: bin/package.sh [--tar] [--deb] [--rpm el9] [--arch amd64,arm64] [--out dist]
#   --tar         k3stui-<버전>-linux-<arch>.tar.gz (bin/install.sh로 설치)와 실행 파일 k3stui-<버전>-linux-<arch>
#   --deb         k3stui_<패키지 버전>_<arch>.deb
#   --rpm <dist>  k3stui-<패키지 버전>-1.<dist>.<arch>.rpm (dist 예: el8, el9, el10)
#   --arch        대상 아키텍처 목록 (기본: amd64,arm64)
#   --out         출력 디렉터리 (기본: dist)
# 형식을 하나도 지정하지 않으면 --tar --deb를 만듭니다.
set -euo pipefail

BASE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
NFPM_VERSION="v2.47.0"
TAR=0
DEB=0
RPM_DIST=""
ARCHS="amd64,arm64"
OUT="$BASE/dist"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tar) TAR=1; shift ;;
    --deb) DEB=1; shift ;;
    --rpm) RPM_DIST="$2"; shift 2 ;;
    --arch) ARCHS="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    *) echo "package: 알 수 없는 옵션: $1" >&2; exit 1 ;;
  esac
done
if [[ $TAR -eq 0 && $DEB -eq 0 && -z "$RPM_DIST" ]]; then TAR=1; DEB=1; fi
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

# ---- 버전 ----
# git describe 결과를 deb·rpm 버전 비교 규칙에 맞게 바꿉니다 (CI-DESIGN 6장).
#   v1.2.0               → 1.2.0
#   v1.2.0-rc.1          → 1.2.0~rc.1          (~는 정식 버전보다 앞서 정렬됩니다)
#   v1.2.0-3-gabc1234    → 1.2.0+3.gabc1234
#   abc1234 (태그 없음)   → 0.0.0~git<날짜>.abc1234
#   ...-dirty            → 끝에 .dirty
VERSION="$(git -C "$BASE" describe --tags --always --dirty 2>/dev/null || echo dev)"
pkg_version() {
  local v="${1#v}" dirty=""
  if [[ "$v" == *-dirty ]]; then dirty=".dirty"; v="${v%-dirty}"; fi
  if [[ "$v" =~ ^([0-9]+\.[0-9]+\.[0-9]+)(-([0-9A-Za-z.]+))?(-([0-9]+)-g([0-9a-f]+))?$ ]]; then
    local out="${BASH_REMATCH[1]}"
    # "-3-gabc" 형식이 프리릴리스 자리에 잘못 잡히지 않도록 구분합니다
    if [[ -n "${BASH_REMATCH[3]}" ]]; then out+="~${BASH_REMATCH[3]}"; fi
    if [[ -n "${BASH_REMATCH[5]}" ]]; then out+="+${BASH_REMATCH[5]}.g${BASH_REMATCH[6]}"; fi
    echo "${out}${dirty}"
  else
    echo "0.0.0~git$(date -u +%Y%m%d).${v//-/.}${dirty}"
  fi
}
PKG_VERSION="$(pkg_version "$VERSION")"
echo "▶ version $VERSION (패키지 버전 $PKG_VERSION)"

# ---- nfpm 준비 ----
NFPM="$(command -v nfpm || true)"
if [[ ($DEB -eq 1 || -n "$RPM_DIST") && -z "$NFPM" ]]; then
  NFPM="$BASE/build/tools/nfpm"
  if [[ ! -x "$NFPM" ]]; then
    echo "▶ nfpm $NFPM_VERSION 설치 (build/tools)"
    GOBIN="$BASE/build/tools" go install "github.com/goreleaser/nfpm/v2/cmd/nfpm@$NFPM_VERSION"
  fi
fi

IFS=',' read -ra ARCH_LIST <<< "$ARCHS"
for arch in "${ARCH_LIST[@]}"; do
  bin="$BASE/build/k3stui-linux-$arch"
  "$BASE/bin/build.sh" --arch "$arch" --out "$bin" >/dev/null
  echo "▶ linux/$arch"

  if [[ $TAR -eq 1 ]]; then
    name="k3stui-$VERSION-linux-$arch"
    stage="$(mktemp -d)"
    mkdir -p "$stage/$name/bin" "$stage/$name/libexec"
    install -m 0755 "$bin" "$stage/$name/libexec/k3stui"
    install -m 0755 "$BASE/bin/k3stui" "$BASE/bin/install.sh" "$stage/$name/bin/"
    cp -r "$BASE/conf" "$stage/$name/conf"
    cp "$BASE/README.md" "$stage/$name/"
    tar -C "$stage" --owner=0 --group=0 -czf "$OUT/$name.tar.gz" "$name"
    rm -rf "$stage"
    # 압축을 풀지 않고 바로 받아 쓸 수 있는 실행 파일 (정적 바이너리)
    install -m 0755 "$bin" "$OUT/$name"
    echo "  ✓ $name.tar.gz, $name"
  fi

  pkg() {  # pkg <deb|rpm> <release>
    install -D -m 0755 "$bin" "$BASE/build/pkg/k3stui"
    (cd "$BASE" && PKG_VERSION="$PKG_VERSION" PKG_RELEASE="$2" PKG_ARCH="$arch" \
      "$NFPM" package --config packaging/nfpm.yaml --packager "$1" --target "$OUT/" >/dev/null)
  }
  if [[ $DEB -eq 1 ]]; then pkg deb 1; echo "  ✓ deb"; fi
  if [[ -n "$RPM_DIST" ]]; then pkg rpm "1.$RPM_DIST"; echo "  ✓ rpm ($RPM_DIST)"; fi
done

(cd "$OUT" && sha256sum -- k3stui-*-linux-amd64 k3stui-*-linux-arm64 *.tar.gz *.deb *.rpm 2>/dev/null > SHA256SUMS || true)
echo "✓ 산출물: $OUT"
ls -1 "$OUT"
