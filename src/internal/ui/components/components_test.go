package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTableKeepsSelectionAcrossRefresh(t *testing.T) {
	tb := &Table{Columns: []Column{{Title: "NAME"}}}
	tb.SetRows([]Row{{ID: "a", Cells: []string{"a"}}, {ID: "b", Cells: []string{"b"}}, {ID: "c", Cells: []string{"c"}}})
	tb.Down(2)
	// 앞에 행이 추가되어도 같은 ID(c)를 계속 선택합니다.
	tb.SetRows([]Row{{ID: "z", Cells: []string{"z"}}, {ID: "a"}, {ID: "b"}, {ID: "c"}})
	if r, _ := tb.Selected(); r.ID != "c" {
		t.Errorf("선택 유지 실패: %s", r.ID)
	}
	// 선택 행이 사라지면 범위 안으로 보정
	tb.SetRows([]Row{{ID: "a"}})
	if tb.Cursor != 0 {
		t.Errorf("cursor = %d", tb.Cursor)
	}
	tb.SetRows(nil)
	if _, ok := tb.Selected(); ok {
		t.Error("빈 테이블에서 선택이 있으면 안 됨")
	}
}

func TestTableFitsWidth(t *testing.T) {
	tb := &Table{Columns: []Column{{Title: "NAME"}, {Title: "MESSAGE"}}}
	tb.SetRows([]Row{{ID: "1", Cells: []string{"pod-1", strings.Repeat("긴메시지", 40)}}})
	out := tb.View(60, 5, lipgloss.NewStyle(), lipgloss.NewStyle(), "")
	for _, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > 60 {
			t.Errorf("줄 폭 %d > 60: %q", w, l)
		}
	}
}

func TestTableScrollsToCursor(t *testing.T) {
	tb := &Table{Columns: []Column{{Title: "N"}}}
	var rows []Row
	for i := 0; i < 50; i++ {
		rows = append(rows, Row{ID: string(rune('A' + i)), Cells: []string{string(rune('A' + i))}})
	}
	tb.SetRows(rows)
	tb.Bottom()
	out := tb.View(20, 6, lipgloss.NewStyle(), lipgloss.NewStyle(), "")
	if !strings.Contains(out, string(rune('A'+49))) {
		t.Error("마지막 행이 보여야 함")
	}
}

func TestTextViewSearchAndFollow(t *testing.T) {
	v := &TextView{MaxLines: 100}
	v.SetText("alpha\nbeta\ngamma\nbeta again")
	if !v.Search("BETA") {
		t.Fatal("대소문자 무시 검색 실패")
	}
	if i, n := v.MatchInfo(); i != 1 || n != 2 {
		t.Errorf("검색 결과 %d/%d", i, n)
	}
	v.NextMatch(1)
	if i, _ := v.MatchInfo(); i != 2 {
		t.Error("다음 결과 이동 실패")
	}
	v.Follow = true
	for i := 0; i < 200; i++ {
		v.Append("line")
	}
	if v.Lines() != 100 {
		t.Errorf("MaxLines 초과 보관: %d", v.Lines())
	}
	v.View(20, 10, lipgloss.NewStyle())
	if v.Percent() != 100 {
		t.Errorf("follow 중에는 끝에 있어야 함: %d%%", v.Percent())
	}
	v.Up(5)
	if v.Follow {
		t.Error("위로 스크롤하면 follow가 해제되어야 함")
	}
}
