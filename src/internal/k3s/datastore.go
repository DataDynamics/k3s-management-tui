package k3s

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // SQLite 드라이버 (cgo 불필요)

	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

const sqlitePrefix = "k3s-sqlite-"
const etcdPrefix = "k3stui-etcd"

// Datastore는 데이터스토어 종류를 판별합니다 (설계 6장).
func (s *System) Datastore() host.DatastoreInfo {
	if kc, err := s.LoadConfig(); err == nil {
		if ep := kc.String("datastore-endpoint"); ep != "" {
			return host.DatastoreInfo{Kind: host.DatastoreExternal, Endpoint: maskEndpoint(ep)}
		}
	}
	dbDir := filepath.Join(s.DataDir(), "server", "db")
	if st, err := os.Stat(filepath.Join(dbDir, "etcd")); err == nil && st.IsDir() {
		return host.DatastoreInfo{Kind: host.DatastoreEtcd, Path: filepath.Join(dbDir, "etcd")}
	}
	db := filepath.Join(dbDir, "state.db")
	if st, err := os.Stat(db); err == nil && s.fl.sqlite {
		size := st.Size()
		if w, err := os.Stat(db + "-wal"); err == nil {
			size += w.Size()
		}
		return host.DatastoreInfo{Kind: host.DatastoreSQLite, Path: db, Size: size}
	}
	return host.DatastoreInfo{Kind: host.DatastoreUnknown}
}

// maskEndpoint는 "mysql://user:pass@tcp(host)/db"의 비밀번호를 가립니다.
func maskEndpoint(ep string) string {
	at := strings.LastIndex(ep, "@")
	scheme := strings.Index(ep, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return ep
	}
	cred := ep[scheme+3 : at]
	if user, _, ok := strings.Cut(cred, ":"); ok {
		return ep[:scheme+3] + user + ":****" + ep[at:]
	}
	return ep
}

func (s *System) backupDir() (string, error) {
	dir := s.cfg.Backup.Dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Backup은 데이터스토어를 백업하고 보관 개수를 넘는 오래된 백업을 지웁니다.
func (s *System) Backup(ctx context.Context) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	ds := s.Datastore()
	dir, err := s.backupDir()
	if err != nil {
		return "", err
	}
	hostname, _ := os.Hostname()
	stamp := s.now().Format("20060102-150405")
	var path string
	switch ds.Kind {
	case host.DatastoreSQLite:
		path = filepath.Join(dir, sqlitePrefix+hostname+"-"+stamp+".db")
		if err := BackupSQLite(ctx, ds.Path, path); err != nil {
			return "", err
		}
		// SQLite 복원 시 같은 서버 토큰이 필요하므로 함께 보관합니다.
		if tok, err := s.Token(); err == nil {
			_ = os.WriteFile(path+".token", []byte(tok+"\n"), 0o600)
		}
	case host.DatastoreEtcd:
		name := etcdPrefix
		out, err := s.cli(ctx, "etcd-snapshot", "save", "--data-dir", s.DataDir(), "--dir", dir, "--name", name)
		if err != nil {
			return "", err
		}
		path = lastField(string(out))
	case host.DatastoreExternal:
		return "", fmt.Errorf("외부 데이터스토어(%s)는 해당 DB의 백업 도구를 사용하세요", ds.Endpoint)
	default:
		return "", fmt.Errorf("데이터스토어를 찾을 수 없습니다 (data-dir: %s)", s.DataDir())
	}
	if err := s.prune(ctx, ds.Kind); err != nil {
		return path, fmt.Errorf("백업은 성공했지만 오래된 백업 정리 실패: %w", err)
	}
	return path, nil
}

func lastField(s string) string {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// BackupSQLite는 VACUUM INTO로 실행 중인 DB의 일관된 사본을 만듭니다.
// K3S(kine)가 쓰는 중에도 안전하며 WAL 내용까지 포함됩니다.
func BackupSQLite(ctx context.Context, src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("이미 존재하는 파일입니다: %s", dst)
	}
	db, err := sql.Open("sqlite", "file:"+src+"?_pragma=busy_timeout(30000)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		os.Remove(dst)
		return fmt.Errorf("SQLite 백업 실패: %w", err)
	}
	return os.Chmod(dst, 0o600)
}

// CheckSQLite는 백업 파일 무결성을 검사합니다.
func CheckSQLite(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("무결성 검사 실패: %s", res)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM kine").Scan(&n); err != nil {
		return fmt.Errorf("kine 테이블이 없습니다 (K3S 백업 파일이 아님): %w", err)
	}
	return nil
}

