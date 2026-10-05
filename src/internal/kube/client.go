// Package kube는 client-go 래퍼입니다: 클라이언트 생성, Informer 캐시, 리소스 작업.
package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client는 클러스터 접근에 필요한 클라이언트 묶음입니다.
type Client struct {
	Kubeconfig string
	REST       *rest.Config
	Core       kubernetes.Interface
	Dynamic    dynamic.Interface
	Metrics    metricsclient.Interface
	Discovery  discovery.DiscoveryInterface

	ServerVersion string
	available     map[schema.GroupVersionResource]bool
}

// New는 kubeconfig로 클라이언트를 만들고 서버 버전과 사용 가능한 리소스를 조회합니다.
func New(kubeconfig string) (*Client, error) {
	rc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig 로딩 실패 (%s): %w", kubeconfig, err)
	}
	rc.QPS, rc.Burst = 50, 100
	rc.Timeout = 30 * time.Second
	core, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	mc, err := metricsclient.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Kubeconfig: kubeconfig, REST: rc, Core: core, Dynamic: dyn, Metrics: mc,
		Discovery: core.Discovery(),
	}
	sv, err := c.Discovery.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("API 서버 연결 실패: %w", err)
	}
	c.ServerVersion = sv.GitVersion
	c.refreshAvailable()
	return c, nil
}

// refreshAvailable은 discovery로 서버가 제공하는 리소스 목록을 캐시합니다.
// CRD(Cilium, HelmChart 등)가 없는 클러스터에서 해당 뷰를 숨기기 위해 씁니다.
func (c *Client) refreshAvailable() {
	c.available = map[schema.GroupVersionResource]bool{}
	_, lists, err := c.Discovery.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		return
	}
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			c.available[gv.WithResource(r.Name)] = true
		}
	}
}

// Has는 서버가 해당 리소스를 제공하는지 알려줍니다. discovery 실패 시에는 true로 봅니다.
func (c *Client) Has(gvr schema.GroupVersionResource) bool {
	if c == nil {
		return false
	}
	if len(c.available) == 0 {
		return true
	}
	return c.available[gvr]
}

// Ping은 API 서버 상태를 확인합니다 (헤더 표시용).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Core.Discovery().RESTClient().Get().AbsPath("/readyz").DoRaw(ctx)
	return err
}
