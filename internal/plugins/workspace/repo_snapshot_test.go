package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

func TestBuildRepoSnapshotCarriesStableIndependentIdentity(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runSnapshotGit(t, "", "init", "-b", "main", repo)
	runSnapshotGit(t, repo, "config", "user.email", "test@example.com")
	runSnapshotGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runSnapshotGit(t, repo, "add", "README")
	runSnapshotGit(t, repo, "commit", "-m", "init")
	remote := filepath.Join(t.TempDir(), "origin.git")
	runSnapshotGit(t, "", "init", "--bare", remote)
	runSnapshotGit(t, repo, "remote", "add", "origin", remote)
	runSnapshotGit(t, repo, "push", "-u", "origin", "main")
	root := t.TempDir()
	feature := filepath.Join(root, "feature", "auth")
	fix := filepath.Join(root, "fix", "auth")
	runSnapshotGit(t, repo, "worktree", "add", "-b", "feature/auth", feature)
	runSnapshotGit(t, repo, "worktree", "add", "-b", "fix/auth", fix)

	snapshot, err := BuildRepoSnapshot(context.Background(), feature)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Key == "" || snapshot.CanonicalCommonDir == "" || snapshot.CanonicalRoot != canonicalGitPath(repo) {
		t.Fatalf("incomplete repository identity: %+v", snapshot)
	}
	if len(snapshot.Worktrees) != 3 {
		t.Fatalf("worktrees = %d, want 3", len(snapshot.Worktrees))
	}
	seen := map[string]WorktreeSnapshot{}
	for _, wt := range snapshot.Worktrees {
		if wt.Key == "" || wt.RepoKey != snapshot.Key || wt.HEADOID == "" {
			t.Fatalf("incomplete worktree snapshot: %+v", wt)
		}
		if _, duplicate := seen[wt.Key]; duplicate {
			t.Fatalf("duplicate stable key %q", wt.Key)
		}
		seen[wt.Key] = wt
	}
	if snapshot.CheckedOut["feature/auth"] == snapshot.CheckedOut["fix/auth"] {
		t.Fatal("same-basename worktrees share checked-out identity")
	}
	for _, wt := range snapshot.Worktrees {
		if wt.Branch == "main" && (wt.Remote != "origin" || wt.Upstream != "origin/main") {
			t.Fatalf("main remote identity = remote %q upstream %q", wt.Remote, wt.Upstream)
		}
	}

	restarted, err := BuildRepoSnapshot(context.Background(), fix)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Key != snapshot.Key || restarted.CheckedOut["feature/auth"] != snapshot.CheckedOut["feature/auth"] {
		t.Fatal("snapshot identity changed across worktree/restart context")
	}
}

func TestScopedResultRejectedAfterSwitch(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 8}
	p.repoSnapshot = &RepoSnapshot{Key: "repo-new"}
	p.worktrees = []*Worktree{{Key: "new-wt", RepoKey: "repo-new", Name: "auth"}}
	stale := OperationScope{Epoch: 7, OperationID: "7-1", RepoKey: "repo-old", WorktreeKey: "old-wt"}
	if p.scopeMatches(stale) {
		t.Fatal("stale switched-project result was accepted")
	}
	current := OperationScope{Epoch: 8, OperationID: "8-1", RepoKey: "repo-new", WorktreeKey: "new-wt"}
	if !p.scopeMatches(current) {
		t.Fatal("current operation result was rejected")
	}
	p.activeLifecycleOperationID = "8-live"
	current.Lifecycle = true
	if p.scopeMatches(current) {
		t.Fatal("result from replaced lifecycle operation was accepted")
	}
}

