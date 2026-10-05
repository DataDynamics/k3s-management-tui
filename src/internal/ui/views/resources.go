package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// ResourceSource는 Informer 캐시를 읽는 Kubernetes 리소스 소스입니다.
type ResourceSource struct {
	Def   *kube.ResourceDef
	extra []*Action
}

// NewResourceSource는 키로 리소스 소스를 만듭니다. 정의가 없으면 nil.
func NewResourceSource(key string) *ResourceSource {
	d := kube.Def(key)
	if d == nil {
		return nil
	}
	return &ResourceSource{Def: d, extra: specificActions(d)}
}

func (s *ResourceSource) Key() string                { return s.Def.Key }
func (s *ResourceSource) Title() string              { return s.Def.Title }
func (s *ResourceSource) Namespaced() bool           { return s.Def.Namespaced }
func (s *ResourceSource) Async() bool                { return false }
func (s *ResourceSource) AutoRefresh() time.Duration { return 0 }

// Actions는 리소스별 작업 뒤에 공통 작업(describe, yaml, edit, delete)을 붙입니다.
func (s *ResourceSource) Actions() []*Action {
	return append(append([]*Action{}, s.extra...), commonActions(s.Def)...)
}

// StoreErr는 watch 오류입니다 (TablePage가 표시).
func (s *ResourceSource) StoreErr(env *Env) string {
	if env.Store == nil {
		return ""
	}
	return env.Store.Err(s.Def.GVR)
}

// Columns는 views.d 재정의가 있으면 그것을, 없으면 내장 컬럼을 씁니다.
func (s *ResourceSource) Columns(env *Env) []kube.Column {
	ov, ok := env.Cfg.Views[s.Def.Key]
	if !ok {
		return s.Def.Columns
	}
	out := make([]kube.Column, len(ov.Columns))
	for i, c := range ov.Columns {
		out[i] = kube.Column{Name: strings.ToUpper(c.Name), MaxWidth: c.Width}
	}
	return out
}

func (s *ResourceSource) Load(env *Env) ([]Row, error) {
	if env.Store == nil {
		if env.KubeErr != nil {
			return nil, env.KubeErr
		}
		return nil, errors.New("클러스터에 연결되지 않았습니다")
	}
	ns := ""
	if s.Def.Namespaced {
		ns = env.Namespace
	}
	items := env.Store.List(s.Def.GVR, ns)
	s.Def.SortObjects(items)
	rc := kube.RowCtx{Now: env.NowTime(), Metrics: env.Metrics}
	ov, hasOv := env.Cfg.Views[s.Def.Key]
	rows := make([]Row, 0, len(items))
	for _, u := range items {
		cells, level := s.Def.Row(u, rc)
		if hasOv {
			cells = s.overrideCells(ov.Columns, u, cells)
		}
		rows = append(rows, Row{
			ID: u.GetNamespace() + "/" + u.GetName(), Cells: cells, Level: level,
			Namespace: u.GetNamespace(), Name: u.GetName(), Data: u,
		})
	}
	return rows, nil
}

func (s *ResourceSource) overrideCells(cols []config.ColumnOverride, u *unstructured.Unstructured, builtin []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		if c.Path != "" {
			out[i] = kube.FieldString(u, c.Path)
			continue
		}
		for j, bc := range s.Def.Columns {
			if strings.EqualFold(bc.Name, c.Name) && j < len(builtin) {
				out[i] = builtin[j]
			}
		}
	}
	return out
}

// ---- kubectl 실행 도우미 ----

func kubectlCmd(env *Env, args ...string) (string, []string) {
	base := env.Cfg.KubectlCommand()
	all := append(append([]string{}, base[1:]...), "--kubeconfig", env.Cfg.K3s.Kubeconfig)
	return base[0], append(all, args...)
}

func runKubectl(env *Env, args ...string) (string, error) {
	name, a := kubectlCmd(env, args...)
	out, err := executil.System{Timeout: env.Cfg.Tools.Timeout}.Run(context.Background(), name, a...)
	return string(out), err
}

func nsArgs(def *kube.ResourceDef, row Row) []string {
	if def.Namespaced && row.Namespace != "" {
		return []string{"-n", row.Namespace}
	}
	return nil
}

