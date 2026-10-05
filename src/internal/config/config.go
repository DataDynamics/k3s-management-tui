// Package config는 conf/*.yaml을 읽어 내장 기본값과 병합합니다.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config는 conf/k3stui.yaml의 내용입니다.
type Config struct {
	Cluster     ClusterConfig     `yaml:"cluster"`
	K3s         K3sConfig         `yaml:"k3s"`
	UI          UIConfig          `yaml:"ui"`
	Safety      SafetyConfig      `yaml:"safety"`
	Backup      BackupConfig      `yaml:"backup"`
	PortForward PortForwardConfig `yaml:"portforward"`
	Tools       ToolsConfig       `yaml:"tools"`
	Logging     LoggingConfig     `yaml:"logging"`
	Audit       AuditConfig       `yaml:"audit"`

	// 아래는 파일에서 읽지 않고 로딩 과정에서 채웁니다.
	File    string        `yaml:"-"` // 실제로 읽은 설정 파일 경로 (없으면 "")
	ConfDir string        `yaml:"-"` // keybindings.yaml, theme.yaml, views.d를 찾을 디렉터리
	Keys    *Keybindings  `yaml:"-"`
	Theme   *Theme        `yaml:"-"`
	Views   ViewOverrides `yaml:"-"`
}

// 배포판 값입니다. auto는 API 서버 버전과 클러스터 정보로 판별합니다.
const (
	DistroAuto       = "auto"
	DistroK3s        = "k3s"
	DistroRKE2       = "rke2"
	DistroKubeadm    = "kubeadm"
	DistroEKS        = "eks"
	DistroGKE        = "gke"
	DistroKubernetes = "kubernetes"
)

// 호스트 관리(Host 탭) 사용 여부입니다.
const (
	HostAuto     = "auto"     // 로컬 K3S일 때만 켭니다
	HostEnabled  = "enabled"  // 항상 켭니다
	HostDisabled = "disabled" // 항상 끕니다
)

// ClusterConfig는 접속할 클러스터와 배포판 설정입니다.
type ClusterConfig struct {
	Distribution   string `yaml:"distribution"`    // auto | k3s | rke2 | kubeadm | eks | gke | kubernetes
	Kubeconfig     string `yaml:"kubeconfig"`      // 비우면 자동 탐색 (KubeconfigCandidates 참고)
	Context        string `yaml:"context"`         // 비우면 kubeconfig의 current-context
	HostManagement string `yaml:"host_management"` // auto | enabled | disabled

	// KubeconfigSource는 kubeconfig를 어디서 골랐는지 기록합니다 (화면·--check 표시용).
	KubeconfigSource string `yaml:"-"`
}

type K3sConfig struct {
	ConfigFile string `yaml:"config_file"`
	// Kubeconfig는 이전 버전 호환용입니다. cluster.kubeconfig가 비어 있으면 이 값을 씁니다.
	Kubeconfig  string `yaml:"kubeconfig"`
	ServiceName string `yaml:"service_name"`
	Binary      string `yaml:"binary"`
	DataDir     string `yaml:"data_dir"` // auto = config.yaml의 data-dir
}

type UIConfig struct {
	RefreshInterval  time.Duration `yaml:"refresh_interval"`
	DefaultNamespace string        `yaml:"default_namespace"`
	DefaultView      string        `yaml:"default_view"`
	Theme            string        `yaml:"theme"`
	LogTailLines     int           `yaml:"log_tail_lines"`
	MaxLogLines      int           `yaml:"max_log_lines"`
}

type SafetyConfig struct {
	ReadOnly            bool     `yaml:"read_only"`
	ConfirmDestructive  bool     `yaml:"confirm_destructive"`
	ProtectedNamespaces []string `yaml:"protected_namespaces"`
}

type BackupConfig struct {
	Dir  string `yaml:"dir"`
	Keep int    `yaml:"keep"`
}

type PortForwardConfig struct {
	Address string `yaml:"address"`
}

type ToolsConfig struct {
	Kubectl    string        `yaml:"kubectl"` // 비우면 "<k3s.binary> kubectl"
	Helm       string        `yaml:"helm"`
	Systemctl  string        `yaml:"systemctl"`
	Journalctl string        `yaml:"journalctl"`
	Editor     string        `yaml:"editor"` // 비우면 $EDITOR → $VISUAL → vi
	Timeout    time.Duration `yaml:"timeout"`
}

type LoggingConfig struct {
	File  string `yaml:"file"`
	Level string `yaml:"level"`
}

type AuditConfig struct {
	File string `yaml:"file"`
}

