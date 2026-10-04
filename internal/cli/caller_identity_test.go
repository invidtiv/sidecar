package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func conflictingCallerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	_, stateDir := setupIsolatedCLI(t)
	a, b := t.TempDir(), t.TempDir()
	socket := tmuxenv.Namespace()
	for key, root := range map[string]string{"a": a, "b": b} {
		writeProjectMeta(t, stateDir, key, root)
		writeProjectShell(t, stateDir, key, shellstate.Definition{TmuxName: "sidecar-sh-" + key, Namespace: socket, WorkDir: root, DisplayName: key})
		if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte(key), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "a", Path: a}, {Name: "b", Path: b}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf 'sidecar-sh-a\\t%%s\\t%%s\\n' %s %s\n", shellQuote(socket), shellQuote(a))
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-a")
	t.Setenv(shellstate.ManagedEnv, "1")
	t.Chdir(b)
	return stateDir, a, b
}

func TestImplicitCommandsRefuseInheritedCallerConflict(t *testing.T) {
	screen := agentFixture(t, "startup_idle.txt")
	for _, args := range [][]string{
		{"shell", "rename", "wrong lane"},
		{"shell", "name"},
		{"open", "doc.md", "--wait", "0"},
		{"layout", "get", "--wait", "0"},
		{"create", "shell", "--tab", "--name", "wrong lane", "--wait", "0"},
		{"project", "current", "--json"},
		{"agent", "get", "--json"},
		{"agent", "read", "--json"},
		{"agent", "prompt", "wrong lane", "--json"},
		{"agent", "start", "--kind", "codex", "--json"},
		{"agent", "broadcast", "wrong lane", "--dry-run", "--json"},
		{"agent", "broadcast", "wrong lane", "--all", "--dry-run", "--json"},
		{"agent", "broadcast", "wrong lane", "--project", "b", "--dry-run", "--json"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			stateDir, _, _ := conflictingCallerFixture(t)
			manifest := filepath.Join(stateDir, "projects", "a", "shells.json")
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			terminal := &cliAgentTerminal{launched: true, screen: screen}
			useCLIAgentTerminal(t, terminal)
			var out, errOut bytes.Buffer
			handled, code := Run(append([]string{"--enable-feature=agent_control"}, args...), &out, &errOut)
			if !handled || code == 0 || !strings.Contains(errOut.String(), "caller identity conflict: caller directory") {
				t.Fatalf("stale caller accepted or conflict lost: handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
			}
			if args[0] == "agent" && !json.Valid(errOut.Bytes()) {
				t.Fatalf("agent conflict did not use structured error: %q", errOut.String())
			}
			after, err := os.ReadFile(manifest)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("conflict mutated another lane manifest: %v", err)
			}
			requests, _ := os.ReadDir(filepath.Join(stateDir, "requests"))
			if len(requests) != 0 || terminal.inspects != 0 || len(terminal.submitted) != 0 || terminal.launchCalls != 0 {
				t.Fatal("conflict reached UI bus or agent transport")
			}
		})
	}
}

func TestExplicitSelectorsEscapeInheritedCallerConflict(t *testing.T) {
	screen := agentFixture(t, "startup_idle.txt")
	for _, args := range [][]string{
		{"shell", "rename", "--target", "sidecar-sh-b", "--project", "b", "chosen lane"},
		{"open", "--shell", "sidecar-sh-b", "doc.md", "--wait", "0"},
		{"agent", "get", "sidecar-sh-b", "--project", "b", "--json"},
		{"agent", "broadcast", "chosen scope", "--project", "b", "--raw", "--include-self", "--dry-run", "--json"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			conflictingCallerFixture(t)
			terminal := &cliAgentTerminal{launched: true, screen: screen}
			useCLIAgentTerminal(t, terminal)
			var out, errOut bytes.Buffer
			handled, code := Run(append([]string{"--enable-feature=agent_control"}, args...), &out, &errOut)
			if !handled || code != 0 {
				t.Fatalf("explicit selector refused: %v %d %q", handled, code, errOut.String())
			}
		})
	}
}

