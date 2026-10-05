package kubeadm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/DataDynamics/k3s-management-tui/internal/host"
)

const (
	backupPrefix = "kubeadm-etcd-"
	pkiSuffix    = ".pki.tar.gz"
	snapshotTmp  = ".k3stui-snapshot.db"
)

// podManifest는 static Pod manifest에서 필요한 부분입니다.
type podManifest struct {
	Spec struct {
		Containers []struct {
			Name    string   `json:"name"`
			Command []string `json:"command"`
		} `json:"containers"`
	} `json:"spec"`
}

// manifestFlag는 static Pod manifest의 첫 컨테이너 command에서 --name=값을 찾습니다.
func manifestFlag(path, name string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var m podManifest
	if yaml.Unmarshal(data, &m) != nil || len(m.Spec.Containers) == 0 {
		return "", false
	}
	for _, c := range m.Spec.Containers[0].Command {
		if v, ok := strings.CutPrefix(c, "--"+name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// Datastore는 로컬 etcd static Pod(etcd.yaml)가 있으면 etcd, 없으면 kube-apiserver의 --etcd-servers로 외부 etcd를 판별합니다.
func (s *System) Datastore() host.DatastoreInfo {
	etcdManifest := filepath.Join(s.ManifestsDir(), "etcd.yaml")
	if _, err := os.Stat(etcdManifest); err == nil {
		dir, ok := manifestFlag(etcdManifest, "data-dir")
		if !ok {
			dir = "/var/lib/etcd"
		}
		return host.DatastoreInfo{Kind: host.DatastoreEtcd, Path: dir, Size: host.DirSize(filepath.Join(dir, "member"))}
	}
	if ep, ok := manifestFlag(filepath.Join(s.ManifestsDir(), "kube-apiserver.yaml"), "etcd-servers"); ok {
		return host.DatastoreInfo{Kind: host.DatastoreExternal, Endpoint: ep}
	}
	return host.DatastoreInfo{Kind: host.DatastoreUnknown}
}

// etcdctlArgs는 로컬 etcd에 붙는 etcdctl 공통 인자입니다.
// kubeadm이 만드는 healthcheck-client 인증서를 쓰고, 없으면 apiserver-etcd-client를 씁니다.
func (s *System) etcdctlArgs() []string {
	cert, key := s.pki("etcd/healthcheck-client.crt"), s.pki("etcd/healthcheck-client.key")
	if _, err := os.Stat(cert); err != nil {
		cert, key = s.pki("apiserver-etcd-client.crt"), s.pki("apiserver-etcd-client.key")
	}
	return []string{"--endpoints=https://127.0.0.1:2379", "--cacert=" + s.pki("etcd/ca.crt"), "--cert=" + cert, "--key=" + key}
}

// etcdContainer는 실행 중인 etcd 컨테이너 ID입니다.
func (s *System) etcdContainer(ctx context.Context) (string, error) {
	out, err := s.crictl(ctx, "ps", "--name", "^etcd$", "--state", "running", "-q")
	if err != nil {
		return "", err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", fmt.Errorf("실행 중인 etcd 컨테이너가 없습니다")
	}
	return ids[0], nil
}

// Backup은 etcd 스냅샷과 PKI 압축본을 만들고 보관 개수를 넘는 오래된 백업을 지웁니다.
// 호스트에 etcdctl이 있으면 그것을, 없으면 etcd 컨테이너 안의 etcdctl을 씁니다.
// 컨테이너 안에서 만든 스냅샷은 etcd 데이터 디렉터리(hostPath)에 잠시 두었다가 백업 위치로 옮깁니다.
func (s *System) Backup(ctx context.Context) (string, error) {
	if err := s.needRoot(); err != nil {
		return "", err
	}
	ds := s.Datastore()
	if ds.Kind != host.DatastoreEtcd {
		if ds.Kind == host.DatastoreExternal {
			return "", fmt.Errorf("외부 etcd(%s)는 해당 etcd 클러스터에서 백업하세요", ds.Endpoint)
		}
		return "", fmt.Errorf("이 노드에 로컬 etcd가 없습니다 (워커 노드이거나 manifests 경로가 다릅니다)")
	}
	dir := s.cfg.Backup.Dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	hostname, _ := os.Hostname()
	name := backupPrefix + hostname + "-" + s.now().Format("20060102-150405") + ".db"
	dst := filepath.Join(dir, name)

	if p, err := exec.LookPath(s.kc.Etcdctl); err == nil {
		args := append(s.etcdctlArgs(), "snapshot", "save", dst)
		if _, err := s.run.Run(ctx, p, args...); err != nil {
			os.Remove(dst)
			return "", fmt.Errorf("etcd 스냅샷 실패: %w", err)
		}
	} else {
		id, err := s.etcdContainer(ctx)
		if err != nil {
			return "", err
		}
		tmp := filepath.Join(ds.Path, snapshotTmp)
		os.Remove(tmp)
		args := append([]string{"exec", id, "etcdctl"}, s.etcdctlArgs()...)
		args = append(args, "snapshot", "save", tmp)
		if _, err := s.crictl(ctx, args...); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("etcd 컨테이너에서 스냅샷 실패: %w", err)
		}
		if err := host.MoveFile(tmp, dst, 0o600); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("스냅샷 이동 실패: %w", err)
		}
	}
	if st, err := os.Stat(dst); err != nil || st.Size() == 0 {
		os.Remove(dst)
		return "", fmt.Errorf("스냅샷 파일이 비어 있습니다")
	}
	_ = os.Chmod(dst, 0o600)
	if err := TarGz(s.pki(""), dst+pkiSuffix); err != nil {
		return dst, fmt.Errorf("스냅샷은 저장했지만 PKI 압축 실패: %w", err)
	}
	if err := s.prune(ctx); err != nil {
		return dst, fmt.Errorf("백업은 성공했지만 오래된 백업 정리 실패: %w", err)
	}
	return dst, nil
}

// TarGz는 디렉터리를 tar.gz로 묶습니다 (0600).
func TarGz(src, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	base := filepath.Dir(filepath.Clean(src))
	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil && walkErr == nil {
			walkErr = err
		}
	}
	if walkErr != nil {
		os.Remove(dst)
	}
	return walkErr
}

