package k3s

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite 드라이버 (cgo 불필요)
)

// DatastoreKind는 K3S 데이터스토어 종류입니다.
type DatastoreKind string

const (
	DatastoreSQLite   DatastoreKind = "sqlite"
	DatastoreEtcd     DatastoreKind = "etcd"
	DatastoreExternal DatastoreKind = "external"
	DatastoreUnknown  DatastoreKind = "unknown"
)

// DatastoreInfo는 데이터스토어 상태입니다.
type DatastoreInfo struct {
	Kind     DatastoreKind
	Path     string // SQLite 파일 또는 etcd 디렉터리
	Endpoint string // external일 때 (자격 증명은 가림)
	Size     int64  // SQLite: db+wal 크기
}

// BackupFile은 백업 파일 하나입니다.
type BackupFile struct {
	Name     string
	Path     string
	Size     int64
	Time     time.Time
	Kind     DatastoreKind
	HasToken bool
}

const sqlitePrefix = "k3s-sqlite-"
const etcdPrefix = "k3stui-etcd"

// Datastore는 데이터스토어 종류를 판별합니다 (설계 6장).
func (s *System) Datastore() DatastoreInfo {
	if kc, err := s.LoadConfig(); err == nil {
		if ep := kc.String("datastore-endpoint"); ep != "" {
			return DatastoreInfo{Kind: DatastoreExternal, Endpoint: maskEndpoint(ep)}
		}
	}
	dbDir := filepath.Join(s.DataDir(), "server", "db")
	if st, err := os.Stat(filepath.Join(dbDir, "etcd")); err == nil && st.IsDir() {
		return DatastoreInfo{Kind: DatastoreEtcd, Path: filepath.Join(dbDir, "etcd")}
	}
	db := filepath.Join(dbDir, "state.db")
	if st, err := os.Stat(db); err == nil {
		size := st.Size()
		if w, err := os.Stat(db + "-wal"); err == nil {
			size += w.Size()
		}
		return DatastoreInfo{Kind: DatastoreSQLite, Path: db, Size: size}
	}
	return DatastoreInfo{Kind: DatastoreUnknown}
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
	host, _ := os.Hostname()
	stamp := s.now().Format("20060102-150405")
	var path string
	switch ds.Kind {
	case DatastoreSQLite:
		path = filepath.Join(dir, sqlitePrefix+host+"-"+stamp+".db")
		if err := BackupSQLite(ctx, ds.Path, path); err != nil {
			return "", err
		}
		// SQLite 복원 시 같은 서버 토큰이 필요하므로 함께 보관합니다.
		if tok, err := s.Token(); err == nil {
			_ = os.WriteFile(path+".token", []byte(tok+"\n"), 0o600)
		}
	case DatastoreEtcd:
		name := etcdPrefix
		out, err := s.k3s(ctx, "etcd-snapshot", "save", "--data-dir", s.DataDir(), "--dir", dir, "--name", name)
		if err != nil {
			return "", err
		}
		path = lastField(string(out))
	case DatastoreExternal:
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

// ListBackups는 백업 디렉터리의 백업 파일을 최신순으로 돌려줍니다.
func (s *System) ListBackups() ([]BackupFile, error) {
	entries, err := os.ReadDir(s.cfg.Backup.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, ErrNotRoot
		}
		return nil, err
	}
	var out []BackupFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".token") {
			continue
		}
		var kind DatastoreKind
		switch {
		case strings.HasPrefix(name, sqlitePrefix) && strings.HasSuffix(name, ".db"):
			kind = DatastoreSQLite
		case strings.HasPrefix(name, etcdPrefix):
			kind = DatastoreEtcd
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(s.cfg.Backup.Dir, name)
		_, tokErr := os.Stat(p + ".token")
		out = append(out, BackupFile{Name: name, Path: p, Size: info.Size(), Time: info.ModTime(), Kind: kind, HasToken: tokErr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func (s *System) prune(ctx context.Context, kind DatastoreKind) error {
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
	if name != filepath.Base(name) || name == "" {
		return fmt.Errorf("잘못된 파일 이름: %q", name)
	}
	p := filepath.Join(s.cfg.Backup.Dir, name)
	if strings.HasPrefix(name, etcdPrefix) {
		// etcd 스냅샷은 K3S가 기록(ConfigMap)도 관리하므로 k3s 명령으로 지웁니다.
		if _, err := s.k3s(ctx, "etcd-snapshot", "delete", "--data-dir", s.DataDir(), "--dir", s.cfg.Backup.Dir, name); err == nil {
			return nil
		}
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + ".token")
	return nil
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
	if ds.Kind != DatastoreSQLite {
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
	progress("k3s 서비스 중지")
	if err := s.ServiceControl(ctx, OpStop); err != nil {
		return err
	}
	restart := func() error {
		progress("k3s 서비스 시작")
		return s.ServiceControl(ctx, OpStart)
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
	if err := copyFile(src, ds.Path, 0o600); err != nil {
		// 실패 시 원래 DB를 되돌립니다.
		for _, suf := range []string{"", "-wal", "-shm"} {
			_ = os.Rename(ds.Path+".pre-restore-"+stamp+suf, ds.Path+suf)
		}
		_ = restart()
		return fmt.Errorf("백업 복사 실패 (원래 DB로 되돌림): %w", err)
	}
	return restart()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
