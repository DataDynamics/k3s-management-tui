package views

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/k3s"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// Dashboard는 클러스터·노드·K3S 서비스 요약과 문제 Pod·경고 이벤트를 보여줍니다.
type Dashboard struct {
	env  *Env
	pods *ResourceSource

	cluster  []string
	nodes    []string
	problems []Row
	events   []string
	cursor   int
}

// NewDashboard는 대시보드 화면을 만듭니다.
func NewDashboard(env *Env) *Dashboard {
	return &Dashboard{env: env, pods: NewResourceSource("pods")}
}

func (d *Dashboard) Init() tea.Cmd {
	if s := d.env.Store; s != nil {
		for _, g := range []schema.GroupVersionResource{kube.GVRPods, kube.GVRNodes, kube.GVREvents,
			kube.GVRNamespaces, kube.GVRDeployments, kube.GVRServices, kube.GVRPVCs} {
			s.Ensure(g)
		}
	}
	d.refresh()
	return nil
}

func (d *Dashboard) refresh() {
	env := d.env
	st := env.Styles
	d.cluster, d.nodes, d.problems, d.events = nil, nil, nil, nil
	if env.Store == nil {
		msg := "클러스터에 연결되지 않았습니다"
		if env.KubeErr != nil {
			msg = env.KubeErr.Error()
		}
		d.cluster = []string{st.Err.Render(msg)}
		return
	}
	now := env.NowTime()
	s := env.Store
	pods := s.List(kube.GVRPods, env.Namespace)
	running, pending, failed, succeeded := 0, 0, 0, 0
	perNS := map[string]int{}
	rc := kube.RowCtx{Now: now, Metrics: env.Metrics}
	for _, u := range pods {
		p := kube.To[corev1.Pod](u)
		perNS[p.Namespace]++
		switch p.Status.Phase {
		case corev1.PodRunning:
			running++
		case corev1.PodPending:
			pending++
		case corev1.PodFailed:
			failed++
		case corev1.PodSucceeded:
			succeeded++
		}
		if _, level := kube.PodStatus(p); level == kube.LevelErr || level == kube.LevelWarn {
			cells, lv := kube.Def("pods").Row(u, rc)
			d.problems = append(d.problems, Row{ID: p.Namespace + "/" + p.Name, Namespace: p.Namespace, Name: p.Name,
				Level: lv, Data: u, Cells: []string{p.Namespace + "/" + p.Name, cells[2], cells[1], cells[3], cells[8]}})
		}
	}
	nodes := s.List(kube.GVRNodes, "")
	readyNodes := 0
	for _, u := range nodes {
		n := kube.To[corev1.Node](u)
		ready, status := kube.NodeReady(n)
		if ready {
			readyNodes++
		}
		alloc := n.Status.Allocatable
		statusSt := st.OK
		if !ready {
			statusSt = st.Err
		} else if n.Spec.Unschedulable {
			statusSt = st.Warn
		}
		podCount := 0
		for _, pu := range pods {
			if nn, _, _ := unstructuredString(pu.Object, "spec", "nodeName"); nn == n.Name {
				podCount++
			}
		}
		line := st.Bold.Render(n.Name) + "  " + statusSt.Render(status) + st.Muted.Render("  "+kube.NodeRoles(n)+"  "+n.Status.NodeInfo.KubeletVersion)
		d.nodes = append(d.nodes, line)
		if m, ok := env.Metrics.Node(n.Name); ok {
			cpuPct := pctf(m.CPUMilli, alloc.Cpu().MilliValue())
			memPct := pctf(m.MemBytes, alloc.Memory().Value())
			d.nodes = append(d.nodes,
				fmt.Sprintf("  CPU %s %5.1f%%  %s / %s", bar(cpuPct, 24), cpuPct, kube.FormatCPU(m.CPUMilli), kube.FormatCPU(alloc.Cpu().MilliValue())),
				fmt.Sprintf("  MEM %s %5.1f%%  %s / %s", bar(memPct, 24), memPct, kube.FormatBytes(m.MemBytes), kube.FormatBytes(alloc.Memory().Value())))
		} else {
			d.nodes = append(d.nodes, st.Muted.Render("  메트릭 없음 (metrics-server 확인)"))
		}
		maxPods := n.Status.Allocatable.Pods().Value()
		podPct := pctf(int64(podCount), maxPods)
		d.nodes = append(d.nodes, fmt.Sprintf("  POD %s %5.1f%%  %d / %d", bar(podPct, 24), podPct, podCount, maxPods))
	}

	deploys := s.List(kube.GVRDeployments, env.Namespace)
	readyDeploy := 0
	for _, u := range deploys {
		if deploymentReady(u) {
			readyDeploy++
		}
	}
	pvcs := s.List(kube.GVRPVCs, env.Namespace)
	boundPVC := 0
	for _, u := range pvcs {
		if ph, _, _ := unstructuredString(u.Object, "status", "phase"); ph == "Bound" {
			boundPVC++
		}
	}
	nsCount := len(s.List(kube.GVRNamespaces, ""))
	svcCount := len(s.List(kube.GVRServices, env.Namespace))

	ver := ""
	if env.Kube != nil {
		ver = env.Kube.ServerVersion
	}
	ratio := func(ok, total int) string {
		txt := fmt.Sprintf("%d/%d", ok, total)
		if ok < total {
			return st.Warn.Render(txt)
		}
		return st.OK.Render(txt)
	}
	podLine := fmt.Sprintf("%s running", st.OK.Render(fmt.Sprint(running)))
	if pending > 0 {
		podLine += ", " + st.Warn.Render(fmt.Sprintf("%d pending", pending))
	}
	if failed > 0 {
		podLine += ", " + st.Err.Render(fmt.Sprintf("%d failed", failed))
	}
	if succeeded > 0 {
		podLine += st.Muted.Render(fmt.Sprintf(", %d completed", succeeded))
	}
	label := func(s string) string { return st.Muted.Render(padRight(s, 12)) }
	d.cluster = []string{
		label("API server") + ver,
		label("Nodes") + ratio(readyNodes, len(nodes)) + st.Muted.Render(" ready"),
		label("Namespaces") + fmt.Sprint(nsCount),
		label("Pods") + podLine + st.Muted.Render(fmt.Sprintf("  (total %d)", len(pods))),
		label("Deployments") + ratio(readyDeploy, len(deploys)) + st.Muted.Render(" ready"),
		label("Services") + fmt.Sprint(svcCount),
		label("PVC") + ratio(boundPVC, len(pvcs)) + st.Muted.Render(" bound"),
	}
	// 네임스페이스별 Pod 수 (많은 순 상위 6개)
	type nc struct {
		ns string
		n  int
	}
	var list []nc
	for k, v := range perNS {
		list = append(list, nc{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].n > list[j].n || (list[i].n == list[j].n && list[i].ns < list[j].ns)
	})
	var parts []string
	for i, x := range list {
		if i == 6 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", x.ns, x.n))
	}
	if len(parts) > 0 {
		d.cluster = append(d.cluster, label("Pods/ns")+st.Muted.Render(strings.Join(parts, " · ")))
	}

	// 최근 Warning 이벤트
	evDef := kube.Def("events")
	evs := s.List(kube.GVREvents, env.Namespace)
	evDef.SortObjects(evs)
	for _, u := range evs {
		if t, _, _ := unstructuredString(u.Object, "type"); t != "Warning" {
			continue
		}
		cells, _ := evDef.Row(u, rc)
		d.events = append(d.events, st.Muted.Render(fmt.Sprintf("%-6s", cells[0]))+" "+st.Warn.Render(cells[2])+" "+
			st.Info.Render(u.GetNamespace()+"/"+cells[3])+" "+cells[4])
		if len(d.events) == 8 {
			break
		}
	}
	if d.cursor >= len(d.problems) {
		d.cursor = max(0, len(d.problems)-1)
	}
}

