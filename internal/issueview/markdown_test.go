package issueview

import (
	"strings"
	"testing"
)

func TestFormatMarkdownIssue(t *testing.T) {
	got := FormatMarkdown(sample())
	for _, want := range []string{
		"# Extract the issue preview into a component",
		"**ID:** `td-abc123`",
		"**Type:** task | **Priority:** P1 | **Status:** in_progress",
		"**Parent:** `td-parent1`",
		"## Description",
		"The modal owns fetch and render today.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Tasks") {
		t.Fatalf("a non-epic included an epic task list:\n%s", got)
	}
}

func TestFormatMarkdownEpicIncludesChildren(t *testing.T) {
	d := sample()
	d.Type = "epic"
	got := FormatMarkdown(d)
	for _, want := range []string{
		"# Epic: Extract the issue preview into a component",
		"**ID:** `td-abc123`",
		"## Tasks",
		"### [x] Snapshot diff",
		"**ID:** `td-83cfc9`",
		"### [ ] Card gutter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("epic markdown missing %q:\n%s", want, got)
		}
	}
}

func TestFormatMarkdownNil(t *testing.T) {
	if got := FormatMarkdown(nil); got != "" {
		t.Fatalf("nil data = %q", got)
	}
}
