package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/agentcatalog"
)

// Creation's pickers and Configuration's Agents page read the same table. These
// assertions are what keeps that true: Configuration cannot import this plugin
// (the plugin imports the app, and the app imports Configuration), so the
// catalog is the shared description and this test proves the pickers still
// agree with it.

func TestAgentPickersFollowCatalog(t *testing.T) {
	families := agentcatalog.Families()
	if len(AgentTypeOrder) != len(families)+1 {
		t.Fatalf("AgentTypeOrder has %d entries, catalog has %d families", len(AgentTypeOrder), len(families))
	}
	for i, family := range families {
		if got := AgentTypeOrder[i]; got != AgentType(family.ID) {
			t.Fatalf("AgentTypeOrder[%d] = %q, catalog says %q", i, got, family.ID)
		}
		if got := ShellAgentOrder[i+1]; got != AgentType(family.ID) {
			t.Fatalf("ShellAgentOrder[%d] = %q, catalog says %q", i+1, got, family.ID)
		}
		if got := AgentDisplayNames[AgentType(family.ID)]; got != family.Name {
			t.Fatalf("%s display name = %q, catalog says %q", family.ID, got, family.Name)
		}
		if got := AgentCommands[AgentType(family.ID)]; got != family.Command {
			t.Fatalf("%s command = %q, catalog says %q", family.ID, got, family.Command)
		}
	}
	if AgentTypeOrder[len(AgentTypeOrder)-1] != AgentNone {
		t.Fatal("worktree creation no longer offers None last")
	}
	if ShellAgentOrder[0] != AgentNone {
		t.Fatal("shell creation no longer offers None first")
	}
}

// The allowlist means the same thing in both places: a narrowed list is
// honoured in catalog order, and an empty or unrecognized one offers everything.
func TestAllowlistResolutionMatchesCatalog(t *testing.T) {
	cases := [][]string{
		nil,
		{"claude"},
		{"grok", "claude"},
		{"nonesuch"},
		{"claude", "claude"},
	}
	for _, allowlist := range cases {
		want := agentcatalog.Resolve(allowlist)
		got := resolveSelectableAgents(allowlist, AgentTypeOrder, false)
		if len(got) != len(want)+1 {
			t.Fatalf("allowlist %v resolved to %v, catalog says %v", allowlist, got, want)
		}
		for i, family := range want {
			if got[i] != AgentType(family.ID) {
				t.Fatalf("allowlist %v: picker %v disagrees with catalog %v", allowlist, got, want)
			}
		}
	}
}
