package gitstatus

import (
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

// repoFingerprint is what "untouched" means for a repository: the same index
// and working tree, and the same commit at HEAD.
//
// Both halves matter and neither alone is enough — a stage moves the first
// without the second, a commit moves both, and a fetch moves neither while
// still having reached the network. It takes git by absolute path so it keeps
// working under the PATH shim that stands in for git during a bound drive.
func repoFingerprint(t *testing.T, gitBin, root string) string {
	t.Helper()
	status, err := exec.Command(gitBin, "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status in %s: %v", root, err)
	}
	head, err := exec.Command(gitBin, "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse in %s: %v", root, err)
	}
	return string(status) + "HEAD " + string(head)
}

// refusalFlash runs one refused gesture and returns the sentence it answered
// with. A refusal that produced no command at all is a silent no-op, which is
// the failure this whole table exists to replace.
func refusalFlash(t *testing.T, p *Plugin, msg tea.KeyPressMsg) string {
	t.Helper()
	_, cmd := p.Update(msg)
	if cmd == nil {
		t.Fatalf("%q refused silently", msg.String())
	}
	flash, ok := cmd().(appmsg.FlashMsg)
	if !ok {
		t.Fatalf("%q produced %#v, want a refusal", msg.String(), flash)
	}
	return flash.Text
}

// stubBrowser keeps a link this machine would open in view of the test rather
// than in front of the developer running it, and returns the links it caught.
func stubBrowser(t *testing.T) *[]string {
	t.Helper()
	var opened []string
	original := browserOpener
	browserOpener = func(url string) tea.Cmd {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { browserOpener = original })
	return &opened
}

// Every gesture with no host verb behind it refuses by name, and neither
// repository moves while it does.
//
// "Neither" is the whole point: the host is asked for nothing at all — not one
// `sidecar repo` call is made by any of these keys — and the twin on this disk
// has the same index and the same HEAD afterwards as before. A refusal that
// returned early after already running git would pass a sentence assertion and
// fail this one.
func TestBoundRefusalsNameTheHostAndTouchNeitherRepository(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)

	twin := p.ctx.WorkDir
	before := repoFingerprint(t, gitBin, twin)
	log := recordLocalGit(t)

	// The cursor is on a file row, which is where every one of these keys has
	// its write meaning.
	cases := []struct {
		key  tea.KeyPressMsg
		want remoteRefusal
	}{
		{tea.KeyPressMsg{Text: "s", Code: 's'}, refuseStage},
		{tea.KeyPressMsg{Text: "u", Code: 'u'}, refuseUnstage},
		{tea.KeyPressMsg{Text: "S", Code: 'S'}, refuseStageAll},
		{tea.KeyPressMsg{Text: "U", Code: 'U'}, refuseUnstageAll},
		{tea.KeyPressMsg{Text: "c", Code: 'c'}, refuseCommit},
		{tea.KeyPressMsg{Text: "A", Code: 'A'}, refuseAmend},
		{tea.KeyPressMsg{Text: "D", Code: 'D'}, refuseDiscard},
		{tea.KeyPressMsg{Text: "P", Code: 'P'}, refusePush},
		{tea.KeyPressMsg{Text: "L", Code: 'L'}, refusePull},
		{tea.KeyPressMsg{Text: "f", Code: 'f'}, refuseFetch},
		{tea.KeyPressMsg{Text: "z", Code: 'z'}, refuseStash},
		{tea.KeyPressMsg{Text: "Z", Code: 'Z'}, refuseStashPop},
		{tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, refuseStashApply},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, refuseOpenEditor},
	}
	for _, tc := range cases {
		t.Run(string(tc.want), func(t *testing.T) {
			asked := len(*calls)
			text := refusalFlash(t, p, tc.key)
			if !strings.Contains(text, "[aerie]") {
				t.Errorf("refusal = %q, want the host named", text)
			}
			if want := remoteRefusals[tc.want]; !strings.Contains(text, want) {
				t.Errorf("refusal = %q, want it to name %q", text, want)
			}
			if got := (*calls)[asked:]; len(got) > 0 {
				t.Errorf("a refused gesture asked the host for %v", got)
			}
			if p.viewMode != ViewModeStatus {
				t.Errorf("viewMode = %v, want a refusal to open no modal", p.viewMode)
			}
		})
	}

	// The branch picker is the one refusal reached through a modal: it lists
	// the host and refuses the checkout, naming the branch as well as the host.
	_, cmd := p.Update(tea.KeyPressMsg{Text: "b", Code: 'b'})
	_ = drive(t, p, cmd)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	_ = drive(t, p, cmd)
	asked := len(*calls)
	text := refusalFlash(t, p, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(text, "[aerie]") || !strings.Contains(text, remoteTopicBranch) {
		t.Errorf("branch refusal = %q, want the host and the branch named", text)
	}
	if !strings.Contains(text, remoteRefusals[refuseBranchSwitch]) {
		t.Errorf("branch refusal = %q, want it to name the gesture", text)
	}
	if got := (*calls)[asked:]; len(got) > 0 {
		t.Errorf("a refused checkout asked the host for %v", got)
	}

	assertNoLocalGit(t, log)
	if after := repoFingerprint(t, gitBin, twin); after != before {
		t.Fatalf("the twin moved while a bound pane refused:\nbefore %q\nafter  %q", before, after)
	}
	// Nothing the host was asked for, across the whole run, was anything but a
	// read: `sidecar repo` has no write sub-verb and this pane invented none.
	for _, call := range *calls {
		if !strings.HasPrefix(call, "repo status ") && !strings.HasPrefix(call, "repo diff ") &&
			!strings.HasPrefix(call, "repo history ") && !strings.HasPrefix(call, "repo commit ") &&
			!strings.HasPrefix(call, "repo refs ") {
			t.Errorf("the host was asked for %q, which is not one of the read verbs", call)
		}
	}
}

