package gitstatus

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestStageFolderIsAsyncAndBatched(t *testing.T) {
	tree := NewFileTree(t.TempDir())
	tree.Untracked = []*FileEntry{{
		Path: "bulk/", IsFolder: true, Unstaged: true,
		Children: []*FileEntry{{Path: "bulk/a"}, {Path: "bulk/b"}},
	}}
	var calls [][]string
	p := &Plugin{
		ctx:      &plugin.Context{Epoch: 7},
		repoRoot: tree.workDir,
		hasRepo:  true,
		tree:     tree,
		writeExecutor: func(_ string, args []string) error {
			calls = append(calls, append([]string(nil), args...))
			return nil
		},
	}

	_, cmd := p.updateStatus(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd == nil {
		t.Fatal("stage returned no command")
	}
	if len(calls) != 0 {
		t.Fatal("stage executed Git before the async command ran")
	}
	if p.activeOperation == nil {
		t.Fatal("stage did not record an active operation")
	}

	rawMsg := cmd()
	msg, ok := rawMsg.(operationResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want operationResultMsg", rawMsg)
	}
	if len(calls) != 1 {
		t.Fatalf("Git calls = %d, want 1", len(calls))
	}
	want := []string{"add", "--", "bulk/"}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("Git args = %#v, want %#v", calls[0], want)
	}
	if msg.Epoch != 7 || msg.Kind != operationStage {
		t.Fatalf("result = %#v", msg)
	}
}