// ListBackups는 백업 디렉터리의 SQLite 백업과 etcd 스냅샷을 최신순으로 돌려줍니다.
func (s *System) ListBackups() ([]host.BackupFile, error) {
	sq, err := host.ListBackupFiles(s.cfg.Backup.Dir, host.DatastoreSQLite, sqlitePrefix, ".db", ".token")
	if err != nil {
		return nil, err
	}
	et, err := host.ListBackupFiles(s.cfg.Backup.Dir, host.DatastoreEtcd, etcdPrefix, "", ".token")
	if err != nil {
		return nil, err
	}
	out := append(sq, et...)
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func (s *System) prune(ctx context.Context, kind host.DatastoreKind) error {
	list, err := s.ListBackups()
	if err != nil {
		return err
	}
	n := 0
	for _, b := range list {
		if b.Kind != kind {
			continue
		}
		n++
		if n > s.cfg.Backup.Keep {
			if err := s.DeleteBackup(ctx, b.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteBackup은 백업 파일을 지웁니다.
func (s *System) DeleteBackup(ctx context.Context, name string) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if err := host.ValidBackupName(name); err != nil {
		return err
	}
	p := filepath.Join(s.cfg.Backup.Dir, name)
	if strings.HasPrefix(name, etcdPrefix) {
		// etcd 스냅샷은 K3S·RKE2가 기록(ConfigMap)도 관리하므로 k3s·rke2 명령으로 지웁니다.
		if _, err := s.cli(ctx, "etcd-snapshot", "delete", "--data-dir", s.DataDir(), "--dir", s.cfg.Backup.Dir, name); err == nil {
			return nil
		}
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + ".token")
	return nil
}

// RestoreGuide는 자동 복원을 지원하지 않는 경우(etcd)의 수동 복원 절차입니다.
func (s *System) RestoreGuide(name string) string {
	p := filepath.Join(s.cfg.Backup.Dir, name)
	c, svc := s.fl.cmd, s.fl.service
	return s.fl.name + " embedded etcd 스냅샷 복원 절차 (모든 서버 노드에 영향이 있습니다)\n\n" +
		"1. 모든 서버 노드에서 서비스를 중지합니다:  systemctl stop " + svc + "\n" +
		"2. 첫 번째 서버에서 클러스터를 스냅샷으로 초기화합니다:\n" +
		"   " + c + " server --cluster-reset --cluster-reset-restore-path=" + p + " --data-dir " + s.DataDir() + "\n" +
		"3. 완료 메시지가 나오면 서비스를 시작합니다:  systemctl start " + svc + "\n" +
		"4. 나머지 서버 노드는 " + s.DataDir() + "/server/db 를 지운 뒤 서비스를 시작해 다시 합류시킵니다.\n\n" +
		"자세한 내용: " + s.fl.docsURL + "\n"
}

// RestoreBackup은 SQLite 백업으로 데이터스토어를 되돌립니다.
// 순서: 무결성 검사 → k3s 중지 → 현재 DB를 pre-restore로 이동 → 백업 복사 → k3s 시작.
func (s *System) RestoreBackup(ctx context.Context, name string, progress func(string)) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if progress == nil {
		progress = func(string) {}
	}
	ds := s.Datastore()
	if ds.Kind != host.DatastoreSQLite {
		return fmt.Errorf("자동 복원은 SQLite 데이터스토어만 지원합니다 (현재: %s). etcd는 'k3s server --cluster-reset --cluster-reset-restore-path=<파일>'을 사용하세요", ds.Kind)
	}
	if name != filepath.Base(name) || !strings.HasPrefix(name, sqlitePrefix) {
		return fmt.Errorf("SQLite 백업 파일이 아닙니다: %s", name)
	}
	src := filepath.Join(s.cfg.Backup.Dir, name)
	progress("백업 파일 무결성 검사")
	if err := CheckSQLite(ctx, src); err != nil {
		return err
	}
	if tok, err := os.ReadFile(src + ".token"); err == nil {
		if cur, err := s.Token(); err == nil && strings.TrimSpace(string(tok)) != cur {
			return fmt.Errorf("백업 시점의 서버 토큰이 현재 토큰과 다릅니다. 토큰을 먼저 맞춘 뒤 복원하세요 (%s.token)", src)
		}
	}
	progress(s.fl.service + " 서비스 중지")
	if err := s.ServiceControl(ctx, host.OpStop); err != nil {
		return err
	}
	restart := func() error {
		progress(s.fl.service + " 서비스 시작")
		return s.ServiceControl(ctx, host.OpStart)
	}
	stamp := s.now().Format("20060102-150405")
	progress("현재 DB 보관: state.db.pre-restore-" + stamp)
	for _, suf := range []string{"", "-wal", "-shm"} {
		cur := ds.Path + suf
		if _, err := os.Stat(cur); err == nil {
			if err := os.Rename(cur, ds.Path+".pre-restore-"+stamp+suf); err != nil {
				_ = restart()
				return fmt.Errorf("현재 DB 이동 실패: %w", err)
			}
		}
	}
	progress("백업 복사")
	if err := host.CopyFile(src, ds.Path, 0o600); err != nil {
		// 실패 시 원래 DB를 되돌립니다.
		for _, suf := range []string{"", "-wal", "-shm"} {
			_ = os.Rename(ds.Path+".pre-restore-"+stamp+suf, ds.Path+suf)
		}
		_ = restart()
		return fmt.Errorf("백업 복사 실패 (원래 DB로 되돌림): %w", err)
	}
	return restart()
}
