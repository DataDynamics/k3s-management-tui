# K3S Management TUI — 설계 문서

> 작성일: 2026-10-05 · 상태: M1~M5 구현 완료 (12장 구현 노트 참고)

## 1. 목표와 범위

이 서버에 설치된 K3S를 터미널 하나에서 관리하는 TUI 도구를 만듭니다.
`kubectl`/`k9s`가 다루는 **Kubernetes 리소스 관리**에 더해, 일반 도구가 다루지 않는
**K3S 호스트 측 관리**(서비스, 설정 파일, 데이터스토어, 자동 배포 manifest, containerd)를 함께 제공하는 것이 차별점입니다.

### 1.1 대상 환경 (현재 서버 실측)

| 항목 | 값 | 설계 영향 |
|---|---|---|
| OS | Ubuntu 24.04.5, 64 core, 376Gi RAM | 리소스 제약 없음 |
| K3S | v1.36.5+k3s1, `k3s.service` (server 단일 노드) | systemd 연동 필요, agent 노드는 추후 |
| 설정 | `/etc/rancher/k3s/config.yaml` | 설정 뷰어/편집기 대상 |
| kubeconfig | `/etc/rancher/k3s/k3s.yaml` (mode 0600) | **root 실행 전제**, 아니면 `--kubeconfig` 지정 |
| data-dir | `/data2/k3s` (기본값 아님) | 경로를 하드코딩하지 말고 config.yaml에서 읽어야 함 |
| 데이터스토어 | SQLite/kine (`/data2/k3s/server/db/state.db`) | `k3s etcd-snapshot` 불가 → SQLite 백업 방식 별도 구현 |
| CNI | Cilium 1.20.2 (Helm), flannel/traefik/servicelb 비활성화 | Cilium 상태 패널, 비활성 컴포넌트는 숨김 |
| 기타 | metrics-server 동작, local-path SC, helm/crictl 설치됨 | CPU/메모리 실시간 표시 가능 |

### 1.2 비목표 (v1)
- 멀티 클러스터 관리, 원격 노드 SSH 관리
- K3S 설치/업그레이드 자동화 (상태 표시와 안내만)
- 웹 UI

## 2. 기술 선택

| 영역 | 선택 | 이유 |
|---|---|---|
| 언어 | **Go 1.27** (설치됨) | K3S·client-go와 같은 생태계, 단일 정적 바이너리 배포 |
| TUI | **Bubble Tea v2** + Bubbles + Lip Gloss | Elm 아키텍처로 상태 관리가 명확, 테스트 용이 |
| K8s 접근 | **client-go** (Informer 기반 watch) | 폴링 없이 실시간 갱신, API 서버 부하 최소 |
| 메트릭 | `k8s.io/metrics` (metrics-server) | `kubectl top`과 동일 소스 |
| 호스트 연동 | `os/exec` 래퍼 (`systemctl`, `journalctl`, `k3s`, `crictl`) | 각 도구의 공식 CLI를 그대로 사용, 경로는 conf에서 지정 |
| 설정 | YAML (`gopkg.in/yaml.v3`) | K3S 설정과 동일 포맷 |
| 로깅 | `log/slog` → 파일 | TUI 화면을 오염시키지 않도록 stdout 사용 금지 |

대안 검토: Python(Textual)은 개발 속도는 빠르지만 런타임 의존성이 있고 client-go Informer 같은 성숙한 watch 캐시가 없습니다. Rust(ratatui + kube-rs)도 가능하지만 K3S 내부 구조 참고와 생태계 측면에서 Go가 유리합니다.

## 3. 디렉터리 구조

