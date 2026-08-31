package lifecycleenv

import (
	"os"
	"strings"
	"testing"
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
