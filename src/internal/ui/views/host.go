package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/DataDynamics/k3s-management-tui/internal/host"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
	"github.com/DataDynamics/k3s-management-tui/internal/textdiff"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
)

// Summary를 구현한 소스는 테이블 위에 요약 한 줄을 보여줍니다.
type summarizer interface{ Summary(env *Env) string }

func kv(key, value string, level kube.Level) Row {
	return Row{ID: key, Cells: []string{key, value}, Level: level, Name: key}
}

// ==== 서비스 ====

type serviceSource struct{ baseSource }

func newServiceSource(h host.Host) Source {
	s := &serviceSource{baseSource{key: "service", title: "Service"}}
	s.actions = serviceActions(h)
	return s
}

func (s *serviceSource) AutoRefresh() time.Duration { return 5 * time.Second }
func (s *serviceSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "ITEM"}, {Name: "VALUE", MaxWidth: 160}}
}

func (s *serviceSource) Load(env *Env) ([]Row, error) {
	h := env.Host
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rows []Row
	st, err := h.ServiceStatus(c)
	if err != nil {
		rows = append(rows, kv("service", "조회 실패: "+err.Error(), kube.LevelErr))
	} else {
		level := kube.LevelOK
		if !st.Active() {
			level = kube.LevelErr
		}
		rows = append(rows, kv("service", fmt.Sprintf("%s.service  %s (%s)  [%s]", st.Unit, st.ActiveState, st.SubState, st.UnitFileState), level))
		if !st.Since.IsZero() {
			rows = append(rows, kv("since", st.Since.Format("2006-01-02 15:04:05")+"  (uptime "+kube.HumanDuration(env.NowTime().Sub(st.Since))+")", kube.LevelNone))
		}
		rows = append(rows, kv("main pid", fmt.Sprint(st.MainPID), kube.LevelNone))
		if st.MemoryBytes >= 0 {
			rows = append(rows, kv("memory (cgroup)", kube.FormatBytes(st.MemoryBytes), kube.LevelNone))
		}
		rows = append(rows, kv("tasks", fmt.Sprint(st.Tasks), kube.LevelNone))
		rl := kube.LevelNone
		if st.Restarts > 0 {
			rl = kube.LevelWarn
		}
		rows = append(rows, kv("restarts", fmt.Sprint(st.Restarts), rl))
	}
	if v, err := h.Version(c); err == nil {
		rows = append(rows, kv("version", v, kube.LevelNone))
	}
	if env.Kube != nil {
		ping := "ok"
		pl := kube.LevelOK
		if err := env.Kube.Ping(c); err != nil {
			ping, pl = err.Error(), kube.LevelErr
		}
		rows = append(rows, kv("api server", env.Kube.ServerVersion+"  readyz="+ping, pl))
	} else if env.KubeErr != nil {
		rows = append(rows, kv("api server", env.KubeErr.Error(), kube.LevelErr))
	}
	rows = append(rows, kv("config file", h.ConfigPath(), kube.LevelNone))
	if kc, err := h.LoadConfig(); err == nil {
		if len(kc.DropIns) > 0 {
			rows = append(rows, kv("config drop-ins", strings.Join(kc.DropIns, ", "), kube.LevelNone))
		}
		if d := kc.Strings("disable"); len(d) > 0 {
			rows = append(rows, kv("disabled", strings.Join(d, ", "), kube.LevelMuted))
		}
		if fb := kc.String("flannel-backend"); fb != "" {
			rows = append(rows, kv("flannel-backend", fb, kube.LevelMuted))
		}
	}
	ds := h.Datastore()
	dsv := string(ds.Kind)
	switch ds.Kind {
	case host.DatastoreSQLite:
		dsv += "  " + ds.Path + "  (" + kube.FormatBytes(ds.Size) + ")"
	case host.DatastoreEtcd:
		dsv += "  " + ds.Path
	case host.DatastoreExternal:
		dsv += "  " + ds.Endpoint
	}
	rows = append(rows, kv("datastore", dsv, kube.LevelNone))
	for _, d := range h.DiskUsage() {
		if d.Err != "" {
			rows = append(rows, kv("disk "+d.Label, d.Path+"  "+d.Err, kube.LevelWarn))
			continue
		}
		dl := kube.LevelNone
		switch {
		case d.Percent() >= 90:
			dl = kube.LevelErr
		case d.Percent() >= 80:
			dl = kube.LevelWarn
		}
		rows = append(rows, kv("disk "+d.Label, fmt.Sprintf("%s  %s %s/%s (%.0f%%)", d.Path, bar(d.Percent(), 20),
			kube.FormatBytes(int64(d.Used)), kube.FormatBytes(int64(d.Total)), d.Percent()), dl))
	}
	if !h.IsRoot() {
		rows = append(rows, kv("권한", "root가 아니므로 서비스 제어·설정 편집·백업이 비활성화됩니다", kube.LevelWarn))
	}
	return rows, nil
}

