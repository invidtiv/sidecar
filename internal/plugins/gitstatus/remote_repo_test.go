package gitstatus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/reposervice"
)

const (
	remoteMarker      = "REMOTE-MARKER.md"
	remoteBranch      = "host-branch"
	remoteTopicBranch = "host-topic"
	twinMarker        = "LOCAL-TWIN.md"
	twinBranchName    = "local-twin-branch"
	twinCommitSubject = "LOCAL-TWIN-COMMIT"
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

	history      RepoHistory
	historyCalls []HistoryRequest
	commits      map[string]*Commit
	refs         RepoRefs
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

func (s *fakeRepoSource) History(_ context.Context, req HistoryRequest) (RepoHistory, error) {
	s.historyCalls = append(s.historyCalls, req)
	return s.history, nil
}

func (s *fakeRepoSource) CommitDetail(_ context.Context, hash string) (*Commit, error) {
	return s.commits[hash], nil
}

func (s *fakeRepoSource) Refs(context.Context) (RepoRefs, error) {
	return s.refs, nil
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
	git("config", "user.email", "twin@example.com")
	git("config", "user.name", "Twin")
	git("checkout", "-b", twinBranchName)
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(root, twinMarker), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("this machine\n")
	git("add", twinMarker)
	// The twin has its own history too, so a bound commit list showing it is
	// visible rather than merely wrong.
	git("commit", "-m", twinCommitSubject)
	write("this machine, edited\n")
	git("add", twinMarker)

	// The twin is real: nothing asserted against it below is vacuous.
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil || !strings.Contains(string(out), twinMarker) {
		t.Fatalf("twin fixture is not a repository with a staged %s: %q %v", twinMarker, out, err)
	}
	log, err := exec.Command("git", "-C", root, "log", "--format=%s").Output()
	if err != nil || !strings.Contains(string(log), twinCommitSubject) {
		t.Fatalf("twin fixture has no %s commit: %q %v", twinCommitSubject, log, err)
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
		RemoteURL:   remoteOriginURL,
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
	remoteOriginURL     = "git@github.com:aerie/sidecar.git"
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

// The host's log. Two pages of it, so paging is something a test can watch
// happen, with a merge in the first page so the graph has a second column to
// draw and one unpushed row so the push state has to survive the wire.
const (
	remoteCommitSubject = "REMOTE-COMMIT"
	remoteCommitAuthor  = "Aerie Author"
	remoteMergeParent   = "aeriemergeparent"
	hostLogLength       = 2 * commitHistoryPageSize
)

func hostCommitHash(i int) string { return fmt.Sprintf("aerie%05d", i) }

// hostLog is the host's whole log, newest first. Only ever served a page at a
// time — a viewer that asked for all of it is the thing decision 9 forbids.
func hostLog() []reposervice.CommitRow {
	rows := make([]reposervice.CommitRow, 0, hostLogLength)
	for i := 0; i < hostLogLength; i++ {
		row := reposervice.CommitRow{
			Hash:        hostCommitHash(i),
			ShortHash:   hostCommitHash(i)[:7],
			Subject:     fmt.Sprintf("%s %d", remoteCommitSubject, i),
			Author:      remoteCommitAuthor,
			AuthorEmail: "aerie@example.com",
			Date:        time.Now().Add(-time.Duration(i) * time.Hour).UTC(),
			Pushed:      i > 0,
		}
		if i+1 < hostLogLength {
			row.Parents = []string{hostCommitHash(i + 1)}
		}
		if i == 1 {
			row.Parents = append(row.Parents, remoteMergeParent)
			row.Merge = true
		}
		rows = append(rows, row)
	}
	return rows
}

// hostHistoryPage is the host's own paging rule: rows after the cursor, capped
// at the limit that was asked for.
func hostHistoryPage(cursor string, limit int) reposervice.HistoryResult {
	rows := hostLog()
	start := 0
	if cursor != "" {
		for i, row := range rows {
			if row.Hash == cursor {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = reposervice.DefaultHistoryLimit
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	page := reposervice.HistoryResult{
		Kind:      reposervice.KindHistory,
		Workspace: hostWorkspaceID,
		Limit:     limit,
		Commits:   rows[start:end],
	}
	if end < len(rows) {
		page.NextCursor = rows[end-1].Hash
	}
	return page
}

func hostCommitDetail(hash string) *reposervice.CommitDetail {
	for _, row := range hostLog() {
		if row.Hash != hash {
			continue
		}
		return &reposervice.CommitDetail{
			Hash:        row.Hash,
			ShortHash:   row.ShortHash,
			Subject:     row.Subject,
			Body:        "REMOTE-COMMIT-BODY for " + row.Hash,
			Author:      row.Author,
			AuthorEmail: row.AuthorEmail,
			Date:        row.Date,
			Parents:     row.Parents,
			Merge:       row.Merge,
			Files: []reposervice.CommitFile{
				{Path: remoteMarker, Status: "M", Additions: 4, Deletions: 2},
			},
		}
	}
	return nil
}

func hostRefsResult() reposervice.RefsResult {
	return reposervice.RefsResult{
		Kind:      reposervice.KindRefs,
		Workspace: hostWorkspaceID,
		Branches: []reposervice.Branch{
			{Name: remoteBranch, Current: true, Upstream: "origin/" + remoteBranch, Ahead: 2, Behind: 1, ShortHash: "4f2b91c"},
			{Name: remoteTopicBranch, ShortHash: "9ab3fe0"},
		},
		RemoteBranches: []reposervice.Branch{
			{Name: "origin/" + remoteBranch, Remote: true},
		},
		Stashes: []reposervice.Stash{
			{Index: 0, Ref: "stash@{0}", Branch: remoteBranch, Message: "REMOTE-STASH"},
		},
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
		case *reposervice.HistoryResult:
			limit, _ := strconv.Atoi(verbFlag(args, "--limit"))
			page := hostHistoryPage(verbFlag(args, "--cursor"), limit)
			if author := verbFlag(args, "--author"); author != "" && author != remoteCommitAuthor {
				page.Commits = nil
			}
			*out = page
		case *reposervice.CommitResult:
			*out = reposervice.CommitResult{
				Kind:      reposervice.KindCommit,
				Workspace: hostWorkspaceID,
				Commit:    hostCommitDetail(verbFlag(args, "--commit")),
			}
		case *reposervice.RefsResult:
			*out = hostRefsResult()
		default:
			t.Fatalf("runner asked to decode into %T", out)
		}
		return nil
	}
}

// verbCalls is every invocation of one `sidecar repo` sub-verb, in order.
func verbCalls(calls []string, verb string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, "repo "+verb+" ") {
			out = append(out, call)
		}
	}
	return out
}

func verbFlag(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func diffCalls(calls []string) []string { return verbCalls(calls, "diff") }

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

// applyStatus runs a load command and feeds its answers back, the way the app's
// event loop does. A bound load is a status read and the first page of history,
// so the whole batch is driven and the status answer must be among it.
func applyStatus(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no status command")
	}
	if !drive(t, p, cmd) {
		t.Fatal("the load produced no status answer")
	}
}

// drive runs a command and feeds everything it produces back into the plugin,
// following batches, so a test sees the same sequence the event loop would —
// including the patch load a status answer schedules for the row the cursor is
// on. It reports whether a status answer passed through.
func drive(t *testing.T, p *Plugin, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	msg := cmd()
	if msg == nil {
		return false
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		status := false
		for _, c := range batch {
			if drive(t, p, c) {
				status = true
			}
		}
		return status
	}
	_, isStatus := msg.(StatusSnapshotLoadedMsg)
	_, follow := p.Update(msg)
	return drive(t, p, follow) || isStatus
}

func TestBoundStatusPaneShowsTheHostAndNeverTheLocalTwin(t *testing.T) {
	src := &fakeRepoSource{
		status:  remoteRepoStatus(hostStatusResult()),
		patches: map[string]RepoDiff{reposervice.ModeStaged: {Patch: hostPatch(reposervice.ModeStaged)}},
		history: RepoHistory{Commits: remoteCommitRows(hostHistoryPage("", commitHistoryPageSize).Commits)},
	}
	p, twin := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src

	applyStatus(t, p, p.Start())

	view := p.View(160, 40)
	for _, want := range []string{remoteMarker, "host-only.txt", remoteBranch, "rebasing", remoteCommitSubject} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing the host's %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{twinMarker, twinBranchName, twinCommitSubject} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("bound pane showed this machine's twin %q:\n%s", unwanted, view)
		}
	}
	// The patch for the row the cursor lands on is the host's, in the sense
	// that row means.
	if !strings.Contains(view, remoteStagedLine) {
		t.Errorf("view is missing the host's staged patch:\n%s", view)
	}
	if len(src.diffCalls) != 1 || src.diffCalls[0].Mode != reposervice.ModeStaged || src.diffCalls[0].Path != remoteMarker {
		t.Errorf("diff calls = %+v, want one staged read of %s", src.diffCalls, remoteMarker)
	}
	if len(p.recentCommits) != commitHistoryPageSize {
		t.Errorf("recentCommits = %d rows, want one page of the host's log", len(p.recentCommits))
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
	// o builds a GitHub link from the host's remote URL, and the drive below
	// presses it; the link is watched rather than handed to a browser.
	links := stubBrowser(t)
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
	// The pane is showing the host before a key is pressed: its files and its
	// patch for the row the cursor landed on.
	opening := p.View(160, 40)
	if !strings.Contains(opening, remoteMarker) {
		t.Fatalf("the bound pane never showed the host:\n%s", opening)
	}
	if !strings.Contains(opening, remoteStagedLine) {
		t.Fatalf("the bound pane never rendered a host patch:\n%s", opening)
	}

	press := func(keys string) {
		t.Helper()
		for _, key := range keys {
			_, cmd := p.Update(tea.KeyPressMsg{Text: string(key), Code: key})
			_ = drive(t, p, cmd)
		}
	}
	code := func(codes ...rune) {
		t.Helper()
		for _, c := range codes {
			_, cmd := p.Update(tea.KeyPressMsg{Code: c})
			_ = drive(t, p, cmd)
		}
	}

	// Movement onto the commits, the graph, the yanks, and the commit link
	// this build cannot answer.
	press("jkgGlhvw,.nNyYo")
	// A commit file's patch full-screen, then back.
	press("d")
	code(tea.KeyEnter, tea.KeyEscape)
	// The search modal, the path filter modal, and clearing the filters.
	press("/")
	code(tea.KeyEscape)
	press("p")
	code(tea.KeyEscape)
	press("F")
	// The branch picker: listed, then a switch refused, then closed.
	press("b")
	code(tea.KeyEnter, tea.KeyEscape)
	// Every write key the local pane binds, each of which now refuses by name.
	press("sucSUPfLDzZA")
	code(tea.KeyEnter)
	// And the pointer, which reaches the same rows: selecting a file and a
	// commit, scrolling the sidebar, focusing the diff pane, dragging the
	// divider, and the double-click that would open an editor.
	_ = p.View(160, 40)
	click := func(x, y int) {
		t.Helper()
		_, cmd := p.Update(clickMsg(x, y))
		_ = drive(t, p, cmd)
		_ = p.View(160, 40)
	}
	fileX, fileY := boundRegion(t, p, regionFile, 0)
	commitX, commitY := boundRegion(t, p, regionCommit, 1)
	paneX, paneY := boundRegionAny(t, p, regionDiffPane)
	dividerX, dividerY := boundRegionAny(t, p, regionPaneDivider)
	click(commitX, commitY)
	click(fileX, fileY)
	click(fileX, fileY) // the double-click that refuses
	click(paneX, paneY)
	_, cmd := p.Update(tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
	_ = drive(t, p, cmd)
	click(dividerX, dividerY)
	_, cmd = p.Update(motionMsg(dividerX+6, dividerY))
	_ = drive(t, p, cmd)
	_, cmd = p.Update(releaseMsg(dividerX+6, dividerY))
	_ = drive(t, p, cmd)

	_ = p.View(160, 40)
	_ = p.Diagnostics()
	_ = p.Commands()
	p.Stop()

	assertNoLocalGit(t, log)
	// Non-vacuousness: every read this build performs was made against the
	// host while the recorder stood in for git, and every gesture that refuses
	// was driven through both the keyboard and the pointer, so the assertion
	// above was made by a pane that had every chance to run one.
	view := p.View(160, 40)
	if !strings.Contains(view, remoteMarker) && !strings.Contains(view, remoteCommitSubject) {
		t.Fatalf("the bound pane ended up showing nothing of the host:\n%s", view)
	}
	for _, verb := range []string{"status", "diff", "history", "commit", "refs"} {
		if len(verbCalls(calls, verb)) == 0 {
			t.Fatalf("repo %s was never read from the host: %v", verb, calls)
		}
	}
	// o reached the link builder rather than falling through: the URL came from
	// the host's remote, with no git on this disk asked for one.
	if len(*links) == 0 {
		t.Fatal("the GitHub link was never built, so pressing o proved nothing")
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
	_ = drive(t, p, cmd)
	view = p.View(160, 40)
	if !strings.Contains(view, remoteUnstagedLine) || strings.Contains(view, remoteStagedLine) {
		t.Fatalf("the unstaged row did not show the unstaged patch:\n%s", view)
	}

	// Row 2 is the untracked file, which is its own mode.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	_ = drive(t, p, cmd)
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
	_ = drive(t, p, cmd)
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
		_ = drive(t, p, cmd)
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
	_ = drive(t, p, cmd)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	_ = drive(t, p, cmd)
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
		// A workspace that is not a repository answers so for every verb, not
		// only for the status the pane happens to ask for first.
		ctx.RemoteRunner = func(_ context.Context, _ string, _ []string, out any) error {
			switch out := out.(type) {
			case *reposervice.StatusResult:
				*out = reposervice.StatusResult{
					Kind:         reposervice.KindStatus,
					Workspace:    "/home/me/sidecar:shell:notes",
					NoRepository: true,
				}
			case *reposervice.HistoryResult:
				*out = reposervice.HistoryResult{
					Kind:         reposervice.KindHistory,
					Workspace:    "/home/me/sidecar:shell:notes",
					NoRepository: true,
				}
			default:
				t.Fatalf("runner asked to decode into %T", out)
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

// boundHostPlugin binds a plugin to a host answered by the real verb runner,
// with the twin planted, and returns the recorded call log.
func boundHostPlugin(t *testing.T) (*Plugin, *[]string) {
	t.Helper()
	ctx := connectedHostContext()
	p, _ := boundGitPlugin(t, ctx)
	calls := &[]string{}
	ctx.RemoteRunner = hostRepoRunner(t, calls)
	return p, calls
}

// The commit list is the host's, and the graph is drawn from the parent hashes
// the host sent — not from a walk of anything on this disk.
func TestBoundCommitListAndGraphAreTheHosts(t *testing.T) {
	p, calls := boundHostPlugin(t)
	p.showCommitGraph = true

	applyStatus(t, p, p.Start())
	view := p.View(160, 40)

	if !strings.Contains(view, remoteCommitSubject+" 0") {
		t.Errorf("the sidebar is missing the host's newest commit:\n%s", view)
	}
	if strings.Contains(view, twinCommitSubject) {
		t.Fatalf("the bound commit list showed this machine's twin history:\n%s", view)
	}
	if len(p.recentCommits) != commitHistoryPageSize {
		t.Fatalf("recentCommits = %d, want one page", len(p.recentCommits))
	}
	if got := p.recentCommits[0]; got.Hash != hostCommitHash(0) || got.Author != remoteCommitAuthor {
		t.Errorf("first row = %+v, want the host's newest commit", got)
	}
	// The host's own pushed state rides with the row; nothing here recomputes
	// it against an upstream this machine cannot see.
	if p.recentCommits[0].Pushed || !p.recentCommits[1].Pushed {
		t.Errorf("pushed flags = %v/%v, want the host's", p.recentCommits[0].Pushed, p.recentCommits[1].Pushed)
	}
	if got := p.recentCommits[1].ParentHashes; !slices.Equal(got, []string{hostCommitHash(2), remoteMergeParent}) {
		t.Errorf("parents = %v, want the host's merge parents", got)
	}

	if len(p.commitGraphLines) != len(p.recentCommits) {
		t.Fatalf("graph lines = %d, commits = %d", len(p.commitGraphLines), len(p.recentCommits))
	}
	widest := 0
	for _, line := range p.commitGraphLines {
		if line.Width > widest {
			widest = line.Width
		}
	}
	// A second column exists only because a row the host sent has two parents.
	if widest < 2 {
		t.Errorf("graph width = %d, want the host's merge to open a second column", widest)
	}
	if len(verbCalls(*calls, "history")) != 1 {
		t.Errorf("history calls = %v, want one page", verbCalls(*calls, "history"))
	}
}

// Decision 9: scrolling past the first page asks for the next one, by cursor,
// and asks for nothing else. A viewer that re-read the whole log on every
// scroll would look identical on screen and cost the host everything.
func TestBoundHistoryPagesInsteadOfRefetching(t *testing.T) {
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)

	if got := verbCalls(*calls, "history"); len(got) != 1 {
		t.Fatalf("history calls after the first load = %v, want one", got)
	}

	// G lands on the last row of the page, which is what asks for the next one.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "G", Code: 'G'})
	_ = drive(t, p, cmd)

	want := []string{
		"repo history --workspace " + hostWorkspaceID + " --limit 50 --json",
		"repo history --workspace " + hostWorkspaceID + " --limit 50 --cursor " + hostCommitHash(commitHistoryPageSize-1) + " --json",
	}
	if got := verbCalls(*calls, "history"); !slices.Equal(got, want) {
		t.Fatalf("history calls =\n%v\nwant\n%v", got, want)
	}
	if len(p.recentCommits) != hostLogLength {
		t.Errorf("recentCommits = %d, want both pages", len(p.recentCommits))
	}
	if p.recentCommits[commitHistoryPageSize].Hash != hostCommitHash(commitHistoryPageSize) {
		t.Errorf("the second page did not continue the first: %+v", p.recentCommits[commitHistoryPageSize])
	}
}

// The commit under the cursor is read from the host, with its body and its file
// list, and each of those files reads its own patch from the host too.
func TestBoundCommitDetailIsTheHosts(t *testing.T) {
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)

	// h jumps from the files to the commits, which loads the first one.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	_ = drive(t, p, cmd)

	want := "repo commit --workspace " + hostWorkspaceID + " --commit " + hostCommitHash(0) + " --json"
	if got := verbCalls(*calls, "commit"); !slices.Equal(got, []string{want}) {
		t.Fatalf("commit calls = %v, want %q", got, want)
	}
	commit := p.previewCommit
	if commit == nil {
		t.Fatal("no commit preview loaded")
	}
	if commit.Subject != remoteCommitSubject+" 0" || !strings.Contains(commit.Body, "REMOTE-COMMIT-BODY") {
		t.Errorf("preview = %+v, want the host's commit", commit)
	}
	if len(commit.Files) != 1 || commit.Files[0].Path != remoteMarker ||
		commit.Files[0].Additions != 4 || commit.Files[0].Deletions != 2 {
		t.Errorf("files = %+v, want the host's file list with its counts", commit.Files)
	}
	if commit.Stats != (CommitStats{FilesChanged: 1, Additions: 4, Deletions: 2}) {
		t.Errorf("stats = %+v, want the sum of the host's file rows", commit.Stats)
	}
	view := p.View(160, 40)
	if !strings.Contains(view, remoteCommitSubject+" 0") || !strings.Contains(view, remoteMarker) {
		t.Errorf("the preview pane is missing the host's commit:\n%s", view)
	}
	if strings.Contains(view, twinCommitSubject) || strings.Contains(view, twinMarker) {
		t.Fatalf("the preview pane showed this machine's twin:\n%s", view)
	}
}

// The picker lists the host's branches and stops there. Enter on one refuses by
// name; nothing is checked out on either machine.
func TestBoundBranchPickerListsTheHostAndRefusesToSwitch(t *testing.T) {
	p, calls := boundHostPlugin(t)
	log := recordLocalGit(t)
	applyStatus(t, p, p.Start())

	_, cmd := p.Update(tea.KeyPressMsg{Text: "b", Code: 'b'})
	_ = drive(t, p, cmd)

	want := "repo refs --workspace " + hostWorkspaceID + " --json"
	if got := verbCalls(*calls, "refs"); !slices.Equal(got, []string{want}) {
		t.Fatalf("refs calls = %v, want %q", got, want)
	}
	if len(p.branches) != 2 || p.branches[0].Name != remoteBranch || !p.branches[0].IsCurrent {
		t.Fatalf("branches = %+v, want the host's list with its current branch", p.branches)
	}
	view := p.View(160, 40)
	if !strings.Contains(view, remoteTopicBranch) {
		t.Errorf("the picker is missing the host's branches:\n%s", view)
	}
	if strings.Contains(view, twinBranchName) {
		t.Fatalf("the picker listed this machine's twin branch:\n%s", view)
	}
	if !strings.Contains(view, "switching is refused") {
		t.Errorf("the picker offered a switch it will not perform:\n%s", view)
	}

	// Down to the branch that is not checked out, then Enter.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	_ = drive(t, p, cmd)
	_, cmd = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on a host branch answered nothing")
	}
	flash, ok := cmd().(appmsg.FlashMsg)
	if !ok {
		t.Fatalf("Enter produced %#v, want a refusal", flash)
	}
	if !strings.Contains(flash.Text, "aerie") || !strings.Contains(flash.Text, remoteTopicBranch) {
		t.Errorf("refusal = %q, want the host and the branch named", flash.Text)
	}
	if p.viewMode != ViewModeBranchPicker {
		t.Errorf("viewMode = %v, want the picker still open after a refusal", p.viewMode)
	}
	assertNoLocalGit(t, log)
}

// Refs is one read and carries both lists. The stash half has no read-only
// surface in the Git tab yet — its only reader is the stash-pop confirm, which
// is a write — so this proves the wire rather than a pane.
func TestBoundRefsCarryTheHostsBranchesAndStashes(t *testing.T) {
	var args []string
	src := &remoteRepoSource{
		hostID:      "aerie",
		workspaceID: hostWorkspaceID,
		run: func(_ context.Context, _ string, a []string, out any) error {
			args = a
			*out.(*reposervice.RefsResult) = hostRefsResult()
			return nil
		},
	}

	refs, err := src.Refs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "repo refs --workspace "+hostWorkspaceID+" --json" {
		t.Errorf("args = %q", got)
	}
	// The picker lists what a local one lists: this repository's own branches.
	if len(refs.Branches) != 2 || refs.Branches[0].Upstream != "origin/"+remoteBranch ||
		refs.Branches[0].Ahead != 2 || refs.Branches[0].Behind != 1 {
		t.Errorf("branches = %+v, want the host's local branches with their tracking", refs.Branches[0])
	}
	if len(refs.Stashes) != 1 || refs.Stashes[0].Ref != "stash@{0}" ||
		refs.Stashes[0].Branch != remoteBranch || refs.Stashes[0].Message != "REMOTE-STASH" {
		t.Errorf("stashes = %+v, want the host's stash", refs.Stashes)
	}
}

// Author and path narrow the log on the host, because a filter applied to the
// page in hand would present itself as an answer about the whole history.
// Subject search is the viewer's, over the rows it already has, and costs the
// host nothing.
func TestBoundAuthorFilterGoesToTheHostAndSearchStaysHere(t *testing.T) {
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)

	// Onto a commit, then f: filter by its author.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	_ = drive(t, p, cmd)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "f", Code: 'f'})
	_ = drive(t, p, cmd)

	want := "repo history --workspace " + hostWorkspaceID + " --limit 50 --author " + remoteCommitAuthor + " --json"
	history := verbCalls(*calls, "history")
	if len(history) != 2 || history[1] != want {
		t.Fatalf("history calls =\n%v\nwant the second to be\n%q", history, want)
	}
	if !p.historyFilterActive || len(p.filteredCommits) == 0 {
		t.Errorf("the author filter produced no rows: active=%v rows=%d", p.historyFilterActive, len(p.filteredCommits))
	}

	// The path filter is the host's too.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "p", Code: 'p'})
	_ = drive(t, p, cmd)
	for _, key := range remoteMarker {
		_, cmd = p.Update(tea.KeyPressMsg{Text: string(key), Code: key})
		_ = drive(t, p, cmd)
	}
	_, cmd = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = drive(t, p, cmd)
	history = verbCalls(*calls, "history")
	if len(history) != 3 || !strings.Contains(history[2], "--path "+remoteMarker) {
		t.Fatalf("history calls =\n%v\nwant the third to carry --path", history)
	}

	// Search runs here: it reads no host verb at all.
	before := len(*calls)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "/", Code: '/'})
	_ = drive(t, p, cmd)
	for _, key := range remoteCommitSubject {
		_, cmd = p.Update(tea.KeyPressMsg{Text: string(key), Code: key})
		_ = drive(t, p, cmd)
	}
	if p.historySearchState == nil || len(p.historySearchState.Matches) == 0 {
		t.Fatalf("search over the loaded rows matched nothing: %+v", p.historySearchState)
	}
	if len(*calls) != before {
		t.Errorf("searching asked the host for %v", (*calls)[before:])
	}
}

// The branch row is answered once, by the status read, and the history load
// leaves it alone. Two answers to one question is how a pane comes to show an
// ahead/behind count from a different instant than the branch beside it.
func TestBoundBranchRowIsReadOnceFromStatus(t *testing.T) {
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())

	if got := len(verbCalls(*calls, "status")); got != 1 {
		t.Errorf("status calls = %d, want one", got)
	}
	if got := len(verbCalls(*calls, "history")); got != 1 {
		t.Errorf("history calls = %d, want one", got)
	}
	if p.pushStatus == nil || p.pushStatus.CurrentBranch != remoteBranch ||
		p.pushStatus.Ahead != 2 || p.pushStatus.Behind != 1 {
		t.Fatalf("pushStatus = %+v, want the host's branch row from the status read", p.pushStatus)
	}
	// The history answer must not blank what the status answered, and the rows
	// keep the pushed state the host stamped on them.
	if p.recentCommits[0].Pushed {
		t.Error("the newest row lost the host's unpushed state")
	}

	// A refresh is still one of each.
	_, cmd := p.Update(plugin.HostInventoryMsg{})
	applyStatus(t, p, cmd)
	if got, want := len(verbCalls(*calls, "status")), 2; got != want {
		t.Errorf("status calls = %d, want %d", got, want)
	}
	if got, want := len(verbCalls(*calls, "history")), 2; got != want {
		t.Errorf("history calls = %d, want %d", got, want)
	}
	if p.pushStatus.Ahead != 2 || p.pushStatus.CurrentBranch != remoteBranch {
		t.Errorf("pushStatus after a refresh = %+v", p.pushStatus)
	}
}