func obj(row Row) *unstructured.Unstructured {
	u, _ := row.Data.(*unstructured.Unstructured)
	return u
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// ---- 공통 작업 ----

func commonActions(def *kube.ResourceDef) []*Action {
	del := &Action{
		ID: "delete", Keys: []string{"x"}, Label: "삭제", Mutating: true, Confirm: ConfirmYesNo,
		ConfirmBody: func(env *Env, row Row) string {
			return fmt.Sprintf("%s %s 을(를) 삭제합니다.", def.Title, nsName(row))
		},
		Do: func(env *Env, row Row, _ string) (string, error) {
			c, cancel := ctx()
			defer cancel()
			return "삭제 요청됨: " + nsName(row), env.Kube.Delete(c, def, row.Namespace, row.Name)
		},
	}
	// 네임스페이스·노드 삭제는 영향이 커서 항상 이름을 다시 입력하게 합니다.
	if def.Key == "namespaces" || def.Key == "nodes" || def.Key == "persistentvolumes" || def.Key == "customresourcedefinitions" {
		del.Confirm = ConfirmType
	}
	return []*Action{
		{ID: "describe", Keys: []string{"d", "enter"}, Label: "상세",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				args := append([]string{"describe", def.Kind, row.Name}, nsArgs(def, row)...)
				return Push(NewTextPage(env, "describe "+def.Key+"/"+nsName(row), func() (string, error) {
					return runKubectl(env, args...)
				}))
			}},
		{ID: "yaml", Keys: []string{"y"}, Label: "YAML",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, "yaml "+def.Key+"/"+nsName(row), func() (string, error) {
					u := env.Store.Get(def.GVR, row.Namespace, row.Name)
					if u == nil {
						return "", fmt.Errorf("%s가 더 이상 존재하지 않습니다", nsName(row))
					}
					return kube.YAML(u, false)
				}).SetStyler(yamlStyler(env)))
			}},
		{ID: "edit", Keys: []string{"e"}, Label: "편집", Mutating: true,
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				name, args := kubectlCmd(env, append([]string{"edit", def.Kind, row.Name}, nsArgs(def, row)...)...)
				c := exec.Command(name, args...)
				c.Env = append(os.Environ(), "KUBE_EDITOR="+strings.Join(env.Cfg.EditorCommand(), " "))
				a := &Action{ID: "edit", Label: "편집", Mutating: true}
				target := Target(def.Key, row)
				return tea.ExecProcess(c, func(err error) tea.Msg {
					info := "편집 완료: " + nsName(row)
					if err != nil {
						err = fmt.Errorf("kubectl edit 실패 (변경 없음 또는 오류): %w", err)
					}
					return ActionDoneMsg{Action: a, Target: target, Info: info, Err: err, Mutating: true}
				})
			}},
		del,
	}
}

func nsName(r Row) string {
	if r.Namespace != "" {
		return r.Namespace + "/" + r.Name
	}
	return r.Name
}

// ---- 리소스별 작업 ----

func specificActions(def *kube.ResourceDef) []*Action {
	switch def.Key {
	case "pods":
		return podActions()
	case "deployments", "statefulsets", "replicasets":
		acts := []*Action{scaleAction(def)}
		if def.Key != "replicasets" {
			acts = append(acts, restartAction(def))
		}
		return acts
	case "daemonsets":
		return []*Action{restartAction(def)}
	case "cronjobs":
		return []*Action{{
			ID: "trigger", Keys: []string{"t"}, Label: "즉시 실행", Mutating: true, Confirm: ConfirmYesNo,
			ConfirmBody: func(_ *Env, row Row) string {
				return "CronJob " + nsName(row) + " 으로 Job을 지금 생성합니다."
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := ctx()
				defer cancel()
				job, err := env.Kube.TriggerCronJob(c, row.Namespace, row.Name)
				return "Job 생성: " + row.Namespace + "/" + job, err
			},
		}}
	case "nodes":
		return nodeActions()
	case "secrets":
		return []*Action{{
			ID: "reveal", Keys: []string{"v"}, Label: "값 보기",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				env.Audit.Record("secret.reveal", Target("secrets", row), "", nil)
				return Push(NewTextPage(env, "secret "+nsName(row)+" (복호화)", func() (string, error) {
					u := env.Store.Get(kube.GVRSecrets, row.Namespace, row.Name)
					if u == nil {
						return "", fmt.Errorf("Secret이 없습니다")
					}
					return kube.DecodeSecret(u), nil
				}))
			},
		}}
	case "services":
		return []*Action{servicePortForwardAction()}
	}
	return nil
}

func podContainers(_ *Env, row Row) []string {
	u := obj(row)
	if u == nil {
		return nil
	}
	main, init := kube.Containers(u)
	for _, c := range init {
		main = append(main, c+" (init)")
	}
	return main
}

