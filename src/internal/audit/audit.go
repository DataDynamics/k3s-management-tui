// Package audit은 변경 작업을 JSON Lines로 기록합니다.
package audit

import (
	"encoding/json"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

// Entry는 감사 로그 한 줄입니다.
type Entry struct {
	Time     time.Time `json:"time"`
	User     string    `json:"user"`
	SudoUser string    `json:"sudo_user,omitempty"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Detail   string    `json:"detail,omitempty"`
	Result   string    `json:"result"` // ok | error | cancelled
	Error    string    `json:"error,omitempty"`
}

// Logger는 동시 사용 가능한 감사 로거입니다.
type Logger struct {
	mu       sync.Mutex
	w        io.Writer
	closer   io.Closer
	user     string
	sudoUser string
}

// Open은 감사 로그 파일을 엽니다 (0600, append). path가 비면 기록하지 않습니다.
func Open(path string) (*Logger, error) {
	l := &Logger{w: io.Discard, sudoUser: os.Getenv("SUDO_USER")}
	if u, err := user.Current(); err == nil {
		l.user = u.Username
	}
	if path == "" {
		return l, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return l, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return l, err
	}
	l.w, l.closer = f, f
	return l, nil
}

// NewWriter는 테스트용으로 임의 Writer에 기록하는 로거를 만듭니다.
func NewWriter(w io.Writer) *Logger { return &Logger{w: w, user: "test"} }

// Record는 작업 결과를 기록합니다. err가 nil이면 ok.
func (l *Logger) Record(action, target, detail string, err error) {
	if l == nil {
		return
	}
	e := Entry{
		Time: time.Now(), User: l.user, SudoUser: l.sudoUser,
		Action: action, Target: target, Detail: detail, Result: "ok",
	}
	if err != nil {
		e.Result, e.Error = "error", err.Error()
	}
	data, _ := json.Marshal(e)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(data, '\n'))
}

// Close는 파일을 닫습니다.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}
