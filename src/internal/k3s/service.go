package k3s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/executil"
)

// ServiceStatus는 systemd 유닛 상태입니다.
type ServiceStatus struct {
	Unit          string
	LoadState     string
	ActiveState   string // active, inactive, failed, activating ...
	SubState      string
	UnitFileState string // enabled, disabled
	MainPID       int
	Since         time.Time
	MemoryBytes   int64 // -1이면 알 수 없음
	Tasks         int64
	Restarts      int64
}

// Active는 서비스가 실행 중인지 알려줍니다.
func (s ServiceStatus) Active() bool { return s.ActiveState == "active" }

// ServiceStatus는 systemctl show 결과를 해석합니다.
func (s *System) ServiceStatus(ctx context.Context) (ServiceStatus, error) {
	out, err := s.run.Run(ctx, s.cfg.Tools.Systemctl, "show", s.cfg.K3s.ServiceName, "--timestamp=unix", "--no-pager",
		"-p", "LoadState,ActiveState,SubState,UnitFileState,MainPID,ActiveEnterTimestamp,MemoryCurrent,TasksCurrent,NRestarts")
	if err != nil {
		return ServiceStatus{Unit: s.cfg.K3s.ServiceName}, err
	}
	return ParseSystemctlShow(s.cfg.K3s.ServiceName, out), nil
}

// ParseSystemctlShow는 key=value 출력을 ServiceStatus로 바꿉니다.
func ParseSystemctlShow(unit string, out []byte) ServiceStatus {
	st := ServiceStatus{Unit: unit, MemoryBytes: -1}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			st.LoadState = v
		case "ActiveState":
			st.ActiveState = v
		case "SubState":
			st.SubState = v
		case "UnitFileState":
			st.UnitFileState = v
		case "MainPID":
			st.MainPID, _ = strconv.Atoi(v)
		case "ActiveEnterTimestamp":
			if sec, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64); err == nil && sec > 0 {
				st.Since = time.Unix(sec, 0)
			}
		case "MemoryCurrent":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n < 1<<62 {
				st.MemoryBytes = n
			}
		case "TasksCurrent":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n < 1<<62 {
				st.Tasks = n
			}
		case "NRestarts":
			st.Restarts, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return st
}

// ServiceControl은 systemctl start/stop/restart를 실행합니다.
func (s *System) ServiceControl(ctx context.Context, op ServiceOp) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	switch op {
	case OpStart, OpStop, OpRestart:
	default:
		return fmt.Errorf("알 수 없는 동작: %s", op)
	}
	// restart는 K3S 기동에 수십 초가 걸릴 수 있어 별도 실행기(긴 타임아웃)를 씁니다.
	_, err := s.ctl.Run(ctx, s.cfg.Tools.Systemctl, string(op), s.cfg.K3s.ServiceName)
	return err
}

// StreamJournal은 journalctl -u <service> 출력을 스트리밍합니다.
func (s *System) StreamJournal(ctx context.Context, lines int, follow bool) (<-chan string, error) {
	args := []string{"-u", s.cfg.K3s.ServiceName, "-n", strconv.Itoa(lines), "-o", "short-iso", "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return executil.Stream(ctx, nil, s.cfg.Tools.Journalctl, args...)
}

// Version은 k3s --version의 첫 줄입니다.
func (s *System) Version(ctx context.Context) (string, error) {
	out, err := s.k3s(ctx, "--version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, nil
}

// CheckConfig는 k3s check-config 출력입니다. 실패 항목이 있으면 종료 코드가 0이 아니어도 출력을 돌려줍니다.
func (s *System) CheckConfig(ctx context.Context) (string, error) {
	out, err := s.k3s(ctx, "check-config")
	if len(out) > 0 {
		return string(out), nil
	}
	return "", err
}

// JournalLevel은 로그 줄의 심각도를 추정합니다 (logrus level= 및 klog E/W 접두사).
func JournalLevel(line string) string {
	switch {
	case strings.Contains(line, "level=error"), strings.Contains(line, "level=fatal"), klogPrefix(line, 'E'), klogPrefix(line, 'F'):
		return "error"
	case strings.Contains(line, "level=warning"), klogPrefix(line, 'W'):
		return "warn"
	}
	return ""
}

// klogPrefix는 "k3s[123]: E1005 12:00:00.000" 형태를 찾습니다.
func klogPrefix(line string, c byte) bool {
	i := strings.Index(line, "]: ")
	if i < 0 || i+8 > len(line) {
		return false
	}
	s := line[i+3:]
	return s[0] == c && s[1] >= '0' && s[1] <= '9' && s[4] >= '0' && s[4] <= '9'
}
