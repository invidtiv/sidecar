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

// traceRow is one sanitized trace line. Phase A files carry seven columns and
// Phase B files carry eight; the extra column is the bounded error class name,
// which is what made cancellation distinguishable from failure.
type traceRow struct {
	kind   string
	typ    string
	status string
	err    string
}

func readTrace(t *testing.T, name string) []traceRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "traces", "opencode", name))
	if err != nil {
		t.Fatal(err)
	}
	var rows []traceRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 7 {
			t.Fatalf("malformed trace row: %q", line)
		}
		row := traceRow{kind: cols[1], typ: cols[2], status: cols[3]}
		if len(cols) > 7 && cols[7] != "-" {
			row.err = cols[7]
		}
		rows = append(rows, row)
	}
	return rows
}

// TestOpenCodeCancellationTraceProvesTheCancelledTransition is the Phase B
// entry-condition evidence, asserted rather than described.
//
// It is what allows the capability matrix to list `cancelled` in OpenCode's
// covered set, and therefore what allows TierFor to derive `full` for it. If
// this trace were removed or its shape changed, the matrix entry would become
// a claim with nothing behind it — so the assertion is deliberately specific
// about the event sequence and the discriminator.
func TestOpenCodeCancellationTraceProvesTheCancelledTransition(t *testing.T) {
	rows := readTrace(t, "cancelled-turn.tsv")

	// The turn must actually have started working before it was cancelled,
	// otherwise the trace proves nothing about interrupting live work.
	var sawBusy bool
	var abortIdx = -1
	for i, r := range rows {
		if r.typ == "session.status" && r.status == `{"type":"busy"}` {
			sawBusy = true
		}
		if r.typ == "session.error" && r.err == "MessageAbortedError" {
			if !sawBusy {
				t.Fatal("cancellation recorded before the session ever went busy")
			}
			abortIdx = i
			break
		}
	}
	if abortIdx < 0 {
		t.Fatal("no session.error carrying MessageAbortedError; the cancellation evidence is missing")
	}

	// The abort must resolve to idle, and it must do so through the
	// state-shaped session.status rather than leaving the lane to be inferred.
	// This is the property that lets a cancelled turn self-correct even if the
	// error event itself is dropped.
	var sawStatusIdle, sawSessionIdle bool
	for _, r := range rows[abortIdx:] {
		if r.typ == "session.status" && r.status == `{"type":"idle"}` {
			sawStatusIdle = true
		}
		if r.typ == "session.idle" {
			sawSessionIdle = true
		}
	}
	if !sawStatusIdle {
		t.Fatal("cancellation did not resolve through session.status{idle}; the lane would have to be inferred")
	}
	if !sawSessionIdle {
		t.Fatal("cancellation did not emit session.idle")
	}

	// A cancelled turn and a failed turn are the same shape on the bus. The
	// only thing separating them is the bounded error class name, so the
	// contrast trace is part of the evidence rather than a nicety: without it
	// "MessageAbortedError means cancelled" would be an assumption.
	var contrastErr string
	for _, r := range readTrace(t, "provider-error-named.tsv") {
		if r.typ == "session.error" && r.err != "" {
			contrastErr = r.err
			break
		}
	}
	if contrastErr == "" {
		t.Fatal("contrast trace records no error name, so cancellation is not shown to be distinguishable")
	}
	if contrastErr == "MessageAbortedError" {
		t.Fatalf("a provider failure also reports MessageAbortedError; cancellation is not distinguishable")
	}
}

// TestOpenCodeEarnsFullOnlyFromTheCancellationEvidence ties the promotion to
// the trace rather than to an edit.
//
// Removing `cancelled` from the covered set — the state Phase A was in — must
// take the entry back to advisory. That is the check that keeps the tier and
// the evidence from drifting apart in opposite directions.
func TestOpenCodeEarnsFullOnlyFromTheCancellationEvidence(t *testing.T) {
	var oc Capability
	for _, c := range loadCapabilityMatrix(t) {
		if c.Provider == "opencode" {
			oc = c
		}
	}
	if oc.Provider == "" {
		t.Fatal("no opencode entry in the capability matrix")
	}
	if got, _ := oc.TierFor(StatusCurrent, true); got != TierFull {
		t.Fatalf("opencode earns %q, want full", got)
	}

	without := oc
	without.Covered = nil
	for _, tr := range oc.Covered {
		if tr != TransitionCancelled {
			without.Covered = append(without.Covered, tr)
		}
	}
	if len(without.Covered) == len(oc.Covered) {
		t.Fatal("opencode does not claim the cancelled transition")
	}
	got, reason := without.TierFor(StatusCurrent, true)
	if got != TierAdvisory || reason != ReasonCapabilityUnproved {
		t.Fatalf("without cancellation evidence opencode earns %q (%s), want advisory/capability_unproved", got, reason)
	}
}

// TestTierForPolicesEveryTierBoundary covers td-f4d92c finding 1: before this,
// only the full-to-advisory boundary was enforced, so an entry could claim
// `advisory` or `session-identity` with no evidence at all and be believed.
// Advisory is not a harmless tier — it still authors state whenever the screen
// has no opinion — so an empty claim has to fall out entirely.
func TestTierForPolicesEveryTierBoundary(t *testing.T) {
	tests := []struct {
		name       string
		cap        Capability
		wantTier   Tier
		wantReason FallbackReason
	}{
		{
			name:       "advisory with no evidence",
			cap:        Capability{Tier: TierAdvisory, Evidence: EvidenceNone, Covered: []Transition{TransitionWorkStart}},
			wantTier:   TierScreenFallback,
			wantReason: ReasonCapabilityUnproved,
		},
		{
			name:       "advisory covering nothing",
			cap:        Capability{Tier: TierAdvisory, Evidence: EvidenceDocsOnly},
			wantTier:   TierScreenFallback,
			wantReason: ReasonCapabilityUnproved,
		},
		{
			name:       "session identity that cannot identify a session",
			cap:        Capability{Tier: TierSessionIdentity, Evidence: EvidenceDocsOnly, Covered: []Transition{TransitionWorkStart}},
			wantTier:   TierScreenFallback,
			wantReason: ReasonCapabilityUnproved,
		},
		{
			name:       "session identity with no evidence",
			cap:        Capability{Tier: TierSessionIdentity, Evidence: EvidenceNone, Covered: []Transition{TransitionSessionIdentity}},
			wantTier:   TierScreenFallback,
			wantReason: ReasonCapabilityUnproved,
		},
		{
			name:       "honest advisory survives",
			cap:        Capability{Tier: TierAdvisory, Evidence: EvidenceDocsOnly, Covered: []Transition{TransitionWorkStart}},
			wantTier:   TierAdvisory,
			wantReason: ReasonNone,
		},
		{
			name:       "honest session identity survives",
			cap:        Capability{Tier: TierSessionIdentity, Evidence: EvidenceDocsOnly, Covered: []Transition{TransitionSessionIdentity}},
			wantTier:   TierSessionIdentity,
			wantReason: ReasonNone,
		},
		{
			name:       "screen fallback needs no evidence",
			cap:        Capability{Tier: TierScreenFallback, Evidence: EvidenceNone},
			wantTier:   TierScreenFallback,
			wantReason: ReasonNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := tc.cap.TierFor(StatusCurrent, true)
			if got != tc.wantTier || reason != tc.wantReason {
				t.Fatalf("got %q/%q, want %q/%q", got, reason, tc.wantTier, tc.wantReason)
			}
		})
	}
}
