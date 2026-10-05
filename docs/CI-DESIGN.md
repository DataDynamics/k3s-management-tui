# CI/CD 설계 — GitHub Actions 빌드

> 작성일: 2026-10-06 · 상태: 구현 완료 (11장 구현 노트 참고)

## 1. 목표

- Ubuntu와 RHEL의 버전별 환경에서 k3stui를 빌드하고 테스트합니다.
- 배포판별 설치 패키지(.deb, .rpm)와 실행 파일 압축본(tar.gz)을 만듭니다.
- 만든 패키지가 각 배포판에서 실제로 설치·실행되는지 확인합니다.
- 태그(`v*`)를 푸시하면 GitHub Release에 산출물을 올립니다.

## 2. 전제와 제약

| 항목 | 내용 | 설계 영향 |
|---|---|---|
| 실행 파일 | 정적 Go 바이너리 (`CGO_ENABLED=0`, SQLite도 순수 Go인 modernc.org/sqlite) | 한 번 만든 바이너리가 모든 Linux 배포판에서 돕니다. 배포판별 빌드의 의미는 "그 배포판에서 빌드·테스트·설치가 되는지 검증"과 "배포판별 패키지 생성"입니다 |
| Go 버전 | `src/go.mod`의 `go 1.27.1` | 배포판 패키지의 Go(RHEL go-toolset, Ubuntu golang)는 이보다 오래돼 쓸 수 없습니다. `actions/setup-go`로 공식 Go를 받습니다 |
| 저장소 | 비공개, 조직 플랜 Free | Actions는 **월 2,000분**입니다. 매 실행 시간을 줄이는 설계가 필요합니다 (8장) |
| 러너 | GitHub 호스팅 Ubuntu 러너 (22.04, 24.04)만 무료 | 그 밖의 Ubuntu 버전과 RHEL은 **컨테이너 작업**으로 실행합니다. arm64 러너는 비공개 저장소에서 무료가 아니므로 arm64는 교차 컴파일만 합니다 |
| RHEL | GitHub에 RHEL 러너가 없고, RHEL 이미지는 구독이 필요합니다 | Red Hat이 무료로 재배포를 허용하는 **UBI(Universal Base Image)** 8·9·10을 씁니다. UBI는 RHEL과 같은 사용자 공간(glibc, dnf, rpm)입니다 |
| 버전 표기 | `bin/build.sh`가 `git describe`로 버전을 정합니다 | checkout 시 태그 이력이 필요합니다 (`fetch-depth: 0`) |

## 3. 대상 환경

| 계열 | 버전 | 실행 방식 | 패키지 |
|---|---|---|---|
| Ubuntu | 22.04 LTS | 호스팅 러너 `ubuntu-22.04` | .deb |
| Ubuntu | 24.04 LTS | 호스팅 러너 `ubuntu-24.04` | .deb |
| Ubuntu | 26.04 LTS | 컨테이너 `ubuntu:26.04` | .deb |
| RHEL | 8 | 컨테이너 `registry.access.redhat.com/ubi8/ubi` | .rpm (el8) |
| RHEL | 9 | 컨테이너 `registry.access.redhat.com/ubi9/ubi` | .rpm (el9) |
| RHEL | 10 | 컨테이너 `registry.access.redhat.com/ubi10/ubi` | .rpm (el10) |

- Ubuntu 20.04는 표준 지원이 끝나(2025-04) 기본 대상에서 뺐습니다. 필요하면 컨테이너 `ubuntu:20.04`로 같은 방식으로 추가할 수 있습니다.
- 아키텍처는 amd64를 기본으로 하고, arm64는 교차 컴파일한 tar.gz·패키지만 만듭니다 (실행 검증 없음).

## 4. 워크플로 구성

파일 두 개로 나눕니다.

