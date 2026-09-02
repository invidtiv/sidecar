package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
	"github.com/marcus/sidecar/internal/agentintegration"
)

// syncReport is what the workflow puts in a pull request body. It is written
// to report.md in the output directory and committed: it is small, it is the
// review surface for a sync, and a diff of it is the fastest way to see what
// changed upstream since the last one.
type syncReport struct {
	Ref            string
	ReleaseTag     string
	Out            string
	IntegrationOut string
	StartedAt      time.Time
	FinishedAt     time.Time

	Lock     *manifests.Lock
	Previous *manifests.Lock

	Aliases   *manifests.Aliases
	Authority *manifests.Authority

	// Integration is the lock for the vendored Herdr integration assets, and
	// PreviousIntegration is the one this sync replaced.
	Integration         *agentintegration.UpstreamLock
	PreviousIntegration *agentintegration.UpstreamLock
	// IntegrationDiffs is what changed upstream since each Sidecar port.
	IntegrationDiffs []integrationPortDiff

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
	if r.Integration != nil {
		files := len(r.Integration.Files)
		for _, provider := range r.Integration.Providers {
			files += len(provider.Files)
		}
		fmt.Fprintf(&b, "vendored %d integration assets for %d providers into %s\n",
			files, len(r.Integration.Providers), filepath.Join(r.IntegrationOut, "upstream"))
	}
	fmt.Fprintf(&b, "wrote %s\n", filepath.Join(r.Out, "report.md"))
	return b.String()
}

func (r *syncReport) render() (string, error) {
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
	if err := r.renderAliases(&b); err != nil {
		return "", err
	}
	r.renderAuthority(&b)
	r.renderIntegrationAssets(&b)
	r.renderFixtureFlips(&b)
	return b.String(), nil
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

func (r *syncReport) renderAliases(b *strings.Builder) error {
	b.WriteString("## Alias table\n\n")
	if r.Aliases == nil {
		b.WriteString("Alias extraction did not run.\n\n")
		return nil
	}
	missing, err := sidecarAliasGaps(r.Aliases)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "%d agents in Herdr's `lookup_agent`; generic runtimes: %s (plus %s).\n\n",
		len(r.Aliases.Agents), strings.Join(backtickAll(r.Aliases.GenericRuntimes), ", "), r.Aliases.PythonRuntimeRule)
	if len(missing) == 0 {
		b.WriteString("Every Herdr alias for a family Sidecar already claims appears literally in `internal/agentactivity/activity.go`.\n\n")
		return nil
	}
	b.WriteString("Herdr aliases that do not appear literally in `internal/agentactivity/activity.go`. " +
		"This is a text scan, not an evaluation: a prefix rule such as `grok-` covers several of these. " +
		"The authoritative check is the alias-parity test in `internal/agentactivity`.\n\n")
	b.WriteString("| Agent | Alias |\n| --- | --- |\n")
	for _, gap := range missing {
		fmt.Fprintf(b, "| `%s` | `%s` |\n", gap.agent, gap.alias)
	}
	b.WriteString("\n")
	return nil
}

type aliasGap struct{ agent, alias string }

// sidecarAliasGaps scans Sidecar's identifyProcessName source for each upstream
// alias as a literal. The tool cannot import internal/agentactivity without
// creating the very dependency the package layout forbids, so this stays a text
// scan and says so wherever it is rendered.
//
// A read failure is an error, not an empty result: "every alias appears" and
// "the file could not be read" look identical in the report, and the reassuring
// one is the wrong default.
func sidecarAliasGaps(aliases *manifests.Aliases) ([]aliasGap, error) {
	data, err := sidecarRepoFile(path.Join("internal", "agentactivity", "activity.go"))
	if err != nil {
		return nil, fmt.Errorf("alias gap scan: %w", err)
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
	return gaps, nil
}

// sidecarModule is this repository's module path, used to find its root.
const sidecarModule = "github.com/marcus/sidecar"

// sidecarRepoFile reads a file from the Sidecar repository the tool runs in,
// located by walking up from the working directory. The wrapper script runs the
// tool from the repository root, but `go test` runs it three levels down, and a
// path that only resolves from one directory is a check that quietly does
// nothing everywhere else.
func sidecarRepoFile(rel string) ([]byte, error) {
	root, err := sidecarRepoRoot()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
}

func sidecarRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if isModuleRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s go.mod found at or above the working directory", sidecarModule)
		}
		dir = parent
	}
}

