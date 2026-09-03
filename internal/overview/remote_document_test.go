package overview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const remoteMarker = "REMOTE-MARKER"

type fakeRemoteFileSource struct {
	mu          sync.Mutex
	body        string
	revision    string
	notModified bool
	resolveErr  error
	loadErr     error
	loads       int
	resolves    int
	lastIfRev   string
	lastTarget  string
	blockLoad   chan struct{}
}

func (f *fakeRemoteFileSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	f.mu.Lock()
	f.resolves++
	f.lastTarget = pending.Raw
	err := f.resolveErr
	f.mu.Unlock()
	if err != nil {
		return contentlink.Ref{}, err
	}
	return contentlink.Ref{Kind: contentlink.KindFile, Value: pending.Raw}, nil
}

func (f *fakeRemoteFileSource) LoadDocument(_ context.Context, _ contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	if f.blockLoad != nil {
		<-f.blockLoad
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	f.lastIfRev = req.IfRevision
	if req.Ref.Value != "" {
		f.lastTarget = req.Ref.Value
	}
	if f.loadErr != nil {
		return contentpanes.DocumentReadResult{}, f.loadErr
	}
	rev := f.revision
	if rev == "" {
		rev = "v1:1"
	}
	if f.notModified {
		return contentpanes.DocumentReadResult{NotModified: true, Revision: rev}, nil
	}
	body := f.body
	return contentpanes.DocumentReadResult{
		Value:    filepreview.PreviewResult{Content: body, Lines: strings.Split(strings.TrimSuffix(body, "\n"), "\n")},
		Revision: rev,
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
	return contentservice.DescribeResult{Fingerprint: contentservice.FingerprintDescriptors(nil)}, nil
}

func (f *fakeRemoteFileSource) ResolveResource(context.Context, contentpanes.SourceContext, resource.Reference, bool) (resource.Document, error) {
	return resource.Document{}, fmt.Errorf("fake file source does not resolve resources")
}

func (f *fakeRemoteFileSource) stats() (loads int, lastIfRev string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads, f.lastIfRev
}

func bindShowingRemoteHost(m *Model, verbs hostproto.VerbCapabilities) {
	if m.hostHealth == nil {
		m.hostHealth = map[string]hosts.Health{}
	}
	m.hostHealth["mac-mini"] = hosts.Health{
		State: hosts.StateOnline,
		Hello: &hostproto.Hello{
			Proto:        hostproto.Version,
			Capabilities: hostproto.Capabilities{Verbs: verbs},
		},
	}
	if m.hostIncarnations == nil {
		m.hostIncarnations = map[string]uint64{}
	}
	m.hostIncarnations["mac-mini"] = 1
	if m.hostRegistered == nil {
		m.hostRegistered = map[string]bool{}
	}
	m.hostRegistered["mac-mini"] = true
}

func showingRemoteTwinModel(t *testing.T, src contentpanes.Source) (*Model, *fakeRemoteFileSource, string) {
	t.Helper()
	m, root := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{ContentReadV1: true})
	fake, _ := src.(*fakeRemoteFileSource)
	if fake == nil {
		lines := make([]string, 30)
		for i := range lines {
			lines[i] = "line" + strconv.Itoa(i+1)
		}
		lines[19] = remoteMarker
		fake = &fakeRemoteFileSource{
			body:     strings.Join(lines, "\n") + "\n",
			revision: "v1:1",
		}
		src = fake
	}
	m.contentSource = src
	return m, fake, root
}

func openRemoteTwin(t *testing.T, m *Model, line int) {
	t.Helper()
	cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt", Line: line})
	if cmd == nil {
		t.Fatal("openPreviewDocTarget returned nil on a showing remote row")
	}
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 6)
	}
	run(t, m, cmd)
	if m.preview.doc == nil || m.preview.doc.view() == nil {
		t.Fatal("remote file click opened no Document pane")
	}
	m.preview.doc.view().SetSize(80, 6)
	m.focusPreviewPane(panelayout.Document)
}

