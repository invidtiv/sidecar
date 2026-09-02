package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// fakeRepoSource answers from a fixed status and a patch per mode, and records
// what it was asked.
type fakeRepoSource struct {
	status RepoStatus
	err    error
	calls  int

	patches   map[string]RepoDiff
	diffErr   error
	diffCalls []DiffRequest
}

func (s *fakeRepoSource) Status(context.Context) (RepoStatus, error) {
	s.calls++
	return s.status, s.err
}

func (s *fakeRepoSource) Diff(_ context.Context, req DiffRequest) (RepoDiff, error) {
	s.diffCalls = append(s.diffCalls, req)
	if s.diffErr != nil {
		return RepoDiff{}, s.diffErr
	}
	return s.patches[req.Mode], nil
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

// The host's patches. Each carries a line naming the sense it is, because the
// failure this suite exists to catch is a pane showing a plausible patch that
// belongs to the other row, or to the twin on this disk.
const (
	hostWorkspaceID     = "/home/me/sidecar:worktree:/home/me/sidecar"
	remoteStagedLine    = "REMOTE-STAGED-LINE"
	remoteUnstagedLine  = "REMOTE-UNSTAGED-LINE"
	remoteUntrackedLine = "REMOTE-UNTRACKED-LINE"
)

func hostPatch(mode string) string {
	switch mode {
	case reposervice.ModeStaged:
		return "diff --git a/" + remoteMarker + " b/" + remoteMarker + "\n" +
			"--- a/" + remoteMarker + "\n+++ b/" + remoteMarker + "\n" +
			"@@ -1 +1 @@\n-before\n+" + remoteStagedLine + "\n"
	case reposervice.ModeUnstaged:
		return "diff --git a/" + remoteMarker + " b/" + remoteMarker + "\n" +
			"--- a/" + remoteMarker + "\n+++ b/" + remoteMarker + "\n" +
			"@@ -1 +1 @@\n-before\n+" + remoteUnstagedLine + "\n"
	case reposervice.ModeUntracked:
		return "diff --git a/host-only.txt b/host-only.txt\nnew file mode 100644\n" +
			"--- /dev/null\n+++ b/host-only.txt\n@@ -0,0 +1 @@\n+" + remoteUntrackedLine + "\n"
	default:
		return ""
	}
}

// hostRepoRunner answers `sidecar repo` the way the host's CLI does, and
// records every invocation so a test can read which verb, which path, and which
// staging sense the viewer asked for.
func hostRepoRunner(t *testing.T, calls *[]string) func(context.Context, string, []string, any) error {
	t.Helper()
	return func(_ context.Context, hostID string, args []string, out any) error {
		if hostID != "aerie" {
			t.Errorf("verb addressed to %q", hostID)
		}
		*calls = append(*calls, strings.Join(args, " "))
		switch out := out.(type) {
		case *reposervice.StatusResult:
			*out = hostStatusResult()
		case *reposervice.DiffResult:
			mode := verbFlag(args, "--mode")
			if mode == "" {
				t.Errorf("repo diff carried no --mode: %v", args)
			}
			*out = reposervice.DiffResult{
				Kind:      reposervice.KindDiff,
				Workspace: hostWorkspaceID,
				Mode:      mode,
				Path:      verbFlag(args, "--path"),
				Patch:     hostPatch(mode),
			}
		default:
			t.Fatalf("runner asked to decode into %T", out)
		}
		return nil
	}
}

func verbFlag(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func diffCalls(calls []string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, "repo diff ") {
			out = append(out, call)
		}
	}
	return out
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
	_, follow := p.Update(msg)
	drive(t, p, follow)
}

// drive runs a command and feeds everything it produces back into the plugin,
// following batches, so a test sees the same sequence the event loop would —
// including the patch load a status answer schedules for the row the cursor is
// on.
func drive(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drive(t, p, c)
		}
		return
	}
	_, follow := p.Update(msg)
	drive(t, p, follow)
}

