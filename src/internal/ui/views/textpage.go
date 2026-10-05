package views

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
)

// TextPage는 텍스트(describe, YAML, 로그 스트림 등)를 보여주는 화면입니다.
type TextPage struct {
	env   *Env
	title string
	view  components.TextView

	loader func() (string, error) // 정적 내용 (ctrl+r로 다시 읽기)
	stream func(ctx context.Context) (<-chan string, error)

	ch      <-chan string
	cancel  context.CancelFunc
	ended   bool
	loading bool
	err     error

	search    textinput.Model
	searching bool

	// Extra는 페이지 고유 키(예: Secret 값 표시)입니다.
	Extra []PageKey
}

// PageKey는 TextPage 고유 단축키입니다.
type PageKey struct {
	Key  string
	Desc string
	Run  func(p *TextPage) tea.Cmd
}

// NewTextPage는 loader로 내용을 읽는 정적 텍스트 화면입니다.
func NewTextPage(env *Env, title string, loader func() (string, error)) *TextPage {
	p := newTextPage(env, title)
	p.loader = loader
	return p
}

// NewStreamPage는 줄 단위 스트림(로그 follow)을 보여주는 화면입니다.
func NewStreamPage(env *Env, title string, stream func(ctx context.Context) (<-chan string, error)) *TextPage {
	p := newTextPage(env, title)
	p.stream = stream
	p.view.Follow = true
	p.view.MaxLines = env.Cfg.UI.MaxLogLines
	return p
}

func newTextPage(env *Env, title string) *TextPage {
	ti := textinput.New()
	ti.Prompt = "검색: "
	ti.SetWidth(40)
	return &TextPage{env: env, title: title, search: ti}
}

// SetStyler는 줄 단위 색상 함수를 지정합니다.
func (p *TextPage) SetStyler(f func(string) string) *TextPage { p.view.Styler = f; return p }

// SetWrap은 줄바꿈 기본값을 지정합니다.
func (p *TextPage) SetWrap(w bool) *TextPage { p.view.Wrap = w; return p }

// SetText는 내용을 직접 바꿉니다.
func (p *TextPage) SetText(s string) { p.view.SetText(s) }

type textLoadedMsg struct {
	page *TextPage
	text string
	err  error
}

func (m textLoadedMsg) TargetPage() Page { return m.page }

type streamLinesMsg struct {
	page  *TextPage
	lines []string
	ch    <-chan string
}

func (m streamLinesMsg) TargetPage() Page { return m.page }

type streamStartedMsg struct {
	page   *TextPage
	ch     <-chan string
	cancel context.CancelFunc
	err    error
}

func (m streamStartedMsg) TargetPage() Page { return m.page }

func (p *TextPage) Init() tea.Cmd {
	if p.stream != nil {
		return p.startStream()
	}
	return p.reload()
}

func (p *TextPage) reload() tea.Cmd {
	if p.loader == nil {
		return nil
	}
	p.loading = true
	return func() tea.Msg {
		text, err := p.loader()
		return textLoadedMsg{page: p, text: text, err: err}
	}
}

func (p *TextPage) startStream() tea.Cmd {
	if p.cancel != nil {
		p.cancel()
	}
	p.ended, p.err = false, nil
	p.view.SetText("")
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := p.stream(ctx)
		if err != nil {
			cancel()
		}
		return streamStartedMsg{page: p, ch: ch, cancel: cancel, err: err}
	}
}

// waitLines는 스트림에서 줄을 모아 하나의 메시지로 보냅니다 (화면 갱신 횟수를 줄이기 위해).
func waitLines(p *TextPage, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return streamLinesMsg{page: p, ch: ch, lines: nil}
		}
		lines := []string{line}
		for len(lines) < 1000 {
			select {
			case l, ok := <-ch:
				if !ok {
					return streamLinesMsg{page: p, ch: ch, lines: lines}
				}
				lines = append(lines, l)
			default:
				return streamLinesMsg{page: p, ch: ch, lines: lines}
			}
		}
		return streamLinesMsg{page: p, ch: ch, lines: lines}
	}
}

