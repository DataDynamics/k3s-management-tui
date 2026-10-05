#!/usr/bin/env bash
# 깨끗한 배포판 환경(컨테이너)에서 패키지 설치를 검증합니다. CI의 install-test 작업과 로컬에서 같은 스크립트를 씁니다.
#   사용법: packaging/test-install.sh <패키지 디렉터리> [기대 버전]
#   - Debian 계열은 *_amd64.deb, RHEL 계열은 *.x86_64.rpm 중 이 배포판에 맞는 것을 고릅니다 (RHEL은 /etc/os-release의 VERSION_ID로 el8·el9·el10 선택)
#   - 설치 → 실행 확인 → 설정 보존(재설치) → 제거 순서로 확인합니다. 네트워크가 필요 없습니다 (dpkg·rpm 직접 사용).
set -euo pipefail

DIR="${1:?패키지 디렉터리를 지정하세요}"
EXPECT="${2:-}"
. /etc/os-release
step() { echo; echo "▶ $*"; }
fail() { echo "✕ $*" >&2; exit 1; }

if command -v dpkg >/dev/null && [[ "${ID_LIKE:-} $ID" == *debian* ]]; then
  KIND=deb
  pkgs=("$DIR"/k3stui_*_amd64.deb); PKG="${pkgs[0]}"
  install_pkg() { dpkg -i "$PKG"; }
  reinstall_pkg() { dpkg -i "$PKG"; }       # 비대화형에서 dpkg는 사용자가 고친 conffile을 유지합니다
  remove_pkg() { dpkg -r k3stui; }
else
  KIND=rpm
  EL="el${VERSION_ID%%.*}"
  pkgs=("$DIR"/k3stui-*."$EL".x86_64.rpm); PKG="${pkgs[0]}"
  install_pkg() { rpm -ivh "$PKG"; }
  reinstall_pkg() { rpm -Uvh --replacepkgs "$PKG"; }
  remove_pkg() { rpm -e k3stui; }
fi
[[ -f "$PKG" ]] || fail "패키지를 찾지 못했습니다: $PKG"
echo "배포판: $PRETTY_NAME · 패키지: $(basename "$PKG")"

step "설치"
install_pkg
test -x /opt/k3stui/libexec/k3stui || fail "실행 파일이 없습니다"
test -x /opt/k3stui/bin/k3stui || fail "런처가 없습니다"
test -L /usr/local/bin/k3stui || fail "/usr/local/bin/k3stui 링크가 없습니다"
test -d /var/log/k3stui || fail "로그 디렉터리가 없습니다"
[[ "$(stat -c %a /var/log/k3stui)" == 750 ]] || fail "로그 디렉터리 권한이 750이 아닙니다"

step "실행"
ver="$(k3stui --version)"
echo "$ver"
if [[ -n "$EXPECT" && "$ver" != "k3stui $EXPECT" ]]; then fail "버전이 다릅니다: $ver (기대: $EXPECT)"; fi
# 출력을 먼저 받은 뒤 검사합니다. "명령 | grep -q"는 grep이 먼저 끝나면 명령이 SIGPIPE를 받아
# pipefail에서 간헐적으로 실패합니다.
cols="$(k3stui --columns pods)"
grep -q "내장 컬럼" <<<"$cols" || fail "--columns 출력이 이상합니다"
out="$(k3stui --check)" || fail "--check 실패"
grep -q "설정 파일.*/opt/k3stui/conf/k3stui.yaml" <<<"$out" || fail "설치된 설정 파일을 읽지 않았습니다:\n$out"
grep -q "kubeconfig" <<<"$out" || fail "--check 출력에 kubeconfig 항목이 없습니다"
for ex in /opt/k3stui/conf/examples/*.yaml; do
  k3stui --config "$ex" --check >/dev/null || fail "예제 설정을 읽지 못했습니다: $ex"
done
examples=(/opt/k3stui/conf/examples/*.yaml)
echo "✓ --version, --columns, --check, 예제 설정 ${#examples[@]}개"

step "설정 보존 (재설치)"
echo "# test-install: local edit" >> /opt/k3stui/conf/k3stui.yaml
reinstall_pkg
grep -q "test-install: local edit" /opt/k3stui/conf/k3stui.yaml || fail "재설치 후 사용자가 고친 설정이 사라졌습니다"
echo "✓ 고친 설정이 유지됩니다"

step "제거"
remove_pkg
test ! -e /opt/k3stui/libexec/k3stui || fail "제거 후 실행 파일이 남았습니다"
test ! -L /usr/local/bin/k3stui || fail "제거 후 링크가 남았습니다"
if [[ $KIND == deb ]]; then
  test -f /opt/k3stui/conf/k3stui.yaml || fail "dpkg -r 후에는 설정 파일이 남아야 합니다 (purge 전)"
else
  test -f /opt/k3stui/conf/k3stui.yaml.rpmsave || fail "rpm -e 후에는 고친 설정이 .rpmsave로 남아야 합니다"
fi
echo "✓ 제거 완료 (설정은 남김)"
echo
echo "✓ $PRETTY_NAME: 설치 검증 통과"
