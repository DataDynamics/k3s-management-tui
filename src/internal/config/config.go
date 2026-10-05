// Package config는 conf/*.yaml을 읽어 내장 기본값과 병합합니다.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config는 conf/k3stui.yaml의 내용입니다.
type Config struct {
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

type K3sConfig struct {
	ConfigFile  string `yaml:"config_file"`
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
		K3s: K3sConfig{
			ConfigFile:  "/etc/rancher/k3s/config.yaml",
			Kubeconfig:  "/etc/rancher/k3s/k3s.yaml",
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
func Candidates(flagPath string) []string {
	var out []string
	if flagPath != "" {
		return []string{flagPath}
	}
	if env := os.Getenv("K3STUI_CONF"); env != "" {
		out = append(out, env)
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
func (c *Config) KubectlCommand() []string {
	if f := strings.Fields(c.Tools.Kubectl); len(f) > 0 {
		return f
	}
	return []string{c.K3s.Binary, "kubectl"}
}
