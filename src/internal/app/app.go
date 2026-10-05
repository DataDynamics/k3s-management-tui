// Package app은 루트 Bubble Tea 모델입니다: 탭·페이지 스택, 전역 키, 다이얼로그, 작업 실행 절차.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/views"
)

type tabState struct {
	def    views.TabDef
	stack  []views.Page
	inited bool
}

func (t *tabState) top() views.Page { return t.stack[len(t.stack)-1] }

// Model은 루트 모델입니다.
type Model struct {
	env    *views.Env
	tabs   []*tabState
	active int

	width, height int

	dialog   *components.Dialog
	cmdMode  bool
	cmdInput textinput.Model

	toast    string
	toastErr bool
	toastAt  time.Time
	running  int

	metricsBusy bool
	hostBusy    bool
	hostAt      time.Time
	slowAt      time.Time
	nodeName    string
}

// New는 루트 모델을 만듭니다.
func New(env *views.Env) *Model {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.SetWidth(50)
	m := &Model{env: env, cmdInput: ti}
	for _, d := range views.BuildTabs(env) {
		m.tabs = append(m.tabs, &tabState{def: d, stack: []views.Page{d.Root}})
	}
	for i, t := range m.tabs {
		if t.def.Key == strings.ToLower(env.Cfg.UI.DefaultView) {
			m.active = i
		}
	}
	m.nodeName, _ = os.Hostname()
	return m
}

type metricsDoneMsg struct{}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.env.Cfg.UI.RefreshInterval, func(t time.Time) tea.Msg { return views.TickMsg(t) })
}

// watchStore는 Informer 변경을 기다렸다가 100ms 동안 모은 뒤 한 번만 알립니다 (설계 7.2-2).
func (m *Model) watchStore() tea.Cmd {
	if m.env.Store == nil {
		return nil
	}
	ch := m.env.Store.Changed()
	return func() tea.Msg {
		<-ch
		time.Sleep(100 * time.Millisecond)
		select {
		case <-ch:
		default:
		}
		return views.StoreChangedMsg{}
	}
}

func (m *Model) pollMetrics() tea.Cmd {
	if m.env.Kube == nil || m.metricsBusy {
		return nil
	}
	m.metricsBusy = true
	env := m.env
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = env.Metrics.Poll(c, env.Kube)
		return metricsDoneMsg{}
	}
}

// pollHost는 호스트 상태를 읽습니다. 버전·인증서는 1분에 한 번만 읽습니다.
func (m *Model) pollHost(force bool) tea.Cmd {
	now := time.Now()
	if m.hostBusy || (!force && now.Sub(m.hostAt) < 5*time.Second) {
		return nil
	}
	m.hostBusy, m.hostAt = true, now
	slow := force || now.Sub(m.slowAt) > time.Minute
	if slow {
		m.slowAt = now
	}
	env := m.env
	prev := env.HostState
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		hs := views.HostStatus{Version: prev.Version, Certs: prev.Certs, Updated: time.Now()}
		hs.Service, hs.Err = env.Host.ServiceStatus(c)
		hs.Datastore = env.Host.Datastore()
		hs.Disk = env.Host.DiskUsage()
		if slow {
			if v, err := env.Host.Version(c); err == nil {
				hs.Version = v
			}
			if certs, err := env.Host.Certificates(); err == nil {
				hs.Certs = certs
			}
		}
		return views.HostStatusMsg(hs)
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.tick(), m.watchStore(), m.pollMetrics(), m.pollHost(true), m.activateTab(m.active))
}

func (m *Model) activateTab(i int) tea.Cmd {
	m.active = i
	t := m.tabs[i]
	if !t.inited {
		t.inited = true
		return t.top().Init()
	}
	return m.updatePage(views.RefreshMsg{})
}

func (m *Model) page() views.Page { return m.tabs[m.active].top() }

// updatePage는 현재 페이지에 메시지를 전달합니다.
func (m *Model) updatePage(msg tea.Msg) tea.Cmd {
	t := m.tabs[m.active]
	p, cmd := t.top().Update(msg)
	t.stack[len(t.stack)-1] = p
	return cmd
}

