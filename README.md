# K3S Management TUI (k3stui)

K3S 서버를 터미널 하나에서 관리하는 TUI입니다.

k9s처럼 Kubernetes 리소스를 다루는 기능에 더해, 일반 도구가 다루지 않는 **K3S 호스트 쪽 관리 기능**을 함께 제공합니다.
systemd 서비스 제어, `config.yaml` 편집, 데이터스토어 백업·복원, 자동 배포 manifest, 인증서, containerd 이미지·컨테이너를 한 화면에서 관리할 수 있습니다.

- 설계 문서: [docs/DESIGN.md](docs/DESIGN.md)
- 컬럼 재정의: [conf/views.d/README.md](conf/views.d/README.md)

## 주요 기능

| 영역 | 기능 |
|---|---|
| 대시보드 | 클러스터 요약, 노드별 CPU·메모리·Pod 게이지, K3S 서비스 상태, 데이터스토어 크기, 가장 빠른 인증서 만료일, 디스크 사용률, 문제 Pod, 최근 Warning 이벤트 |
| 리소스 | Pod·Deployment·StatefulSet·DaemonSet·Job·CronJob·Service·Ingress·PVC·ConfigMap·Secret·RBAC 등 조회, 실시간 갱신(watch), 필터, 네임스페이스 전환 |
| 리소스 작업 | describe, YAML 보기, `$EDITOR`로 편집, 삭제, 로그(follow), 컨테이너 셸, 포트포워딩, 스케일, 롤아웃 재시작, CronJob 즉시 실행, 노드 cordon·drain |
| K3S 서비스 | 상태·가동 시간·메모리 확인, 시작·중지·재시작, `journalctl` 로그 스트림, `k3s check-config`, agent 노드 추가 명령 생성 |
| 설정 | `/etc/rancher/k3s/config.yaml`(+ `config.yaml.d`) 보기·편집 (YAML 검증 → 변경 비교 → 백업 후 저장 → 재시작 질의) |
| 데이터스토어 | SQLite 온라인 백업, 보관 개수 관리, 복원(무결성·토큰 검사 포함), etcd 스냅샷 생성 |
| 기타 | 자동 배포 manifest `.skip` 전환, 인증서 만료 확인·갱신, containerd 컨테이너·이미지 조회와 미사용 이미지 정리, Helm 릴리스(values·이력·롤백·삭제), Cilium 에이전트 상태 |

## 요구 사항

- K3S가 설치된 Linux 서버 (systemd 사용)
- root 권한 (K3S kubeconfig는 기본 권한이 `0600`입니다)
- 빌드할 때만 Go 1.27 이상이 필요합니다. 실행 파일은 정적 바이너리 하나입니다.
- 선택: `helm`(Helm 탭), metrics-server(CPU·메모리 표시, K3S에 기본 포함)

## 빠른 시작

```bash
bin/build.sh              # src → build/k3stui
sudo bin/k3stui --check   # 환경 점검
sudo bin/k3stui           # 저장소에서 바로 실행 (conf/ 자동 인식)
```

`--check`는 root 권한, API 서버, k3s 서비스, data-dir, 데이터스토어, metrics-server, helm을 점검해 다음과 같이 보여줍니다.

```
[OK  ] API 서버        v1.36.5+k3s1 (/etc/rancher/k3s/k3s.yaml)
[OK  ] k3s 서비스      k3s.service active/running
[OK  ] data-dir        /data2/k3s
[OK  ] 데이터스토어    sqlite /data2/k3s/server/db/state.db
```

## 설치

```bash
sudo bin/install.sh                 # /opt/k3stui 에 설치하고 /usr/local/bin/k3stui 링크를 만듭니다
sudo k3stui
sudo bin/install.sh --uninstall     # 제거 (설정과 로그는 남깁니다)
```

| 경로 | 내용 |
|---|---|
| `/opt/k3stui/libexec/k3stui` | 실행 파일 |
| `/opt/k3stui/bin/k3stui` | 런처 (권한 확인 후 실행 파일을 호출합니다) |
| `/opt/k3stui/conf/` | 설정 파일 |
| `/var/log/k3stui/` | 앱 로그(`k3stui.log`)와 감사 로그(`audit.log`) |

