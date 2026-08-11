package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
)

func TestRunDispatch(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		handled  bool
		code     int
		contains string
	}{
		{"legacy", []string{"--version"}, false, 0, ""},
		{"shell help", []string{"shell", "--help"}, true, 0, "sidecar shell <command>"},
		{"name help", []string{"shell", "name", "--help"}, true, 0, "Print the Sidecar display name"},
		{"rename help", []string{"shell", "rename", "--help"}, true, 0, "Sidecar project shell containing"},
		{"unknown", []string{"shell", "wat"}, true, 2, "unknown shell command"},
		{"name positional", []string{"shell", "name", "extra"}, true, 2, "no positional"},
		{"missing name", []string{"shell", "rename"}, true, 2, "exactly one quoted"},
		{"unquoted name", []string{"shell", "rename", "two", "words"}, true, 2, "exactly one quoted"},
		{"invalid name", []string{"shell", "rename", "bad\nname"}, true, 2, "control characters"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if handled != tt.handled || code != tt.code {
				t.Fatalf("Run() = %v,%d", handled, code)
			}
			if tt.contains != "" && !strings.Contains(out.String()+errOut.String(), tt.contains) {
				t.Fatalf("output %q missing %q", out.String()+errOut.String(), tt.contains)
			}
		})
	}
}

func TestRunRenameJSONSteelThread(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "stale")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--json", "  current work  "}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("Run() = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellstate.RenameResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if !result.Changed || result.OldName != "stale" || result.Name != "current work" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunNameSteelThread(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "prior task context")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "name"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("Run(name) = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "prior task context" {
		t.Fatalf("human name = %q", got)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"shell", "name", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("Run(name --json) = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellstate.LookupResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if result.Shell != "sidecar-sh-sidecar-1" || result.Name != "prior task context" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// setupShellCLI installs a fake tmux that reports a fixed session/socket and a
// matching shells.json under an isolated XDG_STATE_HOME tree.
func setupShellCLI(t *testing.T, displayName string) (stateHome, socket string) {
	t.Helper()
	stateHome = t.TempDir()
	projectDir := filepath.Join(stateHome, "sidecar", "projects", "sidecar")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	socket = filepath.Join(t.TempDir(), "tmux.sock")
	manifest := `{"version":1,"shells":[{"tmuxName":"sidecar-sh-sidecar-1","displayName":` + quoteJSON(t, displayName) + `,"namespace":` + quoteJSON(t, socket) + `}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "shells.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	tmux := filepath.Join(binDir, "tmux")
	script := "#!/bin/sh\nprintf 'sidecar-sh-sidecar-1\\t%s\\n' " + shellQuote(socket) + "\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stateHome, socket
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
