package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// syncReport is what the workflow puts in a pull request body. It is written
// to report.md in the output directory and committed: it is small, it is the
// review surface for a sync, and a diff of it is the fastest way to see what
// changed upstream since the last one.
type syncReport struct {
	Ref        string
	ReleaseTag string
	Out        string
	StartedAt  time.Time
	FinishedAt time.Time

	Lock     *manifests.Lock
	Previous *manifests.Lock

	Aliases   *manifests.Aliases
	Authority *manifests.Authority

	Notes []string
	Body  string
}

func (r *syncReport) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vendored %d manifests from herdr %s into %s\n", len(r.Lock.Agents), r.Ref, r.Out)
	incompatible := 0
	for _, agent := range r.Lock.Agents {
		incompatible += len(agent.RegexIncompatibilities)
	}
	fmt.Fprintf(&b, "catalog etag %s; %d regex patterns need an RE2 rewrite\n", r.Lock.Catalog.ETag, incompatible)
	fmt.Fprintf(&b, "wrote %s\n", filepath.Join(r.Out, "report.md"))
	return b.String()
}

func (r *syncReport) render() string {
	var b strings.Builder
	lock := r.Lock

	b.WriteString("# Herdr detection sync report\n\n")
	fmt.Fprintf(&b, "Generated %s by `go run ./internal/tools/herdrsync`.\n\n", lock.GeneratedAt)

	b.WriteString("| Field | Value |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Herdr repository | %s |\n", lock.Herdr.Repository)
	fmt.Fprintf(&b, "| Ref vendored | `%s` |\n", lock.Herdr.Ref)
	if lock.Herdr.Commit != "" {
		fmt.Fprintf(&b, "| Commit | `%s` |\n", lock.Herdr.Commit)
	}
	fmt.Fprintf(&b, "| Pinned release for the differential harness | `%s` |\n", lock.Herdr.PinnedReleaseTag)
	fmt.Fprintf(&b, "| Catalog | %s |\n", lock.Catalog.URL)
	fmt.Fprintf(&b, "| Catalog ETag | `%s` |\n", lock.Catalog.ETag)
	fmt.Fprintf(&b, "| Sidecar manifest engine version | %d |\n", lock.EngineVersion)
	fmt.Fprintf(&b, "| Manifests vendored | %d |\n", len(lock.Agents))
	if lock.Herdr.SourceDir != "" {
		fmt.Fprintf(&b, "| Read from local checkout | `%s` |\n", lock.Herdr.SourceDir)
	}
	b.WriteString("\n")

	for _, note := range append(append([]string{}, lock.Notes...), r.Notes...) {
		fmt.Fprintf(&b, "> %s\n\n", note)
	}

	r.renderVersionChanges(&b)
	r.renderFileChanges(&b)
	r.renderSourceChoices(&b)
	r.renderRegex(&b)
	r.renderAliases(&b)
	r.renderAuthority(&b)
	r.renderIntegrationAssets(&b)
	r.renderFixtureFlips(&b)
	return b.String()
}

func (r *syncReport) renderVersionChanges(b *strings.Builder) {
	b.WriteString("## Version changes\n\n")
	if r.Previous == nil {
		b.WriteString("First sync: there is no previous lock to compare against.\n\n")
		return
	}
	var rows []string
	for _, agent := range r.Lock.Agents {
		before, ok := r.Previous.Agent(agent.ID)
		switch {
		case !ok:
			rows = append(rows, fmt.Sprintf("| `%s` | — | %s | added |", agent.ID, agent.Version))
		case manifest.CompareVersions(agent.Version, before.Version) > 0:
			rows = append(rows, fmt.Sprintf("| `%s` | %s | %s | bumped |", agent.ID, before.Version, agent.Version))
		case manifest.CompareVersions(agent.Version, before.Version) < 0:
			rows = append(rows, fmt.Sprintf("| `%s` | %s | %s | **rolled back** |", agent.ID, before.Version, agent.Version))
		}
	}
	for _, before := range r.Previous.Agents {
		if _, ok := r.Lock.Agent(before.ID); !ok {
			rows = append(rows, fmt.Sprintf("| `%s` | %s | — | removed upstream |", before.ID, before.Version))
		}
	}
	if len(rows) == 0 {
		b.WriteString("No manifest version changed since the previous lock.\n\n")
		return
	}
	b.WriteString("| Agent | Before | After | Change |\n| --- | --- | --- | --- |\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n")
}

