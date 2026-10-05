// Package executil은 외부 명령을 쉘 없이 인자 배열로 실행합니다.
package executil

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Runner는 외부 명령 실행기입니다. 테스트에서는 가짜 구현으로 바꿉니다.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// System은 실제 프로세스를 실행하는 Runner입니다.
type System struct {
	Timeout time.Duration
	Env     []string // 추가 환경변수 (KEY=VALUE)
}

// Run은 명령을 실행하고 stdout을 돌려줍니다. 실패하면 stderr를 포함한 오류를 돌려줍니다.
func (s System) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if len(s.Env) > 0 {
		cmd.Env = append(cmd.Environ(), s.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	slog.Debug("exec", "cmd", name, "args", args, "dur", time.Since(start), "err", err)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.Bytes(), fmt.Errorf("%s: 시간 초과 (%s)", name, s.Timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		if msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// Stream은 장기 실행 명령(journalctl -f 등)의 stdout/stderr를 한 줄씩 채널로 보냅니다.
// ctx가 취소되면 프로세스를 종료하고 채널을 닫습니다.
func Stream(ctx context.Context, env []string, name string, args ...string) (<-chan string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := make(chan string, 256)
	go func() {
		err := cmd.Wait()
		if err != nil && ctx.Err() == nil {
			pw.CloseWithError(fmt.Errorf("%s 종료: %w", name, err))
			return
		}
		pw.Close()
	}()
	go func() {
		defer close(out)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case out <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			select {
			case out <- "[k3stui] " + err.Error():
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}
