package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
)

func TestPrepareViewOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Views = config.ViewOverrides{
		"po.yaml": {Resource: "po", File: "po.yaml", Columns: []config.ColumnOverride{
			{Name: "name"}, {Name: "NOPE"}, {Name: "QOS", Path: "status.qosClass"}, {Name: "BAD", Path: "a[x]"}}},
		"widgets.yaml": {Resource: "widgets", File: "widgets.yaml", Columns: []config.ColumnOverride{{Name: "NAME"}}},
		"svc.yaml":     {Resource: "svc", File: "svc.yaml", Columns: []config.ColumnOverride{{Name: "WRONG"}}},
		"x-pods.yaml":  {Resource: "pods", File: "x-pods.yaml", Columns: []config.ColumnOverride{{Name: "NAME"}, {Name: "AGE"}}},
	}
	warns := PrepareViewOverrides(cfg)
	joined := strings.Join(warns, "\n")
	for _, want := range []string{
		`views.d/po.yaml: 내장 컬럼 "NOPE"가 없습니다`,
		"views.d/po.yaml: 컬럼 BAD의 path가 잘못되었습니다",
		`views.d/widgets.yaml: 알 수 없는 리소스 "widgets"`,
		"views.d/svc.yaml: 쓸 수 있는 컬럼이 없어",
		"pods 재정의가 두 번 있습니다 (po.yaml, x-pods.yaml)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("경고에 %q가 없음:\n%s", want, joined)
		}
	}
	// 별칭이 리소스 키로 바뀌고, 나중 파일(x-pods.yaml)이 남습니다.
	if len(cfg.Views) != 1 {
		t.Fatalf("정리 후 재정의: %+v", cfg.Views)
	}
	if v := cfg.Views["pods"]; v.File != "x-pods.yaml" || len(v.Columns) != 2 {
		t.Errorf("pods 재정의: %+v", v)
	}
}

func TestOverrideColumnsAndCells(t *testing.T) {
	cfg := config.Default()
	cfg.Views = config.ViewOverrides{"deployments": {Resource: "deployments", Columns: []config.ColumnOverride{
		{Name: "name"}, {Name: "IMAGES"}, {Name: "IMAGES2", Path: "spec.template.spec.containers[*].image"},
		{Name: "WIDE", Path: "metadata.name", Width: -1}, {Name: "images", Width: 100}}}}
	src := NewResourceSource("deploy")
	cols := src.Columns(&Env{Cfg: cfg})
	if cols[0].Name != "NAME" || cols[1].MaxWidth != 60 || cols[2].MaxWidth != 0 || cols[3].MaxWidth != 0 || cols[4].MaxWidth != 100 {
		t.Errorf("컬럼 폭 규칙: %+v", cols)
	}
}

// 저장소에 들어 있는 예제가 모두 오류 없이 읽혀야 합니다.
func TestShippedExamplesAreValid(t *testing.T) {
	confDir := filepath.Join("..", "..", "..", "..", "conf")
	examples, _ := filepath.Glob(filepath.Join(confDir, "views.d", "*.yaml.example"))
	if len(examples) == 0 {
		t.Fatal("views.d 예제가 없습니다")
	}
	tmp := t.TempDir()
	for _, f := range examples {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".example")
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	views, err := config.LoadViewOverrides(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Views = views
	if warns := PrepareViewOverrides(cfg); len(warns) > 0 {
		t.Errorf("예제에 경고가 있습니다:\n%s", strings.Join(warns, "\n"))
	}
	if len(cfg.Views) != len(examples) {
		t.Errorf("예제 %d개 중 %d개만 적용됨", len(examples), len(cfg.Views))
	}
	for _, f := range []string{filepath.Join(confDir, "k3stui.yaml"), filepath.Join(confDir, "examples", "k3stui-k8s.yaml")} {
		c, err := config.Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if strings.HasSuffix(f, "k3stui-k8s.yaml") && c.Cluster.HostManagement != config.HostDisabled {
			t.Errorf("k8s 예제는 host_management: disabled여야 함: %+v", c.Cluster)
		}
	}
}
