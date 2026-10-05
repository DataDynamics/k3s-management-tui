package kube

import (
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
)

func TestDistroFromVersion(t *testing.T) {
	cases := map[string]string{
		"v1.36.5+k3s1":        config.DistroK3s,
		"v1.30.4+rke2r1":      config.DistroRKE2,
		"v1.29.1-eks-b9c9ed7": config.DistroEKS,
		"v1.29.1-gke.1589000": config.DistroGKE,
		"v1.31.0":             "",
	}
	for v, want := range cases {
		if got := DistroFromVersion(v); got != want {
			t.Errorf("DistroFromVersion(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestDetectDistroKubeadm(t *testing.T) {
	c := &Client{ServerVersion: "v1.31.0", Core: fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "kubeadm-config", Namespace: "kube-system"}})}
	if d := c.DetectDistro(t.Context()); d != config.DistroKubeadm {
		t.Errorf("kubeadm 판별 실패: %s", d)
	}
	c = &Client{ServerVersion: "v1.31.0", Core: fake.NewClientset()}
	if d := c.DetectDistro(t.Context()); d != config.DistroKubernetes {
		t.Errorf("일반 Kubernetes 판별 실패: %s", d)
	}
	if DistroName(config.DistroK3s) != "K3S" || DistroName("xyz") != "Kubernetes" {
		t.Error("표시 이름")
	}
}

func TestIsLocalServer(t *testing.T) {
	for server, want := range map[string]bool{
		"https://127.0.0.1:6443":   true,
		"https://localhost:6443":   true,
		"https://[::1]:6443":       true,
		"https://203.0.113.10:443": false, // 문서용 예약 주소
		"":                         false,
	} {
		if got := IsLocalServer(server); got != want {
			t.Errorf("IsLocalServer(%q) = %v, want %v", server, got, want)
		}
	}
}

const kubeconfigTmpl = `apiVersion: v1
kind: Config
clusters:
- name: a
  cluster: {server: https://10.0.0.1:6443}
- name: b
  cluster: {server: https://10.0.0.2:6443}
users:
- name: u
  user: {token: x}
contexts:
- name: ctx-a
  context: {cluster: a, user: u}
- name: ctx-b
  context: {cluster: b, user: u}
current-context: ctx-a
`

func TestKubeconfigUsableAndContext(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte(kubeconfigTmpl), 0o600)
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, []byte("apiVersion: v1\nkind: Config\nclusters: null\ncontexts: null\ncurrent-context: \"\"\n"), 0o600)

	if err := UsableKubeconfig(good, ""); err != nil {
		t.Errorf("정상 kubeconfig를 거부: %v", err)
	}
	if err := UsableKubeconfig(empty, ""); err == nil {
		t.Error("빈 kubeconfig(~/.kube/config 기본 파일)를 허용함")
	}
	if err := UsableKubeconfig(filepath.Join(dir, "none"), ""); err == nil {
		t.Error("없는 파일을 허용함")
	}
	if s, _ := ServerURL(good, ""); s != "https://10.0.0.1:6443" {
		t.Errorf("current-context 서버: %s", s)
	}
	if s, _ := ServerURL(good, "ctx-b"); s != "https://10.0.0.2:6443" {
		t.Errorf("--context 서버: %s", s)
	}
	// KUBECONFIG 형식(여러 파일)도 읽습니다.
	if s, _ := ServerURL(empty+string(os.PathListSeparator)+good, ""); s != "https://10.0.0.1:6443" {
		t.Errorf("여러 파일 병합: %s", s)
	}
}
