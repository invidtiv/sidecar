package overview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func selectedRemoteSession(t *testing.T, m *Model) string {
	t.Helper()
	selected, ok := m.SelectedWorkspace()
	if !ok || !selected.Remote() {
		t.Fatal("fixture did not select a remote row")
	}
	return selected.TmuxName
}

func handleRelayedLayout(t *testing.T, m *Model, event hostproto.UIRequest) {
	t.Helper()
	req := requestFromAnnouncement(event)
	if cmd := m.handleUIRequest(req); cmd != nil {
		run(t, m, cmd)
	}
}

func TestRelayedLayoutApplyOpensHostFileNotLocalTwin(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)

	req := requestFromAnnouncement(relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"twin.txt:20"},
	}))
	cmd := m.handleUIRequest(req)
	if m.preview.doc == nil {
		t.Fatal("relayed apply opened no Document pane")
	}
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 6)
	}
	if cmd != nil {
		run(t, m, cmd)
	}
	got := ansi.Strip(m.preview.doc.view().View())
	if !strings.Contains(got, remoteMarker) {
		t.Fatalf("document missing remote bytes: %q", got)
	}
	if strings.Contains(got, localTwinMarker) {
		t.Fatalf("document showed this machine's twin: %q", got)
	}
	if fake.resolves == 0 || fake.lastTarget != "twin.txt" {
		t.Fatalf("source resolve = %d target %q, want twin.txt through Source", fake.resolves, fake.lastTarget)
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil {
		t.Fatal("viewer tree has no file pane")
	}
}

func TestRelayedLayoutApplyHostOnlyFileNotOnViewerDisk(t *testing.T) {
	m, fake, root := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"host-only.txt"},
	}))
	if m.preview.doc == nil {
		t.Fatal("host-only file did not open; local resolve probably ran against " + root)
	}
	if fake.resolves == 0 || fake.lastTarget != "host-only.txt" {
		t.Fatalf("source resolve = %d target %q", fake.resolves, fake.lastTarget)
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
}

func TestRelayedLayoutApplyResolveFailureDeclinesWithoutCommit(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	fake.resolveErr = errors.New("host resolve boom")
	session := selectedRemoteSession(t, m)
	before := previewLayoutSnapshot(m)

	req := requestFromAnnouncement(relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"twin.txt"},
	}))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("failed resolve returned cmd %v", cmd)
	}
	if m.preview.doc != nil {
		t.Fatal("failed resolve opened a Document pane")
	}
	if got := previewLayoutSnapshot(m); got != before {
		t.Fatalf("failed resolve mutated the tree:\nbefore %s\nafter  %s", before, got)
	}
	assertRemoteAck(t, stub, "declined", "host resolve boom")
}

type issueFailSource struct {
	*fakeRemoteContentSource
}

func (s issueFailSource) Resolve(ctx context.Context, src contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if pending.Kind == contentlink.KindIssue {
		return contentlink.Ref{}, errors.New("host issue boom")
	}
	return s.fakeRemoteContentSource.Resolve(ctx, src, pending)
}

func TestRelayedLayoutApplyAllOrNothingOnIssueResolveFailure(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	m.contentSource = issueFailSource{src}
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	before := previewLayoutSnapshot(m)

	req := requestFromAnnouncement(relayedLayoutApplyPanes(session,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"twin.txt"}},
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-a4dd72"}},
	))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("all-or-nothing decline returned cmd %v", cmd)
	}
	if m.preview.doc != nil || m.preview.issue != nil {
		t.Fatal("a declined batch still committed a pane")
	}
	if got := previewLayoutSnapshot(m); got != before {
		t.Fatalf("declined batch mutated the tree:\nbefore %s\nafter  %s", before, got)
	}
	assertRemoteAck(t, stub, "declined", "host issue boom")
}

