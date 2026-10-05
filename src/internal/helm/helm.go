// Package helm은 helm CLI 래퍼입니다.
package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/DataDynamics/k3s-management-tui/internal/executil"
)

// Client는 helm 명령 실행기입니다. KUBECONFIG는 Runner 환경변수로 넘깁니다.
type Client struct {
	Binary  string
	Context string // 비우면 kubeconfig의 current-context를 씁니다
	Run     executil.Runner
}

// Release는 helm list 결과 한 줄입니다.
type Release struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

func (c *Client) helm(ctx context.Context, args ...string) ([]byte, error) {
	if c.Context != "" {
		args = append(args, "--kube-context", c.Context)
	}
	return c.Run.Run(ctx, c.Binary, args...)
}

// List는 모든 네임스페이스의 릴리스(실패·대기 포함)입니다.
func (c *Client) List(ctx context.Context) ([]Release, error) {
	// helm v4는 기본으로 모든 상태를 보여주며 -a 플래그가 없습니다. v3는 -a가 있어야 실패·대기 릴리스가 보입니다.
	out, err := c.helm(ctx, "list", "-A", "-o", "json", "-a")
	if err != nil && strings.Contains(err.Error(), "unknown shorthand flag") {
		out, err = c.helm(ctx, "list", "-A", "-o", "json")
	}
	if err != nil {
		return nil, err
	}
	return ParseList(out)
}

// ParseList는 helm list -o json 출력을 해석합니다.
func ParseList(out []byte) ([]Release, error) {
	var rels []Release
	if err := json.Unmarshal(out, &rels); err != nil {
		return nil, fmt.Errorf("helm list 출력 해석 실패: %w", err)
	}
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Namespace != rels[j].Namespace {
			return rels[i].Namespace < rels[j].Namespace
		}
		return rels[i].Name < rels[j].Name
	})
	return rels, nil
}

// Values는 사용자 지정 values(all이면 계산된 전체 values)입니다.
func (c *Client) Values(ctx context.Context, ns, name string, all bool) (string, error) {
	args := []string{"get", "values", name, "-n", ns, "-o", "yaml"}
	if all {
		args = append(args, "-a")
	}
	out, err := c.helm(ctx, args...)
	return string(out), err
}

// Manifest는 릴리스가 배포한 manifest입니다.
func (c *Client) Manifest(ctx context.Context, ns, name string) (string, error) {
	out, err := c.helm(ctx, "get", "manifest", name, "-n", ns)
	return string(out), err
}

// History는 릴리스 이력 표입니다.
func (c *Client) History(ctx context.Context, ns, name string) (string, error) {
	out, err := c.helm(ctx, "history", name, "-n", ns, "--max", "50")
	return string(out), err
}

// Rollback은 지정 리비전으로 되돌립니다.
func (c *Client) Rollback(ctx context.Context, ns, name string, revision int) error {
	if revision < 1 {
		return fmt.Errorf("리비전은 1 이상이어야 합니다")
	}
	_, err := c.helm(ctx, "rollback", name, strconv.Itoa(revision), "-n", ns, "--wait", "--timeout", "5m")
	return err
}

// Uninstall은 릴리스를 삭제합니다.
func (c *Client) Uninstall(ctx context.Context, ns, name string) error {
	_, err := c.helm(ctx, "uninstall", name, "-n", ns)
	return err
}
