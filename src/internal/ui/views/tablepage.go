package views

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
)

// TablePage는 하나 이상의 소스를 하위 탭으로 보여주는 테이블 화면입니다.
type TablePage struct {
	env     *Env
	title   string
	sources []Source
	idx     int

	table    components.Table
	rows     []Row // 필터 전 전체
	err      error
	loading  bool
	loadedAt time.Time
	summary  string
	gen      int // 소스 전환 후 늦게 도착한 결과를 버리기 위한 세대 번호

	filter    textinput.Model
	filtering bool
}

// NewTablePage는 테이블 화면을 만듭니다.
func NewTablePage(env *Env, title string, sources ...Source) *TablePage {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.SetWidth(40)
	return &TablePage{env: env, title: title, sources: sources, filter: ti}
}

type sourceDataMsg struct {
	page *TablePage
	gen  int
	rows []Row
	err  error
}

func (m sourceDataMsg) TargetPage() Page { return m.page }

func (p *TablePage) source() Source { return p.sources[p.idx] }

// SelectSource는 키로 하위 탭을 선택합니다.
func (p *TablePage) SelectSource(key string) bool {
	for i, s := range p.sources {
		if s.Key() == key {
			p.switchTo(i)
			return true
		}
	}
	return false
}

// HasSource는 키에 해당하는 하위 탭이 있는지 알려줍니다.
func (p *TablePage) HasSource(key string) bool {
	for _, s := range p.sources {
		if s.Key() == key {
			return true
		}
	}
	return false
}

func (p *TablePage) switchTo(i int) {
	p.idx = (i + len(p.sources)) % len(p.sources)
	p.gen++
	// 이전 소스의 로딩 결과는 세대 번호가 달라 버려지므로, 로딩 중 표시를 여기서 풀어야 새 소스를 읽을 수 있습니다.
	p.loading = false
	p.rows, p.err = nil, nil
	p.table = components.Table{}
	p.filter.SetValue("")
	p.filtering = false
	p.loadedAt = time.Time{}
}

func (p *TablePage) Init() tea.Cmd { return p.load() }

// load는 동기 소스면 즉시 읽고, 비동기 소스면 백그라운드 명령을 돌려줍니다.
func (p *TablePage) load() tea.Cmd {
	src := p.source()
	if !src.Async() {
		rows, err := src.Load(p.env)
		p.setRows(rows, err)
		return nil
	}
	if p.loading {
		return nil
	}
	p.loading = true
	gen := p.gen
	env := p.env
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = sourceDataMsg{page: p, gen: gen, err: fmt.Errorf("내부 오류: %v", r)}
			}
		}()
		rows, err := src.Load(env)
		return sourceDataMsg{page: p, gen: gen, rows: rows, err: err}
	}
}

func (p *TablePage) setRows(rows []Row, err error) {
	p.rows, p.err = rows, err
	p.loadedAt = p.env.NowTime()
	p.summary = ""
	if sm, ok := p.source().(summarizer); ok {
		p.summary = sm.Summary(p.env)
	}
	p.applyFilter()
}

func (p *TablePage) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	cols := p.columns()
	out := make([]components.Row, 0, len(p.rows))
	showNS := p.showNamespace()
	for _, r := range p.rows {
		cells := r.Cells
		if showNS {
			cells = append([]string{r.Namespace}, cells...)
		}
		if q != "" && !matchRow(cells, q) {
			continue
		}
		out = append(out, components.Row{ID: r.ID, Cells: cells, Style: p.env.LevelStyle(r.Level)})
	}
	p.table.Columns = cols
	p.table.SetRows(out)
}

// matchRow는 "!"로 시작하면 제외 조건, 아니면 셀 중 하나라도 포함하면 일치로 봅니다.
func matchRow(cells []string, q string) bool {
	neg := strings.HasPrefix(q, "!")
	q = strings.TrimPrefix(q, "!")
	hit := false
	for _, c := range cells {
		if strings.Contains(strings.ToLower(c), q) {
			hit = true
			break
		}
	}
	return hit != neg
}

