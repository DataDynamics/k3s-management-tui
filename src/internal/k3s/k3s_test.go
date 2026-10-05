package k3s

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
)

// fakeRunner는 실행된 명령을 기록하는 가짜 실행기입니다.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
	err   map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
	f.calls = append(f.calls, line)
	for prefix, e := range f.err {
		if strings.HasPrefix(line, prefix) {
			return nil, e
		}
	}
	for prefix, o := range f.out {
		if strings.HasPrefix(line, prefix) {
			return []byte(o), nil
		}
	}
	return nil, nil
}

func (f *fakeRunner) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// testHost는 임시 data-dir을 쓰는 root Host를 만듭니다.
func testHost(t *testing.T) (*System, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "k3s")
	cfg := config.Default()
	cfg.K3s.ConfigFile = filepath.Join(dir, "etc", "config.yaml")
	cfg.K3s.DataDir = dataDir
	cfg.Backup.Dir = filepath.Join(dir, "backups")
	cfg.Backup.Keep = 2
	run := &fakeRunner{out: map[string]string{}, err: map[string]error{}}
	s := NewSystem(cfg, run)
	s.ctl = run
	s.root = true
	return s, run, dataDir
}

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

func TestLoadK3sConfigWithDropIns(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte("data-dir: /data2/k3s\ndisable:\n  - traefik\nkubelet-arg:\n  - root-dir=/data2/kubelet\ntoken: secret\n"), 0o644)
	os.MkdirAll(p+".d", 0o755)
	os.WriteFile(filepath.Join(p+".d", "10-extra.yaml"), []byte("disable+:\n  - servicelb\nnode-name: n1\n"), 0o644)

	kc, err := LoadK3sConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := kc.Strings("disable"); strings.Join(got, ",") != "traefik,servicelb" {
		t.Errorf("drop-in + 병합 실패: %v", got)
	}
	if kc.String("node-name") != "n1" || len(kc.DropIns) != 1 {
		t.Errorf("drop-in 값 누락: %v", kc.Values)
	}
	if kc.Display("token") != "********" {
		t.Error("token은 가려야 함")
	}
	// 없는 파일은 빈 설정
	empty, err := LoadK3sConfig(filepath.Join(dir, "none.yaml"))
	if err != nil || len(empty.Values) != 0 {
		t.Errorf("없는 파일 처리: %v %v", empty, err)
	}
}

func TestDataDirAndKubeletDirFromConfig(t *testing.T) {
	s, _, _ := testHost(t)
	s.cfg.K3s.DataDir = "auto"
	os.MkdirAll(filepath.Dir(s.cfg.K3s.ConfigFile), 0o755)
	os.WriteFile(s.cfg.K3s.ConfigFile, []byte("data-dir: /data2/k3s\nkubelet-arg:\n  - root-dir=/data2/kubelet\n"), 0o644)
	if s.DataDir() != "/data2/k3s" {
		t.Errorf("DataDir = %s", s.DataDir())
	}
	if s.KubeletDir() != "/data2/kubelet" {
		t.Errorf("KubeletDir = %s", s.KubeletDir())
	}
}

func TestWriteConfigBacksUpAndValidates(t *testing.T) {
	s, _, _ := testHost(t)
	os.MkdirAll(filepath.Dir(s.cfg.K3s.ConfigFile), 0o755)
	os.WriteFile(s.cfg.K3s.ConfigFile, []byte("a: 1\n"), 0o640)

	if _, err := s.WriteConfig([]byte("a: [unclosed\n")); err == nil {
		t.Fatal("잘못된 YAML을 저장함")
	}
	backup, err := s.WriteConfig([]byte("a: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "a: 1\n" {
		t.Errorf("백업 내용: %q", b)
	}
	if b, _ := os.ReadFile(s.cfg.K3s.ConfigFile); string(b) != "a: 2\n" {
		t.Errorf("새 내용: %q", b)
	}
	if st, _ := os.Stat(s.cfg.K3s.ConfigFile); st.Mode().Perm() != 0o640 {
		t.Errorf("기존 권한이 유지되지 않음: %v", st.Mode())
	}
	s.root = false
	if _, err := s.WriteConfig([]byte("a: 3\n")); err != ErrNotRoot {
		t.Errorf("root가 아니면 ErrNotRoot여야 함: %v", err)
	}
}

func TestMaskEndpoint(t *testing.T) {
	got := maskEndpoint("mysql://user:pa55@tcp(db:3306)/k3s")
	if got != "mysql://user:****@tcp(db:3306)/k3s" {
		t.Errorf("maskEndpoint = %s", got)
	}
	if maskEndpoint("https://etcd:2379") != "https://etcd:2379" {
		t.Error("자격 증명 없는 주소는 그대로여야 함")
	}
}

// makeKineDB는 kine 스키마를 흉내 낸 SQLite DB를 만듭니다.
func makeKineDB(t *testing.T, path string, rows int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE kine (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, value BLOB)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec("INSERT INTO kine(name, value) VALUES (?, ?)", "/registry/x", []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
}

func countKine(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM kine").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSQLiteBackupPruneAndRestore(t *testing.T) {
	s, run, dataDir := testHost(t)
	dbPath := filepath.Join(dataDir, "server", "db", "state.db")
	makeKineDB(t, dbPath, 5)
	os.WriteFile(filepath.Join(dataDir, "server", "token"), []byte("K10abc::server:xyz\n"), 0o600)

	if ds := s.Datastore(); ds.Kind != DatastoreSQLite || ds.Path != dbPath {
		t.Fatalf("데이터스토어 판별 실패: %+v", ds)
	}

	// 백업 3번 → keep=2이므로 2개만 남습니다.
	var paths []string
	for i := 0; i < 3; i++ {
		ts := time.Date(2026, 10, 5, 12, 0, i, 0, time.Local)
		s.now = func() time.Time { return ts }
		p, err := s.Backup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, ts, ts)
		paths = append(paths, p)
	}
	list, err := s.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("보관 개수 정리 실패: %d개", len(list))
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Error("가장 오래된 백업이 지워지지 않음")
	}
	if !list[0].HasToken {
		t.Error("백업과 함께 토큰이 저장되지 않음")
	}
	if err := CheckSQLite(context.Background(), list[0].Path); err != nil {
		t.Fatalf("백업 무결성: %v", err)
	}

	// 백업 후 데이터가 늘어난 상태에서 복원하면 백업 시점(5행)으로 돌아갑니다.
	makeMore, _ := sql.Open("sqlite", "file:"+dbPath)
	makeMore.Exec("INSERT INTO kine(name, value) VALUES ('/registry/new', 'v')")
	makeMore.Close()
	if countKine(t, dbPath) != 6 {
		t.Fatal("사전 조건 실패")
	}
	var steps []string
	if err := s.RestoreBackup(context.Background(), list[0].Name, func(m string) { steps = append(steps, m) }); err != nil {
		t.Fatal(err)
	}
	if n := countKine(t, dbPath); n != 5 {
		t.Errorf("복원 후 행 수 = %d, want 5", n)
	}
	if !run.called("systemctl stop k3s") || !run.called("systemctl start k3s") {
		t.Errorf("서비스 중지/시작 호출 누락: %v", run.calls)
	}
	pre, _ := filepath.Glob(dbPath + ".pre-restore-*")
	if len(pre) == 0 {
		t.Error("복원 전 DB가 보관되지 않음")
	}
	if len(steps) < 4 {
		t.Errorf("진행 상황 알림 부족: %v", steps)
	}
}