다시 설치해도 사용자가 고친 설정 파일은 덮어쓰지 않습니다. 새 기본값은 `*.new` 파일로 저장합니다.
설치 위치는 `--prefix`, 링크 위치는 `--link`로 바꿀 수 있습니다.

## 실행 옵션

| 옵션 | 설명 |
|---|---|
| `--config <파일>` | 설정 파일을 지정합니다 |
| `--kubeconfig <파일>` | kubeconfig를 지정합니다 (기본 `/etc/rancher/k3s/k3s.yaml`) |
| `--read-only` | 모든 변경 작업을 막습니다 |
| `-n <네임스페이스>` | 시작 네임스페이스를 지정합니다 (`all`은 전체) |
| `--view <탭>` | 시작 탭을 지정합니다 (`dashboard`, `workloads`, `network`, `storage`, `config`, `host`, `helm`) |
| `--check` | 환경을 점검하고 종료합니다 |
| `--dump <소스>` | 소스 하나를 표로 출력하고 종료합니다 (예: `pods`, `nodes`, `service`, `certs`, `backups`, `images`, `releases`) |
| `--version` | 버전을 출력합니다 |

설정 파일은 `--config` → `$K3STUI_CONF` → `<설치 경로>/conf/k3stui.yaml` → `/etc/k3stui/k3stui.yaml` 순서로 찾습니다.
파일이 없어도 내장 기본값으로 동작합니다.

일반 사용자가 읽을 수 있는 kubeconfig를 `--kubeconfig`로 지정하면 root 없이도 실행할 수 있습니다.
이때 Kubernetes 리소스 기능만 쓸 수 있고, 서비스 제어·설정 편집·백업 같은 호스트 기능은 비활성화됩니다.

## 화면 구성

```
 K3S v1.36.5+k3s1 │ dev-sever.dev.net │ k3s ● active │ ns: all                     22:19:14
 1 Dashboard   2 Workloads   3 Network   4 Storage   5 Config   6 Host   7 Helm
Pods │ Deployments │ StatefulSets │ DaemonSets │ ReplicaSets │ Jobs │ CronJobs
Pods ns:all  [1/10]
NAMESPACE    NAME                      READY  STATUS   RESTARTS  CPU  MEM     ...
kube-system  coredns-7cfb7bc9c7-vwzlf  1/1    Running  0         4m   19.1Mi  ...
...
l 로그  L 이전 로그  s 셸  p 포트포워딩  d 상세  y YAML  e 편집  x 삭제  / 필터  n ns  ? 도움말
```

| 키 | 탭 | 하위 탭 |
|---|---|---|
| `1` | Dashboard | — |
| `2` | Workloads | Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs |
| `3` | Network | Services, Ingresses, EndpointSlices, NetworkPolicies, Cilium 정책·에이전트, Port-forwards |
| `4` | Storage | PVC, PV, StorageClasses, local-path 실제 사용량 |
| `5` | Config | ConfigMaps, Secrets, ServiceAccounts, Roles, RoleBindings, ClusterRoles, ClusterRoleBindings |
| `6` | Host | Service, config.yaml, Manifests, Certificates, Backups, Containers, Images |
| `7` | Helm | Releases, HelmCharts, HelmChartConfigs |

Nodes, Events, Namespaces처럼 탭에 없는 리소스는 명령 모드(`:nodes`, `:events`, `:ns`)로 엽니다.

## 키 사용법

### 공통 키

| 키 | 동작 |
|---|---|
| `1`~`7` | 탭 전환 |
| `Tab` / `]`, `Shift+Tab` / `[` | 하위 탭 전환 |
| `↑`/`k`, `↓`/`j`, `PgUp`, `PgDn`, `g`, `G` | 이동 |
| `/` | 필터 (`!`로 시작하면 제외 조건) |
| `n` | 네임스페이스 선택 |
| `:` | 명령 모드 (`:pods`, `:deploy`, `:nodes`, `:events`, `:ns kube-system`, `:journal`, `:host`, `:q`) |
| `Ctrl+R` | 새로고침 |
| `Esc` | 뒤로 / 필터 해제 |
| `?` | 도움말 (현재 화면에서 쓸 수 있는 모든 키를 보여줍니다) |
| `q` | 하위 화면에서는 뒤로, 최상위 화면에서는 종료 |

