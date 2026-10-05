// Package host는 노드(호스트) 관리 기능의 공통 인터페이스와 타입입니다.
// 배포판별 구현은 internal/k3s(K3S, RKE2), internal/kubeadm에 있습니다.
package host

import (
	"context"
	"errors"
	"time"
)

// ErrNotRoot는 root 권한이 필요한 작업을 일반 사용자로 시도했을 때의 오류입니다.
var ErrNotRoot = errors.New("root 권한이 필요합니다 (sudo로 실행하세요)")

// ErrUnsupported는 배포판이 지원하지 않는 작업입니다.
var ErrUnsupported = errors.New("이 배포판에서는 지원하지 않는 작업입니다")

// ServiceOp는 서비스 제어 동작입니다.
type ServiceOp string

const (
	OpStart   ServiceOp = "start"
	OpStop    ServiceOp = "stop"
	OpRestart ServiceOp = "restart"
)

// Capabilities는 배포판별로 할 수 있는 일과 화면 문구입니다. 화면은 이 값을 보고 하위 탭과 작업을 구성합니다.
type Capabilities struct {
	ServiceHint string // 서비스 중지·재시작 확인 창에 덧붙일 설명
	CheckLabel  string // 점검 명령 이름 ("" = 없음)

	ConfigLabel          string // 설정 하위 탭 이름
	ConfigHint           string // 설정 하위 탭 요약 줄
	ConfigRestartService bool   // 설정 저장 후 서비스 재시작을 물을지

	ManifestsLabel string // 매니페스트 하위 탭 이름 ("" = 없음)
	ManifestsHint  string
	ManifestSkip   bool // .skip 전환 (K3S)
	ManifestEdit   bool // 편집 (kubeadm static Pod)

	CertRotateHint string // 인증서 갱신 확인 창 설명 ("" = 갱신 불가)
	CertHint       string

	Backup        bool   // 데이터스토어 백업 가능
	Restore       bool   // 자동 복원 가능 (false면 RestoreGuide를 보여줍니다)
	BackupExtra   string // 백업과 함께 저장하는 부가 파일 이름 (TOKEN, PKI)
	BackupHint    string
	JoinLabel     string // 노드 추가 명령 작업 이름 ("" = 없음)
	JoinMutates   bool   // 노드 추가 명령을 만들 때 클러스터에 토큰을 새로 만드는지 (kubeadm)
	JoinHint      string
	ContainerHint string
}

// Host는 노드 관리 인터페이스입니다 (설계 7.3, 13장). 테스트에서는 가짜 구현을 주입합니다.
type Host interface {
	Distro() string // config.DistroK3s, config.DistroRKE2, config.DistroKubeadm
	Caps() Capabilities
	IsRoot() bool
	ServiceName() string

	ServiceStatus(ctx context.Context) (ServiceStatus, error)
	ServiceControl(ctx context.Context, op ServiceOp) error
	StreamJournal(ctx context.Context, lines int, follow bool) (<-chan string, error)
	Version(ctx context.Context) (string, error)
	CheckConfig(ctx context.Context) (string, error)

	ConfigPath() string
	LoadConfig() (*Config, error)
	ValidateConfig(content []byte) error
	WriteConfig(content []byte) (backupPath string, err error)

	DataDir() string
	KubeletDir() string
	Datastore() DatastoreInfo
	Backup(ctx context.Context) (string, error)
	ListBackups() ([]BackupFile, error)
	DeleteBackup(ctx context.Context, name string) error
	RestoreBackup(ctx context.Context, name string, progress func(string)) error
	RestoreGuide(name string) string

	ManifestsDir() string
	Manifests() ([]Manifest, error)
	SetManifestSkip(path string, skip bool) error
	WriteManifest(path string, content []byte) (backupPath string, err error)

	Certificates() ([]CertInfo, error)
	RotateCertificates(ctx context.Context, progress func(string)) error

	JoinCommand(ctx context.Context, serverIP string) (string, error)
	DiskUsage() []DiskInfo
	ReadFile(path string) (string, error)
	// CrictlCommand는 crictl 실행 명령 앞부분입니다 (예: [k3s crictl], [crictl --runtime-endpoint ...]).
	CrictlCommand() []string
	// KubectlCommand는 노드에 들어 있는 kubectl 명령입니다 (K3S: [k3s kubectl], RKE2: [<data-dir>/bin/kubectl]).
	// nil이면 PATH의 kubectl을 씁니다.
	KubectlCommand() []string
}

// DatastoreKind는 클러스터 데이터스토어 종류입니다.
type DatastoreKind string

const (
	DatastoreSQLite   DatastoreKind = "sqlite"
	DatastoreEtcd     DatastoreKind = "etcd"
	DatastoreExternal DatastoreKind = "external"
	DatastoreUnknown  DatastoreKind = "unknown"
)

// DatastoreInfo는 데이터스토어 상태입니다.
type DatastoreInfo struct {
	Kind     DatastoreKind
	Path     string // SQLite 파일 또는 etcd 데이터 디렉터리
	Endpoint string // external일 때 (자격 증명은 가림)
	Size     int64
}

// BackupFile은 백업 파일 하나입니다.
type BackupFile struct {
	Name     string
	Path     string
	Size     int64
	Time     time.Time
	Kind     DatastoreKind
	HasExtra bool // 부가 파일(K3S: 서버 토큰, kubeadm: PKI 압축본)이 함께 있는지
}

// Manifest는 자동 배포 manifest(K3S) 또는 static Pod manifest(kubeadm) 파일 하나입니다.
type Manifest struct {
	Path    string
	Rel     string
	Size    int64
	ModTime time.Time
	Skipped bool // K3S: <파일>.skip이 있으면 배포하지 않습니다
}
