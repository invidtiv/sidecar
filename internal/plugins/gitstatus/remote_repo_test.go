package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/reposervice"
)

const (
	remoteMarker   = "REMOTE-MARKER.md"
	remoteBranch   = "host-branch"
	twinMarker     = "LOCAL-TWIN.md"
	twinBranchName = "local-twin-branch"
)

// fakeRepoSource answers from a fixed status and records how often it was asked.
type fakeRepoSource struct {
	status RepoStatus
	err    error
	calls  int
}

func (s *fakeRepoSource) Status(context.Context) (RepoStatus, error) {
	s.calls++
	return s.status, s.err
}

// plantLocalTwin creates a same-named checkout on this machine, with its own
// branch and its own staged file. Anything of it reaching a bound pane is the
// failure the remote-project work exists to prevent, so every bound test runs
// with one on disk.
func plantLocalTwin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init")
	git("checkout", "-b", twinBranchName)
	if err := os.WriteFile(filepath.Join(root, twinMarker), []byte("this machine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", twinMarker)

	// The twin is real: nothing asserted against it below is vacuous.
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil || !strings.Contains(string(out), twinMarker) {
		t.Fatalf("twin fixture is not a repository with a staged %s: %q %v", twinMarker, out, err)
	}
	return root
}

// hostStatusResult is what the host's `sidecar repo status --json` answers.
func hostStatusResult() reposervice.StatusResult {
	return reposervice.StatusResult{
		Kind:        reposervice.KindStatus,
		Workspace:   "/home/me/sidecar:worktree:/home/me/sidecar",
		Branch:      remoteBranch,
		Head:        "4f2b91c8ab",
		HasUpstream: true,
		Upstream:    "origin/" + remoteBranch,
		Ahead:       2,
		Behind:      1,
		State:       reposervice.StateRebase,
		Files: []reposervice.StatusFile{
			{
				Path: remoteMarker, Status: "M", Staged: true, Unstaged: true,
				StagedAdditions: 3, StagedDeletions: 1,
				UnstagedAdditions: 7, UnstagedDeletions: 2,
			},
			{Path: "host-only.txt", Status: "?", Unstaged: true, Untracked: true},
		},
	}
}

// boundGitPlugin binds a plugin to a host, with a local twin planted on this
// disk and ctx still carrying the twin's paths — so a plugin that reached for
// ctx.WorkDir would find a repository, and show it.
func boundGitPlugin(t *testing.T, ctx *plugin.Context) (*Plugin, string) {
	t.Helper()
	twin := plantLocalTwin(t)
	ctx.WorkDir = twin
	ctx.ProjectRoot = twin
	p := New()
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	// A deterministic sidebar: the assertions below are about which machine's
	// rows appear, not about where a narrow pane truncates them.
	p.sidebarWidth = 44
	return p, twin
}

func connectedHostContext() *plugin.Context {
	return &plugin.Context{
		HostID:       "aerie",
		ProjectKey:   "/home/me/sidecar",
		RemoteRunner: func(context.Context, string, []string, any) error { return nil },
		HostVerbs:    func() hostproto.VerbCapabilities { return hostproto.VerbCapabilities{RepoReadV1: true} },
		HostShows:    func() bool { return true },
	}
}

// applyStatus runs a load command and feeds its answer back, the way the app's
// event loop does.
func applyStatus(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no status command")
	}
	msg := cmd()
	if _, ok := msg.(StatusSnapshotLoadedMsg); !ok {
		t.Fatalf("status load produced %#v", msg)
	}
	if _, follow := p.Update(msg); follow != nil {
		_ = follow()
	}
}

func TestBoundStatusPaneShowsTheHostAndNeverTheLocalTwin(t *testing.T) {
	src := &fakeRepoSource{status: remoteRepoStatus(hostStatusResult())}
	p, twin := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src

	applyStatus(t, p, p.Start())

	view := p.View(160, 40)
	for _, want := range []string{remoteMarker, "host-only.txt", remoteBranch, "rebasing"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing the host's %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{twinMarker, twinBranchName} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("bound pane showed this machine's twin %q:\n%s", unwanted, view)
		}
	}
	// History and patches are later slices, and the pane says so rather than
	// reporting an empty history or inviting a selection that does nothing.
	for _, want := range []string{
		"Commits are not read from [aerie] yet",
		"Patches are not read from [aerie] yet",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
	if p.recentCommits != nil {
		t.Errorf("recentCommits = %+v, want none", p.recentCommits)
	}
	if _, err := os.Stat(filepath.Join(twin, twinMarker)); err != nil {
		t.Fatalf("twin fixture missing: %v", err)
	}
	if src.calls != 1 {
		t.Errorf("status calls = %d, want 1", src.calls)
	}
}

