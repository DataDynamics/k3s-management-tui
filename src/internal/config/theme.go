package config

import (
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Theme은 색상 팔레트입니다. 값은 "#rrggbb" 또는 ANSI 256 번호 문자열입니다.
type Theme struct {
	Fg         string `yaml:"fg"`
	Muted      string `yaml:"muted"`
	Primary    string `yaml:"primary"`
	Accent     string `yaml:"accent"`
	Border     string `yaml:"border"`
	SelectedFg string `yaml:"selected_fg"`
	SelectedBg string `yaml:"selected_bg"`
	HeaderFg   string `yaml:"header_fg"`
	HeaderBg   string `yaml:"header_bg"`
	OK         string `yaml:"ok"`
	Warn       string `yaml:"warn"`
	Err        string `yaml:"err"`
	Info       string `yaml:"info"`
}

func defaultThemes() map[string]Theme {
	return map[string]Theme{
		"dark": {
			Fg: "#d0d4dc", Muted: "#7a8290", Primary: "#5fafff", Accent: "#ffaf5f",
			Border: "#4a5060", SelectedFg: "#0f1115", SelectedBg: "#5fafff",
			HeaderFg: "#e8ecf2", HeaderBg: "#263040",
			OK: "#5fd787", Warn: "#ffd75f", Err: "#ff5f5f", Info: "#87d7ff",
		},
		"light": {
			Fg: "#1f2328", Muted: "#6e7781", Primary: "#0969da", Accent: "#bc4c00",
			Border: "#c4cad1", SelectedFg: "#ffffff", SelectedBg: "#0969da",
			HeaderFg: "#ffffff", HeaderBg: "#24292f",
			OK: "#1a7f37", Warn: "#9a6700", Err: "#cf222e", Info: "#0550ae",
		},
	}
}

// LoadTheme은 내장 테마 위에 theme.yaml의 같은 이름 테마를 덮어씁니다.
// 파일 형식: { dark: {...}, light: {...} }
func LoadTheme(path, name string) (*Theme, error) {
	themes := defaultThemes()
	t := themes[name]
	if path == "" {
		return &t, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &t, nil
	}
	if err != nil {
		return nil, err
	}
	var file map[string]map[string]string
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s 파싱 실패: %w", path, err)
	}
	over := file[name]
	set := func(dst *string, key string) {
		if v, ok := over[key]; ok && v != "" {
			*dst = v
		}
	}
	set(&t.Fg, "fg")
	set(&t.Muted, "muted")
	set(&t.Primary, "primary")
	set(&t.Accent, "accent")
	set(&t.Border, "border")
	set(&t.SelectedFg, "selected_fg")
	set(&t.SelectedBg, "selected_bg")
	set(&t.HeaderFg, "header_fg")
	set(&t.HeaderBg, "header_bg")
	set(&t.OK, "ok")
	set(&t.Warn, "warn")
	set(&t.Err, "err")
	set(&t.Info, "info")
	return &t, nil
}
