package overview

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func relayedFileAnnouncement(session, path string, line int) hostproto.UIRequest {
	event := relayedOpenAnnouncement("req-relay-file", session, "file", path, "")
	event.Target.Line = line
	return event
}

func relayedOpenAnnouncement(id, session, kind, value, provider string) hostproto.UIRequest {
	return hostproto.UIRequest{
		ID: id, Action: hostproto.UIRequestActionOpen, TTLMs: 5000,
		CreatedAt: time.Now().UTC(),
		Origin:    hostproto.UIRequestOrigin{TmuxSession: session, HostID: "mac-mini"},
		Target:    hostproto.UIRequestTarget{Kind: kind, Value: value, Provider: provider},
	}
}

func assertRemoteAck(t *testing.T, stub *remoteRunnerStub, wantStatus, wantSubstr string) {
	t.Helper()
	if len(stub.calls) != 1 {
		t.Fatalf("ack invocations = %v", stub.calls)
	}
	joined := strings.Join(stub.argv(t, 0), " ")
	if !strings.Contains(joined, "--status "+wantStatus) {
		t.Fatalf("ack = %s, want status %s", joined, wantStatus)
	}
	if wantSubstr != "" && !strings.Contains(joined, wantSubstr) {
		t.Fatalf("ack = %s, want %q", joined, wantSubstr)
	}
}

func TestRelayedKindUIRequestOpensHostFileNotLocalTwin(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok || !selected.Remote() {
		t.Fatal("fixture did not select a remote row")
	}

	event := relayedFileAnnouncement(selected.TmuxName, "twin.txt", 20)
	cmd := m.handleUIRequest(requestFromAnnouncement(event))
	if cmd == nil {
		t.Fatal("relayed file open produced no command")
	}
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 6)
	}
	run(t, m, cmd)
	if m.preview.doc == nil {
		t.Fatal("relayed open opened no Document pane")
	}
	got := ansi.Strip(m.preview.doc.view().View())
	if !strings.Contains(got, remoteMarker) {
		t.Fatalf("document missing remote bytes: %q", got)
	}
	if strings.Contains(got, localTwinMarker) {
		t.Fatalf("document showed this machine's twin: %q", got)
	}
	if fake.lastTarget != "twin.txt" {
		t.Fatalf("resolved %q, want the original token", fake.lastTarget)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("ack invocations = %v", stub.calls)
	}
	args := stub.argv(t, 0)
	if args[0] != "request" || args[1] != "ack" {
		t.Fatalf("ack argv = %v", args)
	}
	if stub.calls[0].HostID != "mac-mini" {
		t.Fatalf("ack host = %q", stub.calls[0].HostID)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--id req-relay-file") || !strings.Contains(joined, "--action open") || !strings.Contains(joined, "--status opened") {
		t.Fatalf("ack argv missing fields: %v", args)
	}
}

func TestRelayedOpenResolveFailureAcksDeclined(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	fake.resolveErr = errors.New("host resolve boom")
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0)))
	if cmd == nil {
		t.Fatal("resolve failure produced no toast command")
	}
	if m.preview.doc != nil {
		t.Fatal("resolve failure opened a Document pane")
	}
	if len(stub.calls) != 1 {
		t.Fatalf("ack invocations = %v", stub.calls)
	}
	joined := strings.Join(stub.argv(t, 0), " ")
	if !strings.Contains(joined, "--status declined") || !strings.Contains(joined, "host resolve boom") {
		t.Fatalf("decline ack = %s", joined)
	}
}

func TestForwardHostUIRequestOpensThroughAnnouncement(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	event := relayedFileAnnouncement(selected.TmuxName, "twin.txt", 20)
	cmd := m.forwardHostUIRequests(hosts.Update{HostID: "mac-mini", UIRequest: []hostproto.UIRequest{event}})
	if cmd == nil {
		t.Fatal("forwarded announcement produced no command")
	}
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 6)
	}
	run(t, m, cmd)
	got := ansi.Strip(m.preview.doc.view().View())
	if !strings.Contains(got, remoteMarker) {
		t.Fatalf("document missing remote bytes: %q", got)
	}
	if strings.Contains(got, localTwinMarker) {
		t.Fatalf("document showed this machine's twin: %q", got)
	}
	if fake.loads == 0 {
		t.Fatal("remote source was not loaded")
	}
}

func TestOverview_HandleUIRequestBindsRelayedHostSession(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	req := requestFromAnnouncement(relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0))
	bound, ok := m.bindOpenWorkspace(req)
	if !ok {
		t.Fatal("relayed origin did not bind")
	}
	if bound.HostID != "mac-mini" || bound.TmuxName != selected.TmuxName {
		t.Fatalf("bound = %+v", bound)
	}

	localReq := uirequest.Request{
		ID: "local-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: selected.TmuxName},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"},
	}
	if _, ok := m.bindOpenWorkspace(localReq); ok {
		t.Fatal("a local request bound a remote row by tmux name")
	}
}