```
k3s-management-tui/
├── bin/                          # 실행 진입점 (사용자가 직접 실행하는 것들)
│   ├── k3stui                    # 런처 스크립트: conf 경로 해석, 권한 확인 후 바이너리 실행
│   ├── build.sh                  # src → build/k3stui-<ver>-linux-amd64 빌드
│   └── install.sh                # /opt/k3stui 설치 + /usr/local/bin 심볼릭 링크
│
├── conf/                         # 설정 (코드와 분리, 배포 시 그대로 복사)
│   ├── k3stui.yaml               # 메인 설정 (경로, 갱신 주기, 기능 on/off, 안전 모드)
│   ├── keybindings.yaml          # 키 매핑 (사용자 재정의 가능)
│   ├── theme.yaml                # 색상 테마 (dark/light)
│   └── views.d/                  # 리소스별 컬럼 정의 (pods.yaml, deployments.yaml ...)
│
├── src/                          # Go 모듈 루트 (go.mod 위치)
│   ├── go.mod
│   ├── cmd/k3stui/main.go        # 플래그 파싱 → config 로드 → app 실행
│   └── internal/
│       ├── config/               # conf/*.yaml 로딩·검증·기본값 병합
│       ├── app/                  # 루트 Bubble Tea 모델, 화면 라우팅, 전역 키 처리
│       ├── ui/
│       │   ├── components/       # table, tabs, statusbar, dialog(confirm), logviewer, yamlviewer, filter
│       │   ├── styles/           # theme.yaml → lipgloss 스타일
│       │   └── views/            # 화면 단위 모델 (dashboard, resources, logs, host, ...)
│       ├── kube/                 # client-go 래퍼: clientset, informer 팩토리, 리소스 레지스트리, 액션
│       ├── k3s/                  # K3S 호스트 측: service, config, datastore, manifests, token, cert
│       ├── runtime/              # containerd(crictl) 조회
│       ├── executil/             # 외부 명령 실행 (타임아웃, 컨텍스트 취소, stderr 수집)
│       └── audit/                # 변경 작업 감사 로그
│
├── docs/                         # 설계·사용 문서
├── build/                        # 빌드 산출물 (.gitignore)
└── logs/                         # 개발 시 로그 (.gitignore, 운영은 /var/log/k3stui)
```

