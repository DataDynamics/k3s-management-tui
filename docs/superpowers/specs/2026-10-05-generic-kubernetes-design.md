# 일반 Kubernetes 지원 (1단계)

> 출처: 이관 전 `docs/DESIGN.md` 13장 (원문 그대로, 2026-10-13 이관)

## 13. 일반 Kubernetes 지원 (1단계, 2026-10-05)

K3S가 아닌 클러스터에서도 리소스 관리 도구로 쓸 수 있게, 시작할 때 동작 모드를 정합니다.

### 13.1 판별 순서

1. **kubeconfig 선택**: `--kubeconfig` → `cluster.kubeconfig`(이전 `k3s.kubeconfig` 호환) → `$KUBECONFIG` → `~/.kube/config`
   → `/etc/rancher/k3s/k3s.yaml` → `/etc/rancher/rke2/rke2.yaml` → `/etc/kubernetes/admin.conf`.
   사용자가 지정한 경로는 그대로 쓰고, 자동 탐색 후보는 context와 API 서버 주소가 있는지 확인한 뒤 씁니다.
   이 서버처럼 내용이 빈 `~/.kube/config`가 있어도 건너뜁니다.
2. **context**: `--context` 또는 `cluster.context`. 비우면 current-context를 씁니다. kubectl에는 `--context`, helm에는 `--kube-context`로 넘깁니다.
3. **배포판**: `cluster.distribution`이 `auto`면 API 서버 버전(`+k3s`, `+rke2`)으로 판별하고,
   알 수 없으면 `kube-system/kubeadm-config` ConfigMap으로 kubeadm을 확인합니다. 그 밖에는 `kubernetes`입니다.
   API 서버가 응답하지 않아도(k3s 중지 등) 로컬 서버이고 k3s 바이너리가 있으면 K3S로 봅니다. 그래야 Host 탭에서 서비스를 다시 띄울 수 있습니다.
4. **로컬 여부**: API 서버 주소가 루프백이거나 이 호스트의 인터페이스 IP이면 로컬로 봅니다.
5. **호스트 관리**: `cluster.host_management`가 `auto`면 "K3S + 로컬 API 서버 + k3s 바이너리 존재"일 때만 켭니다.
   꺼지는 이유는 대시보드, 도움말, `--check`에 표시합니다.

### 13.2 모드별 차이

| 항목 | 로컬 K3S | 그 밖의 클러스터 |
|---|---|---|
| 탭 | 7개 (Host 포함) | 6개 (Host 제외, 번호가 당겨짐) |
| 헤더 | 배포판·버전, 호스트 이름, k3s 서비스 상태 | 배포판·버전, `ctx: <context>` |
| 대시보드 오른쪽 패널 | K3S 호스트 상태 | 연결 정보 (배포판, context, API 서버, kubeconfig와 출처, 호스트 관리가 꺼진 이유) |
| kubectl | `k3s kubectl` | PATH의 `kubectl` (없으면 k3s 바이너리) |
| 호스트 상태 조회 | 5초마다 | 하지 않음 |
| local-path 사용량 | local-path StorageClass가 있으면 표시 | API 서버가 로컬이고 StorageClass가 있을 때만 표시 |

### 13.3 검증

- 단위 테스트: kubeconfig 후보 순서, 빈 kubeconfig 거부, context별 서버 주소, 여러 파일 병합, 배포판 판별(버전·kubeadm ConfigMap),
  로컬 주소 판별, 원격 모드 화면(Host 탭 없음, 연결 정보, 탭 번호, `:journal` 거부, 호스트 상태 미조회).
- 이 서버에서 확인: `KUBECONFIG` 없이 실행하면 빈 `~/.kube/config`를 건너뛰고 K3S kubeconfig를 고릅니다.
  `--distribution kubernetes`, `cluster.host_management: disabled`, context가 두 개인 kubeconfig에서 `--context`로 고르기를 확인했습니다.
  원격 모드 TUI에서 describe(PATH kubectl + context)와 Helm 목록(`--kube-context`)이 동작하는 것도 확인했습니다.
- 관리형 클러스터(EKS, GKE 등)에서는 확인하지 않았습니다 (kubeadm·RKE2는 14·15장 참고).

### 13.4 다음 단계 (2단계)

14장에서 구현했습니다.

### 13.5 설정 보강 (2026-10-05)

- **사용자 설정 위치**: 설정 탐색 순서에 `~/.config/k3stui/k3stui.yaml`을 추가했습니다 (`$K3STUI_CONF` 다음). 설치본을 고치지 않고 클러스터별 설정을 둘 수 있습니다.
- **k8s 설정 예제**: `conf/examples/k3stui-k8s.yaml`에 원격 클러스터용 값과 각 항목의 의미를 적었습니다.
- **views.d path 문법 확장**: `a.b`에 더해 `a["점.이.든/키"]`, `a[0]`, `a[*]`를 지원합니다. 맵 값은 키 순서로 정렬해 표시합니다.
- **views.d 검증**: 시작할 때 별칭 파일 이름을 리소스 키로 바꾸고, 모르는 리소스·없는 내장 컬럼·잘못된 path·중복 재정의를 경고합니다.
  경고는 첫 화면 하단, 로그, `--check`에 표시하며, 잘못된 컬럼만 빼고 나머지는 적용합니다.
- **`--columns`**: 리소스 키·별칭·내장 컬럼을 출력합니다. views.d 문서의 리소스 표도 같은 정보로 만들었습니다.
- **예제 검증 테스트**: 저장소의 `views.d/*.yaml.example`, `conf/k3stui.yaml`, `conf/examples/k3stui-k8s.yaml`이 경고 없이 읽히는지 테스트로 확인합니다.

