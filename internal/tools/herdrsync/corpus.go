package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// The third comparison in this repository, and the only one that asks about the
// sync itself.
//
//   - scripts/herdr-diff.sh asks "does Sidecar's engine reach the verdict
//     Herdr's engine reaches on the same bytes?" Two engines, one manifest.
//   - TestFixtureCensus asks "does every fixture still classify as its header
//     says?" One engine, one manifest.
//   - This file asks "did *this sync* change what Sidecar says?" One engine,
//     two manifests: the bytes the vendored tree held before the sync
//     overwrote it, and the bytes it wrote. That is the review gate the plan's
//     first journey is about — a maintainer reads the manifest diff and a table
//     of the fixtures whose verdict moved, and merges when the table is empty.
//
// The Sidecar overlays are applied to *both* sides. They are not touched by a
// sync, so applying them to one side only would report every overlay rule as a
// flip and drown the upstream change the table exists to show.

const (
	// corpusDir is the fixture corpus, relative to the repository root.
	corpusDir = "internal/agentactivity/testdata"

	// maxFlipRows bounds the verdict-flip table. report.md is the body of the
	// sync pull request and GitHub caps that at 65536 characters; a sync that
	// moved every fixture is a review that needs the first screenful and the
	// count, not 61 rows.
	maxFlipRows = 60

	// maxFixtureNamesPerRule bounds the fixture list an overlay rule's row
	// carries, for the same reason.
	maxFixtureNamesPerRule = 6
)

// corpusFixture is one screen fixture: an observation plus which vendored
// manifest classifies it.
type corpusFixture struct {
	// agent is the fixture directory, which is a Sidecar provider family.
	agent string
	// name is the file name.
	name string
	// base is the vendored manifest file this fixture is evaluated against,
	// without the .toml.
	base string

	input manifest.Input
}

func (f corpusFixture) String() string { return f.agent + "/" + f.name }

// corpusVerdict is one evaluation reduced to what a reviewer compares.
//
// The first three fields are the triple scripts/herdr-diff.sh diffs — state,
// matched rule id, fallback reason — and using the same one keeps the two
// comparisons legible side by side. The visible flags are carried for the
// overlay-redundancy check alone, which needs them: two of the overlay rules
// copy an upstream rule verbatim to add visible_blocker, and on the triple
// alone they would be indistinguishable from a rule that does nothing.
type corpusVerdict struct {
	state    string
	rule     string
	fallback string

	visibleIdle    bool
	visibleWorking bool
	visibleBlocker bool
}

// sameVerdict is the flip test: the triple, exactly as the differential
// harness compares it.
func (v corpusVerdict) sameVerdict(other corpusVerdict) bool {
	return v.state == other.state && v.rule == other.rule && v.fallback == other.fallback
}

// sameBadge is the redundancy test for a `sidecar.` rule: the state and the
// fallback reason, and deliberately not the rule id.
//
// The id is left out because a `sidecar.` id can never equal the upstream id
// that would win without the rule, so folding it in would make "this rule
// changes nothing" unreachable and silently retire the signal. That was a real
// defect in scripts/herdr-diff.sh, found in the Phase 2 review; see
// docs/reference/herdr-detection-parity.md ("Differential harness").
func (v corpusVerdict) sameBadge(other corpusVerdict) bool {
	return v.state == other.state && v.fallback == other.fallback
}

// sameEvidence is the test for an overlay rule carrying an *upstream* id: the
// whole triple plus the visible flags.
//
// The id is included here and excluded from sameBadge, and the asymmetry is the
// point. Such a rule replaces upstream's rule rather than adding one, so it is
// never a deletion candidate — removing it leaves a dead or differently-flagged
// upstream rule, not an absent one — and including the id therefore cannot make
// anything unreachable. What it does buy is accuracy: a rule that changes which
// upstream rule reports without changing the badge is covered by a fixture, and
// saying "no fixture covers it" about one would send a reviewer to write a
// fixture that already exists. The flags are in for the two overlay rules that
// copy an upstream rule verbatim to add visible_blocker, which nothing else
// here can see.
func (v corpusVerdict) sameEvidence(other corpusVerdict) bool {
	return v.sameVerdict(other) &&
		v.visibleIdle == other.visibleIdle &&
		v.visibleWorking == other.visibleWorking &&
		v.visibleBlocker == other.visibleBlocker
}

