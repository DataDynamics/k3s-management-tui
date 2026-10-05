// k3stui는 K3S를 관리하는 TUI입니다.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/app"
	"github.com/DataDynamics/k3s-management-tui/internal/audit"
	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
	"github.com/DataDynamics/k3s-management-tui/internal/helm"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
	"github.com/DataDynamics/k3s-management-tui/internal/k3s"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
	"github.com/DataDynamics/k3s-management-tui/internal/kubeadm"
	"github.com/DataDynamics/k3s-management-tui/internal/runtime"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/styles"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/views"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "k3stui:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		confPath   = flag.String("config", "", "설정 파일 경로 (기본: 탐색 순서에 따름)")
		kubeconfig = flag.String("kubeconfig", "", "kubeconfig 경로 (기본: $KUBECONFIG → ~/.kube/config → K3S·RKE2·kubeadm 기본 경로)")
		kubeCtx    = flag.String("context", "", "사용할 kubeconfig context (기본: current-context)")
		distro     = flag.String("distribution", "", "배포판 지정 (auto, k3s, rke2, kubeadm, eks, gke, kubernetes)")
		readOnly   = flag.Bool("read-only", false, "모든 변경 작업 비활성화")
		namespace  = flag.String("n", "", "시작 네임스페이스 (all = 전체)")
		view       = flag.String("view", "", "시작 탭 (dashboard, workloads, network, storage, config, host, helm)")
		showVer    = flag.Bool("version", false, "버전 출력")
		check      = flag.Bool("check", false, "환경 점검 결과를 출력하고 종료")
		dump       = flag.String("dump", "", "소스 하나(pods, service, backups ...)를 표로 출력하고 종료")
		columns    = flag.Bool("columns", false, "views.d에 쓸 리소스 이름과 내장 컬럼을 출력하고 종료 (뒤에 리소스 이름을 주면 그것만)")
	)
	flag.Parse()
	if *columns {
		return views.PrintColumns(os.Stdout, flag.Arg(0))
	}
	if *showVer {
		fmt.Println("k3stui", version)
		return nil
	}

	cfg, err := config.Load(*confPath)
	if err != nil {
		return err
	}
	if *kubeCtx != "" {
		cfg.Cluster.Context = *kubeCtx
	}
	if *distro != "" {
		cfg.Cluster.Distribution = *distro
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	resolveKubeconfig(cfg, *kubeconfig)
	if *readOnly {
		cfg.Safety.ReadOnly = true
	}
	if *namespace != "" {
		cfg.UI.DefaultNamespace = *namespace
	}
	if *view != "" {
		cfg.UI.DefaultView = *view
		if err := cfg.Validate(); err != nil {
			return err
		}
	}

	logClose := setupLogging(cfg)
	defer logClose()
	viewWarns := views.PrepareViewOverrides(cfg)
	for _, w := range viewWarns {
		slog.Warn(w)
	}
	slog.Info("start", "version", version, "config", cfg.File, "uid", os.Geteuid(),
		"kubeconfig", cfg.Cluster.Kubeconfig, "kubeconfig_source", cfg.Cluster.KubeconfigSource, "context", cfg.Cluster.Context)

	env, cleanup := buildEnv(cfg)
	defer cleanup()
	env.Warnings = viewWarns

	switch {
	case *check:
		return runCheck(env)
	case *dump != "":
		return runDump(env, *dump)
	}

	m := app.New(env)
	p := tea.NewProgram(m)
	env.Send = p.Send
	_, err = p.Run()
	return err
}

// resolveKubeconfig는 탐색 순서대로 kubeconfig를 고릅니다.
// 사용자가 지정한 경로는 그대로 쓰고, 자동 탐색 후보는 실제로 쓸 수 있는지(context와 서버 주소가 있는지) 확인합니다.
// 쓸 수 있는 후보가 없으면 존재하는 첫 파일을 골라 연결 오류가 화면에 보이게 합니다.
func resolveKubeconfig(cfg *config.Config, flagPath string) {
	cands := cfg.KubeconfigCandidates(flagPath)
	set := func(c config.KubeconfigCandidate) {
		cfg.Cluster.Kubeconfig, cfg.Cluster.KubeconfigSource = c.Path, c.Source
	}
	for _, c := range cands {
		if c.Explicit {
			set(c)
			return
		}
		if err := kube.UsableKubeconfig(c.Path, cfg.Cluster.Context); err == nil {
			set(c)
			return
		} else {
			slog.Debug("kubeconfig 후보 건너뜀", "path", c.Path, "err", err)
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c.Path); err == nil {
			set(c)
			return
		}
	}
	if len(cands) > 0 {
		set(cands[0])
	}
}