// Default는 conf 파일이 없을 때 쓰는 내장 기본값입니다.
func Default() *Config {
	return &Config{
		Cluster: ClusterConfig{Distribution: DistroAuto, HostManagement: HostAuto},
		K3s: K3sConfig{
			ConfigFile:  "/etc/rancher/k3s/config.yaml",
			ServiceName: "k3s",
			Binary:      "/usr/local/bin/k3s",
			DataDir:     "auto",
		},
		UI: UIConfig{
			RefreshInterval:  2 * time.Second,
			DefaultNamespace: "all",
			DefaultView:      "dashboard",
			Theme:            "dark",
			LogTailLines:     500,
			MaxLogLines:      20000,
		},
		Safety: SafetyConfig{
			ConfirmDestructive:  true,
			ProtectedNamespaces: []string{"kube-system", "kube-public", "kube-node-lease"},
		},
		Backup:      BackupConfig{Dir: "/var/backups/k3stui", Keep: 7},
		PortForward: PortForwardConfig{Address: "127.0.0.1"},
		Tools: ToolsConfig{
			Helm:       "helm",
			Systemctl:  "systemctl",
			Journalctl: "journalctl",
			Timeout:    30 * time.Second,
		},
		Logging: LoggingConfig{File: "/var/log/k3stui/k3stui.log", Level: "info"},
		Audit:   AuditConfig{File: "/var/log/k3stui/audit.log"},
	}
}

// Candidates는 설정 파일 탐색 순서를 돌려줍니다 (설계 3.2절).
// --config → $K3STUI_CONF → ~/.config/k3stui/k3stui.yaml → $K3STUI_HOME/conf → <실행 파일>/../conf → /etc/k3stui
func Candidates(flagPath string) []string {
	var out []string
	if flagPath != "" {
		return []string{flagPath}
	}
	if env := os.Getenv("K3STUI_CONF"); env != "" {
		out = append(out, env)
	}
	// 사용자별 설정: 설치본(conf/)을 고치지 않고 클러스터별 설정을 둘 때 씁니다.
	if dir, err := os.UserConfigDir(); err == nil {
		out = append(out, filepath.Join(dir, "k3stui", "k3stui.yaml"))
	}
	if home := os.Getenv("K3STUI_HOME"); home != "" {
		out = append(out, filepath.Join(home, "conf", "k3stui.yaml"))
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		// build/k3stui 또는 libexec/k3stui 에서 실행된 경우 ../conf
		out = append(out, filepath.Join(filepath.Dir(exe), "..", "conf", "k3stui.yaml"))
	}
	out = append(out, "/etc/k3stui/k3stui.yaml")
	return out
}

