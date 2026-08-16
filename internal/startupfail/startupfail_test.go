package startupfail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user whose config file will not parse needs three things and no more: what
// happened, where the file and the error are, and what they are allowed to do
// about it. What they must never get is their own settings echoed back at them
// by a tool that is about to tell them to paste this into a public issue.
func TestConfigLoadNamesTheFileAndTheLineWithoutItsContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := "{\n  \"ui\": {\n    \"terminalTitle\": \"secret-client-project\",\n  }\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	err := json.Unmarshal([]byte(body), &probe)
	if err == nil {
		t.Fatal("fixture parsed; it must not")
	}

	out := Render(ConfigLoad(path, err))

	for _, want := range []string{
		"Sidecar could not start.",
		"could not be parsed",
		path,
		"near line 4",
		"never rewrites it",
		DocsURL,
		IssuesURL,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure text missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-client-project") {
		t.Fatalf("failure text leaked a config value:\n%s", out)
	}
	if lines := strings.Count(out, "\n"); lines > 13 {
		t.Errorf("failure text is %d lines; it should stay short:\n%s", lines, out)
	}
}

// A missing file is not a parse failure and must not claim a line number.
func TestConfigLoadWithoutAParsePosition(t *testing.T) {
	out := Render(ConfigLoad("/nowhere/config.json", os.ErrNotExist))
	if strings.Contains(out, "near line") {
		t.Fatalf("invented a parse position:\n%s", out)
	}
	if !strings.Contains(out, "/nowhere/config.json") {
		t.Fatalf("did not name the file:\n%s", out)
	}
}

// The piped-stdout case points at the commands that do work without a
// terminal, not at a settings editor Sidecar does not have.
func TestNotATerminalPointsAtTheNonInteractiveSurface(t *testing.T) {
	out := Render(NotATerminal())
	for _, want := range []string{"requires an interactive terminal", "sidecar help", IssuesURL} {
		if !strings.Contains(out, want) {
			t.Errorf("failure text missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"config.json", "fallback settings"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("failure text mentions %q, which is not this failure:\n%s", forbidden, out)
		}
	}
}

// Every failure ends with the same support block, and none of it acts on its
// own: it names places the user may choose to go.
func TestEveryFailureOffersThePrivateSupportPath(t *testing.T) {
	for name, failure := range map[string]Failure{
		"isolation":   Isolation(os.ErrPermission),
		"projectRoot": ProjectRoot("./missing", os.ErrNotExist),
		"terminal":    Terminal(os.ErrClosed),
		"notTTY":      NotATerminal(),
	} {
		out := Render(failure)
		if !strings.HasPrefix(out, "Sidecar could not start.") {
			t.Errorf("%s: missing the shared opening line:\n%s", name, out)
		}
		if !strings.Contains(out, DocsURL) || !strings.Contains(out, IssuesURL) {
			t.Errorf("%s: missing the support block:\n%s", name, out)
		}
		if !strings.Contains(out, "Next: ") {
			t.Errorf("%s: no next step:\n%s", name, out)
		}
	}
}