func TestRestoreRejectsTokenMismatchAndBadFile(t *testing.T) {
	s, run, dataDir := testHost(t)
	dbPath := filepath.Join(dataDir, "server", "db", "state.db")
	makeKineDB(t, dbPath, 1)
	tokenPath := filepath.Join(dataDir, "server", "token")
	os.WriteFile(tokenPath, []byte("old-token"), 0o600)
	p, err := s.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(tokenPath, []byte("new-token"), 0o600)
	err = s.RestoreBackup(context.Background(), filepath.Base(p), nil)
	if err == nil || !strings.Contains(err.Error(), "토큰") {
		t.Errorf("토큰 불일치를 거부해야 함: %v", err)
	}
	if run.called("systemctl stop") {
		t.Error("검증 실패 시 서비스를 멈추면 안 됨")
	}
	if err := s.RestoreBackup(context.Background(), "../../etc/passwd", nil); err == nil {
		t.Error("경로 조작을 허용함")
	}
}

func TestManifestsSkipToggle(t *testing.T) {
	s, _, dataDir := testHost(t)
	dir := filepath.Join(dataDir, "server", "manifests")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)

	list, err := s.Manifests()
	if err != nil || len(list) != 2 {
		t.Fatalf("manifest 목록: %v %v", list, err)
	}
	if err := s.SetManifestSkip(list[0].Path, true); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Manifests()
	if !list[0].Skipped || list[1].Skipped {
		t.Errorf("skip 상태: %+v", list)
	}
	if err := s.SetManifestSkip(list[0].Path, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManifestSkip("/etc/passwd", true); err == nil {
		t.Error("manifest 디렉터리 밖 경로를 허용함")
	}
}

func writeCert(t *testing.T, path, cn string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func TestCertificatesSortedByExpiry(t *testing.T) {
	s, _, dataDir := testHost(t)
	now := time.Now()
	writeCert(t, filepath.Join(dataDir, "server", "tls", "late.crt"), "late", now.Add(300*24*time.Hour))
	writeCert(t, filepath.Join(dataDir, "server", "tls", "soon.crt"), "soon", now.Add(10*24*time.Hour))
	writeCert(t, filepath.Join(dataDir, "agent", "client.crt"), "agent", now.Add(100*24*time.Hour))
	certs, err := s.Certificates()
	if err != nil || len(certs) != 3 {
		t.Fatalf("인증서 목록: %v %v", certs, err)
	}
	if certs[0].Subject != "soon" || certs[0].DaysLeft(now) != 9 && certs[0].DaysLeft(now) != 10 {
		t.Errorf("만료 순 정렬 실패: %+v", certs[0])
	}
}

func TestRotateCertificatesStopsAndStarts(t *testing.T) {
	s, run, _ := testHost(t)
	if err := s.RotateCertificates(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl stop k3s", "k3s certificate rotate", "systemctl start k3s"}
	if len(run.calls) != 3 {
		t.Fatalf("호출: %v", run.calls)
	}
	for i, w := range want {
		if !strings.HasPrefix(run.calls[i], w) {
			t.Errorf("%d번째 호출 = %q, want %q", i, run.calls[i], w)
		}
	}
}

func TestServiceControlNeedsRoot(t *testing.T) {
	s, run, _ := testHost(t)
	s.root = false
	if err := s.ServiceControl(context.Background(), OpRestart); err != ErrNotRoot {
		t.Errorf("err = %v", err)
	}
	if len(run.calls) != 0 {
		t.Error("root가 아니면 명령을 실행하면 안 됨")
	}
	s.root = true
	if err := s.ServiceControl(context.Background(), "kill"); err == nil {
		t.Error("알 수 없는 동작을 허용함")
	}
}
