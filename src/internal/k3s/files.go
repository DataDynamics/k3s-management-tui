package k3s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// ---- 자동 배포 manifest ----

// ManifestsDir는 자동 배포 디렉터리입니다.
func (s *System) ManifestsDir() string { return filepath.Join(s.DataDir(), "server", "manifests") }

// Manifests는 manifest 파일 목록(하위 디렉터리 포함)입니다.
func (s *System) Manifests() ([]host.Manifest, error) {
	return host.ListManifests(s.ManifestsDir(), true)
}

// SetManifestSkip은 .skip 파일을 만들거나 지웁니다.
func (s *System) SetManifestSkip(path string, skip bool) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if !host.InsideDir(s.ManifestsDir(), path) {
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

// WriteManifest는 K3S·RKE2에서는 지원하지 않습니다. 패키지 manifest는 시작할 때 덮어쓰므로 .skip으로 관리합니다.
func (s *System) WriteManifest(string, []byte) (string, error) { return "", host.ErrUnsupported }

// ---- 인증서 ----

// Certificates는 server/tls, server/tls/etcd, agent의 인증서를 읽습니다.
func (s *System) Certificates() ([]host.CertInfo, error) {
	dd := s.DataDir()
	return host.ScanCerts(dd, []host.CertSource{
		{Glob: filepath.Join(dd, "server/tls/*.crt")},
		{Glob: filepath.Join(dd, "server/tls/etcd/*.crt")},
		{Glob: filepath.Join(dd, "agent/*.crt")},
	})
}

// RotateCertificates는 k3s를 멈추고 인증서를 갱신한 뒤 다시 시작합니다 (CA 인증서는 갱신되지 않음).
func (s *System) RotateCertificates(ctx context.Context, progress func(string)) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if progress == nil {
		progress = func(string) {}
	}
	progress(s.fl.service + " 서비스 중지")
	if err := s.ServiceControl(ctx, host.OpStop); err != nil {
		return err
	}
	progress(s.fl.cmd + " certificate rotate")
	_, rotErr := s.cli(ctx, "certificate", "rotate", "--data-dir", s.DataDir())
	progress(s.fl.service + " 서비스 시작")
	if err := s.ServiceControl(ctx, host.OpStart); err != nil {
		return errors.Join(rotErr, err)
	}
	return rotErr
}

// ---- 토큰 ----

// Token은 서버 토큰입니다 (노드 추가, SQLite 복원 검사용).
func (s *System) Token() (string, error) {
	for _, name := range []string{"token", "node-token"} {
		data, err := os.ReadFile(filepath.Join(s.DataDir(), "server", name))
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		if errors.Is(err, os.ErrPermission) {
			return "", host.ErrNotRoot
		}
	}
	return "", fmt.Errorf("토큰 파일이 없습니다 (agent 노드이거나 data-dir이 다릅니다)")
}

// JoinCommand는 agent 노드 추가 명령을 만듭니다. 서버 토큰을 그대로 담습니다.
func (s *System) JoinCommand(_ context.Context, serverIP string) (string, error) {
	tok, err := s.Token()
	if err != nil {
		return "", err
	}
	if s.fl.distro == config.DistroRKE2 {
		return BuildRKE2JoinCommand(serverIP, tok), nil
	}
	return BuildJoinCommand(serverIP, tok), nil
}

// BuildJoinCommand는 agent 설치 명령 문자열입니다.
func BuildJoinCommand(serverIP, token string) string {
	return fmt.Sprintf("curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s sh -", serverIP, token)
}

// ---- 디스크 ----

// DiskUsage는 data-dir, kubelet, 백업 디렉터리의 파일시스템 사용량입니다.
func (s *System) DiskUsage() []host.DiskInfo {
	return host.FillDiskUsage([]host.DiskInfo{
		{Label: "data-dir", Path: s.DataDir()},
		{Label: "kubelet", Path: s.KubeletDir()},
		{Label: "backup", Path: s.cfg.Backup.Dir},
	})
}