// The local half is today's code routed, not rewritten: the rows a local
// sidebar lists are the rows the two log readers have always produced.
func TestLocalHistoryLoadersKeepTodaysRows(t *testing.T) {
	root, hash := localRepoFixture(t)
	src := localRepoSource{root: root}

	want, wantPush, err := GetCommitHistoryWithPushStatus(root, commitHistoryPageSize)
	if err != nil {
		t.Fatal(err)
	}
	page, err := src.History(context.Background(), HistoryRequest{Limit: commitHistoryPageSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != len(want) || len(want) == 0 {
		t.Fatalf("history = %d rows, want %d", len(page.Commits), len(want))
	}
	if page.Commits[0].Hash != want[0].Hash || page.Commits[0].Subject != want[0].Subject {
		t.Errorf("row = %+v, want %+v", page.Commits[0], want[0])
	}
	// The branch row still arrives with the local history load, exactly as it
	// always has.
	if page.Push == nil || page.Push.CurrentBranch != wantPush.CurrentBranch {
		t.Errorf("push = %+v, want %+v", page.Push, wantPush)
	}

	wantDetail, err := GetCommitDetail(root, hash)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := src.CommitDetail(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if detail == nil || detail.Hash != wantDetail.Hash || len(detail.Files) != len(wantDetail.Files) {
		t.Errorf("detail = %+v, want %+v", detail, wantDetail)
	}

	wantBranches, err := GetBranches(root)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := src.Refs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Branches) != len(wantBranches) || len(wantBranches) == 0 ||
		refs.Branches[0].Name != wantBranches[0].Name {
		t.Errorf("branches = %+v, want %+v", refs.Branches, wantBranches)
	}
}