// label renders a verdict the way the census and the differential harness
// render one: the state, and the rule that said so, with a fallback shown in
// parentheses because it is a policy rather than a rule.
func (v corpusVerdict) label() string {
	rule := v.rule
	if rule == "" {
		reason := v.fallback
		if reason == "" {
			reason = "no rule matched"
		}
		rule = "(" + reason + ")"
	}
	return v.state + " via `" + rule + "`"
}

// manifestFileBase maps a fixture directory — a Sidecar provider family — to
// the vendored manifest file that classifies it.
//
// It is agentactivity.ManifestAgentID, copied rather than imported: this tool
// writes the tree that package reads, and depending on the consumer to vendor
// for it is the wrong direction. The copy is small and it is pinned by
// TestTheCorpusMapsEveryFixtureDirectoryToItsManifest, which does import the
// package, so a third spelling cannot appear without a test failing.
func manifestFileBase(agent string) string {
	if agent == "copilot" {
		return "github-copilot"
	}
	return agent
}

// loadCorpus reads every fixture with a screen: block.
//
// A read failure is an error rather than an empty corpus, for the reason
// sidecarAliasGaps gives: "no fixture changed verdict" and "the corpus could
// not be read" render identically, and the reassuring one is the wrong default.
func loadCorpus() ([]corpusFixture, error) {
	root, err := sidecarRepoRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(corpusDir))
	agents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture corpus: %w", err)
	}

	var out []corpusFixture
	for _, entry := range agents {
		// proof/ holds run transcripts, not captures.
		if !entry.IsDir() || entry.Name() == "proof" {
			continue
		}
		agent := entry.Name()
		files, err := os.ReadDir(filepath.Join(dir, agent))
		if err != nil {
			return nil, fmt.Errorf("read fixture corpus: %w", err)
		}
		names := make([]string, 0, len(files))
		for _, file := range files {
			names = append(names, file.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			if filepath.Ext(name) != ".txt" {
				continue
			}
			// process_identity.txt and availability.txt record environment
			// facts, not screens.
			if name == "process_identity.txt" || name == "availability.txt" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, agent, name))
			if err != nil {
				return nil, fmt.Errorf("read fixture corpus: %w", err)
			}
			head, screen, found := strings.Cut(string(data), "screen:\n")
			if !found {
				// A fixture with no screen block is skipped, exactly as the
				// differential harness and the census skip it.
				continue
			}
			fixture := corpusFixture{
				agent: agent,
				name:  name,
				base:  manifestFileBase(agent),
				input: manifest.Input{Screen: screen},
			}
			for _, line := range strings.Split(head, "\n") {
				key, value, ok := strings.Cut(line, ": ")
				if !ok {
					continue
				}
				switch strings.TrimSpace(key) {
				case "pane_title":
					fixture.input.Title = value
				case "pane_height":
					fixture.input.Rows, _ = strconv.Atoi(strings.TrimSpace(value))
				}
			}
			out = append(out, fixture)
		}
	}
	return out, nil
}

// corpusSide is one set of vendored manifest bytes plus the Sidecar overlays,
// compiled on demand and reused across the fixtures that share a manifest.
//
// Sidecar's process gate is deliberately not applied. It is
// agentactivity's, it reads the fixture's pane_current_command and never the
// manifest, so its answer is identical on both sides of every comparison here
// and it can neither create nor hide a flip. What it would do is drop the six
// deliberate false-positive fixtures from the table, and those are exactly the
// screens a reviewer wants to see a rule start matching on. Every fixture is
// therefore evaluated as a bare manifest, the way the census evaluates kiro and
// qodercli, and the report says so.
type corpusSide struct {
	// upstream is the vendored manifest bytes keyed by file base.
	upstream map[string][]byte
	// overlays is internal/agentactivity/manifests/sidecar keyed the same way.
	// A sync never writes these, so both sides share one map.
	overlays map[string][]byte

	// dropBase and dropRule leave one overlay rule out, which is how the same
	// evaluator answers the redundancy question as well as the flip question.
	dropBase string
	dropRule string

	loaded map[string]loadedSide
}

// loadedSide is one compiled manifest or the reason there is none. Present
// separates "upstream does not ship this file" from "it would not compile",
// which are different pieces of news and lead to different fixes.
type loadedSide struct {
	compiled *manifest.Compiled
	present  bool
	problem  string
}

func newCorpusSide(upstream, overlays map[string][]byte) *corpusSide {
	return &corpusSide{
		upstream: upstream,
		overlays: overlays,
		loaded:   map[string]loadedSide{},
	}
}

