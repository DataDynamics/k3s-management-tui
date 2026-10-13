# k3stui 설계 — 현재 상태

> 이 문서는 **지금 main 브랜치의 모습**만 적습니다. 결정 배경, 검증 기록, 변경 이력은 각 절에 링크한 spec에 있습니다.
> 기능을 바꾸면 같은 커밋에서 이 문서도 고칩니다. 문서 규칙은 [superpowers/README.md](superpowers/README.md)를 보세요.

## 1. 목표와 범위

터미널 하나에서 Kubernetes 클러스터를 관리하는 TUI 도구입니다. `kubectl`/`k9s`가 다루는 **리소스 관리**에 더해,
K3S·RKE2·kubeadm 노드에서는 일반 도구가 다루지 않는 **노드 측 관리**(서비스, 설정 파일, 데이터스토어 백업, manifest, 인증서, 컨테이너 런타임)를 함께 제공합니다.

비목표: 멀티 클러스터 동시 관리, 원격 노드 관리(SSH·에이전트), 클러스터 설치·업그레이드 자동화, 웹 UI.

근거: [최초 설계](superpowers/specs/2026-10-05-k3stui-initial-design.md) 1장

## 2. 기술 스택

| 영역 | 선택 |
|---|---|
| 언어 | Go 1.27 (`CGO_ENABLED=0` 정적 바이너리) |
| TUI | Bubble Tea v2 + Bubbles v2 + Lip Gloss v2 |
| K8s 접근 | client-go v0.37 (Informer 기반 watch), `k8s.io/metrics` |
| 노드 연동 | `os/exec` 래퍼로 공식 CLI 호출 (`systemctl`, `journalctl`, `k3s`/`rke2`/`kubeadm`, `crictl`, `helm`) |
| SQLite 백업 | `modernc.org/sqlite` (`VACUUM INTO`, cgo 불필요) |
| 설정·로깅 | YAML (`gopkg.in/yaml.v3`), `log/slog` → 파일 |

근거: [최초 설계](superpowers/specs/2026-10-05-k3stui-initial-design.md) 2·11·12장

## 3. 디렉터리 구조

```
bin/          런처(k3stui), build.sh, install.sh, package.sh — 쉘 스크립트만, 로직 없음
conf/         k3stui.yaml, keybindings.yaml, theme.yaml, views.d/, examples/
src/cmd/k3stui/   진입점 (플래그 → 설정 → 모드 판별 → 앱 실행)
src/internal/
  config/     설정 로딩·검증·기본값 병합, views.d 검증
  app/        루트 Bubble Tea 모델, 탭·페이지 스택, 공통 작업 절차
  ui/         components/, styles/, views/(Source·Action 정의)
  kube/       clientset, Informer 캐시, 리소스 레지스트리, 액션
  host/       노드 관리 공통 인터페이스(Host, Capabilities)와 헬퍼
  k3s/        K3S·RKE2 구현 (flavor로 차이 처리)
  kubeadm/    kubeadm 구현
  runtime/    crictl 조회
  helm/       helm CLI 래퍼
  executil/   외부 명령 실행 (타임아웃, 인자 배열)
  textdiff/   설정 변경 diff
  audit/      변경 작업 감사 로그
packaging/    nfpm.yaml(.deb·.rpm), test-install.sh
docs/         이 문서, superpowers/(specs, plans), images/
```

## 4. 동작 모드

시작할 때 kubeconfig → context → 배포판 → 로컬 여부 → 노드 관리 여부를 차례로 정합니다.

- **kubeconfig**: `--kubeconfig` → `cluster.kubeconfig` → `$KUBECONFIG` → `~/.kube/config` → `/etc/rancher/k3s/k3s.yaml` → `/etc/rancher/rke2/rke2.yaml` → `/etc/kubernetes/admin.conf`. 자동 탐색 후보는 내용이 유효할 때만 씁니다.
- **배포판** (`cluster.distribution`): `auto | k3s | rke2 | kubeadm | kubernetes`. auto는 API 서버 버전(`+k3s`, `+rke2`) → `kube-system/kubeadm-config` → 로컬 바이너리·manifest 순으로 판별합니다. 관리형 클러스터(EKS·GKE·AKS 등)는 `kubernetes`입니다.
- **노드 관리** (`cluster.host_management`): auto면 K3S·RKE2·kubeadm이고 API 서버가 이 호스트에 있을 때만 Host 탭을 켭니다. 꺼진 이유는 대시보드·도움말·`--check`에 표시합니다.