func TestInitCancelsPriorOperationsAndResetsLifecycleState(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	oldCtx := p.operationCtx
	p.mergeState = &MergeWorkflowState{Worktree: &Worktree{Name: "old"}}
	p.linkingWorktree = &Worktree{Name: "old"}
	p.fetchPRLoading = true
	if err := p.Init(&plugin.Context{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("reinit did not cancel prior subprocess context")
	}
	if p.mergeState != nil || p.linkingWorktree != nil || p.fetchPRLoading {
		t.Fatal("reinit retained lifecycle/modal state")
	}
}

func TestDelayedPROperationIsCancelledAndCannotMutateSwitchedProject(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "started")
	gh := filepath.Join(binDir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)

	p := New()
	if err := p.Init(&plugin.Context{Epoch: 3, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	cmd := p.fetchPRList()
	result := make(chan FetchPRListMsg, 1)
	go func() { result <- cmd().(FetchPRListMsg) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delayed gh command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Init(&plugin.Context{Epoch: 4, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-result:
		p.fetchPRItems = []PRListItem{{Number: 99}}
		p.update(msg)
		if len(p.fetchPRItems) != 1 || p.fetchPRItems[0].Number != 99 {
			t.Fatal("stale cancelled PR result mutated the switched project")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled gh subprocess did not return promptly")
	}
}

func TestLifecycleScopeWithoutExplicitWorktreeCarriesAllIdentity(t *testing.T) {
	p := New()
	workDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 2, WorkDir: workDir, ProjectRoot: workDir}); err != nil {
		t.Fatal(err)
	}
	_, scope := p.newLifecycleScope(nil)
	if scope.Epoch != 2 || scope.OperationID == "" || scope.RepoKey == "" || scope.WorktreeKey == "" {
		t.Fatalf("incomplete lifecycle scope: %+v", scope)
	}
}

func TestPushCommandCancelledByReinit(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "push-started")
	git := filepath.Join(binDir, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	p := New()
	oldDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 10, WorkDir: oldDir, ProjectRoot: oldDir}); err != nil {
		t.Fatal(err)
	}
	p.worktrees = []*Worktree{{Key: "old-key", RepoKey: "old-repo", Name: "feature", Path: oldDir, Branch: "feature"}}
	p.selectedIdx = 0
	cmd := p.pushSelected()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	waitForFile(t, marker)
	newDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 11, WorkDir: newDir, ProjectRoot: newDir}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("git push subprocess survived reinit cancellation")
	}
}

func TestTaskCommandCancelledByStop(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "td-started")
	td := filepath.Join(binDir, "td")
	if err := os.WriteFile(td, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	p := New()
	workDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 12, WorkDir: workDir, ProjectRoot: workDir}); err != nil {
		t.Fatal(err)
	}
	cmd := p.loadOpenTasks()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	waitForFile(t, marker)
	p.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("td subprocess survived Stop cancellation")
	}
}

func TestCleanupCommandCancelledByStop(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cleanup-started")
	git := filepath.Join(binDir, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	p := New()
	mainDir, wtDir := t.TempDir(), t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 14, WorkDir: wtDir, ProjectRoot: mainDir}); err != nil {
		t.Fatal(err)
	}
	wt := &Worktree{Key: "wt-key", RepoKey: "repo-key", Name: "feature", Path: wtDir, Branch: "feature"}
	p.worktrees = []*Worktree{wt}
	_, scope := p.newLifecycleScope(wt)
	cmd := p.performSelectedCleanup(wt, &MergeWorkflowState{OperationScope: scope, Worktree: wt, TargetBranch: "main", DeleteLocalWorktree: true})
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	waitForFile(t, marker)
	p.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup git subprocess survived Stop cancellation")
	}
}

