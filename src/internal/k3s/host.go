// Package k3s는 K3S 계열(K3S, RKE2) 노드용 host.Host 구현입니다:
// systemd 서비스, config.yaml, 데이터스토어, 자동 배포 manifest, 인증서.
// 두 배포판은 구조가 같고 경로·서비스 이름·명령만 다르므로 flavor로 구분합니다.
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

// flavor는 K3S와 RKE2의 차이입니다.
type flavor struct {
	distro      string // config.DistroK3s | config.DistroRKE2
	name        string // 표시 이름 (K3S, RKE2)
	cmd         string // 안내 문구에 쓰는 명령 이름 (k3s, rke2)
	binary      string
	service     string
	configFile  string
	dataDirCfg  string // auto 또는 경로
	defaultData string
	sqlite      bool // SQLite(kine) 데이터스토어 지원 (K3S만)
	checkConfig bool // check-config 명령 지원 (K3S만)
	docsURL     string
}

// System은 K3S 계열 서버 노드를 다루는 host.Host 구현입니다.
type System struct {
	cfg  *config.Config
	fl   flavor
	run  executil.Runner
	sd   *host.Systemd
	root bool
	now  func() time.Time
}

var _ host.Host = (*System)(nil)

func newSystem(cfg *config.Config, fl flavor, run executil.Runner) *System {
	root := os.Geteuid() == 0
	return &System{
		cfg: cfg, fl: fl, run: run, root: root, now: time.Now,
		sd: &host.Systemd{
			Unit: fl.service, Systemctl: cfg.Tools.Systemctl, Journalctl: cfg.Tools.Journalctl,
			Run: run, Ctl: executil.System{Timeout: 3 * time.Minute}, Root: root,
		},
	}
}

// NewSystem은 K3S Host 구현을 만듭니다.
func NewSystem(cfg *config.Config, run executil.Runner) *System {
	return newSystem(cfg, flavor{
		distro: config.DistroK3s, name: "K3S", cmd: "k3s",
		binary: cfg.K3s.Binary, service: cfg.K3s.ServiceName, configFile: cfg.K3s.ConfigFile,
		dataDirCfg: cfg.K3s.DataDir, defaultData: "/var/lib/rancher/k3s",
		sqlite: true, checkConfig: true, docsURL: "https://docs.k3s.io/datastore/backup-restore",
	}, run)
}

func (s *System) Distro() string      { return s.fl.distro }
func (s *System) IsRoot() bool        { return s.root }
func (s *System) ServiceName() string { return s.fl.service }
func (s *System) ConfigPath() string  { return s.fl.configFile }

// Caps는 K3S·RKE2에서 쓸 수 있는 기능입니다.
func (s *System) Caps() host.Capabilities {
	n, c := s.fl.name, s.fl.cmd
	caps := host.Capabilities{
		ServiceHint:          "단일 노드에서는 API 서버가 잠시 끊겨 화면 갱신이 멈춥니다.\n(실행 중인 Pod는 containerd에서 계속 동작합니다)",
		ConfigLabel:          "config.yaml",
		ConfigHint:           "변경 후 " + s.fl.service + " 재시작이 필요합니다",
		ConfigRestartService: true,
		ManifestsLabel:       "Manifests",
		ManifestsHint:        n + "가 시작 시·변경 시 자동 적용하는 파일",
		ManifestSkip:         true,
		CertHint:             "만료 30일 이내는 노란색, 만료는 빨간색. " + n + "는 만료 90일 전부터 재시작 시 자동 갱신합니다.",
		CertRotateHint: s.fl.service + "를 중지하고 '" + c + " certificate rotate'로 서버/클라이언트 인증서를 새로 발급한 뒤 다시 시작합니다.\n" +
			"CA 인증서는 바뀌지 않습니다. 외부에서 쓰는 kubeconfig의 클라이언트 인증서는 다시 복사해야 합니다.",
		Backup:    true,
		Restore:   s.Datastore().Kind == host.DatastoreSQLite,
		JoinLabel: "노드 추가 명령",
		JoinHint:  "새 노드에서 아래 명령을 실행하면 agent로 합류합니다.",
	}
	if s.fl.sqlite {
		caps.BackupExtra = "TOKEN"
	}
	if s.fl.checkConfig {
		caps.CheckLabel = "check-config"
	}
	return caps
}

func (s *System) needRoot() error {
	if !s.root {
		return host.ErrNotRoot
	}
	return nil
}

// DataDir는 데이터 디렉터리입니다. conf의 data_dir이 auto면 config.yaml의 data-dir을 씁니다.
func (s *System) DataDir() string {
	if d := s.fl.dataDirCfg; d != "" && d != "auto" {
		return d
	}
	if kc, err := s.LoadConfig(); err == nil {
		if d := kc.String("data-dir"); d != "" {
			return d
		}
	}
	if s.root || s.fl.distro != config.DistroK3s {
		return s.fl.defaultData
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

// rke2Socket은 RKE2 내장 containerd 소켓입니다 (K3S와 같은 경로를 씁니다).
const rke2Socket = "unix:///run/k3s/containerd/containerd.sock"

// CrictlCommand는 crictl 명령입니다.
// K3S는 k3s crictl이 소켓을 지정하고, RKE2는 <data-dir>/bin/crictl에 소켓을 직접 넘깁니다.
func (s *System) CrictlCommand() []string {
	if s.fl.distro == config.DistroK3s {
		return []string{s.fl.binary, "crictl"}
	}
	bin := filepath.Join(s.DataDir(), "bin", "crictl")
	if _, err := os.Stat(bin); err != nil {
		bin = "crictl"
	}
	return []string{bin, "--runtime-endpoint", rke2Socket}
}

// KubectlCommand는 노드에 들어 있는 kubectl입니다 (K3S: k3s kubectl, RKE2: <data-dir>/bin/kubectl).
// RKE2의 kubectl은 PATH에 없는 경우가 많아 직접 경로를 씁니다. 찾지 못하면 nil입니다.
func (s *System) KubectlCommand() []string {
	if s.fl.distro == config.DistroK3s {
		return []string{s.fl.binary, "kubectl"}
	}
	bin := filepath.Join(s.DataDir(), "bin", "kubectl")
	if _, err := os.Stat(bin); err == nil {
		return []string{bin}
	}
	return nil
}

// cli는 k3s 또는 rke2 명령을 실행합니다.
func (s *System) cli(ctx context.Context, args ...string) ([]byte, error) {
	return s.run.Run(ctx, s.fl.binary, args...)
}