// The table is the inventory of what a bound Git tab will not do, so every
// gesture the plan's contract names must have a row in it. A row that stopped
// naming the host would refuse with a sentence that reads as "this file cannot
// be staged", which is the wrong machine's excuse.
func TestRemoteRefusalTableCoversTheContract(t *testing.T) {
	contract := []remoteRefusal{
		refuseStage, refuseUnstage, refuseStageAll, refuseUnstageAll,
		refuseCommit, refuseAmend, refuseDiscard, refusePush, refusePull,
		refuseFetch, refuseBranchSwitch, refuseStash, refuseStashPop,
		refuseStashApply, refuseInit, refuseOpenEditor, refuseBlame,
	}
	for _, what := range contract {
		if remoteRefusals[what] == "" {
			t.Errorf("no refusal row for %q", what)
		}
	}
	if len(remoteRefusals) != len(contract) {
		t.Errorf("the table has %d rows, the contract names %d", len(remoteRefusals), len(contract))
	}
	p := &Plugin{ctx: &plugin.Context{HostID: "aerie"}}
	for what := range remoteRefusals {
		cmd := p.refuseRemote(what)
		flash, ok := cmd().(appmsg.FlashMsg)
		if !ok {
			t.Fatalf("%q produced %#v", what, flash)
		}
		if !strings.Contains(flash.Text, "[aerie]") || !strings.Contains(flash.Text, remoteRefusals[what]) {
			t.Errorf("%q refused with %q", what, flash.Text)
		}
	}
	// Every refused key resolves to a row; a key mapped to a row that does not
	// exist would refuse with its own identifier.
	for key, what := range remoteRefusedKeys {
		if remoteRefusals[what] == "" {
			t.Errorf("key %q refuses with %q, which has no row", key, what)
		}
	}
}