// TestBoundStatusPaneRunsNoLocalGit is the tripwire the seam exists for: a
// bound pane driven through its whole lifecycle, with a git that fails and
// records every invocation, must never invoke it.
func TestBoundStatusPaneRunsNoLocalGit(t *testing.T) {
	ctx := connectedHostContext()
	p, _ := boundGitPlugin(t, ctx)

	// The twin was built with the real git; from here git is a recorder.
	log := filepath.Join(t.TempDir(), "git-calls")
	binDir := t.TempDir()
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SIDECAR_GIT_CALL_LOG\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIDECAR_GIT_CALL_LOG", log)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := hostStatusResult()
	ctx.RemoteRunner = func(_ context.Context, hostID string, args []string, out any) error {
		if hostID != "aerie" {
			t.Errorf("verb addressed to %q", hostID)
		}
		want := "repo status --workspace /home/me/sidecar:worktree:/home/me/sidecar --json"
		if got := strings.Join(args, " "); got != want {
			t.Errorf("args = %q, want %q", got, want)
		}
		status, ok := out.(*reposervice.StatusResult)
		if !ok {
			t.Fatalf("runner asked to decode into %T", out)
		}
		*status = result
		return nil
	}

	applyStatus(t, p, p.Start())
	if _, cmd := p.Update(app.PluginFocusedMsg{}); cmd != nil {
		applyStatus(t, p, cmd)
	}
	// Movement, and every write key the local pane binds.
	for _, key := range "jkgGsucdSUPfLbDzZA" {
		if _, cmd := p.Update(tea.KeyPressMsg{Text: string(key), Code: key}); cmd != nil {
			_ = cmd()
		}
	}
	if _, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		_ = cmd()
	}
	_ = p.View(160, 40)
	_ = p.Diagnostics()
	_ = p.Commands()
	p.Stop()

	if calls, err := os.ReadFile(log); err == nil && len(strings.TrimSpace(string(calls))) > 0 {
		t.Fatalf("a bound pane ran local git:\n%s", calls)
	}
	if !strings.Contains(p.View(160, 40), remoteMarker) {
		t.Fatal("the bound pane never showed the host, so the assertion above proved nothing")
	}
}

func TestRemoteRepoStatusSplitsAPathIntoItsTwoRows(t *testing.T) {
	status := remoteRepoStatus(hostStatusResult())

	if len(status.Tree.Staged) != 1 || status.Tree.Staged[0].Path != remoteMarker {
		t.Fatalf("staged = %+v", status.Tree.Staged)
	}
	if got := status.Tree.Staged[0].DiffStats; got != (DiffStats{Additions: 3, Deletions: 1}) {
		t.Errorf("staged stats = %+v, want the staged counts", got)
	}
	if len(status.Tree.Modified) != 1 || status.Tree.Modified[0].Path != remoteMarker {
		t.Fatalf("modified = %+v", status.Tree.Modified)
	}
	if status.Tree.Modified[0].Staged {
		t.Error("the unstaged row of an MM path must not claim to be staged")
	}
	if got := status.Tree.Modified[0].DiffStats; got != (DiffStats{Additions: 7, Deletions: 2}) {
		t.Errorf("modified stats = %+v, want the unstaged counts", got)
	}
	if len(status.Tree.Untracked) != 1 || status.Tree.Untracked[0].Status != StatusUntracked {
		t.Fatalf("untracked = %+v", status.Tree.Untracked)
	}
	if status.Push.CurrentBranch != remoteBranch || !status.Push.HasUpstream ||
		status.Push.UpstreamBranch != "origin/"+remoteBranch ||
		status.Push.Ahead != 2 || status.Push.Behind != 1 {
		t.Errorf("push = %+v, want the host's branch row", status.Push)
	}
	if status.State != reposervice.StateRebase {
		t.Errorf("state = %q, want the host's in-progress operation", status.State)
	}
}

func TestBoundUnavailableReasonsAreDistinct(t *testing.T) {
	cases := []struct {
		name  string
		mutir func(*plugin.Context)
		want  string
	}{
		{
			name:  "host offline",
			mutir: func(c *plugin.Context) { c.HostShows = func() bool { return false } },
			want:  "[aerie] is not connected",
		},
		{
			name: "host too old",
			mutir: func(c *plugin.Context) {
				c.HostVerbs = func() hostproto.VerbCapabilities { return hostproto.VerbCapabilities{} }
			},
			want: "[aerie] runs a Sidecar that predates the repository contract (sidecar repo status)",
		},
		{
			name:  "no bound workspace",
			mutir: func(c *plugin.Context) { c.ProjectKey = "" },
			want:  "no worktree on [aerie] is bound yet",
		},
		{
			name:  "not reachable",
			mutir: func(c *plugin.Context) { c.RemoteRunner = nil },
			want:  "[aerie] is not reachable from this Sidecar",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := connectedHostContext()
			tc.mutir(ctx)
			p, _ := boundGitPlugin(t, ctx)

			if got := p.unavailableReason(); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
			if cmd := p.Start(); cmd != nil {
				t.Fatal("an unusable bound pane started a read")
			}
			view := p.View(160, 40)
			if !strings.Contains(view, tc.want) {
				t.Errorf("view = %q, want the reason", view)
			}
			if strings.Contains(view, twinMarker) || strings.Contains(view, "Initialize Git Repository") {
				t.Fatalf("bound pane fell through to this machine's repository:\n%s", view)
			}
			if p.inNoRepoMode() {
				t.Fatal("a bound pane must not offer the local git init path")
			}
			if p.Commands() != nil {
				t.Error("an unusable bound pane advertised commands")
			}
		})
	}
}

