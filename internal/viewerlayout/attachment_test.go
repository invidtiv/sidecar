package viewerlayout

import (
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/state"
	"strings"
	"testing"
)

func validAttachment() *state.PaneAttachmentJSON {
	return &state.PaneAttachmentJSON{Selector: "opaque selector whose syntax the server does not know", ExpectedTarget: mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "host", OwnerConfigGeneration: "cfg", WorkspaceID: "ws", WorkspaceKind: "shell", Session: "session", Pane: "%1", ServerIncarnation: "server", TargetGeneration: "generation"}}
}
func TestAttachmentValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*state.PaneLayoutJSON)
		valid  bool
	}{
		{name: "opaque", valid: true},
		{name: "selector bound", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.Selector = strings.Repeat("x", 513) }},
		{name: "selector boundary", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.Selector = strings.Repeat("x", 512) }, valid: true},
		{name: "JSON size bound", mutate: func(n *state.PaneLayoutJSON) {
			n.Attachment.ExpectedTarget.WorkspaceID = strings.Repeat("x", MaxAttachmentBytes)
		}},
		{name: "selector control", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.Selector = "opaque\x1b" }},
		{name: "identity control", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget.Pane = "%1\u0085" }},
		{name: "hub-scoped identity", mutate: func(n *state.PaneLayoutJSON) {
			n.Attachment.ExpectedTarget.OwnerHostID = "local:aerie"
			n.Attachment.ExpectedTarget.WorkspaceID = "local:aerie\x1fsidecar"
		}, valid: true},
		{name: "scope separator elsewhere", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget.Session = "a\x1fb" }},
		{name: "two scope separators", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget.WorkspaceID = "a\x1fb\x1fc" }},
		{name: "empty scope side", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget.WorkspaceID = "\x1fsidecar" }},
		{name: "other control in scoped key", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget.WorkspaceID = "aerie\x1fside\x1bcar" }},
		{name: "missing identity", mutate: func(n *state.PaneLayoutJSON) { n.Attachment.ExpectedTarget = mobileproto.TargetIdentity{} }},
		{name: "passive leaf", mutate: func(n *state.PaneLayoutJSON) { n.Kind = "doc"; n.Tabs = []state.PaneDocTabJSON{{Path: "README.md"}} }},
		{name: "no session", mutate: func(n *state.PaneLayoutJSON) { n.Session = "" }},
		{name: "split hint", mutate: func(n *state.PaneLayoutJSON) {
			n.Kind = ""
			n.Split = &state.PaneSplitJSON{Axis: "cols", Ratio: 50, A: &state.PaneLayoutJSON{Kind: "terminal"}, B: &state.PaneLayoutJSON{Kind: "terminal"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &state.PaneLayoutJSON{Kind: "terminal", Session: "session", Attachment: validAttachment()}
			if tc.mutate != nil {
				tc.mutate(n)
			}
			err := Validate(Document{Layout: n})
			if (err == nil) != tc.valid {
				t.Fatalf("valid %v got %v", tc.valid, err)
			}
		})
	}
}
