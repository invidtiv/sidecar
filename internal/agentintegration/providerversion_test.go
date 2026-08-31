package agentintegration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/resource"
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

// TestAHostileVersionStringCannotReachTheTUI closes the last gap in this probe.
//
// The value is the stdout of a third-party binary on the user's PATH, and it is
// rendered directly into a status table. Bounding the wait was only half the
// job: an unbounded, unsanitized string could carry ANSI, an OSC hyperlink, or a
// kilobyte of text into a cell, which is exactly what resource.SanitizeLine and
// MaxProviderVersionChars already exist to prevent everywhere else a provider
// string is rendered.
func TestAHostileVersionStringCannotReachTheTUI(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "hostile-version")
	// Escapes, an OSC 8 hyperlink, a control character, and far more text than
	// any real version string.
	script := "#!/bin/sh\n" +
		`printf '\033[31mred\033[0m \033]8;;https://example.com\033\\link\033]8;;\033\\ \a` +
		strings.Repeat("A", 300) + `\n'` + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := detectProviderVersion("hostile-version")
	if got == "" {
		t.Skip("the fixture shell did not produce output; nothing to assert about")
	}
	if len([]rune(got)) > resource.MaxProviderVersionChars {
		t.Fatalf("version is %d runes, over the %d cap: %q",
			len([]rune(got)), resource.MaxProviderVersionChars, got)
	}
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Fatalf("an escape sequence survived into the rendered version: %q", got)
	}
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("a control character survived into the rendered version: %q", got)
		}
	}
}