func TestMergeLifecycleGitCommandsCancelledByStopOrReinit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		start  func(*Plugin, *Worktree) tea.Cmd
		cancel func(*testing.T, *Plugin)
	}{
		{
			name: "status on Stop",
			start: func(p *Plugin, wt *Worktree) tea.Cmd {
				return p.startMergeWorkflow(wt)
			},
			cancel: func(_ *testing.T, p *Plugin) { p.Stop() },
		},
		{
			name: "stage on Init",
			start: func(p *Plugin, wt *Worktree) tea.Cmd {
				p.newLifecycleScope(wt)
				return p.stageAllAndCommit(wt, "test commit")
			},
			cancel: func(t *testing.T, p *Plugin) {
				newDir := t.TempDir()
				if err := p.Init(&plugin.Context{Epoch: 31, WorkDir: newDir, ProjectRoot: newDir}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			marker := filepath.Join(t.TempDir(), "git-started")
			git := filepath.Join(binDir, "git")
			if err := os.WriteFile(git, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SIDECAR_TEST_MARKER", marker)

			p := New()
			workDir := t.TempDir()
			if err := p.Init(&plugin.Context{Epoch: 30, WorkDir: workDir, ProjectRoot: workDir}); err != nil {
				t.Fatal(err)
			}
			wt := &Worktree{Key: "worktree-key", RepoKey: "repo-key", Name: "feature", Path: workDir, Branch: "feature"}
			p.worktrees = []*Worktree{wt}
			cmd := tc.start(p, wt)
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()
			waitForFile(t, marker)
			tc.cancel(t, p)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("merge lifecycle git subprocess survived cancellation")
			}
		})
	}
}

