package host

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

// ParseSystemctlShow는 systemctl show의 key=value 출력을 ServiceStatus로 바꿉니다.
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

// Systemd는 systemd 유닛 하나를 다룹니다 (k3s, kubelet).
type Systemd struct {
	Unit       string
	Systemctl  string
	Journalctl string
	Run        executil.Runner // 상태 조회
	Ctl        executil.Runner // start/stop/restart (기동 대기로 타임아웃이 깁니다)
	Root       bool
}

// Status는 systemctl show 결과입니다.
func (s *Systemd) Status(ctx context.Context) (ServiceStatus, error) {
	out, err := s.Run.Run(ctx, s.Systemctl, "show", s.Unit, "--timestamp=unix", "--no-pager",
		"-p", "LoadState,ActiveState,SubState,UnitFileState,MainPID,ActiveEnterTimestamp,MemoryCurrent,TasksCurrent,NRestarts")
	if err != nil {
		return ServiceStatus{Unit: s.Unit}, err
	}
	return ParseSystemctlShow(s.Unit, out), nil
}

// Control은 systemctl start/stop/restart를 실행합니다.
func (s *Systemd) Control(ctx context.Context, op ServiceOp) error {
	if !s.Root {
		return ErrNotRoot
	}
	switch op {
	case OpStart, OpStop, OpRestart:
	default:
		return fmt.Errorf("알 수 없는 동작: %s", op)
	}
	_, err := s.Ctl.Run(ctx, s.Systemctl, string(op), s.Unit)
	return err
}

// Journal은 journalctl -u <unit> 출력을 스트리밍합니다.
func (s *Systemd) Journal(ctx context.Context, lines int, follow bool) (<-chan string, error) {
	args := []string{"-u", s.Unit, "-n", strconv.Itoa(lines), "-o", "short-iso", "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return executil.Stream(ctx, nil, s.Journalctl, args...)
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
