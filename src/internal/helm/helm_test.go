package helm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct{ calls [][]string }

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	for _, a := range args {
		if a == "-a" && len(args) > 1 && args[0] == "list" {
			// helm v4 동작 흉내
			return nil, errors.New("helm list: exit status 1: Error: unknown shorthand flag: 'a' in -a")
		}
	}
	return []byte(`[{"name":"b","namespace":"kube-system","revision":"2","status":"deployed","chart":"cilium-1.20.2","app_version":"1.20.2"},
	{"name":"a","namespace":"default","revision":"1","status":"failed","chart":"x-1","app_version":"1"}]`), nil
}

func TestListFallsBackForHelmV4(t *testing.T) {
	r := &fakeRunner{}
	c := &Client{Binary: "helm", Run: r}
	rels, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 || strings.Contains(strings.Join(r.calls[1], " "), "-a") {
		t.Errorf("v4 재시도 실패: %v", r.calls)
	}
	if len(rels) != 2 || rels[0].Namespace != "default" {
		t.Errorf("정렬: %+v", rels)
	}
}

func TestRollbackValidatesRevision(t *testing.T) {
	c := &Client{Binary: "helm", Run: &fakeRunner{}}
	if err := c.Rollback(context.Background(), "ns", "r", 0); err == nil {
		t.Error("리비전 0 허용")
	}
}