func (p *TablePage) showNamespace() bool {
	return p.source().Namespaced() && p.env.Namespace == ""
}

func (p *TablePage) columns() []components.Column {
	var out []components.Column
	if p.showNamespace() {
		out = append(out, components.Column{Title: "NAMESPACE"})
	}
	for _, c := range p.source().Columns(p.env) {
		out = append(out, components.Column{Title: c.Name, MaxWidth: c.MaxWidth})
	}
	return out
}

// selectedRow는 테이블에서 선택된 원본 행입니다.
func (p *TablePage) selectedRow() (Row, bool) {
	sel, ok := p.table.Selected()
	if !ok {
		return Row{}, false
	}
	for _, r := range p.rows {
		if r.ID == sel.ID {
			return r, true
		}
	}
	return Row{}, false
}

// actionFor는 키에 해당하는 작업을 찾습니다.
func (p *TablePage) actionFor(key string, row Row, hasRow bool) *Action {
	for _, a := range p.source().Actions() {
		for _, k := range p.env.Cfg.Keys.ActionKeys(a.ID, a.Keys) {
			if k != key {
				continue
			}
			if !hasRow && !a.NoRow {
				continue
			}
			if a.Available != nil && hasRow && !a.Available(p.env, row) {
				continue
			}
			return a
		}
	}
	return nil
}

func (p *TablePage) Update(msg tea.Msg) (Page, tea.Cmd) {
	keys := p.env.Cfg.Keys
	switch msg := msg.(type) {
	case sourceDataMsg:
		if msg.gen == p.gen {
			p.loading = false
			p.setRows(msg.rows, msg.err)
		}
		return p, nil
	case StoreChangedMsg, NamespaceChangedMsg:
		if _, ns := msg.(NamespaceChangedMsg); ns && p.source().Async() {
			return p, p.load()
		}
		if !p.source().Async() {
			return p, p.load()
		}
		return p, nil
	case RefreshMsg:
		return p, p.load()
	case TickMsg:
		src := p.source()
		if !src.Async() {
			return p, p.load() // AGE·메트릭 컬럼 갱신
		}
		if iv := src.AutoRefresh(); iv > 0 && time.Since(p.loadedAt) >= iv {
			return p, p.load()
		}
		return p, nil
	case tea.KeyPressMsg:
		k := msg.String()
		if p.filtering {
			switch k {
			case "enter":
				p.filtering = false
				p.filter.Blur()
			case "esc":
				p.filtering = false
				p.filter.Blur()
				p.filter.SetValue("")
				p.applyFilter()
			default:
				var cmd tea.Cmd
				p.filter, cmd = p.filter.Update(msg)
				p.applyFilter()
				return p, cmd
			}
			return p, nil
		}
		switch {
		case keys.Is(config.KeyUp, k):
			p.table.Up(1)
		case keys.Is(config.KeyDown, k):
			p.table.Down(1)
		case keys.Is(config.KeyPageUp, k):
			p.table.Up(p.table.PageSize())
		case keys.Is(config.KeyPageDown, k):
			p.table.Down(p.table.PageSize())
		case keys.Is(config.KeyTop, k):
			p.table.Top()
		case keys.Is(config.KeyBottom, k):
			p.table.Bottom()
		case keys.Is(config.KeyFilter, k):
			p.filtering = true
			return p, p.filter.Focus()
		case keys.Is(config.KeyBack, k) && p.filter.Value() != "":
			p.filter.SetValue("")
			p.applyFilter()
		case keys.Is(config.KeyBack, k):
			return p, func() tea.Msg { return PopPageMsg{} }
		case keys.Is(config.KeyNextSource, k) && len(p.sources) > 1:
			p.switchTo(p.idx + 1)
			return p, p.load()
		case keys.Is(config.KeyPrevSource, k) && len(p.sources) > 1:
			p.switchTo(p.idx - 1)
			return p, p.load()
		case keys.Is(config.KeyRefresh, k):
			return p, p.load()
		default:
			row, ok := p.selectedRow()
			if a := p.actionFor(k, row, ok); a != nil {
				src := p.source().Key()
				return p, func() tea.Msg { return ActionRequestMsg{Action: a, Row: row, Source: src} }
			}
		}
	}
	return p, nil
}