// The footer tells the truth: every command a bound pane advertises is one the
// bound message loop performs, and every command it performs is advertised.
//
// The list is checked against the local set as well, so a renamed or re-scoped
// command cannot come to mean one thing locally and another while bound.
func TestBoundCommandsAreTheReachableSubset(t *testing.T) {
	p, _ := boundHostPlugin(t)
	applyStatus(t, p, p.Start())

	type entry struct{ context, id string }
	want := map[entry]bool{
		{"git-status", "refresh"}:                      true,
		{"git-status", "show-diff"}:                    true,
		{"git-status", "show-history"}:                 true,
		{"git-status", "branch-picker"}:                true,
		{"git-status", "open-in-file-browser"}:         true,
		{"git-status", "toggle-sidebar"}:               true,
		{"git-status-commits", "view-commit"}:          true,
		{"git-status-commits", "search-history"}:       true,
		{"git-status-commits", "toggle-graph"}:         true,
		{"git-status-commits", "filter-author"}:        true,
		{"git-status-commits", "filter-path"}:          true,
		{"git-status-commits", "clear-filter"}:         true,
		{"git-status-commits", "yank-commit"}:          true,
		{"git-status-commits", "yank-id"}:              true,
		{"git-status-commits", "open-in-github"}:       true,
		{"git-status-commits", "next-match"}:           true,
		{"git-status-commits", "prev-match"}:           true,
		{"git-status-commits", "toggle-sidebar"}:       true,
		{"git-history-search", "select"}:               true,
		{"git-history-search", "cancel"}:               true,
		{"git-history-search", "navigate"}:             true,
		{"git-history-search", "toggle-regex"}:         true,
		{"git-history-search", "toggle-case"}:          true,
		{"git-path-filter", "apply-filter"}:            true,
		{"git-path-filter", "cancel"}:                  true,
		{"git-commit-preview", "view-diff"}:            true,
		{"git-commit-preview", "back"}:                 true,
		{"git-commit-preview", "yank-commit"}:          true,
		{"git-commit-preview", "yank-id"}:              true,
		{"git-commit-preview", "open-in-github"}:       true,
		{"git-commit-preview", "open-in-file-browser"}: true,
		{"git-commit-preview", "toggle-sidebar"}:       true,
		{"git-status-diff", "toggle-diff-view"}:        true,
		{"git-status-diff", "toggle-wrap"}:             true,
		{"git-status-diff", "reset-hscroll"}:           true,
		{"git-status-diff", "toggle-sidebar"}:          true,
		{"git-diff", "close-diff"}:                     true,
		{"git-diff", "scroll"}:                         true,
		{"git-diff", "toggle-diff-view"}:               true,
		{"git-diff", "toggle-wrap"}:                    true,
		{"git-diff", "prev-file"}:                      true,
		{"git-diff", "next-file"}:                      true,
		{"git-diff", "open-in-file-browser"}:           true,
	}

	local := New()
	if err := local.Init(&plugin.Context{WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	local.activateRepo(t.TempDir())
	// The IDs are checked, not their contexts: the bound set deliberately puts
	// refresh in git-status, where a local pane reaches it through the watcher
	// instead. What must not happen is a bound footer inventing an ID this
	// plugin does not otherwise have.
	known := map[string]bool{}
	for _, cmd := range local.Commands() {
		known[cmd.ID] = true
	}

	got := map[entry]bool{}
	for _, cmd := range p.Commands() {
		key := entry{cmd.Context, cmd.ID}
		got[key] = true
		if !known[cmd.ID] {
			t.Errorf("bound footer advertises %q, which is not a command this plugin has", cmd.ID)
		}
	}
	for key := range want {
		if !got[key] {
			t.Errorf("bound footer is missing %+v, which the bound key loop performs", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("bound footer advertises %+v, which refuses or does nothing while bound", key)
		}
	}

	// The writes are the ones that must never appear.
	for _, cmd := range p.Commands() {
		switch cmd.ID {
		case "stage-file", "unstage-file", "stage-all", "unstage-all", "commit",
			"amend", "discard-changes", "push", "pull", "fetch", "stash",
			"stash-pop", "stash-apply", "init-repo", "open-file":
			t.Errorf("bound footer advertises the write %q", cmd.ID)
		}
	}
}

// The mouse was inert while bound until this slice, which stopped being
// defensible once the commit list and the diff pane were on screen. The subset
// that maps to gestures the keyboard already performs works; the one that
// reaches a write refuses from the table.
func TestBoundMouseSelectsScrollsAndRefusesTheEditor(t *testing.T) {
	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)
	log := recordLocalGit(t)

	// Clicking a commit row selects it and reads that commit from the host.
	x, y := boundRegion(t, p, regionCommit, 1)
	asked := len(verbCalls(*calls, "commit"))
	_, cmd := p.Update(clickMsg(x, y))
	_ = drive(t, p, cmd)
	if !p.cursorOnCommit() || p.selectedCommitIndex() != 1 {
		t.Fatalf("a click on a commit row left the cursor at %d", p.cursor)
	}
	if got := len(verbCalls(*calls, "commit")); got != asked+1 {
		t.Errorf("commit reads = %d, want one more after the click", got)
	}
	if p.previewCommit == nil || p.previewCommit.Hash != hostCommitHash(1) {
		t.Errorf("preview = %+v, want the host's second commit", p.previewCommit)
	}

	// The wheel scrolls the sidebar, which is cursor movement over host rows.
	cursor := p.cursor
	_, cmd = p.Update(tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
	_ = drive(t, p, cmd)
	if p.cursor == cursor {
		t.Errorf("the wheel did not move the cursor off %d", cursor)
	}

	// Back onto a file row so the right pane is a patch, then click it: that
	// focuses the pane, the same as l/right.
	_, cmd = p.Update(tea.KeyPressMsg{Text: "g", Code: 'g'})
	_ = drive(t, p, cmd)
	_ = p.View(160, 40)
	x, y = boundRegionAny(t, p, regionDiffPane)
	if _, cmd = p.Update(clickMsg(x, y)); cmd != nil {
		t.Error("focusing the diff pane produced a command")
	}
	if p.activePane != PaneDiff {
		t.Error("a click on the diff pane did not focus it")
	}

	// Back to a file row, and double-click it: that is "open in an editor",
	// which refuses by name rather than opening a same-named path here.
	_ = p.View(160, 40)
	x, y = boundRegion(t, p, regionFile, 0)
	_, cmd = p.Update(clickMsg(x, y))
	_ = drive(t, p, cmd)
	_ = p.View(160, 40)
	_, cmd = p.Update(clickMsg(x, y))
	if cmd == nil {
		t.Fatal("a double-click on a bound file row refused silently")
	}
	flash, ok := cmd().(appmsg.FlashMsg)
	if !ok {
		t.Fatalf("a double-click produced %#v, want a refusal", flash)
	}
	if !strings.Contains(flash.Text, "[aerie]") || !strings.Contains(flash.Text, remoteRefusals[refuseOpenEditor]) {
		t.Errorf("double-click refusal = %q", flash.Text)
	}
	assertNoLocalGit(t, log)
}

// boundRegion is the hit region for one row of a rendered bound sidebar.
func boundRegion(t *testing.T, p *Plugin, id string, data int) (int, int) {
	t.Helper()
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID != id {
			continue
		}
		if idx, ok := r.Data.(int); !ok || idx != data {
			continue
		}
		return r.Rect.X, r.Rect.Y
	}
	t.Fatalf("no %q region for row %d", id, data)
	return 0, 0
}

// boundRegionAny is a point well inside a pane-sized region. Its own corner is
// no good: the drag handle is registered over the diff pane's left edge and
// wins the hit test there, exactly as it does for a real pointer.
func boundRegionAny(t *testing.T, p *Plugin, id string) (int, int) {
	t.Helper()
	rect := findRegion(t, p, id)
	return rect.X + rect.W/2, rect.Y + rect.H/2
}

// The contract row for "open in GitHub" says it works: the URL is the host's
// fact, returned by `repo status`, and the browser is this machine's. Nothing
// here asks git on this disk for a remote a bound pane has no root to read.
func TestBoundGitHubLinkComesFromTheHostsRemoteURL(t *testing.T) {
	opened := stubBrowser(t)

	p, calls := boundHostPlugin(t)
	applyStatus(t, p, p.Start())
	_ = p.View(160, 40)
	log := recordLocalGit(t)

	if p.repoRemoteURL != remoteOriginURL {
		t.Fatalf("remote URL = %q, want the host's", p.repoRemoteURL)
	}
	_, cmd := p.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	_ = drive(t, p, cmd)
	asked := len(*calls)
	_, cmd = p.Update(tea.KeyPressMsg{Text: "o", Code: 'o'})
	if cmd == nil {
		t.Fatal("o on a host commit did nothing")
	}
	_ = drive(t, p, cmd)

	want := "https://github.com/aerie/sidecar/commit/" + hostCommitHash(0)
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want %q", *opened, want)
	}
	if got := (*calls)[asked:]; len(got) > 0 {
		t.Errorf("building a link asked the host for %v", got)
	}
	assertNoLocalGit(t, log)
}

// There is no watcher across the boundary: internal/livewatch is a filesystem
// signal and a host's filesystem is not on this one. A bound pane refreshes on
// the host snapshot generation and on r, and starts nothing that would poll.
func TestBoundPaneStartsNoWatcher(t *testing.T) {
	src := &fakeRepoSource{status: remoteRepoStatus(hostStatusResult())}
	p, _ := boundGitPlugin(t, connectedHostContext())
	p.repoSourceOverride = src
	applyStatus(t, p, p.Start())

	if cmd := p.startWatcher(); cmd != nil {
		t.Fatal("a bound pane produced a watcher command")
	}
	// The watcher lifecycle messages are not routed at all while bound, so a
	// stray one cannot attach a watcher to a pane that has no repository root.
	if _, cmd := p.Update(WatchStartedMsg{}); cmd != nil {
		t.Error("a bound pane answered a watcher message")
	}
	if p.watcher != nil {
		t.Fatal("a bound pane attached a filesystem watcher")
	}
	if p.repoRoot != "" || p.hasRepo {
		t.Fatalf("a bound pane has a repository root %q on this disk", p.repoRoot)
	}
}
