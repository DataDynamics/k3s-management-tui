package kube

import (
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Level은 행의 상태 색상 구분입니다.
type Level int

const (
	LevelNone Level = iota
	LevelOK
	LevelWarn
	LevelErr
	LevelMuted
)

// RowCtx는 행 생성 시 필요한 부가 정보입니다.
type RowCtx struct {
	Now     time.Time
	Metrics *Metrics
}

// Column은 테이블 컬럼 정의입니다.
type Column struct {
	Name     string
	MaxWidth int // 0이면 제한 없음
}

// ResourceDef는 리소스 한 종류의 화면 정의입니다 (설계 7.2-4).
// 새 리소스 추가 = 정의 하나 추가.
type ResourceDef struct {
	Key        string   // 명령 모드·views.d 키 (pods)
	Aliases    []string // :po, :pod
	Title      string   // 탭 제목 (Pods)
	Kind       string   // kubectl에 넘길 이름 (pods, deployments.apps)
	GVR        schema.GroupVersionResource
	Namespaced bool
	Columns    []Column
	Row        func(u *unstructured.Unstructured, c RowCtx) ([]string, Level)
	// Less가 있으면 기본 정렬(네임스페이스·이름) 대신 사용합니다.
	Less func(a, b *unstructured.Unstructured) bool
}

var (
	GVRPods            = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	GVRServices        = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	GVRNodes           = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	GVRNamespaces      = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	GVREvents          = schema.GroupVersionResource{Version: "v1", Resource: "events"}
	GVRConfigMaps      = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	GVRSecrets         = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	GVRServiceAccounts = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
	GVRPVs             = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	GVRPVCs            = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	GVRDeployments     = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	GVRStatefulSets    = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	GVRDaemonSets      = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	GVRReplicaSets     = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	GVRJobs            = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	GVRCronJobs        = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}
	GVRIngresses       = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	GVRNetworkPolicies = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	GVREndpointSlices  = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
	GVRStorageClasses  = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
	GVRRoles           = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}
	GVRRoleBindings    = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
	GVRClusterRoles    = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	GVRClusterRBs      = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	GVRHelmCharts      = schema.GroupVersionResource{Group: "helm.cattle.io", Version: "v1", Resource: "helmcharts"}
	GVRHelmChartConfig = schema.GroupVersionResource{Group: "helm.cattle.io", Version: "v1", Resource: "helmchartconfigs"}
	GVRCiliumNP        = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}
	GVRCiliumCNP       = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}
)

