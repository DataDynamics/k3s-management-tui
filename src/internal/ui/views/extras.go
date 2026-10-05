package views

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// ==== Helm 릴리스 ====

type helmReleaseSource struct{ baseSource }

func newHelmReleaseSource() Source {
	s := &helmReleaseSource{baseSource{key: "releases", title: "Releases"}}
	text := func(id, key, label string, f func(env *Env, ns, name string) (string, error)) *Action {
		return &Action{ID: id, Keys: []string{key}, Label: label,
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, label+" "+nsName(row), func() (string, error) {
					return f(env, row.Namespace, row.Name)
				}).SetStyler(yamlStyler(env)))
			}}
	}
	s.actions = []*Action{
		text("helm_values", "v", "values", func(env *Env, ns, n string) (string, error) {
			return env.Helm.Values(context.Background(), ns, n, false)
		}),
		text("helm_values_all", "a", "전체 values", func(env *Env, ns, n string) (string, error) {
			return env.Helm.Values(context.Background(), ns, n, true)
		}),
		text("helm_manifest", "m", "manifest", func(env *Env, ns, n string) (string, error) {
			return env.Helm.Manifest(context.Background(), ns, n)
		}),
		text("helm_history", "h", "이력", func(env *Env, ns, n string) (string, error) {
			return env.Helm.History(context.Background(), ns, n)
		}),
		{ID: "helm_rollback", Keys: []string{"R"}, Label: "롤백", Mutating: true, Confirm: ConfirmYesNo,
			Prompt: func(_ *Env, row Row) (string, string) {
				rev, _ := strconv.Atoi(row.Data.(helmRelease).Revision)
				return "롤백할 리비전 (" + nsName(row) + ", 현재 " + strconv.Itoa(rev) + ")", strconv.Itoa(max(1, rev-1))
			},
			ConfirmBody: func(_ *Env, row Row) string {
				return "helm rollback " + row.Name + " -n " + row.Namespace + " --wait 를 실행합니다."
			},
			Do: func(env *Env, row Row, input string) (string, error) {
				rev, err := strconv.Atoi(input)
				if err != nil {
					return "", fmt.Errorf("리비전은 숫자여야 합니다: %q", input)
				}
				c, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
				defer cancel()
				return fmt.Sprintf("롤백 완료: %s → rev %d", nsName(row), rev), env.Helm.Rollback(c, row.Namespace, row.Name, rev)
			}},
		{ID: "helm_uninstall", Keys: []string{"x"}, Label: "삭제", Mutating: true, Confirm: ConfirmType,
			ConfirmBody: func(_ *Env, row Row) string {
				return "Helm 릴리스 " + nsName(row) + " 와 배포된 리소스를 모두 삭제합니다 (helm uninstall)."
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				return "삭제: " + nsName(row), env.Helm.Uninstall(c, row.Namespace, row.Name)
			}},
	}
	return s
}

type helmRelease struct{ Revision string }

func (s *helmReleaseSource) Namespaced() bool           { return true }
func (s *helmReleaseSource) AutoRefresh() time.Duration { return 15 * time.Second }
func (s *helmReleaseSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "NAME"}, {Name: "REVISION"}, {Name: "STATUS"}, {Name: "CHART"}, {Name: "APP VERSION"}, {Name: "UPDATED"}}
}

func (s *helmReleaseSource) Load(env *Env) ([]Row, error) {
	c, cancel := ctx()
	defer cancel()
	rels, err := env.Helm.List(c)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, r := range rels {
		if env.Namespace != "" && r.Namespace != env.Namespace {
			continue
		}
		level := kube.LevelOK
		switch r.Status {
		case "deployed":
		case "failed":
			level = kube.LevelErr
		default:
			level = kube.LevelWarn
		}
		updated := r.Updated
		if len(updated) > 19 {
			updated = updated[:19]
		}
		rows = append(rows, Row{ID: r.Namespace + "/" + r.Name, Namespace: r.Namespace, Name: r.Name, Level: level,
			Data:  helmRelease{Revision: r.Revision},
			Cells: []string{r.Name, r.Revision, r.Status, r.Chart, r.AppVersion, updated}})
	}
	return rows, nil
}

// ==== port-forward 목록 ====

type portForwardSource struct{ baseSource }

func newPortForwardSource() Source {
	s := &portForwardSource{baseSource{key: "portforwards", title: "Port-forwards"}}
	s.actions = []*Action{
		{ID: "stop_forward", Keys: []string{"x"}, Label: "중지",
			Do: func(env *Env, row Row, _ string) (string, error) {
				id, _ := strconv.Atoi(row.ID)
				return "포트포워딩 #" + row.ID + " 중지", env.PF.Stop(id)
			}},
	}
	return s
}

func (s *portForwardSource) Async() bool { return false }
func (s *portForwardSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "ID"}, {Name: "TARGET"}, {Name: "POD"}, {Name: "LOCAL"}, {Name: "REMOTE"}, {Name: "STATUS"}, {Name: "AGE"}, {Name: "ERROR", MaxWidth: 80}}
}

func (s *portForwardSource) Summary(env *Env) string {
	return "Workloads›Pods 또는 Network›Services에서 p 키로 시작합니다. TUI를 종료하면 모두 중지됩니다."
}

