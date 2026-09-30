package main

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// The September 2026 docs put less-tested agents in the table, allow empty
// notes, and move Muse to the separate native-support list.
const integrationAgentsMDX = `
### Supported by Herdr

| Agent | Integration | Notes |
| --- | --- | --- |
| Claude Code | ` + "`claude`" + ` | |
| Codex | ` + "`codex`" + ` | |
| GitHub Copilot CLI | ` + "`copilot`" + ` | |
| Cursor Agent CLI | ` + "`cursor`" + ` | |
| OpenCode | ` + "`opencode`" + ` | also reports state |
| Pi | ` + "`pi`" + ` | also reports state |
| OMP | ` + "`omp`" + ` | state requires the integration |
| Droid | ` + "`droid`" + ` | |
| Devin CLI | ` + "`devin`" + ` | |
| Kimi Code CLI | ` + "`kimi`" + ` | also reports state |
| Kilo Code CLI | ` + "`kilo`" + ` | also reports state |
| Hermes Agent | ` + "`hermes`" + ` | |
| Qoder CLI | ` + "`qodercli`" + ` | |
| Qwen Code | ` + "`qwen`" + ` | |
| Letta Code | ` + "`letta`" + ` | CLI install only |
| MastraCode | ` + "`mastracode`" + ` | state requires the integration |
| Grok CLI | ` + "`grok`" + ` | |
| Antigravity CLI | ` + "`antigravity-cli`" + ` | |
| Amp | none | state only |
| Kiro CLI | none | state only |
| Maki | none | state only |
| Gemini CLI | none | state only, less tested |
| Cline | none | state only, less tested |

### Supported by the agent

These agents report their own state to Herdr. There is nothing to install.

- [Crush](https://github.com/charmbracelet/crush)
- Command Code
- Muse
- [Prime Agent](https://github.com/PrimeIntellect-ai/prime-agent)

## How status works

Further prose.
`

func TestExtractAuthorityFromIntegrationTable(t *testing.T) {
	src := stubAssets()
	src.files[authoritySource] = integrationAgentsMDX
	authority, err := stubAuthority(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(authority.Agents) != 24 {
		t.Fatalf("got %d detector agents, want 24", len(authority.Agents))
	}
	for _, id := range []string{"pi", "omp", "kimi", "kilo", "opencode", "mastracode"} {
		if got := authority.Agents[id].LifecycleAuthority; got != manifests.AuthorityHooks {
			t.Errorf("%s authority = %q, want hooks", id, got)
		}
	}
	for _, id := range []string{"claude", "codex", "copilot", "cursor", "droid", "devin", "hermes", "qodercli", "qwen", "letta", "grok", "agy"} {
		if got := authority.Agents[id].LifecycleAuthority; got != manifests.AuthoritySessionIdentity {
			t.Errorf("%s authority = %q, want session identity", id, got)
		}
	}
	for _, id := range []string{"amp", "kiro", "maki", "gemini", "cline", "muse"} {
		if got := authority.Agents[id].LifecycleAuthority; got != manifests.AuthorityNone {
			t.Errorf("%s authority = %q, want no Herdr integration", id, got)
		}
	}
	if got := authority.Agents["muse"]; got.StateAuthority != "agent self-report" || got.IntegrationRole != "native state reporting" {
		t.Errorf("Muse's native state source was lost: %+v", got)
	}
	if got := authority.Agents["claude"].IntegrationVersion; got != 9 {
		t.Errorf("Claude asset version = %d, want 9", got)
	}
}

func TestExtractAuthorityAcceptsLettaInLegacyTable(t *testing.T) {
	doc := strings.Replace(stubAgentsMDX, "| Amp |", "| Letta Code | screen manifest | session |\n| Amp |", 1)
	agents, err := parseAuthorityTable(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := agents["letta"].LifecycleAuthority; got != manifests.AuthoritySessionIdentity {
		t.Fatalf("Letta authority = %q, want session identity", got)
	}
}

func TestAuthorityTableRefusesUnrecognizedChanges(t *testing.T) {
	tests := map[string]string{
		"unknown header":             strings.Replace(integrationAgentsMDX, "| Integration | Notes |", "| Support | Notes |", 1),
		"extra column":               strings.Replace(integrationAgentsMDX, "| Claude Code | `claude` | |", "| Claude Code | `claude` | | extra |", 1),
		"unknown agent":              strings.Replace(integrationAgentsMDX, "| Claude Code |", "| Mystery Agent |", 1),
		"wrong integration identity": strings.Replace(integrationAgentsMDX, "`claude`", "`codex`", 1),
		"unknown notes":              strings.Replace(integrationAgentsMDX, "also reports state", "might report state", 1),
		"contradictory notes":        strings.Replace(integrationAgentsMDX, "| Amp | none | state only |", "| Amp | none | also reports state |", 1),
		"duplicate agent":            strings.Replace(integrationAgentsMDX, "| Codex | `codex` | |", "| Claude Code | `claude` | |", 1),
		"missing native section":     strings.Replace(integrationAgentsMDX, "### Supported by the agent", "### Native agents", 1),
		"unknown native agent":       strings.Replace(integrationAgentsMDX, "- Muse", "- Mystery Agent", 1),
		"duplicated native provider": strings.Replace(integrationAgentsMDX, "- Muse", "- Pi", 1),
		"unknown legacy authority":   strings.Replace(stubAgentsMDX, "screen manifest", "new source", 1),
		"unknown legacy role":        strings.Replace(stubAgentsMDX, "| session |", "| optional hooks |", 1),
		"missing legacy appendix":    strings.Replace(stubAgentsMDX, "Detected but less thoroughly tested:", "Other agents:", 1),
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuthorityTable(doc); err == nil {
				t.Fatal("accepted a changed upstream contract")
			}
		})
	}
}
