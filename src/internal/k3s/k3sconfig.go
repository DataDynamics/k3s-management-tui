package k3s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// K3sConfig는 /etc/rancher/k3s/config.yaml(+ config.yaml.d/*.yaml) 내용입니다.
type K3sConfig struct {
	Path    string
	Raw     string
	Values  map[string]any
	DropIns []string // config.yaml.d의 파일들 (값은 Values에 병합됨)
}

// LoadConfig는 설정 파일을 읽습니다. 파일이 없으면 빈 설정을 돌려줍니다.
func (s *System) LoadConfig() (*K3sConfig, error) {
	return LoadK3sConfig(s.cfg.K3s.ConfigFile)
}

// LoadK3sConfig는 K3S 설정과 drop-in 파일을 읽어 병합합니다.
// K3S 규칙: drop-in은 이름순으로 적용되고, 키 끝에 +가 붙으면 목록에 덧붙입니다.
func LoadK3sConfig(path string) (*K3sConfig, error) {
	kc := &K3sConfig{Path: path, Values: map[string]any{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		if errors.Is(err, os.ErrPermission) {
			return kc, ErrNotRoot
		}
		return kc, err
	default:
		kc.Raw = string(data)
		if err := yaml.Unmarshal(data, &kc.Values); err != nil {
			return kc, fmt.Errorf("%s 파싱 실패: %w", path, err)
		}
		if kc.Values == nil {
			kc.Values = map[string]any{}
		}
	}
	dropins, _ := filepath.Glob(path + ".d/*.yaml")
	sort.Strings(dropins)
	for _, f := range dropins {
		d, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m map[string]any
		if yaml.Unmarshal(d, &m) != nil {
			continue
		}
		kc.DropIns = append(kc.DropIns, f)
		for k, v := range m {
			if base, ok := strings.CutSuffix(k, "+"); ok {
				prev, _ := kc.Values[base].([]any)
				add, _ := v.([]any)
				kc.Values[base] = append(prev, add...)
				continue
			}
			kc.Values[k] = v
		}
	}
	return kc, nil
}

// String은 문자열 값을 돌려줍니다.
func (k *K3sConfig) String(key string) string {
	if v, ok := k.Values[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// Strings는 목록 값(또는 단일 값)을 문자열 목록으로 돌려줍니다.
func (k *K3sConfig) Strings(key string) []string {
	switch v := k.Values[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case string:
		// "a,b" 형식도 K3S가 허용합니다.
		return strings.Split(v, ",")
	case nil:
		return nil
	default:
		return []string{fmt.Sprint(v)}
	}
}

// Keys는 정렬된 키 목록입니다.
func (k *K3sConfig) Keys() []string {
	keys := make([]string, 0, len(k.Values))
	for key := range k.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Display는 값을 한 줄로 표시합니다. token류는 가립니다.
func (k *K3sConfig) Display(key string) string {
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") {
		return "********"
	}
	switch v := k.Values[key].(type) {
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = fmt.Sprint(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

// ValidateYAML은 편집된 설정이 YAML 매핑인지 확인합니다.
func ValidateYAML(data []byte) error {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("YAML 오류: %w", err)
	}
	return nil
}

// WriteConfig는 기존 설정을 백업 디렉터리에 복사한 뒤 새 내용을 씁니다.
func (s *System) WriteConfig(content []byte) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	if err := ValidateYAML(content); err != nil {
		return "", err
	}
	path := s.cfg.K3s.ConfigFile
	mode := os.FileMode(0o644)
	var backup string
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		old, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		dir := filepath.Join(s.cfg.Backup.Dir, "config")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		backup = filepath.Join(dir, filepath.Base(path)+"."+s.now().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("백업 실패: %w", err)
		}
	}
	tmp := path + ".k3stui.tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return backup, err
	}
	return backup, nil
}

// cutFlag는 "root-dir=/x" 또는 "--root-dir=/x"에서 값을 꺼냅니다.
func cutFlag(arg, name string) (string, bool) {
	arg = strings.TrimLeft(arg, "-")
	if v, ok := strings.CutPrefix(arg, name+"="); ok {
		return v, true
	}
	return "", false
}

// ModTime은 파일 수정 시각입니다 (없으면 0).
func ModTime(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}
