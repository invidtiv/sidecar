package ui

import "testing"

func TestTruncateStartKeepsFilenameEnd(t *testing.T) {
	const path = "internal/plugins/workspace/plugin.go"
	if got := TruncateStart(path, 100); got != path {
		t.Fatalf("wide = %q", got)
	}
	if got := TruncateStart(path, 11); got != "…/plugin.go" {
		t.Fatalf("filename budget = %q, want …/plugin.go", got)
	}
	if got := TruncateStart("ab", 1); got != "…" {
		t.Fatalf("width 1 = %q", got)
	}
	if got := TruncateStart("ab", 0); got != "" {
		t.Fatalf("width 0 = %q", got)
	}
}
