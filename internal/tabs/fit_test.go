package tabs

import (
	"strings"
	"testing"
)

func TestFitPathKeepsFilenameAndClamps(t *testing.T) {
	const path = "internal/plugins/workspace/plugin.go"
	if short := "a.go"; FitPath(short, 100) != short {
		t.Fatalf("short path was truncated: %q", FitPath(short, 100))
	}
	got := FitPath(path, 80)
	if got == path {
		t.Fatalf("wide leftover still drew the full path: %q", got)
	}
	if !strings.HasPrefix(got, "…/") || !strings.HasSuffix(got, "plugin.go") {
		t.Fatalf("clamped path = %q, want …/…/plugin.go", got)
	}
	if strings.Contains(got, "internal/plugins") {
		t.Fatalf("clamped path still starts at the repo root: %q", got)
	}
	narrow := FitPath(path, 12)
	if narrow != "…/plugin.go" {
		t.Fatalf("narrow = %q, want …/plugin.go", narrow)
	}
}

func TestFitEndKeepsLeadingID(t *testing.T) {
	title := "td-abc123: A headline that will not fit"
	got := FitEnd(title, 14)
	if !strings.HasPrefix(got, "td-abc123") {
		t.Fatalf("end-truncate dropped the id: %q", got)
	}
	if strings.Contains(got, "will not fit") {
		t.Fatalf("end-truncate kept the tail: %q", got)
	}
}