func TestRemoteSessionsRowOpensHostFileNotLocalTwin(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T, *Model)
	}{
		{"openPreviewContent", func(t *testing.T, m *Model) {
			cmd := m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt", Line: 20}, "Document")
			if view := m.preview.doc.view(); view != nil {
				view.SetSize(80, 6)
			}
			run(t, m, cmd)
		}},
		{"openPreviewDocTarget", func(t *testing.T, m *Model) {
			openRemoteTwin(t, m, 20)
		}},
		{"activatePreviewPlan", func(t *testing.T, m *Model) {
			cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
				Kind: targetactivation.PlanOpenFile, Path: "twin.txt", Line: 20,
			})
			if !handled || cmd == nil {
				t.Fatalf("activatePreviewPlan handled=%v cmd=%v", handled, cmd != nil)
			}
			if view := m.preview.doc.view(); view != nil {
				view.SetSize(80, 6)
			}
			run(t, m, cmd)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, fake, _ := showingRemoteTwinModel(t, nil)
			tc.open(t, m)
			if m.preview.doc == nil {
				t.Fatal("remote file click opened no Document pane")
			}
			view := m.preview.doc.view()
			view.SetSize(80, 6)
			got := ansi.Strip(view.View())
			if !strings.Contains(got, remoteMarker) {
				t.Fatalf("document missing remote bytes: %q", got)
			}
			if strings.Contains(got, localTwinMarker) {
				t.Fatalf("document showed this machine's twin: %q", got)
			}
			if chrome := ansi.Strip(m.previewDocHeaderTabs("twin.txt")); !strings.Contains(chrome, "mac-mini") {
				t.Fatalf("host provenance missing from %q", chrome)
			}
			if fake.lastTarget != "twin.txt" {
				t.Fatalf("resolved %q, want the original token", fake.lastTarget)
			}
			if view.TopSourceLine() != 20 {
				t.Fatalf("top source line = %d, want 20", view.TopSourceLine())
			}
		})
	}
}

func TestRemoteSessionsMissingContentReadV1ToastsAndDoesNotOpen(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{})
	stub := &remoteRunnerStub{}
	stub.install(t)

	cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"})
	toast, ok := toastFrom(t, cmd)
	if !ok {
		t.Fatal("missing ContentReadV1 returned no toast")
	}
	if !strings.Contains(toast.Message, "Update Sidecar on mac-mini") {
		t.Fatalf("toast = %q", toast.Message)
	}
	if m.preview.doc != nil {
		t.Fatalf("opened a document pane: %#v", m.preview.doc)
	}
	if view := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); strings.Contains(view, localTwinMarker) {
		t.Fatalf("missing capability showed local twin bytes in %q", view)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("host without ContentReadV1 was invoked: %v", stub.calls)
	}
}

func TestRemoteDocumentRefreshUpdatesChangedAndKeepsLastBodyOnFailure(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	openRemoteTwin(t, m, 0)
	view := m.preview.doc.view()
	if remoteDocumentRefreshInterval < time.Second || remoteDocumentRefreshInterval > 10*time.Second {
		t.Fatalf("cadence = %s", remoteDocumentRefreshInterval)
	}

	fake.mu.Lock()
	fake.body = "CHANGED-REMOTE\n"
	fake.revision = "v1:2"
	fake.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewDocs()...))
	got := ansi.Strip(view.View())
	if !strings.Contains(got, "CHANGED-REMOTE") {
		t.Fatalf("changed payload did not refresh: %q", got)
	}
	loads, lastIf := fake.stats()
	if lastIf != "v1:1" {
		t.Fatalf("refresh IfRevision = %q, want v1:1", lastIf)
	}

	fake.mu.Lock()
	fake.notModified = true
	fake.mu.Unlock()
	before := loads
	run(t, m, tea.Batch(m.refreshPreviewDocs()...))
	if !strings.Contains(ansi.Strip(view.View()), "CHANGED-REMOTE") {
		t.Fatal("notModified dropped the body")
	}
	if m.preview.doc.hostNotice != "" {
		t.Fatalf("notModified set host notice %q", m.preview.doc.hostNotice)
	}

	fake.mu.Lock()
	fake.notModified = false
	fake.loadErr = context.DeadlineExceeded
	fake.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewDocs()...))
	if !strings.Contains(ansi.Strip(view.View()), "CHANGED-REMOTE") {
		t.Fatal("failed refresh dropped the last body")
	}
	if m.preview.doc.hostNotice != remoteDocumentStaleNotice {
		t.Fatalf("host notice = %q, want %q", m.preview.doc.hostNotice, remoteDocumentStaleNotice)
	}

	fake.mu.Lock()
	fake.loadErr = nil
	loadsAfterFail := fake.loads
	fake.mu.Unlock()
	m.WorkspacesView(40, 10)
	run(t, m, tea.Batch(m.refreshPreviewDocs()...))
	loadsHidden, _ := fake.stats()
	if loadsHidden != loadsAfterFail {
		t.Fatalf("hidden pane issued a remote check: before=%d after=%d (first changed loads=%d)", loadsAfterFail, loadsHidden, before)
	}
}