func TestStageAndUnstageUseOptionSafeArgv(t *testing.T) {
	tests := []struct {
		name   string
		key    tea.KeyPressMsg
		entry  *FileEntry
		want   []string
		cursor int
	}{
		{name: "stage file", key: tea.KeyPressMsg{Code: 's', Text: "s"}, entry: &FileEntry{Path: "-odd name", Unstaged: true}, want: []string{"add", "--", "-odd name"}},
		{name: "unstage file", key: tea.KeyPressMsg{Code: 'u', Text: "u"}, entry: &FileEntry{Path: "-odd name", Staged: true}, want: []string{"restore", "--staged", "--", "-odd name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := NewFileTree(t.TempDir())
			if tt.entry.Staged {
				tree.Staged = []*FileEntry{tt.entry}
			} else {
				tree.Modified = []*FileEntry{tt.entry}
			}
			var got []string
			p := &Plugin{ctx: &plugin.Context{}, repoRoot: tree.workDir, hasRepo: true, tree: tree, writeExecutor: func(_ string, args []string) error {
				got = append([]string(nil), args...)
				return nil
			}}
			_, cmd := p.updateStatus(tt.key)
			_ = cmd()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Git args = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStageAllAndUnstageAllExactArgv(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want []string
	}{
		{name: "stage all", key: tea.KeyPressMsg{Code: 'S', Text: "S"}, want: []string{"add", "-A"}},
		{name: "unstage all", key: tea.KeyPressMsg{Code: 'U', Text: "U"}, want: []string{"reset", "HEAD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := NewFileTree(t.TempDir())
			tree.Modified = []*FileEntry{{Path: "a", Unstaged: true}}
			var got []string
			p := &Plugin{ctx: &plugin.Context{}, repoRoot: tree.workDir, hasRepo: true, tree: tree, writeExecutor: func(_ string, args []string) error {
				got = append([]string(nil), args...)
				return nil
			}}
			_, cmd := p.updateStatus(tt.key)
			_ = cmd()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Git args = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWriteBusyRefusesSecondOperation(t *testing.T) {
	tree := NewFileTree(t.TempDir())
	tree.Modified = []*FileEntry{{Path: "a", Unstaged: true}}
	p := &Plugin{ctx: &plugin.Context{}, repoRoot: tree.workDir, hasRepo: true, tree: tree, activeOperation: &operationRequest{ID: 1}}

	// The refusal speaks as the waiting source: the user has to act on it.
	_, cmd := p.updateStatus(tea.KeyPressMsg{Code: 's', Text: "s"})
	post, ok := cmd().(notify.PostMsg)
	if !ok || !strings.Contains(post.Notification.Title, "already in progress") {
		t.Fatalf("busy result = %#v", cmd())
	}
	if post.Notification.Source != notify.SourceWaiting {
		t.Fatalf("busy refusal source = %q", post.Notification.Source)
	}
}

func TestWriteBusyRefusesStatusMutationEntryPoints(t *testing.T) {
	tree := NewFileTree(t.TempDir())
	tree.Modified = []*FileEntry{{Path: "a", Unstaged: true}}
	for _, key := range []string{"s", "u", "S", "U", "c", "A", "D", "z", "Z", "ctrl+z", "b", "L"} {
		t.Run(key, func(t *testing.T) {
			p := &Plugin{ctx: &plugin.Context{}, repoRoot: tree.workDir, hasRepo: true, tree: tree, activeOperation: &operationRequest{ID: 1}}
			code := rune(0)
			if len([]rune(key)) == 1 {
				code = []rune(key)[0]
			}
			_, cmd := p.updateStatus(tea.KeyPressMsg{Code: code, Text: key})
			if cmd == nil {
				t.Fatal("mutation was not refused")
			}
			msg := cmd()
			if _, ok := msg.(notify.PostMsg); !ok {
				t.Fatalf("refusal returned %T", msg)
			}
			if p.viewMode != ViewModeStatus {
				t.Fatalf("mutation changed view mode to %v", p.viewMode)
			}
		})
	}
}

func TestWriteBusyRefusesRelevantModalAndHelperFlows(t *testing.T) {
	p := &Plugin{activeOperation: &operationRequest{ID: 1}, discardFile: &FileEntry{Path: "a"}, viewMode: ViewModeConfirmDiscard}
	assertBusy := func(name string, cmd tea.Cmd) {
		t.Helper()
		if cmd == nil {
			t.Fatalf("%s returned nil", name)
		}
		msg := cmd()
		if _, ok := msg.(notify.PostMsg); !ok {
			t.Fatalf("%s returned %T", name, msg)
		}
	}
	assertBusy("commit", p.tryCommit())
	_, discardCmd := p.confirmDiscard()
	assertBusy("discard", discardCmd)
	if p.discardFile == nil || p.viewMode != ViewModeConfirmDiscard {
		t.Fatal("refused discard closed or mutated the modal")
	}
	assertBusy("stash push", p.doStashPush())
	assertBusy("stash pop", p.doStashPop())
	assertBusy("stash apply", p.doStashApply())
	assertBusy("branch switch", p.doSwitchBranch("other"))
}

func TestWriteBusyRefusesRemoteActionBoundariesAndAbort(t *testing.T) {
	assertRefused := func(t *testing.T, p *Plugin, result plugin.Plugin, cmd tea.Cmd, wantMode ViewMode) {
		t.Helper()
		if result != p || cmd == nil {
			t.Fatal("action boundary did not return a refusal")
		}
		if _, ok := cmd().(notify.PostMsg); !ok {
			t.Fatalf("refusal returned %T", cmd())
		}
		if p.viewMode != wantMode {
			t.Fatalf("refusal changed view mode to %v", p.viewMode)
		}
	}

	t.Run("pull", func(t *testing.T) {
		p := &Plugin{activeOperation: &operationRequest{ID: 1}, viewMode: ViewModePullMenu}
		result, cmd := p.executePullMenuAction(pullMenuOptionMerge)
		assertRefused(t, p, result, cmd, ViewModePullMenu)
		if p.pullInProgress {
			t.Fatal("refused pull became active")
		}
	})
	t.Run("push", func(t *testing.T) {
		p := &Plugin{activeOperation: &operationRequest{ID: 1}, viewMode: ViewModePushMenu}
		result, cmd := p.executePushMenuAction(0)
		assertRefused(t, p, result, cmd, ViewModePushMenu)
		if p.pushInProgress {
			t.Fatal("refused push became active")
		}
	})
	t.Run("abort", func(t *testing.T) {
		p := &Plugin{activeOperation: &operationRequest{ID: 1}, viewMode: ViewModePullConflict}
		result, cmd := p.abortPullConflict()
		assertRefused(t, p, result, cmd, ViewModePullConflict)
		if p.pullInProgress {
			t.Fatal("refused abort became active")
		}
	})
}

func TestAbortPullJoinsAndClearsWriteLifecycle(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{Epoch: 3}, viewMode: ViewModePullConflict, pullConflictType: "merge"}
	_, cmd := p.abortPullConflict()
	if cmd == nil || !p.pullInProgress || !p.writeInProgress() {
		t.Fatal("abort did not enter write lifecycle")
	}
	_, _ = p.Update(PullAbortedMsg{Epoch: 3})
	if p.pullInProgress || p.writeInProgress() {
		t.Fatal("abort result did not clear write lifecycle")
	}
}

func TestWriteBusyHidesIncompatibleCommands(t *testing.T) {
	p := &Plugin{activeOperation: &operationRequest{ID: 1}}
	for _, command := range p.Commands() {
		if writeBlockedCommand(command.ID) {
			t.Fatalf("busy command list contains %q", command.ID)
		}
	}
}

func TestOperationResultRejectsStaleEpochAndWrongID(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  operationResultMsg
	}{
		{name: "stale epoch", msg: operationResultMsg{ID: 9, Epoch: 3}},
		{name: "wrong id", msg: operationResultMsg{ID: 8, Epoch: 4}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{ctx: &plugin.Context{Epoch: 4}, activeOperation: &operationRequest{ID: 9, Epoch: 4}}
			_, cmd := p.Update(tt.msg)
			if cmd != nil {
				t.Fatal("rejected result returned a command")
			}
			if p.activeOperation == nil || p.activeOperation.ID != 9 {
				t.Fatal("rejected result cleared the active operation")
			}
		})
	}
}

func TestWriteFailurePreservesSnapshotAndReportsDetail(t *testing.T) {
	entry := &FileEntry{Path: "a", Unstaged: true}
	tree := NewFileTree(t.TempDir())
	tree.Modified = []*FileEntry{entry}
	p := &Plugin{ctx: &plugin.Context{}, repoRoot: tree.workDir, hasRepo: true, tree: tree, writeExecutor: func(_ string, _ []string) error {
		return errors.New("hook rejected the file")
	}}

	_, cmd := p.updateStatus(tea.KeyPressMsg{Code: 's', Text: "s"})
	result := cmd().(operationResultMsg)
	_, alertCmd := p.Update(result)
	post, ok := alertCmd().(notify.PostMsg)
	if !ok {
		t.Fatalf("write failure did not file a notification: %T", alertCmd())
	}
	if p.activeOperation != nil {
		t.Fatal("failed operation remained busy")
	}
	if len(p.tree.Modified) != 1 || p.tree.Modified[0] != entry {
		t.Fatal("failure changed the displayed snapshot")
	}
	if post.Notification.Source != notify.SourceSession || post.Notification.Severity != notify.SeverityError {
		t.Fatalf("write failure notification = %s/%s, want session/error", post.Notification.Source, post.Notification.Severity)
	}
	if !strings.Contains(post.Notification.Title, "hook rejected the file") || !strings.Contains(p.operationError, "hook rejected the file") {
		t.Fatalf("failure detail missing: alert=%q state=%q", post.Notification.Title, p.operationError)
	}
}

func TestRestoreOperationSelectionByPathAndSection(t *testing.T) {
	p := &Plugin{tree: NewFileTree(t.TempDir()), cursor: 2, operationSelection: selectionIdentity{path: "b", wantStaged: true}}
	p.tree.Staged = []*FileEntry{{Path: "a", Staged: true}, {Path: "b", Staged: true}}
	p.tree.Modified = []*FileEntry{{Path: "c", Unstaged: true}}
	p.restoreOperationSelection()
	if p.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", p.cursor)
	}
}

func TestGitWritesInNormalAndLinkedWorktrees(t *testing.T) {
	root := t.TempDir()
	runGitTest(t, root, "init")
	runGitTest(t, root, "config", "user.email", "sidecar@example.test")
	runGitTest(t, root, "config", "user.name", "Sidecar Test")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "tracked")
	runGitTest(t, root, "commit", "-m", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	runGitTest(t, root, "worktree", "add", "-b", "linked-proof", linked)

	for _, dir := range []string{root, linked} {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			path := "-option-safe"
			if err := os.WriteFile(filepath.Join(dir, path), []byte("content\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := executeGitWrite(dir, []string{"add", "--", path}); err != nil {
				t.Fatal(err)
			}
			if got := runGitTest(t, dir, "diff", "--cached", "--name-only", "-z"); got != path+"\x00" {
				t.Fatalf("staged paths = %q", got)
			}
			if err := executeGitWrite(dir, []string{"restore", "--staged", "--", path}); err != nil {
				t.Fatal(err)
			}
			if got := runGitTest(t, dir, "diff", "--cached", "--name-only", "-z"); got != "" {
				t.Fatalf("still staged: %q", got)
			}
		})
	}
}

func TestAmendMessageLoadsAsynchronouslyAndRejectsStaleResult(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{Epoch: 4}, repoRoot: t.TempDir(), hasRepo: true, tree: NewFileTree(t.TempDir())}
	p.recentCommits = []*Commit{{Hash: "abc"}}
	_, cmd := p.updateStatus(tea.KeyPressMsg{Code: 'A', Text: "A"})
	if cmd == nil || !p.amendMessageLoading {
		t.Fatal("amend did not start an asynchronous message load")
	}
	if got := p.commitMessage.Value(); got != "" {
		t.Fatalf("message changed synchronously: %q", got)
	}
	requestID := p.amendMessageRequestID
	_, _ = p.Update(AmendMessageLoadedMsg{Epoch: 3, RequestID: requestID, Message: "stale"})
	if got := p.commitMessage.Value(); got != "" {
		t.Fatalf("stale message applied: %q", got)
	}
	_, _ = p.Update(AmendMessageLoadedMsg{Epoch: 4, RequestID: requestID, Message: "current"})
	if got := p.commitMessage.Value(); got != "current" {
		t.Fatalf("message = %q", got)
	}
}

func TestRemoteGitCommandDisablesInteractivePrompts(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GCM_INTERACTIVE", "Always")
	cmd := remoteGitCommand(t.TempDir(), "fetch")
	got := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "SSH_ASKPASS_REQUIRE=never"} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment missing %q", want)
		}
	}
	if strings.Contains(got, "GIT_TERMINAL_PROMPT=1") || strings.Contains(got, "GCM_INTERACTIVE=Always") {
		t.Fatalf("interactive override survived: %s", got)
	}
}

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// A refusal fires from every mutation key while a write is in flight, so it
// must carry a lease. The `waiting` source is sticky by default: without an
// explicit expiry each impatient keypress would leave a permanent unread entry
// in the notification centre.
func TestWriteBusyNoticeIsLeasedNotSticky(t *testing.T) {
	p := &Plugin{}
	cmd := p.writeBusyToast()
	if cmd == nil {
		t.Fatal("writeBusyToast returned no command")
	}
	post, ok := cmd().(notify.PostMsg)
	if !ok {
		t.Fatalf("want notify.PostMsg, got %T", cmd())
	}
	n := post.Notification
	if n.Source != notify.SourceWaiting || n.Severity != notify.SeverityWarning {
		t.Fatalf("want a waiting warning, got %s/%s", n.Source, n.Severity)
	}
	if n.Sticky || n.ExpiresAt == nil {
		t.Fatalf("refusal must expire on its own: sticky=%v expires=%v", n.Sticky, n.ExpiresAt)
	}
}

// A successful push confirms itself with a flash and leaves no sidebar echo.
func TestPushSuccessFlashesAndPaintsNoSidebarLine(t *testing.T) {
	handler := mouse.NewHandler()
	p := &Plugin{
		ctx:          &plugin.Context{},
		tree:         &FileTree{},
		sidebarWidth: 40,
		mouseHandler: handler,
		hasRepo:      true,
	}
	p.pushInProgress = true

	_, cmd := p.Update(PushSuccessMsg{})
	if cmd == nil {
		t.Fatal("push success produced no command")
	}
	if p.pushInProgress {
		t.Fatal("push success left the in-flight flag set")
	}

	var flashed bool
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case tea.BatchMsg:
			for _, sub := range m {
				walk(sub)
			}
		case appmsg.FlashMsg:
			if strings.Contains(m.Text, "Pushed") {
				flashed = true
			}
		}
	}
	walk(cmd)
	if !flashed {
		t.Fatal("push success did not flash")
	}

	sidebar := p.renderSidebar(20)
	if strings.Contains(sidebar, "Pushed") {
		t.Fatalf("push success painted a sidebar line:\n%s", sidebar)
	}
}
