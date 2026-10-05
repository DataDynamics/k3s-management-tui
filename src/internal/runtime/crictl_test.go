package runtime

import "testing"

func TestParseContainers(t *testing.T) {
	out := []byte(`{"containers":[
	 {"id":"abc123","metadata":{"name":"app","attempt":2},"image":{"image":"sha256:1111"},"imageRef":"sha256:1111",
	  "state":"CONTAINER_RUNNING","createdAt":"1791204607000000000",
	  "labels":{"io.kubernetes.pod.name":"web-1","io.kubernetes.pod.namespace":"default"}},
	 {"id":"def456","metadata":{"name":"init","attempt":0},"image":{"image":"sha256:2222"},
	  "state":"CONTAINER_EXITED","createdAt":"1791204600000000000",
	  "labels":{"io.kubernetes.pod.name":"web-1","io.kubernetes.pod.namespace":"default"}}]}`)
	list, err := ParseContainers(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].State != "RUNNING" || list[0].PodName != "web-1" || list[0].Attempt != 2 {
		t.Errorf("파싱 결과: %+v", list)
	}
	if list[0].Created.Unix() != 1791204607 {
		t.Errorf("생성 시각: %v", list[0].Created)
	}
	if _, err := ParseContainers([]byte("not json")); err == nil {
		t.Error("잘못된 JSON 허용")
	}
}

func TestParseImages(t *testing.T) {
	out := []byte(`{"images":[
	 {"id":"sha256:bbb","repoTags":["docker.io/library/busybox:1.37"],"size":"2203648"},
	 {"id":"sha256:aaa","repoTags":[],"repoDigests":["docker.io/x@sha256:ff"],"size":"100"}]}`)
	list, err := ParseImages(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Tags[0] != "docker.io/library/busybox:1.37" || list[0].Size != 2203648 {
		t.Errorf("파싱 결과: %+v", list)
	}
	if list[1].Tags[0] != "docker.io/x@sha256:ff" {
		t.Error("태그가 없으면 digest를 써야 함")
	}
}
