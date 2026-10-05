package kube

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// To는 unstructured 객체를 타입 있는 객체로 변환합니다.
func To[T any](u *unstructured.Unstructured) *T {
	var out T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &out); err != nil {
		return &out
	}
	return &out
}

func age(u *unstructured.Unstructured, c RowCtx) string {
	return Age(u.GetCreationTimestamp().Time, c.Now)
}

func nameAgeRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	return []string{u.GetName(), age(u, c)}, LevelNone
}

// ---- Workloads ----

// PodStatus는 kubectl get pods의 STATUS 컬럼과 같은 규칙으로 상태를 계산합니다.
func PodStatus(p *corev1.Pod) (string, Level) {
	reason := string(p.Status.Phase)
	if p.Status.Reason != "" {
		reason = p.Status.Reason
	}
	level := LevelOK
	initializing := false
	for i, cs := range p.Status.InitContainerStatuses {
		switch {
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0:
			continue
		case cs.State.Terminated != nil:
			if cs.State.Terminated.Reason != "" {
				reason = "Init:" + cs.State.Terminated.Reason
			} else {
				reason = fmt.Sprintf("Init:ExitCode:%d", cs.State.Terminated.ExitCode)
			}
			initializing = true
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + cs.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(p.Spec.InitContainers))
			initializing = true
		}
		break
	}
	if !initializing {
		hasRunning := false
		for i := len(p.Status.ContainerStatuses) - 1; i >= 0; i-- {
			cs := p.Status.ContainerStatuses[i]
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				reason = cs.State.Waiting.Reason
			case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
				reason = cs.State.Terminated.Reason
			case cs.State.Terminated != nil:
				if cs.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", cs.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", cs.State.Terminated.ExitCode)
				}
			case cs.Ready && cs.State.Running != nil:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "Running"
		}
	}
	if p.DeletionTimestamp != nil {
		if p.Status.Reason == "NodeLost" {
			reason = "Unknown"
		} else {
			reason = "Terminating"
		}
	}
	switch reason {
	case "Running":
		if !podReady(p) {
			level = LevelWarn
		}
	case "Succeeded", "Completed":
		level = LevelMuted
	case "Pending", "ContainerCreating", "PodInitializing", "Terminating":
		level = LevelWarn
	default:
		if strings.HasPrefix(reason, "Init:") && strings.Contains(reason, "/") {
			level = LevelWarn
		} else {
			level = LevelErr
		}
	}
	return reason, level
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// PodRestarts는 컨테이너 재시작 합계와 마지막 재시작 시각을 돌려줍니다.
func PodRestarts(p *corev1.Pod) (int32, time.Time) {
	var n int32
	var last time.Time
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
		if t := cs.LastTerminationState.Terminated; t != nil && t.FinishedAt.After(last) {
			last = t.FinishedAt.Time
		}
	}
	return n, last
}

func podRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	p := To[corev1.Pod](u)
	ready := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	status, level := PodStatus(p)
	restarts, last := PodRestarts(p)
	rs := strconv.Itoa(int(restarts))
	if !last.IsZero() {
		rs += " (" + Age(last, c.Now) + " ago)"
	}
	cpu, mem := "-", "-"
	if m, ok := c.Metrics.Pod(p.Namespace, p.Name); ok {
		cpu, mem = FormatCPU(m.CPUMilli), FormatBytes(m.MemBytes)
	}
	return []string{
		p.Name, fmt.Sprintf("%d/%d", ready, len(p.Spec.Containers)), status, rs,
		cpu, mem, orNone(p.Status.PodIP), orNone(p.Spec.NodeName), age(u, c),
	}, level
}

func images(cs []corev1.Container) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Image
	}
	return strings.Join(out, ",")
}

func readyLevel(ready, want int32) Level {
	switch {
	case want == 0:
		return LevelMuted
	case ready >= want:
		return LevelOK
	case ready == 0:
		return LevelErr
	default:
		return LevelWarn
	}
}

func replicas(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

func deploymentRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	d := To[appsv1.Deployment](u)
	want := replicas(d.Spec.Replicas)
	return []string{
		d.Name, fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, want),
		strconv.Itoa(int(d.Status.UpdatedReplicas)), strconv.Itoa(int(d.Status.AvailableReplicas)),
		images(d.Spec.Template.Spec.Containers), age(u, c),
	}, readyLevel(d.Status.ReadyReplicas, want)
}

func statefulSetRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	s := To[appsv1.StatefulSet](u)
	want := replicas(s.Spec.Replicas)
	return []string{
		s.Name, fmt.Sprintf("%d/%d", s.Status.ReadyReplicas, want),
		images(s.Spec.Template.Spec.Containers), age(u, c),
	}, readyLevel(s.Status.ReadyReplicas, want)
}

func daemonSetRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	d := To[appsv1.DaemonSet](u)
	st := d.Status
	return []string{
		d.Name, itoa(st.DesiredNumberScheduled), itoa(st.CurrentNumberScheduled), itoa(st.NumberReady),
		itoa(st.UpdatedNumberScheduled), itoa(st.NumberAvailable), age(u, c),
	}, readyLevel(st.NumberReady, st.DesiredNumberScheduled)
}

func replicaSetRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	r := To[appsv1.ReplicaSet](u)
	want := replicas(r.Spec.Replicas)
	return []string{
		r.Name, itoa(want), itoa(r.Status.Replicas), itoa(r.Status.ReadyReplicas), age(u, c),
	}, readyLevel(r.Status.ReadyReplicas, want)
}

func itoa(i int32) string { return strconv.Itoa(int(i)) }

func jobRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	j := To[batchv1.Job](u)
	completions := int32(1)
	if j.Spec.Completions != nil {
		completions = *j.Spec.Completions
	}
	status, level := "Running", LevelWarn
	for _, cond := range j.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			status, level = "Complete", LevelMuted
		case batchv1.JobFailed:
			status, level = "Failed", LevelErr
		case batchv1.JobSuspended:
			status, level = "Suspended", LevelMuted
		}
	}
	dur := ""
	if j.Status.StartTime != nil {
		end := c.Now
		if j.Status.CompletionTime != nil {
			end = j.Status.CompletionTime.Time
		}
		dur = HumanDuration(end.Sub(j.Status.StartTime.Time))
	}
	return []string{
		j.Name, status, fmt.Sprintf("%d/%d", j.Status.Succeeded, completions), dur, age(u, c),
	}, level
}

func cronJobRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	cj := To[batchv1.CronJob](u)
	suspend := cj.Spec.Suspend != nil && *cj.Spec.Suspend
	last := "<none>"
	if cj.Status.LastScheduleTime != nil {
		last = Age(cj.Status.LastScheduleTime.Time, c.Now)
	}
	level := LevelNone
	if suspend {
		level = LevelMuted
	}
	return []string{
		cj.Name, cj.Spec.Schedule, strconv.FormatBool(suspend), strconv.Itoa(len(cj.Status.Active)), last, age(u, c),
	}, level
}

// ---- Network ----

// ServicePorts는 "80/TCP,443:30443/TCP" 형식으로 포트를 표시합니다.
func ServicePorts(s *corev1.Service) string {
	var parts []string
	for _, p := range s.Spec.Ports {
		v := strconv.Itoa(int(p.Port))
		if p.NodePort != 0 {
			v += ":" + strconv.Itoa(int(p.NodePort))
		}
		parts = append(parts, v+"/"+string(p.Protocol))
	}
	return orNone(strings.Join(parts, ","))
}

func serviceRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	s := To[corev1.Service](u)
	ext := strings.Join(s.Spec.ExternalIPs, ",")
	for _, ing := range s.Status.LoadBalancer.Ingress {
		if ext != "" {
			ext += ","
		}
		ext += ing.IP + ing.Hostname
	}
	if ext == "" && s.Spec.Type == corev1.ServiceTypeLoadBalancer {
		ext = "<pending>"
	}
	if s.Spec.Type == corev1.ServiceTypeExternalName {
		ext = s.Spec.ExternalName
	}
	level := LevelNone
	if ext == "<pending>" {
		level = LevelWarn
	}
	return []string{s.Name, string(s.Spec.Type), orNone(s.Spec.ClusterIP), orNone(ext), ServicePorts(s), age(u, c)}, level
}

func ingressRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	ing := To[networkingv1.Ingress](u)
	class := "<none>"
	if ing.Spec.IngressClassName != nil {
		class = *ing.Spec.IngressClassName
	}
	var hosts []string
	for _, r := range ing.Spec.Rules {
		h := r.Host
		if h == "" {
			h = "*"
		}
		hosts = append(hosts, h)
	}
	var addrs []string
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		addrs = append(addrs, lb.IP+lb.Hostname)
	}
	ports := "80"
	if len(ing.Spec.TLS) > 0 {
		ports = "80, 443"
	}
	return []string{ing.Name, class, orNone(strings.Join(hosts, ",")), orNone(strings.Join(addrs, ",")), ports, age(u, c)}, LevelNone
}

func endpointSliceRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	es := To[discoveryv1.EndpointSlice](u)
	var ports []string
	for _, p := range es.Ports {
		if p.Port != nil {
			ports = append(ports, strconv.Itoa(int(*p.Port)))
		}
	}
	var addrs []string
	ready := 0
	for _, e := range es.Endpoints {
		addrs = append(addrs, e.Addresses...)
		if e.Conditions.Ready == nil || *e.Conditions.Ready {
			ready++
		}
	}
	level := LevelNone
	if len(es.Endpoints) == 0 {
		level = LevelWarn
	}
	return []string{es.Name, string(es.AddressType), orNone(strings.Join(ports, ",")), orNone(strings.Join(addrs, ",")), age(u, c)}, level
}

func netpolRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	np := To[networkingv1.NetworkPolicy](u)
	sel := metav1.FormatLabelSelector(&np.Spec.PodSelector)
	return []string{np.Name, sel, age(u, c)}, LevelNone
}

func ciliumPolicyRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	valid := "?"
	level := LevelNone
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, ci := range conds {
		m, _ := ci.(map[string]any)
		if m["type"] == "Valid" {
			valid, _ = m["status"].(string)
			if valid != "True" {
				level = LevelErr
			}
		}
	}
	return []string{u.GetName(), valid, age(u, c)}, level
}

// ---- Storage ----

func accessModes(m []corev1.PersistentVolumeAccessMode) string {
	short := map[corev1.PersistentVolumeAccessMode]string{
		corev1.ReadWriteOnce: "RWO", corev1.ReadOnlyMany: "ROX", corev1.ReadWriteMany: "RWX", corev1.ReadWriteOncePod: "RWOP",
	}
	out := make([]string, 0, len(m))
	for _, a := range m {
		out = append(out, short[a])
	}
	return strings.Join(out, ",")
}

// PVPath는 PV의 호스트 경로(local/hostPath)를 돌려줍니다.
func PVPath(pv *corev1.PersistentVolume) string {
	switch {
	case pv.Spec.Local != nil:
		return pv.Spec.Local.Path
	case pv.Spec.HostPath != nil:
		return pv.Spec.HostPath.Path
	case pv.Spec.CSI != nil:
		return "csi:" + pv.Spec.CSI.VolumeHandle
	}
	return ""
}

func pvRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	pv := To[corev1.PersistentVolume](u)
	capacity := pv.Spec.Capacity[corev1.ResourceStorage]
	claim := ""
	if pv.Spec.ClaimRef != nil {
		claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
	}
	level := LevelNone
	switch pv.Status.Phase {
	case corev1.VolumeBound:
		level = LevelOK
	case corev1.VolumeFailed:
		level = LevelErr
	case corev1.VolumeReleased:
		level = LevelWarn
	}
	return []string{
		pv.Name, capacity.String(), accessModes(pv.Spec.AccessModes), string(pv.Spec.PersistentVolumeReclaimPolicy),
		string(pv.Status.Phase), orNone(claim), pv.Spec.StorageClassName, orNone(PVPath(pv)), age(u, c),
	}, level
}

func pvcRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	pvc := To[corev1.PersistentVolumeClaim](u)
	capacity := pvc.Status.Capacity[corev1.ResourceStorage]
	sc := ""
	if pvc.Spec.StorageClassName != nil {
		sc = *pvc.Spec.StorageClassName
	}
	level := LevelOK
	if pvc.Status.Phase != corev1.ClaimBound {
		level = LevelWarn
	}
	capStr := ""
	if !capacity.IsZero() {
		capStr = capacity.String()
	}
	return []string{
		pvc.Name, string(pvc.Status.Phase), pvc.Spec.VolumeName, capStr, accessModes(pvc.Status.AccessModes), sc, age(u, c),
	}, level
}

func storageClassRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	sc := To[storagev1.StorageClass](u)
	name := sc.Name
	if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		name += " (default)"
	}
	reclaim, binding := "Delete", "Immediate"
	if sc.ReclaimPolicy != nil {
		reclaim = string(*sc.ReclaimPolicy)
	}
	if sc.VolumeBindingMode != nil {
		binding = string(*sc.VolumeBindingMode)
	}
	exp := sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion
	return []string{name, sc.Provisioner, reclaim, binding, strconv.FormatBool(exp), age(u, c)}, LevelNone
}

// ---- Config ----

func configMapRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	data, _, _ := unstructured.NestedMap(u.Object, "data")
	bin, _, _ := unstructured.NestedMap(u.Object, "binaryData")
	return []string{u.GetName(), strconv.Itoa(len(data) + len(bin)), age(u, c)}, LevelNone
}

func secretRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	data, _, _ := unstructured.NestedMap(u.Object, "data")
	typ, _, _ := unstructured.NestedString(u.Object, "type")
	return []string{u.GetName(), typ, strconv.Itoa(len(data)), age(u, c)}, LevelNone
}

func bindingRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	kind, _, _ := unstructured.NestedString(u.Object, "roleRef", "kind")
	name, _, _ := unstructured.NestedString(u.Object, "roleRef", "name")
	subjects, _, _ := unstructured.NestedSlice(u.Object, "subjects")
	var subs []string
	for _, s := range subjects {
		m, _ := s.(map[string]any)
		n := fmt.Sprint(m["kind"]) + "/" + fmt.Sprint(m["name"])
		if ns, ok := m["namespace"].(string); ok && ns != "" {
			n = fmt.Sprint(m["kind"]) + "/" + ns + "/" + fmt.Sprint(m["name"])
		}
		subs = append(subs, n)
	}
	return []string{u.GetName(), kind + "/" + name, orNone(strings.Join(subs, ",")), age(u, c)}, LevelNone
}

// ---- Cluster ----

// NodeReady는 노드 Ready 여부와 STATUS 문자열을 돌려줍니다.
func NodeReady(n *corev1.Node) (bool, string) {
	status := "Unknown"
	ready := false
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			ready = cond.Status == corev1.ConditionTrue
			if ready {
				status = "Ready"
			} else {
				status = "NotReady"
			}
		}
	}
	if n.Spec.Unschedulable {
		status += ",SchedulingDisabled"
	}
	return ready, status
}

// NodeRoles는 node-role 레이블에서 역할 목록을 만듭니다.
func NodeRoles(n *corev1.Node) string {
	var roles []string
	for k := range n.Labels {
		if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && r != "" {
			roles = append(roles, r)
		}
	}
	sort.Strings(roles)
	return orNone(strings.Join(roles, ","))
}

// NodeInternalIP는 노드 InternalIP를 돌려줍니다.
func NodeInternalIP(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return ""
}

func pct(used, total int64) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", used*100/total)
}

func nodeRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	n := To[corev1.Node](u)
	ready, status := NodeReady(n)
	level := LevelOK
	if !ready {
		level = LevelErr
	} else if n.Spec.Unschedulable {
		level = LevelWarn
	}
	cpu, mem := "-", "-"
	if m, ok := c.Metrics.Node(n.Name); ok {
		alloc := n.Status.Allocatable
		cpu = FormatCPU(m.CPUMilli) + " (" + pct(m.CPUMilli, alloc.Cpu().MilliValue()) + ")"
		mem = FormatBytes(m.MemBytes) + " (" + pct(m.MemBytes, alloc.Memory().Value()) + ")"
	}
	return []string{
		n.Name, status, NodeRoles(n), cpu, mem, n.Status.NodeInfo.KubeletVersion, NodeInternalIP(n), age(u, c),
	}, level
}

func namespaceRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	level := LevelNone
	if phase == "Terminating" {
		level = LevelWarn
	}
	return []string{u.GetName(), phase, age(u, c)}, level
}

// EventTime은 이벤트의 마지막 발생 시각입니다.
func EventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case e.Series != nil:
		return e.Series.LastObservedTime.Time
	}
	return e.CreationTimestamp.Time
}

func eventRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	e := To[corev1.Event](u)
	level := LevelNone
	if e.Type == corev1.EventTypeWarning {
		level = LevelWarn
	}
	obj := strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name
	msg := strings.ReplaceAll(strings.TrimSpace(e.Message), "\n", " ")
	if e.Count > 1 {
		msg += fmt.Sprintf(" (x%d)", e.Count)
	}
	return []string{Age(EventTime(e), c.Now), e.Type, e.Reason, obj, msg}, level
}

func eventLess(a, b *unstructured.Unstructured) bool {
	return EventTime(To[corev1.Event](a)).After(EventTime(To[corev1.Event](b)))
}

func helmChartRow(u *unstructured.Unstructured, c RowCtx) ([]string, Level) {
	s := func(f string) string { v, _, _ := unstructured.NestedString(u.Object, "spec", f); return v }
	return []string{u.GetName(), s("chart"), orNone(s("version")), orNone(s("targetNamespace")), orNone(s("repo")), age(u, c)}, LevelNone
}
