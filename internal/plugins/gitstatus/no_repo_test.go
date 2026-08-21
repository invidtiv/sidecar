package gitstatus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/gitinit"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestInit_NoRepoKeepsPluginAvailable(t *testing.T) {
	tmp := t.TempDir()

	p := New()
	err := p.Init(&plugin.Context{WorkDir: tmp})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if p.hasRepo {
		t.Fatalf("hasRepo = true, want false")
	}
	if p.tree == nil {
		t.Fatalf("tree is nil, want non-nil")
	}
	if got := p.FocusContext(); got != "git-no-repo" {
		t.Fatalf("FocusContext() = %q, want %q", got, "git-no-repo")
	}
	if cmd := p.Start(); cmd == nil {
		t.Fatalf("Start() should detect repositories asynchronously")
	}
}

func TestInit_SwitchRepoToNoRepoClearsRepoState(t *testing.T) {
	repoDir := t.TempDir()
	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	p := New()
	if err := p.Init(&plugin.Context{WorkDir: repoDir}); err != nil {
		t.Fatalf("Init(repo) error = %v", err)
	}
	p.activateRepo(repoDir)
	if !p.hasRepo {
		t.Fatalf("hasRepo = false after repo init")
	}
	if p.repoRoot == "" {
		t.Fatalf("repoRoot is empty after repo init")
	}

	noRepoDir := t.TempDir()
	if err := p.Init(&plugin.Context{WorkDir: noRepoDir}); err != nil {
		t.Fatalf("Init(no-repo) error = %v", err)
	}
	if p.hasRepo {
		t.Fatalf("hasRepo = true after switching to no-repo dir")
	}
	if p.repoRoot != "" {
		t.Fatalf("repoRoot = %q, want empty", p.repoRoot)
	}
}