var registry = []*ResourceDef{
	{Key: "pods", Aliases: []string{"po", "pod"}, Title: "Pods", Kind: "pods", GVR: GVRPods, Namespaced: true,
		Columns: cols("NAME", "READY", "STATUS", "RESTARTS", "CPU", "MEM", "IP", "NODE", "AGE"), Row: podRow},
	{Key: "deployments", Aliases: []string{"deploy", "deployment"}, Title: "Deployments", Kind: "deployments.apps", GVR: GVRDeployments, Namespaced: true,
		Columns: cols("NAME", "READY", "UP-TO-DATE", "AVAILABLE", "IMAGES", "AGE"), Row: deploymentRow},
	{Key: "statefulsets", Aliases: []string{"sts", "statefulset"}, Title: "StatefulSets", Kind: "statefulsets.apps", GVR: GVRStatefulSets, Namespaced: true,
		Columns: cols("NAME", "READY", "IMAGES", "AGE"), Row: statefulSetRow},
	{Key: "daemonsets", Aliases: []string{"ds", "daemonset"}, Title: "DaemonSets", Kind: "daemonsets.apps", GVR: GVRDaemonSets, Namespaced: true,
		Columns: cols("NAME", "DESIRED", "CURRENT", "READY", "UP-TO-DATE", "AVAILABLE", "AGE"), Row: daemonSetRow},
	{Key: "replicasets", Aliases: []string{"rs", "replicaset"}, Title: "ReplicaSets", Kind: "replicasets.apps", GVR: GVRReplicaSets, Namespaced: true,
		Columns: cols("NAME", "DESIRED", "CURRENT", "READY", "AGE"), Row: replicaSetRow},
	{Key: "jobs", Aliases: []string{"job"}, Title: "Jobs", Kind: "jobs.batch", GVR: GVRJobs, Namespaced: true,
		Columns: cols("NAME", "STATUS", "COMPLETIONS", "DURATION", "AGE"), Row: jobRow},
	{Key: "cronjobs", Aliases: []string{"cj", "cronjob"}, Title: "CronJobs", Kind: "cronjobs.batch", GVR: GVRCronJobs, Namespaced: true,
		Columns: cols("NAME", "SCHEDULE", "SUSPEND", "ACTIVE", "LAST SCHEDULE", "AGE"), Row: cronJobRow},

	{Key: "services", Aliases: []string{"svc", "service"}, Title: "Services", Kind: "services", GVR: GVRServices, Namespaced: true,
		Columns: cols("NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "PORTS", "AGE"), Row: serviceRow},
	{Key: "ingresses", Aliases: []string{"ing", "ingress"}, Title: "Ingresses", Kind: "ingresses.networking.k8s.io", GVR: GVRIngresses, Namespaced: true,
		Columns: cols("NAME", "CLASS", "HOSTS", "ADDRESS", "PORTS", "AGE"), Row: ingressRow},
	{Key: "endpointslices", Aliases: []string{"eps", "ep", "endpoints"}, Title: "EndpointSlices", Kind: "endpointslices.discovery.k8s.io", GVR: GVREndpointSlices, Namespaced: true,
		Columns: cols("NAME", "ADDRESSTYPE", "PORTS", "ENDPOINTS", "AGE"), Row: endpointSliceRow},
	{Key: "networkpolicies", Aliases: []string{"netpol", "np"}, Title: "NetworkPolicies", Kind: "networkpolicies.networking.k8s.io", GVR: GVRNetworkPolicies, Namespaced: true,
		Columns: cols("NAME", "POD-SELECTOR", "AGE"), Row: netpolRow},
	{Key: "ciliumnetworkpolicies", Aliases: []string{"cnp"}, Title: "CiliumNP", Kind: "ciliumnetworkpolicies.cilium.io", GVR: GVRCiliumNP, Namespaced: true,
		Columns: cols("NAME", "VALID", "AGE"), Row: ciliumPolicyRow},
	{Key: "ciliumclusterwidenetworkpolicies", Aliases: []string{"ccnp"}, Title: "CiliumCCNP", Kind: "ciliumclusterwidenetworkpolicies.cilium.io", GVR: GVRCiliumCNP,
		Columns: cols("NAME", "VALID", "AGE"), Row: ciliumPolicyRow},

	{Key: "persistentvolumes", Aliases: []string{"pv"}, Title: "PV", Kind: "persistentvolumes", GVR: GVRPVs,
		Columns: cols("NAME", "CAPACITY", "ACCESS", "RECLAIM", "STATUS", "CLAIM", "STORAGECLASS", "PATH", "AGE"), Row: pvRow},
	{Key: "persistentvolumeclaims", Aliases: []string{"pvc"}, Title: "PVC", Kind: "persistentvolumeclaims", GVR: GVRPVCs, Namespaced: true,
		Columns: cols("NAME", "STATUS", "VOLUME", "CAPACITY", "ACCESS", "STORAGECLASS", "AGE"), Row: pvcRow},
	{Key: "storageclasses", Aliases: []string{"sc"}, Title: "StorageClasses", Kind: "storageclasses.storage.k8s.io", GVR: GVRStorageClasses,
		Columns: cols("NAME", "PROVISIONER", "RECLAIM", "BINDING", "EXPANSION", "AGE"), Row: storageClassRow},

	{Key: "configmaps", Aliases: []string{"cm", "configmap"}, Title: "ConfigMaps", Kind: "configmaps", GVR: GVRConfigMaps, Namespaced: true,
		Columns: cols("NAME", "DATA", "AGE"), Row: configMapRow},
	{Key: "secrets", Aliases: []string{"secret"}, Title: "Secrets", Kind: "secrets", GVR: GVRSecrets, Namespaced: true,
		Columns: cols("NAME", "TYPE", "DATA", "AGE"), Row: secretRow},
	{Key: "serviceaccounts", Aliases: []string{"sa"}, Title: "ServiceAccounts", Kind: "serviceaccounts", GVR: GVRServiceAccounts, Namespaced: true,
		Columns: cols("NAME", "AGE"), Row: nameAgeRow},
	{Key: "roles", Aliases: []string{"role"}, Title: "Roles", Kind: "roles.rbac.authorization.k8s.io", GVR: GVRRoles, Namespaced: true,
		Columns: cols("NAME", "AGE"), Row: nameAgeRow},
	{Key: "rolebindings", Aliases: []string{"rb"}, Title: "RoleBindings", Kind: "rolebindings.rbac.authorization.k8s.io", GVR: GVRRoleBindings, Namespaced: true,
		Columns: cols("NAME", "ROLE", "SUBJECTS", "AGE"), Row: bindingRow},
	{Key: "clusterroles", Aliases: []string{"cr"}, Title: "ClusterRoles", Kind: "clusterroles.rbac.authorization.k8s.io", GVR: GVRClusterRoles,
		Columns: cols("NAME", "AGE"), Row: nameAgeRow},
	{Key: "clusterrolebindings", Aliases: []string{"crb"}, Title: "ClusterRoleBindings", Kind: "clusterrolebindings.rbac.authorization.k8s.io", GVR: GVRClusterRBs,
		Columns: cols("NAME", "ROLE", "SUBJECTS", "AGE"), Row: bindingRow},

	{Key: "nodes", Aliases: []string{"no", "node"}, Title: "Nodes", Kind: "nodes", GVR: GVRNodes,
		Columns: cols("NAME", "STATUS", "ROLES", "CPU", "MEM", "VERSION", "INTERNAL-IP", "AGE"), Row: nodeRow},
	{Key: "namespaces", Aliases: []string{"ns", "namespace"}, Title: "Namespaces", Kind: "namespaces", GVR: GVRNamespaces,
		Columns: cols("NAME", "STATUS", "AGE"), Row: namespaceRow},
	{Key: "events", Aliases: []string{"ev", "event"}, Title: "Events", Kind: "events", GVR: GVREvents, Namespaced: true,
		Columns: []Column{{Name: "LAST SEEN"}, {Name: "TYPE"}, {Name: "REASON"}, {Name: "OBJECT", MaxWidth: 50}, {Name: "MESSAGE", MaxWidth: 200}},
		Row:     eventRow, Less: eventLess},

	{Key: "helmcharts", Aliases: []string{"hc", "helmchart"}, Title: "HelmCharts", Kind: "helmcharts.helm.cattle.io", GVR: GVRHelmCharts, Namespaced: true,
		Columns: cols("NAME", "CHART", "VERSION", "TARGET-NS", "REPO", "AGE"), Row: helmChartRow},
	{Key: "helmchartconfigs", Aliases: []string{"hcc"}, Title: "HelmChartConfigs", Kind: "helmchartconfigs.helm.cattle.io", GVR: GVRHelmChartConfig, Namespaced: true,
		Columns: cols("NAME", "AGE"), Row: nameAgeRow},
}

func cols(names ...string) []Column {
	out := make([]Column, len(names))
	for i, n := range names {
		out[i] = Column{Name: n}
		if n == "IMAGES" || n == "PORTS" || n == "SUBJECTS" || n == "HOSTS" || n == "ENDPOINTS" {
			out[i].MaxWidth = 60
		}
	}
	return out
}

// Def는 키 또는 별칭으로 리소스 정의를 찾습니다.
func Def(key string) *ResourceDef {
	key = strings.ToLower(key)
	for _, d := range registry {
		if d.Key == key {
			return d
		}
		for _, a := range d.Aliases {
			if a == key {
				return d
			}
		}
	}
	return nil
}

// Defs는 등록된 모든 정의를 돌려줍니다.
func Defs() []*ResourceDef { return registry }

// SortObjects는 정의의 정렬 규칙으로 목록을 정렬합니다.
func (d *ResourceDef) SortObjects(items []*unstructured.Unstructured) {
	if d.Less != nil {
		sort.SliceStable(items, func(i, j int) bool { return d.Less(items[i], items[j]) })
	}
}