// bar는 텍스트 막대 그래프입니다.
func bar(pct float64, width int) string {
	n := int(pct/100*float64(width) + 0.5)
	n = min(max(n, 0), width)
	return "[" + strings.Repeat("█", n) + strings.Repeat("·", width-n) + "]"
}

// serviceControl은 노드 서비스(k3s, kubelet) 제어 작업입니다. 중지·재시작은 서비스 이름을 다시 입력해야 합니다.
func serviceControl(op host.ServiceOp, key, label, unit string) *Action {
	a := &Action{ID: "service_" + string(op), Keys: []string{key}, Label: label, Mutating: true, NeedRoot: true, NoRow: true,
		Confirm: ConfirmType,
		Expect:  func(Row) string { return unit },
		ConfirmBody: func(env *Env, _ Row) string {
			msg := fmt.Sprintf("systemctl %s %s 을(를) 실행합니다.", op, unit)
			if hint := env.Host.Caps().ServiceHint; op != host.OpStart && hint != "" {
				msg += "\n\n" + hint
			}
			return msg
		},
		Do: func(env *Env, _ Row, _ string) (string, error) {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			return fmt.Sprintf("%s %s 완료", unit, op), env.Host.ServiceControl(c, op)
		}}
	if op == host.OpStart {
		a.Confirm = ConfirmYesNo
	}
	return a
}

func journalAction() *Action {
	return &Action{ID: "journal", Keys: []string{"l"}, Label: "서비스 로그", NoRow: true,
		Open: func(env *Env, _ Row, _ string) tea.Cmd {
			p := NewStreamPage(env, "journalctl -u "+env.Host.ServiceName(), func(c context.Context) (<-chan string, error) {
				return env.Host.StreamJournal(c, env.Cfg.UI.LogTailLines, true)
			})
			st := env.Styles
			p.SetStyler(func(l string) string {
				switch host.JournalLevel(l) {
				case "error":
					return st.Err.Render(l)
				case "warn":
					return st.Warn.Render(l)
				}
				return l
			})
			return Push(p)
		}}
}

// controlPlaneIP는 노드 추가 명령에 넣을 서버 노드 IP입니다.
// control-plane 라벨이 있는 노드 → 이 호스트와 이름이 같은 노드 → 첫 노드 순으로 고릅니다.
// (RKE2는 노드가 Ready가 되기 전에는 control-plane 라벨이 없습니다)
func controlPlaneIP(env *Env) string {
	if env.Store == nil {
		return "<서버 IP>"
	}
	hostname, _ := os.Hostname()
	var byName, first string
	for _, u := range env.Store.List(kube.GVRNodes, "") {
		n := kube.To[corev1.Node](u)
		ip := kube.NodeInternalIP(n)
		if ip == "" {
			continue
		}
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
			return ip
		}
		if n.Name == hostname && byName == "" {
			byName = ip
		}
		if first == "" {
			first = ip
		}
	}
	switch {
	case byName != "":
		return byName
	case first != "":
		return first
	}
	return "<서버 IP>"
}