```
.github/workflows/
├── ci.yml        # PR·main 푸시: 검사 → 배포판별 빌드·테스트·패키징 → 설치 검증 → 클러스터 통합 테스트
└── release.yml   # 태그 v* 푸시: ci와 같은 빌드 + GitHub Release 업로드
packaging/
└── nfpm.yaml     # .deb·.rpm 패키지 정의 (nfpm 하나로 두 형식을 만듭니다)
bin/
└── package.sh    # 로컬에서도 같은 패키지를 만들 수 있는 스크립트 (CI와 동일 명령)
```

### 4.1 ci.yml 작업 흐름

```
            ┌─────────────┐
 push/PR ──▶│ 1. check    │  gofmt·go vet·단위 테스트 (ubuntu-24.04, 1회)
            └──────┬──────┘
                   ▼
            ┌─────────────────────────────────────────────┐
            │ 2. build (matrix: 배포판 6개)                │  배포판 환경에서 go build + go test
            │    ubuntu-22.04 · 24.04 · 26.04             │  → 바이너리, tar.gz, .deb 또는 .rpm
            │    rhel-8 · 9 · 10 (UBI 컨테이너)            │  → artifact 업로드
            └──────┬──────────────────────────────────────┘
                   ▼
            ┌─────────────────────────────────────────────┐
            │ 3. install-test (matrix: 배포판 6개)         │  깨끗한 컨테이너에 패키지 설치
            │                                             │  → --version, --columns, --check, 설치 경로·권한 확인
            └──────┬──────────────────────────────────────┘
                   ▼
            ┌──────────────────┐  ┌──────────────────┐
            │ 4a. e2e-k3s      │  │ 4b. e2e-kubeadm  │   실제 클러스터 통합 테스트 (main·태그에서만)
            │ 러너에 K3s 설치   │  │ kind 클러스터     │
            └──────────────────┘  └──────────────────┘
```

| 작업 | 실행 환경 | 하는 일 | 시간(예상) |
|---|---|---|---|
| check | ubuntu-24.04 | `gofmt -l` 결과가 비었는지, `go vet ./...`, `go vet -tags e2e ./...`, `go test ./...` | 3분 |
| build | 배포판별 (6개) | `bin/build.sh --test`(배포판 환경에서 테스트 포함), amd64·arm64 바이너리, tar.gz, 패키지 생성, artifact 업로드 | 각 3~4분 |
| install-test | 배포판별 (6개) | 패키지 설치(`apt install ./*.deb` / `dnf install ./*.rpm`), 실행 확인, 제거 확인 | 각 1~2분 |
| e2e-k3s | ubuntu-24.04 | 러너에 K3s 설치 → `go test -tags e2e`, `k3stui --check`, `--dump` 로 Host 기능 확인 | 5분 |
| e2e-kubeadm | ubuntu-24.04 | kind 클러스터 생성 → 노드 컨테이너 안에서 `k3stui --check`, `--dump` (kubeadm Host 판별) | 5분 |

RKE2 통합 테스트는 실행 시간(설치·기동 3분 이상)과 사용 시간 한도 때문에 `workflow_dispatch`로 수동 실행할 때만 돌립니다.

### 4.2 build 작업 상세

```yaml
strategy:
  fail-fast: false            # 한 배포판이 실패해도 나머지 결과를 봅니다
  matrix:
    include:
      - { id: ubuntu-22.04, runs-on: ubuntu-22.04, container: "",                                   pkg: deb }
      - { id: ubuntu-24.04, runs-on: ubuntu-24.04, container: "",                                   pkg: deb }
      - { id: ubuntu-26.04, runs-on: ubuntu-24.04, container: "ubuntu:26.04",                       pkg: deb }
      - { id: rhel-8,       runs-on: ubuntu-24.04, container: "registry.access.redhat.com/ubi8/ubi",  pkg: rpm }
      - { id: rhel-9,       runs-on: ubuntu-24.04, container: "registry.access.redhat.com/ubi9/ubi",  pkg: rpm }
      - { id: rhel-10,      runs-on: ubuntu-24.04, container: "registry.access.redhat.com/ubi10/ubi", pkg: rpm }
runs-on: ${{ matrix.runs-on }}
container: ${{ matrix.container || null }}
```

