package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	workspaceRemoteMarker    = "REMOTE-MARKER"
	workspaceLocalTwinMarker = "LOCAL-TWIN"
)

type fakeRemoteFileSource struct {
	body       string
	lastTarget string
	loads      int
}

func (f *fakeRemoteFileSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	f.lastTarget = pending.Raw
	return contentlink.Ref{Kind: contentlink.KindFile, Value: pending.Raw}, nil
}

func (f *fakeRemoteFileSource) LoadDocument(_ context.Context, _ contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	f.loads++
	if req.Ref.Value != "" {
		f.lastTarget = req.Ref.Value
	}
	body := f.body
	return contentpanes.DocumentReadResult{
		Value:    filepreview.PreviewResult{Content: body, Lines: strings.Split(strings.TrimSuffix(body, "\n"), "\n")},
		Revision: "v1:1",
	}, nil
}

func (f *fakeRemoteFileSource) LoadIssue(context.Context, contentpanes.SourceContext, contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	return contentpanes.IssueReadResult{}, fmt.Errorf("fake file source does not load issues")
}
func (f *fakeRemoteFileSource) LoadNote(context.Context, contentpanes.SourceContext, contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	return contentpanes.NoteReadResult{}, fmt.Errorf("fake file source does not load notes")
}
func (f *fakeRemoteFileSource) LoadDiff(context.Context, contentpanes.SourceContext, contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	return contentpanes.DiffReadResult{}, fmt.Errorf("fake file source does not load diffs")
}
func (f *fakeRemoteFileSource) Describe(context.Context, string) (contentservice.DescribeResult, error) {
	return contentservice.DescribeResult{}, nil
}
func (f *fakeRemoteFileSource) ResolveResource(context.Context, contentpanes.SourceContext, resource.Reference, bool) (resource.Document, error) {
	return resource.Document{}, fmt.Errorf("fake file source does not resolve resources")
}

type recordingRemoteRunner struct {
	calls [][]string
}

func (r *recordingRemoteRunner) run(_ context.Context, _ string, args []string, _ any) error {
	copied := append([]string(nil), args...)
	r.calls = append(r.calls, copied)
	return nil
}

func boundWorkspacePlugin(t *testing.T) (*Plugin, *fakeRemoteFileSource, *recordingRemoteRunner) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	t.Cleanup(config.ResetTestStateDir)

	root := t.TempDir()
	writeDocPaneFixture(t, root, "twin.txt", workspaceLocalTwinMarker+"\n")
	p := interactiveUIRequestTestPlugin(t, root)
	p.ctx.HostID = "mac-mini"
	p.ctx.HostIncarnation = 1
	p.ctx.ProjectKey = "/home/me/sidecar"
	p.ctx.WorkDir = ""
	p.ctx.ProjectRoot = ""
	p.shells[0].WorkDir = "/home/me/sidecar"
	p.shells[0].InventoryID = "/home/me/sidecar:shell:test-shell"
	fake := &fakeRemoteFileSource{body: "line1\n" + workspaceRemoteMarker + "\n"}
	p.contentSource = fake
	runner := &recordingRemoteRunner{}
	p.ctx.RemoteRunner = runner.run
	p.View(p.width, p.height)
	return p, fake, runner
}

func relayedFileReq(id, session, path string) uirequest.Request {
	return uirequest.Request{
		ID: id, Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{HostID: "mac-mini", TmuxSession: session, ProjectKey: "/home/me/sidecar"},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: path},
	}
}

func TestRelayedOpenOpensHostFileNotLocalTwin(t *testing.T) {
	p, fake, runner := boundWorkspacePlugin(t)
	cmd := p.handleUIRequest(relayedFileReq("req-relay-file", "test-shell", "twin.txt"))
	if cmd == nil {
		t.Fatal("relayed file open produced no command")
	}
	applyDocOpen(t, p, cmd)
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil {
		t.Fatal("relayed open opened no Document pane")
	}
	doc.view().SetSize(80, 6)
	got := ansi.Strip(doc.view().View())
	if !strings.Contains(got, workspaceRemoteMarker) {
		t.Fatalf("document missing remote bytes: %q", got)
	}
	if strings.Contains(got, workspaceLocalTwinMarker) {
		t.Fatalf("document showed this machine's twin: %q", got)
	}
	if fake.lastTarget != "twin.txt" {
		t.Fatalf("resolved %q, want twin.txt", fake.lastTarget)
	}
	if fake.loads == 0 {
		t.Fatal("remote source was not loaded")
	}
	src := p.workspaceSourceContext("/home/me/sidecar", "shell:test-shell")
	if src.HostID != "mac-mini" || src.HostIncarnation != 1 || src.ProjectKey != "/home/me/sidecar" {
		t.Fatalf("source context = %+v", src)
	}
	if src.WorkspaceID != "/home/me/sidecar:shell:test-shell" {
		t.Fatalf("WorkspaceID = %q", src.WorkspaceID)
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0], " "), "--status opened") {
		t.Fatalf("ack calls = %v", runner.calls)
	}
}

func TestRelayedOpenUnselectedShellDeclinesWithoutQueue(t *testing.T) {
	p, _, runner := boundWorkspacePlugin(t)
	p.shells = append(p.shells, &ShellSession{Name: "other", TmuxName: "sidecar-other", WorkDir: "/home/me/sidecar"})
	req := relayedFileReq("req-relay-unselected", "sidecar-other", "twin.txt")
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatalf("unselected relayed open returned cmd %v", cmd)
	}
	if p.pendingViews["sidecar-other"] != nil {
		t.Fatal("relayed open queued")
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0], " "), "--status declined") {
		t.Fatalf("ack calls = %v", runner.calls)
	}
	joined := strings.Join(runner.calls[0], " ")
	if !strings.Contains(joined, relayedOpenNotOnScreenReason) {
		t.Fatalf("decline reason missing: %s", joined)
	}
}

