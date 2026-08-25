package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestLayoutPaneFlag_AcceptsTheDocumentedShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"file with two targets": `{"kind":"file","targets":["a.go:12","b.md"]}`,
		"issue":                 `{"kind":"issue","targets":["td-756c34"]}`,
		"diff working tree":     `{"kind":"diff"}`,
		"resource":              `{"kind":"resource","provider":"jira-work","targets":["CASH-1245"]}`,
		"shell":                 `{"kind":"shell","run":"make dev","name":"dev server"}`,
		"file at a cell":        `{"kind":"file","targets":["a.go"],"at":"2.1"}`,
	} {
		pane, code, msg := layoutPaneFlag(raw)
		if code != 0 {
			t.Errorf("%s: exit %d msg %q", name, code, msg)
			continue
		}
		if pane.Kind == "" {
			t.Errorf("%s: kind lost", name)
		}
	}
}

func TestLayoutPaneFlag_Refuses(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want string
	}{
		"not json":      {`{"kind":"file"`, "not valid JSON"},
		"unknown kind":  {`{"kind":"browser","targets":["x"]}`, "not one of"},
		"primary":       {`{"kind":"primary"}`, "cannot be opened"},
		"bad cell":      {`{"kind":"file","targets":["a.go"],"at":"2.x"}`, "grid cell"},
		"cell zero":     {`{"kind":"file","targets":["a.go"],"at":"0.1"}`, "grid cell"},
		"shell targets": {`{"kind":"shell","targets":["a.go"]}`, "run/type/name"},
		"no provider":   {`{"kind":"resource","targets":["CASH-1245"]}`, "provider"},
		"no target":     {`{"kind":"file"}`, "needs at least one target"},
		"empty kind":    {`{"targets":["a.go"]}`, "not one of"},
	} {
		_, code, msg := layoutPaneFlag(tc.raw)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (msg %q)", name, code, msg)
			continue
		}
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: msg %q does not mention %q", name, msg, tc.want)
		}
	}
}

func TestLayoutCellParsing(t *testing.T) {
	for raw, want := range map[string]panelayout.Cell{
		"2.1": {Col: 2, Row: 1},
		"3":   {Col: 3, Row: 1},
		"4.4": {Col: 4, Row: 4},
	} {
		got, ok := panelayout.ParseCell(raw)
		if !ok || got != want {
			t.Errorf("ParseCell(%q) = %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "0.1", "2.0", "a.b", "2.", ".1", "-1.2", "99999999999999999999"} {
		if got, ok := panelayout.ParseCell(raw); ok {
			t.Errorf("ParseCell(%q) accepted as %+v", raw, got)
		}
	}
}

func TestLayoutCommandsDeclareAgentDoc(t *testing.T) {
	layout := RootCommand().FindSubcommand("layout")
	if layout == nil {
		t.Fatal("no layout command registered")
	}
	for _, name := range []string{"get", "apply"} {
		sub := layout.FindSubcommand(name)
		if sub == nil {
			t.Fatalf("no layout %s subcommand", name)
		}
		if sub.Agent.Invocation == "" || sub.Agent.Summary == "" {
			t.Errorf("layout %s missing AgentDoc: %+v", name, sub.Agent)
		}
	}
}

// A get request carries no panes and an apply carries only descriptors; the
// payload the CLI writes is exactly the documented shape.
func TestLayoutPayloadWireShape(t *testing.T) {
	payload, err := json.Marshal(uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeApply,
		Panes: []uirequest.LayoutPane{
			{Kind: "file", Targets: []string{"a.go"}, At: "2.1"},
			{Kind: "shell", Run: "make dev"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mode":"apply"`, `"panes":[`, `"at":"2.1"`, `"run":"make dev"`} {
		if !strings.Contains(string(payload), want) {
			t.Errorf("payload missing %s: %s", want, payload)
		}
	}
}