단계:

1. **기본 도구 설치** (컨테이너일 때만): Ubuntu는 `apt-get install git ca-certificates curl tar gzip`, UBI는 `dnf install git tar gzip`.
   `actions/checkout`과 `actions/setup-go`가 git·tar를 필요로 합니다.
2. **checkout**: `fetch-depth: 0` (버전 표기용 태그 이력). 컨테이너에서는 `git config --global --add safe.directory "$GITHUB_WORKSPACE"`.
3. **setup-go**: `go-version-file: src/go.mod`, `cache-dependency-path: src/go.sum` (모듈·빌드 캐시).
4. **빌드·테스트**: `bin/build.sh --test` — 로컬과 같은 스크립트를 써서 CI와 로컬 결과가 어긋나지 않게 합니다.
5. **arm64 교차 컴파일**: `GOARCH=arm64`로 한 번 더 빌드합니다 (정적 바이너리라 cgo 교차 도구가 필요 없습니다).
6. **패키징**: `bin/package.sh` → nfpm으로 패키지 생성.
7. **artifact 업로드**: `k3stui-<id>` 이름으로 바이너리·tar.gz·패키지·SHA256SUMS.

### 4.3 패키지 설계 (nfpm)

| 항목 | 값 |
|---|---|
| 이름 | `k3stui` |
| 설치 경로 | 기존 `bin/install.sh`와 같게: `/opt/k3stui/libexec/k3stui`, `/opt/k3stui/bin/k3stui`(런처), `/opt/k3stui/conf/` |
| 명령 링크 | `/usr/local/bin/k3stui` → `/opt/k3stui/bin/k3stui` (패키지에 심볼릭 링크로 포함, 제거 시 함께 삭제) |
| 설정 파일 | `/opt/k3stui/conf/*.yaml`을 **설정 파일(config|noreplace)** 로 지정 → 업그레이드해도 사용자가 고친 설정을 덮어쓰지 않습니다 (rpm은 `.rpmnew`, deb는 dpkg 질의 대신 `.dpkg-dist`) |
| 로그 디렉터리 | `/var/log/k3stui` (0750) |
| 의존성 | 없음 (정적 바이너리) |
| 파일 이름 | deb: `k3stui_<버전>-1_amd64.deb`, rpm: `k3stui-<버전>-1.el9.x86_64.rpm` |
| 버전 | 태그 `v1.2.3` → `1.2.3`, 태그가 아니면 `0.0.0~git<날짜>.<커밋>` (deb·rpm 버전 비교 규칙에 맞게) |

### 4.4 install-test 작업 상세

build 작업의 artifact를 받아 **깨끗한 배포판 컨테이너**에서 검증합니다 (빌드 도구가 없는 상태에서 설치되는지).

```
apt install ./k3stui_*.deb   /   dnf install -y ./k3stui-*.rpm
test -x /opt/k3stui/libexec/k3stui && test -L /usr/local/bin/k3stui
k3stui --version             # 버전 표기가 태그·커밋과 일치하는지
k3stui --columns pods        # 정적 데이터 출력
k3stui --check               # 클러스터가 없을 때도 오류 없이 점검 결과를 내는지 (FAIL 항목은 정상)
k3stui --config /opt/k3stui/conf/examples/k3stui-k8s.yaml --check   # 예제 설정이 읽히는지
설정 파일 수정 → 패키지 재설치 → 수정 내용이 보존되는지 (noreplace 확인)
apt remove / dnf remove → 링크·실행 파일이 지워지고 설정은 남는지
```

### 4.5 e2e 작업 상세