func isModuleRoot(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest) == sidecarModule
		}
	}
	return false
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
	data, err := sidecarRepoFile(path.Join("internal", "agentlifecycle", "capabilities.json"))
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
	b.WriteString("## Integration assets\n\n")
	if r.Integration == nil {
		b.WriteString("Integration assets were not vendored.\n\n")
		return
	}
	fmt.Fprintf(b, "Vendored verbatim from `%s` into `%s/upstream/`, pinned by `upstream.lock.json` there. "+
		"They are reference material: Sidecar installs its own assets and these exist so a re-port is a diff.\n\n",
		r.Integration.Herdr.AssetsDir, filepath.ToSlash(r.IntegrationOut))

	ported := map[string]agentintegration.PortedFrom{}
	for _, record := range agentintegration.PortedFromRecords() {
		ported[record.UpstreamID] = record
	}

	b.WriteString("| Agent | Asset directory | Version | Previous | Change | Sidecar port |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	var bumps []agentintegration.UpstreamProvider
	for _, provider := range r.Integration.Providers {
		before, known := previousIntegrationVersion(r.PreviousIntegration, provider.ID)
		change := "unchanged"
		previous := fmt.Sprintf("%d", before)
		switch {
		case r.PreviousIntegration == nil:
			change, previous = "first sync", "—"
		case !known:
			change, previous = "added", "—"
			bumps = append(bumps, provider)
		case before < provider.Version:
			change = "**bumped**"
			bumps = append(bumps, provider)
		case before > provider.Version:
			change = "**rolled back**"
			bumps = append(bumps, provider)
		}
		port := "not ported"
		if record, ok := ported[provider.ID]; ok {
			port = fmt.Sprintf("`%s` from version %s", record.Provider, record.Version)
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %d | %s | %s | %s |\n",
			provider.ID, provider.Directory, provider.Version, previous, change, port)
	}
	b.WriteString("\n")

	// A provider Sidecar has not ported gets the bump alone: a heads-up that
	// the provider's hook payload changed, and nothing for anyone to do yet.
	var heads []string
	for _, provider := range bumps {
		if _, ok := ported[provider.ID]; ok {
			continue
		}
		before, known := previousIntegrationVersion(r.PreviousIntegration, provider.ID)
		if !known {
			heads = append(heads, fmt.Sprintf("- `%s` now ships an integration at version %d.", provider.ID, provider.Version))
			continue
		}
		heads = append(heads, fmt.Sprintf("- `%s` moved %d to %d. Sidecar has not ported it; the hook payload changed upstream.",
			provider.ID, before, provider.Version))
	}
	if len(heads) > 0 {
		b.WriteString("### Bumps for providers Sidecar has not ported\n\n")
		b.WriteString(strings.Join(heads, "\n"))
		b.WriteString("\n\n")
	}

	r.renderIntegrationPorts(b)
}

func previousIntegrationVersion(lock *agentintegration.UpstreamLock, id string) (int, bool) {
	if lock == nil {
		return 0, false
	}
	provider, ok := lock.Provider(id)
	if !ok {
		return 0, false
	}
	return provider.Version, true
}

// renderIntegrationPorts is the half of the section that costs a maintainer
// something: for each provider Sidecar has already ported, what upstream did to
// that asset since the version the port was written against.
func (r *syncReport) renderIntegrationPorts(b *strings.Builder) {
	b.WriteString("### Upstream changes since each Sidecar port\n\n")
	if len(r.IntegrationDiffs) == 0 {
		b.WriteString("Sidecar has ported no provider yet.\n\n")
		return
	}
	b.WriteString("`ported-from` is recorded in `internal/agentintegration/portedfrom.go`, not in an asset header: " +
		"two of the three Sidecar assets are Go values with no header to carry it. " +
		"A comparison is made on bytes rather than on the version number, so a file upstream edited " +
		"without bumping still shows here.\n\n")

	for _, entry := range r.IntegrationDiffs {
		fmt.Fprintf(b, "#### `%s` — ported from herdr `%s` version %s\n\n",
			entry.Ported.Provider, entry.Ported.UpstreamID, entry.Ported.Version)
		if entry.Ported.Commit != "" {
			fmt.Fprintf(b, "Compared against `%s`; upstream is now at version %d.\n\n",
				shortCommit(entry.Ported.Commit), entry.CurrentVersion)
		}
		if entry.Note != "" {
			fmt.Fprintf(b, "> %s\n\n", entry.Note)
		}
		changed := 0
		for _, file := range entry.Files {
			if file.Changed {
				changed++
			}
		}
		if changed == 0 && len(entry.Files) > 0 {
			fmt.Fprintf(b, "No upstream change: all %d file(s) are byte-identical to the copy this port was written against. "+
				"Nothing to re-port.\n\n", len(entry.Files))
			continue
		}
		for _, file := range entry.Files {
			if !file.Changed {
				continue
			}
			label := "diff"
			if file.Whole {
				label = "current upstream file"
			}
			fmt.Fprintf(b, "`%s` (%s):\n\n```diff\n%s\n```\n\n", file.Path, label, file.Body)
		}
	}
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