// ListBackups는 백업 디렉터리의 kubeadm etcd 스냅샷입니다.
func (s *System) ListBackups() ([]host.BackupFile, error) {
	return host.ListBackupFiles(s.cfg.Backup.Dir, host.DatastoreEtcd, backupPrefix, ".db", pkiSuffix)
}

func (s *System) prune(ctx context.Context) error {
	list, err := s.ListBackups()
	if err != nil {
		return err
	}
	for i, b := range list {
		if i >= s.cfg.Backup.Keep {
			if err := s.DeleteBackup(ctx, b.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteBackup은 스냅샷과 PKI 압축본을 지웁니다.
func (s *System) DeleteBackup(_ context.Context, name string) error {
	if err := s.needRoot(); err != nil {
		return err
	}
	if err := host.ValidBackupName(name); err != nil {
		return err
	}
	p := filepath.Join(s.cfg.Backup.Dir, name)
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + pkiSuffix)
	return nil
}

// RestoreBackup은 자동으로 하지 않습니다. etcd 복원은 static Pod 수정과 데이터 디렉터리 교체가 필요해 RestoreGuide로 안내합니다.
func (s *System) RestoreBackup(context.Context, string, func(string)) error {
	return host.ErrUnsupported
}

// RestoreGuide는 kubeadm 로컬 etcd 복원 절차입니다.
func (s *System) RestoreGuide(name string) string {
	snap := filepath.Join(s.cfg.Backup.Dir, name)
	dataDir := s.Datastore().Path
	if dataDir == "" {
		dataDir = "/var/lib/etcd"
	}
	restored := dataDir + "-restored"
	return "kubeadm 로컬 etcd 스냅샷 복원 절차 (컨트롤 플레인이 여러 대면 모든 노드에서 etcd를 멈춘 뒤 진행합니다)\n\n" +
		"0. 스냅샷 확인:\n" +
		"   etcdutl snapshot status " + snap + " -w table\n\n" +
		"1. 컨트롤 플레인 static Pod를 멈춥니다 (manifest를 잠시 다른 곳으로 옮기면 kubelet이 Pod를 내립니다):\n" +
		"   mkdir -p /root/manifests-hold && mv " + s.ManifestsDir() + "/*.yaml /root/manifests-hold/\n\n" +
		"2. 새 데이터 디렉터리로 스냅샷을 복원합니다 (etcdutl은 etcd 릴리스에 들어 있습니다):\n" +
		"   etcdutl snapshot restore " + snap + " --data-dir " + restored + "\n\n" +
		"3. 기존 데이터를 보관하고 복원본으로 바꿉니다:\n" +
		"   mv " + dataDir + " " + dataDir + ".bak && mv " + restored + " " + dataDir + "\n\n" +
		"4. manifest를 되돌려 컨트롤 플레인을 다시 띄웁니다:\n" +
		"   mv /root/manifests-hold/*.yaml " + s.ManifestsDir() + "/\n\n" +
		"5. 확인:  kubectl get nodes,pods -A\n\n" +
		"PKI가 바뀌었다면 함께 저장된 " + name + pkiSuffix + " 를 " + s.pki("") + " 에 풀어 맞춥니다.\n" +
		"자세한 내용: https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#restoring-an-etcd-cluster\n"
}
