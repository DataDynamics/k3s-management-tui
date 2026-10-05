// Package styles는 theme.yaml 팔레트를 lipgloss 스타일로 바꿉니다.
package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
)

// Styles는 화면 전체에서 쓰는 스타일 묶음입니다.
type Styles struct {
	Fg, MutedC, PrimaryC, AccentC, BorderC color.Color
	OKC, WarnC, ErrC, InfoC                color.Color

	Base        lipgloss.Style
	Muted       lipgloss.Style
	Bold        lipgloss.Style
	Primary     lipgloss.Style
	Accent      lipgloss.Style
	OK          lipgloss.Style
	Warn        lipgloss.Style
	Err         lipgloss.Style
	Info        lipgloss.Style
	Header      lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	SubActive   lipgloss.Style
	SubInactive lipgloss.Style
	TableHeader lipgloss.Style
	Selected    lipgloss.Style
	Key         lipgloss.Style
	KeyDesc     lipgloss.Style
	Dialog      lipgloss.Style
	DialogTitle lipgloss.Style
	Panel       lipgloss.Style
	PanelTitle  lipgloss.Style
	Badge       lipgloss.Style
	Match       lipgloss.Style
}

// New는 테마로 스타일을 만듭니다.
func New(t *config.Theme) *Styles {
	c := lipgloss.Color
	s := &Styles{
		Fg: c(t.Fg), MutedC: c(t.Muted), PrimaryC: c(t.Primary), AccentC: c(t.Accent), BorderC: c(t.Border),
		OKC: c(t.OK), WarnC: c(t.Warn), ErrC: c(t.Err), InfoC: c(t.Info),
	}
	base := lipgloss.NewStyle().Foreground(s.Fg)
	s.Base = base
	s.Muted = lipgloss.NewStyle().Foreground(s.MutedC)
	s.Bold = base.Bold(true)
	s.Primary = lipgloss.NewStyle().Foreground(s.PrimaryC).Bold(true)
	s.Accent = lipgloss.NewStyle().Foreground(s.AccentC)
	s.OK = lipgloss.NewStyle().Foreground(s.OKC)
	s.Warn = lipgloss.NewStyle().Foreground(s.WarnC)
	s.Err = lipgloss.NewStyle().Foreground(s.ErrC)
	s.Info = lipgloss.NewStyle().Foreground(s.InfoC)
	s.Header = lipgloss.NewStyle().Foreground(c(t.HeaderFg)).Background(c(t.HeaderBg))
	s.TabActive = lipgloss.NewStyle().Foreground(c(t.SelectedFg)).Background(s.PrimaryC).Bold(true).Padding(0, 1)
	s.TabInactive = lipgloss.NewStyle().Foreground(s.MutedC).Padding(0, 1)
	s.SubActive = lipgloss.NewStyle().Foreground(s.PrimaryC).Bold(true).Underline(true)
	s.SubInactive = lipgloss.NewStyle().Foreground(s.MutedC)
	s.TableHeader = lipgloss.NewStyle().Foreground(s.PrimaryC).Bold(true)
	s.Selected = lipgloss.NewStyle().Foreground(c(t.SelectedFg)).Background(c(t.SelectedBg))
	s.Key = lipgloss.NewStyle().Foreground(s.AccentC).Bold(true)
	s.KeyDesc = lipgloss.NewStyle().Foreground(s.MutedC)
	s.Dialog = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.PrimaryC).Padding(1, 2)
	s.DialogTitle = lipgloss.NewStyle().Foreground(s.PrimaryC).Bold(true)
	s.Panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.BorderC).Padding(0, 1)
	s.PanelTitle = lipgloss.NewStyle().Foreground(s.PrimaryC).Bold(true)
	s.Badge = lipgloss.NewStyle().Foreground(c(t.SelectedFg)).Background(s.ErrC).Bold(true).Padding(0, 1)
	s.Match = lipgloss.NewStyle().Foreground(c(t.SelectedFg)).Background(s.AccentC)
	return s
}
