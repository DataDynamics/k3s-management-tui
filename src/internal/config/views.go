package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ColumnOverride는 conf/views.d/<resource>.yaml의 컬럼 하나입니다.
// path가 비어 있으면 내장 컬럼(name과 제목이 같은 것)을 쓰고,
// path가 있으면 객체에서 점(.) 경로로 값을 꺼냅니다 (예: spec.nodeName).
type ColumnOverride struct {
	Name  string `yaml:"name"`
	Path  string `yaml:"path"`
	Width int    `yaml:"width"`
}

// ViewOverride는 리소스 하나의 컬럼 재정의입니다.
type ViewOverride struct {
	Resource string           `yaml:"resource"`
	Columns  []ColumnOverride `yaml:"columns"`
}

// ViewOverrides는 리소스 키(pods, deployments ...) → 재정의입니다.
type ViewOverrides map[string]ViewOverride

// LoadViewOverrides는 디렉터리의 *.yaml을 모두 읽습니다. resource가 비면 파일 이름을 씁니다.
func LoadViewOverrides(dir string) (ViewOverrides, error) {
	out := ViewOverrides{}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var v ViewOverride
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("views.d/%s 파싱 실패: %w", name, err)
		}
		if v.Resource == "" {
			v.Resource = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		}
		if len(v.Columns) == 0 {
			continue
		}
		for i, c := range v.Columns {
			if c.Name == "" {
				return nil, fmt.Errorf("views.d/%s: %d번째 컬럼에 name이 없습니다", name, i+1)
			}
		}
		out[strings.ToLower(v.Resource)] = v
	}
	return out, nil
}