| 작업 | 준비 | 검증 |
|---|---|---|
| e2e-k3s | `curl -sfL https://get.k3s.io \| sh -` (러너는 root 권한 sudo 가능, systemd 있음) | `sudo go test -tags e2e ./internal/kube/`, `sudo k3stui --check`에서 K3S·호스트 관리·서비스 OK, `--dump service`·`certs`·`manifests` |
| e2e-kubeadm | `helm/kind-action` 또는 `kind create cluster` | 노드 컨테이너에 바이너리 복사 → `k3stui --check`에서 kubeadm·kubelet·etcd 판별, `--dump manifests`·`certs` |
| e2e-rke2 (수동) | systemd 컨테이너(kind 노드 이미지)에 get.rke2.io로 설치 | `--check`에서 RKE2 판별, `--dump service`·`backups` |

e2e 작업은 TUI 화면 조작(tmux)까지는 하지 않고, 비대화형 옵션(`--check`, `--dump`)과 통합 테스트로 확인합니다.

### 4.6 release.yml

- 트리거: `push: tags: ['v*']`.
- ci.yml의 check·build·install-test를 재사용합니다 (`workflow_call`로 ci.yml을 호출해 같은 정의를 씁니다).
- 모든 artifact를 모아 `SHA256SUMS`를 만들고 `softprops/action-gh-release`(또는 `gh release create`)로 Release에 올립니다.
- Release 본문은 직전 태그 이후 커밋 제목 목록으로 자동 생성합니다.

## 5. 트리거와 실행 범위

| 이벤트 | check | build | install-test | e2e-k3s / kubeadm | e2e-rke2 |
|---|---|---|---|---|---|
| PR (main 대상) | ✓ | ✓ (6개) | ✓ | – | – |
| main 푸시 | ✓ | ✓ | ✓ | ✓ | – |
| 태그 `v*` | ✓ | ✓ | ✓ | ✓ | – |
| 수동 실행 (`workflow_dispatch`) | ✓ | ✓ | ✓ | 선택 | 선택 |

- **경로 필터**: 문서만 바뀐 커밋(`docs/**`, `**.md`, `docs/images/**`)은 실행하지 않습니다.
- **중복 실행 취소**: `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }` — 같은 브랜치에 새로 푸시하면 이전 실행을 취소합니다.

## 6. 버전과 산출물 이름

| 상황 | `k3stui --version` | 패키지 버전 |
|---|---|---|
| 태그 `v1.2.0` | `v1.2.0` | `1.2.0` |
| main의 태그 이후 커밋 | `v1.2.0-3-gabc1234` | `1.2.0+3.gabc1234` |
| 태그가 하나도 없음 | `abc1234` | `0.0.0~git20261006.abc1234` |

산출물 예 (`v1.2.0`):

```
k3stui-v1.2.0-linux-amd64              # 실행 파일 (정적 바이너리, 받아서 바로 실행)
k3stui-v1.2.0-linux-arm64
k3stui-v1.2.0-linux-amd64.tar.gz      # 바이너리 + bin/ + conf/ (bin/install.sh로 설치)
k3stui-v1.2.0-linux-arm64.tar.gz
k3stui_1.2.0-1_amd64.deb               # Ubuntu 22.04·24.04·26.04 공용 (정적 바이너리)
k3stui_1.2.0-1_arm64.deb
k3stui-1.2.0-1.el8.x86_64.rpm
k3stui-1.2.0-1.el9.x86_64.rpm
k3stui-1.2.0-1.el10.x86_64.rpm
k3stui-1.2.0-1.el9.aarch64.rpm         # (el8·el10도 같은 방식으로 aarch64)
SHA256SUMS
```

## 7. 보안

- 워크플로 권한은 기본 `contents: read`로 두고, release 작업에만 `contents: write`를 줍니다.
- 외부 액션은 커밋 SHA로 고정합니다 (`actions/checkout@<sha> # v5`).
- PR이 포크에서 올 때는 비밀값을 쓰지 않습니다 (이 설계는 비밀값이 필요 없습니다. Release는 기본 `GITHUB_TOKEN`으로 충분합니다).
- 패키지 서명(GPG)은 1차 범위에서 빼고, `SHA256SUMS`로 무결성만 제공합니다.