// without returns the same side with one overlay rule left out.
func (s *corpusSide) without(base, rule string) *corpusSide {
	out := newCorpusSide(s.upstream, s.overlays)
	out.dropBase, out.dropRule = base, rule
	return out
}

// manifestFor compiles one vendored file with its overlay, once per side. The
// result distinguishes a file this side does not carry from one that would not
// compile, which is how a manifest upstream added or dropped stays visible in
// the report instead of quietly vanishing from it.
func (s *corpusSide) manifestFor(base string) loadedSide {
	if entry, ok := s.loaded[base]; ok {
		return entry
	}
	data, ok := s.upstream[base]
	if !ok {
		s.loaded[base] = loadedSide{}
		return s.loaded[base]
	}
	drop := ""
	if base == s.dropBase {
		drop = s.dropRule
	}
	compiled, err := compileWithOverlay(data, s.overlays[base], drop)
	if err != nil {
		s.loaded[base] = loadedSide{present: true, problem: err.Error()}
		return s.loaded[base]
	}
	s.loaded[base] = loadedSide{compiled: compiled, present: true}
	return s.loaded[base]
}

// verdict evaluates one fixture. The bool reports whether this side had a
// manifest to evaluate it with; the string carries why it could not be
// compiled, when that is the reason.
func (s *corpusSide) verdict(f corpusFixture) (corpusVerdict, bool, string) {
	entry := s.manifestFor(f.base)
	if !entry.present || entry.problem != "" {
		return corpusVerdict{}, false, entry.problem
	}
	compiled := entry.compiled
	v := compiled.Evaluate(f.input)
	out := corpusVerdict{
		state:          string(v.State),
		fallback:       v.FallbackReason,
		visibleIdle:    v.VisibleIdle,
		visibleWorking: v.VisibleWorking,
		visibleBlocker: v.VisibleBlocker,
	}
	if v.MatchedRule != nil {
		out.rule = v.MatchedRule.ID
	}
	return out, true, ""
}

// compileWithOverlay is the whole evaluation path in one place: parse the
// vendored bytes, parse the overlay, merge, compile.
//
// AllowIncompatibleRegex matches what internal/agentactivity/manifests.load
// does at runtime, and it has to: the vendored tree legitimately carries
// upstream's four `\p{Alphabetic}` patterns verbatim, and refusing the file
// over them would take twenty working rules down with each one. Compile records
// them per rule instead, and an incompatible rule evaluates as a no-match — the
// same no-match the running binary produces, which is the point.
// dropRule, when non-empty, leaves that one overlay rule out, which is exactly
// what the running binary would see if the rule were deleted from the file.
// Removal is not the same act for the two kinds of rule an overlay holds, and
// the difference is what the report has to say: dropping a `sidecar.`-prefixed
// rule removes it, because upstream never had one, while dropping a rule that
// carries an *upstream* id reverts to upstream's own rule — which for the four
// `\p{Alphabetic}` rewrites means reverting to a rule RE2 cannot compile, a
// dead rule rather than an absent one. Merge does the right thing for both; the
// report is what tells them apart.
func compileWithOverlay(upstreamBytes, overlayBytes []byte, dropRule string) (*manifest.Compiled, error) {
	opts := manifest.ValidateOptions{AllowIncompatibleRegex: true}
	upstream, err := manifest.ParseAndValidateWith(upstreamBytes, opts)
	if err != nil {
		return nil, fmt.Errorf("vendored manifest is invalid: %w", err)
	}
	if len(overlayBytes) == 0 {
		return manifest.Compile(upstream)
	}
	overlay, err := manifest.ParseOverlay(overlayBytes)
	if err != nil {
		return nil, fmt.Errorf("overlay is invalid: %w", err)
	}
	if dropRule != "" {
		overlay = overlayWithoutRule(overlay, dropRule)
		if len(overlay.Rules) == 0 {
			// The overlay's only rule was the one dropped; upstream stands
			// alone. Merge would accept an empty overlay, but going through it
			// would report OverlayApplied for a file contributing nothing.
			return manifest.Compile(upstream)
		}
	}
	merged, err := manifest.Merge(upstream, overlay)
	if err != nil {
		return nil, fmt.Errorf("overlay does not merge: %w", err)
	}
	return manifest.Compile(merged)
}

// overlayWithoutRule copies a parsed overlay with one rule left out. The
// overlay is never written back out, so this works on the parsed form.
func overlayWithoutRule(overlay *manifest.Manifest, ruleID string) *manifest.Manifest {
	out := *overlay
	out.Rules = nil
	for _, rule := range overlay.Rules {
		if rule.ID == ruleID {
			continue
		}
		out.Rules = append(out.Rules, rule)
	}
	return &out
}