func (p *TablePage) Title() string {
	if len(p.sources) == 1 && p.sources[0].Title() != p.title {
		return p.title + " › " + p.sources[0].Title()
	}
	return p.title
}

func (p *TablePage) InputActive() bool { return p.filtering }
func (p *TablePage) Close()            {}

// Hints는 현재 소스에서 쓸 수 있는 작업 키 안내입니다.
func (p *TablePage) Hints() []Hint {
	var out []Hint
	row, ok := p.selectedRow()
	seen := map[string]bool{}
	for _, a := range p.source().Actions() {
		if (!ok && !a.NoRow) || (ok && a.Available != nil && !a.Available(p.env, row)) {
			continue
		}
		ks := p.env.Cfg.Keys.ActionKeys(a.ID, a.Keys)
		if len(ks) == 0 || seen[ks[0]] {
			continue
		}
		seen[ks[0]] = true
		out = append(out, Hint{Key: ks[0], Desc: a.Label})
	}
	return out
}

// AllActions는 도움말 화면용 작업 목록입니다.
func (p *TablePage) AllActions() []*Action { return p.source().Actions() }

func (p *TablePage) View(width, height int) string {
	st := p.env.Styles
	var lines []string

	// 하위 탭 줄
	if len(p.sources) > 1 {
		var tabs []string
		for i, s := range p.sources {
			if i == p.idx {
				tabs = append(tabs, st.SubActive.Render(s.Title()))
			} else {
				tabs = append(tabs, st.SubInactive.Render(s.Title()))
			}
		}
		lines = append(lines, strings.Join(tabs, st.Muted.Render(" │ ")))
	}

	// 정보 줄: 네임스페이스, 개수, 필터
	cur, total := p.table.Position()
	info := st.Bold.Render(p.source().Title())
	if p.source().Namespaced() {
		ns := p.env.Namespace
		if ns == "" {
			ns = "all"
		}
		info += st.Muted.Render(" ns:") + st.Accent.Render(ns)
	}
	info += st.Muted.Render(fmt.Sprintf("  [%d/%d]", cur, total))
	if len(p.rows) != total {
		info += st.Muted.Render(fmt.Sprintf(" (전체 %d)", len(p.rows)))
	}
	if p.loading {
		info += st.Info.Render("  불러오는 중…")
	}
	if p.filtering {
		info += "  " + p.filter.View()
	} else if v := p.filter.Value(); v != "" {
		info += st.Accent.Render("  /"+v) + st.Muted.Render(" (Esc 해제)")
	}
	lines = append(lines, info)
	if p.summary != "" {
		lines = append(lines, st.Muted.Render(p.summary))
	}

	errLine := ""
	if p.err != nil {
		errLine = st.Err.Render("오류: " + firstLine(p.err.Error()))
	} else if r, ok := p.source().(interface{ StoreErr(*Env) string }); ok {
		if e := r.StoreErr(p.env); e != "" {
			errLine = st.Warn.Render("watch 오류: " + firstLine(e))
		}
	}
	if errLine != "" {
		lines = append(lines, errLine)
	}

	head := strings.Join(lines, "\n")
	bodyH := height - lipgloss.Height(head)
	empty := st.Muted.Render("  (항목 없음)")
	if p.loadedAt.IsZero() {
		empty = st.Muted.Render("  불러오는 중…")
	}
	body := p.table.View(width, bodyH, st.TableHeader, st.Selected, empty)
	return head + "\n" + body
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// Source는 키로 하위 소스를 찾습니다 (없으면 nil).
func (p *TablePage) Source(key string) Source {
	for _, s := range p.sources {
		if s.Key() == key {
			return s
		}
	}
	return nil
}