func unstructuredString(obj map[string]any, fields ...string) (string, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false, nil
		}
		cur = m[f]
	}
	s, ok := cur.(string)
	return s, ok, nil
}

func pctf(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) * 100 / float64(total)
}

func (d *Dashboard) hostLines() []string {
	env := d.env
	st := env.Styles
	hs := env.HostState
	label := func(s string) string { return st.Muted.Render(padRight(s, 14)) }
	var out []string
	svc := hs.Service
	switch {
	case hs.Updated.IsZero():
		out = append(out, st.Muted.Render("조회 중…"))
		return out
	case hs.Err != nil:
		out = append(out, label("service")+st.Err.Render(hs.Err.Error()))
	default:
		state := st.OK.Render("● " + svc.ActiveState)
		if !svc.Active() {
			state = st.Err.Render("● " + svc.ActiveState + " (" + svc.SubState + ")")
		}
		out = append(out, label("service")+state+st.Muted.Render("  "+svc.Unit+".service"))
		if !svc.Since.IsZero() {
			out = append(out, label("uptime")+kube.HumanDuration(env.NowTime().Sub(svc.Since)))
		}
		if svc.MemoryBytes >= 0 {
			out = append(out, label("memory")+kube.FormatBytes(svc.MemoryBytes))
		}
		if svc.Restarts > 0 {
			out = append(out, label("restarts")+st.Warn.Render(fmt.Sprint(svc.Restarts)))
		}
	}
	if hs.Version != "" {
		out = append(out, label("version")+strings.TrimPrefix(hs.Version, "k3s version "))
	}
	ds := hs.Datastore
	dsTxt := string(ds.Kind)
	if ds.Kind == k3s.DatastoreSQLite {
		dsTxt += " (" + kube.FormatBytes(ds.Size) + ")"
	}
	out = append(out, label("datastore")+dsTxt)
	if len(hs.Certs) > 0 {
		c := hs.Certs[0] // 만료가 가장 빠른 인증서
		days := c.DaysLeft(env.NowTime())
		txt := fmt.Sprintf("%d일 남음 (%s)", days, c.Subject)
		switch {
		case days < 0:
			txt = st.Err.Render(txt)
		case days < 30:
			txt = st.Warn.Render(txt)
		}
		out = append(out, label("cert 만료")+txt)
	}
	for _, dk := range hs.Disk {
		if dk.Err != "" {
			continue
		}
		p := dk.Percent()
		txt := fmt.Sprintf("%s %4.1f%%", bar(p, 16), p)
		switch {
		case p >= 90:
			txt = st.Err.Render(txt)
		case p >= 80:
			txt = st.Warn.Render(txt)
		}
		out = append(out, label("disk "+dk.Label)+txt)
	}
	if !env.Host.IsRoot() {
		out = append(out, st.Warn.Render("root 아님: 호스트 기능 제한"))
	}
	return out
}