func TestRelayedOpenOffScreenDeclinesWithoutQueue(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	run(t, m, m.SetWorkspacesVisible(false))

	req := requestFromAnnouncement(relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen relayed open returned cmd %v", cmd)
	}
	if m.pendingViews[selected.TmuxName] != nil {
		t.Fatal("relayed open queued")
	}
	if len(stub.calls) != 1 {
		t.Fatalf("ack invocations = %v", stub.calls)
	}
	joined := strings.Join(stub.argv(t, 0), " ")
	if !strings.Contains(joined, "--status declined") || !strings.Contains(joined, relayedOpenNotOnScreenReason) {
		t.Fatalf("decline ack = %s", joined)
	}
	if m.preview.doc != nil {
		t.Fatal("off-screen relayed open still opened a document")
	}
}

func TestRelayedOpenUnselectedRowDeclinesWithoutQueue(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	if !m.workspaces.SelectID("a") {
		t.Fatal("could not select a local row")
	}
	run(t, m, m.bindPreview(false))

	req := requestFromAnnouncement(relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("unselected relayed open returned cmd %v", cmd)
	}
	if m.pendingViews[selected.TmuxName] != nil {
		t.Fatal("relayed open queued")
	}
	if len(stub.calls) != 1 {
		t.Fatalf("ack invocations = %v", stub.calls)
	}
	joined := strings.Join(stub.argv(t, 0), " ")
	if !strings.Contains(joined, "--status declined") {
		t.Fatalf("decline ack = %s", joined)
	}
}

func TestOpenSessionsHitsTheSameHandler(t *testing.T) {
	m, root := layoutSessionsModel(t)
	req := uirequest.Request{
		ID: "sessions-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{Sessions: true},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: filepath.Join(root, "README.md")},
	}
	if cmd := m.handleUIRequest(req); cmd == nil {
		t.Fatal("open --sessions produced no command")
	} else {
		run(t, m, cmd)
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document) == nil && m.preview.doc == nil {
		t.Fatal("open --sessions did not open a Document pane")
	}
}

func TestOpenSessionsOffScreenDeclines(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.SetWorkspacesVisible(false))
	req := uirequest.Request{
		ID: "sessions-open-off", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{Sessions: true},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "README.md"},
	}
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen --sessions open returned cmd %v", cmd)
	}
	if m.pendingViews["sc-alpha"] != nil {
		t.Fatal("open --sessions queued")
	}
}

func TestHostTUISkipsActionOpenWhenLeaseIsForeign(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	original := tty.SessionOwner
	t.Cleanup(func() { tty.SessionOwner = original })
	tty.SessionOwner = func(string) string { return "laptop-99" }

	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	req := uirequest.Request{
		ID: "foreign-lease-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: selected.TmuxName},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "README.md"},
	}
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("host TUI applied an open it does not own: %v", cmd)
	}
	if m.pendingViews[selected.TmuxName] != nil {
		t.Fatal("foreign-lease open queued")
	}
	if m.preview.doc != nil {
		t.Fatal("foreign-lease open opened a document")
	}
}

func TestRelayedKindUIRequestOpensHostIssueNotLocalTwin(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-issue", selected.TmuxName, "issue", "td-a4dd72", "")))
	if cmd == nil {
		t.Fatal("relayed issue open produced no command")
	}
	run(t, m, cmd)
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("relayed open opened no Issue pane")
	}
	view := m.preview.issue.view()
	view.SetSize(80, 16)
	got := ansi.Strip(view.View())
	if !strings.Contains(got, remoteIssueTitle) {
		t.Fatalf("issue missing host title: %q", got)
	}
	if src.issueLoads == 0 {
		t.Fatal("issue did not load through the remote source")
	}
	assertRemoteAck(t, stub, "opened", "--id req-relay-issue")
}

func TestRelayedKindUIRequestOpensHostNoteNotLocalTwin(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-note", selected.TmuxName, "note", "nt-host01", "")))
	if cmd == nil {
		t.Fatal("relayed note open produced no command")
	}
	run(t, m, cmd)
	if m.preview.note == nil || m.preview.note.view() == nil {
		t.Fatal("relayed open opened no Note pane")
	}
	view := m.preview.note.view()
	view.SetSize(80, 12)
	got := ansi.Strip(view.View())
	if !strings.Contains(got, remoteNoteTitle) {
		t.Fatalf("note missing host title: %q", got)
	}
	if src.noteLoads == 0 {
		t.Fatal("note did not load through the remote source")
	}
	assertRemoteAck(t, stub, "opened", "--id req-relay-note")
}

