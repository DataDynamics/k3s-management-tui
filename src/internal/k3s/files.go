package k3s

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ---- 자동 배포 manifest ----

// Manifest는 <data-dir>/server/manifests의 파일 하나입니다.
type Manifest struct {
	Path    string
	Rel     string
	Size    int64
	ModTime time.Time
	Skipped bool // <파일>.skip이 있으면 K3S가 배포하지 않습니다
}

// ManifestsDir는 자동 배포 디렉터리입니다.
func (s *System) ManifestsDir() string { return filepath.Join(s.DataDir(), "server", "manifests") }

// Manifests는 manifest 파일 목록(하위 디렉터리 포함)입니다.
func (s *System) Manifests() ([]Manifest, error) {
	root := s.ManifestsDir()
	var out []Manifest
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		_, skipErr := os.Stat(p + ".skip")
		out = append(out, Manifest{Path: p, Rel: rel, Size: info.Size(), ModTime: info.ModTime(), Skipped: skipErr == nil})
		return nil
	})
	if errors.Is(err, os.ErrPermission) {
		return nil, ErrNotRoot
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

// SetManifestSkip은 .skip 파일을 만들거나 지웁니다.
func (s *System) SetManifestSkip(path string, skip bool) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	root := s.ManifestsDir()
	if rel, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("manifest 디렉터리 밖의 경로입니다: %s", path)
	}
	if skip {
		return os.WriteFile(path+".skip", nil, 0o644)
	}
	err := os.Remove(path + ".skip")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ---- 인증서 ----

// CertInfo는 인증서 파일 하나의 요약입니다.
type CertInfo struct {
	Path     string
	Subject  string
	Issuer   string
	NotAfter time.Time
	IsCA     bool
}

// DaysLeft는 만료까지 남은 일수입니다.
func (c CertInfo) DaysLeft(now time.Time) int {
	return int(c.NotAfter.Sub(now).Hours() / 24)
}

// Certificates는 server/tls, server/tls/etcd, agent의 .crt 파일을 읽습니다.
func (s *System) Certificates() ([]CertInfo, error) {
	dd := s.DataDir()
	var files []string
	for _, pat := range []string{"server/tls/*.crt", "server/tls/etcd/*.crt", "agent/*.crt"} {
		m, _ := filepath.Glob(filepath.Join(dd, pat))
		files = append(files, m...)
	}
	if len(files) == 0 {
		if _, err := os.ReadDir(filepath.Join(dd, "server", "tls")); errors.Is(err, os.ErrPermission) {
			return nil, ErrNotRoot
		}
	}
	var out []CertInfo
	for _, f := range files {
		ci, err := ParseCertFile(f)
		if err != nil {
			continue
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NotAfter.Before(out[j].NotAfter) })
	return out, nil
}

// ParseCertFile은 PEM 파일의 첫 번째 인증서를 읽습니다.
func ParseCertFile(path string) (CertInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CertInfo{}, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return CertInfo{}, fmt.Errorf("%s: PEM 인증서가 아닙니다", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertInfo{}, err
	}
	return CertInfo{
		Path: path, Subject: cert.Subject.CommonName, Issuer: cert.Issuer.CommonName,
		NotAfter: cert.NotAfter, IsCA: cert.IsCA,
	}, nil
}

// RotateCertificates는 k3s를 멈추고 인증서를 갱신한 뒤 다시 시작합니다 (CA 인증서는 갱신되지 않음).
func (s *System) RotateCertificates(ctx context.Context, progress func(string)) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if progress == nil {
		progress = func(string) {}
	}
	progress("k3s 서비스 중지")
	if err := s.ServiceControl(ctx, OpStop); err != nil {
		return err
	}
	progress("k3s certificate rotate")
	_, rotErr := s.k3s(ctx, "certificate", "rotate", "--data-dir", s.DataDir())
	progress("k3s 서비스 시작")
	if err := s.ServiceControl(ctx, OpStart); err != nil {
		return errors.Join(rotErr, err)
	}
	return rotErr
}

// ---- 토큰 ----

// Token은 서버 토큰입니다 (노드 추가용).
func (s *System) Token() (string, error) {
	for _, name := range []string{"token", "node-token"} {
		data, err := os.ReadFile(filepath.Join(s.DataDir(), "server", name))
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		if errors.Is(err, os.ErrPermission) {
			return "", ErrNotRoot
		}
	}
	return "", fmt.Errorf("토큰 파일이 없습니다 (agent 노드이거나 data-dir이 다릅니다)")
}

// JoinCommand는 agent 노드 추가 명령을 만듭니다.
func JoinCommand(serverIP, token string) string {
	return fmt.Sprintf("curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s sh -", serverIP, token)
}

// ---- 디스크 ----

// DiskInfo는 디렉터리가 속한 파일시스템 사용량입니다.
type DiskInfo struct {
	Label string
	Path  string
	Total uint64
	Used  uint64
	Err   string
}

// Percent는 사용률(%)입니다.
func (d DiskInfo) Percent() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Used) * 100 / float64(d.Total)
}

// DiskUsage는 data-dir, kubelet, 백업 디렉터리의 파일시스템 사용량입니다.
func (s *System) DiskUsage() []DiskInfo {
	items := []DiskInfo{
		{Label: "data-dir", Path: s.DataDir()},
		{Label: "kubelet", Path: s.KubeletDir()},
		{Label: "backup", Path: s.cfg.Backup.Dir},
	}
	for i := range items {
		p := items[i].Path
		// 백업 디렉터리가 아직 없으면 상위 디렉터리 기준으로 봅니다.
		for p != "/" {
			if _, err := os.Stat(p); err == nil {
				break
			}
			p = filepath.Dir(p)
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			items[i].Err = err.Error()
			continue
		}
		bs := uint64(st.Bsize)
		items[i].Total = st.Blocks * bs
		items[i].Used = (st.Blocks - st.Bfree) * bs
	}
	return items
}
