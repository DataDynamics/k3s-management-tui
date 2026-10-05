// Package kubeadm은 kubeadm으로 만든 노드용 host.Host 구현입니다.
// kubelet 서비스, kubelet 설정, static Pod manifest, PKI 인증서, 로컬 etcd 스냅샷, 노드 추가 토큰을 다룹니다.
package kubeadm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// System은 kubeadm 노드를 다루는 host.Host 구현입니다.
type System struct {
	cfg  *config.Config
	kc   config.KubeadmConfig
	run  executil.Runner
	sd   *host.Systemd
	root bool
	now  func() time.Time
}

var _ host.Host = (*System)(nil)

// NewSystem은 kubeadm Host 구현을 만듭니다.
func NewSystem(cfg *config.Config, run executil.Runner) *System {
	root := os.Geteuid() == 0
	return &System{
		cfg: cfg, kc: cfg.Kubeadm, run: run, root: root, now: time.Now,
		sd: &host.Systemd{
			Unit: cfg.Kubeadm.ServiceName, Systemctl: cfg.Tools.Systemctl, Journalctl: cfg.Tools.Journalctl,
			Run: run, Ctl: executil.System{Timeout: 3 * time.Minute}, Root: root,
		},
	}
}

func (s *System) Distro() string      { return config.DistroKubeadm }
func (s *System) IsRoot() bool        { return s.root }
func (s *System) ServiceName() string { return s.kc.ServiceName }
func (s *System) ConfigPath() string  { return s.kc.KubeletConfig }
func (s *System) DataDir() string     { return s.kc.KubernetesDir }
func (s *System) ManifestsDir() string {
	return filepath.Join(s.kc.KubernetesDir, "manifests")
}

func (s *System) pki(name string) string { return filepath.Join(s.kc.KubernetesDir, "pki", name) }

// ControlPlane은 이 노드가 컨트롤 플레인인지(kube-apiserver static Pod가 있는지) 알려줍니다.
func (s *System) ControlPlane() bool {
	_, err := os.Stat(filepath.Join(s.ManifestsDir(), "kube-apiserver.yaml"))
	return err == nil
}

// Caps는 kubeadm에서 쓸 수 있는 기능입니다. 워커 노드에서는 인증서 갱신·etcd 백업·노드 추가 명령을 끕니다.
func (s *System) Caps() host.Capabilities {
	cp := s.ControlPlane()
	ds := s.Datastore()
	c := host.Capabilities{
		ServiceHint: "kubelet이 멈춘 동안 이 노드의 Pod 상태 보고와 static Pod(컨트롤 플레인 포함) 관리가 멈춥니다.\n" +
			"이미 실행 중인 컨테이너는 계속 동작합니다. 중지한 뒤에는 반드시 다시 시작하세요.",
		CheckLabel:           "",
		ConfigLabel:          "kubelet config",
		ConfigHint:           "KubeletConfiguration — 변경 후 kubelet 재시작이 필요합니다",
		ConfigRestartService: true,
		ManifestsLabel:       "Static Pods",
		ManifestsHint:        "kubelet이 직접 실행하는 Pod 정의 — 저장하면 kubelet이 Pod를 자동으로 다시 만듭니다",
		ManifestEdit:         true,
		CertHint:             "kubeadm 인증서는 기본 1년 유효하며 'kubeadm upgrade' 때 자동 갱신됩니다. 만료 30일 이내는 노란색, 만료는 빨간색입니다.",
		BackupExtra:          "PKI",
		BackupHint:           "etcd 스냅샷과 함께 " + s.pki("") + " 압축본(<백업>.pki.tar.gz)을 저장합니다",
		ContainerHint:        "",
	}
	if cp {
		c.CheckLabel = "certs check-expiration"
		c.CertRotateHint = "'kubeadm certs renew all'로 인증서를 갱신한 뒤 컨트롤 플레인 컨테이너(kube-apiserver, kube-controller-manager,\n" +
			"kube-scheduler, etcd)를 재시작합니다. kubelet이 바로 다시 띄우며, 그동안 API 서버가 잠시 끊깁니다.\n" +
			"CA는 바뀌지 않습니다. admin.conf가 새로 만들어지므로 복사해 둔 kubeconfig(~/.kube/config 등)는 다시 복사해야 합니다."
		c.JoinLabel = "노드 추가 명령"
		c.JoinMutates = true
		c.JoinHint = "새 부트스트랩 토큰(기본 24시간 유효)을 만들고 워커 노드 추가 명령을 출력합니다."
	}
	if ds.Kind == host.DatastoreEtcd {
		c.Backup = true
	}
	return c
}