func (r *syncReport) renderFileChanges(b *strings.Builder) {
	b.WriteString("## File changes\n\n")
	if r.Previous == nil {
		fmt.Fprintf(b, "%d files vendored for the first time.\n\n", len(r.Lock.Agents))
		return
	}
	changed, unchanged := 0, 0
	var lines []string
	for _, agent := range r.Lock.Agents {
		before, ok := r.Previous.Agent(agent.ID)
		if ok && before.SHA256 == agent.SHA256 {
			unchanged++
			continue
		}
		changed++
		lines = append(lines, fmt.Sprintf("- `%s` (%s, %d bytes, %d rules)", agent.Path, agent.Version, agent.Bytes, agent.RuleCount))
	}
	fmt.Fprintf(b, "%d file(s) changed, %d unchanged.\n\n", changed, unchanged)
	if len(lines) > 0 {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}
}

func (r *syncReport) renderSourceChoices(b *strings.Builder) {
	b.WriteString("## Published versus bundled\n\n")
	b.WriteString("Each row is the copy a Herdr client would load, and why.\n\n")
	b.WriteString("| Agent | Vendored from | Bundled | Published | Reason |\n| --- | --- | --- | --- | --- |\n")
	for _, agent := range r.Lock.Agents {
		published := agent.PublishedVersion
		if published == "" {
			published = "—"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n",
			agent.ID, agent.Source, agent.BundledVersion, published, agent.SourceReason)
	}
	b.WriteString("\n")
}

func (r *syncReport) renderRegex(b *strings.Builder) {
	b.WriteString("## Regex compatibility\n\n")
	total := 0
	var bad []string
	for _, agent := range r.Lock.Agents {
		for _, incompatible := range agent.RegexIncompatibilities {
			bad = append(bad, fmt.Sprintf("- `%s` rule `%s` %s: `%s`\n  - %s",
				agent.Path, incompatible.RuleID, incompatible.Field, incompatible.Pattern, incompatible.Error))
		}
		total += len(agent.RegexIncompatibilities)
	}
	if total == 0 {
		b.WriteString("Every vendored pattern compiles under Go's `regexp` (RE2).\n\n")
		return
	}
	fmt.Fprintf(b, "%d pattern(s) that Rust's `regex` crate accepts cannot compile under Go's RE2. "+
		"The vendored files keep them verbatim; an overlay carries the rewrite. "+
		"See `docs/reference/herdr-detection-parity.md`.\n\n", total)
	b.WriteString(strings.Join(bad, "\n"))
	b.WriteString("\n\n")
}

func (r *syncReport) renderAliases(b *strings.Builder) {
	b.WriteString("## Alias table\n\n")
	if r.Aliases == nil {
		b.WriteString("Alias extraction did not run.\n\n")
		return
	}
	missing := sidecarAliasGaps(r.Aliases)
	fmt.Fprintf(b, "%d agents in Herdr's `lookup_agent`; generic runtimes: %s (plus %s).\n\n",
		len(r.Aliases.Agents), strings.Join(backtickAll(r.Aliases.GenericRuntimes), ", "), r.Aliases.PythonRuntimeRule)
	if len(missing) == 0 {
		b.WriteString("Every Herdr alias for a family Sidecar already claims appears literally in `internal/agentactivity/activity.go`.\n\n")
		return
	}
	b.WriteString("Herdr aliases that do not appear literally in `internal/agentactivity/activity.go`. " +
		"This is a text scan, not an evaluation: a prefix rule such as `grok-` covers several of these. " +
		"The authoritative check is the alias-parity test in `internal/agentactivity`.\n\n")
	b.WriteString("| Agent | Alias |\n| --- | --- |\n")
	for _, gap := range missing {
		fmt.Fprintf(b, "| `%s` | `%s` |\n", gap.agent, gap.alias)
	}
	b.WriteString("\n")
}

type aliasGap struct{ agent, alias string }

