package k3s

import (
	"context"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// ServiceStatus는 k3s·rke2 서비스 상태입니다.
func (s *System) ServiceStatus(ctx context.Context) (host.ServiceStatus, error) {
	return s.sd.Status(ctx)
}

// ServiceControl은 systemctl start/stop/restart <서비스>를 실행합니다.
func (s *System) ServiceControl(ctx context.Context, op host.ServiceOp) error {
	return s.sd.Control(ctx, op)
}

// StreamJournal은 journalctl -u <서비스> 출력을 스트리밍합니다.
func (s *System) StreamJournal(ctx context.Context, lines int, follow bool) (<-chan string, error) {
	return s.sd.Journal(ctx, lines, follow)
}

// Version은 k3s·rke2 --version의 첫 줄입니다.
func (s *System) Version(ctx context.Context) (string, error) {
	out, err := s.cli(ctx, "--version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, nil
}

// CheckConfig는 k3s check-config 출력입니다. 실패 항목이 있어 종료 코드가 0이 아니어도 출력을 돌려줍니다.
// RKE2에는 이 명령이 없습니다.
func (s *System) CheckConfig(ctx context.Context) (string, error) {
	if !s.fl.checkConfig {
		return "", host.ErrUnsupported
	}
	out, err := s.cli(ctx, "check-config")
	if len(out) > 0 {
		return string(out), nil
	}
	return "", err
}
