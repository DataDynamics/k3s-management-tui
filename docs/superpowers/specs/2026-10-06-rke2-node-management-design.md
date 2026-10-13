# RKE2 노드 관리와 배포판 정리

> 출처: 이관 전 `docs/DESIGN.md` 15장 (원문 그대로, 2026-10-13 이관)

## 15. RKE2 노드 관리와 배포판 정리 (2026-10-06)

### 15.1 배포판 값 정리

`cluster.distribution`은 `auto | k3s | rke2 | kubeadm | kubernetes`입니다.
구현 없이 이름만 판별하던 `eks`, `gke`는 없앴습니다. 관리형 클러스터는 `kubernetes`(리소스 관리 모드)로 동작하며,
설정에 `eks`·`gke`를 적으면 `kubernetes` 또는 `auto`를 쓰라는 오류를 냅니다.

| 값 | 노드 관리 | 판별 |
|---|---|---|
| `k3s` | 지원 | 버전 `+k3s` 또는 로컬 k3s 바이너리 |
| `rke2` | 지원 | 버전 `+rke2` 또는 로컬 rke2 바이너리 |
| `kubeadm` | 지원 | `kube-system/kubeadm-config` 또는 로컬 `/etc/kubernetes/manifests` |
| `kubernetes` | 없음 (리소스 관리만) | 그 밖의 모든 클러스터 |

### 15.2 RKE2 구현

RKE2는 K3S와 구조가 같으므로 `internal/k3s`에 flavor(배포판별 차이)를 두고 같은 구현을 씁니다.

| 차이 | K3S | RKE2 |
|---|---|---|
| 실행 파일 | `k3s.binary` | `/usr/local/bin/rke2` → `/usr/bin/rke2` → `/opt/rke2/bin/rke2` (auto) |
| 서비스 | `k3s` | `rke2-server` / `rke2-agent` (auto: 실행 중인 유닛 → 설치된 유닛) |
| 설정 | `/etc/rancher/k3s/config.yaml` | `/etc/rancher/rke2/config.yaml` |
| data-dir 기본값 | `/var/lib/rancher/k3s` | `/var/lib/rancher/rke2` |
| kubectl | `k3s kubectl` | `<data-dir>/bin/kubectl` (PATH에 없는 경우가 많음) |
| crictl | `k3s crictl` | `<data-dir>/bin/crictl --runtime-endpoint unix:///run/k3s/containerd/containerd.sock` |
| 데이터스토어 | SQLite 또는 etcd | etcd |
| check-config | 있음 | 없음 |
| 노드 추가 | `curl get.k3s.io \| K3S_URL=…:6443 K3S_TOKEN=…` | get.rke2.io agent 설치 + `config.yaml`(`server: …:9345`, `token`) + `rke2-agent` 시작 |

- Host 인터페이스에 `KubectlCommand()`를 추가했습니다. 로컬 노드면 Host 탭을 끈 경우에도 노드의 kubectl을 씁니다.
- 노드 추가 명령의 서버 IP는 control-plane 라벨 노드 → 이 호스트와 이름이 같은 노드 → 첫 노드 순으로 고릅니다 (RKE2는 Ready 전에는 라벨이 없음).

### 15.3 검증

- 단위 테스트: RKE2 서비스 자동 선택, kubectl·crictl 경로, check-config 미지원, etcd 판별(state.db가 있어도 SQLite로 보지 않음), 스냅샷 명령, 복원 안내, 노드 추가 절차, 인증서 갱신 순서, `eks`·`gke` 설정 거부.
- 실제 RKE2 (v1.36.5+rke2r1, 일회용 systemd 컨테이너에 공식 설치 스크립트로 설치한 뒤 삭제):
  배포판·서비스·kubeconfig·kubectl·crictl·etcd 판별, 모든 Host 하위 탭 데이터, etcd 스냅샷(`rke2 etcd-snapshot ls`에 등록 확인),
  manifest `.skip` 전환·해제, config.yaml 편집과 `rke2-server` 재시작, 인증서 갱신(serving-kube-apiserver 시리얼 변경), 노드 추가 절차, 복원 안내,
  `<data-dir>/bin/kubectl`로 describe를 확인했습니다.
- 검증 중 고친 문제: skip 확인 문구가 RKE2에서도 "K3S"로 나오던 문제, Ready 전 RKE2 노드에서 노드 추가 명령의 서버 IP가 비던 문제.