// deliver는 특정 페이지가 대상인 메시지를 찾아 전달합니다 (탭을 옮긴 뒤 도착한 결과 포함).
func (m *Model) deliver(target views.Page, msg tea.Msg) tea.Cmd {
	for _, t := range m.tabs {
		for i, p := range t.stack {
			if p == target {
				np, cmd := p.Update(msg)
				t.stack[i] = np
				return cmd
			}
		}
	}
	return nil // 이미 닫힌 페이지
}

func (m *Model) setToast(text string, isErr bool) {
	m.toast, m.toastErr, m.toastAt = text, isErr, time.Now()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case views.Targeted:
		return m, m.deliver(msg.TargetPage(), msg)

	case views.TickMsg:
		return m, tea.Batch(m.tick(), m.pollMetrics(), m.pollHost(false), m.updatePage(msg))

	case views.StoreChangedMsg:
		return m, tea.Batch(m.watchStore(), m.updatePage(msg))

	case metricsDoneMsg:
		m.metricsBusy = false
		return m, nil

	case views.HostStatusMsg:
		m.hostBusy = false
		m.env.HostState = views.HostStatus(msg)
		return m, nil

	case views.PushPageMsg:
		t := m.tabs[m.active]
		t.stack = append(t.stack, msg.Page)
		return m, msg.Page.Init()

	case views.PopPageMsg:
		return m, m.pop()

	case views.ToastMsg:
		m.setToast(msg.Text, msg.Err)
		return m, nil

	case views.DialogMsg:
		m.dialog = msg.Dialog
		return m, nil

	case views.ActionRequestMsg:
		return m, m.requestAction(msg)

	case actionExecMsg:
		return m, m.execAction(msg)

	case views.ActionDoneMsg:
		return m, m.actionDone(msg)

	case setNamespaceMsg:
		return m, m.setNamespace(string(msg))

	case views.JumpMsg:
		return m, m.jump(msg.Key)

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	// textinput 커서 깜박임 등
	if m.dialog != nil {
		_, cmd := m.dialog.Update(msg)
		return m, cmd
	}
	if m.cmdMode {
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
		return m, cmd
	}
	return m, m.updatePage(msg)
}

func (m *Model) pop() tea.Cmd {
	t := m.tabs[m.active]
	if len(t.stack) <= 1 {
		return nil
	}
	t.top().Close()
	t.stack = t.stack[:len(t.stack)-1]
	return m.updatePage(views.RefreshMsg{})
}

func (m *Model) quit() tea.Cmd {
	for _, t := range m.tabs {
		for _, p := range t.stack {
			p.Close()
		}
	}
	if m.env.PF != nil {
		m.env.PF.StopAll()
	}
	return tea.Quit
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	kb := m.env.Cfg.Keys
	if m.dialog != nil {
		closed, cmd := m.dialog.Update(msg)
		if closed {
			m.dialog = nil
		}
		return cmd
	}
	if m.cmdMode {
		switch k {
		case "enter":
			m.cmdMode = false
			line := strings.TrimSpace(m.cmdInput.Value())
			m.cmdInput.Blur()
			return m.runCommand(line)
		case "esc", "ctrl+c":
			m.cmdMode = false
			m.cmdInput.Blur()
			return nil
		}
		var cmd tea.Cmd
		m.cmdInput, cmd = m.cmdInput.Update(msg)
		return cmd
	}
	if k == "ctrl+c" {
		return m.quit()
	}
	p := m.page()
	if p.InputActive() {
		return m.updatePage(msg)
	}
	_, isText := p.(*views.TextPage)
	depth := len(m.tabs[m.active].stack)
	switch {
	case kb.Is(config.KeyQuit, k):
		if depth > 1 {
			return m.pop()
		}
		return m.quit()
	case kb.Is(config.KeyHelp, k):
		return views.Push(views.NewHelpPage(m.env, p))
	case kb.Is(config.KeyCommand, k):
		m.cmdMode = true
		m.cmdInput.SetValue("")
		return m.cmdInput.Focus()
	case kb.Is(config.KeyNamespace, k) && !isText:
		return m.namespacePicker()
	case len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'1') < len(m.tabs):
		return m.activateTab(int(k[0] - '1'))
	}
	return m.updatePage(msg)
}

