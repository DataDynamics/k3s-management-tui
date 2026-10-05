package kube

import (
	"fmt"
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

// FieldString은 점(.) 경로로 문자열 값을 꺼냅니다. 문자열이 아니면 fmt로 표시합니다.
// 배열 인덱스는 지원하지 않습니다 (views.d 사용자 컬럼용).
func FieldString(u *unstructured.Unstructured, path string) string {
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return ""
	}
	v, found, err := unstructured.NestedFieldNoCopy(u.Object, strings.Split(path, ".")...)
	if !found || err != nil || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		parts := make([]string, 0, len(t))
		for k, val := range t {
			parts = append(parts, fmt.Sprintf("%s=%v", k, val))
		}
		return strings.Join(parts, ",")
	case []any:
		parts := make([]string, 0, len(t))
		for _, val := range t {
			parts = append(parts, fmt.Sprint(val))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(t)
	}
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