// A workspace the host will not serve as a repository is the host's own answer,
// and it reaches the viewer by two different paths. Both must read as "not a git
// repository", and neither may become this machine's no-repo view.
func TestBoundWorkspaceThatIsNotARepository(t *testing.T) {
	t.Run("host answers NoRepository", func(t *testing.T) {
		ctx := connectedHostContext()
		p, _ := boundGitPlugin(t, ctx)
		ctx.RemoteRunner = func(_ context.Context, _ string, _ []string, out any) error {
			*out.(*reposervice.StatusResult) = reposervice.StatusResult{
				Kind:         reposervice.KindStatus,
				Workspace:    "/home/me/sidecar:shell:notes",
				NoRepository: true,
			}
			return nil
		}
		assertNotARepository(t, p, "[aerie] is not a git repository")
	})

	t.Run("host rejects the worktree id", func(t *testing.T) {
		ctx := connectedHostContext()
		p, _ := boundGitPlugin(t, ctx)
		ctx.RemoteRunner = func(context.Context, string, []string, any) error {
			return &hosts.RunError{
				Failure:  hosts.FailRejected,
				HostID:   "aerie",
				Args:     []string{"repo", "status"},
				ExitCode: 5,
				Detail:   `workspace "/home/me/sidecar:worktree:/home/me/sidecar" no longer owns this worktree`,
			}
		}
		assertNotARepository(t, p,
			`[aerie] is not a git repository: workspace "/home/me/sidecar:worktree:/home/me/sidecar" no longer owns this worktree`)
	})
}

func assertNotARepository(t *testing.T, p *Plugin, want string) {
	t.Helper()
	applyStatus(t, p, p.Start())

	if got := p.unavailableReason(); got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	view := p.View(160, 40)
	if !strings.Contains(view, want) {
		t.Errorf("view = %q, want the reason", view)
	}
	if strings.Contains(view, "Initialize Git Repository") || strings.Contains(view, twinMarker) {
		t.Fatalf("bound pane offered this machine's no-repo view:\n%s", view)
	}
	if p.inNoRepoMode() {
		t.Fatal("a bound pane must not enter local no-repo mode")
	}
	diags := p.Diagnostics()
	if len(diags) != 1 || diags[0].Status != "warn" || !strings.Contains(diags[0].Detail, want) {
		t.Errorf("diagnostics = %+v, want one warn naming the reason", diags)
	}
}

func TestBoundStatusRefreshesOnHostInventory(t *testing.T) {
	src := &fakeRepoSource{status: remoteRepoStatus(hostStatusResult())}
	p, _ := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src
	applyStatus(t, p, p.Start())

	_, cmd := p.Update(plugin.HostInventoryMsg{})
	if cmd == nil {
		t.Fatal("a host snapshot bump did not refresh the bound pane")
	}
	applyStatus(t, p, cmd)
	if src.calls != 2 {
		t.Fatalf("status calls = %d, want a second read", src.calls)
	}

	// r is the other half of the refresh contract; there is no watcher across
	// the boundary and no timer poll.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "r", Code: 'r'})
	if cmd == nil {
		t.Fatal("r did not refresh the bound pane")
	}
	applyStatus(t, p, cmd)
	if src.calls != 3 {
		t.Fatalf("status calls = %d, want a third read", src.calls)
	}
	if p.watcher != nil {
		t.Fatal("a bound pane started a filesystem watcher")
	}
}

func TestLocalRepoSourceKeepsTheInjectedLoader(t *testing.T) {
	p := New()
	root := t.TempDir()
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	p.activateRepo(root)
	var asked string
	p.statusLoader = func(dir string) (*FileTree, error) {
		asked = dir
		return &FileTree{Modified: []*FileEntry{{Path: "local.go", Status: StatusModified, Unstaged: true}}}, nil
	}

	applyStatus(t, p, p.refresh())
	if asked != root {
		t.Fatalf("loader asked for %q, want %q", asked, root)
	}
	if len(p.tree.Modified) != 1 || p.tree.Modified[0].Path != "local.go" {
		t.Fatalf("tree = %+v", p.tree)
	}
	// The branch row still arrives with history on a local project, exactly as
	// it always has.
	if p.pushStatus != nil {
		t.Errorf("pushStatus = %+v, want the history load to own it locally", p.pushStatus)
	}
	if p.repoState != "" {
		t.Errorf("repoState = %q, want empty for a local read", p.repoState)
	}
}