func (s *System) needRoot() error {
	if !s.root {
		return host.ErrNotRoot
	}
	return nil
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

// kubeletFlags는 kubeadm-flags.env의 KUBELET_KUBEADM_ARGS를 --이름=값 맵으로 읽습니다.
func (s *System) kubeletFlags() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(s.kc.KubeletFlags)
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		_, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(strings.Trim(v, `"'`)) {
			k, val, ok := strings.Cut(strings.TrimLeft(f, "-"), "=")
			if ok {
				out[k] = val
			}
		}
	}
	return out
}

// KubeletDir는 kubelet root-dir입니다 (--root-dir 플래그, 없으면 /var/lib/kubelet).
func (s *System) KubeletDir() string {
	if v := s.kubeletFlags()["root-dir"]; v != "" {
		return v
	}
	return "/var/lib/kubelet"
}

// CrictlCommand는 crictl 명령입니다. kubelet에 지정된 런타임 소켓이 있으면 함께 넘깁니다.
func (s *System) CrictlCommand() []string {
	cmd := []string{s.kc.Crictl}
	ep := s.kubeletFlags()["container-runtime-endpoint"]
	if ep == "" {
		if c, err := host.LoadYAMLConfig(s.kc.KubeletConfig); err == nil {
			ep = c.String("containerRuntimeEndpoint")
		}
	}
	if ep != "" {
		cmd = append(cmd, "--runtime-endpoint", ep)
	}
	return cmd
}

func (s *System) crictl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := s.CrictlCommand()
	return s.run.Run(ctx, cmd[0], append(cmd[1:], args...)...)
}

// KubectlCommand는 nil입니다. kubeadm 노드는 PATH의 kubectl을 씁니다.
func (s *System) KubectlCommand() []string { return nil }

// ---- 서비스 ----

func (s *System) ServiceStatus(ctx context.Context) (host.ServiceStatus, error) {
	return s.sd.Status(ctx)
}
func (s *System) ServiceControl(ctx context.Context, op host.ServiceOp) error {
	return s.sd.Control(ctx, op)
}
func (s *System) StreamJournal(ctx context.Context, lines int, follow bool) (<-chan string, error) {
	return s.sd.Journal(ctx, lines, follow)
}

// Version은 kubeadm과 kubelet 버전입니다.
func (s *System) Version(ctx context.Context) (string, error) {
	var parts []string
	if out, err := s.run.Run(ctx, s.kc.Binary, "version", "-o", "short"); err == nil {
		parts = append(parts, "kubeadm "+strings.TrimSpace(string(out)))
	}
	if out, err := s.run.Run(ctx, s.kc.Kubelet, "--version"); err == nil {
		parts = append(parts, strings.TrimSpace(string(out))) // "Kubernetes v1.31.0"
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("kubeadm·kubelet 버전을 확인할 수 없습니다")
	}
	return strings.Join(parts, " · "), nil
}

// CheckConfig는 kubeadm certs check-expiration 출력입니다 (컨트롤 플레인 전용).
func (s *System) CheckConfig(ctx context.Context) (string, error) {
	out, err := s.run.Run(ctx, s.kc.Binary, "certs", "check-expiration")
	if len(out) > 0 {
		return string(out), nil
	}
	return "", err
}

// ---- kubelet 설정 ----

// LoadConfig는 kubelet 설정(KubeletConfiguration)을 읽습니다.
func (s *System) LoadConfig() (*host.Config, error) { return host.LoadYAMLConfig(s.kc.KubeletConfig) }

// ValidateConfig는 YAML이고 kind가 있다면 KubeletConfiguration인지 확인합니다.
func (s *System) ValidateConfig(content []byte) error {
	if err := host.ValidateYAML(content); err != nil {
		return err
	}
	var m struct {
		Kind string `json:"kind"`
	}
	_ = yaml.Unmarshal(content, &m)
	if m.Kind != "" && m.Kind != "KubeletConfiguration" {
		return fmt.Errorf("kind가 KubeletConfiguration이 아닙니다: %s", m.Kind)
	}
	return nil
}

