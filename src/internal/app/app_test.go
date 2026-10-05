package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/audit"
	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/helm"
	"github.com/DataDynamics/k3s-management-tui/internal/k3s"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
	"github.com/DataDynamics/k3s-management-tui/internal/runtime"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/styles"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/views"
)

type failRunner struct{}

func (failRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("테스트: 외부 명령 없음")
}

// fakeHost는 실제 서버를 건드리지 않는 Host 구현입니다 (설계 7.3).
type fakeHost struct {
	root bool
	ops  []k3s.ServiceOp
}

func (f *fakeHost) IsRoot() bool        { return f.root }
func (f *fakeHost) ServiceName() string { return "k3s" }
func (f *fakeHost) ServiceStatus(context.Context) (k3s.ServiceStatus, error) {
	return k3s.ServiceStatus{Unit: "k3s", ActiveState: "active", SubState: "running", Since: time.Now().Add(-time.Hour), MemoryBytes: 1 << 30}, nil
}
func (f *fakeHost) ServiceControl(_ context.Context, op k3s.ServiceOp) error {
	f.ops = append(f.ops, op)
	return nil
}
func (f *fakeHost) StreamJournal(context.Context, int, bool) (<-chan string, error) {
	ch := make(chan string, 2)
	ch <- "level=error msg=x"
	close(ch)
	return ch, nil
}
func (f *fakeHost) Version(context.Context) (string, error)     { return "k3s version v1.36.5+k3s1", nil }
func (f *fakeHost) CheckConfig(context.Context) (string, error) { return "ok", nil }
func (f *fakeHost) ConfigPath() string                          { return "/etc/rancher/k3s/config.yaml" }
func (f *fakeHost) LoadConfig() (*k3s.K3sConfig, error) {
	return &k3s.K3sConfig{Values: map[string]any{"data-dir": "/data2/k3s"}}, nil
}
func (f *fakeHost) WriteConfig([]byte) (string, error) { return "/tmp/backup", nil }
func (f *fakeHost) DataDir() string                    { return "/data2/k3s" }
func (f *fakeHost) KubeletDir() string                 { return "/var/lib/kubelet" }
func (f *fakeHost) Datastore() k3s.DatastoreInfo {
	return k3s.DatastoreInfo{Kind: k3s.DatastoreSQLite, Path: "/data2/k3s/server/db/state.db", Size: 1 << 20}
}
func (f *fakeHost) Backup(context.Context) (string, error) { return "/backups/b.db", nil }
func (f *fakeHost) ListBackups() ([]k3s.BackupFile, error) {
	return []k3s.BackupFile{{Name: "k3s-sqlite-x.db", Kind: k3s.DatastoreSQLite, Time: time.Now()}}, nil
}
func (f *fakeHost) DeleteBackup(context.Context, string) error                { return nil }
func (f *fakeHost) RestoreBackup(context.Context, string, func(string)) error { return nil }
func (f *fakeHost) Manifests() ([]k3s.Manifest, error)                        { return nil, nil }
func (f *fakeHost) SetManifestSkip(string, bool) error                        { return nil }
func (f *fakeHost) Certificates() ([]k3s.CertInfo, error)                     { return nil, nil }
func (f *fakeHost) RotateCertificates(context.Context, func(string)) error    { return nil }
func (f *fakeHost) Token() (string, error)                                    { return "K10token", nil }
func (f *fakeHost) DiskUsage() []k3s.DiskInfo                                 { return nil }
func (f *fakeHost) ReadFile(string) (string, error)                           { return "data-dir: /data2/k3s\n", nil }

func newTestModel(t *testing.T, root bool) (*Model, *fakeHost, *bytes.Buffer) {
	t.Helper()
	return newTestModelWith(t, root, func(env *views.Env) {
		env.Distro, env.LocalAPI, env.HostEnabled = "k3s", true, true
	})
}

func newTestModelWith(t *testing.T, root bool, setup func(*views.Env)) (*Model, *fakeHost, *bytes.Buffer) {
	t.Helper()
	cfg := config.Default()
	cfg.Keys = config.DefaultKeybindings()
	cfg.Theme, _ = config.LoadTheme("", "dark")
	cfg.Views = config.ViewOverrides{}
	host := &fakeHost{root: root}
	var auditBuf bytes.Buffer
	env := &views.Env{
		Cfg: cfg, Styles: styles.New(cfg.Theme), Metrics: kube.NewMetrics(), Host: host,
		Audit: audit.NewWriter(&auditBuf), KubeErr: errors.New("테스트: 클러스터 없음"),
		Helm:   &helm.Client{Binary: "helm", Run: failRunner{}},
		Crictl: &runtime.Crictl{Binary: "k3s", Run: failRunner{}},
	}
	setup(env)
	m := New(env)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, host, &auditBuf
}

