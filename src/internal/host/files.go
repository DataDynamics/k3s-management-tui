package host

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config는 노드 설정 파일 내용입니다 (K3S config.yaml, kubelet config.yaml).
type Config struct {
	Path    string
	Raw     string
	Values  map[string]any
	DropIns []string // K3S config.yaml.d의 파일들 (값은 Values에 병합됨)
}

// String은 문자열 값을 돌려줍니다.
func (k *Config) String(key string) string {
	if v, ok := k.Values[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// Strings는 목록 값(또는 단일 값)을 문자열 목록으로 돌려줍니다.
func (k *Config) Strings(key string) []string {
	switch v := k.Values[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case string:
		return strings.Split(v, ",") // "a,b" 형식도 K3S가 허용합니다
	case nil:
		return nil
	default:
		return []string{fmt.Sprint(v)}
	}
}

// Keys는 정렬된 키 목록입니다.
func (k *Config) Keys() []string {
	keys := make([]string, 0, len(k.Values))
	for key := range k.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Display는 값을 한 줄로 표시합니다. token·secret·password가 들어간 키는 가립니다.
func (k *Config) Display(key string) string {
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") {
		return "********"
	}
	return displayValue(k.Values[key])
}

func displayValue(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = displayValue(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + displayValue(t[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprint(t)
	}
}

// LoadYAMLConfig는 YAML 매핑 파일을 읽습니다. 파일이 없으면 빈 설정입니다.
func LoadYAMLConfig(path string) (*Config, error) {
	c := &Config{Path: path, Values: map[string]any{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return c, nil
	case errors.Is(err, os.ErrPermission):
		return c, ErrNotRoot
	case err != nil:
		return c, err
	}
	c.Raw = string(data)
	if err := yaml.Unmarshal(data, &c.Values); err != nil {
		return c, fmt.Errorf("%s 파싱 실패: %w", path, err)
	}
	if c.Values == nil {
		c.Values = map[string]any{}
	}
	return c, nil
}

// ValidateYAML은 내용이 YAML 매핑인지 확인합니다.
func ValidateYAML(data []byte) error {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("YAML 오류: %w", err)
	}
	return nil
}

// ModTime은 파일 수정 시각입니다 (없으면 0).
func ModTime(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// WriteFileWithBackup은 기존 파일을 backupDir에 <이름>.<시각>으로 복사한 뒤 새 내용을 원자적으로 씁니다.
// 기존 파일 권한을 유지하고, 파일이 없었으면 defaultMode로 만듭니다. 백업 경로를 돌려줍니다.
func WriteFileWithBackup(path string, content []byte, backupDir string, defaultMode os.FileMode, now time.Time) (string, error) {
	mode := defaultMode
	var backup string
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		old, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			return "", err
		}
		backup = filepath.Join(backupDir, filepath.Base(path)+"."+now.Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("백업 실패: %w", err)
		}
	}
	// kubelet은 manifests 디렉터리의 숨김 파일(.으로 시작)을 무시하므로 임시 파일 이름을 숨김으로 둡니다.
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".k3stui.tmp")
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return backup, err
	}
	return backup, nil
}

// CopyFile은 dst가 없을 때만 파일을 복사합니다.
func CopyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// MoveFile은 rename을 시도하고, 다른 파일시스템이면 복사 후 원본을 지웁니다.
func MoveFile(src, dst string, mode os.FileMode) error {
	if err := os.Rename(src, dst); err == nil {
		return os.Chmod(dst, mode)
	}
	if err := CopyFile(src, dst, mode); err != nil {
		return err
	}
	return os.Remove(src)
}

// ListManifests는 디렉터리의 YAML·JSON 파일 목록(하위 디렉터리 포함)입니다. checkSkip이면 <파일>.skip 여부를 봅니다.
func ListManifests(root string, checkSkip bool) ([]Manifest, error) {
	var out []Manifest
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		m := Manifest{Path: p, Rel: rel, Size: info.Size(), ModTime: info.ModTime()}
		if checkSkip {
			_, skipErr := os.Stat(p + ".skip")
			m.Skipped = skipErr == nil
		}
		out = append(out, m)
		return nil
	})
	if errors.Is(err, os.ErrPermission) {
		return nil, ErrNotRoot
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

// InsideDir는 path가 dir 안에 있는지 확인합니다 (경로 조작 방지).
func InsideDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// ListBackupFiles는 dir에서 prefix로 시작하고 suffix로 끝나는 백업 파일을 최신순으로 돌려줍니다.
// extraSuffix 파일(<백업><extraSuffix>)이 있으면 HasExtra를 켭니다.
func ListBackupFiles(dir string, kind DatastoreKind, prefix, suffix, extraSuffix string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, ErrNotRoot
		}
		return nil, err
	}
	var out []BackupFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		if extraSuffix != "" && strings.HasSuffix(name, extraSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, name)
		hasExtra := false
		if extraSuffix != "" {
			_, xerr := os.Stat(p + extraSuffix)
			hasExtra = xerr == nil
		}
		out = append(out, BackupFile{Name: name, Path: p, Size: info.Size(), Time: info.ModTime(), Kind: kind, HasExtra: hasExtra})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// ValidBackupName은 백업 파일 이름에 경로가 섞이지 않았는지 확인합니다.
func ValidBackupName(name string) error {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("잘못된 파일 이름: %q", name)
	}
	return nil
}

// DiskInfo는 디렉터리가 속한 파일시스템 사용량입니다.
type DiskInfo struct {
	Label string
	Path  string
	Total uint64
	Used  uint64
	Err   string
}

// Percent는 사용률(%)입니다.
func (d DiskInfo) Percent() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Used) * 100 / float64(d.Total)
}

// FillDiskUsage는 각 항목의 파일시스템 사용량을 채웁니다. 디렉터리가 아직 없으면 상위 디렉터리 기준으로 봅니다.
func FillDiskUsage(items []DiskInfo) []DiskInfo {
	for i := range items {
		p := items[i].Path
		for p != "/" && p != "." {
			if _, err := os.Stat(p); err == nil {
				break
			}
			p = filepath.Dir(p)
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			items[i].Err = err.Error()
			continue
		}
		bs := uint64(st.Bsize)
		items[i].Total = st.Blocks * bs
		items[i].Used = (st.Blocks - st.Bfree) * bs
	}
	return items
}

// DirSize는 디렉터리 안 일반 파일 크기의 합입니다.
func DirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
