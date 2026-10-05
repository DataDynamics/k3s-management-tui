package kube

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

func (c *Client) res(d *ResourceDef, ns string) interface {
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions, sub ...string) error
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, sub ...string) (*unstructured.Unstructured, error)
	Get(ctx context.Context, name string, opts metav1.GetOptions, sub ...string) (*unstructured.Unstructured, error)
} {
	if d.Namespaced {
		return c.Dynamic.Resource(d.GVR).Namespace(ns)
	}
	return c.Dynamic.Resource(d.GVR)
}

// Delete는 리소스를 삭제합니다 (Background 전파).
func (c *Client) Delete(ctx context.Context, d *ResourceDef, ns, name string) error {
	prop := metav1.DeletePropagationBackground
	return c.res(d, ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &prop})
}

// Scale은 spec.replicas를 변경합니다 (Deployment, StatefulSet, ReplicaSet).
func (c *Client) Scale(ctx context.Context, d *ResourceDef, ns, name string, replicas int) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := c.res(d, ns).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// RolloutRestart는 kubectl rollout restart와 같이 Pod 템플릿 주석을 바꿔 재배포합니다.
func (c *Client) RolloutRestart(ctx context.Context, d *ResourceDef, ns, name string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().Format(time.RFC3339))
	_, err := c.res(d, ns).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// SetUnschedulable은 노드 cordon/uncordon입니다.
func (c *Client) SetUnschedulable(ctx context.Context, node string, v bool) error {
	patch := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, v)
	_, err := c.Core.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// Drain은 노드를 cordon한 뒤 Pod를 축출합니다.
// kubectl drain --ignore-daemonsets --delete-emptydir-data 와 같은 동작입니다.
// 컨트롤러 없는 Pod(mirror 제외)는 축출 후 다시 생성되지 않으므로 force가 아니면 중단합니다.
func (c *Client) Drain(ctx context.Context, node string, force bool, progress func(string)) error {
	if err := c.SetUnschedulable(ctx, node, true); err != nil {
		return fmt.Errorf("cordon 실패: %w", err)
	}
	pods, err := c.Core.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return err
	}
	var targets []corev1.Pod
	var orphans []string
	for _, p := range pods.Items {
		if _, mirror := p.Annotations[corev1.MirrorPodAnnotationKey]; mirror {
			continue
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		owner := metav1.GetControllerOf(&p)
		if owner != nil && owner.Kind == "DaemonSet" {
			continue
		}
		if owner == nil {
			orphans = append(orphans, p.Namespace+"/"+p.Name)
		}
		targets = append(targets, p)
	}
	if len(orphans) > 0 && !force {
		return fmt.Errorf("컨트롤러가 없는 Pod가 있어 중단합니다 (force 필요): %s", strings.Join(orphans, ", "))
	}
	for _, p := range targets {
		if progress != nil {
			progress("evict " + p.Namespace + "/" + p.Name)
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}}
		for attempt := 0; ; attempt++ {
			err := c.Core.CoreV1().Pods(p.Namespace).EvictV1(ctx, ev)
			if err == nil || apierrors.IsNotFound(err) {
				break
			}
			// PDB로 거부되면(429) 잠시 후 재시도합니다.
			if apierrors.IsTooManyRequests(err) && attempt < 30 {
				select {
				case <-time.After(2 * time.Second):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return fmt.Errorf("%s/%s 축출 실패: %w", p.Namespace, p.Name, err)
		}
	}
	return nil
}

// TriggerCronJob은 CronJob으로부터 Job을 즉시 생성합니다 (kubectl create job --from=cronjob/x).
func (c *Client) TriggerCronJob(ctx context.Context, ns, name string) (string, error) {
	cj, err := c.Core.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	jobName := fmt.Sprintf("%s-manual-%d", name, time.Now().Unix())
	if len(jobName) > 63 {
		jobName = jobName[len(jobName)-63:]
	}
	isController := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: jobName, Namespace: ns,
			Labels:      cj.Spec.JobTemplate.Labels,
			Annotations: map[string]string{"cronjob.kubernetes.io/instantiate": "manual"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1", Kind: "CronJob", Name: cj.Name, UID: cj.UID, Controller: &isController,
			}},
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}
	_, err = c.Core.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	return jobName, err
}