| 항목 | 노드 관리 모드 | 리소스 관리 모드 |
|---|---|---|
| 탭 | 7개 (Host 포함) | 6개 |
| 대시보드 오른쪽 패널 | 노드 서비스 상태 | 연결 정보 (배포판, context, API 서버, kubeconfig 출처) |
| kubectl | 노드의 kubectl (`k3s kubectl`, `<rke2 data-dir>/bin/kubectl`) | PATH의 `kubectl` |

근거: [일반 Kubernetes 지원](superpowers/specs/2026-10-05-generic-kubernetes-design.md), [RKE2](superpowers/specs/2026-10-06-rke2-node-management-design.md) 15.1

## 5. 설정

탐색 순서: `--config` → `$K3STUI_CONF` → `~/.config/k3stui/k3stui.yaml` → `$K3STUI_HOME/conf` → `<실행 파일>/../conf` → `/etc/k3stui/k3stui.yaml` → 내장 기본값.

주요 절: `cluster`(모드), `k3s`·`rke2`·`kubeadm`(배포판별 경로, 해당 노드에서만 사용), `ui`, `safety`, `backup`, `portforward`, `tools`, `logging`, `audit`.
항목별 의미는 [conf/k3stui.yaml](../conf/k3stui.yaml)의 주석이 기준입니다. 리소스 컬럼은 `conf/views.d/`로 재정의하며, 시작할 때 검증해 잘못된 항목만 경고하고 뺍니다 ([views.d 문서](../conf/views.d/README.md)).

## 6. 화면

| 탭 | 내용 |
|---|---|
| Dashboard | 노드·메트릭, 네임스페이스별 Pod, 비정상 Pod, Warning 이벤트, 노드 서비스 상태 또는 연결 정보 |
| Workloads | Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs |
| Network | Services, Ingresses, Endpoints, NetworkPolicies, port-forward, Cilium (CRD가 있을 때) |
| Storage | PV, PVC, StorageClass, local-path 사용량 |
| Config | ConfigMaps, Secrets(마스킹), ServiceAccounts, RBAC |
| Host | 노드 관리 (7장) — 노드 관리 모드에서만 |
| Helm | Helm releases, K3S `HelmChart` CRD |

- 탭마다 페이지 스택을 두고, 표 화면은 여러 **소스**(Informer 캐시 리소스 또는 비동기 소스)를 하위 탭으로 보여줍니다. 새 화면 = 소스 하나 추가.
- 키는 `conf/keybindings.yaml`로 재정의합니다. `q`는 하위 화면에서는 뒤로, 최상위에서만 종료합니다. 작업 키와 전역 키의 충돌은 테스트로 막습니다.
- 비대화형: `--check`(환경 점검), `--dump <소스>`(표 출력), `--columns`(리소스·컬럼 목록).

## 7. 노드 관리 (Host 탭)

`internal/host`의 `Host` 인터페이스 하나에 배포판별 구현(`k3s`, `kubeadm`)을 둡니다. 배포판마다 가능한 작업과 문구는 `Capabilities`로 표현하고, Host 탭은 이 값을 보고 하위 탭과 작업을 구성합니다.