func TestCreatePostAddInventoryCancelledWithPartialResult(t *testing.T) {
	for _, cancelWithInit := range []bool{false, true} {
		name := "Stop"
		if cancelWithInit {
			name = "Init"
		}
		t.Run(name, func(t *testing.T) {
			binDir := t.TempDir()
			marker := filepath.Join(t.TempDir(), "inventory-started")
			count := filepath.Join(t.TempDir(), "git-count")
			git := filepath.Join(binDir, "git")
			script := "#!/bin/sh\nn=0\n[ ! -f \"$SIDECAR_TEST_COUNT\" ] || n=$(cat \"$SIDECAR_TEST_COUNT\")\nn=$((n + 1))\nprintf '%s' \"$n\" > \"$SIDECAR_TEST_COUNT\"\nif [ \"$n\" -eq 1 ]; then exit 0; fi\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"
			if err := os.WriteFile(git, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SIDECAR_TEST_MARKER", marker)
			t.Setenv("SIDECAR_TEST_COUNT", count)

			p := New()
			oldDir := t.TempDir()
			if err := p.Init(&plugin.Context{Epoch: 40, WorkDir: oldDir, ProjectRoot: oldDir}); err != nil {
				t.Fatal(err)
			}
			p.initCreateModalBase()
			p.createNameInput.SetValue("feature")
			cmd := p.createWorktree()
			done := make(chan CreateDoneMsg, 1)
			go func() { done <- cmd().(CreateDoneMsg) }()
			waitForFile(t, marker)
			if cancelWithInit {
				newDir := t.TempDir()
				if err := p.Init(&plugin.Context{Epoch: 41, WorkDir: newDir, ProjectRoot: newDir}); err != nil {
					t.Fatal(err)
				}
			} else {
				p.Stop()
			}
			select {
			case msg := <-done:
				if msg.Worktree == nil || msg.Worktree.Branch != "feature" {
					t.Fatalf("post-add cancellation lost partial worktree: %+v", msg.Worktree)
				}
				if msg.Err == nil {
					t.Fatal("post-add cancellation returned no error")
				}
				if cancelWithInit {
					p.worktrees = []*Worktree{{Key: "new-key", Name: "new-project"}}
					p.update(msg)
					if len(p.worktrees) != 1 || p.worktrees[0].Key != "new-key" {
						t.Fatal("stale partial create result mutated the new project")
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatal("post-add worktree inventory survived cancellation")
			}
		})
	}
}

func TestPRImportDefaultBranchDiscoveryCancelledAfterAdd(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "default-branch-started")
	count := filepath.Join(t.TempDir(), "git-count")
	git := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nn=0\n[ ! -f \"$SIDECAR_TEST_COUNT\" ] || n=$(cat \"$SIDECAR_TEST_COUNT\")\nn=$((n + 1))\nprintf '%s' \"$n\" > \"$SIDECAR_TEST_COUNT\"\nif [ \"$n\" -le 2 ]; then exit 0; fi\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"
	if err := os.WriteFile(git, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	t.Setenv("SIDECAR_TEST_COUNT", count)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p := New()
	oldDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 50, WorkDir: oldDir, ProjectRoot: oldDir}); err != nil {
		t.Fatal(err)
	}
	cmd := p.fetchAndCreateWorktree(PRListItem{Branch: "feature", URL: "https://example.test/pr/1"})
	done := make(chan FetchPRDoneMsg, 1)
	go func() { done <- cmd().(FetchPRDoneMsg) }()
	waitForFile(t, marker)
	newDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 51, WorkDir: newDir, ProjectRoot: newDir}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-done:
		if msg.Worktree == nil || msg.Worktree.Branch != "feature" || msg.Err == nil {
			t.Fatalf("PR import cancellation did not retain partial result: worktree=%+v err=%v", msg.Worktree, msg.Err)
		}
		p.worktrees = []*Worktree{{Key: "new-key", Name: "new-project"}}
		p.update(msg)
		if len(p.worktrees) != 1 || p.worktrees[0].Key != "new-key" {
			t.Fatal("stale partial PR import mutated the new project")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PR default-branch discovery survived reinit cancellation")
	}
}

func TestDelayedTaskAndBranchResultsCannotMutateSwitchedProject(t *testing.T) {
	binDir := t.TempDir()
	markerDir := t.TempDir()
	for name, body := range map[string]string{
		"td":  "#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKERS/td\"\nexec sleep 30\n",
		"git": "#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKERS/git\"\nexec sleep 30\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKERS", markerDir)
	p := New()
	oldDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 20, WorkDir: oldDir, ProjectRoot: oldDir}); err != nil {
		t.Fatal(err)
	}
	taskCmd, branchCmd := p.loadOpenTasks(), p.loadBranches()
	results := make(chan tea.Msg, 2)
	go func() { results <- taskCmd() }()
	go func() { results <- branchCmd() }()
	waitForFile(t, filepath.Join(markerDir, "td"))
	waitForFile(t, filepath.Join(markerDir, "git"))
	newDir := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 21, WorkDir: newDir, ProjectRoot: newDir}); err != nil {
		t.Fatal(err)
	}
	p.taskSearchAll = []Task{{ID: "new"}}
	p.branchAll = []string{"new"}
	for range 2 {
		select {
		case msg := <-results:
			p.update(msg)
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled modal loader did not return")
		}
	}
	if len(p.taskSearchAll) != 1 || p.taskSearchAll[0].ID != "new" || len(p.branchAll) != 1 || p.branchAll[0] != "new" {
		t.Fatalf("stale modal result mutated new project: tasks=%v branches=%v", p.taskSearchAll, p.branchAll)
	}
}

func TestAgentOutputRoutesInteractiveCursorByStableKey(t *testing.T) {
	agent := &Agent{OutputBuf: tty.NewOutputBuffer(outputBufferCap)}
	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.worktrees = []*Worktree{{Key: "stable-key", Name: "feature/auth", Agent: agent}}
	p.selectedIdx = 0
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true}
	gen := p.pollScheduler.Invalidate(agentPollKey("stable-key"))
	p.update(AgentOutputMsg{WorkspaceName: "stable-key", Generation: gen, HasCursor: true, CursorRow: 7, CursorCol: 9, CursorVisible: true})
	if p.interactiveState.CursorRow != 7 || p.interactiveState.CursorCol != 9 {
		t.Fatalf("keyed cursor result not applied: %d,%d", p.interactiveState.CursorRow, p.interactiveState.CursorCol)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("command marker %s not created", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runSnapshotGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
