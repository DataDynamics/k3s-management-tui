package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func toU(t *testing.T, obj any) *unstructured.Unstructured {
	t.Helper()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func TestPodStatus(t *testing.T) {
	ready := corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue}
	cases := []struct {
		name  string
		pod   corev1.Pod
		want  string
		level Level
	}{
		{"running", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{ready},
			ContainerStatuses: []corev1.ContainerStatus{{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}},
			"Running", LevelOK},
		{"crashloop", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}}},
			"CrashLoopBackOff", LevelErr},
		{"completed", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed"}}}}}},
			"Completed", LevelMuted},
		{"init", corev1.Pod{Spec: corev1.PodSpec{InitContainers: []corev1.Container{{Name: "a"}, {Name: "b"}}},
			Status: corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}},
			"Init:1/2", LevelWarn},
		{"terminating", corev1.Pod{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &metav1.Time{Time: time.Now()}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}}, "Terminating", LevelWarn},
		{"not-ready", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}},
			"Running", LevelWarn},
	}
	for _, c := range cases {
		got, level := PodStatus(&c.pod)
		if got != c.want || level != c.level {
			t.Errorf("%s: got (%s,%d) want (%s,%d)", c.name, got, level, c.want, c.level)
		}
	}
}

func TestRowsMatchColumns(t *testing.T) {
	now := time.Now()
	rc := RowCtx{Now: now, Metrics: NewMetrics()}
	objs := map[string]any{
		"pods":        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", CreationTimestamp: metav1.Time{Time: now.Add(-time.Hour)}}},
		"deployments": &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
		"services":    &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "s"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Ports: []corev1.ServicePort{{Port: 80, NodePort: 30080, Protocol: "TCP"}}}},
		"nodes":       &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{"node-role.kubernetes.io/control-plane": "true"}}},
		"events":      &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Type: "Warning", Count: 3, Message: "a\nb"},
	}
	for _, d := range Defs() {
		obj, ok := objs[d.Key]
		var u *unstructured.Unstructured
		if ok {
			u = toU(t, obj)
		} else {
			u = &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "x"}}}
		}
		cells, _ := d.Row(u, rc)
		if len(cells) != len(d.Columns) {
			t.Errorf("%s: 셀 %d개, 컬럼 %d개", d.Key, len(cells), len(d.Columns))
		}
	}
	svc, _ := Def("svc").Row(toU(t, objs["services"]), rc)
	if svc[3] != "<pending>" || svc[4] != "80:30080/TCP" {
		t.Errorf("service row: %v", svc)
	}
	node, _ := Def("no").Row(toU(t, objs["nodes"]), rc)
	if node[2] != "control-plane" {
		t.Errorf("node roles: %v", node)
	}
	ev, level := Def("events").Row(toU(t, objs["events"]), rc)
	if level != LevelWarn || strings.Contains(ev[4], "\n") || !strings.HasSuffix(ev[4], "(x3)") {
		t.Errorf("event row: %v", ev)
	}
}

func TestDefAliases(t *testing.T) {
	for alias, key := range map[string]string{"po": "pods", "deploy": "deployments", "svc": "services", "pvc": "persistentvolumeclaims", "NS": "namespaces"} {
		if d := Def(alias); d == nil || d.Key != key {
			t.Errorf("Def(%q) = %v", alias, d)
		}
	}
	if Def("nope") != nil {
		t.Error("없는 리소스")
	}
}

