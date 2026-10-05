package views

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/kube"
)

// Hint는 상태바 키 안내입니다.
type Hint struct{ Key, Desc string }

// Page는 탭 안에 쌓이는 화면 하나입니다.
type Page interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Page, tea.Cmd)
	View(width, height int) string
	Title() string
	Hints() []Hint
	// InputActive가 true면 페이지가 문자 입력(필터·검색)을 받는 중이므로 전역 키를 처리하지 않습니다.
	InputActive() bool
	// Close는 페이지가 닫힐 때 스트림 등을 정리합니다.
	Close()
}

// Row는 소스가 만드는 테이블 행입니다.
type Row struct {
	ID        string
	Cells     []string
	Level     kube.Level
	Namespace string
	Name      string
	Data      any
}

// Source는 테이블 페이지의 데이터 공급자입니다 (리소스, 호스트 항목, Helm 릴리스 ...).
type Source interface {
	Key() string
	Title() string
	Columns(env *Env) []kube.Column
	// Load는 행 목록을 만듭니다. Async()가 true면 백그라운드에서 호출됩니다.
	Load(env *Env) ([]Row, error)
	Async() bool
	// AutoRefresh는 비동기 소스의 자동 갱신 주기입니다 (0이면 수동).
	AutoRefresh() time.Duration
	Actions() []*Action
	// Namespaced가 true면 네임스페이스 선택을 따릅니다.
	Namespaced() bool
}

// ConfirmKind는 실행 전 확인 방식입니다.
type ConfirmKind int

const (
	ConfirmNone  ConfirmKind = iota
	ConfirmYesNo             // y/N
	ConfirmType              // 이름 재입력
)

// Action은 행(또는 소스 전체)에 대한 작업입니다.
// Do는 백그라운드에서 실행되어 결과가 감사 로그·알림으로 남고,
// Open은 화면 이동·외부 프로세스 실행 같은 명령을 돌려줍니다.
type Action struct {
	ID       string
	Keys     []string
	Label    string
	Mutating bool // read-only 모드에서 차단, 감사 로그 기록
	NeedRoot bool
	NoRow    bool // 선택된 행 없이 실행 가능 (백업 생성, 이미지 정리 등)
	Confirm  ConfirmKind

	// ConfirmBody는 확인 다이얼로그 설명입니다 (nil이면 기본 문구).
	ConfirmBody func(env *Env, row Row) string
	// Expect는 ConfirmType에서 입력해야 할 값입니다 (nil이면 행 이름).
	Expect func(row Row) string
	// Choices가 둘 이상을 돌려주면 목록에서 고르게 하고, 하나면 그 값을 입력으로 씁니다 (컨테이너 선택 등).
	Choices func(env *Env, row Row) []string
	// Prompt가 있으면 실행 전 값을 입력받습니다 (title, 초깃값).
	Prompt func(env *Env, row Row) (string, string)
	// Available이 false를 돌려주면 해당 행에서는 작업을 숨깁니다.
	Available func(env *Env, row Row) bool

	Do   func(env *Env, row Row, input string) (info string, err error)
	Open func(env *Env, row Row, input string) tea.Cmd
}

// Target은 감사 로그용 대상 문자열입니다.
func Target(source string, r Row) string {
	switch {
	case r.Namespace != "" && r.Name != "":
		return source + "/" + r.Namespace + "/" + r.Name
	case r.Name != "":
		return source + "/" + r.Name
	default:
		return source
	}
}

// baseSource는 소스 공통 기본값입니다.
type baseSource struct {
	key, title string
	actions    []*Action
}

func (b *baseSource) Key() string                { return b.key }
func (b *baseSource) Title() string              { return b.title }
func (b *baseSource) Actions() []*Action         { return b.actions }
func (b *baseSource) Namespaced() bool           { return false }
func (b *baseSource) Async() bool                { return true }
func (b *baseSource) AutoRefresh() time.Duration { return 0 }
