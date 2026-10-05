package kubeadm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// Certificates는 PKI 인증서, 컨트롤 플레인 kubeconfig의 클라이언트 인증서, kubelet 인증서를 읽습니다.
func (s *System) Certificates() ([]host.CertInfo, error) {
	kd := s.kc.KubernetesDir
	kubelet := filepath.Join(s.KubeletDir(), "pki")
	return host.ScanCerts(kd, []host.CertSource{
		{Glob: filepath.Join(kd, "pki", "*.crt")},
		{Glob: filepath.Join(kd, "pki", "etcd", "*.crt")},
		{Glob: filepath.Join(kd, "admin.conf"), Kubeconfig: true},
		{Glob: filepath.Join(kd, "super-admin.conf"), Kubeconfig: true},
		{Glob: filepath.Join(kd, "controller-manager.conf"), Kubeconfig: true},
		{Glob: filepath.Join(kd, "scheduler.conf"), Kubeconfig: true},
		{Glob: filepath.Join(kubelet, "kubelet-client-current.pem")},
		{Glob: filepath.Join(kubelet, "kubelet.crt")},
	})
}

// controlPlaneComponents는 인증서 갱신 후 재시작할 static Pod 컨테이너입니다.
var controlPlaneComponents = []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler", "etcd"}

// RotateCertificates는 'kubeadm certs renew all' 후 컨트롤 플레인 컨테이너를 재시작합니다.
// 컨테이너를 멈추면 kubelet이 같은 static Pod를 바로 다시 띄우므로 새 인증서를 읽게 됩니다.
func (s *System) RotateCertificates(ctx context.Context, progress func(string)) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if !s.ControlPlane() {
		return fmt.Errorf("컨트롤 플레인 노드가 아닙니다")
	}
	if progress == nil {
		progress = func(string) {}
	}
	progress("kubeadm certs renew all")
	if _, err := s.run.Run(ctx, s.kc.Binary, "certs", "renew", "all"); err != nil {
		return err
	}
	for _, name := range controlPlaneComponents {
		out, err := s.crictl(ctx, "ps", "--name", "^"+name+"$", "--state", "running", "-q")
		if err != nil {
			return fmt.Errorf("%s 컨테이너 조회 실패: %w", name, err)
		}
		ids := strings.Fields(string(out))
		if len(ids) == 0 {
			continue // 외부 etcd 등
		}
		progress(name + " 재시작")
		if _, err := s.crictl(ctx, append([]string{"stop"}, ids...)...); err != nil {
			return fmt.Errorf("%s 재시작 실패: %w", name, err)
		}
	}
	progress("kube-apiserver 기동 대기")
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		out, err := s.crictl(ctx, "ps", "--name", "^kube-apiserver$", "--state", "running", "-q")
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("인증서는 갱신했지만 kube-apiserver가 3분 안에 다시 뜨지 않았습니다 (journalctl -u kubelet 확인)")
}
