package overview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const localTwinMarker = "LOCAL-TWIN"

// Slice 0/3 of docs/plans/active/remote-host-content-pane-parity.md: a remote
// row whose Path exists on this machine must not show this machine's bytes.
// A host that does not Shows() still refuses; a showing host with ContentReadV1
// is the file steel thread in remote_document_test.go.

func remoteTwinSessionsModel(t *testing.T) (*Model, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "twin.txt"), []byte(localTwinMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := previewModel(t)
	remote := remoteSessionsRow(t, m)
	result := m.results["sidecar"]
	for i, ws := range result.Workspaces {
		if ws.ID != remote.ID {
			continue
		}
		ws.Path = root
		result.Workspaces[i] = ws
		remote = ws
	}
	m.results["sidecar"] = result
	m.syncBoard()
	if !m.workspaces.SelectID(remote.ID) {
		t.Fatalf("could not select remote row %q", remote.ID)
	}
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	ws, ok := m.SelectedWorkspace()
	if !ok || !ws.Remote() {
		t.Fatal("fixture did not select a remote workspace")
	}
	if ws.Path != root {
		t.Fatalf("remote Path = %q, want the local twin root %q", ws.Path, root)
	}
	if _, _, ok := terminallink.ResolveFile(root, "twin.txt"); !ok {
		t.Fatal("the local twin is not resolvable; a refusal here would not prove the guard")
	}
	return m, root
}

func assertRemoteContentClosed(t *testing.T, m *Model) {
	t.Helper()
	if _, ok := m.previewDeckContext(); ok {
		t.Fatal("previewDeckContext admitted a remote workspace")
	}
	if m.preview.deck != nil {
		t.Fatal("remote row created a content deck")
	}
	if m.preview.doc != nil {
		t.Fatalf("remote row opened a Document pane: %#v", m.preview.doc)
	}
	if m.preview.issue != nil {
		t.Fatalf("remote row opened an Issue pane: %#v", m.preview.issue)
	}
	if m.preview.note != nil {
		t.Fatalf("remote row opened a Note pane: %#v", m.preview.note)
	}
	if m.preview.diff != nil {
		t.Fatalf("remote row opened a Diff pane: %#v", m.preview.diff)
	}
	if m.preview.resource != nil {
		t.Fatalf("remote row opened a Resource pane: %#v", m.preview.resource)
	}
	if view := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); strings.Contains(view, localTwinMarker) {
		t.Fatalf("remote row showed this machine's twin bytes in %q", view)
	}
}

func TestRemoteSessionsRowDoesNotOpenALocalTwinFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T, *Model)
	}{
		{"openPreviewContent", func(t *testing.T, m *Model) {
			cmd := m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document")
			run(t, m, cmd)
			if cmd != nil {
				t.Fatal("openPreviewContent started a load on a remote row")
			}
		}},
		{"openPreviewDocTarget", func(t *testing.T, m *Model) {
			cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"})
			run(t, m, cmd)
			if cmd != nil {
				t.Fatal("openPreviewDocTarget started a load on a remote row")
			}
		}},
		{"activatePreviewPlan", func(t *testing.T, m *Model) {
			cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
				Kind: targetactivation.PlanOpenFile, Path: "twin.txt",
			})
			run(t, m, cmd)
			if handled || cmd != nil {
				t.Fatalf("activatePreviewPlan handled=%v cmd=%v on a remote row", handled, cmd != nil)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := remoteTwinSessionsModel(t)
			tc.open(t, m)
			assertRemoteContentClosed(t, m)
		})
	}
}

func TestRemoteSessionsDispatchDeclaresEveryPlanKindWithoutLocalContent(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)

	for _, kind := range targetactivation.PlanKindsFromSpans() {
		if !previewHandlesPlanKind(kind) {
			t.Fatalf("remote Sessions lost the dispatch declaration for %s", kind)
		}
	}

	for _, plan := range []targetactivation.Plan{
		{Kind: targetactivation.PlanOpenFile, Path: "twin.txt"},
		{Kind: targetactivation.PlanOpenIssue, Issue: "td-196c42"},
		{Kind: targetactivation.PlanOpenNote, Note: "nt-abc123"},
		{Kind: targetactivation.PlanOpenDiff, Spec: "abc1234"},
		{Kind: targetactivation.PlanOpenResource, Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245"},
	} {
		cmd, handled := m.activatePreviewPlan(plan)
		run(t, m, cmd)
		if handled || cmd != nil {
			t.Fatalf("%s: handled=%v cmd=%v on a remote row", plan.Kind, handled, cmd != nil)
		}
		assertRemoteContentClosed(t, m)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("remote resource activation asked the local resolver: %v", refs)
	}
}