func TestRelayedLayoutApplyIssueNoteDiffResourceThroughSource(t *testing.T) {
	t.Run("issue", func(t *testing.T) {
		m, src := showingRemoteIssueNoteModel(t)
		stub := &remoteRunnerStub{}
		stub.install(t)
		session := selectedRemoteSession(t, m)
		handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{
			Kind: "issue", Targets: []string{"td-a4dd72"},
		}))
		if m.preview.issue == nil || m.preview.issue.view() == nil {
			t.Fatal("relayed apply opened no Issue pane")
		}
		if src.issueLoads == 0 {
			t.Fatal("issue did not load through the remote source")
		}
		assertRemoteAck(t, stub, "opened", "--action layout")
	})
	t.Run("note", func(t *testing.T) {
		m, src := showingRemoteIssueNoteModel(t)
		stub := &remoteRunnerStub{}
		stub.install(t)
		session := selectedRemoteSession(t, m)
		handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{
			Kind: "note", Targets: []string{"nt-host01"},
		}))
		if m.preview.note == nil || m.preview.note.view() == nil {
			t.Fatal("relayed apply opened no Note pane")
		}
		if src.noteLoads == 0 {
			t.Fatal("note did not load through the remote source")
		}
		assertRemoteAck(t, stub, "opened", "--action layout")
	})
	t.Run("diff", func(t *testing.T) {
		m, src := showingRemoteDiffModel(t)
		stub := &remoteRunnerStub{}
		stub.install(t)
		session := selectedRemoteSession(t, m)
		handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{
			Kind: "diff", Targets: []string{hostOnlyHash},
		}))
		if m.preview.diff == nil || m.preview.diff.view() == nil {
			t.Fatal("relayed apply opened no Diff pane")
		}
		if src.loads == 0 {
			t.Fatal("diff did not load through the remote source")
		}
		assertRemoteAck(t, stub, "opened", "--action layout")
	})
	t.Run("resource", func(t *testing.T) {
		m, src := showingRemoteResourceModel(t)
		stub := &remoteRunnerStub{}
		stub.install(t)
		runRemoteDescribe(t, m)
		session := selectedRemoteSession(t, m)
		handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{
			Kind: "resource", Targets: []string{"CASH-1245"}, Provider: "jira-work",
		}))
		if m.preview.resource == nil || m.preview.resource.view() == nil {
			t.Fatal("relayed apply opened no Resource pane")
		}
		if _, resolves, _, _ := src.stats(); resolves == 0 {
			t.Fatal("resource did not resolve through the remote source")
		}
		assertRemoteAck(t, stub, "opened", "--action layout")
	})
}

func TestRelayedLayoutApplyNewShellDeclines(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	refuseLocalTerminalSplit(t)
	session := selectedRemoteSession(t, m)
	before := previewLayoutSnapshot(m)

	req := requestFromAnnouncement(relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "shell", Name: "dev",
	}))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("new-shell decline returned cmd %v", cmd)
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell) != nil {
		t.Fatal("new shell leaf was created on a remote row")
	}
	if got := previewLayoutSnapshot(m); got != before {
		t.Fatalf("new-shell decline mutated the tree:\nbefore %s\nafter  %s", before, got)
	}
	assertRemoteAck(t, stub, "declined", remoteNewShellReason)
}

func TestRelayedLayoutApplySpecNewShellDeclines(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	refuseLocalTerminalSplit(t)
	session := selectedRemoteSession(t, m)
	before := previewLayoutSnapshot(m)

	req := requestFromAnnouncement(relayedLayoutApplySpec(session, []uirequest.LayoutSpecColumn{
		{Panes: []uirequest.LayoutPane{{Kind: "primary"}}},
		{Panes: []uirequest.LayoutPane{{Kind: "file", Targets: []string{"twin.txt"}}}},
		{Panes: []uirequest.LayoutPane{{Kind: "shell", Name: "dev"}}},
	}))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("spec new-shell decline returned cmd %v", cmd)
	}
	if m.preview.doc != nil {
		t.Fatal("spec with a new shell still opened a file pane")
	}
	if got := previewLayoutSnapshot(m); got != before {
		t.Fatalf("spec new-shell decline mutated the tree:\nbefore %s\nafter  %s", before, got)
	}
	assertRemoteAck(t, stub, "declined", remoteNewShellReason)
}