func containerName(choice string) string {
	return strings.TrimSuffix(choice, " (init)")
}

func podActions() []*Action {
	logs := func(previous bool) func(env *Env, row Row, input string) tea.Cmd {
		return func(env *Env, row Row, input string) tea.Cmd {
			ctr := containerName(input)
			title := "logs " + nsName(row) + " [" + ctr + "]"
			if previous {
				title += " (previous)"
			}
			p := NewStreamPage(env, title, func(c context.Context) (<-chan string, error) {
				return env.Kube.StreamLogs(c, row.Namespace, row.Name, kube.LogOptions{
					Container: ctr, Follow: !previous, Previous: previous, TailLines: int64(env.Cfg.UI.LogTailLines),
				})
			})
			p.SetStyler(logStyler(env))
			return Push(p)
		}
	}
	return []*Action{
		{ID: "logs", Keys: []string{"l"}, Label: "로그", Choices: podContainers, Open: logs(false)},
		{ID: "logs_previous", Keys: []string{"L"}, Label: "이전 로그", Choices: podContainers, Open: logs(true)},
		{ID: "shell", Keys: []string{"s"}, Label: "셸", Mutating: true,
			Choices: func(env *Env, row Row) []string {
				u := obj(row)
				if u == nil {
					return nil
				}
				main, _ := kube.Containers(u)
				return main
			},
			Open: func(env *Env, row Row, input string) tea.Cmd {
				name, args := kubectlCmd(env, "exec", "-it", row.Name, "-n", row.Namespace, "-c", input, "--",
					"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh")
				c := exec.Command(name, args...)
				a := &Action{ID: "shell", Label: "셸", Mutating: true}
				target := Target("pods", row)
				return tea.ExecProcess(c, func(err error) tea.Msg {
					return ActionDoneMsg{Action: a, Target: target, Detail: "container=" + input, Info: "셸 종료: " + nsName(row), Err: err, Mutating: true}
				})
			}},
		{ID: "portforward", Keys: []string{"p"}, Label: "포트포워딩",
			Prompt: func(env *Env, row Row) (string, string) {
				initial := ""
				if u := obj(row); u != nil {
					p := kube.To[corev1.Pod](u)
					for _, c := range p.Spec.Containers {
						for _, cp := range c.Ports {
							if initial == "" {
								initial = fmt.Sprintf("%d:%d", cp.ContainerPort, cp.ContainerPort)
							}
						}
					}
				}
				return "포트포워딩 " + nsName(row) + " — 로컬:원격 (로컬 0 = 자동)", initial
			},
			Do: func(env *Env, row Row, input string) (string, error) {
				local, remote, err := kube.ParsePorts(input)
				if err != nil {
					return "", err
				}
				f, err := env.PF.Start("pod/"+nsName(row), row.Namespace, row.Name, local, remote)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("포트포워딩 #%d %s:%d → %s:%d", f.ID, f.Address, f.Local, f.Pod, f.Remote), nil
			}},
	}
}

func servicePortForwardAction() *Action {
	return &Action{ID: "portforward", Keys: []string{"p"}, Label: "포트포워딩",
		Prompt: func(env *Env, row Row) (string, string) {
			initial := ""
			if u := obj(row); u != nil {
				s := kube.To[corev1.Service](u)
				if len(s.Spec.Ports) > 0 {
					initial = fmt.Sprintf("%d:%d", s.Spec.Ports[0].Port, s.Spec.Ports[0].Port)
				}
			}
			return "포트포워딩 svc/" + nsName(row) + " — 로컬:서비스포트", initial
		},
		Do: func(env *Env, row Row, input string) (string, error) {
			local, svcPort, err := kube.ParsePorts(input)
			if err != nil {
				return "", err
			}
			c, cancel := ctx()
			defer cancel()
			pod, podPort, err := env.PF.ResolveServicePort(c, row.Namespace, row.Name, svcPort)
			if err != nil {
				return "", err
			}
			f, err := env.PF.Start("svc/"+nsName(row), row.Namespace, pod, local, podPort)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("포트포워딩 #%d %s:%d → svc/%s:%d", f.ID, f.Address, f.Local, row.Name, svcPort), nil
		}}
}

func currentReplicas(row Row) int {
	u := obj(row)
	if u == nil {
		return 1
	}
	v, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if !found {
		return 1
	}
	return int(v)
}