func TestRelayedKindUIRequestOpensHostDiffNotLocalTwin(t *testing.T) {
	m, src := showingRemoteDiffModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-diff", selected.TmuxName, "diff", hostOnlyHash, "")))
	if cmd == nil {
		t.Fatal("relayed diff open produced no command")
	}
	run(t, m, cmd)
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("relayed open opened no Diff pane")
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
	assertRemoteAck(t, stub, "opened", "--id req-relay-diff")
}

func TestRelayedKindUIRequestOpensHostResourceNotViewerSnapshot(t *testing.T) {
	m, src := showingRemoteResourceModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)
	runRemoteDescribe(t, m)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-resource", selected.TmuxName, "resource", "CASH-1245", "jira-work")))
	if cmd == nil {
		t.Fatal("relayed resource open produced no command")
	}
	run(t, m, cmd)
	if m.preview.resource == nil || m.preview.resource.view() == nil {
		t.Fatal("relayed open opened no Resource pane")
	}
	doc, ok := m.preview.resource.view().Document()
	if !ok || doc.Title != hostResourceTitle {
		t.Fatalf("document = %+v, want host title", doc)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("relayed open asked the viewer snapshot resolver: %v", refs)
	}
	if _, resolves, _, _ := src.stats(); resolves == 0 {
		t.Fatal("did not resolve through the remote source")
	}
	assertRemoteAck(t, stub, "opened", "--id req-relay-resource")
}

func TestRelayedResourceOpenWithoutHostMatchersDeclines(t *testing.T) {
	m, _ := showingRemoteResourceModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	if cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-resource-empty", selected.TmuxName, "resource", "CASH-1245", "jira-work"))); cmd != nil {
		t.Fatalf("empty host matcher cache returned cmd %v", cmd)
	}
	if m.preview.resource != nil {
		t.Fatal("viewer snapshot matchers opened a Resource pane")
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("empty host cache asked the viewer resolver: %v", refs)
	}
	assertRemoteAck(t, stub, "declined", "no live matchers")
}

func TestRelayedIssueResolveFailureAcksDeclined(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	src.resolveErr = errors.New("host issue boom")
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}

	cmd := m.handleUIRequest(requestFromAnnouncement(relayedOpenAnnouncement(
		"req-relay-issue-fail", selected.TmuxName, "issue", "td-a4dd72", "")))
	if cmd == nil {
		t.Fatal("resolve failure produced no toast command")
	}
	if m.preview.issue != nil {
		t.Fatal("resolve failure opened an Issue pane")
	}
	assertRemoteAck(t, stub, "declined", "host issue boom")
}

func TestForwardHostUIRequestOpensIssueThroughAnnouncement(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	event := relayedOpenAnnouncement("req-relay-issue-fwd", selected.TmuxName, "issue", "td-a4dd72", "")
	cmd := m.forwardHostUIRequests(hosts.Update{HostID: "mac-mini", UIRequest: []hostproto.UIRequest{event}})
	if cmd == nil {
		t.Fatal("forwarded announcement produced no command")
	}
	run(t, m, cmd)
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("forwarded issue open opened no pane")
	}
	if src.issueLoads == 0 {
		t.Fatal("issue did not load through the remote source")
	}
	assertRemoteAck(t, stub, "opened", "--id req-relay-issue-fwd")
}

func TestRelayedOpenSkippedWhenBoundWorkspaceOwnsLanding(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	m.RelayedLanding = func(uirequest.Request) bool { return false }
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	req := requestFromAnnouncement(relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("overview handled a request the bound workspace owns: %v", cmd)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("overview acked a request the bound workspace owns: %v", stub.calls)
	}
	if m.preview.doc != nil {
		t.Fatal("overview opened a document for a bound-workspace request")
	}

	event := relayedFileAnnouncement(selected.TmuxName, "twin.txt", 0)
	cmd := m.forwardHostUIRequests(hosts.Update{HostID: "mac-mini", UIRequest: []hostproto.UIRequest{event}})
	if cmd == nil {
		t.Fatal("forwarded bound-workspace request produced no RequestMsg")
	}
	msg := cmd()
	reqMsg, ok := msg.(uirequest.RequestMsg)
	if !ok {
		t.Fatalf("forwarded msg = %T, want RequestMsg", msg)
	}
	if reqMsg.Request.Origin.HostID != "mac-mini" || reqMsg.Request.Origin.TmuxSession != selected.TmuxName {
		t.Fatalf("forwarded request = %+v", reqMsg.Request)
	}
}
