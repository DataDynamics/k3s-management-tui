// Package components는 화면 구성 요소(테이블, 텍스트 뷰어, 다이얼로그)입니다.
package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Column은 테이블 컬럼입니다.
type Column struct {
	Title    string
	MaxWidth int
}

// Row는 테이블 행입니다. ID는 갱신 후에도 선택을 유지하는 데 씁니다.
type Row struct {
	ID    string
	Cells []string
	Style *lipgloss.Style // 선택되지 않았을 때의 색상 (nil이면 기본)
}

// Table은 스크롤 가능한 선택 테이블입니다.
type Table struct {
	Columns []Column
	Rows    []Row
	Cursor  int
	offset  int
	height  int // 마지막 렌더링 시 본문 높이 (페이지 이동용)
}

// SetRows는 행을 바꾸되, 이전에 선택했던 ID가 있으면 그 행을 계속 선택합니다.
func (t *Table) SetRows(rows []Row) {
	var selID string
	if t.Cursor >= 0 && t.Cursor < len(t.Rows) {
		selID = t.Rows[t.Cursor].ID
	}
	t.Rows = rows
	if selID != "" {
		for i, r := range rows {
			if r.ID == selID {
				t.Cursor = i
				t.clamp()
				return
			}
		}
	}
	t.clamp()
}

func (t *Table) clamp() {
	if t.Cursor >= len(t.Rows) {
		t.Cursor = len(t.Rows) - 1
	}
	if t.Cursor < 0 {
		t.Cursor = 0
	}
}

// Selected는 선택된 행입니다.
func (t *Table) Selected() (Row, bool) {
	if t.Cursor >= 0 && t.Cursor < len(t.Rows) {
		return t.Rows[t.Cursor], true
	}
	return Row{}, false
}

func (t *Table) Up(n int)   { t.Cursor -= n; t.clamp() }
func (t *Table) Down(n int) { t.Cursor += n; t.clamp() }
func (t *Table) Top()       { t.Cursor = 0 }
func (t *Table) Bottom()    { t.Cursor = len(t.Rows) - 1; t.clamp() }

// PageSize는 한 화면에 보이는 행 수입니다.
func (t *Table) PageSize() int {
	if t.height < 1 {
		return 10
	}
	return t.height
}

// widths는 화면 폭에 맞게 컬럼 폭을 계산합니다.
func (t *Table) widths(width int) []int {
	n := len(t.Columns)
	w := make([]int, n)
	for i, c := range t.Columns {
		w[i] = ansi.StringWidth(c.Title)
	}
	for _, r := range t.Rows {
		for i := 0; i < n && i < len(r.Cells); i++ {
			if cw := ansi.StringWidth(r.Cells[i]); cw > w[i] {
				w[i] = cw
			}
		}
	}
	for i, c := range t.Columns {
		if c.MaxWidth > 0 && w[i] > c.MaxWidth {
			w[i] = c.MaxWidth
		}
	}
	// 컬럼 사이 간격 2칸
	total := func() int {
		s := 0
		for _, x := range w {
			s += x
		}
		return s + 2*(n-1)
	}
	// 넘치면 가장 넓은 컬럼부터 줄입니다.
	for total() > width {
		maxI := 0
		for i := range w {
			if w[i] > w[maxI] {
				maxI = i
			}
		}
		if w[maxI] <= 6 {
			break
		}
		w[maxI]--
	}
	return w
}

func fit(s string, w int) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// View는 테이블을 그립니다. header 스타일, 선택 스타일, 빈 목록 메시지를 받습니다.
func (t *Table) View(width, height int, header, selected lipgloss.Style, empty string) string {
	if height < 2 {
		return ""
	}
	w := t.widths(width)
	var sb strings.Builder
	cells := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cells[i] = fit(c.Title, w[i])
	}
	sb.WriteString(header.Render(fit(strings.Join(cells, "  "), width)))
	body := height - 1
	t.height = body
	if len(t.Rows) == 0 {
		sb.WriteString("\n" + empty)
		return sb.String()
	}
	// 커서가 보이도록 offset 조정
	if t.Cursor < t.offset {
		t.offset = t.Cursor
	}
	if t.Cursor >= t.offset+body {
		t.offset = t.Cursor - body + 1
	}
	if t.offset > len(t.Rows)-body {
		t.offset = max(0, len(t.Rows)-body)
	}
	end := min(len(t.Rows), t.offset+body)
	for ri := t.offset; ri < end; ri++ {
		r := t.Rows[ri]
		for i := range t.Columns {
			v := ""
			if i < len(r.Cells) {
				v = r.Cells[i]
			}
			cells[i] = fit(v, w[i])
		}
		line := fit(strings.Join(cells, "  "), width)
		switch {
		case ri == t.Cursor:
			line = selected.Render(line)
		case r.Style != nil:
			line = r.Style.Render(line)
		}
		sb.WriteString("\n" + line)
	}
	return sb.String()
}

// Position은 "선택/전체" 표시입니다.
func (t *Table) Position() (int, int) {
	if len(t.Rows) == 0 {
		return 0, 0
	}
	return t.Cursor + 1, len(t.Rows)
}
