# kubeadm 노드 관리 (2단계)

> 출처: 이관 전 `docs/DESIGN.md` 14장 (원문 그대로, 2026-10-13 이관)

## 14. kubeadm 노드 관리 (2단계, 2026-10-05)

### 14.1 구조 변경

노드 관리 인터페이스를 배포판 공통 패키지로 분리하고, 배포판별 구현을 나란히 둡니다.

```
internal/host      Host 인터페이스, Capabilities, 공통 타입(ServiceStatus, CertInfo, Manifest, BackupFile, DiskInfo, Config)
                   공통 헬퍼(Systemd, ScanCerts·DescribeCertFile, WriteFileWithBackup, ListManifests, ListBackupFiles, FillDiskUsage)
internal/k3s       K3S 구현 (기존 동작 유지)
internal/kubeadm   kubeadm 구현 (새로 작성)
```

- **Capabilities**: 배포판마다 할 수 있는 일과 화면 문구가 다릅니다 (K3S `.skip`, kubeadm static Pod 편집, 자동 복원 여부, 노드 추가 명령이 토큰을 새로 만드는지 등).
  Host 탭(`views.HostSources(h)`)은 이 값을 보고 하위 탭과 작업을 구성합니다. 워커 노드는 인증서 갱신·백업·노드 추가를 끕니다.
- **구현 선택**: `decideHost`가 배포판 판별 결과로 `k3s.NewSystem` 또는 `kubeadm.NewSystem`을 고릅니다.
  API 서버가 응답하지 않아도 로컬에 `/etc/kubernetes/manifests/kube-apiserver.yaml` 또는 kubelet 설정이 있으면 kubeadm으로 봅니다.
- **편집 흐름 일반화**: 설정 파일과 static Pod 편집이 같은 `editFile`(편집기 → 검증 → diff 확인 → 백업 후 저장 → 후속 안내)을 씁니다.
- **crictl**: 배포판별 명령(`k3s crictl`, `crictl --runtime-endpoint <kubelet 설정의 소켓>`)을 `Host.CrictlCommand()`로 받습니다.

### 14.2 kubeadm 기능

| 기능 | 구현 |
|---|---|
| 서비스 | systemd `kubelet` (상태, journal, start/stop/restart) |
| 버전 | `kubeadm version -o short`, `kubelet --version` |
| 점검 | `kubeadm certs check-expiration` (컨트롤 플레인) |
| 설정 | `/var/lib/kubelet/config.yaml` — YAML·kind 검증, `<backup.dir>/config/`에 백업 |
| static Pod | `/etc/kubernetes/manifests` — kind: Pod·이름·컨테이너 검증, `<backup.dir>/manifests/`에 백업, 임시 파일은 숨김 파일로 써서 kubelet이 읽지 않게 함 |
| 인증서 | `pki/*.crt`, `pki/etcd/*.crt`, admin.conf·super-admin.conf·controller-manager.conf·scheduler.conf의 client-certificate-data, kubelet 인증서 |
| 인증서 갱신 | `kubeadm certs renew all` → 컨트롤 플레인 컨테이너 `crictl stop` → kube-apiserver 기동 대기(최대 3분) |
| etcd 판별 | `manifests/etcd.yaml`의 `--data-dir`, 없으면 kube-apiserver `--etcd-servers`로 외부 etcd |
| etcd 백업 | 호스트 etcdctl 또는 `crictl exec <etcd> etcdctl snapshot save <data-dir>/.k3stui-snapshot.db` → 백업 위치로 이동, PKI tar.gz 저장, 보관 개수 정리 |
| etcd 복원 | 자동화하지 않음 (`RestoreGuide`: etcdutl 기반 단계별 명령) |
| 노드 추가 | `kubeadm token create --print-join-command` (토큰을 만들므로 확인·감사 기록) |

### 14.3 검증

- **단위 테스트** (`internal/kubeadm`): 컨트롤 플레인·워커 기능 구분, etcd 경로·외부 etcd 판별, kubelet root-dir·런타임 소켓 판별,
  kubelet 설정 kind 검증, static Pod 검증과 백업 위치 보호, 컨테이너 etcdctl 스냅샷·PKI 압축·보관 정리, 인증서 갱신 순서, 노드 추가 명령, kubeconfig 안 인증서.
- **화면 테스트** (`internal/app`): kubeadm Host 탭 구성, 노드 추가 명령의 확인·감사, 백업 `R`의 복원 안내, 행과 무관한 작업의 감사 대상.
- **실제 kubeadm 클러스터** (kind v0.33, Kubernetes v1.37.0, 일회용 클러스터를 만들어 노드 안에서 실행한 뒤 삭제):
  배포판·kubelet·etcd·런타임 소켓 판별, 모든 Host 하위 탭 데이터, etcd 스냅샷(etcdutl로 무결성 확인, 키 373개) + PKI 압축본,
  kube-scheduler static Pod 편집(`--v=3` 반영, 백업 위치 확인), 인증서 갱신(apiserver 시리얼 변경, 컨트롤 플레인 4개 재시작, readyz ok),
  노드 추가 명령(토큰 생성 확인), kubelet 설정 편집과 재시작, 복원 안내, 감사 로그를 확인했습니다.
- 검증 중 발견해 고친 문제: 비동기 로딩 중 하위 탭을 바꾸면 "불러오는 중"에 멈추던 버그, 행과 무관한 작업이 선택된 행을 감사 대상으로 기록하던 버그,
  `--check`가 없는 편집기를 OK로 표시하던 문제.

### 14.4 남은 범위

- RKE2 노드 관리 → 15장에서 구현했습니다.
- 여러 컨트롤 플레인 노드의 etcd 일괄 백업, 원격 노드 관리 (SSH·에이전트 필요, 비목표).