func TestImplicitCallerRealPaneAllowsCDWithoutRebindingProject(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	a, b := t.TempDir(), t.TempDir()
	privateDir, err := os.MkdirTemp("/tmp", "sc-caller-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(privateDir) })
	t.Setenv("TMUX_TMPDIR", privateDir)
	socket := tmuxenv.Namespace() // A dedicated private server, never the live server.
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	session := "sidecar-sh-caller-cd"
	writeProjectMeta(t, stateDir, "a", a)
	writeProjectMeta(t, stateDir, "b", b)
	writeProjectShell(t, stateDir, "a", shellstate.Definition{TmuxName: session, Namespace: socket, WorkDir: a, DisplayName: "owning lane"})
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "a", Path: a}, {Name: "b", Path: b}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	out, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-c", b, "/bin/bash", "--noprofile", "--norc").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "%") {
		t.Fatalf("private shell: %s: %v", out, err)
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(out)))
	t.Setenv(shellstate.SessionEnv, session)
	t.Chdir(b)
	if identity, err := currentShellIdentity(t.Context()); err != nil {
		t.Fatalf("private pane identity: %v, pane=%q", err, string(out))
	} else if canonicalOpenPath(identity.path) != canonicalOpenPath(b) {
		t.Fatalf("pane cwd=%q, want %q", identity.path, b)
	}
	var stdout, stderr bytes.Buffer
	handled, code := Run([]string{"project", "current", "--json"}, &stdout, &stderr)
	if !handled || code != 0 {
		t.Fatalf("intentional cd refused: %v %d %q", handled, code, stderr.String())
	}
	var got struct {
		Shell struct {
			Key string `json:"key"`
		} `json:"shell"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Shell.Key != "a" {
		t.Fatalf("shell rebound after cd: %q: %v", stdout.String(), err)
	}
}

func TestImplicitCallerVerifiesManagedWorktreeCue(t *testing.T) {
	stateHome, stateDir, _, _, session, socket := setupWorktreeCLI(t, "managed worktree")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(shellstate.SessionEnv, session)
	if err := validateImplicitCaller(t.Context(), stateDir); err != nil {
		t.Fatalf("registered worktree cue refused: %v", err)
	}
}

func TestImplicitCallerUsesReportedSocket(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	socket := filepath.Join(t.TempDir(), "custom.sock")
	writeProjectMeta(t, stateDir, "a", root)
	writeProjectShell(t, stateDir, "a", shellstate.Definition{TmuxName: "sidecar-sh-a", Namespace: socket, WorkDir: root})
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf 'sidecar-sh-a\\t%%s\\t%%s\\n' %s %s\n", shellQuote(socket), shellQuote(root))
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-a")
	t.Chdir(root)
	if err := validateImplicitCaller(t.Context(), stateDir); err != nil {
		t.Fatalf("reported socket ignored: %v", err)
	}
}

func TestImplicitCallerRegisteredDirectoryConflictWithoutPane(t *testing.T) {
	stateDir, a, b := conflictingCallerFixture(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	if err := validateImplicitCaller(t.Context(), stateDir); !isCallerConflict(err) {
		t.Fatalf("registered cwd %q accepted claim from %q: %v", b, a, err)
	}
	orig := projectOrigin(Env{StateDir: stateDir})
	if orig.TmuxSession != "" || orig.ProjectKey != "" || canonicalOpenPath(orig.WorkDir) != canonicalOpenPath(b) {
		t.Fatalf("explicit mutation metadata retained wrong lane: %+v", orig)
	}
	t.Chdir(a)
	if err := validateImplicitCaller(t.Context(), stateDir); err != nil {
		t.Fatalf("consistent owned directory refused: %v", err)
	}
}
