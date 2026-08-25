package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderLayoutSketchAndTable(t *testing.T) {
	report := decodeLayoutReport(json.RawMessage(`{
	  "version": 1,
	  "surface": "shell:s",
	  "grid": {"columns":[
	    {"column":1,"panes":[{"cell":"1.1","kind":"primary"},{"cell":"1.2","kind":"shell","session":"sidecar-tp-x"}]},
	    {"column":2,"panes":[{"cell":"2.1","kind":"file","tabs":["README.md","docs/x.md"],"active":1},{"cell":"2.2","kind":"issue","tabs":["td-e9a089"]}]}
	  ]},
	  "caps": {"maxColumns":4,"maxRows":4,"liveLeaves":2}
	}`))

	sketch := renderLayoutSketch(report)
	for _, want := range []string{"primary", "shell", "sidecar-tp-x", "file", "docs/x.md", "issue"} {
		if !strings.Contains(sketch, want) {
			t.Errorf("sketch missing %q:\n%s", want, sketch)
		}
	}
	if !strings.Contains(sketch, "|") || !strings.Contains(sketch, "+") {
		t.Errorf("sketch drew no boxes:\n%s", sketch)
	}

	table := renderLayoutTable(report)
	for _, want := range []string{"CELL", "1.1", "primary", "2.1", "README.md, docs/x.md*", "td-e9a089", "caps 4x4"} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q:\n%s", want, table)
		}
	}

	nullReport := decodeLayoutReport(json.RawMessage(`{"version":1,"grid":null}`))
	if got := renderLayoutSketch(nullReport); !strings.Contains(got, "does not resolve to grid columns") {
		t.Errorf("null grid sketch = %q", got)
	}
}

// A short column must hold its horizontal place while a taller one keeps
// drawing, or every cell to its right slides left and the sketch stops
// describing the layout it claims to draw. This is the canonical shape —
// primary alone on the left, two stacked panes on the right.
func TestRenderLayoutSketchKeepsRaggedColumnsAligned(t *testing.T) {
	report := decodeLayoutReport(json.RawMessage(`{
	  "version": 1,
	  "grid": {"columns":[
	    {"column":1,"panes":[{"cell":"1.1","kind":"primary"}]},
	    {"column":2,"panes":[{"cell":"2.1","kind":"file","tabs":["README.md"]},{"cell":"2.2","kind":"issue","tabs":["td-756c34"]}]}
	  ]},
	  "caps": {"maxColumns":4,"maxRows":4,"liveLeaves":2}
	}`))

	lines := strings.Split(strings.TrimRight(renderLayoutSketch(report), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("sketch has %d lines, want 5 (one box left, two stacked right):\n%s", len(lines), strings.Join(lines, "\n"))
	}
	// The right column starts at a fixed offset and every one of its rows
	// begins there, including the rows the left column no longer reaches.
	rightAt := strings.Index(lines[0], "+- file")
	if rightAt <= 0 {
		t.Fatalf("no second column in the first line: %q", lines[0])
	}
	for i, line := range lines {
		if len([]rune(line)) > 0 && i >= 3 {
			// Rows 4 and 5 belong to the right column alone: they must be
			// indented to it, never flush left where column 1 was.
			if !strings.HasPrefix(line, strings.Repeat(" ", rightAt)) {
				t.Errorf("line %d is not indented to the right column (want %d spaces): %q", i, rightAt, line)
			}
		}
	}
	// Both of the right column's cells start at the same column.
	if got := strings.Index(lines[3], "|"); got != rightAt {
		t.Errorf("right column body starts at %d, want %d:\n%s", got, rightAt, strings.Join(lines, "\n"))
	}
	if got := strings.Index(lines[2], "+- issue"); got != rightAt {
		t.Errorf("right column's second header starts at %d, want %d:\n%s", got, rightAt, strings.Join(lines, "\n"))
	}
	// The left column closes where it ends and does not reappear below.
	if !strings.HasPrefix(lines[2], "+-") {
		t.Errorf("left column did not close on its own last line: %q", lines[2])
	}
	for _, want := range []string{"terminal", "README.md", "td-756c34"} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Errorf("sketch missing %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

// A resource pane's provider is what makes get → edit → apply a round trip:
// the spec grammar requires it, so the table has to show it.
func TestRenderLayoutTableShowsResourceProvider(t *testing.T) {
	report := decodeLayoutReport(json.RawMessage(`{
	  "version": 1,
	  "grid": {"columns":[
	    {"column":1,"panes":[{"cell":"1.1","kind":"primary"}]},
	    {"column":2,"panes":[{"cell":"2.1","kind":"resource","provider":"jira-work","tabs":["CASH-1245"]}]}
	  ]},
	  "caps": {"maxColumns":4,"maxRows":4,"liveLeaves":2}
	}`))
	table := renderLayoutTable(report)
	if !strings.Contains(table, "jira-work") {
		t.Errorf("table hides the provider a spec would need:\n%s", table)
	}
	if !strings.Contains(table, "SESSION/PROVIDER") {
		t.Errorf("table header does not name the provider column:\n%s", table)
	}
}
