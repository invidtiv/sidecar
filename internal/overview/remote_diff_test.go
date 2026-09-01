package overview

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	hostOnlyDiffMarker = "HOST-ONLY-DIFF"
	hostOnlyHash       = "aabbcc1"
)

type fakeRemoteDiffSource struct {
	mu          sync.Mutex
	file        fakeRemoteFileSource
	snapshot    *workspacediff.Snapshot
	commit      *workspacediff.CommitDetail
	revision    string
	notModified bool
	loadErr     error
	loads       int
	resolves    int
	lastIfRev   string
	lastOp      string
	lastTarget  string
}

func (f *fakeRemoteDiffSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	f.lastTarget = pending.Raw
	switch pending.Kind {
	case contentlink.KindFile:
		return contentlink.Ref{Kind: contentlink.KindFile, Value: pending.Raw}, nil
	case contentlink.KindDiff:
		raw := pending.Raw
		if raw == hostOnlyHash || strings.HasPrefix(raw, "c:"+hostOnlyHash) {
			return contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:" + hostOnlyHash}, nil
		}
		target, ok := workspacediff.ParseSpec(raw)
		if !ok {
			return contentlink.Ref{}, nil
		}
		return contentlink.Ref{Kind: contentlink.KindDiff, Value: target.Identity()}, nil
	default:
		return contentlink.Ref{}, nil
	}
}

func (f *fakeRemoteDiffSource) LoadDocument(ctx context.Context, src contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	return f.file.LoadDocument(ctx, src, req)
}

func (f *fakeRemoteDiffSource) LoadIssue(context.Context, contentpanes.SourceContext, contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	return contentpanes.IssueReadResult{}, nil
}

func (f *fakeRemoteDiffSource) LoadNote(context.Context, contentpanes.SourceContext, contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	return contentpanes.NoteReadResult{}, nil
}

func (f *fakeRemoteDiffSource) LoadDiff(_ context.Context, _ contentpanes.SourceContext, req contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	f.lastIfRev = req.IfRevision
	f.lastOp = req.Operation
	if req.Ref.Value != "" {
		f.lastTarget = req.Ref.Value
	}
	if f.loadErr != nil {
		return contentpanes.DiffReadResult{}, f.loadErr
	}
	rev := f.revision
	if rev == "" {
		rev = "v1:diff-1"
	}
	if f.notModified {
		return contentpanes.DiffReadResult{NotModified: true, Revision: rev}, nil
	}
	payload := contentpanes.DiffPayload{Snapshot: f.snapshot, Commit: f.commit}
	if req.Operation == contentservice.OpCommit && f.commit != nil {
		payload.Snapshot = nil
	}
	return contentpanes.DiffReadResult{Value: payload, Revision: rev}, nil
}

func hostDiffSnapshot() *workspacediff.Snapshot {
	raw := "diff --git a/host-only.txt b/host-only.txt\n--- /dev/null\n+++ b/host-only.txt\n@@ -0,0 +1,1 @@\n+" + hostOnlyDiffMarker + "\n"
	return &workspacediff.Snapshot{
		State: workspacediff.LoadStateReady, WorkingTree: raw,
		Files: []workspacediff.File{{Path: "host-only.txt", Raw: raw, Additions: 1}},
	}
}

func showingRemoteDiffModel(t *testing.T) (*Model, *fakeRemoteDiffSource) {
	t.Helper()
	src := &fakeRemoteDiffSource{
		snapshot: hostDiffSnapshot(),
		commit:   &workspacediff.CommitDetail{Hash: hostOnlyHash + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ShortHash: hostOnlyHash, Subject: "host only"},
		revision: "v1:diff-1",
		file:     fakeRemoteFileSource{body: remoteMarker + "\n", revision: "v1:1"},
	}
	m, _, _ := showingRemoteTwinModel(t, nil)
	m.contentSource = src
	return m, src
}

func TestRemoteSessionsRowOpensHostDiffNotLocalTwin(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	cmd := m.openPreviewDiff(workspacediff.MustParse(hostOnlyHash))
	if cmd == nil {
		t.Fatal("openPreviewDiff returned nil on a showing remote row")
	}
	run(t, m, cmd)
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("remote diff click opened no Diff pane")
	}
	view := m.preview.diff.view()
	view.SetSize(80, 16)
	got := ansi.Strip(view.Render(80, 16, workspacediff.RenderOpts{}))
	if !strings.Contains(got, hostOnlyDiffMarker) && (view.CommitDetail == nil || view.CommitDetail.ShortHash != hostOnlyHash) {
		t.Fatalf("diff missing host data: view=%q detail=%#v", got, view.CommitDetail)
	}
	if src.loads == 0 {
		t.Fatal("diff did not load through the remote source")
	}
}