### 3.1 디렉터리 역할 원칙
- **bin/**: 쉘 스크립트만 둡니다. 로직은 넣지 않고 "환경 확인 → 바이너리 호출"만 합니다.
- **conf/**: 코드 수정 없이 동작을 바꿀 수 있는 모든 것. 바이너리에는 기본값이 내장되어 conf 파일이 없어도 동작합니다.
- **src/**: 순수 Go 코드. `internal/`로 외부 import를 막습니다.

### 3.2 설정 파일 탐색 순서
1. `--config` 플래그
2. `$K3STUI_CONF` 환경변수
3. `bin/../conf/k3stui.yaml` (런처 스크립트 기준 상대 경로 — 저장소에서 바로 실행할 때)
4. `/etc/k3stui/k3stui.yaml`
5. 내장 기본값

## 4. 설정 파일 (conf/k3stui.yaml)

```yaml
k3s:
  config_file: /etc/rancher/k3s/config.yaml
  kubeconfig: /etc/rancher/k3s/k3s.yaml
  service_name: k3s                 # agent 노드면 k3s-agent
  binary: /usr/local/bin/k3s
  data_dir: auto                    # auto = config.yaml의 data-dir 사용, 없으면 /var/lib/rancher/k3s

ui:
  refresh_interval: 2s              # 메트릭/호스트 상태 갱신 주기 (리소스는 watch)
  default_namespace: all
  default_view: dashboard
  theme: dark
  log_tail_lines: 500

safety:
  read_only: false                  # true면 모든 변경 작업 비활성화
  confirm_destructive: true         # 삭제/재시작/drain은 확인 다이얼로그
  protected_namespaces: [kube-system, kube-public, kube-node-lease]  # 삭제 시 이름 재입력 요구

backup:
  dir: /data2/k3stui-backups
  keep: 7

logging:
  file: /var/log/k3stui/k3stui.log
  level: info
audit:
  file: /var/log/k3stui/audit.log
```

## 5. 화면 설계

### 5.1 레이아웃

```
┌ K3S v1.36.5+k3s1 │ dev-sever.dev.net │ k3s.service ● active │ ns: all ─────────── 14:32:05 ┐
│ [1]Dashboard [2]Workloads [3]Network [4]Storage [5]Config [6]Host [7]Helm          │
├───────────────────────────────────────────────────────────────────────────────────┤
│                                                                                   │
│                             (현재 뷰 본문)                                          │
│                                                                                   │
├───────────────────────────────────────────────────────────────────────────────────┤
│ /필터  :명령  l 로그  d 상세  e 편집  x 삭제  ? 도움말  q 종료        [READ-ONLY]   │
└───────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 뷰 목록

| # | 뷰 | 내용 | 주요 동작 |
|---|---|---|---|
| 1 | **Dashboard** | 노드 상태·CPU/메모리 게이지, 네임스페이스별 Pod 수, 비정상 Pod 목록, 최근 Warning 이벤트, k3s 서비스 상태 | 항목 선택 시 해당 리소스로 점프 |
| 2 | **Workloads** | Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs | 로그, exec 셸, 스케일, 롤아웃 재시작, 삭제, YAML 보기/편집 |
| 3 | **Network** | Services, Ingresses, Endpoints, NetworkPolicies, Cilium 상태 | port-forward 시작/중지 |
| 4 | **Storage** | PV, PVC, StorageClass, local-path 실제 사용량 | PVC 삭제, 호스트 경로 표시 |
| 5 | **Config** | ConfigMaps, Secrets(값 마스킹), ServiceAccounts, RBAC | Secret 값 토글 표시, 편집 |
| 6 | **Host (K3S)** | 아래 6장 참조 | 서비스 제어, 설정 편집, 백업 |
| 7 | **Helm** | Helm releases + K3S `HelmChart` CRD | values 보기, 이력, 롤백 |

공통: Nodes와 Events는 `:nodes`, `:events` 명령으로 진입 (cordon/uncordon/drain 지원).

### 5.3 키 체계 (conf/keybindings.yaml 기본값)

| 키 | 동작 | 키 | 동작 |
|---|---|---|---|
| `1`~`7` | 뷰 전환 | `/` | 현재 테이블 필터 |
| `:` | 명령 모드 (`:pods`, `:ns kube-system`) | `n` | 네임스페이스 선택 |
| `Enter`/`d` | 상세(describe) | `y` | YAML 보기 |
| `e` | `$EDITOR`로 편집 후 apply | `l` | 로그 (follow 토글 `f`) |
| `s` | exec 셸 | `x` | 삭제 (확인) |
| `r` | 롤아웃 재시작 | `Esc` | 뒤로 |
| `?` | 도움말 | `q` | 종료 |

`e`, `s`는 `tea.ExecProcess`로 TUI를 일시 중단하고 외부 프로세스에 터미널을 넘깁니다.

## 6. K3S 호스트 관리 (차별 기능)

`internal/k3s` 패키지가 담당합니다. 모두 root 권한이 필요하며, root가 아니면 뷰는 읽기 가능한 정보만 보여주고 동작은 비활성화합니다.

| 기능 | 구현 방법 | 비고 |
|---|---|---|
| 서비스 상태 | `systemctl show k3s -p ActiveState,SubState,MainPID,ExecMainStartTimestamp,MemoryCurrent` | 파싱 쉬운 key=value 출력 |
| 서비스 제어 | `systemctl restart/stop/start k3s` | 단일 노드에서 stop은 TUI 자신의 API 연결도 끊김 → 경고 표시 |
| 서비스 로그 | `journalctl -u k3s -f -o short-iso` 스트리밍 | 레벨(`level=error`) 하이라이트/필터 |
| config.yaml | 뷰어 + `$EDITOR` 편집 → YAML 검증 → 백업 후 저장 → 재시작 여부 질의 | 변경 diff 표시 |
| 데이터 디렉터리 | config.yaml의 `data-dir` 해석, 디스크 사용량 (`/data2/k3s`, kubelet root-dir) | |
| 데이터스토어 백업 | **SQLite**: `sqlite3 .backup` 방식(온라인 백업 API)으로 `state.db` 복사, 보관 개수 관리 | 현재 서버는 kine/SQLite. embedded etcd일 때만 `k3s etcd-snapshot save/ls` 메뉴 표시 |
| 자동 배포 manifest | `<data-dir>/server/manifests/*.yaml` 목록, 내용 보기, `.skip` 파일 토글 | K3S 고유 기능 |
| 토큰 | `<data-dir>/server/token` (마스킹, 복사) | 노드 추가 안내 명령 생성 |
| 인증서 | `<data-dir>/server/tls/*.crt` 만료일 표시, `k3s certificate rotate` 안내 | 30일 이내 만료 경고 |
| 컨테이너 런타임 | `k3s crictl ps/images/stats` | 미사용 이미지 정리 (`crictl rmi --prune`) |
| 진단 | `k3s check-config` 결과 요약 | |

데이터스토어 종류는 `<data-dir>/server/db/state.db` 존재 여부(SQLite)와 `<data-dir>/server/db/etcd` 존재 여부(etcd)로 자동 판별합니다.

## 7. 내부 아키텍처

### 7.1 구성도

```
          ┌──────────────────────── app (root tea.Model) ──────────────────────┐
 키 입력 → │  router ─ views/{dashboard, resources, logs, host, helm}           │ → 화면
          │     ▲                         │                                    │
          │     │ tea.Msg                 │ tea.Cmd (비동기 작업)               │
          └─────┼─────────────────────────┼────────────────────────────────────┘
                │                         ▼
        ┌───────┴─────────┐    ┌──────────────────────┐   ┌───────────────┐
        │ kube.Store      │    │ kube.Actions          │   │ k3s.Host      │
        │ (Informer 캐시)  │    │ delete/scale/restart  │   │ systemd/journal│
        │ 변경 → Msg 발행  │    │ logs/exec/portfwd     │   │ config/backup │
        └───────┬─────────┘    └──────────┬───────────┘   └──────┬────────┘
                │ watch                   │ REST                 │ exec
                ▼                         ▼                      ▼
                    K3S API Server (127.0.0.1:6443)          호스트 OS
```

### 7.2 핵심 규칙
1. **UI 스레드에서 블로킹 I/O 금지.** 모든 API 호출/명령 실행은 `tea.Cmd`로 감싸고 결과를 `tea.Msg`로 돌려받습니다.
2. **리소스 목록은 Informer 캐시에서 읽습니다.** Informer 이벤트는 채널 → `p.Send()`로 프로그램에 전달하되, 100ms 단위로 묶어(debounce) 화면 갱신 폭주를 막습니다.
3. **Informer는 지연 시작.** 사용자가 해당 리소스 뷰를 처음 열 때 시작하고, 네임스페이스 필터는 캐시에서 처리합니다.
4. **리소스 레지스트리.** 각 리소스 종류는 `ResourceDef{GVR, Columns, RowFunc, Actions}`로 등록합니다. 컬럼 정의는 `conf/views.d/`로 덮어쓸 수 있습니다. 새 리소스 추가 = 정의 하나 추가.
5. **모든 변경 작업은 하나의 경로를 거칩니다:** `read_only 확인 → 확인 다이얼로그 → 실행 → audit 기록 → 결과 토스트`.

### 7.3 주요 인터페이스 (요지)

```go
// internal/kube
type ResourceDef struct {
    Name    string                       // "pods"
    GVR     schema.GroupVersionResource
    Columns []Column
    Row     func(obj *unstructured.Unstructured) []string
    Actions []ActionID                   // logs, exec, scale, restart, delete ...
}

// internal/k3s
type Host interface {
    ServiceStatus(ctx context.Context) (ServiceStatus, error)
    ServiceControl(ctx context.Context, op ServiceOp) error   // start|stop|restart
    StreamJournal(ctx context.Context, follow bool) (<-chan string, error)
    LoadConfig() (*K3sConfig, error)
    Datastore() DatastoreKind                                  // SQLite | Etcd | External
    Backup(ctx context.Context) (path string, err error)
    Manifests() ([]Manifest, error)
    Certificates() ([]CertInfo, error)
}
```

`Host`를 인터페이스로 두어 테스트에서는 가짜 구현을 주입합니다 (실제 서비스를 재시작하지 않고 UI 테스트).

## 8. 안전·보안

- **read-only 모드**: `--read-only` 플래그 또는 conf. 상태바에 항상 표시.
- **확인 단계**: 일반 삭제는 `y/N`, `protected_namespaces` 내 리소스 삭제와 k3s stop/restart는 리소스 이름 재입력.
- **Secret**: 기본 마스킹, 표시 토글 시 audit 기록.
- **감사 로그**: JSON Lines — `{time, user(SUDO_USER 포함), action, target, result}`.
- **권한**: kubeconfig가 0600이므로 일반 사용자는 `bin/k3stui`가 안내 메시지 출력 후 종료 (sudo 권장). 호스트 기능 없이 쓰려면 별도 kubeconfig 지정.
- 외부 명령은 인자 배열로만 실행 (쉘 경유 금지), 모두 타임아웃 적용.

## 9. 빌드·배포·테스트

```bash
bin/build.sh                 # cd src && go build -trimpath -ldflags "-X main.version=..." -o ../build/k3stui
bin/k3stui                   # 저장소에서 바로 실행 (conf/ 자동 인식)
sudo bin/install.sh          # /opt/k3stui/{bin,conf} 설치, /usr/local/bin/k3stui 링크
```

- **단위 테스트**: config 병합, 리소스 Row 함수, 데이터스토어 판별, journal 파싱
- **UI 테스트**: `teatest`로 키 입력 → 화면 골든 파일 비교 (fake clientset + fake Host)
- **통합 테스트**: 실제 K3S에 `k3stui-e2e` 네임스페이스를 만들어 생성/스케일/삭제 검증 후 정리

## 10. 개발 단계

| 단계 | 범위 | 완료 기준 |
|---|---|---|
| **M1 골격** | 디렉터리, go.mod, config 로딩, bin 스크립트, 빈 TUI(탭·상태바·도움말) | `bin/k3stui` 실행 시 화면 표시, `q` 종료 |
| **M2 조회** | Informer Store, 리소스 레지스트리, Workloads/Network/Storage/Config 테이블, 필터, 네임스페이스 전환, describe/YAML | 모든 뷰가 실시간 갱신 |
| **M3 Dashboard·Host 조회** | 메트릭, 이벤트, k3s 서비스 상태, journal, config.yaml/manifest/인증서 보기 | Host 뷰 읽기 기능 완성 |
| **M4 변경 작업** | 삭제, 스케일, 재시작, 편집, 로그 follow, exec, cordon/drain, 서비스 제어, audit | 확인 다이얼로그·read-only 동작 |
| **M5 운영 기능** | SQLite 백업/보관, Helm 뷰, crictl, port-forward, 테마/키 재정의 | 설치 스크립트로 /opt 배포 |

## 11. 결정 사항

| 항목 | 결정 |
|---|---|
| 언어 | Go 1.27 + Bubble Tea v2 / Lip Gloss v2 / Bubbles v2, client-go v0.37 |
| 실행 권한 | root 실행이 기본. 일반 사용자는 `--kubeconfig`로 리소스 기능만 사용 (호스트 기능 비활성) |
| 편집 방식 | 외부 `$EDITOR` 호출 (리소스는 `kubectl edit`, config.yaml은 임시 파일 → 검증 → diff 확인) |
| 설치 위치 | `/opt/k3stui/{bin,libexec,conf}` + `/usr/local/bin/k3stui` 심볼릭 링크 |

## 12. 구현 노트 (설계와 달라진 점)

- **패키지 추가**: `internal/helm`(helm CLI 래퍼), `internal/textdiff`(config.yaml 변경 diff). Helm은 Go SDK 대신 설치된 `helm` CLI를 써서
  v3/v4 차이를 흡수합니다 (`list -a`가 v4에서 없어져 자동 재시도).
- **화면 구조**: 탭마다 페이지 스택을 두고, 표 화면(TablePage)은 여러 "소스"를 하위 탭으로 보여줍니다.
  소스 = Kubernetes 리소스(Informer 캐시) 또는 비동기 소스(호스트 항목, crictl, helm). 새 화면 = 소스 하나 추가.
- **작업 절차**: 모든 작업은 `Action` 하나로 정의하고, 앱이 `read-only/root 확인 → 선택(컨테이너 등) → 값 입력 → 확인 → 실행 → 감사 기록 → 알림`을 공통 처리합니다.
- **키 충돌**: 서비스 로그는 `j` 대신 `l`, check-config는 `k` 대신 `c` (이동 키 j/k와 충돌). 작업 키가 전역 키와 겹치지 않는지 테스트로 검사합니다.
- **q 동작**: 하위 화면(로그·YAML 등)에서는 뒤로, 최상위에서만 종료.
- **SQLite 백업**: `VACUUM INTO`(modernc.org/sqlite, cgo 불필요)로 실행 중 일관된 사본을 만들고 서버 토큰을 `<백업>.token`으로 함께 저장합니다.
  복원은 무결성·kine 테이블·토큰 일치 검사 후 진행합니다. etcd는 `k3s etcd-snapshot`으로 백업만 지원하고 복원은 안내 문구로 대신합니다.
- **비대화형 모드**: 점검·스크립트용으로 `--check`(환경 점검), `--dump <소스>`(표 출력)를 추가했습니다.
- **Cilium**: Network 탭에 정책 CRD와 에이전트 Pod 목록을 두고 `cilium-dbg status/endpoint/service`를 실행해 보여줍니다 (CRD가 없으면 숨김).

### 검증 현황 (2026-10-05, 이 서버)

| 구분 | 내용 |
|---|---|
| 단위 테스트 | config, k3s(서비스 파싱·config drop-in·SQLite 백업/정리/복원·토큰 검사·manifest·인증서), kube(Pod 상태·행·drain), runtime, helm, components, app(확인 절차·read-only·보호 NS·명령 모드) |
| 통합 테스트 (`-tags e2e`) | 실제 K3S에서 생성 → 스케일 → 재시작 → port-forward HTTP → 로그 → 삭제 |
| 수동 확인 (tmux) | 대시보드, 필터, 네임스페이스 선택, 로그/YAML/describe, 스케일, 롤아웃 재시작, CronJob 실행, 서비스 port-forward, Pod 셸·삭제, kubectl edit, config.yaml diff(취소), 실제 SQLite 백업, read-only 차단, 일반 사용자 실행 |
| 실서버 미실행 | k3s 재시작/중지, 인증서 갱신, 데이터스토어 복원, drain/cordon (서비스 영향이 있어 가짜 Host·fake clientset 테스트로만 검증) |

## 13. 일반 Kubernetes 지원 (1단계, 2026-10-05)

K3S가 아닌 클러스터에서도 리소스 관리 도구로 쓸 수 있게, 시작할 때 동작 모드를 정합니다.

### 13.1 판별 순서

1. **kubeconfig 선택**: `--kubeconfig` → `cluster.kubeconfig`(이전 `k3s.kubeconfig` 호환) → `$KUBECONFIG` → `~/.kube/config`
   → `/etc/rancher/k3s/k3s.yaml` → `/etc/rancher/rke2/rke2.yaml` → `/etc/kubernetes/admin.conf`.
   사용자가 지정한 경로는 그대로 쓰고, 자동 탐색 후보는 context와 API 서버 주소가 있는지 확인한 뒤 씁니다.
   이 서버처럼 내용이 빈 `~/.kube/config`가 있어도 건너뜁니다.
2. **context**: `--context` 또는 `cluster.context`. 비우면 current-context를 씁니다. kubectl에는 `--context`, helm에는 `--kube-context`로 넘깁니다.
3. **배포판**: `cluster.distribution`이 `auto`면 API 서버 버전(`+k3s`, `+rke2`, `-eks-`, `-gke.`)으로 판별하고,
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
- 실제 kubeadm, EKS, GKE 클러스터에서는 아직 확인하지 않았습니다. 이 서버에는 K3S만 있습니다.

### 13.4 다음 단계 (2단계)

kubeadm 노드용 `Host` 구현을 추가합니다: kubelet 서비스, `/etc/kubernetes/manifests`, `/etc/kubernetes/pki`와 `kubeadm certs`,
`etcdctl snapshot`, crictl 소켓 설정, `kubeadm token create --print-join-command`.

### 13.5 설정 보강 (2026-10-05)

- **사용자 설정 위치**: 설정 탐색 순서에 `~/.config/k3stui/k3stui.yaml`을 추가했습니다 (`$K3STUI_CONF` 다음). 설치본을 고치지 않고 클러스터별 설정을 둘 수 있습니다.
- **k8s 설정 예제**: `conf/examples/k3stui-k8s.yaml`에 원격 클러스터용 값과 각 항목의 의미를 적었습니다.
- **views.d path 문법 확장**: `a.b`에 더해 `a["점.이.든/키"]`, `a[0]`, `a[*]`를 지원합니다. 맵 값은 키 순서로 정렬해 표시합니다.
- **views.d 검증**: 시작할 때 별칭 파일 이름을 리소스 키로 바꾸고, 모르는 리소스·없는 내장 컬럼·잘못된 path·중복 재정의를 경고합니다.
  경고는 첫 화면 하단, 로그, `--check`에 표시하며, 잘못된 컬럼만 빼고 나머지는 적용합니다.
- **`--columns`**: 리소스 키·별칭·내장 컬럼을 출력합니다. views.d 문서의 리소스 표도 같은 정보로 만들었습니다.
- **예제 검증 테스트**: 저장소의 `views.d/*.yaml.example`, `conf/k3stui.yaml`, `conf/examples/k3stui-k8s.yaml`이 경고 없이 읽히는지 테스트로 확인합니다.