### 작업 키

| 대상 | 키 |
|---|---|
| 모든 리소스 | `d`/`Enter` 상세, `y` YAML, `e` 편집, `x` 삭제 |
| Pod | `l` 로그, `L` 이전 컨테이너 로그, `s` 셸, `p` 포트포워딩 |
| Deployment, StatefulSet | `S` 스케일, `r` 롤아웃 재시작 |
| DaemonSet | `r` 롤아웃 재시작 |
| CronJob | `t` 즉시 실행 |
| Service | `p` 포트포워딩 |
| Node | `c` cordon, `u` uncordon, `D` drain |
| Secret | `v` 값 보기 (감사 로그에 기록됩니다) |
| Host › Service | `l` k3s 서비스 로그, `r` 재시작, `t` 중지, `a` 시작, `c` check-config, `J` 노드 추가 명령 |
| Host › config.yaml | `v` 파일 보기, `e` 편집, `r` k3s 재시작 |
| Host › Manifests | `v` 보기, `s` `.skip` 전환 |
| Host › Certificates | `v` 상세, `R` 인증서 갱신 |
| Host › Backups | `b` 지금 백업, `R` 복원, `x` 삭제 |
| Host › Containers | `i` inspect, `l` 로그 |
| Host › Images | `i` inspect, `x` 삭제, `P` 미사용 이미지 정리 |
| Helm › Releases | `v` values, `a` 전체 values, `m` manifest, `h` 이력, `R` 롤백, `x` 삭제 |
| Network › Port-forwards | `x` 중지 |

### 텍스트 화면 (로그·YAML·describe)

| 키 | 동작 |
|---|---|
| `/`, `n`, `N` | 검색, 다음 결과, 이전 결과 |
| `w` | 줄바꿈 전환 |
| `f` | follow 전환 (로그 스트림) |
| `←`, `→` | 가로 스크롤 |
| `Esc` / `q` | 뒤로 |

## 설정

모든 설정 파일은 `conf/` 디렉터리에 있습니다. 바꾸고 싶은 값만 적어도 나머지는 기본값을 씁니다.

| 파일 | 내용 |
|---|---|
| `k3stui.yaml` | K3S 경로, 갱신 주기, 시작 화면, 안전 장치, 백업 위치·보관 개수, 외부 도구 경로, 로그 위치 |
| `keybindings.yaml` | 공통 키와 작업 키 재정의 (작업 ID는 `?` 도움말 화면에 표시됩니다) |
| `theme.yaml` | `dark` / `light` 색상 팔레트 |
| `views.d/*.yaml` | 리소스별 표시 컬럼 재정의 (예제 `*.yaml.example` 포함) |

자주 바꾸는 항목은 다음과 같습니다.

```yaml
k3s:
  data_dir: auto                 # config.yaml의 data-dir을 자동으로 읽습니다
safety:
  read_only: false
  protected_namespaces: [kube-system, kube-public, kube-node-lease]
backup:
  dir: /data2/k3stui-backups
  keep: 7
tools:
  editor: ""                     # 비우면 $EDITOR → $VISUAL → vi 순서로 씁니다
```

## 안전 장치

- **읽기 전용 모드**: `--read-only` 또는 `safety.read_only: true`로 켭니다. 켜져 있으면 헤더에 `READ-ONLY`가 표시되고 모든 변경 작업이 막힙니다.
- **확인 단계**: 삭제·재시작 같은 작업은 실행 전에 `y/N`으로 확인합니다.
  보호 네임스페이스의 리소스 변경, k3s 서비스 제어, 데이터스토어 복원, 인증서 갱신, 노드 drain은 대상 이름을 다시 입력해야 실행됩니다.
