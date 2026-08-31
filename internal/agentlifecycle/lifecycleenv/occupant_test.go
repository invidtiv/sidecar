package lifecycleenv

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentsession"
)

// TestTheStrictGenerationRefusesWhereTheLenientOneFallsBack is the regression
// test for a hole a live proof found.
//
// providerGeneration falls back to the pane's root process when it cannot walk
// from this process to the pane, and LiveProviderGeneration returns the pane's
// root process when the pane has no provider running. Those are the exact two
// conditions that hold for a hook left behind by an exited provider: it has been
// reparented away from the pane, and the pane it named is now empty. Both sides
// fell back, the fence compared them, found them equal, and accepted a report
// from a dead process — the precise case the fence exists to reject.
//
// The fix is that the reporting side has no fallback. This test pins the
// difference rather than the fix, so it keeps meaning something if either
// function is rewritten.
func TestTheStrictGenerationRefusesWhereTheLenientOneFallsBack(t *testing.T) {
	// A pane whose root process is not an ancestor of this test binary. Any pid
	// that is not on our own parent chain models an orphaned reporter: the walk
	// runs out before it reaches the claimed pane.
	stranger := unrelatedPID(t)

	lenient, err := providerGeneration(stranger)
	if err != nil {
		t.Fatalf("providerGeneration refused, so the contrast this test draws no longer exists: %v", err)
	}
	if lenient == "" {
		t.Fatal("providerGeneration returned an empty generation")
	}

	live, err := LiveProviderGeneration(stranger)
	if err != nil {
		t.Skipf("no process table available: %v", err)
	}

	// The heart of it: the lenient derivation and the live occupant agree, even
	// though this process has nothing to do with that pane. A fence built from
	// those two values alone would accept this report.
	if lenient != live {
		t.Skipf("the stranger pid has children, so the fallback collision this test models does not arise (lenient=%s live=%s)", lenient, live)
	}

	strict, err := ReportingProviderGeneration(stranger)
	if err == nil {
		t.Fatalf("ReportingProviderGeneration returned %q for a pane this process does not run under; "+
			"a reporter that cannot prove which provider it belongs to must be refused", strict)
	}
	if !strings.Contains(err.Error(), "not running under the pane's provider") &&
		!strings.Contains(err.Error(), "does not reach the pane") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

// TestTheStrictGenerationAcceptsTheOrdinaryCase keeps the test above honest: the
// strict rule must still answer for a reporter that really is running under the
// pane it names.
func TestTheStrictGenerationAcceptsTheOrdinaryCase(t *testing.T) {
	// This process's own parent is, by construction, a process this process runs
	// under. Naming it as the pane root makes this test binary the "provider".
	parent, err := parentPID(os.Getpid())
	if err != nil {
		t.Skipf("cannot read this process's parent: %v", err)
	}
	gen, err := ReportingProviderGeneration(parent)
	if err != nil {
		t.Fatalf("a reporter running directly under the named pane was refused: %v", err)
	}
	if !strings.HasPrefix(gen, "pid=") {
		t.Fatalf("generation = %q, wanted a pid= form", gen)
	}
}

func TestGenerationDerivationsRefuseAnImpossiblePane(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := ReportingProviderGeneration(pid); err == nil {
			t.Fatalf("ReportingProviderGeneration(%d) was accepted", pid)
		}
		if _, err := LiveProviderGeneration(pid); err == nil {
			t.Fatalf("LiveProviderGeneration(%d) was accepted", pid)
		}
	}
}

// unrelatedPID finds a live pid that is not on this process's parent chain.
func unrelatedPID(t *testing.T) int {
	t.Helper()
	ancestors := map[int]bool{}
	for pid, i := os.Getpid(), 0; pid > 1 && i < maxAncestry; i++ {
		ancestors[pid] = true
		parent, err := parentPID(pid)
		if err != nil {
			break
		}
		pid = parent
	}
	// pid 1 is never on a useful ancestor chain for this purpose and always
	// exists, and it has children, so prefer a leaf-ish stranger when one is
	// available. Fall back to 1 only if nothing else is found.
	all, err := childrenOf(1)
	if err == nil {
		for _, pid := range all {
			if ancestors[pid] {
				continue
			}
			kids, err := childrenOf(pid)
			if err == nil && len(kids) == 0 {
				return pid
			}
		}
	}
	t.Skip("no unrelated childless process was available to model an orphaned reporter")
	return 0
}

// TestOnlyAPositiveMismatchRefusesAReportedKind pins the rule behind td-11040b
// in both directions, because both directions are load-bearing.
//
// Refusing a named mismatch is the fix: grok reads ~/.claude/settings.json for
// Claude Code compatibility, so Sidecar's installed Claude hook fires inside
// grok sessions and reports --kind claude for a grok conversation.
//
// Passing an unnamed occupant is not laziness, it is the fail-open rule this
// surface owes its callers. Process identity has no adapter on some platforms
// and resolves to nothing when a provider spawns its hook into a fresh process
// group; refusing there would silently disable session binding for every
// provider on those hosts, which is a far larger failure than the one being
// guarded — and it would fail in the direction a hook surface must never fail.
func TestOnlyAPositiveMismatchRefusesAReportedKind(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reported string
		occupant string
		refuse   bool
	}{
		{"the grok pane running a claude hook", "claude", "grok", true},
		{"a claude hook in a codex pane", "claude", "codex", true},
		{"a genuine claude pane", "claude", "claude", false},
		{"a genuine grok pane", "grok", "grok", false},
		{"an occupant no adapter could name", "claude", "", false},
		{"an occupant this platform cannot see", "claude", "   ", false},
		{"no claim to check", "", "grok", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReportedKind(tc.reported, tc.occupant)
			if tc.refuse && err == nil {
				t.Fatalf("%q reported inside a %q pane was accepted", tc.reported, tc.occupant)
			}
			if !tc.refuse && err != nil {
				t.Fatalf("%q reported inside a %q pane was refused: %v", tc.reported, tc.occupant, err)
			}
			if !tc.refuse {
				return
			}
			if !errors.Is(err, agentsession.ErrKindMismatch) {
				t.Fatalf("refusal did not carry the kind-mismatch sentinel: %v", err)
			}
			// The message has to name both providers or a user reading a hook's
			// stderr cannot tell which of their agents is misconfigured.
			if !strings.Contains(err.Error(), tc.reported) || !strings.Contains(err.Error(), tc.occupant) {
				t.Fatalf("refusal named neither side clearly: %v", err)
			}
		})
	}
}

// TestAnEmptyPaneNamesNoOccupant keeps OccupantKind from turning "no provider is
// running" into evidence, and keeps an ordinary live process from being
// mistaken for a provider that contradicts the report.
func TestAnEmptyPaneNamesNoOccupant(t *testing.T) {
	if got := OccupantKind(0); got != "" {
		t.Fatalf("OccupantKind(0) = %q; wanted no claim", got)
	}
	if got := OccupantKind(-1); got != "" {
		t.Fatalf("OccupantKind(-1) = %q; wanted no claim", got)
	}
	if err := VerifyReportedKind(os.Getpid(), "claude"); err != nil {
		t.Fatalf("a non-provider process refused a report: %v", err)
	}
}
