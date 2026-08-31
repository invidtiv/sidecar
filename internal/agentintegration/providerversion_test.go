package agentintegration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAProviderThatNeverAnswersDoesNotHangTheSurface is a regression test with a
// concrete origin: `cursor --version` does not exit, and the moment the
// capability registry gained an entry named `cursor`, `sidecar agent integration
// list` hung forever. The surface asks every known provider for its version, so
// one uncooperative third-party binary was enough to take the whole command
// down.
//
// A version string is decoration on a status line -- nothing decides anything on
// its absence -- so the correct response to a provider that will not answer is
// to stop waiting for it.
func TestAProviderThatNeverAnswersDoesNotHangTheSurface(t *testing.T) {
	dir := t.TempDir()
	// A "provider" that ignores --version and blocks, which is the behaviour
	// being defended against rather than a crash or a bad exit code.
	fake := filepath.Join(dir, "hangs-forever")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 120\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan string, 1)
	start := time.Now()
	go func() { done <- detectProviderVersion("hangs-forever") }()

	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("a provider that never answered reported a version %q", got)
		}
		// Generous on purpose: the point is that it returns near its own
		// deadline rather than near the child's lifetime.
		if elapsed := time.Since(start); elapsed > providerVersionTimeout+5*time.Second {
			t.Fatalf("gave up after %s, want about %s", elapsed, providerVersionTimeout)
		}
	case <-time.After(providerVersionTimeout + 15*time.Second):
		t.Fatal("detectProviderVersion never returned; this is the hang the timeout exists to prevent")
	}
}

// TestAProviderThatAnswersIsStillRead guards the other direction. A timeout that
// swallowed every answer would leave the field permanently blank, and no test
// above would notice.
func TestAProviderThatAnswersIsStillRead(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "answers-fast")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'answers-fast 9.9.9'\necho 'a second line nobody wants'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := detectProviderVersion("answers-fast"); got != "answers-fast 9.9.9" {
		t.Fatalf("version = %q, want the first line only", got)
	}
}