| 기능 | K3S | RKE2 | kubeadm |
|---|---|---|---|
| 서비스 | `k3s`/`k3s-agent` | `rke2-server`/`rke2-agent` | `kubelet` |
| 설정 편집 | config.yaml | config.yaml | kubelet config.yaml |
| manifest | 자동 배포 `.skip` 전환 | 자동 배포 `.skip` 전환 | static Pod 편집 |
| 데이터스토어 백업 | SQLite(`VACUUM INTO` + 토큰) 또는 etcd 스냅샷 | etcd 스냅샷 | etcd 스냅샷 + PKI 압축본 |
| 복원 | SQLite 자동, etcd는 안내 | 안내 | 안내 |
| 인증서 | 만료 표시·갱신 | 만료 표시·갱신 | 만료 표시·`kubeadm certs renew` |
| 노드 추가 | 설치 명령 생성 | 설치 절차 생성 | `kubeadm token create` (토큰 생성, 감사 기록) |
| 점검 | `k3s check-config` | 없음 | `kubeadm certs check-expiration` |

설정·static Pod 편집은 모두 `편집기 → 검증 → diff 확인 → 백업 후 저장 → 후속 안내` 흐름을 씁니다. 워커 노드에서는 인증서 갱신·백업·노드 추가를 끕니다.

근거: [최초 설계](superpowers/specs/2026-10-05-k3stui-initial-design.md) 6·12장, [kubeadm](superpowers/specs/2026-10-05-kubeadm-node-management-design.md), [RKE2](superpowers/specs/2026-10-06-rke2-node-management-design.md)

## 8. 내부 구조 규칙

1. **UI 스레드에서 블로킹 I/O 금지.** API 호출·명령 실행은 `tea.Cmd`로 감싸고 결과를 `tea.Msg`로 받습니다.
2. **리소스 목록은 Informer 캐시에서 읽습니다.** Informer는 해당 뷰를 처음 열 때 시작하고, 이벤트는 100ms 단위로 묶어 화면에 전달합니다.
3. **리소스 레지스트리.** 리소스 종류마다 `ResourceDef` 하나를 등록합니다. 컬럼은 views.d로 덮어쓸 수 있습니다.
4. **모든 변경 작업은 `Action` 하나로 정의하고 같은 경로를 거칩니다:** `read-only·root 확인 → 대상 선택 → 값 입력 → 확인 → 실행 → 감사 기록 → 알림`.
5. **노드 연동은 `Host` 인터페이스 뒤에 둡니다.** 테스트는 가짜 Host와 fake clientset을 주입합니다.

근거: [최초 설계](superpowers/specs/2026-10-05-k3stui-initial-design.md) 7·12장

## 9. 안전·보안

- read-only 모드(`--read-only` 또는 `safety.read_only`)는 상태바에 항상 표시합니다.
- 삭제·재시작·drain은 `y/N` 확인을 받습니다. `protected_namespaces`의 리소스 변경과 노드 서비스 중지·재시작은 이름을 다시 입력해야 합니다.
- Secret은 기본으로 마스킹하고, 표시를 켜면 감사 로그에 남깁니다.
- 감사 로그는 JSON Lines(`time, user(SUDO_USER 포함), action, target, result`)입니다.
- 외부 명령은 인자 배열로만 실행하고(쉘 경유 금지), 모두 타임아웃을 적용합니다.
- 노드 관리는 root가 필요합니다. root가 아니면 읽을 수 있는 정보만 보여주고 작업은 끕니다.

## 10. 빌드·배포·테스트

```bash
bin/build.sh                 # build/k3stui
bin/k3stui                   # 저장소에서 바로 실행 (conf/ 자동 인식)
sudo bin/install.sh          # /opt/k3stui/{bin,libexec,conf} + /usr/local/bin/k3stui
bin/package.sh               # .deb·.rpm·tar.gz (CI와 같은 명령)
```

- 단위·화면 테스트: `cd src && go test ./...` (`teatest`, fake clientset, 가짜 Host)
- 통합 테스트: `-tags e2e`로 실제 클러스터에서 생성 → 스케일 → 재시작 → port-forward → 로그 → 삭제
- CI: GitHub Actions가 Ubuntu 22.04·24.04·26.04와 RHEL 8·9·10(UBI)에서 빌드·테스트·패키징·설치 검증을 하고, K3s e2e를 실행합니다. 태그 `v*`를 푸시하면 Release에 산출물을 올립니다.

근거: [CI 설계](superpowers/specs/2026-10-06-ci-github-actions-design.md)
