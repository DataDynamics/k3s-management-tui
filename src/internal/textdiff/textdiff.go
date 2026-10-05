// Package textdiff는 설정 파일 변경 확인용 간단한 줄 단위 diff를 만듭니다.
package textdiff

import (
	"fmt"
	"strings"
)

// Unified는 a → b 변경을 unified diff 형식(문맥 context줄)으로 돌려줍니다. 같으면 "".
func Unified(aName, bName, a, b string, context int) string {
	if a == b {
		return ""
	}
	al, bl := splitLines(a), splitLines(b)
	ops := lcsOps(al, bl)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aName, bName)
	// 변경이 있는 op 주변 context줄만 출력합니다.
	n := len(ops)
	show := make([]bool, n)
	for i, op := range ops {
		if op.kind != ' ' {
			for j := max(0, i-context); j <= min(n-1, i+context); j++ {
				show[j] = true
			}
		}
	}
	inHunk := false
	for i, op := range ops {
		if !show[i] {
			inHunk = false
			continue
		}
		if !inHunk {
			fmt.Fprintf(&sb, "@@ -%d +%d @@\n", op.aLine, op.bLine)
			inHunk = true
		}
		sb.WriteByte(op.kind)
		sb.WriteString(op.text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

type op struct {
	kind         byte // ' ', '-', '+'
	text         string
	aLine, bLine int
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcsOps는 최장 공통 부분열로 편집 스크립트를 만듭니다. 설정 파일 크기(수백 줄)에서는 O(n*m)로 충분합니다.
func lcsOps(a, b []string) []op {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i], i + 1, j + 1})
			i++
			j++
		case j < m && (i == n || dp[i][j+1] >= dp[i+1][j]):
			ops = append(ops, op{'+', b[j], i + 1, j + 1})
			j++
		default:
			ops = append(ops, op{'-', a[i], i + 1, j + 1})
			i++
		}
	}
	return ops
}
