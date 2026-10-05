package k3s

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// LoadConfig는 /etc/rancher/k3s/config.yaml(+ config.yaml.d)을 읽습니다. 파일이 없으면 빈 설정입니다.
func (s *System) LoadConfig() (*host.Config, error) {
	return LoadK3sConfig(s.fl.configFile)
}

// LoadK3sConfig는 K3S 설정과 drop-in 파일을 읽어 병합합니다.
// K3S 규칙: drop-in은 이름순으로 적용되고, 키 끝에 +가 붙으면 목록에 덧붙입니다.
func LoadK3sConfig(path string) (*host.Config, error) {
	kc, err := host.LoadYAMLConfig(path)
	if err != nil {
		return kc, err
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

// ValidateConfig는 편집한 config.yaml이 YAML 매핑인지 확인합니다.
func (s *System) ValidateConfig(content []byte) error { return host.ValidateYAML(content) }

// WriteConfig는 기존 설정을 <backup.dir>/config/에 복사한 뒤 새 내용을 씁니다.
func (s *System) WriteConfig(content []byte) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	if err := host.ValidateYAML(content); err != nil {
		return "", err
	}
	return host.WriteFileWithBackup(s.fl.configFile, content, filepath.Join(s.cfg.Backup.Dir, "config"), 0o644, s.now())
}

// cutFlag는 "root-dir=/x" 또는 "--root-dir=/x"에서 값을 꺼냅니다.
func cutFlag(arg, name string) (string, bool) {
	arg = strings.TrimLeft(arg, "-")
	if v, ok := strings.CutPrefix(arg, name+"="); ok {
		return v, true
	}
	return "", false
}
