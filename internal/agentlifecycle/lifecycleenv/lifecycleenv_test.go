package lifecycleenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/shellstate"
)

// This package is the boundary where untrusted provider input meets Sidecar's
// own view of the world, so the cases worth pinning are the ones where it
// declines to answer: outside a managed shell, without a pane, and when the
// process tree cannot be read.

// TestResolveIsANoOpOutsideAManagedShell is the property the whole hook surface
// rests on. A provider hook fires on every event whether or not the user is
// running inside Sidecar, so "not applicable" must be silent and must not be an
// error — anything else puts Sidecar's complaints in the agent's own terminal.
func TestResolveIsANoOpOutsideAManagedShell(t *testing.T) {
	t.Run("no managed cue", func(t *testing.T) {
		t.Setenv(shellstate.ManagedEnv, "")
		t.Setenv("TMUX_PANE", "%7")

		ctx, err := Resolve(t.TempDir())
		if err != nil {
			t.Fatalf("outside a managed shell must not be an error: %v", err)
		}
		if ctx.Managed {
			t.Fatal("reported managed with no cue")
		}
	})

	t.Run("cue set to something other than 1", func(t *testing.T) {
		// The cue is an exact match rather than a truthiness test, so a stale or
		// hand-set value cannot switch reporting on.
		t.Setenv(shellstate.ManagedEnv, "true")
		t.Setenv("TMUX_PANE", "%7")

		ctx, err := Resolve(t.TempDir())
		if err != nil || ctx.Managed {
			t.Fatalf("cue %q was accepted: managed=%v err=%v", "true", ctx.Managed, err)
		}
	})

	t.Run("managed but no pane", func(t *testing.T) {
		// A managed shell always has a pane. Without one there is nothing a
		// report could be about, so this is "not applicable", not a failure.
		t.Setenv(shellstate.ManagedEnv, "1")
		t.Setenv("TMUX_PANE", "")

		ctx, err := Resolve(t.TempDir())
		if err != nil {
			t.Fatalf("a missing pane must not be an error: %v", err)
		}
		if ctx.Managed {
			t.Fatal("reported managed with no pane")
		}
	})

	t.Run("no subprocess is spawned when the cue is absent", func(t *testing.T) {
		// The cheap check must come first: a hook outside Sidecar reaches the
		// answer with no tmux call and no file access. Emptying PATH makes any
		// attempt to run tmux or ps fail loudly instead of silently succeeding.
		t.Setenv(shellstate.ManagedEnv, "")
		t.Setenv("PATH", "")

		if _, err := Resolve(t.TempDir()); err != nil {
			t.Fatalf("the no-cue path touched a subprocess: %v", err)
		}
	})
}

// TestProviderGenerationWalksToThePaneRoot covers the ordinary case: the
// generation is the last process before the pane's shell, so relaunching an
// agent in the same pane produces a new one while the pane and shell stay the
// same.
func TestProviderGenerationWalksToThePaneRoot(t *testing.T) {
	// This process's parent stands in for the pane's root process, which makes
	// this process the "provider" one level below it.
	gen, err := providerGeneration(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	want := "pid=" + strconv.Itoa(os.Getpid())
	if !strings.HasPrefix(gen, want) {
		t.Fatalf("generation = %q, want it to identify this process (%s)", gen, want)
	}
}

// TestProviderGenerationFallsBackToThePaneRoot covers an unusual process tree,
// a hook that daemonised, or a walk that runs out of ancestors.
//
// The fallback is weaker — it will not rotate when the agent is relaunched in
// the same pane — but it is stable and truthful, and the resolver's other
// identity checks still apply. Failing outright here would disable reporting
// entirely for a shape we merely did not anticipate.
func TestProviderGenerationFallsBackToThePaneRoot(t *testing.T) {
	// A pid that is certainly not an ancestor of this process, so the walk
	// terminates without ever finding it.
	const unreachable = 999999

	gen, err := providerGeneration(unreachable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gen, "pid="+strconv.Itoa(unreachable)) {
		t.Fatalf("generation = %q, want a fallback to the pane root", gen)
	}
}

// TestProviderGenerationSurvivesPsBeingUnavailable is the degraded-environment
// case. With no ps the ancestry cannot be walked and no start time can be read,
// but a usable generation must still come back.
func TestProviderGenerationSurvivesPsBeingUnavailable(t *testing.T) {
	t.Setenv("PATH", "")

	gen, err := providerGeneration(4242)
	if err != nil {
		t.Fatalf("a missing ps must not fail the walk: %v", err)
	}
	if gen != "pid=4242" {
		t.Fatalf("generation = %q, want the bare pane-root form with no start time", gen)
	}
}

// TestProviderGenerationRefusesAPanelessRoot is the one genuine error: without
// a pane process there is nothing to anchor a generation to, and inventing one
// would let unrelated runs share an identity.
func TestProviderGenerationRefusesAPanelessRoot(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := providerGeneration(pid); err == nil {
			t.Fatalf("providerGeneration(%d) was accepted", pid)
		}
	}
}

