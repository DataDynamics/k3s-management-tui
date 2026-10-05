# K3s & K8s Management TUI (k3stui)

K3s와 Kubernetes(K8s) 클러스터를 터미널 하나에서 관리하는 TUI입니다.

k9s처럼 Pod, Deployment, Service 같은 **Kubernetes 리소스**를 다루는 데 그치지 않고,
일반 도구가 다루지 않는 **노드(호스트) 관리**까지 한 화면에서 합니다.
서비스 제어와 로그, 설정 파일 편집, 데이터스토어 백업, 인증서 갱신, 노드 추가 명령, containerd 이미지·컨테이너 정리를 TUI 안에서 처리할 수 있습니다.

노드 관리는 TUI를 실행한 노드의 배포판을 자동으로 판별해 그에 맞게 구성합니다.

| 배포판 | 리소스 관리 | 노드 관리 (Host 탭) |
|---|---|---|
| **K3s** | 지원 | k3s 서비스, config.yaml, 자동 배포 manifest, SQLite·etcd 백업과 SQLite 복원, 인증서 갱신, 노드 추가 명령 |
| **RKE2** | 지원 | rke2-server/agent 서비스, config.yaml, 자동 배포 manifest, etcd 스냅샷, 인증서 갱신, 노드 추가 명령 |
| **kubeadm** | 지원 | kubelet 서비스와 설정, static Pod 편집, PKI 인증서 갱신, etcd 스냅샷, 노드 추가 토큰 |
| 그 밖의 Kubernetes (EKS·GKE·AKS 등 관리형, 원격 클러스터) | 지원 | 없음 |

