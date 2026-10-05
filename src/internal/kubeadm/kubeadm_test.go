package kubeadm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/config"
	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

// fakeRunner는 실행된 명령을 기록하고, 필요하면 부수 효과(파일 생성)를 흉내 냅니다.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	reply func(line string, args []string) ([]byte, error)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	line := strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(line, args)
	}
	return nil, nil
}

func (f *fakeRunner) has(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

type fixture struct {
	sys     *System
	run     *fakeRunner
	root    string
	kdir    string
	etcdDir string
}

const apiserverManifest = `apiVersion: v1
kind: Pod
metadata:
  name: kube-apiserver
  namespace: kube-system
spec:
  containers:
  - name: kube-apiserver
    image: registry.k8s.io/kube-apiserver:v1.31.0
    command:
    - kube-apiserver
    - --etcd-servers=https://127.0.0.1:2379
`

func etcdManifest(dataDir string) string {
	return `apiVersion: v1
kind: Pod
metadata:
  name: etcd
  namespace: kube-system
spec:
  containers:
  - name: etcd
    image: registry.k8s.io/etcd:3.5.15-0
    command:
    - etcd
    - --data-dir=` + dataDir + `
    - --listen-client-urls=https://127.0.0.1:2379
`
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newFixture는 컨트롤 플레인 노드(etcd 포함) 파일 구조를 임시 디렉터리에 만듭니다.
func newFixture(t *testing.T, controlPlane bool) *fixture {
	t.Helper()
	root := t.TempDir()
	kdir := filepath.Join(root, "etc", "kubernetes")
	etcdDir := filepath.Join(root, "var", "lib", "etcd")
	cfg := config.Default()
	cfg.Kubeadm.KubernetesDir = kdir
	cfg.Kubeadm.KubeletConfig = filepath.Join(root, "var", "lib", "kubelet", "config.yaml")
	cfg.Kubeadm.KubeletFlags = filepath.Join(root, "var", "lib", "kubelet", "kubeadm-flags.env")
	cfg.Kubeadm.Etcdctl = "k3stui-test-no-such-etcdctl" // 호스트 etcdctl이 없는 경우(컨테이너 안 etcdctl 사용)를 시험합니다
	cfg.Backup.Dir = filepath.Join(root, "backups")
	cfg.Backup.Keep = 2

	write(t, cfg.Kubeadm.KubeletConfig, "apiVersion: kubelet.config.k8s.io/v1beta1\nkind: KubeletConfiguration\ncgroupDriver: systemd\n")
	write(t, cfg.Kubeadm.KubeletFlags, `KUBELET_KUBEADM_ARGS="--container-runtime-endpoint=unix:///run/containerd/containerd.sock --root-dir=/data/kubelet"`+"\n")
	write(t, filepath.Join(kdir, "pki", "ca.crt"), "")
	if controlPlane {
		write(t, filepath.Join(kdir, "manifests", "kube-apiserver.yaml"), apiserverManifest)
		write(t, filepath.Join(kdir, "manifests", "etcd.yaml"), etcdManifest(etcdDir))
		write(t, filepath.Join(etcdDir, "member", "snap", "db"), strings.Repeat("x", 1024))
		write(t, filepath.Join(kdir, "pki", "etcd", "ca.crt"), "ca")
	}

	run := &fakeRunner{}
	s := NewSystem(cfg, run)
	s.root, s.sd.Root, s.sd.Ctl = true, true, run
	return &fixture{sys: s, run: run, root: root, kdir: kdir, etcdDir: etcdDir}
}

func TestCapsControlPlaneAndWorker(t *testing.T) {
	cp := newFixture(t, true).sys.Caps()
	if !cp.Backup || cp.Restore || cp.CertRotateHint == "" || !cp.JoinMutates || !cp.ManifestEdit || cp.ManifestSkip {
		t.Errorf("컨트롤 플레인 기능: %+v", cp)
	}
	if cp.CheckLabel != "certs check-expiration" || cp.BackupExtra != "PKI" {
		t.Errorf("점검·부가 파일 이름: %+v", cp)
	}
	w := newFixture(t, false).sys.Caps()
	if w.Backup || w.CertRotateHint != "" || w.JoinLabel != "" || w.CheckLabel != "" {
		t.Errorf("워커 노드는 백업·인증서 갱신·노드 추가를 끄고 있어야 함: %+v", w)
	}
}

func TestDatastoreAndPaths(t *testing.T) {
	f := newFixture(t, true)
	ds := f.sys.Datastore()
	if ds.Kind != host.DatastoreEtcd || ds.Path != f.etcdDir || ds.Size != 1024 {
		t.Errorf("etcd 판별: %+v", ds)
	}
	if f.sys.KubeletDir() != "/data/kubelet" {
		t.Errorf("kubelet root-dir: %s", f.sys.KubeletDir())
	}
	if got := strings.Join(f.sys.CrictlCommand(), " "); got != "crictl --runtime-endpoint unix:///run/containerd/containerd.sock" {
		t.Errorf("crictl 명령: %s", got)
	}
	// 외부 etcd: etcd.yaml이 없고 apiserver가 원격 etcd를 가리킴
	os.Remove(filepath.Join(f.kdir, "manifests", "etcd.yaml"))
	write(t, filepath.Join(f.kdir, "manifests", "kube-apiserver.yaml"), strings.Replace(apiserverManifest, "127.0.0.1", "10.0.0.5", 1))
	if ds := f.sys.Datastore(); ds.Kind != host.DatastoreExternal || !strings.Contains(ds.Endpoint, "10.0.0.5") {
		t.Errorf("외부 etcd 판별: %+v", ds)
	}
	if _, err := f.sys.Backup(context.Background()); err == nil || !strings.Contains(err.Error(), "외부 etcd") {
		t.Errorf("외부 etcd 백업은 거부해야 함: %v", err)
	}
}

func TestValidateConfigAndManifest(t *testing.T) {
	f := newFixture(t, true)
	if err := f.sys.ValidateConfig([]byte("kind: KubeletConfiguration\n")); err != nil {
		t.Error(err)
	}
	if err := f.sys.ValidateConfig([]byte("kind: Pod\n")); err == nil {
		t.Error("kubelet 설정에 다른 kind를 허용함")
	}
	if err := ValidatePodManifest([]byte(apiserverManifest)); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"kind: Deployment\nmetadata: {name: x}\nspec: {containers: [{}]}\n", "kind: Pod\nspec: {containers: [{}]}\n", "kind: Pod\nmetadata: {name: x}\n", "a: [\n"} {
		if ValidatePodManifest([]byte(bad)) == nil {
			t.Errorf("잘못된 static Pod를 허용함: %q", bad)
		}
	}
}

func TestWriteManifestBacksUpOutsideManifestsDir(t *testing.T) {
	f := newFixture(t, true)
	p := filepath.Join(f.kdir, "manifests", "kube-apiserver.yaml")
	edited := strings.Replace(apiserverManifest, "- kube-apiserver\n", "- kube-apiserver\n    - --v=2\n", 1)
	backup, err := f.sys.WriteManifest(p, []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if host.InsideDir(filepath.Join(f.kdir, "manifests"), backup) {
		t.Errorf("백업이 manifests 디렉터리 안에 있으면 kubelet이 Pod로 띄웁니다: %s", backup)
	}
	if b, _ := os.ReadFile(backup); string(b) != apiserverManifest {
		t.Error("백업 내용이 원본과 다름")
	}
	entries, _ := os.ReadDir(filepath.Join(f.kdir, "manifests"))
	if len(entries) != 2 {
		t.Errorf("manifests 디렉터리에 다른 파일이 생김: %v", entries)
	}
	if _, err := f.sys.WriteManifest(filepath.Join(f.root, "elsewhere.yaml"), []byte(edited)); err == nil {
		t.Error("manifests 밖 경로를 허용함")
	}
	if _, err := f.sys.WriteManifest(p, []byte("kind: ConfigMap\n")); err == nil {
		t.Error("Pod가 아닌 manifest를 허용함")
	}
	// 백업 위치를 manifests 안으로 잘못 설정하면 거부합니다.
	f.sys.cfg.Backup.Dir = f.kdir
	if _, err := f.sys.WriteManifest(p, []byte(edited)); err == nil {
		t.Error("manifests 안의 백업 위치를 허용함")
	}
}

func TestBackupViaEtcdContainer(t *testing.T) {
	f := newFixture(t, true)
	tmpSnap := filepath.Join(f.etcdDir, snapshotTmp)
	f.run.reply = func(line string, args []string) ([]byte, error) {
		switch {
		case strings.Contains(line, "ps --name ^etcd$"):
			return []byte("abc123\n"), nil
		case strings.Contains(line, "exec abc123 etcdctl"):
			// 컨테이너 안 etcdctl이 hostPath(etcd 데이터 디렉터리)에 스냅샷을 쓴 상황을 흉내 냅니다.
			if args[len(args)-1] != tmpSnap {
				t.Errorf("스냅샷 경로 = %s, want %s", args[len(args)-1], tmpSnap)
			}
			return nil, os.WriteFile(tmpSnap, []byte("snapshot-data"), 0o600)
		}
		return nil, nil
	}
	var paths []string
	for i := 0; i < 3; i++ {
		ts := time.Date(2026, 10, 5, 12, 0, i, 0, time.Local)
		f.sys.now = func() time.Time { return ts }
		p, err := f.sys.Backup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, ts, ts)
		paths = append(paths, p)
	}
	if !f.run.has("--cacert=" + filepath.Join(f.kdir, "pki", "etcd", "ca.crt")) {
		t.Errorf("etcd 인증서 인자 누락: %v", f.run.calls)
	}
	if _, err := os.Stat(tmpSnap); !os.IsNotExist(err) {
		t.Error("etcd 데이터 디렉터리에 임시 스냅샷이 남음")
	}
	list, err := f.sys.ListBackups()
	if err != nil || len(list) != 2 {
		t.Fatalf("보관 개수 정리: %v %v", list, err)
	}
	if !list[0].HasExtra || list[0].Kind != host.DatastoreEtcd {
		t.Errorf("PKI 압축본 표시: %+v", list[0])
	}
	if _, err := os.Stat(paths[0] + pkiSuffix); !os.IsNotExist(err) {
		t.Error("지운 백업의 PKI 압축본이 남음")
	}
	// PKI 압축본 안에 pki/etcd/ca.crt가 들어 있어야 합니다.
	names := tarNames(t, list[0].Path+pkiSuffix)
	if !names["pki/etcd/ca.crt"] || !names["pki/ca.crt"] {
		t.Errorf("PKI 압축 내용: %v", names)
	}
	if g := f.sys.RestoreGuide(list[0].Name); !strings.Contains(g, "etcdutl snapshot restore "+list[0].Path) {
		t.Errorf("복원 안내에 스냅샷 경로가 없음:\n%s", g)
	}
	if f.sys.RestoreBackup(context.Background(), list[0].Name, nil) != host.ErrUnsupported {
		t.Error("자동 복원은 지원하지 않아야 함")
	}
}

func tarNames(t *testing.T, p string) map[string]bool {
	t.Helper()
	fh, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = true
	}
}

