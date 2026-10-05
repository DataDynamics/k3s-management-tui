// Package views는 화면(페이지)과 데이터 소스, 작업 정의입니다.
package views

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/audit"
	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/helm"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
	"github.com/DataDynamics/k3s-management-tui/internal/kube"
	"github.com/DataDynamics/k3s-management-tui/internal/runtime"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/components"
	"github.com/DataDynamics/k3s-management-tui/internal/ui/styles"
)

// Env는 모든 페이지가 공유하는 의존성과 상태입니다.
type Env struct {
	Cfg     *config.Config
	Styles  *styles.Styles
	Kube    *kube.Client // 연결 실패 시 nil
	KubeErr error
	Store   *kube.Store
	Metrics *kube.Metrics
	Host    host.Host
	Crictl  *runtime.Crictl
	Helm    *helm.Client
	PF      *kube.PortForwarder
	Audit   *audit.Logger

	// Distro는 판별한 배포판입니다 (config.DistroK3s 등).
	Distro string
	// LocalAPI는 API 서버가 이 호스트에 있는지 여부입니다.
	LocalAPI bool
	// HostEnabled가 false면 Host 탭과 대시보드 호스트 패널을 숨깁니다. HostReason은 그 이유입니다.
	HostEnabled bool
	HostReason  string
	// KubectlBase는 노드 구현이 알려준 kubectl 명령입니다 (로컬 K3S·RKE2 노드일 때만, 없으면 nil).
	KubectlBase []string
	// LocalPath는 local-path 사용량 화면을 보여줄지 여부입니다 (local-path StorageClass가 있고 로컬 API 서버일 때).
	LocalPath bool

	// Warnings는 시작할 때 발견한 설정 경고입니다 (views.d 등). 첫 화면에 알리고 --check에 표시합니다.
	Warnings []string

	Namespace string // "" = 전체
	ReadOnly  bool
	HostState HostStatus

	// Send는 백그라운드 작업이 진행 상황을 화면에 알릴 때 씁니다 (tea.Program.Send).
	Send func(tea.Msg)

	// Now는 테스트에서 시각을 고정하기 위해 씁니다.
	Now func() time.Time
}

// HostStatus는 주기적으로 갱신되는 호스트 상태 캐시입니다.
type HostStatus struct {
	Service   host.ServiceStatus
	Err       error
	Version   string
	Datastore host.DatastoreInfo
	Disk      []host.DiskInfo
	Certs     []host.CertInfo
	Updated   time.Time
}

// LevelStyle은 행 상태 색상입니다.
func (e *Env) LevelStyle(l kube.Level) *lipgloss.Style {
	s := e.Styles
	var st lipgloss.Style
	switch l {
	case kube.LevelOK:
		st = s.Base
	case kube.LevelWarn:
		st = s.Warn
	case kube.LevelErr:
		st = s.Err
	case kube.LevelMuted:
		st = s.Muted
	default:
		return nil
	}
	return &st
}

// NowTime은 현재 시각입니다.
func (e *Env) NowTime() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// ---- 메시지 ----

// PushPageMsg는 현재 탭에 페이지를 쌓습니다.
type PushPageMsg struct{ Page Page }

// PopPageMsg는 현재 페이지를 닫습니다.
type PopPageMsg struct{}

// ToastMsg는 상태바 알림입니다.
type ToastMsg struct {
	Text string
	Err  bool
}

// RefreshMsg는 페이지에 다시 읽기를 요청합니다.
type RefreshMsg struct{}

// StoreChangedMsg는 Informer 캐시가 바뀌었음을 알립니다.
type StoreChangedMsg struct{}

// TickMsg는 refresh_interval마다 발생합니다.
type TickMsg time.Time

// HostStatusMsg는 호스트 상태 갱신 결과입니다.
type HostStatusMsg HostStatus

// NamespaceChangedMsg는 네임스페이스가 바뀌었음을 알립니다.
type NamespaceChangedMsg struct{}

// ActionRequestMsg는 페이지가 작업 실행을 요청할 때 보냅니다. 앱이 read-only·확인 절차를 처리합니다.
type ActionRequestMsg struct {
	Action *Action
	Row    Row
	Source string
}

// ActionDoneMsg는 작업 결과입니다. 변경 작업이면 감사 로그에 기록됩니다.
type ActionDoneMsg struct {
	Action   *Action
	Target   string
	Detail   string
	Info     string
	Err      error
	Mutating bool
}

// DialogMsg는 다이얼로그를 띄웁니다.
type DialogMsg struct{ Dialog *components.Dialog }

// JumpMsg는 명령 모드에서 특정 소스로 이동합니다.
type JumpMsg struct{ Key string }

// Targeted는 특정 페이지로 전달되어야 하는 메시지입니다 (비동기 로딩 결과 등).
type Targeted interface{ TargetPage() Page }

// Toast는 알림 명령입니다.
func Toast(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return ToastMsg{Text: text, Err: isErr} }
}

// Push는 페이지를 여는 명령입니다.
func Push(p Page) tea.Cmd {
	return func() tea.Msg { return PushPageMsg{Page: p} }
}