func TestPreviewDeckConfigUsesRemoteSourceForRemoteContext(t *testing.T) {
	m, stub := remoteCreateModel(t)
	cfg := m.previewDeckConfig(contentpanes.SurfaceContext{
		Source: contentpanes.SourceContext{HostID: "mac-mini", WorkspaceID: "p:shell:s1"},
	})
	src, ok := cfg.Source.(contentpanes.RemoteSource)
	if !ok {
		t.Fatalf("document source = %T, want RemoteSource", cfg.Source)
	}
	if src.HostID != "mac-mini" {
		t.Fatalf("remote source host = %q", src.HostID)
	}
	cfg = m.previewDeckConfig(contentpanes.SurfaceContext{})
	if _, ok := cfg.Source.(contentpanes.LocalSource); !ok {
		t.Fatalf("local context source = %T, want LocalSource", cfg.Source)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("constructing a content source invoked sidecar: %v", stub.calls)
	}
}

func TestPreviewDeckContextAdmitsShowingRemoteHost(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{ContentReadV1: true})
	stub := &remoteRunnerStub{}
	stub.install(t)
	ctx, ok := m.previewDeckContext()
	if !ok || !ctx.Source.Remote() || ctx.Source.HostID != "mac-mini" {
		t.Fatalf("admitted=%v ctx=%#v", ok, ctx)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("admitting a remote context invoked sidecar: %v", stub.calls)
	}
}

func TestLocalSessionsDocumentLoadStartsNoRemoteContentCommand(t *testing.T) {
	stub := &remoteRunnerStub{}
	stub.install(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	if _, ok := m.previewDeckContext(); !ok {
		t.Fatal("a local Sessions row must still have a content context")
	}

	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	view := m.preview.doc.view()
	if view == nil {
		t.Fatal("local document pane is not a docview")
	}
	if view.Title() != "README.md" {
		t.Fatalf("local document title = %q", view.Title())
	}
	view.SetSize(80, 20)
	if got := ansi.Strip(view.View()); !strings.Contains(got, "Hello from preview") {
		t.Fatalf("local document did not load through docview: %q", got)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("local document load invoked remote sidecar: %v", stub.calls)
	}
}

func TestLocalSessionsResourceMatchersAreTheViewerSnapshot(t *testing.T) {
	m := resourcePreviewModel(t)
	if len(m.resourceMatchers) != 0 {
		t.Fatalf("fixture started with matchers: %#v", m.resourceMatchers)
	}
	m.SetResourceMatchers(jiraMatchers())
	if len(m.resourceMatchers) != 1 || m.resourceMatchers[0].Provider != "jira-work" {
		t.Fatalf("SetResourceMatchers did not publish the local snapshot: %#v", m.resourceMatchers)
	}

	spans := preparedPreviewLineForTest(t, m, resourceLine).Spans(resourceLine, 0)
	found := false
	for _, span := range spans {
		if span.Kind == terminallink.KindResource && span.Value == "CASH-1245" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("local row did not match through resourceMatchers: %#v", spans)
	}

	remote := remoteSessionsRow(t, m)
	if !m.workspaces.SelectID(remote.ID) {
		t.Fatalf("could not select remote row %q", remote.ID)
	}
	run(t, m, m.previewSync())
	m.WorkspacesView(previewWide, previewTall)
	resolver := &fakeResolver{}
	m.SetResourceResolver(resolver.resolve)
	cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
		Kind:     targetactivation.PlanOpenResource,
		Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
	})
	run(t, m, cmd)
	if handled || cmd != nil || m.preview.resource != nil {
		t.Fatalf("remote row consulted a matcher: handled=%v cmd=%v resource=%#v", handled, cmd != nil, m.preview.resource)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("remote row asked the local resolver: %v", refs)
	}
	if len(m.resourceMatchers) != 1 || m.resourceMatchers[0].Provider != "jira-work" {
		t.Fatalf("selecting a remote row replaced the local matcher snapshot: %#v", m.resourceMatchers)
	}
}