func TestRotateCertificatesRestartsControlPlane(t *testing.T) {
	f := newFixture(t, true)
	f.run.reply = func(line string, _ []string) ([]byte, error) {
		if strings.Contains(line, " ps --name ^") {
			return []byte("id-" + strings.Split(strings.Split(line, "^")[1], "$")[0] + "\n"), nil
		}
		return nil, nil
	}
	if err := f.sys.RotateCertificates(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.run.calls[0], "kubeadm certs renew all") {
		t.Errorf("첫 호출은 인증서 갱신이어야 함: %v", f.run.calls)
	}
	for _, c := range controlPlaneComponents {
		if !f.run.has("stop id-" + c) {
			t.Errorf("%s 재시작 누락: %v", c, f.run.calls)
		}
	}
	w := newFixture(t, false)
	if err := w.sys.RotateCertificates(context.Background(), nil); err == nil {
		t.Error("워커 노드에서 인증서 갱신을 허용함")
	}
}

func TestJoinCommand(t *testing.T) {
	f := newFixture(t, true)
	f.run.reply = func(string, []string) ([]byte, error) {
		return []byte("kubeadm join 10.0.0.1:6443 --token abc.def --discovery-token-ca-cert-hash sha256:00\n"), nil
	}
	cmd, err := f.sys.JoinCommand(context.Background(), "")
	if err != nil || !strings.HasPrefix(cmd, "kubeadm join") {
		t.Fatalf("join: %q %v", cmd, err)
	}
	if !f.run.has("token create --print-join-command --kubeconfig " + filepath.Join(f.kdir, "admin.conf")) {
		t.Errorf("호출: %v", f.run.calls)
	}
	f.sys.root = false
	if _, err := f.sys.JoinCommand(context.Background(), ""); err != host.ErrNotRoot {
		t.Errorf("root 아님: %v", err)
	}
}

