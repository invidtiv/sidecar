package main

import (
	"fmt"
	"strings"
)

// A small line diff, for the report only.
//
// The report body has to show what changed in an upstream asset since the
// version a Sidecar port was written against, and neither the standard library
// nor anything already in this module renders one. The files involved are hook
// scripts of a few hundred lines, so an ordinary quadratic LCS is the right
// amount of machinery: it is exact, it is thirty lines, and it needs no
// dependency in a tool that runs in CI.

// diffLineBudget bounds one rendered diff. A sync report is a pull-request
// body, and an unbounded diff of a rewritten asset would bury the rest of it.
const diffLineBudget = 120

// diffContext is how many unchanged lines surround each hunk.
const diffContext = 3

// maxDiffLines is the size past which the LCS table is not worth building.
// Nothing upstream is close to it; the guard exists so a file that is one day
// generated cannot make a sync hang.
const maxDiffLines = 4000

// unifiedDiff renders old versus new as a unified diff, bounded to budget
// lines. It returns the rendered diff and whether anything differed at all.
func unifiedDiff(oldName, newName string, oldData, newData []byte, budget int) (string, bool) {
	if string(oldData) == string(newData) {
		return "", false
	}
	oldLines := splitLines(oldData)
	newLines := splitLines(newData)
	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		return fmt.Sprintf("%s and %s differ; both are too large to diff here (%d and %d lines).",
			oldName, newName, len(oldLines), len(newLines)), true
	}

	ops := diffOps(oldLines, newLines)
	var out []string
	out = append(out, "--- "+oldName, "+++ "+newName)

	// Emit each run of changes with diffContext unchanged lines around it,
	// eliding the runs of unchanged lines between hunks.
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i - diffContext
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// Keep going while another change is within twice the context.
			next := end
			for next < len(ops) && next < end+2*diffContext && ops[next].kind == ' ' {
				next++
			}
			if next < len(ops) && ops[next].kind != ' ' {
				end = next
				continue
			}
			break
		}
		stop := end + diffContext
		if stop > len(ops) {
			stop = len(ops)
		}
		if start > 0 {
			out = append(out, "@@")
		}
		for _, op := range ops[start:stop] {
			out = append(out, string(op.kind)+op.line)
		}
		i = stop
	}

	truncated := false
	if budget > 0 && len(out) > budget {
		out = out[:budget]
		truncated = true
	}
	body := strings.Join(out, "\n")
	if truncated {
		body += fmt.Sprintf("\n... diff truncated at %d lines; read the vendored file for the rest.", budget)
	}
	return body, true
}

type diffOp struct {
	// kind is ' ', '-' or '+'.
	kind byte
	line string
}

// diffOps is the classic LCS walk. Deletions are emitted before insertions at
// the same position so a replaced line reads as one before the other.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// splitLines splits on newlines and drops the empty piece a trailing newline
// leaves behind, so a file and the same file differ only where their content
// does.
func splitLines(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// truncateLines bounds a rendered file to budget lines, saying so when it cut.
func truncateLines(text string, budget int) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) <= budget {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:budget], "\n") +
		fmt.Sprintf("\n... truncated at %d of %d lines; read the vendored file for the rest.", budget, len(lines))
}
