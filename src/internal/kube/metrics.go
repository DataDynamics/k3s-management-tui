package kube

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Usage는 CPU(millicore)와 메모리(byte) 사용량입니다.
type Usage struct {
	CPUMilli int64
	MemBytes int64
}

// Metrics는 metrics-server에서 주기적으로 읽어 온 사용량 캐시입니다.
type Metrics struct {
	mu      sync.RWMutex
	pods    map[string]Usage // ns/name → 컨테이너 합계
	nodes   map[string]Usage
	err     error
	updated time.Time
}

func NewMetrics() *Metrics {
	return &Metrics{pods: map[string]Usage{}, nodes: map[string]Usage{}}
}

// Poll은 metrics-server에서 노드·Pod 사용량을 읽어 캐시를 갱신합니다.
func (m *Metrics) Poll(ctx context.Context, c *Client) error {
	if c == nil {
		return nil
	}
	nodes := map[string]Usage{}
	pods := map[string]Usage{}
	nl, err := c.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, n := range nl.Items {
			nodes[n.Name] = Usage{CPUMilli: n.Usage.Cpu().MilliValue(), MemBytes: n.Usage.Memory().Value()}
		}
		podList, perr := c.Metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if perr == nil {
			for _, p := range podList.Items {
				var u Usage
				for _, ct := range p.Containers {
					u.CPUMilli += ct.Usage.Cpu().MilliValue()
					u.MemBytes += ct.Usage.Memory().Value()
				}
				pods[p.Namespace+"/"+p.Name] = u
			}
		}
		err = perr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
	if err == nil {
		m.nodes, m.pods, m.updated = nodes, pods, time.Now()
	}
	return err
}

// Pod는 Pod 사용량을 돌려줍니다.
func (m *Metrics) Pod(ns, name string) (Usage, bool) {
	if m == nil {
		return Usage{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.pods[ns+"/"+name]
	return u, ok
}

// Node는 노드 사용량을 돌려줍니다.
func (m *Metrics) Node(name string) (Usage, bool) {
	if m == nil {
		return Usage{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.nodes[name]
	return u, ok
}

// Err는 마지막 조회 오류입니다 (metrics-server 미설치 등).
func (m *Metrics) Err() error {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.err
}
