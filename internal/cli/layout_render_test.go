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