// run은 명령을 실행해 나온 메시지를 모델에 다시 넣습니다. 오래 걸리는 명령(tick 등)은 건너뜁니다.
func run(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 8 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(300 * time.Millisecond):
		return
	}
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			run(t, m, c, depth+1)
		}
	default:
		if _, quit := msg.(tea.QuitMsg); quit {
			return
		}
		_, next := m.Update(msg)
		run(t, m, next, depth+1)
	}
}

func key(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func typeText(t *testing.T, m *Model, s string) {
	for _, r := range s {
		_, cmd := m.Update(key(string(r)))
		run(t, m, cmd, 0)
	}
}

func TestRendersAllTabsWithoutCluster(t *testing.T) {
	m, _, _ := newTestModel(t, true)
	for i := range m.tabs {
		run(t, m, m.activateTab(i), 0)
		for _, size := range [][2]int{{120, 40}, {80, 24}, {200, 60}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			out := m.render()
			if got := strings.Count(out, "\n") + 1; got != size[1] {
				t.Errorf("tab %s %dx%d: 줄 수 %d", m.tabs[i].def.Name, size[0], size[1], got)
			}
		}
	}
	m.activateTab(0)
	if !strings.Contains(m.render(), "테스트: 클러스터 없음") {
		t.Error("연결 오류가 대시보드에 표시되어야 함")
	}
}

func TestActionKeysDoNotShadowGlobalKeys(t *testing.T) {
	m, _, _ := newTestModel(t, true)
	kb := m.env.Cfg.Keys
	reserved := map[string]string{}
	for _, id := range []string{config.KeyUp, config.KeyDown, config.KeyTop, config.KeyBottom, config.KeyPageUp, config.KeyPageDown,
		config.KeyFilter, config.KeyCommand, config.KeyNamespace, config.KeyHelp, config.KeyQuit, config.KeyBack,
		config.KeyNextSource, config.KeyPrevSource, config.KeyRefresh} {
		for _, k := range kb.Global[id] {
			reserved[k] = id
		}
	}
	for i := 1; i <= 9; i++ {
		reserved[string(rune('0'+i))] = "tab"
	}
	check := func(where string, acts []*views.Action) {
		seen := map[string]string{}
		for _, a := range acts {
			for _, k := range kb.ActionKeys(a.ID, a.Keys) {
				if g, ok := reserved[k]; ok {
					t.Errorf("%s: 작업 %s의 키 %q가 전역 키 %s와 겹침", where, a.ID, k, g)
				}
				if prev, ok := seen[k]; ok && prev != a.ID {
					t.Errorf("%s: 키 %q가 %s와 %s에 중복", where, k, prev, a.ID)
				}
				seen[k] = a.ID
			}
		}
	}
	for _, tab := range m.tabs {
		tp, ok := tab.def.Root.(*views.TablePage)
		if !ok {
			continue
		}
		for _, d := range kube.Defs() {
			if s := tp.Source(d.Key); s != nil {
				check(tab.def.Name+"/"+d.Key, s.Actions())
			}
		}
		for _, s := range views.HostSources() {
			check("Host/"+s.Key(), s.Actions())
		}
	}
	for _, d := range kube.Defs() {
		check("resource/"+d.Key, views.NewResourceSource(d.Key).Actions())
	}
}

func testAction(called *int, confirm views.ConfirmKind) *views.Action {
	return &views.Action{ID: "test_op", Label: "테스트", Mutating: true, Confirm: confirm,
		Do: func(*views.Env, views.Row, string) (string, error) {
			*called++
			return "완료", nil
		}}
}

func TestReadOnlyBlocksMutatingActions(t *testing.T) {
	m, _, auditBuf := newTestModel(t, true)
	m.env.ReadOnly = true
	called := 0
	_, cmd := m.Update(views.ActionRequestMsg{Action: testAction(&called, views.ConfirmNone), Row: views.Row{Name: "x"}, Source: "pods"})
	run(t, m, cmd, 0)
	if called != 0 || !m.toastErr || !strings.Contains(m.toast, "읽기 전용") {
		t.Errorf("read-only 차단 실패: called=%d toast=%q", called, m.toast)
	}
	if auditBuf.Len() != 0 {
		t.Error("차단된 작업이 감사 로그에 남음")
	}
}

func TestNeedRootBlocks(t *testing.T) {
	m, host, _ := newTestModel(t, false)
	a := &views.Action{ID: "service_restart", Label: "재시작", Mutating: true, NeedRoot: true, NoRow: true,
		Do: func(env *views.Env, _ views.Row, _ string) (string, error) {
			return "", env.Host.ServiceControl(context.Background(), k3s.OpRestart)
		}}
	_, cmd := m.Update(views.ActionRequestMsg{Action: a, Source: "service"})
	run(t, m, cmd, 0)
	if len(host.ops) != 0 || !strings.Contains(m.toast, "root") {
		t.Errorf("root 아님 차단 실패: ops=%v toast=%q", host.ops, m.toast)
	}
}

func TestConfirmFlowRunsAndAudits(t *testing.T) {
	m, _, auditBuf := newTestModel(t, true)
	called := 0
	req := views.ActionRequestMsg{Action: testAction(&called, views.ConfirmYesNo), Row: views.Row{Namespace: "default", Name: "web"}, Source: "pods"}
	_, cmd := m.Update(req)
	run(t, m, cmd, 0)
	if m.dialog == nil || m.dialog.Kind != components.DialogConfirm {
		t.Fatal("y/N 다이얼로그가 떠야 함")
	}
	// n은 취소
	_, cmd = m.Update(key("n"))
	run(t, m, cmd, 0)
	if called != 0 || m.dialog != nil {
		t.Fatal("취소했는데 실행됨")
	}
	// y는 실행
	_, cmd = m.Update(req)
	run(t, m, cmd, 0)
	_, cmd = m.Update(key("y"))
	run(t, m, cmd, 0)
	if called != 1 {
		t.Fatalf("실행 횟수 = %d", called)
	}
	if !strings.Contains(auditBuf.String(), `"action":"test_op"`) || !strings.Contains(auditBuf.String(), `"target":"pods/default/web"`) {
		t.Errorf("감사 로그: %s", auditBuf.String())
	}
	if m.toast != "완료" || m.running != 0 {
		t.Errorf("toast=%q running=%d", m.toast, m.running)
	}
}

func TestProtectedNamespaceRequiresTypedName(t *testing.T) {
	m, _, _ := newTestModel(t, true)
	called := 0
	req := views.ActionRequestMsg{Action: testAction(&called, views.ConfirmYesNo), Row: views.Row{Namespace: "kube-system", Name: "coredns"}, Source: "deployments"}
	_, cmd := m.Update(req)
	run(t, m, cmd, 0)
	if m.dialog == nil || m.dialog.Kind != components.DialogConfirmType || m.dialog.Expect != "coredns" {
		t.Fatalf("보호 네임스페이스는 이름 재입력이어야 함: %+v", m.dialog)
	}
	typeText(t, m, "wrong")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if called != 0 || m.dialog == nil {
		t.Fatal("틀린 이름으로 실행됨")
	}
	// 지우고 정확히 입력
	for range "wrong" {
		m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(t, m, "coredns")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if called != 1 {
		t.Errorf("정확한 이름 입력 후 실행 안 됨 (called=%d)", called)
	}
}

func TestPromptStagePassesInput(t *testing.T) {
	m, _, auditBuf := newTestModel(t, true)
	var got string
	a := &views.Action{ID: "scale", Label: "스케일", Mutating: true,
		Prompt: func(*views.Env, views.Row) (string, string) { return "replicas", "1" },
		Do: func(_ *views.Env, _ views.Row, in string) (string, error) {
			got = in
			return "ok", nil
		}}
	_, cmd := m.Update(views.ActionRequestMsg{Action: a, Row: views.Row{Namespace: "default", Name: "web"}, Source: "deployments"})
	run(t, m, cmd, 0)
	if m.dialog == nil || m.dialog.Kind != components.DialogInput {
		t.Fatal("입력 다이얼로그가 떠야 함")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	typeText(t, m, "4")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if got != "4" || !strings.Contains(auditBuf.String(), "input=4") {
		t.Errorf("입력값 전달 실패: got=%q audit=%s", got, auditBuf.String())
	}
}

func TestHostTabServiceRestartFlow(t *testing.T) {
	m, host, auditBuf := newTestModel(t, true)
	for i, tab := range m.tabs {
		if tab.def.Key == "host" {
			run(t, m, m.activateTab(i), 0)
		}
	}
	if !strings.Contains(m.render(), "k3s.service") {
		t.Fatalf("Host 탭에 서비스 상태가 보여야 함:\n%s", m.render())
	}
	_, cmd := m.Update(key("r"))
	run(t, m, cmd, 0)
	if m.dialog == nil || m.dialog.Kind != components.DialogConfirmType {
		t.Fatal("서비스 재시작은 이름 재입력 확인이어야 함")
	}
	typeText(t, m, "k3s")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if len(host.ops) != 1 || host.ops[0] != k3s.OpRestart {
		t.Errorf("재시작 호출: %v", host.ops)
	}
	if !strings.Contains(auditBuf.String(), "service_restart") {
		t.Errorf("감사 로그: %s", auditBuf.String())
	}
}

func TestCommandModeAndQuit(t *testing.T) {
	m, _, _ := newTestModel(t, true)
	_, cmd := m.Update(key(":"))
	run(t, m, cmd, 0)
	if !m.cmdMode {
		t.Fatal("명령 모드 진입 실패")
	}
	typeText(t, m, "helm")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if m.tabs[m.active].def.Key != "helm" {
		t.Errorf(":helm 이동 실패 (active=%s)", m.tabs[m.active].def.Key)
	}
	_, cmd = m.Update(key(":"))
	run(t, m, cmd, 0)
	typeText(t, m, "nonsense")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if !m.toastErr {
		t.Error("알 수 없는 명령은 오류 알림이어야 함")
	}
	// 하위 화면에서 q는 뒤로, 최상위에서 q는 종료
	_, cmd = m.Update(key("?"))
	run(t, m, cmd, 0)
	if len(m.tabs[m.active].stack) != 2 {
		t.Fatal("도움말이 열려야 함")
	}
	m.Update(key("q"))
	if len(m.tabs[m.active].stack) != 1 {
		t.Error("하위 화면에서 q는 뒤로 가야 함")
	}
	_, cmd = m.Update(key("q"))
	if cmd == nil {
		t.Fatal("최상위에서 q는 종료 명령이어야 함")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("QuitMsg가 아님")
	}
}

func TestRemoteClusterHidesHostFeatures(t *testing.T) {
	m, host, _ := newTestModelWith(t, true, func(env *views.Env) {
		env.Distro, env.LocalAPI, env.HostEnabled = "eks", false, false
		env.HostReason = "API 서버가 원격에 있습니다 (https://example.eks.amazonaws.com)"
		env.Cfg.Cluster.Kubeconfig, env.Cfg.Cluster.KubeconfigSource = "/home/u/.kube/config", "~/.kube/config"
	})
	var names []string
	for _, tab := range m.tabs {
		names = append(names, tab.def.Key)
		if tab.def.Key == "host" {
			t.Error("원격 클러스터에서는 Host 탭이 없어야 함")
		}
	}
	if strings.Join(names, ",") != "dashboard,workloads,network,storage,config,helm" {
		t.Errorf("탭 순서: %v", names)
	}
	out := m.render()
	for _, want := range []string{"EKS", "연결 정보", "API 서버가 원격에 있습니다", "6 Helm"} {
		if !strings.Contains(out, want) {
			t.Errorf("화면에 %q가 없음:\n%s", want, out)
		}
	}
	if strings.Contains(out, "k3s.service") {
		t.Error("원격 모드 헤더에 k3s 서비스 상태가 나오면 안 됨")
	}
	// 호스트 상태를 읽지 않아야 합니다.
	if cmd := m.pollHost(true); cmd != nil {
		t.Error("호스트 관리가 꺼져 있으면 호스트 상태를 조회하면 안 됨")
	}
	_ = host
	// :host, :journal은 없는 기능으로 안내합니다.
	_, cmd := m.Update(key(":"))
	run(t, m, cmd, 0)
	typeText(t, m, "journal")
	_, cmd = m.Update(key("enter"))
	run(t, m, cmd, 0)
	if !m.toastErr {
		t.Error(":journal은 원격 모드에서 오류 알림이어야 함")
	}
	// 도움말의 탭 번호도 6개 기준이어야 합니다.
	help := views.NewHelpPage(m.env, m.page(), m.tabNames())
	run(t, m, help.Init(), 0)
	m.Update(views.PushPageMsg{Page: help})
	run(t, m, help.Init(), 0)
	if !strings.Contains(m.render(), "1 ~ 6") {
		t.Errorf("도움말 탭 번호:\n%s", m.render())
	}
}
