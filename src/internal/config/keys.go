package config

import (
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Keybindings는 conf/keybindings.yaml의 내용입니다.
// global은 화면 공통 키, actions는 리소스·호스트 작업 키(작업 ID → 키 목록)입니다.
type Keybindings struct {
	Global  map[string][]string `yaml:"global"`
	Actions map[string][]string `yaml:"actions"`
}

// 전역 키 ID
const (
	KeyQuit       = "quit"
	KeyHelp       = "help"
	KeyFilter     = "filter"
	KeyCommand    = "command"
	KeyNamespace  = "namespace"
	KeyBack       = "back"
	KeyRefresh    = "refresh"
	KeyNextSource = "next_source"
	KeyPrevSource = "prev_source"
	KeyUp         = "up"
	KeyDown       = "down"
	KeyPageUp     = "page_up"
	KeyPageDown   = "page_down"
	KeyTop        = "top"
	KeyBottom     = "bottom"
	KeyLeft       = "left"
	KeyRight      = "right"
)

// DefaultKeybindings는 설계 5.3절의 기본 키 체계입니다.
// actions의 기본값은 각 작업 정의에 들어 있으므로 여기서는 비워 둡니다.
func DefaultKeybindings() *Keybindings {
	return &Keybindings{
		Global: map[string][]string{
			KeyQuit:       {"q", "ctrl+c"},
			KeyHelp:       {"?"},
			KeyFilter:     {"/"},
			KeyCommand:    {":"},
			KeyNamespace:  {"n"},
			KeyBack:       {"esc"},
			KeyRefresh:    {"ctrl+r"},
			KeyNextSource: {"tab", "]"},
			KeyPrevSource: {"shift+tab", "["},
			KeyUp:         {"up", "k"},
			KeyDown:       {"down", "j"},
			KeyPageUp:     {"pgup", "ctrl+b"},
			KeyPageDown:   {"pgdown", "ctrl+f"},
			KeyTop:        {"home", "g"},
			KeyBottom:     {"end", "G"},
			KeyLeft:       {"left", "h"},
			KeyRight:      {"right"},
		},
		Actions: map[string][]string{},
	}
}

// LoadKeybindings는 기본값 위에 파일 내용을 덮어씁니다. path가 비었거나 파일이 없으면 기본값만 씁니다.
func LoadKeybindings(path string) (*Keybindings, error) {
	kb := DefaultKeybindings()
	if path == "" {
		return kb, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return kb, nil
	}
	if err != nil {
		return nil, err
	}
	var file Keybindings
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s 파싱 실패: %w", path, err)
	}
	for k, v := range file.Global {
		kb.Global[k] = v
	}
	for k, v := range file.Actions {
		kb.Actions[k] = v
	}
	return kb, nil
}

// Is는 key가 전역 키 id에 해당하는지 확인합니다.
func (k *Keybindings) Is(id, key string) bool {
	for _, b := range k.Global[id] {
		if b == key {
			return true
		}
	}
	return false
}

// First는 전역 키 id의 첫 번째 키(도움말 표시용)를 돌려줍니다.
func (k *Keybindings) First(id string) string {
	if v := k.Global[id]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// ActionKeys는 작업 id의 키 목록을 돌려줍니다. 설정에 없으면 def를 씁니다.
func (k *Keybindings) ActionKeys(id string, def []string) []string {
	if v, ok := k.Actions[id]; ok {
		return v
	}
	return def
}