func TestEnsureGitignoreEntries_AddAndIdempotent(t *testing.T) {
	tmp := t.TempDir()
	gitignore := filepath.Join(tmp, ".gitignore")

	if err := os.WriteFile(gitignore, []byte("node_modules/\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := ensureGitignoreEntries(tmp, sidecarGitignoreEntries); err != nil {
		t.Fatalf("ensureGitignoreEntries() first call error = %v", err)
	}
	if err := ensureGitignoreEntries(tmp, sidecarGitignoreEntries); err != nil {
		t.Fatalf("ensureGitignoreEntries() second call error = %v", err)
	}

	data, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, entry := range sidecarGitignoreEntries {
		if strings.Count(content, entry) != 1 {
			t.Fatalf("%q count = %d, want 1\ncontent:\n%s", entry, strings.Count(content, entry), content)
		}
	}
}

func TestEnsureGitignoreEntries_AllSidecarEntries(t *testing.T) {
	tmp := t.TempDir()

	// Verify all expected sidecar state paths are covered
	expected := []string{
		".todos/",
		".sidecar/",
		".sidecar-agent",
		".sidecar-task",
		".sidecar-pr",
		".sidecar-start.sh",
		".sidecar-base",
		".td-root",
	}
	for _, e := range expected {
		found := false
		for _, s := range sidecarGitignoreEntries {
			if s == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sidecarGitignoreEntries missing expected entry %q", e)
		}
	}

	// Ensure entries are applied cleanly to an empty .gitignore
	if err := ensureGitignoreEntries(tmp, sidecarGitignoreEntries); err != nil {
		t.Fatalf("ensureGitignoreEntries() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, entry := range expected {
		if !strings.Contains(content, entry) {
			t.Errorf(".gitignore missing entry %q\ncontent:\n%s", entry, content)
		}
	}
}

func TestStart_DoesNotMutateGitignoreForExistingRepo(t *testing.T) {
	repoDir := t.TempDir()
	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	// Write a .gitignore that deliberately omits sidecar entries
	gitignore := filepath.Join(repoDir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("node_modules/\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	p := New()
	if err := p.Init(&plugin.Context{WorkDir: repoDir}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned nil; repository detection must be asynchronous")
	}

	data, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if content != "node_modules/\n" {
		t.Fatalf("Start() mutated .gitignore synchronously: %q", content)
	}
}

func TestInitAndStartDoNotSpawnGitSynchronously(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "git-called")
	shim := filepath.Join(tmp, "git")
	content := "#!/bin/sh\nprintf called >\"$SIDECAR_TEST_GIT_MARKER\"\n"
	if err := os.WriteFile(shim, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	t.Setenv("SIDECAR_TEST_GIT_MARKER", marker)
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp}); err != nil {
		t.Fatal(err)
	}
	if cmd := p.Start(); cmd == nil {
		t.Fatal("Start() returned nil")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Init or Start synchronously spawned git: %v", err)
	}
}

func TestDiagnosticsReportDegradedGitData(t *testing.T) {
	p := New()
	p.hasRepo = true
	p.tree = NewFileTree(t.TempDir())
	p.statusError = "status unavailable"
	p.historyError = "history unavailable"
	p.watcherError = "watcher unavailable"

	got := p.Diagnostics()
	if len(got) != 4 {
		t.Fatalf("Diagnostics() count = %d, want 4: %#v", len(got), got)
	}
	want := map[string]string{
		"git-status-refresh": "status unavailable",
		"git-history":        "history unavailable",
		"git-watcher":        "watcher unavailable",
	}
	for _, diagnostic := range got {
		if detail, ok := want[diagnostic.ID]; ok {
			if diagnostic.Status != "warn" || diagnostic.Detail != detail {
				t.Errorf("diagnostic %q = %#v", diagnostic.ID, diagnostic)
			}
			delete(want, diagnostic.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics: %#v", want)
	}
}

func TestInitRepo_CreatesMainBranch(t *testing.T) {
	tmp := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp, Epoch: 1}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	msg := p.initRepo()()
	done, ok := msg.(RepoInitDoneMsg)
	if !ok {
		t.Fatalf("initRepo produced %T, want RepoInitDoneMsg", msg)
	}
	if done.Err != nil {
		t.Fatalf("initRepo error = %v", done.Err)
	}
	if done.Root == "" {
		t.Fatal("initRepo returned empty root")
	}

	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = tmp
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "main" {
		t.Fatalf("HEAD = %q, want main", got)
	}
}

func TestNoRepoView_RegistersPaddedInitButton(t *testing.T) {
	tmp := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	view := p.View(80, 24)
	if !strings.Contains(view, "Initialize Git Repository") {
		t.Fatalf("no-repo view missing init button:\n%s", view)
	}
	if !strings.Contains(view, "worktrees") {
		t.Fatalf("no-repo view missing git explanation:\n%s", view)
	}

	var found bool
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionInitRepo {
			found = true
			if region.Rect.W < 8 || region.Rect.H != 1 {
				t.Fatalf("init button hit region too small: %+v", region.Rect)
			}
		}
	}
	if !found {
		t.Fatal("Initialize Git Repository has no hit region")
	}
	if strings.Contains(view, "Press i") || strings.Contains(view, "Press r") {
		t.Fatalf("no-repo view duplicated footer key hints:\n%s", view)
	}
}

func TestNoRepoReadyMsgActivatesRepo(t *testing.T) {
	tmp := t.TempDir()
	if _, err := gitinit.Init(tmp); err != nil {
		t.Fatalf("git init: %v", err)
	}
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: t.TempDir(), Epoch: 1}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if p.hasRepo {
		t.Fatal("hasRepo true before ReadyMsg")
	}

	updatedPlugin, cmd := p.Update(gitinit.ReadyMsg{Root: tmp})
	updated, ok := updatedPlugin.(*Plugin)
	if !ok {
		t.Fatalf("updated plugin type = %T", updatedPlugin)
	}
	if !updated.hasRepo || updated.repoRoot != tmp {
		t.Fatalf("ReadyMsg did not activate repo: hasRepo=%v root=%q", updated.hasRepo, updated.repoRoot)
	}
	if cmd == nil {
		t.Fatal("ReadyMsg produced no reload command")
	}
}

func TestNoRepoMouse_InitClickStartsInit(t *testing.T) {
	tmp := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	p.View(80, 24)

	var target mouse.Region
	var found bool
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionInitRepo {
			target = region
			found = true
		}
	}
	if !found {
		t.Fatal("no init button hit region")
	}

	updatedPlugin, cmd := p.Update(tea.MouseClickMsg{
		X: target.Rect.X, Y: target.Rect.Y, Button: tea.MouseLeft,
	})
	updated, ok := updatedPlugin.(*Plugin)
	if !ok {
		t.Fatalf("updated plugin type = %T", updatedPlugin)
	}
	if !updated.repoInitInProgress {
		t.Fatal("click did not start init")
	}
	if cmd == nil {
		t.Fatal("click produced no init command")
	}
}

func TestUpdateNoRepo_EnterStartsInit(t *testing.T) {
	tmp := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	updatedPlugin, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, ok := updatedPlugin.(*Plugin)
	if !ok {
		t.Fatalf("updated plugin type = %T, want *Plugin", updatedPlugin)
	}
	if !updated.repoInitInProgress || cmd == nil {
		t.Fatal("enter did not start init")
	}
}

func TestUpdateNoRepo_InitKeyStartsInit(t *testing.T) {
	tmp := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: tmp}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	updatedPlugin, cmd := p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	updated, ok := updatedPlugin.(*Plugin)
	if !ok {
		t.Fatalf("updated plugin type = %T, want *Plugin", updatedPlugin)
	}
	if !updated.repoInitInProgress {
		t.Fatalf("repoInitInProgress = false, want true")
	}
	if cmd == nil {
		t.Fatalf("expected init command, got nil")
	}
}