func joinAction(h host.Host) *Action {
	caps := h.Caps()
	a := &Action{ID: "join_command", Keys: []string{"J"}, Label: caps.JoinLabel, NoRow: true, NeedRoot: true}
	open := func(env *Env, _ Row, _ string) tea.Cmd {
		return Push(NewTextPage(env, caps.JoinLabel, func() (string, error) {
			c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd, err := env.Host.JoinCommand(c, controlPlaneIP(env))
			if caps.JoinMutates {
				env.Audit.Record("join.token.create", env.Host.Distro(), "", err)
			} else {
				env.Audit.Record("join.token.view", env.Host.Distro(), "", err)
			}
			if err != nil {
				return "", err
			}
			return caps.JoinHint + "\n\n" + cmd + "\n\n" +
				"⚠ 이 명령의 토큰으로 노드가 클러스터에 합류할 수 있습니다. 화면 공유·기록에 주의하세요.\n", nil
		}).SetWrap(true))
	}
	a.Open = open
	if caps.JoinMutates {
		// kubeadm은 명령을 볼 때마다 클러스터에 새 부트스트랩 토큰을 만듭니다.
		a.Mutating, a.Confirm = true, ConfirmYesNo
		a.ConfirmBody = func(*Env, Row) string { return caps.JoinHint }
	}
	return a
}

func serviceActions(h host.Host) []*Action {
	caps, unit := h.Caps(), h.ServiceName()
	acts := []*Action{
		journalAction(),
		serviceControl(host.OpRestart, "r", "재시작", unit),
		serviceControl(host.OpStop, "t", "중지", unit),
		serviceControl(host.OpStart, "a", "시작", unit),
	}
	if caps.CheckLabel != "" {
		label := caps.CheckLabel
		acts = append(acts, &Action{ID: "check_config", Keys: []string{"c"}, Label: label, NoRow: true,
			Open: func(env *Env, _ Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, label, func() (string, error) {
					c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					return env.Host.CheckConfig(c)
				}))
			}})
	}
	if caps.JoinLabel != "" {
		acts = append(acts, joinAction(h))
	}
	return acts
}

// ==== 노드 설정 파일 (K3S config.yaml, kubelet config) ====

type configSource struct{ baseSource }

func newConfigSource(h host.Host) Source {
	caps, unit := h.Caps(), h.ServiceName()
	s := &configSource{baseSource{key: "nodeconfig", title: caps.ConfigLabel}}
	s.actions = []*Action{
		{ID: "view_file", Keys: []string{"v", "enter"}, Label: "파일 보기", NoRow: true,
			Open: func(env *Env, _ Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, env.Host.ConfigPath(), func() (string, error) {
					return env.Host.ReadFile(env.Host.ConfigPath())
				}).SetStyler(yamlStyler(env)))
			}},
		{ID: "edit_config", Keys: []string{"e"}, Label: "편집", Mutating: true, NeedRoot: true, NoRow: true,
			Open: func(env *Env, _ Row, _ string) tea.Cmd {
				path := env.Host.ConfigPath()
				orig, err := env.Host.ReadFile(path)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return Toast(err.Error(), true)
				}
				return editFile(env, editSpec{
					path: path, audit: "config.edit",
					validate: env.Host.ValidateConfig, save: env.Host.WriteConfig,
					after: func(env *Env, backup string) tea.Msg {
						if !caps.ConfigRestartService {
							return ToastMsg{Text: "저장했습니다 (백업: " + backup + ")"}
						}
						restart := serviceControl(host.OpRestart, "", unit+" 재시작", unit)
						restart.Confirm = ConfirmNone
						body := "저장했습니다.\n백업: " + backup + "\n\n설정은 " + unit + "을(를) 재시작해야 적용됩니다. 지금 재시작할까요?"
						return DialogMsg{Dialog: components.NewConfirmType(unit+" 재시작", body, unit, func(string) tea.Cmd {
							return func() tea.Msg { return ActionRequestMsg{Action: restart, Source: "service"} }
						})}
					},
				}, orig, orig)
			}},
		serviceControl(host.OpRestart, "r", unit+" 재시작", unit),
	}
	return s
}

