package kube

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Age는 kubectl과 같은 형식(5s, 3m, 4h, 12d)으로 경과 시간을 표시합니다.
func Age(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	return HumanDuration(now.Sub(t))
}

// HumanDuration은 기간을 짧게 표시합니다.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute*2:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour*3:
		m := int(d.Minutes())
		if s := int(d.Seconds()) % 60; s != 0 && d < 10*time.Minute {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	case d < time.Hour*48:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m != 0 && d < 8*time.Hour {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	case d < time.Hour*24*365*2:
		days := int(d.Hours() / 24)
		if h := int(d.Hours()) % 24; h != 0 && days < 8 {
			return fmt.Sprintf("%dd%dh", days, h)
		}
		return fmt.Sprintf("%dd", days)
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// FormatCPU는 millicore를 "250m" 또는 "1.5" 형태로 표시합니다.
func FormatCPU(milli int64) string {
	if milli < 1000 {
		return fmt.Sprintf("%dm", milli)
	}
	return fmt.Sprintf("%.1f", float64(milli)/1000)
}

// FormatBytes는 바이트를 이진 단위(Ki, Mi, Gi)로 표시합니다.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	suffix := []string{"Ki", "Mi", "Gi", "Ti", "Pi"}[exp]
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, suffix)
	}
	return fmt.Sprintf("%.1f%s", v, suffix)
}

// pathSeg는 필드 경로의 한 단계입니다.
type pathSeg struct {
	key   string // 맵 키
	index int    // 배열 위치 (key가 비어 있을 때)
	all   bool   // [*]: 배열의 모든 항목
}

// ParseFieldPath는 views.d의 path 값을 해석합니다.
//
//	spec.nodeName                           점으로 구분한 키
//	metadata.labels["app.kubernetes.io/name"] 점·슬래시가 들어간 키는 ["..."] 또는 ['...']
//	spec.containers[0].image                배열 위치 (0부터)
//	spec.containers[*].image                배열의 모든 항목 (쉼표로 이어서 표시)
//
// 맨 앞의 점(.)은 있어도 되고 없어도 됩니다 (kubectl jsonpath 습관 호환).
func ParseFieldPath(path string) ([]pathSeg, error) {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(p, "{"), "}"), ".")
	if p == "" {
		return nil, fmt.Errorf("path가 비어 있습니다")
	}
	var segs []pathSeg
	for i := 0; i < len(p); {
		switch p[i] {
		case '.':
			if i+1 >= len(p) || p[i+1] == '.' || p[i+1] == '[' {
				return nil, fmt.Errorf("%q: %d번째 글자 근처의 점(.)이 올바르지 않습니다", path, i+1)
			}
			i++
		case '[':
			j := i + 1
			for j < len(p) && p[j] == ' ' {
				j++
			}
			if j < len(p) && (p[j] == '"' || p[j] == '\'') {
				// ["키"] 또는 ['키']: 따옴표 안의 점·슬래시·]는 키의 일부입니다.
				q := p[j]
				closeQ := strings.IndexByte(p[j+1:], q)
				if closeQ < 0 {
					return nil, fmt.Errorf("%q: 닫는 따옴표가 없습니다", path)
				}
				key := p[j+1 : j+1+closeQ]
				k := j + 1 + closeQ + 1
				for k < len(p) && p[k] == ' ' {
					k++
				}
				if k >= len(p) || p[k] != ']' {
					return nil, fmt.Errorf("%q: 따옴표 키 뒤에 ]가 와야 합니다", path)
				}
				segs = append(segs, pathSeg{key: key})
				i = k + 1
				continue
			}
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("%q: 닫는 ]가 없습니다", path)
			}
			inner := strings.TrimSpace(p[i+1 : i+end])
			if inner == "*" {
				segs = append(segs, pathSeg{all: true})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("%q: [%s]는 0 이상의 숫자, *, 또는 따옴표로 감싼 키여야 합니다", path, inner)
				}
				segs = append(segs, pathSeg{index: n})
			}
			i += end + 1
		default:
			end := i
			for end < len(p) && p[end] != '.' && p[end] != '[' {
				end++
			}
			segs = append(segs, pathSeg{key: p[i:end]})
			i = end
		}
	}
	return segs, nil
}

func walkPath(v any, segs []pathSeg) []any {
	if len(segs) == 0 {
		if v == nil {
			return nil
		}
		return []any{v}
	}
	s := segs[0]
	switch {
	case s.key != "":
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		return walkPath(m[s.key], segs[1:])
	case s.all:
		arr, ok := v.([]any)
		if !ok {
			return nil
		}
		var out []any
		for _, it := range arr {
			out = append(out, walkPath(it, segs[1:])...)
		}
		return out
	default:
		arr, ok := v.([]any)
		if !ok || s.index >= len(arr) {
			return nil
		}
		return walkPath(arr[s.index], segs[1:])
	}
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + formatValue(t[k])
		}
		return strings.Join(parts, ",")
	case []any:
		parts := make([]string, 0, len(t))
		for _, it := range t {
			parts = append(parts, formatValue(it))
		}
		return strings.Join(parts, ",")
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// FieldString은 path(ParseFieldPath 문법)로 값을 꺼내 문자열로 표시합니다.
// 값이 없거나 path가 잘못되면 ""를 돌려줍니다. 여러 값([*])은 쉼표로 잇습니다.
func FieldString(u *unstructured.Unstructured, path string) string {
	segs, err := ParseFieldPath(path)
	if err != nil {
		return ""
	}
	vals := walkPath(u.Object, segs)
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, formatValue(v))
	}
	return strings.Join(parts, ",")
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
