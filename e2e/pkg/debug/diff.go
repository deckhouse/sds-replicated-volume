/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package debug

import (
	"fmt"
	"strings"
)

const (
	opEqual = iota
	opReplace
	opInsert
	opDelete
)

type opcode struct {
	tag    int
	i1, i2 int
	j1, j2 int
}

// UnifiedDiff computes a unified diff between two slices of YAML lines.
// Returns colored diff lines with YAML path breadcrumbs instead of raw line
// numbers. Returns nil when the inputs are equal.
func UnifiedDiff(a, b []string) []string {
	ops := diffOpcodes(a, b)

	const ctx = 0
	type group struct{ ops []opcode }
	var groups []group

	for _, op := range ops {
		if op.tag == opEqual {
			continue
		}
		if len(groups) == 0 {
			groups = append(groups, group{})
		}
		last := &groups[len(groups)-1]
		if len(last.ops) > 0 {
			prev := last.ops[len(last.ops)-1]
			gap := op.i1 - prev.i2
			if g := op.j1 - prev.j2; g > gap {
				gap = g
			}
			if gap > 2*ctx {
				groups = append(groups, group{})
				last = &groups[len(groups)-1]
			}
		}
		last.ops = append(last.ops, op)
	}

	if len(groups) == 0 {
		return nil
	}

	var out []string
	prevPath := ""

	for _, g := range groups {
		first := g.ops[0]
		lastOp := g.ops[len(g.ops)-1]

		i1 := max(first.i1-ctx, 0)
		i2 := min(lastOp.i2+ctx, len(a))
		j1 := max(first.j1-ctx, 0)
		j2 := min(lastOp.j2+ctx, len(b))

		path := yamlPath(b, first.j1)
		if path != "" && path != prevPath {
			out = append(out, fmt.Sprintf("── %s", path))
		} else if path == "" {
			out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", i1+1, i2-i1, j1+1, j2-j1))
		}
		prevPath = path

		ia, ib := i1, j1
		for _, op := range g.ops {
			for ia < op.i1 && ib < op.j1 {
				out = append(out, " "+a[ia])
				ia++
				ib++
			}
			switch op.tag {
			case opReplace:
				for i := op.i1; i < op.i2; i++ {
					out = append(out, "-"+a[i])
				}
				for j := op.j1; j < op.j2; j++ {
					out = append(out, "+"+b[j])
				}
			case opDelete:
				for i := op.i1; i < op.i2; i++ {
					out = append(out, "-"+a[i])
				}
			case opInsert:
				for j := op.j1; j < op.j2; j++ {
					out = append(out, "+"+b[j])
				}
			}
			ia, ib = op.i2, op.j2
		}
		for ia < i2 && ib < j2 {
			out = append(out, " "+a[ia])
			ia++
			ib++
		}
	}

	return out
}

// yamlPath computes the YAML key path leading to line idx in lines.
// It walks backwards to find parent keys at decreasing indentation levels.
func yamlPath(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}

	targetIndent := yamlIndent(lines[idx])

	var parts []string
	needIndent := targetIndent
	for i := idx; i >= 0; i-- {
		line := lines[i]
		indent := yamlIndent(line)

		if indent >= needIndent && i != idx {
			continue
		}

		key := yamlKeyFromLine(line)
		if key == "" {
			continue
		}

		if indent < needIndent {
			parts = append(parts, key)
			needIndent = indent
		}

		if indent == 0 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// yamlIndent returns the number of leading spaces in a YAML line.
func yamlIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// yamlKeyFromLine extracts the YAML mapping key from a line like "  foo:"
// or "  foo: bar". Returns "" for list items ("- ...") or lines without a key.
func yamlKeyFromLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		return ""
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		return trimmed[:idx]
	}
	return ""
}

func diffOpcodes(a, b []string) []opcode {
	n, m := len(a), len(b)

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var raw []opcode
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			raw = append(raw, opcode{opEqual, i, i + 1, j, j + 1})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			raw = append(raw, opcode{opDelete, i, i + 1, j, j})
			i++
		default:
			raw = append(raw, opcode{opInsert, i, i, j, j + 1})
			j++
		}
	}
	for i < n {
		raw = append(raw, opcode{opDelete, i, i + 1, j, j})
		i++
	}
	for j < m {
		raw = append(raw, opcode{opInsert, i, i, j, j + 1})
		j++
	}

	if len(raw) == 0 {
		return nil
	}

	merged := []opcode{raw[0]}
	for _, op := range raw[1:] {
		last := &merged[len(merged)-1]
		if op.tag == last.tag {
			last.i2 = op.i2
			last.j2 = op.j2
		} else {
			merged = append(merged, op)
		}
	}

	var result []opcode
	for k := 0; k < len(merged); k++ {
		if k+1 < len(merged) && merged[k].tag == opDelete && merged[k+1].tag == opInsert {
			result = append(result, opcode{opReplace, merged[k].i1, merged[k].i2, merged[k+1].j1, merged[k+1].j2})
			k++
		} else {
			result = append(result, merged[k])
		}
	}

	return result
}