- **감사 로그**: 모든 변경 작업을 `/var/log/k3stui/audit.log`에 JSON Lines 형식으로 기록합니다. `sudo`로 실행하면 원래 사용자도 함께 남깁니다.
- **설정 백업**: `config.yaml`을 저장하기 전에 기존 파일을 `<backup.dir>/config/`에 복사합니다.
- **데이터스토어 백업**: SQLite는 `VACUUM INTO`로 K3S가 실행 중일 때도 일관된 사본을 만들고, 복원에 필요한 서버 토큰을 `<백업>.token`으로 함께 저장합니다.
  복원할 때는 무결성 검사와 토큰 일치를 먼저 확인하고, 기존 DB를 `state.db.pre-restore-<시각>`으로 남긴 뒤 교체합니다.
- **포트포워딩**: 기본으로 `127.0.0.1`에만 바인딩하며, TUI를 종료하면 모두 중지됩니다.

## 디렉터리 구조

```
bin/            런처(k3stui), build.sh, install.sh
conf/           k3stui.yaml, keybindings.yaml, theme.yaml, views.d/
src/            Go 모듈
  cmd/k3stui/   진입점 (옵션 처리, --check, --dump)
  internal/
    app/        루트 모델: 탭·페이지 스택, 전역 키, 작업 실행 절차
    ui/         components(표·텍스트 뷰어·다이얼로그), styles, views(화면·소스·작업)
    kube/       client-go 래퍼: Informer 캐시, 리소스 정의, 작업, 포트포워딩, 메트릭
    k3s/        K3S 호스트: systemd, config.yaml, 데이터스토어, manifest, 인증서
    runtime/    containerd (k3s crictl)
    helm/       helm CLI 래퍼
    config/     설정 로딩
    executil/   외부 명령 실행 (쉘 미사용, 타임아웃)
    audit/      감사 로그
    textdiff/   설정 변경 비교
docs/           설계 문서
```

## 개발

```bash
bin/build.sh --test                                   # go vet + 단위 테스트 + 빌드
cd src && go test ./...                               # 단위 테스트만
cd src && go test -tags e2e ./internal/kube/ -v       # 실제 K3S 대상 통합 테스트
```

통합 테스트는 `k3stui-e2e-<시각>` 네임스페이스를 만들어 배포·스케일·재시작·포트포워딩·로그·삭제를 확인하고, 끝나면 네임스페이스를 지웁니다.
이미지는 `K3STUI_E2E_IMAGE`(기본 `docker.io/traefik/whoami:v1.11`), kubeconfig는 `K3STUI_E2E_KUBECONFIG`로 바꿀 수 있습니다.

새 리소스 화면은 `src/internal/kube/registry.go`에 `ResourceDef`를 하나 추가하고, `src/internal/ui/views/tabs.go`의 탭에 키를 넣으면 됩니다.
새 작업은 `views.Action`으로 정의합니다. 읽기 전용 확인, 확인 다이얼로그, 감사 기록은 앱이 공통으로 처리합니다.

## 문제 해결

| 증상 | 확인할 점 |
|---|---|
| `root 권한이 필요합니다` | `sudo`로 실행하거나 `--kubeconfig`로 읽을 수 있는 파일을 지정합니다 |
| CPU·메모리가 `-`로 표시됩니다 | metrics-server Pod 상태를 확인합니다 (`--check`의 metrics-server 항목) |
| Helm 탭에 오류가 표시됩니다 | `helm`이 PATH에 있는지, 또는 `tools.helm` 경로를 확인합니다 |
| 데이터스토어가 `unknown`으로 표시됩니다 | `k3s.data_dir`이 실제 data-dir과 같은지 확인합니다 |
| 화면이 깨집니다 | 256색 이상을 지원하는 터미널과 `TERM=xterm-256color`를 사용합니다 |
| 동작 기록을 보고 싶습니다 | `/var/log/k3stui/k3stui.log`(앱 로그), `/var/log/k3stui/audit.log`(변경 작업) |