func TestRemoteSessionsPrepareMakesHostOnlyHashClickable(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	if _, _, ok := terminallink.ResolveGitSpec(ws.Path, hostOnlyHash); ok {
		t.Fatal("local ResolveGitSpec found the host-only hash; the span would not prove remote resolution")
	}
	const line = "review " + hostOnlyHash + " please"
	state := preparedPreviewLineForTest(t, m, line)
	span, ok := state.SpanAt(line, 0, strings.Index(line, hostOnlyHash))
	if !ok || span.Kind != contentlink.KindDiff {
		t.Fatalf("host-only hash was not a diff span: ok=%v span=%+v spans=%+v", ok, span, state.Spans(line, 0))
	}
	plan, err := targetactivation.PlanForSpan(span)
	if err != nil {
		t.Fatal(err)
	}
	cmd, handled := m.activatePreviewPlan(plan)
	if !handled || cmd == nil {
		t.Fatalf("click handled=%v cmd=%v", handled, cmd != nil)
	}
	run(t, m, cmd)
	if m.preview.diff == nil {
		t.Fatal("click opened no Diff pane")
	}
	if src.lastTarget != hostOnlyHash && src.lastTarget != "c:"+hostOnlyHash {
		t.Fatalf("resolved %q, want the host-only hash", src.lastTarget)
	}
	if src.loads == 0 {
		t.Fatal("click did not load through the remote source")
	}
}

func TestRemoteWorkingTreeDiffOpenAndRestoreUsesHostSnapshot(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	cmd := m.openPreviewDiff(workspacediff.WorkingTreeTarget())
	if cmd == nil {
		t.Fatal("working-tree open returned nil")
	}
	run(t, m, cmd)
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("working-tree Diff pane did not open")
	}
	view := m.preview.diff.view()
	view.SetSize(80, 16)
	got := ansi.Strip(view.Render(80, 16, workspacediff.RenderOpts{}))
	if !strings.Contains(got, hostOnlyDiffMarker) && view.SelectedFileName() != "host-only.txt" {
		t.Fatalf("working-tree missing host snapshot: %q files=%v", got, view.FileNames())
	}
	if src.lastOp != "" && src.lastOp != contentservice.OpWorkingTree {
		t.Fatalf("working-tree operation = %q", src.lastOp)
	}

	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	layout := &state.PaneLayoutJSON{
		Root: ws.Path, Surface: ws.ID, Open: true, HostID: "mac-mini", FocusKind: "diff",
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Kind: "diff", DiffTabs: []state.PaneDiffTabJSON{{Spec: "wt"}}},
		},
	}
	m2, src2 := showingRemoteDiffModel(t)
	run(t, m2, m2.restoreSpecPreviewLayout(layout))
	if m2.preview.diff == nil || m2.preview.diff.view() == nil {
		t.Fatal("restore dropped the remote Diff tab")
	}
	if src2.loads == 0 {
		t.Fatal("restore did not load through the remote source")
	}
	if m2.preview.resource != nil {
		t.Fatal("restore opened a resource pane")
	}
}

func TestLocalSessionsDiffLoadStartsNoRemoteContentCommand(t *testing.T) {
	stub := &remoteRunnerStub{}
	stub.install(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	fake := &fakeRemoteDiffSource{snapshot: hostDiffSnapshot()}
	m.contentSource = fake
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if fake.loads != 0 {
		t.Fatalf("local diff load used the remote source: %d", fake.loads)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("local diff load invoked remote sidecar: %v", stub.calls)
	}
}

func TestRemoteSessionsMissingContentReadV1ToastsDiffAndDoesNotOpen(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{})
	stub := &remoteRunnerStub{}
	stub.install(t)

	cmd := m.openPreviewDiff(workspacediff.WorkingTreeTarget())
	toast, ok := toastFrom(t, cmd)
	if !ok {
		t.Fatal("missing ContentReadV1 returned no toast")
	}
	if !strings.Contains(toast.Message, "Update Sidecar on mac-mini") {
		t.Fatalf("toast = %q", toast.Message)
	}
	if m.preview.diff != nil {
		t.Fatalf("opened a diff pane: %#v", m.preview.diff)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("host without ContentReadV1 was invoked: %v", stub.calls)
	}
}