// decideHost는 배포판을 판별하고 호스트 관리(Host 탭)를 켤지, 어떤 구현(K3S, kubeadm)을 쓸지 정합니다.
func decideHost(env *views.Env) {
	cfg := env.Cfg
	server, _ := kube.ServerURL(cfg.Cluster.Kubeconfig, cfg.Cluster.Context)
	if env.Kube != nil {
		server = env.Kube.Server
	}
	env.LocalAPI = kube.IsLocalServer(server)
	_, k3sErr := os.Stat(cfg.K3s.Binary)
	kubeadmNode := fileExists(filepath.Join(cfg.Kubeadm.KubernetesDir, "manifests", "kube-apiserver.yaml")) ||
		fileExists(cfg.Kubeadm.KubeletConfig)

	env.Distro = cfg.Cluster.Distribution
	if env.Distro == config.DistroAuto {
		switch {
		case env.Kube != nil:
			env.Distro = env.Kube.DetectDistro(contextBG())
		// API 서버가 응답하지 않아도(k3s·kubelet 중지 등) 로컬 노드면 서비스를 다시 띄울 수 있어야 합니다.
		case env.LocalAPI && k3sErr == nil:
			env.Distro = config.DistroK3s
		case env.LocalAPI && kubeadmNode:
			env.Distro = config.DistroKubeadm
		default:
			env.Distro = config.DistroKubernetes
		}
	}

	runner := executil.System{Timeout: cfg.Tools.Timeout}
	var h host.Host
	switch env.Distro {
	case config.DistroK3s:
		h = k3s.NewSystem(cfg, runner)
	case config.DistroKubeadm:
		h = kubeadm.NewSystem(cfg, runner)
	}

	switch {
	case cfg.Cluster.HostManagement == config.HostDisabled:
		env.HostReason = "설정에서 끔 (cluster.host_management: disabled)"
	case h == nil:
		env.HostReason = kube.DistroName(env.Distro) + " 클러스터는 호스트 관리를 지원하지 않습니다 (K3S, kubeadm만 지원)"
	case cfg.Cluster.HostManagement == config.HostEnabled:
		env.HostEnabled = true
	case !env.LocalAPI:
		env.HostReason = "API 서버가 원격에 있습니다 (" + server + ")"
	case env.Distro == config.DistroK3s && k3sErr != nil:
		env.HostReason = "k3s 바이너리가 없습니다 (" + cfg.K3s.Binary + ")"
	case env.Distro == config.DistroKubeadm && !kubeadmNode:
		env.HostReason = "kubeadm 노드 파일이 없습니다 (" + cfg.Kubeadm.KubernetesDir + "/manifests, " + cfg.Kubeadm.KubeletConfig + ")"
	default:
		env.HostEnabled = true
	}
	if env.HostEnabled {
		env.Host = h
		env.Crictl = &runtime.Crictl{Command: h.CrictlCommand(), Run: runner}
	}
	if env.Kube != nil && env.LocalAPI {
		env.LocalPath = env.Kube.HasProvisioner(contextBG(), "rancher.io/local-path")
	}
	slog.Info("cluster", "distro", env.Distro, "server", server, "local", env.LocalAPI,
		"host", env.HostEnabled, "reason", env.HostReason, "localpath", env.LocalPath)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// stateDir는 root가 아닐 때 로그를 둘 위치입니다.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "k3stui")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "k3stui")
}

func openLog(path string) (*os.File, string) {
	try := func(p string) *os.File {
		if p == "" {
			return nil
		}
		if os.MkdirAll(filepath.Dir(p), 0o750) != nil {
			return nil
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return nil
		}
		return f
	}
	if f := try(path); f != nil {
		return f, path
	}
	alt := filepath.Join(stateDir(), filepath.Base(path))
	if f := try(alt); f != nil {
		return f, alt
	}
	return nil, ""
}