func TestRemoteDocumentActionsRefuseLocalIOAndKeepInDocumentSearch(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	openRemoteTwin(t, m, 0)
	if !m.docPaneFocused() {
		t.Fatal("document pane is not focused")
	}

	for _, tc := range []struct {
		key    tea.KeyPressMsg
		want   string
		action string
	}{
		{tea.KeyPressMsg{Code: 'e', Text: "e"}, "Inline editing", "e"},
		{tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, "File finding", "ctrl+p"},
		{tea.KeyPressMsg{Code: 'f', Text: "f"}, "Project search", "f"},
	} {
		handled, cmd := m.WorkspacesKey(tc.key)
		if !handled {
			t.Fatalf("%s was not handled", tc.action)
		}
		toast, ok := toastFrom(t, cmd)
		if !ok || !strings.Contains(toast.Message, tc.want) || !strings.Contains(toast.Message, "mac-mini") {
			t.Fatalf("%s toast = %#v", tc.action, toast)
		}
		if m.preview.doc.editing() {
			t.Fatalf("%s started an inline editor", tc.action)
		}
		if m.preview.doc.mode != nil {
			t.Fatalf("%s opened a local finder/search", tc.action)
		}
	}

	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"}) {
		t.Fatal("/ was not handled")
	}
	if !m.preview.doc.view().SearchActive() {
		t.Fatal("in-document search did not start")
	}
}

func TestRemoteDocumentStaleLoadIsDiscardedAfterRowSwitchAndTabClose(t *testing.T) {
	t.Run("row switch", func(t *testing.T) {
		block := make(chan struct{})
		fake := &fakeRemoteFileSource{body: remoteMarker + "\n", revision: "v1:1", blockLoad: block}
		m, _, _ := showingRemoteTwinModel(t, fake)
		cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"})
		if cmd == nil || m.preview.doc == nil {
			t.Fatal("blocked open did not create a document pane")
		}
		remoteID := m.preview.workspaceID
		if !m.workspaces.SelectID("a") {
			t.Fatal("could not select the local row")
		}
		run(t, m, m.previewSync())
		close(block)
		run(t, m, cmd)
		if m.preview.workspaceID == remoteID {
			t.Fatal("row switch did not leave the remote row")
		}
		if view := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); strings.Contains(view, remoteMarker) {
			t.Fatalf("stale remote load landed on the local row: %q", view)
		}
	})
	t.Run("tab close", func(t *testing.T) {
		block := make(chan struct{})
		fake := &fakeRemoteFileSource{body: remoteMarker + "\n", revision: "v1:1", blockLoad: block}
		m, _, _ := showingRemoteTwinModel(t, fake)
		cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"})
		if cmd == nil || m.preview.doc == nil {
			t.Fatal("blocked open did not create a document pane")
		}
		run(t, m, m.closePreviewDocTab())
		close(block)
		run(t, m, cmd)
		if m.preview.doc != nil {
			t.Fatal("a closed tab applied a stale remote load")
		}
		if view := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); strings.Contains(view, remoteMarker) {
			t.Fatalf("closed tab still showed remote bytes: %q", view)
		}
	})
}

func TestRemoteRestoreDropsIssueAndDiffAndDoesNotReadLocalTwin(t *testing.T) {
	m, fake, root := showingRemoteTwinModel(t, nil)
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	layout := &state.PaneLayoutJSON{
		Root: root, Surface: ws.ID, Open: true, HostID: "mac-mini", FocusKind: "doc",
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
				Axis: "rows", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "twin.txt"}}},
				B: &state.PaneLayoutJSON{Kind: "issue", IssueTabs: []state.PaneIssueTabJSON{{Issue: "td-196c42"}}},
			}},
		},
	}
	cmd := m.restoreSpecPreviewLayout(layout)
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 8)
	}
	run(t, m, cmd)
	if m.preview.doc == nil {
		t.Fatal("restore dropped the remote Document tab")
	}
	got := ansi.Strip(m.preview.doc.view().View())
	if !strings.Contains(got, remoteMarker) {
		t.Fatalf("restored document = %q", got)
	}
	if strings.Contains(got, localTwinMarker) {
		t.Fatal("restore loaded this machine's twin")
	}
	if fake.loads == 0 {
		t.Fatal("restore did not load through the remote source")
	}
}

