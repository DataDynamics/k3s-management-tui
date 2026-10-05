package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergesFileOverDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "k3stui.yaml"), `
ui:
  refresh_interval: 5s
  default_view: host
safety:
  read_only: true
backup:
  dir: /data/backups
`)
	write(t, filepath.Join(dir, "keybindings.yaml"), "global:\n  quit: [Q]\nactions:\n  delete: [D]\n")
	write(t, filepath.Join(dir, "theme.yaml"), "dark:\n  primary: \"#123456\"\n")
	write(t, filepath.Join(dir, "views.d", "pods.yaml"), "columns:\n  - name: NAME\n  - name: QOS\n    path: status.qosClass\n")

	cfg, err := Load(filepath.Join(dir, "k3stui.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.RefreshInterval != 5*time.Second || cfg.UI.DefaultView != "host" {
		t.Errorf("ui 값이 반영되지 않음: %+v", cfg.UI)
	}
	if !cfg.Safety.ReadOnly || cfg.Backup.Dir != "/data/backups" {
		t.Errorf("safety/backup 값이 반영되지 않음")
	}
	// 파일에 없는 값은 기본값 유지
	if cfg.K3s.ServiceName != "k3s" || cfg.UI.LogTailLines != 500 || len(cfg.Safety.ProtectedNamespaces) != 3 {
		t.Errorf("기본값이 유지되지 않음: %+v %+v", cfg.K3s, cfg.UI)
	}
	if !cfg.Keys.Is(KeyQuit, "Q") || cfg.Keys.Is(KeyQuit, "q") {
		t.Errorf("전역 키 재정의 실패: %v", cfg.Keys.Global[KeyQuit])
	}
	if !cfg.Keys.Is(KeyFilter, "/") {
		t.Error("재정의하지 않은 전역 키는 기본값이어야 함")
	}
	if got := cfg.Keys.ActionKeys("delete", []string{"x"}); len(got) != 1 || got[0] != "D" {
		t.Errorf("작업 키 재정의 실패: %v", got)
	}
	if got := cfg.Keys.ActionKeys("yaml", []string{"y"}); got[0] != "y" {
		t.Errorf("작업 키 기본값 실패: %v", got)
	}
	if cfg.Theme.Primary != "#123456" || cfg.Theme.OK == "" {
		t.Errorf("테마 병합 실패: %+v", cfg.Theme)
	}
	v, ok := cfg.Views["pods"]
	if !ok || len(v.Columns) != 2 || v.Columns[1].Path != "status.qosClass" {
		t.Errorf("views.d 로딩 실패: %+v", cfg.Views)
	}
}

func TestLoadWithoutFileUsesDefaults(t *testing.T) {
	t.Setenv("K3STUI_CONF", "")
	t.Setenv("K3STUI_HOME", t.TempDir())
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.K3s.Kubeconfig != "/etc/rancher/k3s/k3s.yaml" {
		t.Errorf("unexpected kubeconfig %q", cfg.K3s.Kubeconfig)
	}
}

func TestLoadExplicitMissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("지정한 파일이 없으면 오류여야 함")
	}
}

func TestValidate(t *testing.T) {
	cfg := Default()
	cfg.UI.Theme = "neon"
	if err := cfg.Validate(); err == nil {
		t.Error("잘못된 테마를 허용함")
	}
	cfg = Default()
	cfg.UI.DefaultView = "nowhere"
	if err := cfg.Validate(); err == nil {
		t.Error("잘못된 default_view를 허용함")
	}
	cfg = Default()
	cfg.UI.RefreshInterval = time.Millisecond
	cfg.Backup.Keep = 0
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.UI.RefreshInterval != 2*time.Second || cfg.Backup.Keep != 1 {
		t.Errorf("범위 밖 값 보정 실패: %v %d", cfg.UI.RefreshInterval, cfg.Backup.Keep)
	}
}

func TestEditorAndKubectl(t *testing.T) {
	cfg := Default()
	t.Setenv("EDITOR", "nano -w")
	t.Setenv("VISUAL", "")
	if got := cfg.EditorCommand(); len(got) != 2 || got[0] != "nano" {
		t.Errorf("EDITOR 해석 실패: %v", got)
	}
	cfg.Tools.Editor = "vim"
	if got := cfg.EditorCommand(); got[0] != "vim" {
		t.Errorf("conf editor 우선순위 실패: %v", got)
	}
	if got := cfg.KubectlCommand(); got[0] != "/usr/local/bin/k3s" || got[1] != "kubectl" {
		t.Errorf("기본 kubectl 명령: %v", got)
	}
	if !cfg.IsProtected("kube-system") || cfg.IsProtected("default") {
		t.Error("보호 네임스페이스 판정 오류")
	}
}