func (m *Model) namespacePicker() tea.Cmd {
	if m.env.Store == nil {
		m.setToast("클러스터에 연결되지 않았습니다", true)
		return nil
	}
	items := []string{"all"}
	for _, u := range m.env.Store.List(kube.GVRNamespaces, "") {
		items = append(items, u.GetName())
	}
	cur := m.env.Namespace
	if cur == "" {
		cur = "all"
	}
	m.dialog = components.NewPicker("네임스페이스 선택", items, cur, func(v string) tea.Cmd {
		return func() tea.Msg { return setNamespaceMsg(v) }
	})
	return nil
}

type setNamespaceMsg string

func (m *Model) setNamespace(ns string) tea.Cmd {
	if ns == "all" || ns == "*" {
		ns = ""
	}
	m.env.Namespace = ns
	var cmds []tea.Cmd
	// 모든 탭의 페이지가 다음에 보일 때 새 네임스페이스로 다시 읽도록 현재 탭부터 갱신합니다.
	cmds = append(cmds, m.updatePage(views.NamespaceChangedMsg{}))
	return tea.Batch(cmds...)
}

// runCommand는 명령 모드 입력을 처리합니다.
func (m *Model) runCommand(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	f := strings.Fields(line)
	switch strings.ToLower(f[0]) {
	case "q", "q!", "quit", "exit":
		return m.quit()
	case "help", "h":
		return views.Push(views.NewHelpPage(m.env, m.page()))
	case "ns", "namespace":
		if len(f) == 1 {
			return m.namespacePicker()
		}
		return m.setNamespace(f[1])
	case "journal", "journalctl":
		for _, t := range m.tabs {
			if t.def.Key == "host" {
				if tp, ok := t.def.Root.(*views.TablePage); ok {
					for _, a := range tp.AllActions() {
						if a.ID == "journal" {
							return a.Open(m.env, views.Row{}, "")
						}
					}
				}
			}
		}
	}
	return m.jump(f[0])
}

// jump는 탭 이름 또는 소스 키로 이동합니다.
func (m *Model) jump(key string) tea.Cmd {
	key = strings.ToLower(key)
	for i, t := range m.tabs {
		if t.def.Key == key {
			return m.activateTab(i)
		}
	}
	if d := kube.Def(key); d != nil {
		key = d.Key
	}
	for i, t := range m.tabs {
		tp, ok := t.def.Root.(*views.TablePage)
		if !ok || !tp.HasSource(key) {
			continue
		}
		for len(t.stack) > 1 {
			t.top().Close()
			t.stack = t.stack[:len(t.stack)-1]
		}
		tp.SelectSource(key)
		t.inited = true
		m.active = i
		return tp.Init()
	}
	if src := views.NewResourceSource(key); src != nil {
		if m.env.Kube != nil && !m.env.Kube.Has(src.Def.GVR) {
			m.setToast("서버에 없는 리소스입니다: "+key, true)
			return nil
		}
		return views.Push(views.NewTablePage(m.env, src.Title(), src))
	}
	m.setToast("알 수 없는 명령: "+key+"  (? 도움말)", true)
	return nil
}

// ---- 작업 실행 절차 (설계 7.2-5) ----
// read_only 확인 → (선택/입력) → 확인 다이얼로그 → 실행 → 감사 기록 → 결과 알림

type actionExecMsg struct {
	req   views.ActionRequestMsg
	input string
}

func (m *Model) requestAction(req views.ActionRequestMsg) tea.Cmd {
	a, env := req.Action, m.env
	if a.Mutating && env.ReadOnly {
		m.setToast("읽기 전용 모드입니다: "+a.Label+" 실행 불가", true)
		return nil
	}
	if a.NeedRoot && !env.Host.IsRoot() {
		m.setToast(a.Label+": root 권한이 필요합니다", true)
		return nil
	}
	if a.Choices != nil {
		list := a.Choices(env, req.Row)
		switch len(list) {
		case 0:
			m.setToast(a.Label+": 선택할 항목이 없습니다", true)
			return nil
		case 1:
			return m.promptStage(req, list[0])
		default:
			m.dialog = components.NewPicker(a.Label+" — "+req.Row.Name, list, list[0], func(v string) tea.Cmd {
				return m.promptStage(req, v)
			})
			return nil
		}
	}
	return m.promptStage(req, "")
}

