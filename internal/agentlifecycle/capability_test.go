package agentlifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCapabilityMatrix(t *testing.T) []Capability {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var caps []Capability
	if err := json.Unmarshal(data, &caps); err != nil {
		t.Fatal(err)
	}
	if len(caps) == 0 {
		t.Fatal("capability matrix is empty")
	}
	return caps
}

// TestCapabilityMatrixCannotClaimUnearnedAuthority is the point of checking the
// matrix in at all.
//
// It re-derives each entry's exercisable tier from that entry's own recorded
// evidence and asserts the declared tier matches. An entry cannot be edited to
// claim full lifecycle authority without also recording real traces covering
// every transition in FullLifecycleTransitions — which cannot be done honestly
// without actually capturing them.
func TestCapabilityMatrixCannotClaimUnearnedAuthority(t *testing.T) {
	for _, c := range loadCapabilityMatrix(t) {
		t.Run(c.Provider, func(t *testing.T) {
			// Evaluated as if installed, current, and in range: the most
			// favourable possible runtime conditions. Anything the entry cannot
			// earn here it can never earn.
			got, reason := c.TierFor(StatusCurrent, true)
			if got != c.Tier {
				t.Fatalf("declared tier %q but evidence earns %q (%s)", c.Tier, got, reason)
			}
			if c.Tier == TierFull && c.Evidence != EvidenceRealTrace {
				t.Fatalf("tier full without real traces")
			}
		})
	}
}

// TestCapabilityMatrixIsWellFormed pins the vocabularies and the honesty rules
// that are not expressible as a tier derivation.
func TestCapabilityMatrixIsWellFormed(t *testing.T) {
	validTier := map[Tier]bool{}
	for _, v := range Tiers() {
		validTier[v] = true
	}
	validEvidence := map[EvidenceQuality]bool{}
	for _, v := range EvidenceQualities() {
		validEvidence[v] = true
	}
	validTransition := map[Transition]bool{}
	for _, v := range Transitions() {
		validTransition[v] = true
	}

	seen := map[string]bool{}
	for _, c := range loadCapabilityMatrix(t) {
		t.Run(c.Provider, func(t *testing.T) {
			if c.SchemaVersion != SchemaVersion {
				t.Fatalf("schemaVersion = %d, want %d", c.SchemaVersion, SchemaVersion)
			}
			if seen[c.Provider] {
				t.Fatalf("duplicate entry for provider %q", c.Provider)
			}
			seen[c.Provider] = true

			if !validTier[c.Tier] {
				t.Fatalf("unknown tier %q", c.Tier)
			}
			if !validEvidence[c.Evidence] {
				t.Fatalf("unknown evidence quality %q", c.Evidence)
			}
			for _, tr := range c.Covered {
				if !validTransition[tr] {
					t.Fatalf("unknown transition %q", tr)
				}
			}
			if c.Source == "" || c.SourceDoc == "" || c.SourceVersionNote == "" {
				t.Fatal("every entry must cite its source, doc, and the version its evidence came from")
			}
			// An entry with gaps must say what they are, and an entry with no
			// gaps had better be one that was actually traced.
			if len(c.KnownGaps) == 0 && c.Evidence != EvidenceRealTrace {
				t.Fatal("an untraced entry claiming no known gaps is not credible")
			}

			// A real-trace claim has to be backed by files on disk. This is the
			// check that stops "real-trace" from becoming a word someone types.
			dir := filepath.Join("testdata", "traces", c.Provider)
			entries, err := os.ReadDir(dir)
			traced := err == nil && len(entries) > 0
			if c.Evidence == EvidenceRealTrace && !traced {
				t.Fatalf("evidence claims real traces but %s holds none", dir)
			}
			if c.Evidence == EvidenceDocsOnly && traced {
				t.Fatalf("evidence says docs-only but real traces exist in %s", dir)
			}
		})
	}
}

// TestOpenCodeTraceProvesTheSteelThreadTransitions reads the recorded trace and
// asserts the exact ordered lifecycle it demonstrates.
//
// This is the evidence behind selecting OpenCode as the first steel thread, so
// it is asserted rather than described. It also pins the two findings that
// argue against over-trusting the integration: the blocked lane never appears
// in session.status, and tool.execute.after never fires.
func TestOpenCodeTraceProvesTheSteelThreadTransitions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "traces", "opencode", "tool-turn-with-permission.tsv"))
	if err != nil {
		t.Fatal(err)
	}

	var types []string
	var statuses []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 4 {
			t.Fatalf("malformed trace row: %q", line)
		}
		types = append(types, cols[2])
		if cols[3] != "-" {
			statuses = append(statuses, cols[3])
		}
	}

	// The ordered lifecycle skeleton the steel thread depends on.
	want := []string{
		"session.created",     // session identity
		"chat.message",        // work start
		"session.status",      // busy
		"tool.execute.before", // tool use
		"permission.asked",    // blocked
		"permission.replied",  // unblocked
		"session.idle",        // turn complete
		"dispose",             // process exit
	}
	idx := 0
	for _, got := range types {
		if idx < len(want) && got == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("trace did not contain the lifecycle skeleton in order; matched %d of %d, stuck at %q",
			idx, len(want), want[idx])
	}

	// session.status is state-shaped, and only ever busy or idle. That is the
	// self-healing property that made OpenCode the choice, and the fact that
	// blocked is absent from it is the caveat on that property.
	for _, s := range statuses {
		if s != `{"type":"busy"}` && s != `{"type":"idle"}` {
			t.Fatalf("unexpected session.status shape %q", s)
		}
	}
	for _, got := range types {
		if got == "tool.execute.after" {
			t.Fatal("tool.execute.after fired; the recorded gap is stale and the matrix must be updated")
		}
	}
}
