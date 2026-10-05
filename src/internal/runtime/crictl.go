// Package runtime은 노드의 컨테이너 런타임을 crictl로 조회합니다 (K3S: k3s crictl, kubeadm: crictl).
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DataDynamics/k3s-management-tui/internal/executil"
)

// Crictl은 crictl 래퍼입니다. Command는 배포판별 실행 명령 앞부분입니다
// (예: [k3s crictl], [crictl --runtime-endpoint unix:///run/containerd/containerd.sock]).
type Crictl struct {
	Command []string
	Run     executil.Runner
}

// Container는 crictl ps 결과 한 줄입니다.
type Container struct {
	ID        string
	Name      string
	PodName   string
	Namespace string
	State     string
	Image     string
	ImageRef  string
	Created   time.Time
	Attempt   int
}

// Image는 crictl images 결과 한 줄입니다.
type Image struct {
	ID    string
	Tags  []string
	Size  int64
	InUse bool
}

func (c *Crictl) crictl(ctx context.Context, args ...string) ([]byte, error) {
	if len(c.Command) == 0 {
		return nil, fmt.Errorf("crictl 명령이 설정되지 않았습니다")
	}
	return c.Run.Run(ctx, c.Command[0], append(append([]string{}, c.Command[1:]...), args...)...)
}

// Containers는 모든 컨테이너(종료된 것 포함)를 돌려줍니다.
func (c *Crictl) Containers(ctx context.Context) ([]Container, error) {
	out, err := c.crictl(ctx, "ps", "-a", "-o", "json")
	if err != nil {
		return nil, err
	}
	list, err := ParseContainers(out)
	if err != nil {
		return nil, err
	}
	// crictl ps의 image 필드는 이미지 ID(sha256)이므로 태그 이름으로 바꿔 보여줍니다.
	if iout, err := c.crictl(ctx, "images", "-o", "json"); err == nil {
		if imgs, err := ParseImages(iout); err == nil {
			names := map[string]string{}
			for _, im := range imgs {
				names[im.ID] = firstTag(im)
			}
			for i := range list {
				if n, ok := names[list[i].Image]; ok {
					list[i].Image = n
				} else if n, ok := names[list[i].ImageRef]; ok {
					list[i].Image = n
				}
			}
		}
	}
	return list, nil
}

// ParseContainers는 crictl ps -o json 출력을 해석합니다.
func ParseContainers(out []byte) ([]Container, error) {
	var resp struct {
		Containers []struct {
			ID       string `json:"id"`
			Metadata struct {
				Name    string `json:"name"`
				Attempt int    `json:"attempt"`
			} `json:"metadata"`
			Image struct {
				Image string `json:"image"`
			} `json:"image"`
			ImageRef  string            `json:"imageRef"`
			State     string            `json:"state"`
			CreatedAt string            `json:"createdAt"`
			Labels    map[string]string `json:"labels"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("crictl ps 출력 해석 실패: %w", err)
	}
	list := make([]Container, 0, len(resp.Containers))
	for _, ct := range resp.Containers {
		ns, _ := strconv.ParseInt(ct.CreatedAt, 10, 64)
		list = append(list, Container{
			ID: ct.ID, Name: ct.Metadata.Name, Attempt: ct.Metadata.Attempt,
			PodName: ct.Labels["io.kubernetes.pod.name"], Namespace: ct.Labels["io.kubernetes.pod.namespace"],
			State: strings.TrimPrefix(ct.State, "CONTAINER_"), Image: ct.Image.Image, ImageRef: ct.ImageRef,
			Created: time.Unix(0, ns),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Namespace != list[j].Namespace {
			return list[i].Namespace < list[j].Namespace
		}
		if list[i].PodName != list[j].PodName {
			return list[i].PodName < list[j].PodName
		}
		return list[i].Created.After(list[j].Created)
	})
	return list, nil
}

// Images는 이미지 목록이며, 컨테이너가 참조하는 이미지는 InUse로 표시합니다.
func (c *Crictl) Images(ctx context.Context) ([]Image, error) {
	out, err := c.crictl(ctx, "images", "-o", "json")
	if err != nil {
		return nil, err
	}
	imgs, err := ParseImages(out)
	if err != nil {
		return nil, err
	}
	if pout, err := c.crictl(ctx, "ps", "-a", "-o", "json"); err == nil {
		cs, _ := ParseContainers(pout)
		used := map[string]bool{}
		for _, ct := range cs {
			used[ct.ImageRef] = true
			used[ct.Image] = true
		}
		for i := range imgs {
			if used[imgs[i].ID] {
				imgs[i].InUse = true
				continue
			}
			for _, t := range imgs[i].Tags {
				if used[t] {
					imgs[i].InUse = true
				}
			}
		}
	}
	return imgs, nil
}

// ParseImages는 crictl images -o json 출력을 해석합니다.
func ParseImages(out []byte) ([]Image, error) {
	var resp struct {
		Images []struct {
			ID          string   `json:"id"`
			RepoTags    []string `json:"repoTags"`
			RepoDigests []string `json:"repoDigests"`
			Size        string   `json:"size"`
		} `json:"images"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("crictl images 출력 해석 실패: %w", err)
	}
	list := make([]Image, 0, len(resp.Images))
	for _, im := range resp.Images {
		size, _ := strconv.ParseInt(im.Size, 10, 64)
		tags := im.RepoTags
		if len(tags) == 0 {
			tags = im.RepoDigests
		}
		list = append(list, Image{ID: im.ID, Tags: tags, Size: size})
	}
	sort.Slice(list, func(i, j int) bool { return firstTag(list[i]) < firstTag(list[j]) })
	return list, nil
}

func firstTag(i Image) string {
	if len(i.Tags) > 0 {
		return i.Tags[0]
	}
	return i.ID
}

// Inspect는 crictl inspect 출력(JSON)입니다.
func (c *Crictl) Inspect(ctx context.Context, id string) (string, error) {
	out, err := c.crictl(ctx, "inspect", id)
	return string(out), err
}

// InspectImage는 crictl inspecti 출력(JSON)입니다.
func (c *Crictl) InspectImage(ctx context.Context, id string) (string, error) {
	out, err := c.crictl(ctx, "inspecti", id)
	return string(out), err
}

// Logs는 컨테이너 로그 마지막 n줄입니다.
func (c *Crictl) Logs(ctx context.Context, id string, tail int) (string, error) {
	// crictl logs는 컨테이너 stderr를 stderr로 내보내므로 Runner 대신 결합 출력을 씁니다.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(c.Command) == 0 {
		return "", fmt.Errorf("crictl 명령이 설정되지 않았습니다")
	}
	args := append(append([]string{}, c.Command[1:]...), "logs", "--tail", strconv.Itoa(tail), id)
	ch, err := executil.Stream(ctx, nil, c.Command[0], args...)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for line := range ch {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// RemoveImage는 이미지를 삭제합니다.
func (c *Crictl) RemoveImage(ctx context.Context, id string) error {
	_, err := c.crictl(ctx, "rmi", id)
	return err
}

// PruneImages는 사용하지 않는 이미지를 모두 삭제하고 결과 출력을 돌려줍니다.
func (c *Crictl) PruneImages(ctx context.Context) (string, error) {
	out, err := c.crictl(ctx, "rmi", "--prune")
	return strings.TrimSpace(string(out)), err
}
