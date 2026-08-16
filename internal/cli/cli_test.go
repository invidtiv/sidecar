package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
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
		{"rename help", []string{"shell", "rename", "--help"}, true, 0, "Sidecar-managed shell or worktree agent"},
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

func TestRunShellCommandsResolveManagedWorktreeAgent(t *testing.T) {
	stateHome, stateDir, projectRoot, worktreeRoot, session, socket := setupWorktreeCLI(t, "panes")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	if handled, code := Run([]string{"shell", "name", "--json"}, &out, &errOut); !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("name = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var lookup shellstate.LookupResult
	if err := json.Unmarshal(out.Bytes(), &lookup); err != nil {
		t.Fatalf("name output = %q: %v", out.String(), err)
	}
	if lookup.Shell != session || lookup.Name != "panes" {
		t.Fatalf("lookup = %+v", lookup)
	}

	out.Reset()
	errOut.Reset()
	if handled, code := Run([]string{"shell", "rename", "--json", "trim pane handles"}, &out, &errOut); !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("rename = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var renamed shellstate.RenameResult
	if err := json.Unmarshal(out.Bytes(), &renamed); err != nil {
		t.Fatalf("rename output = %q: %v", out.String(), err)
	}
	if !renamed.Changed || renamed.OldName != "panes" || renamed.Name != "trim pane handles" {
		t.Fatalf("rename = %+v", renamed)
	}
	if got, err := workspaceops.LookupWorktreeDisplayName(stateDir, projectRoot, worktreeRoot); err != nil || got != "trim pane handles" {
		t.Fatalf("persisted name = %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "requests"))
	if err != nil {
		t.Fatalf("read repaint requests: %v", err)
	}
	var repaint uirequest.Request
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-rename-worktree.json") {
			continue
		}
		repaint, err = uirequest.ReadRequest(filepath.Join(stateDir, "requests", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
	}
	if repaint.Action != uirequest.ActionRenameWorktree || repaint.Origin.WorkDir != worktreeRoot || repaint.Target.Value != "trim pane handles" {
		t.Fatalf("repaint request = %+v", repaint)
	}
}

func TestRunShellNameRejectsLookalikeWorktreeSession(t *testing.T) {
	stateHome, _, _, _, _, socket := setupWorktreeCLI(t, "panes")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("FAKE_TMUX_SESSION", "sidecar-ws-lookalike")

	var out, errOut bytes.Buffer
	if handled, code := Run([]string{"shell", "name"}, &out, &errOut); !handled || code != 1 {
		t.Fatalf("name = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "does not match its Sidecar worktree identity") {
		t.Fatalf("unexpected refusal: %q", errOut.String())
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

func setupWorktreeCLI(t *testing.T, displayName string) (stateHome, stateDir, projectRoot, worktreeRoot, session, socket string) {
	t.Helper()
	stateHome = t.TempDir()
	stateDir = filepath.Join(stateHome, "sidecar")
	projectRoot = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projectRoot); err == nil {
		projectRoot = resolved
	}
	worktreeRoot = projectRoot
	cmd := exec.Command("git", "init", "-q", projectRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if _, err := projectdir.WorktreeDirWithBase(stateDir, projectRoot, worktreeRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceops.RenameWorktreeDisplayName(t.Context(), stateDir, projectRoot, worktreeRoot, displayName); err != nil {
		t.Fatal(err)
	}
	session = workspaceops.WorktreeSessionName(worktreeRoot, "")
	socket = filepath.Join(t.TempDir(), "tmux.sock")
	binDir := t.TempDir()
	tmux := filepath.Join(binDir, "tmux")
	script := "#!/bin/sh\nprintf '%s\\t%s\\t%s\\n' \"${FAKE_TMUX_SESSION:-" + session + "}\" " + shellQuote(socket) + " " + shellQuote(worktreeRoot) + "\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stateHome, stateDir, projectRoot, worktreeRoot, session, socket
}

// setupIsolatedCLI points state at a temp tree and clears TMUX so open
// resolution cannot see a Sidecar shell.
func setupIsolatedCLI(t *testing.T) (stateHome, stateDir string) {
	t.Helper()
	stateHome = t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	stateDir = filepath.Join(stateHome, "sidecar")
	if err := os.MkdirAll(filepath.Join(stateDir, "projects"), 0755); err != nil {
		t.Fatal(err)
	}
	return stateHome, stateDir
}

func writeProjectMeta(t *testing.T, stateDir, slug, workDir string) {
	t.Helper()
	dir := filepath.Join(stateDir, "projects", slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"path":`+quoteJSON(t, workDir)+`}`), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeProjectShell(t *testing.T, stateDir, slug string, shell shellstate.Definition) {
	t.Helper()
	dir := filepath.Join(stateDir, "projects", slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := struct {
		Version int                     `json:"version"`
		Shells  []shellstate.Definition `json:"shells"`
	}{Version: 1, Shells: []shellstate.Definition{shell}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shells.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
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