// sidecarAliasGaps scans Sidecar's identifyProcessName source for each upstream
// alias as a literal. The tool cannot import internal/agentactivity without
// creating the very dependency the package layout forbids, so this stays a text
// scan and says so wherever it is rendered.
func sidecarAliasGaps(aliases *manifests.Aliases) []aliasGap {
	data, err := os.ReadFile(filepath.Join("internal", "agentactivity", "activity.go"))
	if err != nil {
		return nil
	}
	source := string(data)
	ids := make([]string, 0, len(aliases.Agents))
	for id := range aliases.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var gaps []aliasGap
	for _, id := range ids {
		// Only report families Sidecar already claims; the rest are Phase 4
		// coverage, not a parity gap in this phase.
		if !strings.Contains(source, `"`+id+`"`) {
			continue
		}
		for _, alias := range aliases.Agents[id] {
			if !strings.Contains(source, `"`+alias+`"`) {
				gaps = append(gaps, aliasGap{agent: id, alias: alias})
			}
		}
	}
	return gaps
}

func (r *syncReport) renderAuthority(b *strings.Builder) {
	b.WriteString("## Authority gaps\n\n")
	if r.Authority == nil {
		b.WriteString("Authority extraction did not run.\n\n")
		return
	}
	tiers := sidecarTiers()
	if tiers == nil {
		b.WriteString("`internal/agentlifecycle/capabilities.json` could not be read; no comparison made.\n\n")
		return
	}
	b.WriteString("Herdr's published authority is a *target*. Sidecar tiers are earned by traces and are never copied.\n\n")
	b.WriteString("| Agent | Herdr authority | Sidecar tier | Below target |\n| --- | --- | --- | --- |\n")
	ids := make([]string, 0, len(r.Authority.Agents))
	for id := range r.Authority.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		herdr := r.Authority.Agents[id].LifecycleAuthority
		tier, known := tiers[id]
		if !known {
			tier = "—"
		}
		gap := ""
		if authorityRank(herdr) > tierRank(tier) {
			gap = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", id, herdr, tier, gap)
	}
	b.WriteString("\n")
}

func authorityRank(authority string) int {
	switch authority {
	case manifests.AuthorityHooks:
		return 2
	case manifests.AuthoritySessionIdentity:
		return 1
	default:
		return 0
	}
}

func tierRank(tier string) int {
	switch tier {
	case "full":
		return 2
	case "session-identity":
		return 1
	default:
		return 0
	}
}

func sidecarTiers() map[string]string {
	data, err := os.ReadFile(filepath.Join("internal", "agentlifecycle", "capabilities.json"))
	if err != nil {
		return nil
	}
	var entries []struct {
		Provider string `json:"provider"`
		Tier     string `json:"tier"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	tiers := map[string]string{}
	for _, entry := range entries {
		// Sidecar spells Antigravity out; Herdr's agent id is agy.
		id := entry.Provider
		if id == "antigravity" {
			id = "agy"
		}
		if rank := tierRank(entry.Tier); rank > tierRank(tiers[id]) || tiers[id] == "" {
			tiers[id] = entry.Tier
		}
	}
	return tiers
}

func (r *syncReport) renderIntegrationAssets(b *strings.Builder) {
	b.WriteString("## Integration asset versions\n\n")
	if r.Authority == nil {
		b.WriteString("Authority extraction did not run.\n\n")
		return
	}
	b.WriteString("Vendoring the assets themselves is Phase 3; these are the `HERDR_INTEGRATION_VERSION` values " +
		"upstream carries today, so a bump is visible before the assets land here.\n\n")
	b.WriteString("| Agent | Asset directory | Version |\n| --- | --- | --- |\n")
	ids := make([]string, 0, len(r.Authority.Agents))
	for id, agent := range r.Authority.Agents {
		if agent.IntegrationVersion > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		agent := r.Authority.Agents[id]
		fmt.Fprintf(b, "| `%s` | `%s` | %d |\n", id, agent.IntegrationAssetDir, agent.IntegrationVersion)
	}
	b.WriteString("\n")
}

func (r *syncReport) renderFixtureFlips(b *strings.Builder) {
	b.WriteString("## Fixture verdict flips\n\n")
	b.WriteString("Engine not yet wired; see Phase 1.\n")
}

func backtickAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "`" + v + "`"
	}
	return out
}
