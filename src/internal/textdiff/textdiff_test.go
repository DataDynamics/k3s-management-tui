package textdiff

import (
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	a := "a: 1\nb: 2\nc: 3\n"
	b := "a: 1\nb: 20\nc: 3\nd: 4\n"
	got := Unified("old", "new", a, b, 1)
	for _, want := range []string{"--- old", "+++ new", "-b: 2", "+b: 20", "+d: 4", " a: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff에 %q가 없음:\n%s", want, got)
		}
	}
	if Unified("x", "y", a, a, 3) != "" {
		t.Error("같은 내용이면 빈 문자열이어야 함")
	}
}

func TestUnifiedFromEmpty(t *testing.T) {
	got := Unified("old", "new", "", "x: 1\n", 3)
	if !strings.Contains(got, "+x: 1") {
		t.Errorf("새 파일 diff 실패:\n%s", got)
	}
}