// Load는 탐색 순서대로 첫 번째로 존재하는 파일을 읽습니다. 파일이 하나도 없으면 기본값을 씁니다.
// flagPath가 지정되었는데 파일이 없으면 오류입니다.
func Load(flagPath string) (*Config, error) {
	cfg := Default()
	for _, p := range Candidates(flagPath) {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && flagPath == "" {
				continue
			}
			return nil, fmt.Errorf("설정 파일 읽기 실패 %s: %w", p, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("설정 파일 파싱 실패 %s: %w", p, err)
		}
		abs, _ := filepath.Abs(p)
		cfg.File = abs
		cfg.ConfDir = filepath.Dir(abs)
		break
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var err error
	if cfg.Keys, err = LoadKeybindings(cfg.confPath("keybindings.yaml")); err != nil {
		return nil, err
	}
	if cfg.Theme, err = LoadTheme(cfg.confPath("theme.yaml"), cfg.UI.Theme); err != nil {
		return nil, err
	}
	if cfg.Views, err = LoadViewOverrides(cfg.confPath("views.d")); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) confPath(name string) string {
	if c.ConfDir == "" {
		return ""
	}
	return filepath.Join(c.ConfDir, name)
}

// Validate는 값 범위를 검사하고 비어 있는 값은 기본값으로 채웁니다.
func (c *Config) Validate() error {
	d := Default()
	if c.UI.RefreshInterval < 500*time.Millisecond {
		c.UI.RefreshInterval = d.UI.RefreshInterval
	}
	if c.UI.LogTailLines <= 0 {
		c.UI.LogTailLines = d.UI.LogTailLines
	}
	if c.UI.MaxLogLines < 1000 {
		c.UI.MaxLogLines = d.UI.MaxLogLines
	}
	if c.Tools.Timeout <= 0 {
		c.Tools.Timeout = d.Tools.Timeout
	}
	if c.Backup.Keep < 1 {
		c.Backup.Keep = 1
	}
	if c.K3s.ServiceName == "" {
		c.K3s.ServiceName = d.K3s.ServiceName
	}
	if c.K3s.Binary == "" {
		c.K3s.Binary = d.K3s.Binary
	}
	if c.K3s.DataDir == "" {
		c.K3s.DataDir = "auto"
	}
	if c.UI.DefaultNamespace == "" {
		c.UI.DefaultNamespace = "all"
	}
	if c.Cluster.Distribution == "" {
		c.Cluster.Distribution = DistroAuto
	}
	c.Cluster.Distribution = strings.ToLower(c.Cluster.Distribution)
	switch c.Cluster.Distribution {
	case DistroAuto, DistroK3s, DistroRKE2, DistroKubeadm, DistroEKS, DistroGKE, DistroKubernetes:
	default:
		return fmt.Errorf("cluster.distribution 값이 올바르지 않습니다: %q (auto, k3s, rke2, kubeadm, eks, gke, kubernetes)", c.Cluster.Distribution)
	}
	if c.Cluster.HostManagement == "" {
		c.Cluster.HostManagement = HostAuto
	}
	c.Cluster.HostManagement = strings.ToLower(c.Cluster.HostManagement)
	switch c.Cluster.HostManagement {
	case HostAuto, HostEnabled, HostDisabled:
	default:
		return fmt.Errorf("cluster.host_management는 auto, enabled, disabled 중 하나여야 합니다: %q", c.Cluster.HostManagement)
	}
	if c.Cluster.Kubeconfig == "" && c.K3s.Kubeconfig != "" {
		c.Cluster.Kubeconfig = c.K3s.Kubeconfig
	}
	switch strings.ToLower(c.UI.Theme) {
	case "dark", "light":
	default:
		return fmt.Errorf("ui.theme은 dark 또는 light여야 합니다: %q", c.UI.Theme)
	}
	switch strings.ToLower(c.UI.DefaultView) {
	case "dashboard", "workloads", "network", "storage", "config", "host", "helm":
	default:
		return fmt.Errorf("ui.default_view 값이 올바르지 않습니다: %q", c.UI.DefaultView)
	}
	return nil
}

// IsProtected는 삭제 시 이름 재입력이 필요한 네임스페이스인지 판단합니다.
func (c *Config) IsProtected(ns string) bool {
	for _, p := range c.Safety.ProtectedNamespaces {
		if p == ns {
			return true
		}
	}
	return false
}

// EditorCommand는 외부 편집기 실행 명령을 돌려줍니다.
func (c *Config) EditorCommand() []string {
	for _, e := range []string{c.Tools.Editor, os.Getenv("EDITOR"), os.Getenv("VISUAL")} {
		if f := strings.Fields(e); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// KubectlCommand는 kubectl 실행 명령(앞부분)을 돌려줍니다.
// tools.kubectl이 있으면 그 값을 쓰고, 로컬 K3S면 "k3s kubectl"을,
// 그 밖에는 PATH의 kubectl을 씁니다. PATH에 없으면 k3s 바이너리로 대신합니다.
func (c *Config) KubectlCommand(localK3s bool) []string {
	if f := strings.Fields(c.Tools.Kubectl); len(f) > 0 {
		return f
	}
	if localK3s {
		return []string{c.K3s.Binary, "kubectl"}
	}
	if p, err := exec.LookPath("kubectl"); err == nil {
		return []string{p}
	}
	if _, err := os.Stat(c.K3s.Binary); err == nil {
		return []string{c.K3s.Binary, "kubectl"}
	}
	return []string{"kubectl"}
}

// KubeconfigCandidate는 kubeconfig 후보 하나입니다.
type KubeconfigCandidate struct {
	Path     string // ':'로 여러 파일을 이을 수 있습니다 (KUBECONFIG 형식)
	Source   string // 화면 표시용 출처
	Explicit bool   // 사용자가 직접 지정했으면 검증 없이 씁니다
}

// KubeconfigCandidates는 kubeconfig 탐색 순서를 돌려줍니다.
// --kubeconfig → cluster.kubeconfig → $KUBECONFIG → ~/.kube/config → K3S → RKE2 → kubeadm
func (c *Config) KubeconfigCandidates(flagPath string) []KubeconfigCandidate {
	var out []KubeconfigCandidate
	if flagPath != "" {
		out = append(out, KubeconfigCandidate{Path: flagPath, Source: "--kubeconfig", Explicit: true})
	}
	if c.Cluster.Kubeconfig != "" {
		out = append(out, KubeconfigCandidate{Path: c.Cluster.Kubeconfig, Source: "cluster.kubeconfig", Explicit: true})
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		out = append(out, KubeconfigCandidate{Path: env, Source: "$KUBECONFIG"})
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, KubeconfigCandidate{Path: filepath.Join(home, ".kube", "config"), Source: "~/.kube/config"})
	}
	out = append(out,
		KubeconfigCandidate{Path: "/etc/rancher/k3s/k3s.yaml", Source: "K3S 기본 경로"},
		KubeconfigCandidate{Path: "/etc/rancher/rke2/rke2.yaml", Source: "RKE2 기본 경로"},
		KubeconfigCandidate{Path: "/etc/kubernetes/admin.conf", Source: "kubeadm 기본 경로"},
	)
	return out
}
