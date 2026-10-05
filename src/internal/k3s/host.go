// Package k3s는 K3S 노드용 host.Host 구현입니다: systemd 서비스, config.yaml, 데이터스토어, 자동 배포 manifest, 인증서.
package k3s

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// System은 K3S 서버 노드를 다루는 host.Host 구현입니다.
type System struct {
	cfg  *config.Config
	run  executil.Runner
	sd   *host.Systemd
	root bool
	now  func() time.Time
}

var _ host.Host = (*System)(nil)

// NewSystem은 K3S Host 구현을 만듭니다.
func NewSystem(cfg *config.Config, run executil.Runner) *System {
	root := os.Geteuid() == 0
	return &System{
		cfg: cfg, run: run, root: root, now: time.Now,
		sd: &host.Systemd{
			Unit: cfg.K3s.ServiceName, Systemctl: cfg.Tools.Systemctl, Journalctl: cfg.Tools.Journalctl,
			Run: run, Ctl: executil.System{Timeout: 3 * time.Minute}, Root: root,
		},
	}
}

func (s *System) Distro() string      { return config.DistroK3s }
func (s *System) IsRoot() bool        { return s.root }
func (s *System) ServiceName() string { return s.cfg.K3s.ServiceName }
func (s *System) ConfigPath() string  { return s.cfg.K3s.ConfigFile }

// Caps는 K3S에서 쓸 수 있는 기능입니다.
func (s *System) Caps() host.Capabilities {
	return host.Capabilities{
		ServiceHint:          "단일 노드에서는 API 서버가 잠시 끊겨 화면 갱신이 멈춥니다.\n(실행 중인 Pod는 containerd에서 계속 동작합니다)",
		CheckLabel:           "check-config",
		ConfigLabel:          "config.yaml",
		ConfigHint:           "변경 후 k3s 재시작이 필요합니다",
		ConfigRestartService: true,
		ManifestsLabel:       "Manifests",
		ManifestsHint:        "K3S가 시작 시·변경 시 자동 적용하는 파일",
		ManifestSkip:         true,
		CertHint:             "만료 30일 이내는 노란색, 만료는 빨간색. K3S는 만료 90일 전부터 재시작 시 자동 갱신합니다.",
		CertRotateHint: "k3s를 중지하고 'k3s certificate rotate'로 서버/클라이언트 인증서를 새로 발급한 뒤 다시 시작합니다.\n" +
			"CA 인증서는 바뀌지 않습니다. 외부에서 쓰는 kubeconfig의 클라이언트 인증서는 다시 복사해야 합니다.",
		Backup:        true,
		Restore:       s.Datastore().Kind == host.DatastoreSQLite,
		BackupExtra:   "TOKEN",
		JoinLabel:     "노드 추가 명령",
		JoinHint:      "새 노드에서 아래 명령을 실행하면 agent로 합류합니다.",
		ContainerHint: "",
	}
}

func (s *System) needRoot() error {
	if !s.root {
		return host.ErrNotRoot
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
			return "", host.ErrNotRoot
		}
		return "", err
	}
	return string(data), nil
}

// CrictlCommand는 K3S 내장 crictl입니다 (containerd 소켓 경로를 K3S가 지정합니다).
func (s *System) CrictlCommand() []string { return []string{s.cfg.K3s.Binary, "crictl"} }

func (s *System) k3s(ctx context.Context, args ...string) ([]byte, error) {
	return s.run.Run(ctx, s.cfg.K3s.Binary, args...)
}