func TestBoundStatusPaneShowsTheHostAndNeverTheLocalTwin(t *testing.T) {
	src := &fakeRepoSource{
		status:  remoteRepoStatus(hostStatusResult()),
		patches: map[string]RepoDiff{reposervice.ModeStaged: {Patch: hostPatch(reposervice.ModeStaged)}},
	}
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
	// History is a later slice, and the pane says so rather than reporting an
	// empty history.
	if !strings.Contains(view, "Commits are not read from [aerie] yet") {
		t.Errorf("view is missing the history sentence:\n%s", view)
	}
	// The patch for the row the cursor lands on is the host's, in the sense
	// that row means.
	if !strings.Contains(view, remoteStagedLine) {
		t.Errorf("view is missing the host's staged patch:\n%s", view)
	}
	if len(src.diffCalls) != 1 || src.diffCalls[0].Mode != reposervice.ModeStaged || src.diffCalls[0].Path != remoteMarker {
		t.Errorf("diff calls = %+v, want one staged read of %s", src.diffCalls, remoteMarker)
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
	log := recordLocalGit(t)

	var calls []string
	ctx.RemoteRunner = hostRepoRunner(t, &calls)

	applyStatus(t, p, p.Start())
	if _, cmd := p.Update(app.PluginFocusedMsg{}); cmd != nil {
		applyStatus(t, p, cmd)
	}
	// Movement, the patch surfaces the cursor reaches, and every write key the
	// local pane binds.
	for _, key := range "jkgGlvw,.nNd" + "sucSUPfLbDzZA" {
		_, cmd := p.Update(tea.KeyPressMsg{Text: string(key), Code: key})
		drive(t, p, cmd)
	}
	for _, code := range []rune{tea.KeyEnter, tea.KeyEscape} {
		_, cmd := p.Update(tea.KeyPressMsg{Code: code})
		drive(t, p, cmd)
	}
	_ = p.View(160, 40)
	_ = p.Diagnostics()
	_ = p.Commands()
	p.Stop()

	assertNoLocalGit(t, log)
	// Non-vacuousness: the pane read the host, and it read patches, so the
	// assertion above was made against a pane that had every chance to run git.
	view := p.View(160, 40)
	if !strings.Contains(view, remoteMarker) {
		t.Fatal("the bound pane never showed the host, so the assertion above proved nothing")
	}
	if !strings.Contains(view, remoteStagedLine) && !strings.Contains(view, remoteUnstagedLine) &&
		!strings.Contains(view, remoteUntrackedLine) {
		t.Fatalf("the bound pane never rendered a host patch:\n%s", view)
	}
	if len(diffCalls(calls)) == 0 {
		t.Fatalf("no patch was read from the host: %v", calls)
	}
}

// The staging sense of the row the cursor is on is the whole of what --mode
// means. An MM path is two rows; answering either with the other's patch is a
// quiet, plausible lie about the host's working tree, and it is the one thing
// this suite cannot let pass.
func TestBoundPatchesFollowTheRowsStagingSense(t *testing.T) {
	ctx := connectedHostContext()
	p, _ := boundGitPlugin(t, ctx)
	var calls []string
	ctx.RemoteRunner = hostRepoRunner(t, &calls)

	applyStatus(t, p, p.Start())

	// Row 0 is the staged side of REMOTE-MARKER.md.
	view := p.View(160, 40)
	if !strings.Contains(view, remoteStagedLine) || strings.Contains(view, remoteUnstagedLine) {
		t.Fatalf("the staged row did not show the staged patch:\n%s", view)
	}
	if strings.Contains(view, twinMarker) {
		t.Fatalf("bound diff pane showed this machine's twin:\n%s", view)
	}

	// Row 1 is the same path, unstaged.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	drive(t, p, cmd)
	view = p.View(160, 40)
	if !strings.Contains(view, remoteUnstagedLine) || strings.Contains(view, remoteStagedLine) {
		t.Fatalf("the unstaged row did not show the unstaged patch:\n%s", view)
	}

	// Row 2 is the untracked file, which is its own mode.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	drive(t, p, cmd)
	if view = p.View(160, 40); !strings.Contains(view, remoteUntrackedLine) {
		t.Fatalf("the untracked row did not show the untracked patch:\n%s", view)
	}

	want := []string{
		"repo diff --workspace " + hostWorkspaceID + " --path " + remoteMarker + " --mode staged --json",
		"repo diff --workspace " + hostWorkspaceID + " --path " + remoteMarker + " --mode unstaged --json",
		"repo diff --workspace " + hostWorkspaceID + " --path host-only.txt --mode untracked --json",
	}
	if got := diffCalls(calls); !slices.Equal(got, want) {
		t.Errorf("diff calls =\n%v\nwant\n%v", got, want)
	}
}

// A host that answered the other side of an MM path would be plausible in every
// visible way, so the answer's own mode is checked against the request.
func TestBoundPatchWithTheWrongModeIsRefused(t *testing.T) {
	src := &remoteRepoSource{
		hostID:      "aerie",
		workspaceID: hostWorkspaceID,
		run: func(_ context.Context, _ string, _ []string, out any) error {
			*out.(*reposervice.DiffResult) = reposervice.DiffResult{
				Kind:      reposervice.KindDiff,
				Workspace: hostWorkspaceID,
				Mode:      reposervice.ModeUnstaged,
				Path:      remoteMarker,
				Patch:     hostPatch(reposervice.ModeUnstaged),
			}
			return nil
		},
	}
	_, err := src.Diff(context.Background(), DiffRequest{Path: remoteMarker, Mode: reposervice.ModeStaged})
	if err == nil || !strings.Contains(err.Error(), "answered the unstaged patch for a staged row") {
		t.Fatalf("err = %v, want the mismatch named", err)
	}
}

func TestBoundTruncatedPatchIsLabelled(t *testing.T) {
	src := &fakeRepoSource{
		status: remoteRepoStatus(hostStatusResult()),
		patches: map[string]RepoDiff{
			reposervice.ModeStaged: {Patch: hostPatch(reposervice.ModeStaged), Truncated: true},
		},
	}
	p, _ := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src
	applyStatus(t, p, p.Start())

	if view := p.View(160, 40); !strings.Contains(view, "(truncated)") {
		t.Errorf("the diff pane rendered a cut patch as if it were whole:\n%s", view)
	}
	// The full-screen view carries the same label; a patch does not become
	// whole by being opened.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "d", Code: 'd'})
	drive(t, p, cmd)
	if !p.diffTruncated {
		t.Fatal("the full-screen view lost the truncation flag")
	}
	if view := p.View(160, 40); !strings.Contains(view, "(truncated)") {
		t.Errorf("the full-screen diff rendered a cut patch as if it were whole:\n%s", view)
	}
}

// Full-file view needs a file's contents on both sides of the change and no
// host verb answers those. The pane says so; it does not sit on a load that
// will never arrive, and it does not read the file here.
func TestBoundFullFileViewNamesWhatTheHostCannotAnswer(t *testing.T) {
	src := &fakeRepoSource{
		status:  remoteRepoStatus(hostStatusResult()),
		patches: map[string]RepoDiff{reposervice.ModeStaged: {Patch: hostPatch(reposervice.ModeStaged)}},
	}
	p, _ := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src
	log := recordLocalGit(t)
	p.diffPaneViewMode = DiffViewFullFile
	applyStatus(t, p, p.Start())

	view := p.View(160, 40)
	if !strings.Contains(view, "Full-file view is not available on [aerie]") {
		t.Errorf("view does not say why a full file is missing:\n%s", view)
	}
	if strings.Contains(view, "Loading full file") {
		t.Errorf("view is waiting on a read that will never arrive:\n%s", view)
	}
	if !strings.Contains(view, remoteStagedLine) {
		t.Errorf("view dropped the patch the host did answer:\n%s", view)
	}
	if p.diffPaneFullFileDiff != nil {
		t.Error("a bound pane built a full-file diff")
	}
	assertNoLocalGit(t, log)
}

// A folder row is an aggregate of files, not one repository read. It refuses by
// name rather than turning one cursor move into one round trip per file, and it
// must not leave the previous row's patch on screen under the folder's name.
func TestBoundFolderRowRefusesAndDoesNotLeaveAStalePatch(t *testing.T) {
	status := hostStatusResult()
	status.Files = append(status.Files,
		reposervice.StatusFile{Path: "pkg/one.txt", Status: "?", Unstaged: true, Untracked: true},
		reposervice.StatusFile{Path: "pkg/two.txt", Status: "?", Unstaged: true, Untracked: true},
	)
	src := &fakeRepoSource{
		status: remoteRepoStatus(status),
		patches: map[string]RepoDiff{
			reposervice.ModeStaged:    {Patch: hostPatch(reposervice.ModeStaged)},
			reposervice.ModeUntracked: {Patch: hostPatch(reposervice.ModeUntracked)},
		},
	}
	p, _ := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src
	applyStatus(t, p, p.Start())

	// Down to the folder row: staged, modified, host-only.txt, then pkg/.
	for i := 0; i < 3; i++ {
		_, cmd := p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
		drive(t, p, cmd)
	}
	entries := p.treeEntries()
	if p.cursor >= len(entries) || !entries[p.cursor].IsFolder {
		t.Fatalf("cursor %d is not on the folder row: %+v", p.cursor, entries)
	}
	view := p.View(160, 40)
	if !strings.Contains(view, "A folder's combined patch is not read from [aerie]") {
		t.Errorf("the folder row did not say why it has no patch:\n%s", view)
	}
	if strings.Contains(view, remoteUntrackedLine) || strings.Contains(view, remoteStagedLine) {
		t.Errorf("the folder row kept the previous row's patch on screen:\n%s", view)
	}

	// Opening the folder puts its files in reach, and each reads its own patch.
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drive(t, p, cmd)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	drive(t, p, cmd)
	last := src.diffCalls[len(src.diffCalls)-1]
	if last.Path != "pkg/one.txt" || last.Mode != reposervice.ModeUntracked {
		t.Errorf("a file inside the folder read %+v, want the untracked patch for pkg/one.txt", last)
	}
}

// The local half of the seam is today's code routed, not rewritten: the bytes a
// local pane renders are the bytes the three git readers have always produced.
func TestLocalPatchLoadersKeepTodaysBytes(t *testing.T) {
	root, hash := localRepoFixture(t)
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	p.activateRepo(root)

	cases := []struct {
		name string
		load func() tea.Cmd
		want func() (string, error)
	}{
		{
			name: "staged",
			load: func() tea.Cmd { return p.loadInlineDiff("tracked.txt", true, StatusModified) },
			want: func() (string, error) { return GetDiff(root, "tracked.txt", true) },
		},
		{
			name: "unstaged",
			load: func() tea.Cmd { return p.loadInlineDiff("tracked.txt", false, StatusModified) },
			want: func() (string, error) { return GetDiff(root, "tracked.txt", false) },
		},
		{
			name: "untracked",
			load: func() tea.Cmd { return p.loadInlineDiff("new.txt", false, StatusUntracked) },
			want: func() (string, error) { return GetNewFileDiff(root, "new.txt") },
		},
	}
	patches := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.want()
			if err != nil {
				t.Fatal(err)
			}
			msg, ok := tc.load()().(InlineDiffLoadedMsg)
			if !ok {
				t.Fatalf("load produced %T", msg)
			}
			if msg.Raw != want {
				t.Errorf("patch =\n%q\nwant\n%q", msg.Raw, want)
			}
			if msg.Truncated {
				t.Error("a local patch reported itself truncated")
			}
			patches[tc.name] = msg.Raw
		})
	}
	if patches["staged"] == patches["unstaged"] {
		t.Fatalf("the two senses of one path returned the same patch, so the fixture proved nothing:\n%s", patches["staged"])
	}

	wantCommit, err := GetCommitDiff(root, hash, "tracked.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := p.loadCommitFileDiff(hash, "tracked.txt", "")().(DiffLoadedMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("commit patch load = %#v", msg)
	}
	if msg.Raw != wantCommit {
		t.Errorf("commit patch =\n%q\nwant\n%q", msg.Raw, wantCommit)
	}
}

// localRepoFixture is a repository with one path changed in both senses, one
// untracked file, and one commit to read a patch out of.
func localRepoFixture(t *testing.T) (root, hash string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) []byte {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init")
	git("config", "user.email", "fixture@example.com")
	git("config", "user.name", "Fixture")
	write("tracked.txt", "one\n")
	git("add", "tracked.txt")
	git("commit", "-m", "first")
	write("tracked.txt", "one\nstaged\n")
	git("add", "tracked.txt")
	write("tracked.txt", "one\nstaged\nunstaged\n")
	write("new.txt", "brand new\n")
	return root, strings.TrimSpace(string(git("rev-parse", "HEAD")))
}

// recordLocalGit replaces git on PATH with a recorder that fails, so any local
// invocation is visible and none of them can succeed.
func recordLocalGit(t *testing.T) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "git-calls")
	binDir := t.TempDir()
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SIDECAR_GIT_CALL_LOG\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIDECAR_GIT_CALL_LOG", log)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func assertNoLocalGit(t *testing.T, log string) {
	t.Helper()
	if recorded, err := os.ReadFile(log); err == nil && len(strings.TrimSpace(string(recorded))) > 0 {
		t.Fatalf("a bound pane ran local git:\n%s", recorded)
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
