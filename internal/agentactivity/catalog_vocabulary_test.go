package agentactivity

import (
	"testing"

	"github.com/marcus/sidecar/internal/agentcatalog"
)

// The two vocabularies used to line up by hand. This makes it structural.
//
// agentcatalog names the providers Sidecar can start; this package names the
// provider it can see running in a pane. They were equal family for family, but
// nothing said so, and the cost of them drifting is no longer cosmetic: since
// td-11040b the process name is the evidence a hook's --kind claim is checked
// against, so a catalog family with no case in identifyProcessName is a family
// whose panes have no identity at all. That degrades rather than breaks — an
// unnamable occupant passes the gate instead of failing it — which is precisely
// why it would go unnoticed without this test.
func TestTheProcessNameVocabularyMatchesTheAgentCatalog(t *testing.T) {
	for _, family := range agentcatalog.Families() {
		t.Run(family.ID, func(t *testing.T) {
			if got := identifyProcessName(family.Command); got != family.ID {
				t.Fatalf("identifyProcessName(%q) = %q, want %q.\n"+
					"A catalog family whose launch command this resolver cannot name has panes with no\n"+
					"provider identity, so `agent report-session --kind %s` is never checked against the\n"+
					"pane. Add a case to identifyProcessName in activity.go.",
					family.Command, got, family.ID, family.ID)
			}
		})
	}
}

// Claude's version-string argv[0] is the one identity this resolver infers from
// a shape rather than a name, so no other family may ever present that shape.
func TestNoOtherCatalogFamilyLaunchesUnderAVersionShapedArgv0(t *testing.T) {
	for _, family := range agentcatalog.Families() {
		if family.ID == "claude" {
			continue
		}
		if claudeVersionArgv0(family.Command) {
			t.Fatalf("catalog family %q launches %q, which the resolver reads as Claude's version argv[0]; "+
				"its reports would be refused as the wrong provider", family.ID, family.Command)
		}
	}
}

func TestOnlyClaudesOwnVersionArgv0ResolvesToAProvider(t *testing.T) {
	// The trailing-space case is deliberate: tmux pads some command fields, and
	// identifyProcessName trims before matching.
	claude := []string{"claude", "2.0.14", "1.0.0", "10.20.30", " 1.2.3 "}
	for _, command := range claude {
		if got := identifyProcessName(command); got != "claude" {
			t.Errorf("identifyProcessName(%q) = %q, want claude", command, got)
		}
	}

	// Everything version-adjacent but not Claude's exact format stays unnamed.
	// Unnamed is the safe answer: VerifyReportedKind passes an unnamable
	// occupant and refuses a differently-named one, so a loose pattern here
	// turns into refused legitimate reports rather than wrong bindings.
	notClaude := []string{
		"v1.2.3",
		"1.2",
		"1.2.3.4",
		"1.2.3-beta",
		"claude 1.2.3",
		"node1.2", // a runtime with a version glued on is not a version
	}
	for _, command := range notClaude {
		if got := identifyProcessName(command); got != "" {
			t.Errorf("identifyProcessName(%q) = %q, want no identity", command, got)
		}
	}
}