// readVendoredManifests reads every vendored .toml from an upstream directory,
// keyed by file base.
//
// This is how the report gets the *old* bytes, and it is called before
// writeTree replaces them. Reading them from disk rather than from git is
// deliberate: `git show HEAD:<path>` answers a different question on a dirty
// tree, answers nothing useful when --out points somewhere outside the
// repository (which is how the tool is tested and how a sync is rehearsed
// against an older ref), and answers nothing at all in a checkout with no
// commit for the path. The bytes on disk are the bytes the previous sync wrote,
// which is what "before" means here.
//
// A missing directory is a first sync, not an error.
func readVendoredManifests(upstream string) map[string][]byte {
	entries, err := os.ReadDir(upstream)
	if err != nil {
		return nil
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "index.toml" || !strings.HasSuffix(name, ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(upstream, name))
		if err != nil {
			continue
		}
		out[strings.TrimSuffix(name, ".toml")] = data
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readSidecarOverlays reads the Sidecar overlays from the embedded copy rather
// than from the output directory.
//
// A sync writes upstream/, the two extracted tables and the lock; it never
// writes sidecar/, so an --out pointed at a temp directory has no overlay tree
// to read and the comparison would silently run with no overlays at all. The
// embedded copy is this repository's overlays, which is what the running binary
// merges and what the report is about.
func readSidecarOverlays() (map[string][]byte, error) {
	dir, err := manifests.SidecarOverlays()
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".toml") {
			continue
		}
		data, err := fs.ReadFile(dir, name)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(name, ".toml")] = data
	}
	return out, nil
}

// overlayRule is one rule in one Sidecar overlay file.
type overlayRule struct {
	// base is the overlay file without .toml, which is also the vendored
	// manifest it amends.
	base string
	id   string
	// rewrite records that the id is an upstream rule id, so this rule replaces
	// upstream's rule rather than adding one. Six of them do today: four RE2
	// rewrites of upstream's `\p{Alphabetic}` patterns, and two copies of an
	// upstream rule that only add visible_blocker.
	rewrite bool
	// disables records `disable = true`: the rule removes an upstream rule
	// rather than adding or replacing one.
	disables bool
}

// overlayRules lists every rule in every overlay, in file order, with each one
// classified against the manifest it amends.
func overlayRules(overlays, upstream map[string][]byte) ([]overlayRule, error) {
	bases := make([]string, 0, len(overlays))
	for base := range overlays {
		bases = append(bases, base)
	}
	sort.Strings(bases)

	var out []overlayRule
	for _, base := range bases {
		overlay, err := manifest.ParseOverlay(overlays[base])
		if err != nil {
			return nil, fmt.Errorf("sidecar/%s.toml: %w", base, err)
		}
		ids := map[string]bool{}
		if data, ok := upstream[base]; ok {
			parsed, err := manifest.ParseAndValidateWith(data,
				manifest.ValidateOptions{AllowIncompatibleRegex: true})
			if err != nil {
				return nil, fmt.Errorf("upstream/%s.toml: %w", base, err)
			}
			for _, rule := range parsed.Rules {
				ids[rule.ID] = true
			}
		}
		for _, rule := range overlay.Rules {
			out = append(out, overlayRule{
				base:     base,
				id:       rule.ID,
				rewrite:  ids[rule.ID] && !rule.Disabled(),
				disables: rule.Disabled(),
			})
		}
	}
	return out, nil
}

// harnessExemptPattern reads the exemption a `sidecar.` rule declares in its
// own overlay file. The format is scripts/herdr-diff.sh's, character for
// character, because a second spelling would mean a rule exempt in one
// comparison and not in the other.
var harnessExemptPattern = regexp.MustCompile(`(?m)^# harness-exempt: (sidecar\.[A-Za-z0-9_.]*)`)

// harnessExempt returns the declared exemptions as "<overlay base>:<rule id>".
//
// The key is scoped to the file that declares it, exactly as the script scopes
// it, because a rule id is not unique across agents: sidecar.overlay_retain
// exists in both claude.toml and grok.toml, and an unscoped whitelist would
// silence one agent's rule because another agent's rule of the same name is
// exempt.
func harnessExempt(overlays map[string][]byte) map[string]bool {
	out := map[string]bool{}
	for base, data := range overlays {
		for _, match := range harnessExemptPattern.FindAllStringSubmatch(string(data), -1) {
			out[base+":"+match[1]] = true
		}
	}
	return out
}