func scaleAction(def *kube.ResourceDef) *Action {
	return &Action{ID: "scale", Keys: []string{"S"}, Label: "스케일", Mutating: true,
		Prompt: func(_ *Env, row Row) (string, string) {
			return "스케일 " + def.Title + " " + nsName(row) + " — replicas", strconv.Itoa(currentReplicas(row))
		},
		Do: func(env *Env, row Row, input string) (string, error) {
			n, err := strconv.Atoi(input)
			if err != nil || n < 0 || n > 1000 {
				return "", fmt.Errorf("replicas는 0~1000 사이 정수여야 합니다: %q", input)
			}
			c, cancel := ctx()
			defer cancel()
			return fmt.Sprintf("%s replicas=%d", nsName(row), n), env.Kube.Scale(c, def, row.Namespace, row.Name, n)
		}}
}

func restartAction(def *kube.ResourceDef) *Action {
	return &Action{ID: "restart", Keys: []string{"r"}, Label: "롤아웃 재시작", Mutating: true, Confirm: ConfirmYesNo,
		ConfirmBody: func(_ *Env, row Row) string {
			return def.Title + " " + nsName(row) + " 의 Pod를 순차적으로 재시작합니다."
		},
		Do: func(env *Env, row Row, _ string) (string, error) {
			c, cancel := ctx()
			defer cancel()
			return "재시작 요청됨: " + nsName(row), env.Kube.RolloutRestart(c, def, row.Namespace, row.Name)
		}}
}

func nodeActions() []*Action {
	unsched := func(row Row) bool {
		u := obj(row)
		if u == nil {
			return false
		}
		v, _, _ := unstructured.NestedBool(u.Object, "spec", "unschedulable")
		return v
	}
	return []*Action{
		{ID: "cordon", Keys: []string{"c"}, Label: "cordon", Mutating: true, Confirm: ConfirmYesNo,
			Available: func(_ *Env, row Row) bool { return !unsched(row) },
			ConfirmBody: func(_ *Env, row Row) string {
				return "노드 " + row.Name + " 에 새 Pod가 배치되지 않도록 합니다."
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := ctx()
				defer cancel()
				return "cordon: " + row.Name, env.Kube.SetUnschedulable(c, row.Name, true)
			}},
		{ID: "uncordon", Keys: []string{"u"}, Label: "uncordon", Mutating: true, Confirm: ConfirmYesNo,
			Available: func(_ *Env, row Row) bool { return unsched(row) },
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := ctx()
				defer cancel()
				return "uncordon: " + row.Name, env.Kube.SetUnschedulable(c, row.Name, false)
			}},
		{ID: "drain", Keys: []string{"D"}, Label: "drain", Mutating: true, Confirm: ConfirmType,
			ConfirmBody: func(env *Env, row Row) string {
				msg := "노드 " + row.Name + " 를 cordon하고 DaemonSet을 제외한 Pod를 모두 축출합니다.\n" +
					"(emptyDir 데이터는 삭제되며, 컨트롤러 없는 Pod가 있으면 중단합니다)"
				if env.Store != nil && len(env.Store.List(kube.GVRNodes, "")) <= 1 {
					msg += "\n\n⚠ 단일 노드 클러스터입니다. 축출된 Pod는 다시 배치될 곳이 없습니다."
				}
				return msg
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				err := env.Kube.Drain(c, row.Name, false, func(s string) {
					if env.Send != nil {
						env.Send(ToastMsg{Text: "drain: " + s})
					}
				})
				return "drain 완료: " + row.Name, err
			}},
	}
}

// ---- 스타일 도우미 ----

func yamlStyler(env *Env) func(string) string {
	st := env.Styles
	return func(l string) string {
		if strings.HasPrefix(strings.TrimLeft(l, " "), "#") {
			return st.Muted.Render(l)
		}
		i := strings.Index(l, ":")
		if i <= 0 {
			return l
		}
		key := strings.TrimLeft(l[:i], " -")
		if key == "" || strings.ContainsAny(key, " \"'") {
			return l
		}
		return st.Info.Render(l[:i]) + l[i:]
	}
}

func logStyler(env *Env) func(string) string {
	st := env.Styles
	return func(l string) string {
		low := strings.ToLower(l)
		switch {
		case strings.Contains(low, "error") || strings.Contains(low, "level=fatal") || strings.Contains(low, "panic"):
			return st.Err.Render(l)
		case strings.Contains(low, "warn"):
			return st.Warn.Render(l)
		}
		return l
	}
}

// deploymentReady는 대시보드에서 준비된 Deployment 수를 셀 때 씁니다.
func deploymentReady(u *unstructured.Unstructured) bool {
	d := kube.To[appsv1.Deployment](u)
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	return d.Status.ReadyReplicas >= want
}