// YAML은 객체를 managedFields 없이 YAML로 직렬화합니다.
// Secret은 reveal이 false면 data 값을 가립니다.
func YAML(u *unstructured.Unstructured, reveal bool) (string, error) {
	obj := u.DeepCopy()
	obj.SetManagedFields(nil)
	if obj.GetKind() == "Secret" && !reveal {
		if data, ok, _ := unstructured.NestedMap(obj.Object, "data"); ok {
			for k := range data {
				data[k] = "********"
			}
			_ = unstructured.SetNestedMap(obj.Object, data, "data")
		}
		if ann := obj.GetAnnotations(); ann != nil {
			if _, ok := ann["kubectl.kubernetes.io/last-applied-configuration"]; ok {
				ann["kubectl.kubernetes.io/last-applied-configuration"] = "********"
				obj.SetAnnotations(ann)
			}
		}
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	out, err := yaml.JSONToYAML(data)
	return string(out), err
}

// DecodeSecret은 Secret의 data를 base64 디코드한 "키: 값" 목록으로 만듭니다.
func DecodeSecret(u *unstructured.Unstructured) string {
	data, _, _ := unstructured.NestedStringMap(u.Object, "data")
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		v, err := base64.StdEncoding.DecodeString(data[k])
		sb.WriteString("── " + k + " ──\n")
		if err != nil {
			sb.WriteString("(base64 디코드 실패) " + data[k] + "\n")
			continue
		}
		sb.WriteString(string(v))
		if len(v) > 0 && v[len(v)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	if len(keys) == 0 {
		sb.WriteString("(data 없음)\n")
	}
	return sb.String()
}

// Containers는 Pod의 컨테이너 이름 목록(init 포함)을 돌려줍니다.
func Containers(u *unstructured.Unstructured) (main []string, init []string) {
	p := To[corev1.Pod](u)
	for _, c := range p.Spec.Containers {
		main = append(main, c.Name)
	}
	for _, c := range p.Spec.InitContainers {
		init = append(init, c.Name)
	}
	return main, init
}

// LogOptions는 로그 스트리밍 옵션입니다.
type LogOptions struct {
	Container string
	Follow    bool
	Previous  bool
	TailLines int64
}

// StreamLogs는 Pod 로그를 한 줄씩 채널로 보냅니다. ctx 취소 시 종료합니다.
func (c *Client) StreamLogs(ctx context.Context, ns, pod string, o LogOptions) (<-chan string, error) {
	opts := &corev1.PodLogOptions{Container: o.Container, Follow: o.Follow, Previous: o.Previous}
	if o.TailLines > 0 {
		opts.TailLines = &o.TailLines
	}
	rc, err := c.Core.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return nil, err
	}
	return linesFrom(ctx, rc), nil
}

func linesFrom(ctx context.Context, rc io.ReadCloser) <-chan string {
	out := make(chan string, 256)
	go func() {
		defer close(out)
		defer rc.Close()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case out <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			select {
			case out <- "[k3stui] 로그 스트림 종료: " + err.Error():
			case <-ctx.Done():
			}
		}
	}()
	return out
}

// PodsForSelector는 서비스 셀렉터에 맞는 Running Pod 목록을 돌려줍니다.
func (c *Client) PodsForSelector(ctx context.Context, ns string, selector map[string]string) ([]corev1.Pod, error) {
	if len(selector) == 0 {
		return nil, fmt.Errorf("셀렉터가 없는 서비스입니다")
	}
	sel := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: selector})
	list, err := c.Core.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for _, p := range list.Items {
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			out = append(out, p)
		}
	}
	return out, nil
}
