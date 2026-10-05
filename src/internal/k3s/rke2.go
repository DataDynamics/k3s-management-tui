package k3s

import (
	"context"
	"fmt"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/executil"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// NewRKE2System은 RKE2 Host 구현을 만듭니다.
// rke2.service_name이 auto면 rke2-server와 rke2-agent 중 실행 중인(없으면 설치된) 유닛을 고릅니다.
func NewRKE2System(cfg *config.Config, run executil.Runner) *System {
	return newSystem(cfg, flavor{
		distro: config.DistroRKE2, name: "RKE2", cmd: "rke2",
		binary: cfg.ResolveRKE2Binary(), service: rke2Service(cfg, run), configFile: cfg.RKE2.ConfigFile,
		dataDirCfg: cfg.RKE2.DataDir, defaultData: "/var/lib/rancher/rke2",
		docsURL: "https://docs.rke2.io/datastore/backup_restore",
	}, run)
}

// rke2Service는 RKE2 서비스 유닛 이름을 정합니다.
func rke2Service(cfg *config.Config, run executil.Runner) string {
	if n := cfg.RKE2.ServiceName; n != "" && n != "auto" {
		return n
	}
	loaded := ""
	for _, unit := range []string{"rke2-server", "rke2-agent"} {
		out, err := run.Run(context.Background(), cfg.Tools.Systemctl, "show", unit, "-p", "LoadState,ActiveState", "--no-pager")
		if err != nil {
			continue
		}
		st := host.ParseSystemctlShow(unit, out)
		if st.Active() {
			return unit
		}
		if st.LoadState == "loaded" && loaded == "" {
			loaded = unit
		}
	}
	if loaded != "" {
		return loaded
	}
	return "rke2-server"
}

// BuildRKE2JoinCommand는 RKE2 agent 노드 추가 절차입니다.
// RKE2 agent는 supervisor 포트(9345)로 서버에 붙고, 서버 주소·토큰은 config.yaml로 넘깁니다.
func BuildRKE2JoinCommand(serverIP, token string) string {
	var b strings.Builder
	b.WriteString("# 1) RKE2 agent 설치\n")
	b.WriteString(`curl -sfL https://get.rke2.io | INSTALL_RKE2_TYPE="agent" sh -` + "\n\n")
	b.WriteString("# 2) 서버 주소와 토큰 설정\n")
	b.WriteString("mkdir -p /etc/rancher/rke2\n")
	fmt.Fprintf(&b, "cat > /etc/rancher/rke2/config.yaml <<'EOF'\nserver: https://%s:9345\ntoken: %s\nEOF\n\n", serverIP, token)
	b.WriteString("# 3) agent 시작\n")
	b.WriteString("systemctl enable --now rke2-agent.service\n")
	return b.String()
}
