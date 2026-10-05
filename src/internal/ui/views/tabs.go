package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// TabDef는 상단 탭 하나입니다.
type TabDef struct {
	Key  string // default_view 값
	Name string
	Root Page
}

func resources(env *Env, keys ...string) []Source {
	var out []Source
	for _, k := range keys {
		s := NewResourceSource(k)
		if s == nil {
			continue
		}
		if env.Kube != nil && !env.Kube.Has(s.Def.GVR) {
			continue // CRD가 없는 클러스터에서는 숨깁니다
		}
		out = append(out, s)
	}
	return out
}

// BuildTabs는 설계 5.2절의 탭 7개를 만듭니다.
func BuildTabs(env *Env) []TabDef {
	network := resources(env, "services", "ingresses", "endpointslices", "networkpolicies",
		"ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies")
	if env.Kube != nil && env.Kube.Has(kube.GVRCiliumNP) {
		network = append(network, newCiliumSource())
	}
	network = append(network, newPortForwardSource())

	storage := resources(env, "persistentvolumeclaims", "persistentvolumes", "storageclasses")
	if env.LocalPath {
		storage = append(storage, newLocalPathSource())
	}
	helm := append([]Source{newHelmReleaseSource()}, resources(env, "helmcharts", "helmchartconfigs")...)

	tabs := []TabDef{
		{Key: "dashboard", Name: "Dashboard", Root: NewDashboard(env)},
		{Key: "workloads", Name: "Workloads", Root: NewTablePage(env, "Workloads",
			resources(env, "pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs")...)},
		{Key: "network", Name: "Network", Root: NewTablePage(env, "Network", network...)},
		{Key: "storage", Name: "Storage", Root: NewTablePage(env, "Storage", storage...)},
		{Key: "config", Name: "Config", Root: NewTablePage(env, "Config",
			resources(env, "configmaps", "secrets", "serviceaccounts", "roles", "rolebindings", "clusterroles", "clusterrolebindings")...)},
	}
	// Host 탭은 K3S 서버 노드에서 실행할 때만 의미가 있습니다 (설정으로 강제 가능).
	if env.HostEnabled {
		tabs = append(tabs, TabDef{Key: "host", Name: "Host", Root: NewTablePage(env, "Host", HostSources(env.Host)...)})
	}
	return append(tabs, TabDef{Key: "helm", Name: "Helm", Root: NewTablePage(env, "Helm", helm...)})
}

// actionLister는 도움말에 작업 목록을 보여줄 수 있는 페이지입니다.
type actionLister interface{ AllActions() []*Action }

// NewHelpPage는 전역 키와 현재 화면의 작업 키를 보여줍니다. tabNames는 표시 순서대로의 탭 이름입니다.
func NewHelpPage(env *Env, current Page, tabNames []string) *TextPage {
	kb := env.Cfg.Keys
	var sb strings.Builder
	line := func(keys, desc string) { fmt.Fprintf(&sb, "  %-22s %s\n", keys, desc) }
	join := func(id string) string { return strings.Join(kb.Global[id], ", ") }

	sb.WriteString("전역 키\n")
	line(fmt.Sprintf("1 ~ %d", len(tabNames)), "탭 전환 ("+strings.Join(tabNames, ", ")+")")
	line(join(config.KeyNextSource)+" / "+join(config.KeyPrevSource), "하위 탭 전환")
	line(join(config.KeyUp)+" / "+join(config.KeyDown), "이동")
	line(join(config.KeyPageUp)+" / "+join(config.KeyPageDown), "페이지 이동")
	line(join(config.KeyTop)+" / "+join(config.KeyBottom), "처음 / 끝")
	line(join(config.KeyFilter), "필터 (!로 시작하면 제외)  ·  텍스트 화면에서는 검색")
	line(join(config.KeyCommand), "명령 모드 (아래 참고)")
	line(join(config.KeyNamespace), "네임스페이스 선택")
	line(join(config.KeyRefresh), "새로고침")
	line(join(config.KeyBack), "뒤로 / 필터 해제")
	line(join(config.KeyHelp), "도움말")
	line(join(config.KeyQuit), "종료 (하위 화면에서 q는 뒤로)")

	if al, ok := current.(actionLister); ok {
		sb.WriteString("\n현재 화면 작업\n")
		for _, a := range al.AllActions() {
			flags := ""
			if a.Mutating {
				flags += " [변경]"
			}
			if a.NeedRoot {
				flags += " [root]"
			}
			if a.Confirm == ConfirmType {
				flags += " [이름 확인]"
			}
			line(strings.Join(kb.ActionKeys(a.ID, a.Keys), ", "), a.Label+flags+"  ("+a.ID+")")
		}
	}

	sb.WriteString("\n텍스트 화면 (로그·YAML·describe)\n")
	line("/ , n , N", "검색, 다음, 이전")
	line("w", "줄바꿈 전환")
	line("f", "follow 전환 (스트림)")
	line("← →", "가로 스크롤")

	sb.WriteString("\n명령 모드 (:)\n")
	line(":<리소스>", "리소스로 이동 (예: :pods, :deploy, :svc, :nodes, :events, :ns)")
	line(":ns <이름|all>", "네임스페이스 변경")
	if env.HostEnabled {
		line(":journal", "k3s 서비스 로그")
	}
	line(":<탭이름>", "탭 이동 (:host, :helm ...)")
	line(":q", "종료")

	var keys []string
	for _, d := range kube.Defs() {
		keys = append(keys, d.Key+" ("+strings.Join(d.Aliases, ",")+")")
	}
	sort.Strings(keys)
	sb.WriteString("\n리소스 이름\n")
	for _, k := range keys {
		sb.WriteString("  " + k + "\n")
	}

	sb.WriteString("\n안전 장치\n")
	line("[변경]", "read-only 모드에서 차단되고 감사 로그에 기록됩니다: "+env.Cfg.Audit.File)
	line("보호 네임스페이스", strings.Join(env.Cfg.Safety.ProtectedNamespaces, ", ")+" — 변경 시 이름 재입력")
	sb.WriteString("\n클러스터\n")
	line("배포판", kube.DistroName(env.Distro))
	if env.Kube != nil {
		line("API 서버", env.Kube.Server)
		line("context", env.Kube.Context)
	}
	line("kubeconfig", env.Cfg.Cluster.Kubeconfig+"  ("+env.Cfg.Cluster.KubeconfigSource+")")
	if !env.HostEnabled {
		line("호스트 관리", "꺼짐 — "+env.HostReason)
	}
	sb.WriteString("\n설정 파일: ")
	if env.Cfg.File != "" {
		sb.WriteString(env.Cfg.File)
	} else {
		sb.WriteString("(내장 기본값)")
	}
	sb.WriteString("\n")

	text := sb.String()
	return NewTextPage(env, "도움말", func() (string, error) { return text, nil })
}