## 8. 사용 시간 예산 (월 2,000분)

| 실행 종류 | 예상 시간 | 비고 |
|---|---|---|
| PR | 약 28분 | check 3 + build 6×3 + install-test 6×1.5 (병렬이어도 사용 시간은 합산) |
| main 푸시 | 약 38분 | PR + e2e 2×5 |
| 태그 | 약 40분 | main + Release 업로드 |

- 한 달에 PR·main 푸시를 합쳐 약 50~60회 돌릴 수 있습니다.
- 줄여야 할 때의 선택지:
  1. PR에서는 build를 3개(ubuntu-24.04, rhel-9, ubuntu-26.04)로 줄이고, 전체 6개는 main·태그에서만 돌립니다 (PR 약 15분).
  2. 정적 바이너리이므로 "빌드 1회 + 배포판별 설치 검증"으로 바꿉니다. 배포판별 컴파일·테스트는 빠지지만 시간이 절반으로 줍니다.
  3. 저장소를 공개로 바꾸면 Actions가 무료이고 arm64 러너도 쓸 수 있습니다.

## 9. 검증 계획 (구현 시)

- 워크플로를 푸시하기 전에, 이 서버의 Docker로 같은 컨테이너 이미지(ubuntu:26.04, ubi8·9·10)에서 build·install-test 단계를 그대로 실행해 확인합니다.
- 푸시 후에는 `gh run watch`로 실행 결과를 확인하고, 실패한 작업은 로그를 보고 고칩니다.
- 첫 Release는 `v0.1.0-rc.1` 같은 시험 태그로 확인한 뒤 지우거나 그대로 둘지 정합니다.

## 10. 결정이 필요한 사항

1. **대상 버전**: Ubuntu 22.04·24.04·26.04, RHEL 8·9·10(UBI)로 충분한지, Ubuntu 20.04를 넣을지
2. **빌드 방식**: 배포판별 컴파일·테스트(권장, 이 문서 기준) vs 빌드 1회 + 배포판별 설치 검증(사용 시간 절약)
3. **산출물**: tar.gz + .deb + .rpm(권장) vs tar.gz만
4. **아키텍처**: amd64만 vs arm64 교차 컴파일 포함(권장, 실행 검증은 없음)
5. **Release 자동화**: 태그 `v*` 푸시 시 자동 Release(권장) 여부와 첫 버전 번호
6. **통합 테스트**: main 푸시마다 K3s·kind e2e 실행(권장) vs 수동 실행만

## 11. 구현 노트 (2026-10-06)

### 11.1 만든 파일

| 파일 | 역할 |
|---|---|
| `.github/workflows/ci.yml` | check → build(6) → install-test(6) → e2e-k3s·e2e-kubeadm (main·태그), e2e-rke2 (수동 선택) |
| `.github/workflows/release.yml` | `v*` 태그: ci.yml 재사용(`workflow_call`) → 산출물 모아 `SHA256SUMS` → `gh release create` (`-`가 든 태그는 prerelease) |
| `packaging/nfpm.yaml` | .deb·.rpm 정의 (설치 배치는 `bin/install.sh`와 같음) |
| `packaging/test-install.sh` | 깨끗한 배포판에서 설치·실행·설정 보존·제거 검증 (네트워크 불필요, dpkg·rpm 직접 사용) |
| `bin/package.sh` | tar.gz·deb·rpm 생성, git 버전 → 패키지 버전 변환. nfpm이 없으면 `build/tools`에 고정 버전(v2.47.0) 설치 |
| `bin/build.sh` | `--arch`, `--out` 옵션 추가 (교차 컴파일) |
| `bin/install.sh` | 배포용 압축본(`libexec/k3stui`)에서도 설치할 수 있게 수정 |