func (s *configSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "KEY"}, {Name: "VALUE", MaxWidth: 160}}
}

func (s *configSource) Summary(env *Env) string {
	p := env.Host.ConfigPath()
	if t := host.ModTime(p); !t.IsZero() {
		return p + "  (수정: " + t.Format("2006-01-02 15:04") + ")  — " + env.Host.Caps().ConfigHint
	}
	return p + " (파일 없음 — 편집하면 새로 만듭니다)"
}

func (s *configSource) Load(env *Env) ([]Row, error) {
	kc, err := env.Host.LoadConfig()
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, k := range kc.Keys() {
		rows = append(rows, kv(k, kc.Display(k), kube.LevelNone))
	}
	return rows, nil
}

// editSpec은 외부 편집기로 노드 파일을 고치는 절차의 설정입니다.
type editSpec struct {
	path     string
	audit    string                                  // 감사 로그 작업 이름
	validate func([]byte) error                      // 저장 전 검증
	save     func([]byte) (backup string, err error) // 백업 후 저장
	after    func(env *Env, backup string) tea.Msg   // 저장 후 안내 (재시작 질의 등)
}

// editFile은 임시 파일을 외부 편집기로 연 뒤 검증 → diff 확인 → 백업 후 저장 → 후속 안내 순으로 진행합니다.
// 검증에 실패하면 고친 내용을 그대로 다시 편집할 수 있습니다.
func editFile(env *Env, spec editSpec, orig, content string) tea.Cmd {
	f, err := os.CreateTemp("", "k3stui-*-"+filepath.Base(spec.path))
	if err != nil {
		return Toast(err.Error(), true)
	}
	tmp := f.Name()
	_, _ = f.WriteString(content)
	f.Close()
	editor := env.Cfg.EditorCommand()
	c := exec.Command(editor[0], append(editor[1:], tmp)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		data, rerr := os.ReadFile(tmp)
		os.Remove(tmp)
		if err != nil {
			return ToastMsg{Text: "편집기 실행 실패: " + err.Error(), Err: true}
		}
		if rerr != nil {
			return ToastMsg{Text: rerr.Error(), Err: true}
		}
		edited := string(data)
		if edited == orig {
			return ToastMsg{Text: "변경 사항이 없습니다"}
		}
		if verr := spec.validate(data); verr != nil {
			return DialogMsg{Dialog: components.NewConfirm("검증 실패 — 다시 편집할까요?", verr.Error(),
				func(string) tea.Cmd { return editFile(env, spec, orig, edited) })}
		}
		diff := textdiff.Unified(spec.path, spec.path+" (편집본)", orig, edited, 3)
		return DialogMsg{Dialog: components.NewConfirm(filepath.Base(spec.path)+" 변경을 저장할까요?", diff,
			func(string) tea.Cmd {
				return func() tea.Msg {
					backup, err := spec.save(data)
					env.Audit.Record(spec.audit, spec.path, "backup="+backup, err)
					if err != nil {
						return ToastMsg{Text: "저장 실패: " + err.Error(), Err: true}
					}
					return spec.after(env, backup)
				}
			})}
	})
}

// ==== manifest (K3S 자동 배포, kubeadm static Pod) ====

type manifestSource struct {
	baseSource
	skip bool
}