// TestGenerationStringsAreValidIdentityFields guards a quiet failure mode: ps
// prints a start time containing spaces, and an identity field with whitespace
// is rejected by validation. Every report would then be refused, from inside a
// code path whose errors are deliberately swallowed.
func TestGenerationStringsAreValidIdentityFields(t *testing.T) {
	gen := generationString(os.Getpid())
	if strings.ContainsAny(gen, " \t\n") {
		t.Fatalf("generation %q contains whitespace and would fail validation", gen)
	}
	if len(gen) > agentlifecycle.MaxIdentityFieldBytes {
		t.Fatalf("generation is %d bytes, over the %d limit", len(gen), agentlifecycle.MaxIdentityFieldBytes)
	}
}

// TestFingerprintIsSaltedAndOpaque pins the privacy property: the store retains
// a digest, never the provider's own session identifier.
func TestFingerprintIsSaltedAndOpaque(t *testing.T) {
	dir := t.TempDir()
	ctx := Context{salt: loadSalt(dir)}

	const sessionID = "ses_01ABCDEF"
	got := ctx.Fingerprint(sessionID)

	if got == "" {
		t.Fatal("no fingerprint produced")
	}
	if strings.Contains(got, sessionID) {
		t.Fatalf("fingerprint %q contains the session id", got)
	}
	if ctx.Fingerprint(sessionID) != got {
		t.Fatal("fingerprint is not stable for the same input")
	}
	if ctx.Fingerprint("ses_somethingelse") == got {
		t.Fatal("two session ids produced the same fingerprint")
	}
	if ctx.Fingerprint("") != "" {
		t.Fatal("an absent session id must not produce a fingerprint")
	}

	// A different host salt must produce a different digest, or the digest
	// would be a stable global identifier for that session.
	other := Context{salt: loadSalt(t.TempDir())}
	if other.Fingerprint(sessionID) == got {
		t.Fatal("the fingerprint does not depend on the host salt")
	}
}

// TestSaltIsCreatedPrivately checks the one file here whose value is only
// useful to someone who should not have it.
func TestSaltIsCreatedPrivately(t *testing.T) {
	dir := t.TempDir()
	if salt := loadSalt(dir); len(salt) == 0 {
		t.Fatal("no salt was created")
	}
	info, err := os.Stat(filepath.Join(dir, saltFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("salt mode = %04o, want 0600", perm)
	}

	// Reused rather than regenerated, or every restart would rotate every
	// fingerprint and break run continuity.
	first := loadSalt(dir)
	second := loadSalt(dir)
	if string(first) != string(second) {
		t.Fatal("the salt was regenerated on a second read")
	}
}

// TestLoadSaltDegradesRatherThanFailing covers an unwritable state directory.
// An unsalted digest is a weaker privacy property and a working hook, which is
// the right trade for a diagnostic record.
func TestLoadSaltDegradesRatherThanFailing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permissions cannot be made to fail")
	}
	dir := t.TempDir()
	readonly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readonly, 0o500); err != nil {
		t.Fatal(err)
	}
	// Nothing here may panic or block; an empty salt is an acceptable answer.
	_ = loadSalt(filepath.Join(readonly, "nested"))
	_ = loadSalt("")
}

// TestIdentityForRotatesTheRunOnEitherDiscontinuity pins why the run id is
// derived from the generation and the session fingerprint together: a provider
// restart in the same pane is a new run even if it resumes the same session,
// and a session switch inside one process is a new run even though the process
// did not change. Both are discontinuities that must not let earlier reports
// keep authority.
func TestIdentityForRotatesTheRunOnEitherDiscontinuity(t *testing.T) {
	base := Context{
		Managed:           true,
		Host:              "host-a",
		ServerIncarnation: "pid=1",
		PaneID:            "%7",
		ProcessGeneration: "pid=100,start=x",
		salt:              loadSalt(t.TempDir()),
	}

	first := base.IdentityFor("opencode", "ses_one")
	if first.RunID == "" {
		t.Fatal("no run id derived")
	}
	if first.SessionFingerprint == "" {
		t.Fatal("a supplied session id produced no fingerprint")
	}
	if base.IdentityFor("opencode", "ses_one").RunID != first.RunID {
		t.Fatal("the run id is not stable for an unchanged context")
	}

	rotatedSession := base.IdentityFor("opencode", "ses_two")
	if rotatedSession.RunID == first.RunID {
		t.Fatal("a session rotation did not rotate the run")
	}

	restarted := base
	restarted.ProcessGeneration = "pid=200,start=y"
	if restarted.IdentityFor("opencode", "ses_one").RunID == first.RunID {
		t.Fatal("a provider restart did not rotate the run")
	}

	// Identity must never be selectable through the provider argument: the
	// pane, host, and server come from the context alone.
	if first.PaneID != "%7" || first.Host != "host-a" || first.ServerIncarnation != "pid=1" {
		t.Fatalf("identity was not taken from the verified context: %+v", first)
	}
}
