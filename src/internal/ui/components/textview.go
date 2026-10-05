package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TextView는 스크롤·줄바꿈·검색·follow를 지원하는 텍스트 뷰어입니다 (로그, YAML, describe).
type TextView struct {
	lines    []string
	offset   int
	xoffset  int
	Wrap     bool
	Follow   bool // 끝에 고정 (로그 follow)
	MaxLines int
	Styler   func(line string) string // 줄 단위 색상 처리 (nil 가능)

	query   string
	matches []int
	matchI  int

	height, width int
}

// SetText는 전체 내용을 바꿉니다.
func (v *TextView) SetText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	v.lines = strings.Split(strings.TrimRight(s, "\n"), "\n")
	v.recomputeMatches()
	v.clamp()
}

// Append는 줄을 추가합니다 (스트리밍). MaxLines를 넘으면 앞부분을 버립니다.
func (v *TextView) Append(lines ...string) {
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(strings.TrimRight(l, "\r"), "\t", "    ")
	}
	v.lines = append(v.lines, lines...)
	if v.MaxLines > 0 && len(v.lines) > v.MaxLines {
		drop := len(v.lines) - v.MaxLines
		v.lines = append([]string(nil), v.lines[drop:]...)
		v.offset = max(0, v.offset-drop)
	}
	if v.query != "" {
		v.recomputeMatches()
	}
	if v.Follow {
		v.Bottom()
	}
}

// Lines는 전체 줄 수입니다.
func (v *TextView) Lines() int { return len(v.lines) }

func (v *TextView) visualLines() []string {
	if !v.Wrap || v.width <= 0 {
		return v.lines
	}
	out := make([]string, 0, len(v.lines))
	for _, l := range v.lines {
		if ansi.StringWidth(l) <= v.width {
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(ansi.Hardwrap(l, v.width, true), "\n")...)
	}
	return out
}

func (v *TextView) maxOffset() int {
	h := v.height
	if h <= 0 {
		h = 1
	}
	return max(0, len(v.visualLines())-h)
}

func (v *TextView) clamp() {
	v.offset = min(max(0, v.offset), v.maxOffset())
	if v.xoffset < 0 {
		v.xoffset = 0
	}
}

func (v *TextView) Up(n int) {
	v.offset -= n
	v.Follow = false
	v.clamp()
}

func (v *TextView) Down(n int) {
	v.offset += n
	v.clamp()
}

func (v *TextView) Top()    { v.offset = 0; v.Follow = false }
func (v *TextView) Bottom() { v.offset = v.maxOffset() }

func (v *TextView) Left()  { v.xoffset -= 8; v.clamp() }
func (v *TextView) Right() { v.xoffset += 8 }

// PageSize는 화면 높이입니다.
func (v *TextView) PageSize() int { return max(1, v.height) }

// Search는 대소문자 무시 검색을 시작하고 첫 결과로 이동합니다.
func (v *TextView) Search(q string) bool {
	v.query = strings.ToLower(q)
	v.recomputeMatches()
	v.matchI = -1
	return v.NextMatch(1)
}

// Query는 현재 검색어입니다.
func (v *TextView) Query() string { return v.query }

// MatchInfo는 (현재 결과 번호, 전체 결과 수)입니다.
func (v *TextView) MatchInfo() (int, int) { return v.matchI + 1, len(v.matches) }

func (v *TextView) recomputeMatches() {
	v.matches = v.matches[:0]
	if v.query == "" {
		return
	}
	for i, l := range v.lines {
		if strings.Contains(strings.ToLower(ansi.Strip(l)), v.query) {
			v.matches = append(v.matches, i)
		}
	}
}

// NextMatch는 dir(+1/-1) 방향 다음 결과로 이동합니다. 줄바꿈 모드에서도 원본 줄 기준으로 이동합니다.
func (v *TextView) NextMatch(dir int) bool {
	if len(v.matches) == 0 {
		return false
	}
	v.matchI = (v.matchI + dir + len(v.matches)) % len(v.matches)
	target := v.matches[v.matchI]
	if v.Wrap {
		// 원본 줄 번호를 시각 줄 번호로 변환
		vis := 0
		for i := 0; i < target && i < len(v.lines); i++ {
			vis += max(1, (ansi.StringWidth(v.lines[i])+v.width-1)/max(1, v.width))
		}
		target = vis
	}
	v.Follow = false
	v.offset = target - v.PageSize()/3
	v.clamp()
	return true
}

// View는 width×height 영역을 그립니다.
func (v *TextView) View(width, height int, match lipgloss.Style) string {
	v.width, v.height = width, height
	v.clamp()
	if v.Follow {
		v.offset = v.maxOffset()
	}
	lines := v.visualLines()
	end := min(len(lines), v.offset+height)
	var sb strings.Builder
	for i := v.offset; i < end; i++ {
		l := lines[i]
		if v.xoffset > 0 && !v.Wrap {
			l = ansi.Cut(l, v.xoffset, v.xoffset+width)
		} else if ansi.StringWidth(l) > width {
			l = ansi.Truncate(l, width, "…")
		}
		if v.query != "" {
			l = highlight(l, v.query, match)
		} else if v.Styler != nil {
			l = v.Styler(l)
		}
		if i > v.offset {
			sb.WriteByte('\n')
		}
		sb.WriteString(l)
	}
	return sb.String()
}

// Percent는 스크롤 위치(%)입니다.
func (v *TextView) Percent() int {
	m := v.maxOffset()
	if m == 0 {
		return 100
	}
	return v.offset * 100 / m
}

// highlight는 ANSI가 없는 줄에서 검색어를 강조합니다.
func highlight(line, q string, st lipgloss.Style) string {
	plain := ansi.Strip(line)
	lower := strings.ToLower(plain)
	if !strings.Contains(lower, q) || len(lower) != len(plain) {
		return line
	}
	var sb strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], q)
		if j < 0 {
			sb.WriteString(plain[i:])
			break
		}
		sb.WriteString(plain[i : i+j])
		sb.WriteString(st.Render(plain[i+j : i+j+len(q)]))
		i += j + len(q)
	}
	return sb.String()
}