func newManifestSource(h host.Host) Source {
	caps := h.Caps()
	s := &manifestSource{baseSource: baseSource{key: "manifests", title: caps.ManifestsLabel}, skip: caps.ManifestSkip}
	s.actions = []*Action{
		{ID: "view_file", Keys: []string{"v", "enter"}, Label: "보기",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				m := row.Data.(host.Manifest)
				return Push(NewTextPage(env, m.Path, func() (string, error) { return env.Host.ReadFile(m.Path) }).
					SetStyler(yamlStyler(env)))
			}},
	}
	if caps.ManifestEdit {
		s.actions = append(s.actions, &Action{ID: "edit_manifest", Keys: []string{"e"}, Label: "편집", Mutating: true, NeedRoot: true,
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				m := row.Data.(host.Manifest)
				orig, err := env.Host.ReadFile(m.Path)
				if err != nil {
					return Toast(err.Error(), true)
				}
				return editFile(env, editSpec{
					path: m.Path, audit: "manifest.edit",
					validate: func(b []byte) error { return host.ValidateYAML(b) },
					save:     func(b []byte) (string, error) { return env.Host.WriteManifest(m.Path, b) },
					after: func(_ *Env, backup string) tea.Msg {
						return ToastMsg{Text: "저장했습니다 (백업: " + backup + "). kubelet이 " + m.Rel + " Pod를 다시 만듭니다"}
					},
				}, orig, orig)
			}})
	}
	if caps.ManifestSkip {
		s.actions = append(s.actions, &Action{ID: "toggle_skip", Keys: []string{"s"}, Label: "skip 전환", Mutating: true, NeedRoot: true, Confirm: ConfirmYesNo,
			ConfirmBody: func(env *Env, row Row) string {
				m := row.Data.(host.Manifest)
				name := kube.DistroName(env.Host.Distro())
				if m.Skipped {
					return m.Rel + ".skip 을 지워 " + name + "가 다시 이 manifest를 배포하게 합니다."
				}
				return m.Rel + ".skip 을 만들어 " + name + "가 이 manifest를 더 이상 적용하지 않게 합니다.\n(이미 배포된 리소스는 지워지지 않습니다)"
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				m := row.Data.(host.Manifest)
				err := env.Host.SetManifestSkip(m.Path, !m.Skipped)
				if m.Skipped {
					return "skip 해제: " + m.Rel, err
				}
				return "skip 설정: " + m.Rel, err
			}})
	}
	return s
}

func (s *manifestSource) Columns(*Env) []kube.Column {
	cols := []kube.Column{{Name: "FILE"}, {Name: "SIZE"}, {Name: "MODIFIED"}}
	if s.skip {
		cols = append(cols, kube.Column{Name: "SKIP"})
	}
	return cols
}

func (s *manifestSource) Summary(env *Env) string {
	return env.Host.ManifestsDir() + " — " + env.Host.Caps().ManifestsHint
}

func (s *manifestSource) Load(env *Env) ([]Row, error) {
	list, err := env.Host.Manifests()
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(list))
	for _, m := range list {
		level := kube.LevelNone
		cells := []string{m.Rel, kube.FormatBytes(m.Size), m.ModTime.Format("2006-01-02 15:04")}
		if s.skip {
			skip := ""
			if m.Skipped {
				skip, level = "skip", kube.LevelMuted
			}
			cells = append(cells, skip)
		}
		rows = append(rows, Row{ID: m.Path, Name: m.Rel, Data: m, Level: level, Cells: cells})
	}
	return rows, nil
}

// ==== 인증서 ====

type certSource struct{ baseSource }

func newCertSource(h host.Host) Source {
	caps := h.Caps()
	s := &certSource{baseSource{key: "certs", title: "Certificates"}}
	s.actions = []*Action{
		{ID: "view_cert", Keys: []string{"v", "enter"}, Label: "상세",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				ci := row.Data.(host.CertInfo)
				return Push(NewTextPage(env, ci.Path, func() (string, error) { return host.DescribeCertFile(ci.Path) }))
			}},
	}
	if caps.CertRotateHint != "" {
		s.actions = append(s.actions, &Action{ID: "rotate_certs", Keys: []string{"R"}, Label: "인증서 갱신",
			Mutating: true, NeedRoot: true, NoRow: true, Confirm: ConfirmType,
			Expect:      func(Row) string { return "renew" },
			ConfirmBody: func(*Env, Row) string { return caps.CertRotateHint },
			Do: func(env *Env, _ Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				return "인증서 갱신 완료", env.Host.RotateCertificates(c, progressFn(env, "인증서 갱신"))
			}})
	}
	return s
}

