package views

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// PrepareViewOverrides는 views.d 재정의를 검사하고 정리합니다.
//   - 별칭 파일 이름(po.yaml, deploy.yaml 등)을 리소스 키(pods, deployments)로 바꿉니다.
//   - 모르는 리소스, 없는 내장 컬럼, 잘못된 path는 경고로 돌려줍니다.
//
// 모르는 리소스와 컬럼이 하나도 남지 않는 재정의는 버리고 내장 컬럼을 씁니다.
func PrepareViewOverrides(cfg *config.Config) []string {
	var warns []string
	out := config.ViewOverrides{}
	keys := make([]string, 0, len(cfg.Views))
	for k := range cfg.Views {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 파일 이름 순서
	for _, k := range keys {
		ov := cfg.Views[k]
		file := ov.File
		if file == "" {
			file = k
		}
		res := ov.Resource
		if res == "" {
			res = k
		}
		d := kube.Def(res)
		if d == nil {
			warns = append(warns, fmt.Sprintf("views.d/%s: 알 수 없는 리소스 %q입니다 (k3stui --columns로 리소스 이름을 확인하세요)", file, res))
			continue
		}
		if prev, dup := out[d.Key]; dup {
			warns = append(warns, fmt.Sprintf("views.d: %s 재정의가 두 번 있습니다 (%s, %s). 나중 파일(%s)을 씁니다", d.Key, prev.File, file, file))
		}
		var cols []config.ColumnOverride
		for _, c := range ov.Columns {
			if c.Path != "" {
				if _, err := kube.ParseFieldPath(c.Path); err != nil {
					warns = append(warns, fmt.Sprintf("views.d/%s: 컬럼 %s의 path가 잘못되었습니다: %v", file, c.Name, err))
					continue
				}
				cols = append(cols, c)
				continue
			}
			if builtinIndex(d, c.Name) < 0 {
				warns = append(warns, fmt.Sprintf("views.d/%s: 내장 컬럼 %q가 없습니다 (쓸 수 있는 컬럼: %s). path를 지정하거나 이름을 고치세요",
					file, c.Name, strings.Join(columnNames(d), ", ")))
				continue
			}
			cols = append(cols, c)
		}
		if len(cols) == 0 {
			warns = append(warns, fmt.Sprintf("views.d/%s: 쓸 수 있는 컬럼이 없어 내장 컬럼을 그대로 씁니다", file))
			continue
		}
		ov.Resource, ov.Columns, ov.File = d.Key, cols, file
		out[d.Key] = ov
	}
	cfg.Views = out
	return warns
}

func builtinIndex(d *kube.ResourceDef, name string) int {
	for i, c := range d.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

func columnNames(d *kube.ResourceDef) []string {
	out := make([]string, len(d.Columns))
	for i, c := range d.Columns {
		out[i] = c.Name
	}
	return out
}

// PrintColumns는 리소스별 키, 별칭, 내장 컬럼을 출력합니다 (k3stui --columns).
// key가 비어 있으면 전체를 출력합니다.
func PrintColumns(w io.Writer, key string) error {
	defs := kube.Defs()
	if key != "" {
		d := kube.Def(key)
		if d == nil {
			return fmt.Errorf("알 수 없는 리소스: %s", key)
		}
		defs = []*kube.ResourceDef{d}
	}
	for _, d := range defs {
		scope := "클러스터 범위"
		if d.Namespaced {
			scope = "네임스페이스 범위"
		}
		fmt.Fprintf(w, "%s (%s)\n", d.Key, scope)
		fmt.Fprintf(w, "  파일 이름: views.d/%s.yaml", d.Key)
		if len(d.Aliases) > 0 {
			fmt.Fprintf(w, "  (별칭으로도 가능: %s)", strings.Join(d.Aliases, ", "))
		}
		fmt.Fprintf(w, "\n  내장 컬럼: %s\n", strings.Join(columnNames(d), ", "))
		if d.Namespaced {
			fmt.Fprintln(w, "  NAMESPACE 컬럼은 네임스페이스가 all일 때 맨 앞에 자동으로 붙습니다 (재정의 대상 아님)")
		}
		fmt.Fprintln(w)
	}
	return nil
}
