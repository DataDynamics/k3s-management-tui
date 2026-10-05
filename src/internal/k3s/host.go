// Package k3s는 K3S 호스트 측 관리 기능입니다: systemd 서비스, 설정 파일, 데이터스토어, 자동 배포 manifest, 인증서.
package k3s

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
)

// ErrNotRoot는 root 권한이 필요한 작업을 일반 사용자로 시도했을 때의 오류입니다.
var ErrNotRoot = errors.New("root 권한이 필요합니다 (sudo로 실행하세요)")

// ServiceOp는 서비스 제어 동작입니다.
type ServiceOp string

const (
	OpStart   ServiceOp = "start"
	OpStop    ServiceOp = "stop"
	OpRestart ServiceOp = "restart"
)

// Host는 K3S 호스트 관리 인터페이스입니다 (설계 7.3). 테스트에서는 가짜 구현을 주입합니다.
type Host interface {
	IsRoot() bool
	ServiceName() string

	ServiceStatus(ctx context.Context) (ServiceStatus, error)
	ServiceControl(ctx context.Context, op ServiceOp) error
	StreamJournal(ctx context.Context, lines int, follow bool) (<-chan string, error)
	Version(ctx context.Context) (string, error)
	CheckConfig(ctx context.Context) (string, error)

	ConfigPath() string
	LoadConfig() (*K3sConfig, error)
	WriteConfig(content []byte) (backupPath string, err error)

	DataDir() string
	KubeletDir() string
	Datastore() DatastoreInfo
	Backup(ctx context.Context) (string, error)
	ListBackups() ([]BackupFile, error)
	DeleteBackup(ctx context.Context, name string) error
	RestoreBackup(ctx context.Context, name string, progress func(string)) error

	Manifests() ([]Manifest, error)
	SetManifestSkip(path string, skip bool) error

	Certificates() ([]CertInfo, error)
	RotateCertificates(ctx context.Context, progress func(string)) error

	Token() (string, error)
	DiskUsage() []DiskInfo
	ReadFile(path string) (string, error)
}

// System은 실제 서버를 다루는 Host 구현입니다.
type System struct {
	cfg  *config.Config
	run  executil.Runner
	ctl  executil.Runner // systemctl start/stop/restart (K3S 기동 대기로 타임아웃이 깁니다)
	root bool
	now  func() time.Time
}

// NewSystem은 Host 구현을 만듭니다.
func NewSystem(cfg *config.Config, run executil.Runner) *System {
	return &System{cfg: cfg, run: run, ctl: executil.System{Timeout: 3 * time.Minute}, root: os.Geteuid() == 0, now: time.Now}
}

func (s *System) IsRoot() bool        { return s.root }
func (s *System) ServiceName() string { return s.cfg.K3s.ServiceName }
func (s *System) ConfigPath() string  { return s.cfg.K3s.ConfigFile }

func (s *System) needRoot() error {
	if !s.root {
		return ErrNotRoot
	}
	return nil
}

// DataDir는 K3S 데이터 디렉터리입니다. conf의 data_dir이 auto면 config.yaml의 data-dir을 씁니다.
func (s *System) DataDir() string {
	if d := s.cfg.K3s.DataDir; d != "" && d != "auto" {
		return d
	}
	if kc, err := s.LoadConfig(); err == nil {
		if d := kc.String("data-dir"); d != "" {
			return d
		}
	}
	if s.root {
		return "/var/lib/rancher/k3s"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".rancher", "k3s")
	}
	return "/var/lib/rancher/k3s"
}

// KubeletDir는 kubelet root-dir입니다 (kubelet-arg의 root-dir=, 없으면 /var/lib/kubelet).
func (s *System) KubeletDir() string {
	if kc, err := s.LoadConfig(); err == nil {
		for _, a := range kc.Strings("kubelet-arg") {
			if v, ok := cutFlag(a, "root-dir"); ok {
				return v
			}
		}
	}
	return "/var/lib/kubelet"
}

func (s *System) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", ErrNotRoot
		}
		return "", err
	}
	return string(data), nil
}

func (s *System) k3s(ctx context.Context, args ...string) ([]byte, error) {
	return s.run.Run(ctx, s.cfg.K3s.Binary, args...)
}