func progressFn(env *Env, prefix string) func(string) {
	return func(s string) {
		if env.Send != nil {
			env.Send(ToastMsg{Text: prefix + ": " + s})
		}
	}
}

func (s *certSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "FILE"}, {Name: "SUBJECT", MaxWidth: 40}, {Name: "ISSUER", MaxWidth: 40}, {Name: "NOT AFTER"}, {Name: "DAYS"}}
}

func (s *certSource) Summary(env *Env) string { return env.Host.Caps().CertHint }

func (s *certSource) Load(env *Env) ([]Row, error) {
	list, err := env.Host.Certificates()
	if err != nil {
		return nil, err
	}
	now := env.NowTime()
	rows := make([]Row, 0, len(list))
	for _, c := range list {
		rel := c.Label
		days := c.DaysLeft(now)
		level := kube.LevelNone
		switch {
		case days < 0:
			level = kube.LevelErr
		case days < 30:
			level = kube.LevelWarn
		}
		subj := c.Subject
		if c.IsCA {
			subj += " (CA)"
		}
		rows = append(rows, Row{ID: c.Path + "#" + rel, Name: rel, Data: c, Level: level,
			Cells: []string{rel, subj, c.Issuer, c.NotAfter.Local().Format("2006-01-02"), fmt.Sprint(days)}})
	}
	return rows, nil
}

// ==== 데이터스토어 백업 ====

type backupSource struct {
	baseSource
	extra string
}

func newBackupSource(h host.Host) Source {
	caps := h.Caps()
	s := &backupSource{baseSource: baseSource{key: "backups", title: "Backups"}, extra: caps.BackupExtra}
	canRestore := func(row Row) bool { return caps.Restore && row.Data.(host.BackupFile).Kind == host.DatastoreSQLite }
	s.actions = []*Action{
		{ID: "backup_now", Keys: []string{"b"}, Label: "지금 백업", Mutating: true, NeedRoot: true, NoRow: true,
			Do: func(env *Env, _ Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				p, err := env.Host.Backup(c)
				return "백업 완료: " + p, err
			}},
		{ID: "restore_backup", Keys: []string{"R"}, Label: "복원", Mutating: true, NeedRoot: true, Confirm: ConfirmType,
			Available: func(_ *Env, row Row) bool { return canRestore(row) },
			Expect:    func(Row) string { return "restore" },
			ConfirmBody: func(env *Env, row Row) string {
				b := row.Data.(host.BackupFile)
				return fmt.Sprintf("데이터스토어를 %s (%s) 시점으로 되돌립니다.\n\n"+
					"1) 백업 무결성 검사  2) k3s 중지  3) 현재 state.db를 state.db.pre-restore-<시각>으로 보관\n"+
					"4) 백업 복사  5) k3s 시작\n\n백업 이후 생성·변경된 모든 리소스 정보가 사라집니다.",
					b.Name, b.Time.Format("2006-01-02 15:04:05"))
			},
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				b := row.Data.(host.BackupFile)
				return "복원 완료: " + b.Name, env.Host.RestoreBackup(c, b.Name, progressFn(env, "복원"))
			}},
		// 자동 복원을 지원하지 않는 백업(etcd 스냅샷)은 같은 키로 수동 복원 절차를 보여줍니다.
		{ID: "restore_guide", Keys: []string{"R"}, Label: "복원 안내",
			Available: func(_ *Env, row Row) bool { return !canRestore(row) },
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, "복원 안내: "+row.Name, func() (string, error) {
					return env.Host.RestoreGuide(row.Name), nil
				}).SetWrap(true))
			}},
		{ID: "delete_backup", Keys: []string{"x"}, Label: "삭제", Mutating: true, NeedRoot: true, Confirm: ConfirmYesNo,
			ConfirmBody: func(_ *Env, row Row) string { return "백업 파일 " + row.Name + " 을(를) 삭제합니다." },
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := ctx()
				defer cancel()
				return "삭제: " + row.Name, env.Host.DeleteBackup(c, row.Name)
			}},
	}
	return s
}