func setupLogging(cfg *config.Config) func() {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Logging.Level))
	f, path := openLog(cfg.Logging.File)
	var w io.Writer = io.Discard
	if f != nil {
		w = f
		cfg.Logging.File = path
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))
	return func() {
		if f != nil {
			f.Close()
		}
	}
}

func buildEnv(cfg *config.Config) (*views.Env, func()) {
	env := &views.Env{
		Cfg:     cfg,
		Styles:  styles.New(cfg.Theme),
		Metrics: kube.NewMetrics(),
		Helm: &helm.Client{Binary: cfg.Tools.Helm, Context: cfg.Cluster.Context,
			Run: executil.System{Timeout: cfg.Tools.Timeout, Env: []string{"KUBECONFIG=" + cfg.Cluster.Kubeconfig}}},
		ReadOnly: cfg.Safety.ReadOnly,
	}
	if ns := cfg.UI.DefaultNamespace; ns != "all" {
		env.Namespace = ns
	}

	auditPath := cfg.Audit.File
	al, err := audit.Open(auditPath)
	if err != nil {
		alt := filepath.Join(stateDir(), filepath.Base(auditPath))
		if al2, err2 := audit.Open(alt); err2 == nil {
			al, cfg.Audit.File = al2, alt
		} else {
			slog.Warn("audit log disabled", "err", err)
		}
	}
	env.Audit = al

	client, err := kube.New(cfg.Cluster.Kubeconfig, cfg.Cluster.Context)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
			err = fmt.Errorf("%w — root로 실행하거나 --kubeconfig로 읽을 수 있는 파일을 지정하세요", err)
		}
		env.KubeErr = err
		slog.Error("kube connect", "err", err)
	} else {
		env.Kube = client
		env.Store = kube.NewStore(client.Dynamic)
		env.PF = kube.NewPortForwarder(client, cfg.PortForward.Address, func() {
			if env.Send != nil {
				env.Send(views.StoreChangedMsg{})
			}
		})
	}
	decideHost(env)
	return env, func() {
		if env.PF != nil {
			env.PF.StopAll()
		}
		env.Store.Stop()
		_ = al.Close()
	}
}

