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

	// Manifests is the vendored bytes this sync wrote and PreviousManifests is
	// what it replaced, both keyed by file base. The old bytes are captured
	// before the tree is overwritten, because a sync writes in place and there
	// is no second copy afterwards.
	Manifests         map[string][]byte
	PreviousManifests map[string][]byte

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

	comparison, err := r.compareCorpus()
	if err != nil {
		return "", err
	}
	comparison.renderFixtureFlips(&b)
	comparison.renderOverlayRules(&b)
	return boundReport(b.String()), nil
}

// maxReportChars is GitHub's cap on a pull request body, which is what
// report.md becomes. A run that overflows it is a run whose report would be
// truncated by GitHub silently, in the middle of whatever section happened to
// be last; truncating here instead costs the same content and says so.
const maxReportChars = 65536

// boundReport trims the rendered report to something GitHub will accept as a
// pull request body, cutting at a line boundary and saying what it did.
func boundReport(body string) string {
	if len(body) <= maxReportChars {
		return body
	}
	notice := "\n> The rest of this report was truncated: it exceeded the %d-character limit on a " +
		"GitHub pull request body. Regenerate it with `go run ./internal/tools/herdrsync` to read it in full.\n"
	keep := maxReportChars - len(fmt.Sprintf(notice, maxReportChars))
	cut := body[:keep]
	if at := strings.LastIndexByte(cut, '\n'); at > 0 {
		cut = cut[:at+1]
	}
	return cut + fmt.Sprintf(notice, maxReportChars)
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

// corpusComparison is the fixture corpus run against both sides of the sync,
// with the overlays that a sync never touches applied to both. It backs the two
// sections a reviewer actually reads before merging.
type corpusComparison struct {
	fixtures []corpusFixture
	before   *corpusSide
	after    *corpusSide
	rules    []overlayRule
	exempt   map[string]bool
	// firstSync records that there were no vendored manifests to compare
	// against, which is a different report from "nothing changed".
	firstSync bool
	// setup is why the comparison could not be made at all, when it could not.
	setup string
}

// compareCorpus loads the fixtures, the overlays, and both sets of manifest
// bytes.
//
// A failure to read the corpus or the overlays is an error rather than an empty
// comparison, for the reason sidecarAliasGaps gives: "no fixture changed
// verdict" is the answer a reviewer merges on, and it must never be what a
// missing directory renders as.
func (r *syncReport) compareCorpus() (*corpusComparison, error) {
	fixtures, err := loadCorpus()
	if err != nil {
		return nil, err
	}
	overlays, err := readSidecarOverlays()
	if err != nil {
		return nil, fmt.Errorf("read Sidecar overlays: %w", err)
	}
	out := &corpusComparison{
		fixtures:  fixtures,
		after:     newCorpusSide(r.Manifests, overlays),
		before:    newCorpusSide(r.PreviousManifests, overlays),
		exempt:    harnessExempt(overlays),
		firstSync: len(r.PreviousManifests) == 0,
	}
	rules, err := overlayRules(overlays, r.Manifests)
	if err != nil {
		// An overlay that no longer parses is a finding, not a reason to fail
		// the sync: the vendored bytes are already written and correct, and the
		// report is what tells the maintainer which file to fix.
		out.setup = err.Error()
		return out, nil
	}
	out.rules = rules
	return out, nil
}

func (c *corpusComparison) renderFixtureFlips(b *strings.Builder) {
	b.WriteString("## Fixture verdict flips\n\n")
	b.WriteString("Every fixture in `internal/agentactivity/testdata` with a `screen:` block, " +
		"classified against the manifests this sync replaced and against the ones it wrote. " +
		"A verdict is the state, the matched rule id, and the fallback reason: the same triple " +
		"`scripts/herdr-diff.sh` compares. The Sidecar overlays are applied to **both** sides, " +
		"because a sync never touches them and applying them to one side would report every " +
		"overlay rule as a flip. Sidecar's process gate is not applied: it reads the pane's " +
		"process name and never the manifest, so its answer is the same on both sides and it " +
		"cannot create or hide a flip.\n\n")

	type flipRow struct {
		fixture       corpusFixture
		before, after corpusVerdict
		beforeMissing bool
		afterMissing  bool
	}

	evaluated := 0
	var flips []flipRow
	var unevaluable []string
	for _, fixture := range c.fixtures {
		after, afterOK, afterProblem := c.after.verdict(fixture)
		before, beforeOK, beforeProblem := c.before.verdict(fixture)

		if !afterOK && afterProblem == "" && !beforeOK && beforeProblem == "" {
			// Neither side vendors a manifest for this agent. Nothing about the
			// sync moved, but a fixture nothing classifies is worth naming.
			unevaluable = append(unevaluable,
				fmt.Sprintf("- `%s` has no vendored `%s.toml` on either side of this sync.",
					fixture, fixture.base))
			continue
		}
		if afterProblem != "" || beforeProblem != "" {
			problem := afterProblem
			side := "after"
			if problem == "" {
				problem, side = beforeProblem, "before"
			}
			unevaluable = append(unevaluable,
				fmt.Sprintf("- `%s` could not be evaluated (%s this sync): %s", fixture, side, problem))
			continue
		}
		evaluated++
		if c.firstSync {
			continue
		}
		switch {
		case !beforeOK:
			flips = append(flips, flipRow{fixture: fixture, after: after, beforeMissing: true})
		case !afterOK:
			flips = append(flips, flipRow{fixture: fixture, before: before, afterMissing: true})
		case !before.sameVerdict(after):
			flips = append(flips, flipRow{fixture: fixture, before: before, after: after})
		}
	}

	switch {
	case c.firstSync:
		fmt.Fprintf(b, "First sync: the output directory held no vendored manifests before this run, "+
			"so there is nothing to compare against. %d fixture(s) were classified against the new manifests.\n\n",
			evaluated)
	case len(flips) == 0:
		fmt.Fprintf(b, "**No fixture changed verdict.** %d fixture(s) reach the same state, "+
			"the same matched rule and the same fallback reason under the new manifests as under the old ones. "+
			"That is the expected result and it is what the review gate is for.\n\n", evaluated)
	default:
		fmt.Fprintf(b, "**%d of %d fixture(s) changed verdict.** Each row is a screen Sidecar now reads "+
			"differently. Read the manifest diff above for the rule that moved, and decide per row whether "+
			"the new verdict is the better one.\n\n", len(flips), evaluated)
		b.WriteString("| Agent | Fixture | Before | After |\n| --- | --- | --- | --- |\n")
		shown := flips
		if len(shown) > maxFlipRows {
			shown = shown[:maxFlipRows]
		}
		for _, row := range shown {
			before, after := row.before.label(), row.after.label()
			if row.beforeMissing {
				before = "— (manifest added this sync)"
			}
			if row.afterMissing {
				after = "— (**manifest no longer vendored**)"
			}
			fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n", row.fixture.agent, row.fixture.name, before, after)
		}
		b.WriteString("\n")
		if len(flips) > len(shown) {
			fmt.Fprintf(b, "%d further flip(s) are not listed; the table is capped at %d rows to keep this "+
				"report inside GitHub's pull request body limit. Run `go test ./internal/agentactivity/ "+
				"-run TestFixtureCensus -v` after merging for the full classification.\n\n",
				len(flips)-len(shown), maxFlipRows)
		}
	}

	if len(unevaluable) > 0 {
		b.WriteString("Fixtures no verdict could be minted for:\n\n")
		b.WriteString(strings.Join(unevaluable, "\n"))
		b.WriteString("\n\n")
	}
}

// renderOverlayRules is the plan's "overlay changes nothing" signal: for each
// rule in each internal/agentactivity/manifests/sidecar/<agent>.toml, whether
// removing that one rule changes any fixture verdict.
//
// A rule that changes nothing is how a Sidecar rule gets retired once upstream
// adopts the same idea, which is the second journey the plan asks for. It is a
// finding for the maintainer to act on and not something this tool acts on:
// deleting an overlay rule is a separate decision with a fixture attached.
func (c *corpusComparison) renderOverlayRules(b *strings.Builder) {
	b.WriteString("## Overlay rules\n\n")
	if c.setup != "" {
		fmt.Fprintf(b, "The overlays could not be read: %s\n\n", c.setup)
		return
	}
	if len(c.rules) == 0 {
		b.WriteString("No Sidecar overlay rules are vendored.\n\n")
		return
	}
	b.WriteString("Each rule is removed on its own from the manifests this sync wrote, and the corpus " +
		"is reclassified. A rule that changes no verdict has stopped earning its place, which is the " +
		"signal that upstream has adopted the same idea and the rule can go. Redundancy is judged on the " +
		"state and the fallback reason alone, never on the rule id: a `sidecar.` id can never equal the " +
		"upstream id that would win without it, so folding the id in would make the check unreachable.\n\n")
	b.WriteString("A rule carrying an **upstream** id is not in that bucket and is never a deletion " +
		"candidate. It replaces upstream's rule rather than adding one, so removing it leaves a rule that " +
		"is dead (the `\\p{Alphabetic}` rewrites RE2 cannot compile) or differently flagged (the copies " +
		"that only add `visible_blocker`), which is a regression rather than a cleanup. Those rows are " +
		"judged on the matched rule id and the visible flags as well, and say when no fixture covers " +
		"them at all.\n\n")

	b.WriteString("| Overlay | Rule | Kind | Effect |\n| --- | --- | --- | --- |\n")
	candidates := 0
	uncovered := 0
	for _, rule := range c.rules {
		kind := "addition"
		switch {
		case rule.disables:
			kind = "disables upstream"
		case rule.rewrite:
			kind = "replaces upstream"
		}

		fixtures := c.fixturesFor(rule.base)
		if len(fixtures) == 0 {
			fmt.Fprintf(b, "| `%s` | `%s` | %s | no fixture for this agent |\n", rule.base, rule.id, kind)
			uncovered++
			continue
		}
		without := c.after.without(rule.base, rule.id)
		var changed []string
		// instead names what reports on a fixture this rule currently wins,
		// once the rule is gone. It is what turns "changes nothing" from a
		// verdict into a next step: an upstream id there means upstream has
		// adopted the idea and the rule can go, and a `sidecar.` id means a
		// sibling overlay rule already covers the only fixture that exercises
		// this one.
		matched := 0
		var instead []string
		broken := ""
		for _, fixture := range fixtures {
			with, ok, problem := c.after.verdict(fixture)
			if problem != "" || !ok {
				broken = problem
				break
			}
			bare, ok, problem := without.verdict(fixture)
			if problem != "" || !ok {
				broken = problem
				break
			}
			same := with.sameBadge(bare)
			if rule.rewrite || rule.disables {
				same = with.sameEvidence(bare)
			}
			if !same {
				changed = append(changed, fixture.name)
			}
			if with.rule == rule.id {
				matched++
				if same {
					instead = appendUnique(instead, bare.label())
				}
			}
		}

		effect := ""
		switch {
		case broken != "":
			effect = "could not be judged: " + broken
		case len(changed) > 0:
			names := changed
			if len(names) > maxFixtureNamesPerRule {
				names = append(append([]string{}, names[:maxFixtureNamesPerRule]...),
					fmt.Sprintf("and %d more", len(changed)-maxFixtureNamesPerRule))
			}
			effect = fmt.Sprintf("changes %d fixture(s): %s", len(changed), strings.Join(backtickAll(names), ", "))
		case rule.rewrite || rule.disables:
			if matched > 0 {
				effect = "matches, but upstream's own rule reaches the same verdict and flags without it"
				break
			}
			effect = "no fixture covers it; upstream's own rule stands without it"
			uncovered++
		case c.exempt[rule.base+":"+rule.id]:
			// Declared by the overlay itself with a `# harness-exempt:` line,
			// and honoured with the same file-scoped key scripts/herdr-diff.sh
			// uses, because a rule id is not unique across agents.
			effect = "changes nothing, and the overlay declares it `harness-exempt`: " +
				"no fixture holds the screen it is for, and its case is a Go test"
		case matched == 0:
			effect = "**no fixture matches this rule**; nothing here proves what it is for"
			candidates++
		default:
			effect = fmt.Sprintf("**changes nothing: deletion candidate**; without it %s", strings.Join(instead, ", "))
			candidates++
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n", rule.base, rule.id, kind, effect)
	}
	b.WriteString("\n")

	switch {
	case candidates > 0:
		fmt.Fprintf(b, "%d overlay rule(s) changed no fixture verdict. Delete the rule, or record why it "+
			"stays and add the fixture that proves it. Deleting one is a separate change with a fixture "+
			"attached, so this report flags it rather than making it.\n\n", candidates)
	case uncovered > 0:
		fmt.Fprintf(b, "Every overlay addition still changes a verdict. %d row(s) are replacements or "+
			"disables no fixture exercises, which is a coverage gap rather than a deletion candidate.\n\n", uncovered)
	default:
		b.WriteString("Every overlay rule still changes a fixture verdict.\n\n")
	}
}

// fixturesFor returns the fixtures classified by one vendored manifest.
func (c *corpusComparison) fixturesFor(base string) []corpusFixture {
	var out []corpusFixture
	for _, fixture := range c.fixtures {
		if fixture.base == base {
			out = append(out, fixture)
		}
	}
	return out
}

// appendUnique keeps a short list of distinct labels in first-seen order, so a
// rule whose fixtures all fall back to the same replacement names it once.
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func backtickAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "`" + v + "`"
	}
	return out
}