func (s *backupSource) Columns(*Env) []kube.Column {
	cols := []kube.Column{{Name: "NAME"}, {Name: "KIND"}, {Name: "SIZE"}, {Name: "CREATED"}}
	if s.extra != "" {
		cols = append(cols, kube.Column{Name: s.extra})
	}
	return cols
}

func (s *backupSource) Summary(env *Env) string {
	ds := env.Host.Datastore()
	sum := fmt.Sprintf("datastore: %s", ds.Kind)
	if ds.Path != "" {
		sum += " (" + ds.Path + ")"
	}
	sum += fmt.Sprintf("  ·  백업 위치: %s  ·  보관 %d개", env.Cfg.Backup.Dir, env.Cfg.Backup.Keep)
	if hint := env.Host.Caps().BackupHint; hint != "" {
		sum += "  ·  " + hint
	}
	return sum
}

func (s *backupSource) Load(env *Env) ([]Row, error) {
	list, err := env.Host.ListBackups()
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(list))
	for _, b := range list {
		cells := []string{b.Name, string(b.Kind), kube.FormatBytes(b.Size), b.Time.Format("2006-01-02 15:04:05")}
		if s.extra != "" {
			extra := ""
			if b.HasExtra {
				extra = "yes"
			}
			cells = append(cells, extra)
		}
		rows = append(rows, Row{ID: b.Path, Name: b.Name, Data: b, Cells: cells})
	}
	return rows, nil
}

// ==== containerd (crictl) ====

type containerSource struct{ baseSource }

func newContainerSource() Source {
	s := &containerSource{baseSource{key: "containers", title: "Containers"}}
	s.actions = []*Action{
		{ID: "inspect", Keys: []string{"i", "enter"}, Label: "inspect",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, "crictl inspect "+row.Name, func() (string, error) {
					c, cancel := ctx()
					defer cancel()
					return env.Crictl.Inspect(c, row.ID)
				}).SetStyler(yamlStyler(env)))
			}},
		{ID: "logs", Keys: []string{"l"}, Label: "로그",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, "crictl logs "+row.Name, func() (string, error) {
					return env.Crictl.Logs(context.Background(), row.ID, env.Cfg.UI.LogTailLines)
				}).SetStyler(logStyler(env)))
			}},
	}
	return s
}

func (s *containerSource) AutoRefresh() time.Duration { return 5 * time.Second }
func (s *containerSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "ID"}, {Name: "POD", MaxWidth: 60}, {Name: "CONTAINER"}, {Name: "STATE"}, {Name: "ATTEMPT"}, {Name: "IMAGE", MaxWidth: 60}, {Name: "AGE"}}
}

func (s *containerSource) Load(env *Env) ([]Row, error) {
	if !env.Host.IsRoot() {
		return nil, host.ErrNotRoot
	}
	c, cancel := ctx()
	defer cancel()
	list, err := env.Crictl.Containers(c)
	if err != nil {
		return nil, err
	}
	now := env.NowTime()
	rows := make([]Row, 0, len(list))
	for _, ct := range list {
		level := kube.LevelNone
		switch ct.State {
		case "RUNNING":
			level = kube.LevelOK
		case "EXITED":
			level = kube.LevelMuted
		default:
			level = kube.LevelWarn
		}
		rows = append(rows, Row{ID: ct.ID, Name: ct.Name, Data: ct, Level: level,
			Cells: []string{short(ct.ID), ct.Namespace + "/" + ct.PodName, ct.Name, ct.State, fmt.Sprint(ct.Attempt), ct.Image, kube.Age(ct.Created, now)}})
	}
	return rows, nil
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 13 {
		return id[:13]
	}
	return id
}

