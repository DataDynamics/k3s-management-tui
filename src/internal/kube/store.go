package kube

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Store는 리소스 종류별 Informer 캐시입니다. 처음 요청될 때 Informer를 시작합니다 (설계 7.2-3).
type Store struct {
	factory dynamicinformer.DynamicSharedInformerFactory
	stop    chan struct{}

	mu        sync.Mutex
	informers map[schema.GroupVersionResource]cache.SharedIndexInformer
	errors    map[schema.GroupVersionResource]string

	changed chan struct{}
}

// NewStore는 모든 네임스페이스를 감시하는 Store를 만듭니다.
func NewStore(dyn dynamic.Interface) *Store {
	return &Store{
		factory:   dynamicinformer.NewDynamicSharedInformerFactory(dyn, 10*time.Minute),
		stop:      make(chan struct{}),
		informers: map[schema.GroupVersionResource]cache.SharedIndexInformer{},
		errors:    map[schema.GroupVersionResource]string{},
		changed:   make(chan struct{}, 1),
	}
}

// Changed는 캐시 변경 알림 채널입니다. 여러 변경이 하나로 합쳐집니다.
func (s *Store) Changed() <-chan struct{} { return s.changed }

func (s *Store) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Ensure는 해당 리소스의 Informer가 없으면 만들어 시작합니다.
func (s *Store) Ensure(gvr schema.GroupVersionResource) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.informers[gvr]; ok {
		return
	}
	inf := s.factory.ForResource(gvr).Informer()
	// managedFields는 화면에서 쓰지 않으므로 메모리 절약을 위해 제거합니다.
	_ = inf.SetTransform(func(obj any) (any, error) {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			u.SetManagedFields(nil)
		}
		return obj, nil
	})
	_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		slog.Warn("watch error", "gvr", gvr.String(), "err", err)
		s.mu.Lock()
		s.errors[gvr] = err.Error()
		s.mu.Unlock()
		s.notify()
	})
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { s.clearErr(gvr); s.notify() },
		UpdateFunc: func(any, any) { s.notify() },
		DeleteFunc: func(any) { s.notify() },
	})
	s.informers[gvr] = inf
	s.factory.Start(s.stop)
	go func() {
		if cache.WaitForCacheSync(s.stop, inf.HasSynced) {
			s.clearErr(gvr)
			s.notify()
		}
	}()
}

func (s *Store) clearErr(gvr schema.GroupVersionResource) {
	s.mu.Lock()
	delete(s.errors, gvr)
	s.mu.Unlock()
}

// Synced는 첫 목록 조회가 끝났는지 알려줍니다.
func (s *Store) Synced(gvr schema.GroupVersionResource) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	inf, ok := s.informers[gvr]
	s.mu.Unlock()
	return ok && inf.HasSynced()
}

// Err는 마지막 watch 오류를 돌려줍니다.
func (s *Store) Err(gvr schema.GroupVersionResource) string {
	if s == nil {
		return "클러스터에 연결되지 않았습니다"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errors[gvr]
}

// List는 캐시에서 객체 목록을 돌려줍니다. ns가 ""이면 전체. 네임스페이스·이름 순으로 정렬합니다.
// 반환된 객체는 캐시와 공유되므로 수정하면 안 됩니다.
func (s *Store) List(gvr schema.GroupVersionResource, ns string) []*unstructured.Unstructured {
	if s == nil {
		return nil
	}
	s.Ensure(gvr)
	s.mu.Lock()
	inf := s.informers[gvr]
	s.mu.Unlock()
	var items []any
	if ns == "" {
		items = inf.GetStore().List()
	} else {
		items, _ = inf.GetIndexer().ByIndex(cache.NamespaceIndex, ns)
	}
	out := make([]*unstructured.Unstructured, 0, len(items))
	for _, it := range items {
		if u, ok := it.(*unstructured.Unstructured); ok {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GetNamespace() != out[j].GetNamespace() {
			return out[i].GetNamespace() < out[j].GetNamespace()
		}
		return out[i].GetName() < out[j].GetName()
	})
	return out
}

// Get은 캐시에서 객체 하나를 찾습니다.
func (s *Store) Get(gvr schema.GroupVersionResource, ns, name string) *unstructured.Unstructured {
	if s == nil {
		return nil
	}
	s.Ensure(gvr)
	s.mu.Lock()
	inf := s.informers[gvr]
	s.mu.Unlock()
	key := name
	if ns != "" {
		key = ns + "/" + name
	}
	obj, ok, _ := inf.GetStore().GetByKey(key)
	if !ok {
		return nil
	}
	u, _ := obj.(*unstructured.Unstructured)
	return u
}

// Stop은 모든 Informer를 멈춥니다.
func (s *Store) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