func TestFormatting(t *testing.T) {
	if FormatBytes(512) != "512B" || FormatBytes(1536) != "1.5Ki" || FormatBytes(5*1024*1024*1024) != "5.0Gi" {
		t.Errorf("FormatBytes: %s %s %s", FormatBytes(512), FormatBytes(1536), FormatBytes(5*1024*1024*1024))
	}
	if FormatCPU(250) != "250m" || FormatCPU(1500) != "1.5" {
		t.Error("FormatCPU")
	}
	for d, want := range map[time.Duration]string{30 * time.Second: "30s", 5*time.Minute + 3*time.Second: "5m3s",
		90 * time.Minute: "90m", 5 * time.Hour: "5h", 50 * time.Hour: "2d2h", 400 * 24 * time.Hour: "400d"} {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestParsePorts(t *testing.T) {
	if l, r, err := ParsePorts("8080:80"); err != nil || l != 8080 || r != 80 {
		t.Error("8080:80")
	}
	if l, r, err := ParsePorts("443"); err != nil || l != 443 || r != 443 {
		t.Error("443")
	}
	for _, bad := range []string{"", "a:1", "1:0", "70000:1", "1:99999"} {
		if _, _, err := ParsePorts(bad); err == nil {
			t.Errorf("%q를 허용함", bad)
		}
	}
}

func TestYAMLMasksSecret(t *testing.T) {
	sec := toU(t, &corev1.Secret{TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "s", Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{\"data\":{\"pw\":\"aHVudGVyMg==\"}}"}},
		Data:       map[string][]byte{"pw": []byte("hunter2")}})
	masked, err := YAML(sec, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(masked, "aHVudGVyMg") || !strings.Contains(masked, "********") {
		t.Errorf("Secret 값이 가려지지 않음:\n%s", masked)
	}
	if got := DecodeSecret(sec); !strings.Contains(got, "hunter2") {
		t.Errorf("디코드 실패: %s", got)
	}
	// 원본 캐시 객체는 바뀌면 안 됩니다.
	if d, _, _ := unstructured.NestedString(sec.Object, "data", "pw"); d != "aHVudGVyMg==" {
		t.Error("YAML()이 원본 객체를 수정함")
	}
}

func TestDrainEvictsNonDaemonSetPods(t *testing.T) {
	isCtl := true
	pod := func(name, ownerKind string) *corev1.Pod {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: corev1.PodSpec{NodeName: "n1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
		if ownerKind != "" {
			p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: "o", Controller: &isCtl}}
		}
		return p
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	cs := fake.NewClientset(node, pod("web", "ReplicaSet"), pod("agent", "DaemonSet"))
	c := &Client{Core: cs}
	if err := c.Drain(context.Background(), "n1", false, nil); err != nil {
		t.Fatal(err)
	}
	n, _ := cs.CoreV1().Nodes().Get(context.Background(), "n1", metav1.GetOptions{})
	if !n.Spec.Unschedulable {
		t.Error("cordon되지 않음")
	}
	var evicted []string
	for _, a := range cs.Actions() {
		if a.GetSubresource() == "eviction" {
			evicted = append(evicted, a.(k8stesting.CreateAction).GetObject().(metav1.Object).GetName())
		}
	}
	if strings.Join(evicted, ",") != "web" {
		t.Errorf("축출 대상 = %v, want [web] (DaemonSet 제외)", evicted)
	}

	// 컨트롤러 없는 Pod가 있으면 force 없이는 중단
	cs2 := fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}, pod("bare", ""))
	if err := (&Client{Core: cs2}).Drain(context.Background(), "n1", false, nil); err == nil {
		t.Error("컨트롤러 없는 Pod가 있으면 중단해야 함")
	}
}

func TestTriggerCronJob(t *testing.T) {
	cs := fake.NewClientset()
	c := &Client{Core: cs}
	_, err := c.TriggerCronJob(context.Background(), "default", "missing")
	if err == nil {
		t.Error("없는 CronJob이면 오류여야 함")
	}
}

func TestStoreNilSafe(t *testing.T) {
	var s *Store
	if s.List(GVRPods, "") != nil || s.Get(GVRPods, "a", "b") != nil || s.Synced(GVRPods) {
		t.Error("nil Store는 빈 결과를 돌려야 함")
	}
	s.Ensure(GVRPods)
	s.Stop()
	var m *Metrics
	if _, ok := m.Pod("a", "b"); ok {
		t.Error("nil Metrics")
	}
}