type imageSource struct{ baseSource }

func newImageSource() Source {
	s := &imageSource{baseSource{key: "images", title: "Images"}}
	s.actions = []*Action{
		{ID: "inspect", Keys: []string{"i", "enter"}, Label: "inspect",
			Open: func(env *Env, row Row, _ string) tea.Cmd {
				return Push(NewTextPage(env, "crictl inspecti "+row.Name, func() (string, error) {
					c, cancel := ctx()
					defer cancel()
					return env.Crictl.InspectImage(c, row.ID)
				}).SetStyler(yamlStyler(env)))
			}},
		{ID: "remove_image", Keys: []string{"x"}, Label: "삭제", Mutating: true, NeedRoot: true, Confirm: ConfirmYesNo,
			ConfirmBody: func(_ *Env, row Row) string { return "이미지 " + row.Name + " 을(를) 삭제합니다." },
			Do: func(env *Env, row Row, _ string) (string, error) {
				c, cancel := ctx()
				defer cancel()
				return "이미지 삭제: " + row.Name, env.Crictl.RemoveImage(c, row.ID)
			}},
		{ID: "prune_images", Keys: []string{"P"}, Label: "미사용 정리", Mutating: true, NeedRoot: true, NoRow: true, Confirm: ConfirmYesNo,
			ConfirmBody: func(*Env, Row) string {
				return "컨테이너가 사용하지 않는 이미지를 모두 삭제합니다 (crictl rmi --prune).\n에어갭 환경이라면 다시 받을 수 없는 이미지가 있는지 확인하세요."
			},
			Do: func(env *Env, _ Row, _ string) (string, error) {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				out, err := env.Crictl.PruneImages(c)
				n := 0
				if out != "" {
					n = len(strings.Split(out, "\n"))
				}
				return fmt.Sprintf("이미지 정리 완료 (%d줄 출력)", n), err
			}},
	}
	return s
}

func (s *imageSource) Columns(*Env) []kube.Column {
	return []kube.Column{{Name: "IMAGE", MaxWidth: 90}, {Name: "ID"}, {Name: "SIZE"}, {Name: "IN USE"}}
}

func (s *imageSource) Summary(env *Env) string {
	return "IN USE 표시가 없는 이미지는 P(미사용 정리)로 삭제됩니다"
}

func (s *imageSource) Load(env *Env) ([]Row, error) {
	if !env.Host.IsRoot() {
		return nil, host.ErrNotRoot
	}
	c, cancel := ctx()
	defer cancel()
	list, err := env.Crictl.Images(c)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(list))
	for _, im := range list {
		name := strings.Join(im.Tags, ",")
		if name == "" {
			name = "<none>"
		}
		used, level := "", kube.LevelMuted
		if im.InUse {
			used, level = "yes", kube.LevelNone
		}
		rows = append(rows, Row{ID: im.ID, Name: name, Data: im, Level: level,
			Cells: []string{name, short(im.ID), kube.FormatBytes(im.Size), used}})
	}
	return rows, nil
}

// HostSources는 Host 탭의 하위 탭 목록입니다. 배포판이 지원하는 기능(Capabilities)에 따라 구성합니다.
func HostSources(h host.Host) []Source {
	caps := h.Caps()
	out := []Source{newServiceSource(h), newConfigSource(h)}
	if caps.ManifestsLabel != "" {
		out = append(out, newManifestSource(h))
	}
	out = append(out, newCertSource(h))
	if caps.Backup {
		out = append(out, newBackupSource(h))
	}
	return append(out, newContainerSource(), newImageSource())
}