func (d *Dashboard) Update(msg tea.Msg) (Page, tea.Cmd) {
	keys := d.env.Cfg.Keys
	switch msg := msg.(type) {
	case StoreChangedMsg, TickMsg, NamespaceChangedMsg, RefreshMsg:
		d.refresh()
	case tea.KeyPressMsg:
		k := msg.String()
		switch {
		case keys.Is(config.KeyUp, k):
			d.cursor = max(0, d.cursor-1)
		case keys.Is(config.KeyDown, k):
			d.cursor = min(len(d.problems)-1, d.cursor+1)
		default:
			if d.cursor < len(d.problems) {
				row := d.problems[d.cursor]
				for _, a := range d.pods.Actions() {
					for _, ak := range d.env.Cfg.Keys.ActionKeys(a.ID, a.Keys) {
						if ak == k {
							return d, func() tea.Msg { return ActionRequestMsg{Action: a, Row: row, Source: "pods"} }
						}
					}
				}
			}
		}
	}
	return d, nil
}

func panel(st lipgloss.Style, titleSt lipgloss.Style, title string, lines []string, width int) string {
	inner := max(10, width-4)
	for i, l := range lines {
		if ansi.StringWidth(l) > inner {
			lines[i] = ansi.Truncate(l, inner, "…")
		}
	}
	body := titleSt.Render(title) + "\n" + strings.Join(lines, "\n")
	return st.Width(width).Render(body)
}

