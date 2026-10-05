//go:build e2e

// 실제 K3S 클러스터를 대상으로 하는 통합 테스트입니다.
//
//	cd src && go test -tags e2e ./internal/kube/ -run E2E -v
//
// 환경변수: K3STUI_E2E_KUBECONFIG (기본 /etc/rancher/k3s/k3s.yaml),
//
//	K3STUI_E2E_IMAGE (기본 docker.io/traefik/whoami:v1.11, 80 포트로 HTTP 응답하는 이미지)
//
// 전용 네임스페이스(k3stui-e2e-<시각>)를 만들고 테스트가 끝나면 지웁니다.
package kube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("시간 초과: %s", what)
}

func TestE2EWorkloadLifecycle(t *testing.T) {
	ctx := context.Background()
	c, err := New(envOr("K3STUI_E2E_KUBECONFIG", "/etc/rancher/k3s/k3s.yaml"))
	if err != nil {
		t.Skipf("클러스터에 연결할 수 없음: %v", err)
	}
	ns := fmt.Sprintf("k3stui-e2e-%d", time.Now().Unix())
	if _, err := c.Core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Core.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	one := int32(1)
	labels := map[string]string{"app": "e2e"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "web", Image: envOr("K3STUI_E2E_IMAGE", "docker.io/traefik/whoami:v1.11"),
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports:           []corev1.ContainerPort{{ContainerPort: 80, Name: "http"}},
				}}},
			},
		},
	}
	if _, err := c.Core.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Informer 캐시가 변경을 반영하는지 확인합니다.
	store := NewStore(c.Dynamic)
	defer store.Stop()
	readyReplicas := func() int64 {
		u := store.Get(GVRDeployments, ns, "web")
		if u == nil {
			return -1
		}
		d := To[appsv1.Deployment](u)
		return int64(d.Status.ReadyReplicas)
	}
	waitFor(t, 90*time.Second, "Deployment 1개 준비", func() bool { return readyReplicas() == 1 })

	def := Def("deployments")
	if err := c.Scale(ctx, def, ns, "web", 2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 90*time.Second, "스케일 2 반영", func() bool { return readyReplicas() == 2 })

	if err := c.RolloutRestart(ctx, def, ns, "web"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "restartedAt 주석", func() bool {
		u := store.Get(GVRDeployments, ns, "web")
		return u != nil && To[appsv1.Deployment](u).Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != ""
	})
	waitFor(t, 120*time.Second, "재시작 완료", func() bool {
		d, err := c.Core.AppsV1().Deployments(ns).Get(ctx, "web", metav1.GetOptions{})
		return err == nil && d.Status.UpdatedReplicas == 2 && d.Status.ReadyReplicas == 2 && d.Status.Replicas == 2
	})

	// 로그 스트림
	pods, err := c.PodsForSelector(ctx, ns, labels)
	if err != nil || len(pods) == 0 {
		t.Fatalf("Pod 조회: %v %d", err, len(pods))
	}

	// port-forward로 실제 HTTP 응답 확인
	pf := NewPortForwarder(c, "127.0.0.1", nil)
	defer pf.StopAll()
	f, err := pf.Start("pod/"+pods[0].Name, ns, pods[0].Name, 0, 80)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", f.Local))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body) == 0 {
		t.Errorf("port-forward 응답: %d %q", resp.StatusCode, body)
	}
	if err := pf.Stop(f.ID); err != nil {
		t.Error(err)
	}

	logCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.StreamLogs(logCtx, ns, pods[0].Name, LogOptions{TailLines: 10}); err != nil {
		t.Errorf("로그 스트림: %v", err)
	}

	if err := c.Delete(ctx, def, ns, "web"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "삭제 반영", func() bool { return store.Get(GVRDeployments, ns, "web") == nil })
}
