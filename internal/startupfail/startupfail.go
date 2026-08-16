// Package startupfail renders the message Sidecar prints when it cannot reach
// its first frame. Every pre-render exit in cmd/sidecar goes through here so a
// user meets one shape of failure: what happened, the specific thing to do
// about it, and a support path that stays private.
//
// The package never reads a user's settings into its output. It prints paths
// and error positions — the config file's location and the line a parser
// stopped on — and never the file's contents beyond that reference. Nothing
// here uploads anything, and nothing here rewrites configuration to make a
// launch succeed: a startup that cannot honor the user's config stops and says
// so rather than quietly running on something else.
package startupfail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const (
	// DocsURL and IssuesURL are the two support destinations the failure text
	// offers. Both are things the user chooses to visit; Sidecar contacts
	// neither on its own.
	DocsURL   = "https://sidecar.haplab.com"
	IssuesURL = "https://github.com/marcus/sidecar/issues/new"
)

// Failure is one pre-render startup failure in the terms a user needs: a plain
// diagnosis, the evidence worth copying, and the next step when one is known.
type Failure struct {
	// Diagnosis is one plain-language sentence. No Go error text, no jargon.
	Diagnosis string
	// Evidence is the specific detail: a path, an error string, a position in
	// a file. Never a setting's value and never a file's contents.
	Evidence []string
	// NextStep is what the user can do about it, when Sidecar knows. Empty is
	// honest when it does not.
	NextStep string
}

// Render builds the text for a failure. It is deliberately short: a user who
// cannot start their tool is looking for one instruction, not a report.
func Render(f Failure) string {
	var b strings.Builder
	b.WriteString("Sidecar could not start.\n\n")
	b.WriteString("  " + f.Diagnosis + "\n")
	for _, line := range f.Evidence {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	if f.NextStep != "" {
		b.WriteString("\nNext: " + f.NextStep + "\n")
	}
	b.WriteString("\nIf you need help, copy the lines above — they name paths and positions,\n")
	b.WriteString("never your settings. Sidecar sends nothing anywhere on its own.\n")
	b.WriteString("  Docs   " + DocsURL + "\n")
	b.WriteString("  Issues " + IssuesURL + "\n")
	return b.String()
}

// Print writes a failure to a stream, normally stderr. The caller exits.
func Print(w io.Writer, f Failure) {
	_, _ = fmt.Fprint(w, Render(f))
}

// ConfigLoad describes a configuration file Sidecar could not read or parse.
// It reports where the file is and, for a parse error, which line the parser
// stopped on — the two facts a user needs to open an editor and fix it — and
// says plainly that Sidecar will not rewrite the file for them.
func ConfigLoad(path string, err error) Failure {
	f := Failure{
		Diagnosis: "Your configuration file could not be read.",
		NextStep:  "fix or move the file, then start Sidecar again. Sidecar never rewrites it for you.",
	}
	if path != "" {
		f.Evidence = append(f.Evidence, path)
	}
	if err != nil {
		f.Evidence = append(f.Evidence, err.Error())
	}
	if errors.Is(err, fs.ErrPermission) {
		f.Diagnosis = "Your configuration file exists but Sidecar is not allowed to read it."
		f.NextStep = "check the file's permissions, then start Sidecar again."
	}
	if line := jsonErrorLine(path, err); line != "" {
		f.Diagnosis = "A setting in your configuration file could not be parsed."
		f.Evidence = append(f.Evidence, line)
	}
	return f
}

// Isolation describes a run that asserted an isolated state tree and did not
// get one. The check's own error is the diagnosis: it already names the paths
// that failed the assertion.
func Isolation(err error) Failure {
	f := Failure{
		Diagnosis: "This run asked for an isolated state tree and would have written the real one.",
		NextStep:  "set XDG_STATE_HOME and -config to a temporary location, then run again.",
	}
	if err != nil {
		f.Evidence = append(f.Evidence, err.Error())
	}
	return f
}

// ProjectRoot describes a project directory Sidecar could not resolve.
func ProjectRoot(path string, err error) Failure {
	f := Failure{
		Diagnosis: "The project directory could not be resolved.",
		NextStep:  "run Sidecar from a directory that exists, or pass -project <path>.",
	}
	if path != "" {
		f.Evidence = append(f.Evidence, "-project "+path)
	}
	if err != nil {
		f.Evidence = append(f.Evidence, err.Error())
	}
	return f
}

// NotATerminal describes the piped or redirected stdout case. Sidecar's
// non-interactive surface is its commands, so the next step names them rather
// than suggesting a settings editor Sidecar does not have.
func NotATerminal() Failure {
	return Failure{
		Diagnosis: "Sidecar requires an interactive terminal; stdout is not one.",
		Evidence:  []string{"stdout is piped, redirected, or has no terminal attached"},
		NextStep:  "run sidecar in a terminal. For scripted use, run `sidecar help` to see the non-interactive commands.",
	}
}

// Terminal describes a terminal that accepted Sidecar and then failed under it.
func Terminal(err error) Failure {
	f := Failure{
		Diagnosis: "Sidecar started but its terminal session ended with an error.",
		NextStep:  "try again in a plain terminal window; if it repeats, the summary above is worth filing.",
	}
	if err != nil {
		f.Evidence = append(f.Evidence, err.Error())
	}
	return f
}

// jsonErrorLine turns a JSON parser's byte offset into the line number a user
// has to open. It reads the file only to count newlines up to that offset and
// returns nothing at all if it cannot — the file's contents never leave here.
func jsonErrorLine(path string, err error) string {
	var offset int64 = -1
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		offset = syntax.Offset
	case errors.As(err, &typeErr):
		offset = typeErr.Offset
	}
	if offset < 0 || path == "" {
		return ""
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || offset > int64(len(content)) {
		return ""
	}
	return fmt.Sprintf("near line %d", 1+strings.Count(string(content[:offset]), "\n"))
}