func (m *Model) promptStage(req views.ActionRequestMsg, input string) tea.Cmd {
	a := req.Action
	if a.Prompt == nil {
		return m.confirmStage(req, input)
	}
	title, initial := a.Prompt(m.env, req.Row)
	d := components.NewInput(title, "", initial, func(v string) tea.Cmd {
		if v == "" {
			return views.Toast("입력이 비어 있어 취소했습니다", true)
		}
		return m.confirmStage(req, v)
	})
	return func() tea.Msg { return views.DialogMsg{Dialog: d} }
}

func (m *Model) confirmStage(req views.ActionRequestMsg, input string) tea.Cmd {
	a, env, row := req.Action, m.env, req.Row
	kind := a.Confirm
	if kind != views.ConfirmNone && a.Mutating && (env.Cfg.IsProtected(row.Namespace) || (req.Source == "namespaces" && env.Cfg.IsProtected(row.Name))) {
		kind = views.ConfirmType
	}
	if kind == views.ConfirmYesNo && !env.Cfg.Safety.ConfirmDestructive {
		kind = views.ConfirmNone
	}
	exec := func(string) tea.Cmd {
		return func() tea.Msg { return actionExecMsg{req: req, input: input} }
	}
	body := a.Label + ": " + views.Target(req.Source, row)
	if a.ConfirmBody != nil {
		body = a.ConfirmBody(env, row)
	}
	if input != "" && a.Prompt != nil {
		body += "\n\n입력값: " + input
	}
	var d *components.Dialog
	switch kind {
	case views.ConfirmNone:
		return exec("")
	case views.ConfirmYesNo:
		d = components.NewConfirm(a.Label+" 확인", body, exec)
	case views.ConfirmType:
		expect := row.Name
		if a.Expect != nil {
			expect = a.Expect(row)
		}
		d = components.NewConfirmType(a.Label+" 확인", body, expect, exec)
	}
	return func() tea.Msg { return views.DialogMsg{Dialog: d} }
}

func (m *Model) execAction(x actionExecMsg) tea.Cmd {
	a, env, row := x.req.Action, m.env, x.req.Row
	target := views.Target(x.req.Source, row)
	if a.Open != nil {
		return a.Open(env, row, x.input)
	}
	if a.Do == nil {
		return nil
	}
	m.running++
	m.setToast("실행 중: "+a.Label+" "+target+" …", false)
	detail := ""
	if x.input != "" {
		detail = "input=" + x.input
	}
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("action panic", "action", a.ID, "panic", r, "stack", string(debug.Stack()))
				msg = views.ActionDoneMsg{Action: a, Target: target, Detail: detail, Err: fmt.Errorf("내부 오류: %v", r), Mutating: a.Mutating}
			}
		}()
		info, err := a.Do(env, row, x.input)
		return views.ActionDoneMsg{Action: a, Target: target, Detail: detail, Info: info, Err: err, Mutating: a.Mutating}
	}
}

func (m *Model) actionDone(d views.ActionDoneMsg) tea.Cmd {
	if m.running > 0 && d.Action != nil && d.Action.Do != nil {
		m.running--
	}
	id := ""
	if d.Action != nil {
		id = d.Action.ID
	}
	if d.Mutating {
		m.env.Audit.Record(id, d.Target, d.Detail, d.Err)
	}
	if d.Err != nil {
		m.setToast("실패: "+firstLine(d.Err.Error()), true)
	} else if d.Info != "" {
		m.setToast(d.Info, false)
	}
	return tea.Batch(m.updatePage(views.RefreshMsg{}), m.pollHost(true))
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// ---- 화면 ----

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "k3stui"
	return v
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return "로딩 중…"
	}
	st := m.env.Styles
	header := m.headerLine()
	tabs := m.tabLine()
	status := m.statusLine()
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(tabs) - lipgloss.Height(status)
	var body string
	if m.dialog != nil {
		body = m.dialog.View(m.width, bodyH, st)
	} else {
		body = m.page().View(m.width, bodyH)
	}
	// 본문 높이 고정 (짧으면 채우고 길면 자릅니다)
	lines := strings.Split(body, "\n")
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > m.width {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
	}
	return header + "\n" + tabs + "\n" + strings.Join(lines, "\n") + "\n" + status
}