func TestRelayedLayoutGetOnBoundWorkspace(t *testing.T) {
	p, _, runner := boundWorkspacePlugin(t)
	req := uirequest.Request{
		ID: "req-relay-layout-get", Action: uirequest.ActionLayout, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{HostID: "mac-mini", TmuxSession: "test-shell", ProjectKey: "/home/me/sidecar"},
		Payload: json.RawMessage(`{"mode":"get"}`),
	}
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatalf("relayed get emitted a command: %v", cmd)
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0], " "), "--status opened") {
		t.Fatalf("ack calls = %v", runner.calls)
	}
}

func TestRelayedLayoutOffScreenDeclinesWithoutQueue(t *testing.T) {
	p, _, runner := boundWorkspacePlugin(t)
	p.shells = append(p.shells, &ShellSession{Name: "other", TmuxName: "sidecar-other"})
	req := uirequest.Request{
		ID: "req-relay-layout-off", Action: uirequest.ActionLayout, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{HostID: "mac-mini", TmuxSession: "sidecar-other", ProjectKey: "/home/me/sidecar"},
		Payload: json.RawMessage(`{"mode":"get"}`),
	}
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen relayed layout returned cmd %v", cmd)
	}
	if p.pendingViews["sidecar-other"] != nil {
		t.Fatal("relayed layout queued")
	}
	joined := strings.Join(runner.calls[0], " ")
	if !strings.Contains(joined, "--status declined") || !strings.Contains(joined, layoutapply.NotOnScreenReason) {
		t.Fatalf("decline ack = %s", joined)
	}
}

func TestWorkspaceSourceContextCarriesBoundHost(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{
		HostID: "aerie", HostIncarnation: 7, ProjectKey: "/home/me/sidecar",
	}}
	p.shellSelected = true
	p.selectedShellIdx = 0
	p.shells = []*ShellSession{{TmuxName: "s1", InventoryID: "/home/me/sidecar:shell:s1", WorkDir: "/home/me/sidecar"}}
	src := p.workspaceSourceContext("/home/me/sidecar", "shell:s1")
	if src.HostID != "aerie" || src.HostIncarnation != 7 || src.ProjectKey != "/home/me/sidecar" {
		t.Fatalf("source = %+v", src)
	}
	if src.WorkspaceID != "/home/me/sidecar:shell:s1" {
		t.Fatalf("WorkspaceID = %q", src.WorkspaceID)
	}
	if src.WorkspaceKind != workspaceinventory.KindShell || src.WorkspaceKey != "s1" {
		t.Fatalf("kind/key = %s %s", src.WorkspaceKind, src.WorkspaceKey)
	}
}

func TestWorkspaceDeckConfigUsesRemoteSourceWhenBound(t *testing.T) {
	fake := &fakeRemoteFileSource{body: workspaceRemoteMarker}
	p := &Plugin{contentSource: fake, ctx: &plugin.Context{HostID: "aerie"}}
	if p.documentSource() != fake {
		t.Fatal("bound workspace did not use the injected source")
	}
	p.contentSource = nil
	p.ctx.RemoteRunner = func(context.Context, string, []string, any) error { return nil }
	p.ctx.HostVerbs = func() hostproto.VerbCapabilities {
		return hostproto.VerbCapabilities{ContentReadV1: true}
	}
	if _, ok := p.documentSource().(contentpanes.RemoteSource); !ok {
		t.Fatalf("bound workspace source = %T, want RemoteSource", p.documentSource())
	}
	p.ctx.HostID = ""
	if _, ok := p.documentSource().(contentpanes.LocalSource); !ok {
		t.Fatal("local workspace did not use LocalSource")
	}
}

func TestHostIncarnationBumpChangesSourceContext(t *testing.T) {
	p := boundWorkspacePluginReady(t)
	first := p.workspaceSourceContext("/home/me/sidecar", "shell:test-shell")
	p.ctx.HostIncarnation = 99
	second := p.workspaceSourceContext("/home/me/sidecar", "shell:test-shell")
	if first.HostIncarnation == second.HostIncarnation {
		t.Fatal("incarnation bump did not change SourceContext")
	}
	if second.HostIncarnation != 99 {
		t.Fatalf("HostIncarnation = %d", second.HostIncarnation)
	}
}

func boundWorkspacePluginReady(t *testing.T) *Plugin {
	t.Helper()
	p, _, _ := boundWorkspacePlugin(t)
	return p
}

func TestNestedDocLinksFollowDeckSource(t *testing.T) {
	p, fake, _ := boundWorkspacePlugin(t)
	cmd := p.handleUIRequest(relayedFileReq("req-nested", "test-shell", "twin.txt"))
	applyDocOpen(t, p, cmd)
	if p.contentDeck == nil {
		t.Fatal("no content deck")
	}
	if p.contentDeck.ContentSource() != fake {
		t.Fatal("deck source is not the remote adapter")
	}
	src := p.contentDeck.Context().Source
	if src.HostID != "mac-mini" {
		t.Fatalf("deck source HostID = %q", src.HostID)
	}
}

func TestRelayedOpenIgnoresUnboundWorkspace(t *testing.T) {
	p, _, runner := boundWorkspacePlugin(t)
	p.ctx.HostID = ""
	req := relayedFileReq("req-unbound", "test-shell", "twin.txt")
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatalf("unbound plugin applied a relayed open: %v", cmd)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unbound plugin acked: %v", runner.calls)
	}
}