func (s *portForwardSource) Load(env *Env) ([]Row, error) {
	if env.PF == nil {
		return nil, nil
	}
	now := env.NowTime()
	var rows []Row
	for _, f := range env.PF.List() {
		level := kube.LevelOK
		if f.Status != "active" {
			level = kube.LevelErr
		}
		rows = append(rows, Row{ID: strconv.Itoa(f.ID), Name: f.Target, Level: level,
			Cells: []string{strconv.Itoa(f.ID), f.Target, f.Pod, fmt.Sprintf("%s:%d", f.Address, f.Local),
				strconv.Itoa(f.Remote), f.Status, kube.Age(f.Started, now), f.Error}})
	}
	return rows, nil
}

// ==== Cilium 에이전트 ====

type ciliumSource struct{ baseSource }

func newCiliumSource() Source {
	s := &ciliumSource{baseSource{key: "cilium", title: "Cilium"}}
	status := func(id, key, label string, args ...string) *Action {
		return &Action{ID: id, Keys: []string{key}, Label: label,
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				full := append([]string{"exec", "-n", row.Namespace, row.Name, "-c", "cilium-agent", "--", "cilium-dbg"}, args...)
				return Push(NewTextPage(env, "cilium-dbg "+strings.Join(args, " ")+" @ "+row.Name, func() (string, error) {
					return runKubectl(env, full...)
				}))
			}}
	}
	s.actions = []*Action{
		status("cilium_status", "enter", "status", "status", "--verbose"),
		status("cilium_health", "h", "health", "status", "--all-health"),
		status("cilium_endpoints", "e", "endpoints", "endpoint", "list"),
		status("cilium_services", "v", "services", "service", "list"),
	}
	return s
}

func (s *ciliumSource) Async() bool { return false }
func (s *ciliumSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "POD"}, {Name: "STATUS"}, {Name: "RESTARTS"}, {Name: "NODE"}, {Name: "AGE"}}
}

func (s *ciliumSource) Summary(env *Env) string {
	return "Cilium 에이전트 Pod — Enter: status, h: health, e: endpoints, v: services (cilium-dbg)"
}

func (s *ciliumSource) Load(env *Env) ([]Row, error) {
	if env.Store == nil {
		return nil, env.KubeErr
	}
	rc := kube.RowCtx{Now: env.NowTime(), Metrics: env.Metrics}
	var rows []Row
	for _, u := range env.Store.List(kube.GVRPods, "") {
		if u.GetLabels()["k8s-app"] != "cilium" {
			continue
		}
		cells, level := kube.Def("pods").Row(u, rc)
		// pods 컬럼: NAME READY STATUS RESTARTS CPU MEM IP NODE AGE
		rows = append(rows, Row{ID: u.GetNamespace() + "/" + u.GetName(), Namespace: u.GetNamespace(), Name: u.GetName(),
			Level: level, Data: u, Cells: []string{cells[0], cells[2], cells[3], cells[7], cells[8]}})
	}
	return rows, nil
}

// ==== local-path 실제 사용량 ====

type localPathSource struct{ baseSource }

func newLocalPathSource() Source {
	return &localPathSource{baseSource{key: "localpath", title: "Local-path 사용량"}}
}

func (s *localPathSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "DIRECTORY", MaxWidth: 90}, {Name: "PVC"}, {Name: "USED"}, {Name: "MODIFIED"}}
}

func (s *localPathSource) Summary(env *Env) string {
	return "local-path-provisioner 저장 경로의 실제 디스크 사용량 (ctrl+r로 다시 계산)"
}

// Load는 local-path-config ConfigMap에서 저장 경로를 읽어 디렉터리별 사용량을 계산합니다.
func (s *localPathSource) Load(env *Env) ([]Row, error) {
	if env.Kube == nil {
		return nil, env.KubeErr
	}
	c, cancel := ctx()
	defer cancel()
	paths := map[string]bool{}
	if cm, err := env.Kube.Core.CoreV1().ConfigMaps("kube-system").Get(c, "local-path-config", metav1.GetOptions{}); err == nil {
		var conf struct {
			NodePathMap []struct {
				Paths []string `json:"paths"`
			} `json:"nodePathMap"`
		}
		if json.Unmarshal([]byte(cm.Data["config.json"]), &conf) == nil {
			for _, n := range conf.NodePathMap {
				for _, p := range n.Paths {
					paths[p] = true
				}
			}
		}
	}
	if len(paths) == 0 {
		paths[filepath.Join(env.Host.DataDir(), "storage")] = true
	}
	var rows []Row
	deadline := time.Now().Add(30 * time.Second)
	for root := range paths {
		entries, err := os.ReadDir(root)
		if err != nil {
			rows = append(rows, Row{ID: root, Name: root, Level: kube.LevelWarn, Cells: []string{root, "", "", err.Error()}})
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			size, partial := dirSize(dir, deadline)
			used := kube.FormatBytes(size)
			if partial {
				used += "+ (시간 초과)"
			}
			mod := ""
			if info, err := e.Info(); err == nil {
				mod = info.ModTime().Format("2006-01-02 15:04")
			}
			rows = append(rows, Row{ID: dir, Name: e.Name(), Data: size, Cells: []string{dir, pvcFromDir(e.Name()), used, mod}})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, _ := rows[i].Data.(int64)
		b, _ := rows[j].Data.(int64)
		return a > b
	})
	return rows, nil
}

// pvcFromDir는 "pvc-<uid>_<ns>_<name>" 형식에서 ns/name을 꺼냅니다.
func pvcFromDir(name string) string {
	parts := strings.SplitN(name, "_", 3)
	if len(parts) == 3 && strings.HasPrefix(parts[0], "pvc-") {
		return parts[1] + "/" + parts[2]
	}
	return ""
}

func dirSize(root string, deadline time.Time) (int64, bool) {
	var total int64
	partial := false
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			partial = true
			return fs.SkipAll
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, partial
}
