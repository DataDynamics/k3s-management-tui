package kube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forward는 실행 중인 port-forward 하나입니다.
type Forward struct {
	ID      int
	Target  string // svc/ns/name 또는 pod/ns/name
	Pod     string // 실제 연결된 ns/pod
	Address string
	Local   int
	Remote  int
	Started time.Time
	Status  string // starting | active | stopped | error
	Error   string
	stop    chan struct{}
}

// PortForwarder는 port-forward 목록을 관리합니다.
type PortForwarder struct {
	client  *Client
	address string

	mu       sync.Mutex
	nextID   int
	forwards map[int]*Forward
	onChange func()
}

func NewPortForwarder(c *Client, address string, onChange func()) *PortForwarder {
	if address == "" {
		address = "127.0.0.1"
	}
	return &PortForwarder{client: c, address: address, forwards: map[int]*Forward{}, onChange: onChange}
}

func (pf *PortForwarder) changed() {
	if pf.onChange != nil {
		pf.onChange()
	}
}

// List는 port-forward 목록(ID 순)의 복사본을 돌려줍니다.
func (pf *PortForwarder) List() []Forward {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	out := make([]Forward, 0, len(pf.forwards))
	for _, f := range pf.forwards {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ParsePorts는 "8080:80" 또는 "80"(로컬=원격)을 해석합니다.
func ParsePorts(s string) (local, remote int, err error) {
	var l, r string
	if i := indexByte(s, ':'); i >= 0 {
		l, r = s[:i], s[i+1:]
	} else {
		l, r = s, s
	}
	if local, err = strconv.Atoi(l); err != nil || local < 0 || local > 65535 {
		return 0, 0, fmt.Errorf("로컬 포트가 올바르지 않습니다: %q", l)
	}
	if remote, err = strconv.Atoi(r); err != nil || remote < 1 || remote > 65535 {
		return 0, 0, fmt.Errorf("원격 포트가 올바르지 않습니다: %q", r)
	}
	return local, remote, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// ResolveServicePort는 서비스 포트를 Pod의 컨테이너 포트로 바꾸고 대상 Pod를 고릅니다.
func (pf *PortForwarder) ResolveServicePort(ctx context.Context, ns, svcName string, svcPort int) (pod string, podPort int, err error) {
	svc, err := pf.client.Core.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", 0, err
	}
	pods, err := pf.client.PodsForSelector(ctx, ns, svc.Spec.Selector)
	if err != nil {
		return "", 0, err
	}
	if len(pods) == 0 {
		return "", 0, fmt.Errorf("서비스 %s/%s에 Running Pod가 없습니다", ns, svcName)
	}
	var target *intstr.IntOrString
	for _, p := range svc.Spec.Ports {
		if int(p.Port) == svcPort {
			t := p.TargetPort
			target = &t
		}
	}
	if target == nil {
		return "", 0, fmt.Errorf("서비스에 %d 포트가 없습니다", svcPort)
	}
	p := pods[0]
	switch {
	case target.Type == intstr.Int && target.IntValue() != 0:
		return p.Name, target.IntValue(), nil
	case target.Type == intstr.String:
		for _, c := range p.Spec.Containers {
			for _, cp := range c.Ports {
				if cp.Name == target.StrVal {
					return p.Name, int(cp.ContainerPort), nil
				}
			}
		}
		return "", 0, fmt.Errorf("Pod %s에 이름이 %q인 포트가 없습니다", p.Name, target.StrVal)
	}
	return p.Name, svcPort, nil
}

// Start는 Pod로 port-forward를 시작합니다. local이 0이면 임의 포트를 씁니다.
func (pf *PortForwarder) Start(target, ns, pod string, local, remote int) (*Forward, error) {
	p, err := pf.client.Core.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if p.Status.Phase != corev1.PodRunning {
		return nil, fmt.Errorf("Pod %s/%s가 Running 상태가 아닙니다 (%s)", ns, pod, p.Status.Phase)
	}
	transport, upgrader, err := spdy.RoundTripperFor(pf.client.REST)
	if err != nil {
		return nil, err
	}
	reqURL := pf.client.Core.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, (*url.URL)(reqURL))

	pf.mu.Lock()
	pf.nextID++
	f := &Forward{
		ID: pf.nextID, Target: target, Pod: ns + "/" + pod, Address: pf.address,
		Local: local, Remote: remote, Started: time.Now(), Status: "starting", stop: make(chan struct{}),
	}
	pf.forwards[f.ID] = f
	pf.mu.Unlock()

	ready := make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{pf.address},
		[]string{fmt.Sprintf("%d:%d", local, remote)}, f.stop, ready, io.Discard, io.Discard)
	if err != nil {
		pf.setStatus(f.ID, "error", err.Error())
		return nil, err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()
	select {
	case <-ready:
		if ports, err := fw.GetPorts(); err == nil && len(ports) > 0 {
			pf.mu.Lock()
			f.Local = int(ports[0].Local)
			pf.mu.Unlock()
		}
		pf.setStatus(f.ID, "active", "")
	case err := <-errCh:
		pf.setStatus(f.ID, "error", fmt.Sprint(err))
		return nil, fmt.Errorf("port-forward 시작 실패: %v", err)
	case <-time.After(15 * time.Second):
		close(f.stop)
		pf.setStatus(f.ID, "error", "시작 시간 초과")
		return nil, fmt.Errorf("port-forward 시작 시간 초과")
	}
	go func() {
		err := <-errCh
		pf.mu.Lock()
		if f.Status != "stopped" {
			f.Status = "error"
			if err != nil {
				f.Error = err.Error()
			} else {
				f.Error = "연결 종료"
			}
		}
		pf.mu.Unlock()
		pf.changed()
	}()
	pf.mu.Lock()
	out := *f
	pf.mu.Unlock()
	return &out, nil
}

func (pf *PortForwarder) setStatus(id int, status, errMsg string) {
	pf.mu.Lock()
	if f, ok := pf.forwards[id]; ok {
		f.Status, f.Error = status, errMsg
	}
	pf.mu.Unlock()
	pf.changed()
}

// Stop은 port-forward를 중지하고 목록에서 제거합니다.
func (pf *PortForwarder) Stop(id int) error {
	pf.mu.Lock()
	f, ok := pf.forwards[id]
	if !ok {
		pf.mu.Unlock()
		return fmt.Errorf("port-forward #%d가 없습니다", id)
	}
	wasActive := f.Status == "active" || f.Status == "starting"
	f.Status = "stopped"
	delete(pf.forwards, id)
	pf.mu.Unlock()
	if wasActive {
		close(f.stop)
	}
	pf.changed()
	return nil
}

// StopAll은 종료 시 모든 port-forward를 정리합니다.
func (pf *PortForwarder) StopAll() {
	for _, f := range pf.List() {
		_ = pf.Stop(f.ID)
	}
}