func (p *TextPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	keys := p.env.Cfg.Keys
	switch msg := msg.(type) {
	case textLoadedMsg:
		p.loading = false
		p.err = msg.err
		if msg.err == nil || msg.text != "" {
			p.view.SetText(msg.text)
		}
		return p, nil
	case streamStartedMsg:
		if msg.err != nil {
			p.err = msg.err
			p.ended = true
			return p, nil
		}
		p.ch, p.cancel = msg.ch, msg.cancel
		return p, waitLines(p, msg.ch)
	case streamLinesMsg:
		if msg.ch != p.ch {
			return p, nil // 재시작 전 스트림
		}
		if msg.lines == nil {
			p.ended = true
			return p, nil
		}
		p.view.Append(msg.lines...)
		return p, waitLines(p, msg.ch)
	case tea.KeyPressMsg:
		k := msg.String()
		if p.searching {
			switch k {
			case "enter":
				p.searching = false
				p.search.Blur()
				if q := strings.TrimSpace(p.search.Value()); q != "" && !p.view.Search(q) {
					return p, Toast("검색 결과 없음: "+q, true)
				}
			case "esc":
				p.searching = false
				p.search.Blur()
			default:
				var cmd tea.Cmd
				p.search, cmd = p.search.Update(msg)
				return p, cmd
			}
			return p, nil
		}
		for _, x := range p.Extra {
			if x.Key == k {
				return p, x.Run(p)
			}
		}
		switch {
		case keys.Is(config.KeyBack, k) && p.view.Query() != "":
			p.view.Search("")
		case keys.Is(config.KeyBack, k):
			return p, func() tea.Msg { return PopPageMsg{} }
		case keys.Is(config.KeyUp, k):
			p.view.Up(1)
		case keys.Is(config.KeyDown, k):
			p.view.Down(1)
		case keys.Is(config.KeyPageUp, k):
			p.view.Up(p.view.PageSize())
		case keys.Is(config.KeyPageDown, k), k == "space":
			p.view.Down(p.view.PageSize())
		case keys.Is(config.KeyTop, k):
			p.view.Top()
		case keys.Is(config.KeyBottom, k):
			p.view.Bottom()
			if p.stream != nil {
				p.view.Follow = true
			}
		case keys.Is(config.KeyLeft, k):
			p.view.Left()
		case keys.Is(config.KeyRight, k):
			p.view.Right()
		case keys.Is(config.KeyFilter, k):
			p.searching = true
			p.search.SetValue("")
			return p, p.search.Focus()
		case k == "n":
			p.view.NextMatch(1)
		case k == "N":
			p.view.NextMatch(-1)
		case k == "w":
			p.view.Wrap = !p.view.Wrap
		case k == "f" && p.stream != nil:
			p.view.Follow = !p.view.Follow
			if p.view.Follow {
				p.view.Bottom()
			}
		case keys.Is(config.KeyRefresh, k):
			if p.stream != nil {
				return p, p.startStream()
			}
			return p, p.reload()
		}
	}
	return p, nil
}

func (p *TextPage) View(width, height int) string {
	st := p.env.Styles
	status := st.Bold.Render(p.title)
	if p.stream != nil {
		if p.view.Follow {
			status += st.OK.Render("  ● follow")
		} else {
			status += st.Muted.Render("  ○ follow 해제")
		}
		if p.ended {
			status += st.Muted.Render("  (스트림 종료 — ctrl+r 다시 연결)")
		}
	}
	if p.view.Wrap {
		status += st.Muted.Render("  [wrap]")
	}
	if q := p.view.Query(); q != "" {
		i, n := p.view.MatchInfo()
		status += st.Accent.Render(fmt.Sprintf("  \"%s\" %d/%d", q, i, n))
	}
	status += st.Muted.Render(fmt.Sprintf("  %d줄 %d%%", p.view.Lines(), p.view.Percent()))
	if p.loading {
		status += st.Info.Render("  불러오는 중…")
	}
	head := status
	if p.searching {
		head += "\n" + p.search.View()
	}
	if p.err != nil {
		head += "\n" + st.Err.Render("오류: "+p.err.Error())
	}
	bodyH := height - strings.Count(head, "\n") - 1
	return head + "\n" + p.view.View(width, max(1, bodyH), st.Match)
}

func (p *TextPage) Title() string { return p.title }

func (p *TextPage) Hints() []Hint {
	h := []Hint{{"/", "검색"}, {"n/N", "다음/이전"}, {"w", "줄바꿈"}}
	if p.stream != nil {
		h = append(h, Hint{"f", "follow"})
	}
	for _, x := range p.Extra {
		h = append(h, Hint{x.Key, x.Desc})
	}
	return append(h, Hint{"ctrl+r", "새로고침"}, Hint{"esc", "뒤로"})
}

func (p *TextPage) InputActive() bool { return p.searching }

func (p *TextPage) Close() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}