func (m *Model) headerLine() string {
	st := m.env.Styles
	env := m.env
	ver := "연결 안 됨"
	if env.Kube != nil {
		ver = env.Kube.ServerVersion
	}
	svc := env.HostState.Service
	svcTxt := "k3s ?"
	switch {
	case env.HostState.Updated.IsZero():
	case env.HostState.Err != nil:
		svcTxt = env.Host.ServiceName() + " ✕"
	case svc.Active():
		svcTxt = svc.Unit + " ● " + svc.ActiveState
	default:
		svcTxt = svc.Unit + " ○ " + svc.ActiveState
	}
	ns := env.Namespace
	if ns == "" {
		ns = "all"
	}
	parts := []string{" K3S " + ver, m.nodeName, svcTxt, "ns: " + ns}
	if env.ReadOnly {
		parts = append(parts, "READ-ONLY")
	}
	if !env.Host.IsRoot() {
		parts = append(parts, "non-root")
	}
	if m.running > 0 {
		parts = append(parts, fmt.Sprintf("작업 %d개 실행 중", m.running))
	}
	left := strings.Join(parts, " │ ")
	right := time.Now().Format("15:04:05") + " "
	gap := max(1, m.width-ansi.StringWidth(left)-ansi.StringWidth(right))
	line := left + strings.Repeat(" ", gap) + right
	return st.Header.Width(m.width).Render(ansi.Truncate(line, m.width, ""))
}

func (m *Model) tabLine() string {
	st := m.env.Styles
	var parts []string
	for i, t := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.def.Name)
		if i == m.active {
			parts = append(parts, st.TabActive.Render(label))
		} else {
			parts = append(parts, st.TabInactive.Render(label))
		}
	}
	line := strings.Join(parts, " ")
	// 페이지 경로 (스택)
	t := m.tabs[m.active]
	if len(t.stack) > 1 {
		var crumbs []string
		for _, p := range t.stack[1:] {
			crumbs = append(crumbs, p.Title())
		}
		line += st.Muted.Render("  › " + strings.Join(crumbs, " › "))
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m *Model) statusLine() string {
	st := m.env.Styles
	if m.cmdMode {
		return m.cmdInput.View()
	}
	age := time.Since(m.toastAt)
	if m.toast != "" && (age < 5*time.Second || (m.toastErr && age < 10*time.Second) || m.running > 0) {
		if m.toastErr {
			return st.Err.Render("✕ " + ansi.Truncate(m.toast, m.width-2, "…"))
		}
		return st.OK.Render("✓ " + ansi.Truncate(m.toast, m.width-2, "…"))
	}
	kb := m.env.Cfg.Keys
	hint := func(k, d string) string { return st.Key.Render(k) + " " + st.KeyDesc.Render(d) }
	var parts []string
	for _, h := range m.page().Hints() {
		parts = append(parts, hint(h.Key, h.Desc))
	}
	var global []string
	if _, isText := m.page().(*views.TextPage); !isText {
		global = append(global, hint(kb.First(config.KeyFilter), "필터"), hint(kb.First(config.KeyNamespace), "ns"))
	}
	global = append(global, hint(kb.First(config.KeyCommand), "명령"), hint(kb.First(config.KeyHelp), "도움말"))
	if len(m.tabs[m.active].stack) > 1 {
		global = append(global, hint(kb.First(config.KeyQuit), "뒤로"))
	} else {
		global = append(global, hint(kb.First(config.KeyQuit), "종료"))
	}
	line := strings.Join(append(parts, global...), "  ")
	if ansi.StringWidth(line) > m.width {
		line = ansi.Truncate(line, m.width, "…")
	}
	return line
}