func (d *Dashboard) View(width, height int) string {
	st := d.env.Styles
	var top string
	cl := append([]string{}, d.cluster...)
	hl := d.hostLines()
	if width >= 110 {
		lw := width / 2
		left := panel(st.Panel, st.PanelTitle, "클러스터", cl, lw)
		right := panel(st.Panel, st.PanelTitle, "K3S 호스트", hl, width-lw)
		// 높이를 맞춥니다
		h := max(lipgloss.Height(left), lipgloss.Height(right))
		left = panel(st.Panel.Height(h), st.PanelTitle, "클러스터", cl, lw)
		right = panel(st.Panel.Height(h), st.PanelTitle, "K3S 호스트", hl, width-lw)
		top = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	} else {
		top = panel(st.Panel, st.PanelTitle, "클러스터", cl, width) + "\n" + panel(st.Panel, st.PanelTitle, "K3S 호스트", hl, width)
	}
	nodes := panel(st.Panel, st.PanelTitle, "노드", append([]string{}, d.nodes...), width)

	used := lipgloss.Height(top) + lipgloss.Height(nodes)
	remain := height - used
	// 문제 Pod 목록 (선택 가능)
	var pl []string
	if len(d.problems) == 0 {
		pl = append(pl, st.OK.Render("문제 있는 Pod가 없습니다"))
	} else {
		maxRows := max(1, remain/2-3)
		start := 0
		if d.cursor >= maxRows {
			start = d.cursor - maxRows + 1
		}
		for i := start; i < len(d.problems) && i < start+maxRows; i++ {
			r := d.problems[i]
			line := fmt.Sprintf("%-50s %-24s %-6s %-14s %s", r.Cells[0], r.Cells[1], r.Cells[2], r.Cells[3], r.Cells[4])
			line = ansi.Truncate(line, width-4, "…")
			if i == d.cursor {
				line = st.Selected.Render(line)
			} else if ls := d.env.LevelStyle(r.Level); ls != nil {
				line = ls.Render(line)
			}
			pl = append(pl, line)
		}
		if len(d.problems) > maxRows {
			pl = append(pl, st.Muted.Render(fmt.Sprintf("… 총 %d개", len(d.problems))))
		}
	}
	probTitle := fmt.Sprintf("문제 Pod (%d)", len(d.problems))
	probs := panel(st.Panel, st.PanelTitle, probTitle, pl, width)
	ev := d.events
	if len(ev) == 0 {
		ev = []string{st.Muted.Render("최근 Warning 이벤트 없음")}
	}
	maxEv := max(1, height-used-lipgloss.Height(probs)-3)
	if len(ev) > maxEv {
		ev = ev[:maxEv]
	}
	events := panel(st.Panel, st.PanelTitle, "최근 Warning 이벤트", append([]string{}, ev...), width)
	out := lipgloss.JoinVertical(lipgloss.Left, top, nodes, probs, events)
	// 화면보다 길면 잘라냅니다
	lines := strings.Split(out, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (d *Dashboard) Title() string { return "Dashboard" }

func (d *Dashboard) Hints() []Hint {
	if len(d.problems) == 0 {
		return nil
	}
	return []Hint{{"enter", "상세"}, {"l", "로그"}, {"y", "YAML"}, {"x", "삭제"}}
}

func (d *Dashboard) InputActive() bool { return false }
func (d *Dashboard) Close()            {}

// padRight는 표시 폭(한글 2칸) 기준으로 오른쪽을 채웁니다.
func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(1, w-ansi.StringWidth(s)))
}
