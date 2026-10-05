package components

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DataDynamics/k3s-management-tui/internal/ui/styles"
)

// DialogKind는 다이얼로그 종류입니다.
type DialogKind int

const (
	DialogConfirm     DialogKind = iota // y/N
	DialogConfirmType                   // 이름 재입력
	DialogInput                         // 값 입력
	DialogPicker                        // 목록 선택 (필터 가능)
)

// Dialog는 모달 다이얼로그입니다. 결과는 OnOK 콜백으로 전달됩니다.
type Dialog struct {
	Kind   DialogKind
	Title  string
	Body   string // 설명 (여러 줄 가능, diff 등)
	Expect string // DialogConfirmType에서 입력해야 하는 값
	Items  []string
	OnOK   func(value string) tea.Cmd

	input   textinput.Model
	cursor  int
	errText string
	scroll  int
}

// NewConfirm은 y/N 확인 다이얼로그입니다.
func NewConfirm(title, body string, onOK func(string) tea.Cmd) *Dialog {
	return &Dialog{Kind: DialogConfirm, Title: title, Body: body, OnOK: onOK}
}

// NewConfirmType은 expect를 정확히 입력해야 진행하는 다이얼로그입니다.
func NewConfirmType(title, body, expect string, onOK func(string) tea.Cmd) *Dialog {
	d := &Dialog{Kind: DialogConfirmType, Title: title, Body: body, Expect: expect, OnOK: onOK}
	d.initInput("")
	d.input.Placeholder = expect
	return d
}

// NewInput은 값 입력 다이얼로그입니다.
func NewInput(title, body, initial string, onOK func(string) tea.Cmd) *Dialog {
	d := &Dialog{Kind: DialogInput, Title: title, Body: body, OnOK: onOK}
	d.initInput(initial)
	return d
}

// NewPicker는 목록 선택 다이얼로그입니다. 입력하면 목록이 걸러집니다.
func NewPicker(title string, items []string, current string, onOK func(string) tea.Cmd) *Dialog {
	d := &Dialog{Kind: DialogPicker, Title: title, Items: items, OnOK: onOK}
	d.initInput("")
	d.input.Placeholder = "필터"
	for i, it := range items {
		if it == current {
			d.cursor = i
		}
	}
	return d
}

func (d *Dialog) initInput(v string) {
	d.input = textinput.New()
	d.input.Prompt = "› "
	d.input.SetValue(v)
	d.input.CursorEnd()
	d.input.SetWidth(48)
	d.input.Focus()
}

func (d *Dialog) filtered() []string {
	q := strings.ToLower(strings.TrimSpace(d.input.Value()))
	if q == "" {
		return d.Items
	}
	var out []string
	for _, it := range d.Items {
		if strings.Contains(strings.ToLower(it), q) {
			out = append(out, it)
		}
	}
	return out
}

// Update는 키 입력을 처리합니다. closed가 true면 다이얼로그를 닫습니다.
func (d *Dialog) Update(msg tea.Msg) (closed bool, cmd tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if d.Kind != DialogConfirm {
			d.input, cmd = d.input.Update(msg)
		}
		return false, cmd
	}
	k := key.String()
	switch k {
	case "esc", "ctrl+c":
		return true, nil
	case "pgup":
		d.scroll = max(0, d.scroll-10)
		return false, nil
	case "pgdown":
		d.scroll += 10
		return false, nil
	}
	switch d.Kind {
	case DialogConfirm:
		switch k {
		case "y", "Y":
			return true, d.ok("")
		case "n", "N", "enter", "q":
			return true, nil
		case "up", "k":
			d.scroll = max(0, d.scroll-1)
		case "down", "j":
			d.scroll++
		}
		return false, nil
	case DialogConfirmType:
		if k == "enter" {
			if d.input.Value() != d.Expect {
				d.errText = "입력이 일치하지 않습니다"
				return false, nil
			}
			return true, d.ok(d.input.Value())
		}
	case DialogInput:
		if k == "enter" {
			return true, d.ok(strings.TrimSpace(d.input.Value()))
		}
	case DialogPicker:
		items := d.filtered()
		switch k {
		case "enter":
			if d.cursor >= 0 && d.cursor < len(items) {
				return true, d.ok(items[d.cursor])
			}
			return false, nil
		case "up", "ctrl+p":
			d.cursor = max(0, d.cursor-1)
			return false, nil
		case "down", "ctrl+n":
			d.cursor = min(len(items)-1, d.cursor+1)
			return false, nil
		}
	}
	d.errText = ""
	d.input, cmd = d.input.Update(msg)
	if d.Kind == DialogPicker {
		if n := len(d.filtered()); d.cursor >= n {
			d.cursor = max(0, n-1)
		}
	}
	return false, cmd
}

func (d *Dialog) ok(v string) tea.Cmd {
	if d.OnOK == nil {
		return nil
	}
	return d.OnOK(v)
}

// View는 화면 중앙에 다이얼로그를 그립니다.
func (d *Dialog) View(width, height int, st *styles.Styles) string {
	inner := min(max(50, width*2/3), width-8)
	var parts []string
	parts = append(parts, st.DialogTitle.Render(d.Title), "")
	if d.Body != "" {
		lines := strings.Split(strings.TrimRight(d.Body, "\n"), "\n")
		maxBody := max(3, height-16)
		if d.scroll > max(0, len(lines)-maxBody) {
			d.scroll = max(0, len(lines)-maxBody)
		}
		shown := lines[d.scroll:min(len(lines), d.scroll+maxBody)]
		for _, l := range shown {
			if ansi.StringWidth(l) > inner {
				l = ansi.Truncate(l, inner, "…")
			}
			switch {
			case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
				l = st.OK.Render(l)
			case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
				l = st.Err.Render(l)
			case strings.HasPrefix(l, "@@"):
				l = st.Info.Render(l)
			}
			parts = append(parts, l)
		}
		if len(lines) > maxBody {
			parts = append(parts, st.Muted.Render("… PgUp/PgDn 스크롤 ("+itoa(d.scroll+1)+"-"+itoa(min(len(lines), d.scroll+maxBody))+"/"+itoa(len(lines))+")"))
		}
		parts = append(parts, "")
	}
	switch d.Kind {
	case DialogConfirm:
		parts = append(parts, st.Key.Render("y")+st.KeyDesc.Render(" 실행   ")+st.Key.Render("n/Esc")+st.KeyDesc.Render(" 취소"))
	case DialogConfirmType:
		parts = append(parts, "계속하려면 "+st.Warn.Render(d.Expect)+" 을(를) 입력하세요:", d.input.View())
	case DialogInput:
		parts = append(parts, d.input.View())
	case DialogPicker:
		parts = append(parts, d.input.View(), "")
		items := d.filtered()
		maxItems := max(3, height-14)
		start := 0
		if d.cursor >= maxItems {
			start = d.cursor - maxItems + 1
		}
		for i := start; i < len(items) && i < start+maxItems; i++ {
			line := fit(items[i], inner)
			if i == d.cursor {
				line = st.Selected.Render(line)
			}
			parts = append(parts, line)
		}
		if len(items) == 0 {
			parts = append(parts, st.Muted.Render("(일치하는 항목 없음)"))
		}
	}
	if d.errText != "" {
		parts = append(parts, st.Err.Render(d.errText))
	}
	if d.Kind != DialogConfirm {
		parts = append(parts, "", st.Muted.Render("Enter 확인 · Esc 취소"))
	}
	box := st.Dialog.Width(inner + 6).Render(strings.Join(parts, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func itoa(i int) string { return strconv.Itoa(i) }
