package agentactivity

import (
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
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

// upstreamAliases reads Herdr's `lookup_agent` table out of the vendored
// aliases.upstream.json, restricted to the families Sidecar claims today and
// re-keyed from Herdr's label to Sidecar's family id.
//
// It used to be a hand-copied Go literal. Driving it from the extracted file
// instead is the point of extracting the file: a sync that adds an alias for a
// family Sidecar claims now fails this test on the sync pull request, which is
// the moment somebody can act on it, rather than waiting for a user's pane to
// show no badge. Upstream families Sidecar does not claim are ignored on
// purpose — registering those is Phase 4, and failing here would only make the
// sync noisy in the meantime.
//
// Muse's `muse-bin-<version>` launcher spelling is not in the alias list because
// upstream matches it by shape (`is_muse_versioned_binary`); the extracted
// versioned_binary_prefixes table carries the prefix and one representative
// value stands in for the shape.
func upstreamAliases(t *testing.T) map[string][]string {
	t.Helper()
	table, err := manifests.LoadAliases()
	if err != nil {
		t.Fatalf("load aliases.upstream.json: %v", err)
	}
	out := make(map[string][]string, len(claimedFamilies))
	for _, family := range claimedFamilies {
		label := HerdrAgentLabel(family)
		aliases, ok := table.Agents[label]
		if !ok {
			t.Fatalf("upstream alias table has no entry for %q (Herdr label %q)", family, label)
		}
		out[family] = append([]string(nil), aliases...)
		if prefix, ok := table.VersionedBinaryPrefixes[label]; ok {
			out[family] = append(out[family], prefix+"0.1.0-R708.1")
		}
	}
	return out
}

// claimedFamilies are the ten providers Sidecar has screen detection for today.
var claimedFamilies = []string{
	"claude", "codex", "grok", "antigravity", "pi",
	"copilot", "cursor", "opencode", "amp", "muse",
}

// A pane running an agent under a spelling upstream knows and Sidecar does not
// has no provider identity at all: no state badge, and `agent report-session
// --kind` never checked against it. Herdr's alias table is the shared
// vocabulary, so every entry in it for a family Sidecar claims must resolve.
func TestUpstreamAliasesResolveForClaimedFamilies(t *testing.T) {
	for family, aliases := range upstreamAliases(t) {
		for _, alias := range aliases {
			t.Run(family+"/"+alias, func(t *testing.T) {
				if got := identifyProcessName(alias); got != family {
					t.Fatalf("identifyProcessName(%q) = %q, want %q (upstream alias for %s)",
						alias, got, family, family)
				}
			})
		}
	}
}

// Every claimed family is one Sidecar can launch, so the two tables must not
// drift apart in either direction.
func TestUpstreamAliasTableCoversEveryCatalogFamily(t *testing.T) {
	claimed := make(map[string]bool, len(claimedFamilies))
	for _, family := range claimedFamilies {
		claimed[family] = true
	}
	for _, family := range agentcatalog.Families() {
		if !claimed[family.ID] {
			t.Errorf("catalog family %q is not in claimedFamilies; add it there and to Supports, or it has no upstream alias record", family.ID)
		}
	}
}

// npm and Windows shims present the same program under a wrapper extension,
// and a pane's command can arrive path-qualified. Upstream folds both away
// before its alias lookup; so does this resolver, which is why the table above
// carries only bare names.
func TestLauncherSuffixAndPathSpellingsResolve(t *testing.T) {
	cases := map[string]string{
		"claude.cmd":                     "claude",
		"CLAUDE.EXE":                     "claude",
		"opencode.js":                    "opencode",
		"cursor-agent.cmd":               "cursor",
		"codex.ps1":                      "codex",
		"amp.bat":                        "amp",
		"/opt/homebrew/bin/opencode":     "opencode",
		`C:\Users\a\AppData\claude.cmd`:  "claude",
		"/usr/local/bin/cursor-agent/":   "cursor",
		"/bin/zsh":                       "shell",
		" /Users/a/.local/bin/grok-cli ": "grok",
	}
	for command, want := range cases {
		if got := identifyProcessName(command); got != want {
			t.Errorf("identifyProcessName(%q) = %q, want %q", command, got, want)
		}
	}
}

// The version-shaped argv[0] is matched before suffix stripping so that
// stripping can never manufacture Claude's shape out of something else.
func TestLauncherSuffixStrippingCannotManufactureClaudesVersionArgv0(t *testing.T) {
	for _, command := range []string{"1.2.3.js", "1.2.3.exe", "1.2.3.cmd", "1.2.3.bat", "1.2.3.ps1"} {
		if got := identifyProcessName(command); got != "" {
			t.Errorf("identifyProcessName(%q) = %q, want no identity", command, got)
		}
	}
}

// Herdr treats these as generic runtimes rather than agents; Sidecar must not
// name a provider for any of them either. Sidecar's "shell" bucket is
// deliberately narrower (see identifyProcessName), so the assertion is only
// that none of them resolves to a provider family.
func TestHerdrGenericRuntimesNeverResolveToAProvider(t *testing.T) {
	// src/detect/mod.rs:696 `is_generic_runtime_or_shell` at e2b85c7.
	runtimes := []string{
		"sh", "bash", "zsh", "fish", "tmux", "node", "bun", "cmd",
		"powershell", "pwsh", "python", "python3", "python3.12",
	}
	for _, runtime := range runtimes {
		if got := identifyProcessName(runtime); got != "" && got != "shell" {
			t.Errorf("identifyProcessName(%q) = %q, want no provider identity", runtime, got)
		}
	}
}