func certPEM(t *testing.T, cn string, notAfter time.Time) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCertificatesIncludeKubeconfigClientCerts(t *testing.T) {
	f := newFixture(t, true)
	now := time.Now()
	write(t, filepath.Join(f.kdir, "pki", "apiserver.crt"), string(certPEM(t, "kube-apiserver", now.Add(300*24*time.Hour))))
	admin := certPEM(t, "kubernetes-admin", now.Add(20*24*time.Hour))
	write(t, filepath.Join(f.kdir, "admin.conf"), "apiVersion: v1\nkind: Config\nusers:\n- name: kubernetes-admin\n  user:\n    client-certificate-data: "+
		base64.StdEncoding.EncodeToString(admin)+"\n    client-key-data: eA==\n")
	certs, err := f.sys.Certificates()
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 2 || certs[0].Subject != "kubernetes-admin" || certs[0].Label != "admin.conf (client)" {
		t.Fatalf("인증서 목록(만료 순): %+v", certs)
	}
	desc, err := host.DescribeCertFile(filepath.Join(f.kdir, "admin.conf"))
	if err != nil || !strings.Contains(desc, "CN=kubernetes-admin") {
		t.Errorf("kubeconfig 인증서 상세: %v\n%s", err, desc)
	}
}

func TestServiceAndVersion(t *testing.T) {
	f := newFixture(t, true)
	f.run.reply = func(line string, _ []string) ([]byte, error) {
		switch {
		case strings.HasPrefix(line, "kubeadm version"):
			return []byte("v1.31.0\n"), nil
		case strings.HasPrefix(line, "kubelet --version"):
			return []byte("Kubernetes v1.31.0\n"), nil
		}
		return nil, nil
	}
	v, err := f.sys.Version(context.Background())
	if err != nil || v != "kubeadm v1.31.0 · Kubernetes v1.31.0" {
		t.Errorf("버전: %q %v", v, err)
	}
	if err := f.sys.ServiceControl(context.Background(), host.OpRestart); err != nil {
		t.Fatal(err)
	}
	if !f.run.has("systemctl restart kubelet") {
		t.Errorf("kubelet 재시작 호출: %v", f.run.calls)
	}
}
