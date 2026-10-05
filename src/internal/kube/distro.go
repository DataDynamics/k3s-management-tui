package kube

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
)

// DistroFromVersion은 API 서버 버전 문자열로 배포판을 추정합니다.
// 버전만으로 알 수 없으면 ""를 돌려줍니다.
//
//	v1.36.5+k3s1 → k3s, v1.30.4+rke2r1 → rke2
//
// 관리형 클러스터(EKS, GKE, AKS 등)는 따로 구분하지 않고 kubernetes로 봅니다.
func DistroFromVersion(v string) string {
	switch {
	case strings.Contains(v, "+k3s"):
		return config.DistroK3s
	case strings.Contains(v, "+rke2"):
		return config.DistroRKE2
	}
	return ""
}

// DetectDistro는 배포판을 판별합니다. 버전으로 알 수 없으면
// kube-system/kubeadm-config ConfigMap이 있는지로 kubeadm 여부를 확인합니다 (kind, minikube 포함).
func (c *Client) DetectDistro(ctx context.Context) string {
	if d := DistroFromVersion(c.ServerVersion); d != "" {
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.Core.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubeadm-config", metav1.GetOptions{}); err == nil {
		return config.DistroKubeadm
	}
	return config.DistroKubernetes
}

// DistroName은 화면에 표시할 배포판 이름입니다.
func DistroName(d string) string {
	switch d {
	case config.DistroK3s:
		return "K3S"
	case config.DistroRKE2:
		return "RKE2"
	case config.DistroKubeadm:
		return "kubeadm"
	}
	return "Kubernetes"
}

// IsLocalServer는 API 서버 주소가 이 호스트(루프백 또는 로컬 인터페이스 IP)를 가리키는지 판단합니다.
// 호스트 관리 기능은 TUI가 그 노드에서 실행될 때만 의미가 있습니다.
func IsLocalServer(server string) bool {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return false
		}
		ips = ips[:0]
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	local, _ := net.InterfaceAddrs()
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
		for _, a := range local {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return true
			}
		}
	}
	return false
}
