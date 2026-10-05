package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSystemctlShow(t *testing.T) {
	out := "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nMainPID=1234\n" +
		"ActiveEnterTimestamp=@1791204607\nMemoryCurrent=4294967296\nTasksCurrent=211\nNRestarts=2\n"
	st := ParseSystemctlShow("k3s", []byte(out))
	if !st.Active() || st.MainPID != 1234 || st.MemoryBytes != 4294967296 || st.Tasks != 211 || st.Restarts != 2 {
		t.Errorf("파싱 결과 이상: %+v", st)
	}
	if st.Since.Unix() != 1791204607 {
		t.Errorf("시작 시각: %v", st.Since)
	}
	// 측정 불가 값([not set], uint64 max)은 -1/0으로 둡니다.
	st = ParseSystemctlShow("k3s", []byte("ActiveState=failed\nMemoryCurrent=[not set]\nTasksCurrent=18446744073709551615\n"))
	if st.Active() || st.MemoryBytes != -1 || st.Tasks != 0 {
		t.Errorf("측정 불가 값 처리 이상: %+v", st)
	}
}

func TestJournalLevel(t *testing.T) {
	cases := map[string]string{
		`Oct 05 k3s[1]: time="x" level=error msg="boom"`:              "error",
		`Oct 05 k3s[1]: time="x" level=warning msg="hmm"`:             "warn",
		`2026-10-05T22:22:16+09:00 host k3s[2211085]: E1005 22:22:16`: "error",
		`2026-10-05T22:22:16+09:00 host k3s[2211085]: W1005 22:22:16`: "warn",
		`2026-10-05T22:22:16+09:00 host k3s[2211085]: I1005 22:22:16`: "",
		`short`: "",
	}
	for line, want := range cases {
		if got := JournalLevel(line); got != want {
			t.Errorf("JournalLevel(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestWriteFileWithBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kube-apiserver.yaml")
	os.WriteFile(p, []byte("old\n"), 0o600)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	backup, err := WriteFileWithBackup(p, []byte("new\n"), filepath.Join(dir, "bk"), 0o644, now)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "old\n" {
		t.Errorf("백업 내용: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("권한 유지 실패: %v", st.Mode())
	}
	// 임시 파일이 남지 않아야 합니다 (kubelet이 manifests 디렉터리의 파일을 모두 읽기 때문).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("임시 파일이 남음: %s", e.Name())
		}
	}
}

func TestInsideDirAndBackupName(t *testing.T) {
	if !InsideDir("/etc/kubernetes/manifests", "/etc/kubernetes/manifests/etcd.yaml") {
		t.Error("안쪽 경로")
	}
	for _, p := range []string{"/etc/passwd", "/etc/kubernetes/manifests/../admin.conf", "/etc/kubernetes/manifests"} {
		if InsideDir("/etc/kubernetes/manifests", p) {
			t.Errorf("바깥 경로 허용: %s", p)
		}
	}
	for _, n := range []string{"", "../x", "a/b", ".hidden"} {
		if ValidBackupName(n) == nil {
			t.Errorf("잘못된 이름 허용: %q", n)
		}
	}
}

func TestConfigDisplay(t *testing.T) {
	c := &Config{Values: map[string]any{
		"authentication":  map[string]any{"webhook": map[string]any{"enabled": true}, "anonymous": map[string]any{"enabled": false}},
		"bootstrap-token": "x", "list": []any{"a", "b"},
	}}
	if got := c.Display("authentication"); got != "{anonymous: {enabled: false}, webhook: {enabled: true}}" {
		t.Errorf("중첩 맵 표시: %s", got)
	}
	if c.Display("bootstrap-token") != "********" || c.Display("list") != "[a, b]" {
		t.Error("가림·목록 표시")
	}
}