// WriteConfig는 기존 kubelet 설정을 <backup.dir>/config/에 복사한 뒤 새 내용을 씁니다.
func (s *System) WriteConfig(content []byte) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	if err := s.ValidateConfig(content); err != nil {
		return "", err
	}
	return host.WriteFileWithBackup(s.kc.KubeletConfig, content, filepath.Join(s.cfg.Backup.Dir, "config"), 0o644, s.now())
}

// ---- static Pod manifest ----

// Manifests는 /etc/kubernetes/manifests의 static Pod 정의입니다.
func (s *System) Manifests() ([]host.Manifest, error) {
	return host.ListManifests(s.ManifestsDir(), false)
}

// SetManifestSkip은 kubeadm에서는 지원하지 않습니다 (kubelet은 .skip 규칙이 없습니다).
func (s *System) SetManifestSkip(string, bool) error { return host.ErrUnsupported }

// WriteManifest는 static Pod manifest를 백업 후 저장합니다. kubelet이 변경을 감지해 Pod를 다시 만듭니다.
// 백업은 manifests 디렉터리 밖(<backup.dir>/manifests)에 둡니다. 안에 두면 kubelet이 백업까지 Pod로 띄웁니다.
func (s *System) WriteManifest(path string, content []byte) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	if !host.InsideDir(s.ManifestsDir(), path) {
		return "", fmt.Errorf("manifest 디렉터리 밖의 경로입니다: %s", path)
	}
	if err := ValidatePodManifest(content); err != nil {
		return "", err
	}
	backupDir := filepath.Join(s.cfg.Backup.Dir, "manifests")
	if host.InsideDir(s.ManifestsDir(), backupDir) || filepath.Clean(backupDir) == filepath.Clean(s.ManifestsDir()) {
		return "", fmt.Errorf("백업 위치(%s)가 manifests 디렉터리 안에 있으면 안 됩니다", backupDir)
	}
	return host.WriteFileWithBackup(path, content, backupDir, 0o600, s.now())
}

// ValidatePodManifest는 static Pod manifest가 Pod 하나인지 확인합니다.
func ValidatePodManifest(content []byte) error {
	if err := host.ValidateYAML(content); err != nil {
		return err
	}
	var m struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Containers []any `json:"containers"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(content, &m); err != nil {
		return err
	}
	switch {
	case m.Kind != "Pod":
		return fmt.Errorf("static Pod manifest는 kind: Pod여야 합니다 (현재: %q)", m.Kind)
	case m.Metadata.Name == "":
		return fmt.Errorf("metadata.name이 없습니다")
	case len(m.Spec.Containers) == 0:
		return fmt.Errorf("spec.containers가 비어 있습니다")
	}
	return nil
}

// ---- 노드 추가 ----

// JoinCommand는 'kubeadm token create --print-join-command'로 새 토큰과 워커 추가 명령을 만듭니다.
func (s *System) JoinCommand(ctx context.Context, _ string) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	if !s.ControlPlane() {
		return "", fmt.Errorf("컨트롤 플레인 노드에서만 만들 수 있습니다")
	}
	out, err := s.run.Run(ctx, s.kc.Binary, "token", "create", "--print-join-command",
		"--kubeconfig", filepath.Join(s.kc.KubernetesDir, "admin.conf"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ---- 디스크 ----

// DiskUsage는 kubelet, etcd, containerd, 백업 디렉터리의 파일시스템 사용량입니다.
func (s *System) DiskUsage() []host.DiskInfo {
	items := []host.DiskInfo{{Label: "kubelet", Path: s.KubeletDir()}}
	if ds := s.Datastore(); ds.Kind == host.DatastoreEtcd {
		items = append(items, host.DiskInfo{Label: "etcd", Path: ds.Path})
	}
	items = append(items,
		host.DiskInfo{Label: "containerd", Path: "/var/lib/containerd"},
		host.DiskInfo{Label: "backup", Path: s.cfg.Backup.Dir})
	return host.FillDiskUsage(items)
}