// runCheck는 설치 직후 환경을 점검합니다.
func runCheck(env *views.Env) error {
	ok := func(b bool) string {
		if b {
			return "OK  "
		}
		return "FAIL"
	}
	cfg := env.Cfg
	h := env.Host
	fmt.Printf("k3stui %s\n", version)
	fmt.Printf("[%s] 설정 파일       %s\n", ok(true), orDefault(cfg.File, "(내장 기본값)"))
	fmt.Printf("[%s] kubeconfig      %s (%s)\n", ok(env.Kube != nil), cfg.Cluster.Kubeconfig, cfg.Cluster.KubeconfigSource)
	if env.Kube != nil {
		fmt.Printf("[%s] API 서버        %s  %s (context: %s)\n", ok(true), env.Kube.ServerVersion, env.Kube.Server, env.Kube.Context)
	} else {
		fmt.Printf("[%s] API 서버        %v\n", ok(false), env.KubeErr)
	}
	local := "원격"
	if env.LocalAPI {
		local = "로컬"
	}
	fmt.Printf("[%s] 배포판          %s (API 서버: %s)\n", ok(true), kube.DistroName(env.Distro), local)
	if env.Kube != nil {
		err := env.Metrics.Poll(contextBG(), env.Kube)
		fmt.Printf("[%s] metrics-server  %v\n", ok(err == nil), orDefault(errString(err), "응답함"))
	}
	if _, err := env.Helm.List(contextBG()); err == nil {
		fmt.Printf("[%s] helm            %s\n", ok(true), cfg.Tools.Helm)
	} else {
		fmt.Printf("[%s] helm            %v\n", ok(false), err)
	}
	kc := cfg.KubectlCommand(env.LocalK3s())
	fmt.Printf("[%s] kubectl         %s\n", ok(true), strings.Join(kc, " "))
	editor := cfg.EditorCommand()
	_, edErr := exec.LookPath(editor[0])
	fmt.Printf("[%s] 편집기          %s\n", ok(edErr == nil), strings.Join(editor, " "))

	if !env.HostEnabled {
		fmt.Printf("[SKIP] 호스트 관리   %s\n", env.HostReason)
	} else {
		fmt.Printf("[%s] 호스트 관리     %s\n", ok(true), kube.DistroName(h.Distro()))
		fmt.Printf("[%s] root 권한       uid=%d\n", ok(h.IsRoot()), os.Geteuid())
		st, err := h.ServiceStatus(contextBG())
		if err != nil {
			fmt.Printf("[%s] 서비스          %v\n", ok(false), err)
		} else {
			fmt.Printf("[%s] 서비스          %s.service %s/%s\n", ok(st.Active()), st.Unit, st.ActiveState, st.SubState)
		}
		if v, err := h.Version(contextBG()); err == nil {
			fmt.Printf("[%s] 버전            %s\n", ok(true), v)
		} else {
			fmt.Printf("[%s] 버전            %v\n", ok(false), err)
		}
		fmt.Printf("[%s] 노드 디렉터리   %s\n", ok(dirExists(h.DataDir())), h.DataDir())
		fmt.Printf("[%s] 설정 파일       %s\n", ok(fileExists(h.ConfigPath())), h.ConfigPath())
		ds := h.Datastore()
		fmt.Printf("[%s] 데이터스토어    %s %s\n", ok(ds.Kind != host.DatastoreUnknown), ds.Kind, ds.Path+ds.Endpoint)
		fmt.Printf("     crictl          %s\n", strings.Join(h.CrictlCommand(), " "))
		caps := h.Caps()
		if caps.Backup {
			fmt.Printf("     백업 위치       %s (보관 %d개)\n", cfg.Backup.Dir, cfg.Backup.Keep)
		}
	}
	fmt.Printf("     로그            %s\n", orDefault(cfg.Logging.File, "(비활성)"))
	fmt.Printf("     감사 로그       %s\n", cfg.Audit.File)
	if len(env.Warnings) == 0 {
		fmt.Printf("[%s] views.d         재정의 %d개\n", ok(true), len(cfg.Views))
	}
	for _, w := range env.Warnings {
		fmt.Printf("[WARN] %s\n", w)
	}
	return nil
}

// runDump는 TUI 없이 소스 하나를 표로 출력합니다 (스크립트·점검용).
func runDump(env *views.Env, key string) error {
	var src views.Source
	// 탭에 있는 소스(호스트 항목 포함)를 먼저 찾고, 없으면 리소스 이름·별칭으로 찾습니다.
	for _, t := range views.BuildTabs(env) {
		if tp, ok := t.Root.(*views.TablePage); ok && tp.HasSource(key) && src == nil {
			src = tp.Source(key)
		}
	}
	if d := kube.Def(key); src == nil && d != nil {
		src = views.NewResourceSource(d.Key)
	}
	if src == nil {
		return fmt.Errorf("알 수 없는 소스: %s", key)
	}
	if !src.Async() && env.Store != nil {
		gvr := kube.GVRPods // cilium 등 Pod 기반 소스
		if rs, ok := src.(*views.ResourceSource); ok {
			gvr = rs.Def.GVR
		}
		env.Store.Ensure(gvr)
		deadline := time.Now().Add(15 * time.Second)
		for !env.Store.Synced(gvr) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		_ = env.Metrics.Poll(contextBG(), env.Kube)
	}
	rows, err := src.Load(env)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	var head []string
	nsCol := src.Namespaced() && env.Namespace == ""
	if nsCol {
		head = append(head, "NAMESPACE")
	}
	for _, c := range src.Columns(env) {
		head = append(head, c.Name)
	}
	fmt.Fprintln(tw, strings.Join(head, "\t"))
	for _, r := range rows {
		cells := r.Cells
		if nsCol {
			cells = append([]string{r.Namespace}, cells...)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// contextBG는 점검용 컨텍스트입니다. 외부 명령은 executil 타임아웃이 적용됩니다.
func contextBG() context.Context { return context.Background() }