func TestRemoteDiffRefreshChangedNotModifiedHiddenAndFailure(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := m.preview.diff.view()
	view.SetSize(80, 16)
	if src.lastIfRev != "" {
		t.Fatalf("first load IfRevision = %q, want empty", src.lastIfRev)
	}

	src.mu.Lock()
	changed := hostDiffSnapshot()
	changed.WorkingTree = strings.ReplaceAll(changed.WorkingTree, hostOnlyDiffMarker, "CHANGED-HOST-DIFF")
	changed.Files[0].Raw = changed.WorkingTree
	src.snapshot = changed
	src.revision = "v1:diff-2"
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewDiffs()...))
	got := ansi.Strip(view.Render(80, 16, workspacediff.RenderOpts{}))
	if !strings.Contains(got, "CHANGED-HOST-DIFF") && view.SelectedFileName() == "" {
		// refresh may keep files; check snapshot
		if view.Snapshot == nil || !strings.Contains(view.Snapshot.WorkingTree, "CHANGED-HOST-DIFF") {
			t.Fatalf("changed payload did not refresh: %q snapshot=%v", got, view.Snapshot != nil)
		}
	}
	if src.lastIfRev != "v1:diff-1" {
		t.Fatalf("refresh IfRevision = %q, want v1:diff-1", src.lastIfRev)
	}

	src.mu.Lock()
	src.notModified = true
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewDiffs()...))
	if m.preview.diff.hostNotice != "" {
		t.Fatalf("notModified set host notice %q", m.preview.diff.hostNotice)
	}

	src.mu.Lock()
	src.notModified = false
	src.loadErr = context.DeadlineExceeded
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewDiffs()...))
	if view.Snapshot == nil || !strings.Contains(view.Snapshot.WorkingTree, "CHANGED-HOST-DIFF") && !strings.Contains(view.Snapshot.WorkingTree, hostOnlyDiffMarker) {
		t.Fatal("failed refresh dropped the last body")
	}
	if m.preview.diff.hostNotice != remoteDocumentStaleNotice {
		t.Fatalf("host notice = %q, want %q", m.preview.diff.hostNotice, remoteDocumentStaleNotice)
	}

	src.mu.Lock()
	src.loadErr = nil
	loadsAfterFail := src.loads
	src.mu.Unlock()
	m.WorkspacesView(40, 10)
	run(t, m, tea.Batch(m.refreshPreviewDiffs()...))
	src.mu.Lock()
	hiddenLoads := src.loads
	src.mu.Unlock()
	if hiddenLoads != loadsAfterFail {
		t.Fatalf("hidden pane issued a remote check: before=%d after=%d", loadsAfterFail, hiddenLoads)
	}
}

func TestRemoteDiffDoesNotLivewatchLocalPath(t *testing.T) {
	m, _ := showingRemoteDiffModel(t)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if targets := m.previewDiffTargets(); len(targets) != 0 {
		t.Fatalf("remote diff livewatch targets = %v", targets)
	}
	if cmd := m.resolvePreviewDiffAdmin(); cmd != nil {
		t.Fatal("remote diff queued a local git admin resolve")
	}
}

func TestRemoteResourceStillRefused(t *testing.T) {
	m, _ := showingRemoteDiffModel(t)
	cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
		Kind: targetactivation.PlanOpenResource, Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
	})
	run(t, m, cmd)
	if handled || cmd != nil {
		t.Fatalf("resource: handled=%v cmd=%v", handled, cmd != nil)
	}
	if m.preview.resource != nil {
		t.Fatalf("opened a resource pane: %#v", m.preview.resource)
	}
}

func TestRemoteDiffYankAndNavigationStay(t *testing.T) {
	m, _ := showingRemoteDiffModel(t)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil {
		t.Fatal("no diff pane")
	}
	m.preview.diff.focused = true
	view := m.preview.diff.view()
	view.SetSize(80, 16)
	handled, cmd := m.previewDiffPaneKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !handled {
		t.Fatal("j was not handled")
	}
	_ = cmd
	handled, cmd = m.previewDiffPaneKey(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if !handled || cmd == nil {
		t.Fatalf("Y on remote diff: handled=%v cmd=%v", handled, cmd != nil)
	}
}