func TestRemoteDeckContextRefusesDisconnectedHost(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	m.contentSource = &fakeRemoteFileSource{body: remoteMarker + "\n"}
	if _, ok := m.previewDeckContext(); ok {
		t.Fatal("previewDeckContext admitted a host that does not Show()")
	}
	cmd := m.openPreviewDocTarget(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"})
	if cmd != nil || m.preview.doc != nil {
		t.Fatal("a disconnected host opened a document")
	}
}

func TestRemoteSessionsPrepareMakesHostOnlyFileClickable(t *testing.T) {
	fake := &fakeRemoteFileSource{body: remoteMarker + "\nhost-only file\n", revision: "v1:1"}
	m, fake, root := showingRemoteTwinModel(t, fake)
	if _, err := os.Stat(filepath.Join(root, "only-on-host.go")); !os.IsNotExist(err) {
		t.Fatalf("only-on-host.go must not exist locally: %v", err)
	}
	if _, _, ok := terminallink.ResolveFile(root, "only-on-host.go"); ok {
		t.Fatal("local ResolveFile found only-on-host.go; the span would not prove remote resolution")
	}
	const line = "see only-on-host.go and twin.txt"
	state := preparedPreviewLineForTest(t, m, line)
	var hostOnly, twin bool
	for _, span := range state.Spans(line, 0) {
		if span.Kind != contentlink.KindFile {
			continue
		}
		switch span.Value {
		case "only-on-host.go":
			hostOnly = true
		case "twin.txt":
			twin = true
		}
	}
	if !hostOnly {
		t.Fatalf("only-on-host.go was not a file span: %+v", state.Spans(line, 0))
	}
	if !twin {
		t.Fatalf("twin.txt was not a file span: %+v", state.Spans(line, 0))
	}

	span, ok := state.SpanAt(line, 0, strings.Index(line, "only-on-host.go"))
	if !ok {
		t.Fatal("no span at only-on-host.go")
	}
	plan, err := targetactivation.PlanForSpan(span)
	if err != nil {
		t.Fatal(err)
	}
	cmd, handled := m.activatePreviewPlan(plan)
	if !handled || cmd == nil {
		t.Fatalf("click handled=%v cmd=%v", handled, cmd != nil)
	}
	if view := m.preview.doc.view(); view != nil {
		view.SetSize(80, 6)
	}
	run(t, m, cmd)
	if m.preview.doc == nil {
		t.Fatal("click opened no Document pane")
	}
	got := ansi.Strip(m.preview.doc.view().View())
	if !strings.Contains(got, remoteMarker) {
		t.Fatalf("document missing host bytes: %q", got)
	}
	if strings.Contains(got, localTwinMarker) {
		t.Fatalf("document showed this machine's twin: %q", got)
	}
	if fake.lastTarget != "only-on-host.go" {
		t.Fatalf("resolved %q, want only-on-host.go", fake.lastTarget)
	}
}

func TestLocalSessionsFileLinkPrepareStartsNoRemoteContentCommand(t *testing.T) {
	stub := &remoteRunnerStub{}
	stub.install(t)
	fake := &fakeRemoteFileSource{body: remoteMarker + "\n"}
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.contentSource = fake
	state := preparedPreviewLineForTest(t, m, "see README.md")
	span, ok := state.SpanAt("see README.md", 0, strings.Index("see README.md", "README.md"))
	if !ok || span.Kind != contentlink.KindFile {
		t.Fatalf("local file span = (%+v, %v)", span, ok)
	}
	if fake.resolves != 0 {
		t.Fatalf("local prepare resolved through the remote source: %d", fake.resolves)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("local prepare invoked sidecar: %v", stub.calls)
	}
}

func TestRemoteSessionsDiffTokenIsNotDecoratedFromLocalGit(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	const line = "review abc1234"
	state := preparedPreviewLineForTest(t, m, line)
	for _, span := range state.Spans(line, 0) {
		if span.Kind == contentlink.KindDiff {
			t.Fatalf("remote diff token was decorated: %+v", span)
		}
	}
	if fake.loads != 0 {
		t.Fatalf("remote diff prepare loaded a document: %d", fake.loads)
	}
}

// The plugin-collection twins of ResolveResource. This fake is about the file,
// issue, note or diff journeys; a collection read is not part of what it is
// pinning, so both refuse rather than pretend.
func (*fakeRemoteFileSource) ListCollection(context.Context, contentpanes.SourceContext, string, contentservice.CollectionParams) (pluginhost.Page, error) {
	return pluginhost.Page{}, errors.New("collection listing is not part of this fixture")
}

func (*fakeRemoteFileSource) GetCollectionItem(context.Context, contentpanes.SourceContext, string, string, string, bool) (resource.Document, error) {
	return resource.Document{}, errors.New("collection items are not part of this fixture")
}