노드 관리는 API 서버가 있는 노드에서 실행할 때 켜지며, 서비스 제어·편집·백업 같은 변경 작업에는 root 권한이 필요합니다.
원격에서 접속하거나 그 밖의 배포판이면 리소스 관리 기능만 씁니다.
자세한 내용은 [다른 Kubernetes 클러스터에서 사용](#다른-kubernetes-클러스터에서-사용)을 참고하세요.

- 설계 문서: [docs/DESIGN.md](docs/DESIGN.md)
- 컬럼 재정의: [conf/views.d/README.md](conf/views.d/README.md)
- 설정 예제: [원격·관리형 클러스터](conf/examples/k3stui-k8s.yaml), [RKE2 노드](conf/examples/k3stui-rke2.yaml), [kubeadm 노드](conf/examples/k3stui-kubeadm.yaml)
- 화면별 스크린샷: [스크린샷](#스크린샷)

![대시보드](docs/images/dashboard.png)

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

## 스크린샷

실제 K3S 서버(v1.36.5+k3s1, 단일 노드, Cilium, SQLite 데이터스토어)에서 실행한 화면입니다.
150×42 터미널에서 촬영했습니다.

### Dashboard

클러스터 요약, K3S 호스트 상태, 노드 사용량, 문제 Pod, 최근 Warning 이벤트를 한 화면에 보여줍니다.

![Dashboard](docs/images/dashboard.png)

### Workloads

Pod 목록에는 상태, 재시작 횟수, metrics-server 기준 CPU·메모리 사용량이 함께 표시됩니다.

![Workloads - Pods](docs/images/workloads-pods.png)

![Workloads - Deployments](docs/images/workloads-deployments.png)

Pod에서 `l`을 누르면 로그를 실시간으로 따라가고, `y`를 누르면 YAML을 보여줍니다.

![Pod 로그](docs/images/pod-logs.png)

![Pod YAML](docs/images/pod-yaml.png)

Deployment에서 `S`를 누르면 replicas를 입력받아 스케일합니다.

![스케일 다이얼로그](docs/images/dialog-scale.png)

### Network

![Network - Services](docs/images/network-services.png)

Service나 Pod에서 `p`로 시작한 포트포워딩은 Port-forwards 하위 탭에서 관리합니다.

![Network - Port-forwards](docs/images/network-portforwards.png)

Cilium 하위 탭에서는 에이전트 Pod의 `cilium-dbg status`를 바로 확인할 수 있습니다.

![Cilium 상태](docs/images/network-cilium-status.png)

### Storage

![Storage - PVC](docs/images/storage-pvc.png)

local-path 저장 경로의 실제 디스크 사용량을 PVC별로 계산합니다.

![Storage - Local-path 사용량](docs/images/storage-localpath.png)

### Config

![Config - ConfigMaps](docs/images/config-configmaps.png)

### Host (노드 관리)

아래는 K3S 노드 화면입니다. kubeadm 노드에서는 같은 자리에 kubelet 서비스, kubelet config, Static Pods, PKI 인증서, etcd 스냅샷이 나옵니다
(「[kubeadm 노드 관리](#kubeadm-노드-관리)」 참고).

k3s 서비스 상태, 버전, API 서버 상태, 데이터스토어, 디스크 사용량을 보여주고 서비스를 제어합니다.

![Host - Service](docs/images/host-service.png)

`l`을 누르면 `journalctl -u k3s` 로그를 실시간으로 보여주며, 오류와 경고는 색으로 구분합니다.

![Host - k3s 서비스 로그](docs/images/host-journal.png)

`/etc/rancher/k3s/config.yaml`을 보고 편집합니다. 토큰처럼 민감한 값은 가려서 표시합니다.

![Host - config.yaml](docs/images/host-config.png)

![Host - Manifests](docs/images/host-manifests.png)

인증서는 만료가 빠른 순서로 정렬되며, 30일 이내 만료는 노란색, 만료된 인증서는 빨간색으로 표시합니다.

![Host - Certificates](docs/images/host-certificates.png)

![Host - Containers](docs/images/host-containers.png)

![Host - Images](docs/images/host-images.png)

### Helm

![Helm - Releases](docs/images/helm-releases.png)

### 노드, 네임스페이스 선택, 도움말

`:nodes` 명령으로 노드 목록을 열고 cordon, uncordon, drain을 실행합니다.

![Nodes](docs/images/nodes.png)

`n`을 누르면 네임스페이스를 고를 수 있으며, 입력하면 목록이 걸러집니다.

![네임스페이스 선택](docs/images/dialog-namespace.png)

`?`를 누르면 공통 키와 현재 화면에서 쓸 수 있는 작업 키를 모두 보여줍니다.

![도움말](docs/images/help.png)

### 보호 네임스페이스 삭제 확인

`kube-system` 같은 보호 네임스페이스의 리소스를 삭제하거나 변경하려면 리소스 이름을 다시 입력해야 합니다.

![보호 네임스페이스 삭제 확인](docs/images/dialog-delete-protected.png)

## 요구 사항

- K3S 노드에서 호스트 관리까지 쓰려면: K3S가 설치된 Linux 서버(systemd 사용)와 root 권한이 필요합니다 (K3S kubeconfig는 기본 권한이 `0600`입니다).
- RKE2 노드에서 호스트 관리까지 쓰려면: RKE2가 설치된 노드(systemd 사용)와 root 권한이 필요합니다.
- kubeadm 노드에서 호스트 관리까지 쓰려면: kubeadm·kubelet·crictl이 있는 노드와 root 권한이 필요합니다. etcdctl은 없어도 됩니다 (etcd 컨테이너 안의 것을 씁니다).
- 다른 Kubernetes 클러스터에 붙이려면: 접속할 수 있는 kubeconfig와 `kubectl`만 있으면 됩니다.
- 빌드할 때만 Go 1.27 이상이 필요합니다. 실행 파일은 정적 바이너리 하나입니다.
- 선택: `helm`(Helm 탭), metrics-server(CPU·메모리 표시, K3S에 기본 포함)

## 빠른 시작

```bash
bin/build.sh              # src → build/k3stui
sudo bin/k3stui --check   # 환경 점검
sudo bin/k3stui           # 저장소에서 바로 실행 (conf/ 자동 인식)
```

`--check`는 kubeconfig, API 서버, 배포판, metrics-server, helm, kubectl을 점검하고,
호스트 관리가 켜져 있으면 root 권한, 노드 서비스(k3s 또는 kubelet), 버전, 노드 디렉터리, 설정 파일, 데이터스토어까지 확인해 다음과 같이 보여줍니다.

```
[OK  ] kubeconfig      /etc/rancher/k3s/k3s.yaml (K3S 기본 경로)
[OK  ] API 서버        v1.36.5+k3s1  https://127.0.0.1:6443 (context: default)
[OK  ] 배포판          K3S (API 서버: 로컬)
[OK  ] 호스트 관리     K3S
[OK  ] 서비스          k3s.service active/running
[OK  ] 노드 디렉터리   /data2/k3s
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
| `--kubeconfig <파일>` | kubeconfig를 지정합니다 (기본: 자동 탐색, 아래 참고) |
| `--context <이름>` | kubeconfig의 context를 고릅니다 (기본: current-context) |
| `--distribution <배포판>` | 배포판을 직접 지정합니다 (`auto`, `k3s`, `rke2`, `kubeadm`, `kubernetes`) |
| `--read-only` | 모든 변경 작업을 막습니다 |
| `-n <네임스페이스>` | 시작 네임스페이스를 지정합니다 (`all`은 전체) |
| `--view <탭>` | 시작 탭을 지정합니다 (`dashboard`, `workloads`, `network`, `storage`, `config`, `host`, `helm`). Host 탭이 없으면 대시보드로 시작합니다 |
| `--check` | 환경을 점검하고 종료합니다 |
| `--columns [리소스]` | 컬럼 재정의(views.d)에 쓸 리소스 이름과 내장 컬럼을 출력하고 종료합니다 |
| `--dump <소스>` | 소스 하나를 표로 출력하고 종료합니다 (예: `pods`, `nodes`, `service`, `certs`, `backups`, `images`, `releases`) |
| `--version` | 버전을 출력합니다 |

설정 파일은 다음 순서로 찾습니다. 파일이 없어도 내장 기본값으로 동작합니다.

1. `--config <파일>`
2. `$K3STUI_CONF`
3. `~/.config/k3stui/k3stui.yaml` (사용자별 설정, 설치본을 고치지 않고 클러스터별 설정을 둘 때)
4. `<설치 경로>/conf/k3stui.yaml`
5. `/etc/k3stui/k3stui.yaml`

`keybindings.yaml`, `theme.yaml`, `views.d/`는 고른 설정 파일과 같은 디렉터리에서 찾습니다.

kubeconfig는 다음 순서로 찾고, 자동 탐색 후보 중 context와 API 서버 주소가 실제로 들어 있는 첫 파일을 씁니다.
그래서 내용이 빈 `~/.kube/config`가 있어도 건너뜁니다.

1. `--kubeconfig`
2. 설정 파일의 `cluster.kubeconfig`
3. `$KUBECONFIG` (여러 파일을 `:`로 이은 형식 포함)
4. `~/.kube/config`
5. `/etc/rancher/k3s/k3s.yaml` (K3S)
6. `/etc/rancher/rke2/rke2.yaml` (RKE2)
7. `/etc/kubernetes/admin.conf` (kubeadm)

일반 사용자도 읽을 수 있는 kubeconfig가 있으면 root 없이 실행할 수 있습니다.
이때 Kubernetes 리소스 기능만 쓸 수 있고, 서비스 제어·설정 편집·백업 같은 호스트 기능은 비활성화됩니다.

## 다른 Kubernetes 클러스터에서 사용

k3stui는 시작할 때 배포판과 API 서버 위치를 판별해 동작 모드를 정합니다.

| 상황 | 동작 |
|---|---|
| K3S 서버 노드에서 실행 (API 서버가 이 호스트) | 모든 기능을 씁니다. 탭은 7개(Host 포함)입니다 |
| RKE2 서버 노드에서 실행 | 모든 기능을 씁니다. Host 탭은 K3S와 같은 구성입니다 (아래 「RKE2 노드 관리」) |
| kubeadm 컨트롤 플레인 노드에서 실행 | 모든 기능을 씁니다. Host 탭은 kubeadm용으로 구성됩니다 (아래 「kubeadm 노드 관리」) |
| 원격 K3S·RKE2·kubeadm, 관리형 클러스터(EKS, GKE, AKS 등) | Host 탭과 대시보드 호스트 패널을 숨기고, 대시보드 오른쪽에 연결 정보(배포판·context·API 서버·kubeconfig)를 보여줍니다. 탭은 6개입니다 |

- **배포판 판별**: API 서버 버전(`+k3s`, `+rke2`)을 보고, 알 수 없으면 `kube-system/kubeadm-config` ConfigMap이 있는지로 kubeadm을 확인합니다. 그래도 모르면 일반 Kubernetes(`kubernetes`)로 봅니다.
  `distribution` 값은 `auto`, `k3s`, `rke2`, `kubeadm`, `kubernetes`이며, 관리형 클러스터는 별도 값 없이 `kubernetes`로 동작합니다.
- **로컬 판별**: API 서버 주소가 루프백이거나 이 호스트의 네트워크 인터페이스 IP이면 로컬로 봅니다.
- **헤더**: 로컬 K3S 모드에서는 호스트 이름과 k3s 서비스 상태를, 그 밖에는 `ctx: <context>`를 표시합니다.
- **kubectl**: 로컬 K3S면 `k3s kubectl`, 로컬 RKE2면 `<data-dir>/bin/kubectl`, 그 밖에는 PATH의 `kubectl`을 쓰며, kubeconfig와 context를 함께 넘깁니다. Helm도 같은 kubeconfig와 `--kube-context`를 씁니다.
- **local-path 사용량**: `rancher.io/local-path` StorageClass가 있고 API 서버가 로컬일 때만 보여줍니다.

설정 예제 [conf/examples/k3stui-k8s.yaml](conf/examples/k3stui-k8s.yaml)에 각 항목의 의미와 권장값을 정리해 두었습니다.

```bash
mkdir -p ~/.config/k3stui
cp conf/examples/k3stui-k8s.yaml ~/.config/k3stui/k3stui.yaml   # 사용자 설정으로 복사
k3stui --context prod-cluster                                   # 이후에는 옵션 없이도 이 설정을 씁니다
```

예시:

```bash
k3stui --kubeconfig ~/.kube/config --context prod-cluster      # 원격 클러스터
k3stui --context kind-dev                                       # $KUBECONFIG 또는 ~/.kube/config의 context
sudo k3stui --distribution kubernetes                           # K3S 노드지만 리소스 관리만 쓰기
```

호스트 관리 사용 여부는 설정 파일의 `cluster.host_management`(`auto`, `enabled`, `disabled`)로 바꿀 수 있습니다.
### RKE2 노드 관리

RKE2 서버 노드에서 root로 실행하면 Host 탭이 K3S와 같은 하위 탭으로 구성됩니다. 설정 예제는 [conf/examples/k3stui-rke2.yaml](conf/examples/k3stui-rke2.yaml)에 있습니다.

| 항목 | RKE2에서의 동작 |
|---|---|
| 서비스 | `rke2-server` 또는 `rke2-agent` (`rke2.service_name: auto`는 실행 중인 유닛을 고릅니다) |
| 실행 파일 | `/usr/local/bin/rke2` → `/usr/bin/rke2` → `/opt/rke2/bin/rke2` 순으로 찾습니다 (tarball, RPM 설치 모두 지원) |
| 설정 | `/etc/rancher/rke2/config.yaml`(+ `config.yaml.d`) 보기·편집, 저장 후 서비스 재시작 질의 |
| Manifests | `<data-dir>/server/manifests` 보기, `.skip` 전환 |
| 인증서 | `server/tls`, `server/tls/etcd`, `agent` 인증서. `R`은 서비스 중지 → `rke2 certificate rotate` → 시작 |
| 백업 | `rke2 etcd-snapshot save`로 etcd 스냅샷을 만들고 RKE2 스냅샷 목록에도 등록됩니다. `R`은 `rke2 server --cluster-reset` 복원 절차를 보여줍니다 |
| 노드 추가 명령 | get.rke2.io agent 설치, `config.yaml`에 `server: https://<서버>:9345`와 토큰 작성, `rke2-agent` 시작까지 한 번에 보여줍니다 |
| kubectl, crictl | `<data-dir>/bin/kubectl`, `<data-dir>/bin/crictl --runtime-endpoint unix:///run/k3s/containerd/containerd.sock` |

RKE2에는 `check-config` 명령과 SQLite 데이터스토어가 없어서 Service의 `c` 키와 자동 복원은 나오지 않습니다.

### kubeadm 노드 관리

kubeadm 컨트롤 플레인 노드에서 root로 실행하면 Host 탭이 다음처럼 구성됩니다. 설정 예제는 [conf/examples/k3stui-kubeadm.yaml](conf/examples/k3stui-kubeadm.yaml)에 있습니다.

| 하위 탭 | 내용 | 작업 키 |
|---|---|---|
| Service | kubelet 서비스 상태, kubeadm·kubelet 버전, API 서버 상태, etcd 데이터 경로, 디스크 사용량 | `l` kubelet 로그, `r` 재시작, `t` 중지, `a` 시작, `c` `kubeadm certs check-expiration`, `J` 노드 추가 명령 |
| kubelet config | `/var/lib/kubelet/config.yaml` (KubeletConfiguration) | `v` 보기, `e` 편집 (kind 검증 → diff → 백업 → kubelet 재시작 질의) |
| Static Pods | `/etc/kubernetes/manifests`의 kube-apiserver, etcd 등 | `v` 보기, `e` 편집 (Pod 검증 → diff → 백업 후 저장, kubelet이 Pod를 다시 만듭니다) |
| Certificates | `pki/`, `pki/etcd/`, admin.conf 등 kubeconfig 안의 클라이언트 인증서, kubelet 인증서 | `v` 상세, `R` `kubeadm certs renew all` 후 컨트롤 플레인 재시작 |
| Backups | etcd 스냅샷과 PKI 압축본 | `b` 지금 백업, `R` 복원 절차 안내, `x` 삭제 |
| Containers, Images | `crictl` (런타임 소켓은 kubelet 설정에서 자동으로 찾습니다) | K3S와 같습니다 |

동작 방식과 주의할 점:

- **etcd 스냅샷**: 호스트에 `etcdctl`이 있으면 그것을, 없으면 실행 중인 etcd 컨테이너 안의 `etcdctl`을 씁니다. 스냅샷과 함께 `/etc/kubernetes/pki`를 `<백업>.pki.tar.gz`로 저장합니다.
- **etcd 복원**: static Pod를 내리고 데이터 디렉터리를 바꾸는 위험한 절차라 자동으로 하지 않습니다. `R`을 누르면 해당 스냅샷 경로가 들어간 단계별 명령을 보여줍니다.
- **static Pod 편집**: 저장 전 원본을 `<backup.dir>/manifests/`에 백업합니다. 백업 위치가 manifests 디렉터리 안이면 kubelet이 백업까지 Pod로 띄우므로 저장을 거부합니다.
- **인증서 갱신**: 갱신 후 kube-apiserver, kube-controller-manager, kube-scheduler, etcd 컨테이너를 멈추면 kubelet이 바로 다시 띄웁니다. 그동안 API 서버가 잠시 끊기며, `admin.conf`가 새로 만들어지므로 복사해 둔 kubeconfig는 다시 복사해야 합니다.
- **노드 추가 명령**: 볼 때마다 새 부트스트랩 토큰(기본 24시간 유효)을 만들므로 확인을 거치고 감사 로그에 남깁니다.
- **워커 노드**: API 서버가 원격이라 auto에서는 Host 탭이 꺼집니다. `cluster.host_management: enabled`로 켜면 kubelet·설정·컨테이너 관리만 쓸 수 있습니다.

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
| Host › Service | `l` 서비스 로그, `r` 재시작, `t` 중지, `a` 시작, `c` 점검(K3S: check-config, kubeadm: certs check-expiration, RKE2: 없음), `J` 노드 추가 명령 |
| Host › config.yaml / kubelet config | `v` 파일 보기, `e` 편집, `r` 서비스 재시작 |
| Host › Static Pods (kubeadm) | `v` 보기, `e` 편집 |
| Host › Manifests | `v` 보기, `s` `.skip` 전환 |
| Host › Certificates | `v` 상세, `R` 인증서 갱신 |
| Host › Backups | `b` 지금 백업, `R` 복원(K3S SQLite) 또는 복원 안내(K3S·RKE2·kubeadm etcd), `x` 삭제 |
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
| `k3stui.yaml` | 클러스터 접속(배포판·kubeconfig·context·호스트 관리), K3S 경로, 갱신 주기, 시작 화면, 안전 장치, 백업 위치·보관 개수, 외부 도구 경로, 로그 위치 |
| `keybindings.yaml` | 공통 키와 작업 키 재정의 (작업 ID는 `?` 도움말 화면에 표시됩니다) |
| `theme.yaml` | `dark` / `light` 색상 팔레트 |
| `views.d/*.yaml` | 리소스별 표시 컬럼 재정의. 항목·path 문법·리소스별 내장 컬럼은 [conf/views.d/README.md](conf/views.d/README.md)에 있습니다 |
| `examples/k3stui-k8s.yaml` | 일반 Kubernetes(원격) 클러스터용 설정 예제 |
| `examples/k3stui-kubeadm.yaml` | kubeadm 컨트롤 플레인 노드용 설정 예제 |
| `examples/k3stui-rke2.yaml` | RKE2 서버 노드용 설정 예제 |

자주 바꾸는 항목은 다음과 같습니다.

```yaml
cluster:
  distribution: auto             # auto | k3s | rke2 | kubeadm | kubernetes
  kubeconfig: ""                 # 비우면 자동 탐색합니다
  context: ""                    # 비우면 current-context를 씁니다
  host_management: auto          # auto | enabled | disabled
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
    host/       노드 관리 공통 인터페이스·타입 (systemd, 인증서, 파일 백업, 기능 목록)
    k3s/        K3S·RKE2 노드: 서비스, config.yaml, SQLite·etcd, 자동 배포 manifest, 인증서 (flavor로 구분)
    kubeadm/    kubeadm 노드: kubelet, kubelet 설정, static Pod, PKI, etcd 스냅샷, 노드 추가 토큰
    runtime/    컨테이너 런타임 (crictl)
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
| `읽을 수 있는 kubeconfig가 없습니다` | K3S 노드라면 `sudo`로 실행하고, 다른 클러스터라면 `--kubeconfig`로 파일을 지정합니다 |
| 엉뚱한 클러스터에 연결됩니다 | `--check`의 kubeconfig 항목에서 어떤 파일을 골랐는지(출처 포함) 확인하고, `--kubeconfig`·`--context`로 지정합니다 |
| Host 탭이 보이지 않습니다 | `--check`의 `호스트 관리` 줄에 이유가 나옵니다. 로컬 K3S가 아니면 숨기며, `cluster.host_management: enabled`로 강제할 수 있습니다 |
| CPU·메모리가 `-`로 표시됩니다 | metrics-server Pod 상태를 확인합니다 (`--check`의 metrics-server 항목) |
| Helm 탭에 오류가 표시됩니다 | `helm`이 PATH에 있는지, 또는 `tools.helm` 경로를 확인합니다 |
| 데이터스토어가 `unknown`으로 표시됩니다 | `k3s.data_dir`이 실제 data-dir과 같은지 확인합니다 |
| 시작 화면 아래에 `설정 경고`가 나옵니다 | `k3stui --check`의 `[WARN]` 줄에 파일과 이유가 나옵니다. views.d 문제는 [conf/views.d/README.md](conf/views.d/README.md)의 「검증과 문제 해결」을 참고하세요 |
| 화면이 깨집니다 | 256색 이상을 지원하는 터미널과 `TERM=xterm-256color`를 사용합니다 |
| 동작 기록을 보고 싶습니다 | `/var/log/k3stui/k3stui.log`(앱 로그), `/var/log/k3stui/audit.log`(변경 작업) |