func TestRelayedLayoutApplyCarriesLiveShellSession(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	refuseLocalTerminalSplit(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	liveSession := termpanes.SessionName(selected.TmuxName)
	shell := &panelayout.Node{ID: 2, Kind: panelayout.Shell}
	m.preview.paneRoot = &panelayout.Node{ID: 3, Split: &panelayout.Split{
		Axis: panelayout.Columns, Ratio: 50,
		A: &panelayout.Node{ID: 1, Kind: panelayout.Primary},
		B: shell,
	}}
	m.preview.paneNextID = 4
	leaf := m.terminalLeaf(shell.ID)
	leaf.Requested = true
	leaf.Session = liveSession
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = selected.ID
	m.WorkspacesView(previewWide, previewTall)

	handleRelayedLayout(t, m, relayedLayoutApplySpec(selected.TmuxName, []uirequest.LayoutSpecColumn{
		{Panes: []uirequest.LayoutPane{{Kind: "primary"}}},
		{Panes: []uirequest.LayoutPane{
			{Kind: "shell", Session: liveSession},
			{Kind: "file", Targets: []string{"twin.txt"}},
		}},
	}))
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil || m.preview.doc == nil {
		t.Fatal("carried-shell spec opened no file pane")
	}
	carried := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if carried == nil {
		t.Fatal("carried shell leaf is missing")
	}
	if got := m.terminalLeaf(carried.ID); got.Session != liveSession {
		t.Fatalf("carried session = %q, want %q", got.Session, liveSession)
	}
	if fake.resolves == 0 {
		t.Fatal("file target did not resolve through Source")
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
}

func TestRelayedLayoutMoveRelaysOntoViewerTree(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document"))
	m.WorkspacesView(previewWide, previewTall)
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	if doc == nil {
		t.Fatal("fixture opened no document pane")
	}
	if got := sessionsGridIDs(m.preview.paneRoot); len(got) != 2 {
		t.Fatalf("fixture grid = %v, want a primary beside a document", got)
	}

	handleRelayedLayout(t, m, relayedLayoutMove(session, uirequest.LayoutMove{From: "2.1", To: "left"}))
	assertRemoteAck(t, stub, "moved", "--action layout")
	if got := sessionsGridIDs(m.preview.paneRoot); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("grid after the move = %v, want one column of two", got)
	}
	if panelayout.Find(m.preview.paneRoot, doc.ID) != doc {
		t.Fatal("the relayed move rebuilt the document leaf instead of grafting it")
	}
}

func TestRelayedLayoutApplyOffScreenDeclines(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	run(t, m, m.SetWorkspacesVisible(false))
	before := previewLayoutSnapshot(m)

	req := requestFromAnnouncement(relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"twin.txt"},
	}))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen apply returned cmd %v", cmd)
	}
	if m.preview.doc != nil {
		t.Fatal("off-screen apply opened a Document pane")
	}
	if got := previewLayoutSnapshot(m); got != before {
		t.Fatal("off-screen apply mutated the tree")
	}
	assertRemoteAck(t, stub, "declined", layoutapply.SessionsNotOnScreenReason)
}

func TestRelayedLayoutMoveOffScreenDeclines(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	run(t, m, m.SetWorkspacesVisible(false))

	req := requestFromAnnouncement(relayedLayoutMove(session, uirequest.LayoutMove{From: "2.1", To: "left"}))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen move returned cmd %v", cmd)
	}
	assertRemoteAck(t, stub, "declined", layoutapply.SessionsNotOnScreenReason)
}

func TestForwardHostUIRequestLayoutApplyThroughAnnouncement(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	event := relayedLayoutApplyPanes(session, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"twin.txt"},
	})
	if cmd := m.forwardHostUIRequests(hosts.Update{HostID: "mac-mini", UIRequest: []hostproto.UIRequest{event}}); cmd != nil {
		run(t, m, cmd)
	}
	if m.preview.doc == nil {
		t.Fatal("forwarded apply opened no Document pane")
	}
	if fake.resolves == 0 {
		t.Fatal("forwarded apply did not resolve through Source")
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
}

func TestHostTUILayoutApplyAnswersLocallyWhenLeaseHeld(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)

	req := sessionsLayoutPayload(t, uirequest.LayoutModeApply, uirequest.LayoutPane{
		Kind: "file", Targets: []string{"README.md"},
	})
	run(t, m, m.handleUIRequest(req))
	if len(stub.calls) != 0 {
		t.Fatalf("host TUI acked remotely: %v", stub.calls)
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("local apply ack = %s %q", ack.Status, ack.Reason)
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil {
		t.Fatal("local apply opened no Document pane")
	}
}

func TestRelayedLayoutApplyWorkingTreeDiffThroughSource(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	session := selectedRemoteSession(t, m)
	handleRelayedLayout(t, m, relayedLayoutApplyPanes(session, uirequest.LayoutPane{Kind: "diff"}))
	if m.preview.diff == nil {
		t.Fatal("empty-target diff apply opened no Diff pane")
	}
	if src.resolves == 0 {
		t.Fatal("working-tree diff did not resolve through Source")
	}
	if src.lastTarget != workspacediff.IdentityWorkingTree && src.lastTarget != "" {
		t.Fatalf("working-tree target = %q", src.lastTarget)
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
}