### 11.2 설계와 달라진 점

- **install-test는 checkout하지 않습니다.** 깨끗한 UBI·Ubuntu 컨테이너에는 git·tar가 없어서, build 작업이 검증 스크립트를 산출물에 함께 넣고 install-test는 산출물만 내려받습니다.
- **명령 링크는 postinstall 스크립트 대신 패키지의 심볼릭 링크 항목**으로 넣었습니다. 제거할 때 패키지 관리자가 함께 지웁니다.
- **권장 의존성(Recommends)은 넣지 않았습니다.** 편집기 패키지 이름이 배포판마다 다르고(vim, vim-enhanced), helm은 기본 저장소에 없기 때문입니다. `k3stui --check`가 편집기·helm 유무를 알려줍니다.
- **deb 파일 이름에는 데비안 리비전 `-1`이 붙습니다** (`k3stui_1.2.0-1_amd64.deb`).
- **액션 버전**: checkout v7.0.1, setup-go v7.0.0, upload-artifact v7.0.1, download-artifact v8.0.1 (모두 커밋 SHA로 고정), kind v0.33.0, nfpm v2.47.0.

### 11.3 푸시 전 검증 (이 서버의 Docker)

| 검증 | 결과 |
|---|---|
| actionlint v1.7.12 | 두 워크플로 모두 오류 없음 |
| build 작업 재현 — 6개 배포판 컨테이너에 워크플로와 같은 기본 도구를 설치하고 `bin/build.sh --test`, `bin/package.sh` 실행 | 6개 모두 vet·테스트(11개 패키지)·패키징 통과 |
| install-test 재현 — 깨끗한 6개 컨테이너에서 `packaging/test-install.sh` | 6개 모두 설치·실행·설정 보존·제거 통과 (RHEL은 제거 시 `.rpmsave` 보존 확인) |
| 버전 변환 | `v1.2.0`, `v1.2.0-rc.1`, `v1.2.0-3-gabc1234`, 태그 없음, `-dirty` 사례와 deb 정렬 순서 확인 |

GitHub 호스팅 러너에서만 확인할 수 있는 부분(setup-go 캐시, 아티팩트 전달, e2e 작업)은 첫 푸시 후 실행 결과로 확인합니다.

### 11.4 첫 실행 결과 (GitHub Actions)

| 실행 | 결과 | 조치 |
|---|---|---|
| 1차 (`b1a9199`) | 15개 작업 중 `e2e (K3s)`만 실패 | API 서버가 응답한 직후 `kubectl wait node --all`을 해서 노드가 아직 없어 "no matching resources"로 실패했습니다. 노드가 등록될 때까지 기다린 뒤 Ready를 확인하도록 고쳤습니다 |
| 2차 (`396736d`) | `install-test (rhel-9)`만 실패 (1차에서는 통과) | `k3stui --columns pods \| grep -q …`에서 grep이 먼저 끝나면 k3stui가 SIGPIPE(141)로 종료돼 `pipefail`에서 간헐적으로 실패했습니다. 이 서버에서 2만 4천 회 반복해 2회 재현한 뒤, 출력을 변수에 받아 검사하도록 고쳤습니다 |
| 3차 (`3a218ea`) | **모든 작업 통과** (RKE2는 수동 실행 전용이라 건너뜀) | — |

- 3차 실행은 Go 모듈·빌드 캐시가 채워진 상태에서 경과 약 7분, 작업 시간 합계 약 17분이었습니다. Actions는 작업마다 분 단위로 올려 계산하므로 실제 차감은 한 번에 약 25분입니다 (8장 예상 38분보다 적음).
- 캐시가 없던 1차 실행은 배포판 빌드가 각 9~12분 걸렸습니다. 캐시가 만료(7일 미사용)되면 다시 길어질 수 있습니다.
