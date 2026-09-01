package overview

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const remoteIssueTitle = "HOST-ONLY-ISSUE"
const remoteNoteTitle = "HOST-ONLY-NOTE"

type fakeRemoteContentSource struct {
	mu          sync.Mutex
	file        fakeRemoteFileSource
	issue       *issueview.Data
	issueOwner  *issueview.Owner
	note        *noteview.Data
	issueRev    string
	noteRev     string
	notModified bool
	loadErr     error
	issueLoads  int
	noteLoads   int
	lastIfRev   string
	lastKind    string
}

func (f *fakeRemoteContentSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch pending.Kind {
	case contentlink.KindFile:
		return contentlink.Ref{Kind: contentlink.KindFile, Value: pending.Raw}, nil
	case contentlink.KindIssue:
		return contentlink.Ref{Kind: contentlink.KindIssue, Value: issueview.NormalizeID(pending.Raw)}, nil
	case contentlink.KindInternal:
		return contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: noteview.NormalizeID(pending.Raw)}, nil
	default:
		return contentlink.Ref{}, nil
	}
}

func (f *fakeRemoteContentSource) LoadDocument(ctx context.Context, src contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	return f.file.LoadDocument(ctx, src, req)
}

func (f *fakeRemoteContentSource) LoadIssue(_ context.Context, _ contentpanes.SourceContext, req contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueLoads++
	f.lastIfRev = req.IfRevision
	f.lastKind = "issue"
	if f.loadErr != nil {
		return contentpanes.IssueReadResult{}, f.loadErr
	}
	rev := f.issueRev
	if rev == "" {
		rev = "v1:issue-1"
	}
	if f.notModified {
		return contentpanes.IssueReadResult{NotModified: true, Revision: rev}, nil
	}
	return contentpanes.IssueReadResult{
		Value:    contentpanes.IssuePayload{Data: f.issue, Owner: f.issueOwner},
		Revision: rev,
	}, nil
}

func (f *fakeRemoteContentSource) LoadDiff(context.Context, contentpanes.SourceContext, contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	return contentpanes.DiffReadResult{}, fmt.Errorf("fake content source does not load diffs")
}

func (f *fakeRemoteContentSource) Describe(context.Context, string) (contentservice.DescribeResult, error) {
	return contentservice.DescribeResult{Fingerprint: contentservice.FingerprintDescriptors(nil)}, nil
}

func (f *fakeRemoteContentSource) ResolveResource(context.Context, contentpanes.SourceContext, resource.Reference, bool) (resource.Document, error) {
	return resource.Document{}, fmt.Errorf("fake content source does not resolve resources")
}

func (f *fakeRemoteContentSource) LoadNote(_ context.Context, _ contentpanes.SourceContext, req contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noteLoads++
	f.lastIfRev = req.IfRevision
	f.lastKind = "note"
	if f.loadErr != nil {
		return contentpanes.NoteReadResult{}, f.loadErr
	}
	rev := f.noteRev
	if rev == "" {
		rev = "v1:note-1"
	}
	if f.notModified {
		return contentpanes.NoteReadResult{NotModified: true, Revision: rev}, nil
	}
	return contentpanes.NoteReadResult{Value: f.note, Revision: rev}, nil
}

func showingRemoteIssueNoteModel(t *testing.T) (*Model, *fakeRemoteContentSource) {
	t.Helper()
	src := &fakeRemoteContentSource{
		issue:      &issueview.Data{ID: "td-a4dd72", Title: remoteIssueTitle, Status: "open", Description: "host body"},
		issueOwner: &issueview.Owner{Name: "host-proj", Root: "/remote/host-proj"},
		issueRev:   "v1:issue-1",
		note:       &noteview.Data{ID: "nt-host01", Title: remoteNoteTitle, Content: "host note body"},
		noteRev:    "v1:note-1",
		file:       fakeRemoteFileSource{body: remoteMarker + "\ntd-a4dd72\n", revision: "v1:1"},
	}
	m, _, _ := showingRemoteTwinModel(t, nil)
	m.contentSource = src
	return m, src
}

func TestRemoteSessionsRowOpensHostIssueNotLocalTwin(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T, *Model)
	}{
		{"openPreviewIssue", func(t *testing.T, m *Model) {
			cmd := m.openPreviewIssue("td-a4dd72")
			if cmd == nil {
				t.Fatal("openPreviewIssue returned nil on a showing remote row")
			}
			run(t, m, cmd)
		}},
		{"activatePreviewPlan", func(t *testing.T, m *Model) {
			cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
				Kind: targetactivation.PlanOpenIssue, Issue: "td-a4dd72",
			})
			if !handled || cmd == nil {
				t.Fatalf("activatePreviewPlan handled=%v cmd=%v", handled, cmd != nil)
			}
			run(t, m, cmd)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, src := showingRemoteIssueNoteModel(t)
			tc.open(t, m)
			if m.preview.issue == nil || m.preview.issue.view() == nil {
				t.Fatal("remote issue click opened no Issue pane")
			}
			view := m.preview.issue.view()
			view.SetSize(80, 16)
			got := ansi.Strip(view.View())
			if !strings.Contains(got, remoteIssueTitle) {
				t.Fatalf("issue missing host title: %q", got)
			}
			if !strings.Contains(got, "host-proj") {
				t.Fatalf("issue missing host owner badge: %q", got)
			}
			if src.issueLoads == 0 {
				t.Fatal("issue did not load through the remote source")
			}
		})
	}
}

func TestRemoteSessionsRowOpensHostNoteNotLocalTwin(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	cmd := m.openPreviewNote("nt-host01")
	if cmd == nil {
		t.Fatal("openPreviewNote returned nil on a showing remote row")
	}
	run(t, m, cmd)
	if m.preview.note == nil || m.preview.note.view() == nil {
		t.Fatal("remote note click opened no Note pane")
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
}

func TestLocalSessionsIssueNoteLoadStartsNoRemoteContentCommand(t *testing.T) {
	stub := &remoteRunnerStub{}
	stub.install(t)
	stubPreviewTd(t)
	stubTdNote(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	fake := &fakeRemoteContentSource{
		issue: &issueview.Data{ID: "td-1111aa", Title: remoteIssueTitle},
		note:  &noteview.Data{ID: "nt-short", Title: remoteNoteTitle, Content: "x"},
	}
	m.contentSource = fake

	run(t, m, m.openPreviewIssue("td-1111aa"))
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("local issue pane did not open")
	}
	got := ansi.Strip(m.preview.issue.view().View())
	if strings.Contains(got, remoteIssueTitle) {
		t.Fatalf("local issue used the remote fake: %q", got)
	}
	if fake.issueLoads != 0 {
		t.Fatalf("local issue load used the remote source: %d", fake.issueLoads)
	}

	run(t, m, m.openPreviewNote("nt-short"))
	if m.preview.note == nil || m.preview.note.view() == nil {
		t.Fatal("local note pane did not open")
	}
	if fake.noteLoads != 0 {
		t.Fatalf("local note load used the remote source: %d", fake.noteLoads)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("local issue/note load invoked remote sidecar: %v", stub.calls)
	}
}

func TestRemoteSessionsMissingContentReadV1ToastsIssueAndDoesNotOpen(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{})
	stub := &remoteRunnerStub{}
	stub.install(t)

	cmd := m.openPreviewIssue("td-a4dd72")
	toast, ok := toastFrom(t, cmd)
	if !ok {
		t.Fatal("missing ContentReadV1 returned no toast")
	}
	if !strings.Contains(toast.Message, "Update Sidecar on mac-mini") {
		t.Fatalf("toast = %q", toast.Message)
	}
	if m.preview.issue != nil {
		t.Fatalf("opened an issue pane: %#v", m.preview.issue)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("host without ContentReadV1 was invoked: %v", stub.calls)
	}
}

func TestRemoteOpenInTDIsRefusedAndLocalStillWorks(t *testing.T) {
	m, _ := showingRemoteIssueNoteModel(t)
	run(t, m, m.openPreviewIssue("td-a4dd72"))
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("no remote issue pane")
	}
	m.preview.issue.focused = true
	m.preview.issue.view().SetFocused(true)
	m.preview.issue.view().SetSize(80, 16)
	handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
	if !handled || cmd == nil {
		t.Fatalf("O on remote issue: handled=%v cmd=%v", handled, cmd != nil)
	}
	toast, ok := toastFrom(t, cmd)
	if !ok || !strings.Contains(toast.Message, "Open in td") || !strings.Contains(toast.Message, "mac-mini") {
		t.Fatalf("remote O toast = %#v", toast)
	}
	if _, isJump := cmd().(OpenIssueInTDMsg); isJump {
		t.Fatal("remote O sent OpenIssueInTDMsg")
	}
}

func TestRemoteNestedIssueFromDocumentAndCardStaysOnHost(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	src.file.body = "see td-a4dd72\n"
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document"))
	if m.preview.doc == nil {
		t.Fatal("document pane did not open")
	}
	cmd := m.activatePreviewDocLink(contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-a4dd72"})
	if cmd == nil {
		t.Fatal("nested issue from a remote document was refused")
	}
	run(t, m, cmd)
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("nested issue from document opened no pane")
	}
	if src.issueLoads == 0 {
		t.Fatal("nested document issue did not load through the remote source")
	}

	child := &issueview.Data{ID: "td-1111aa", Title: "HOST-CHILD", Status: "open"}
	src.mu.Lock()
	src.issue = child
	src.issueOwner = &issueview.Owner{Name: "host-proj", Root: "/remote/host-proj"}
	src.issueRev = "v1:child"
	src.mu.Unlock()
	run(t, m, m.openPreviewIssue("td-1111aa"))
	view := m.preview.issue.view()
	if view == nil || view.IssueID() != "td-1111aa" {
		t.Fatalf("nested card issue = %#v", view)
	}
	view.SetSize(80, 16)
	got := ansi.Strip(view.View())
	if !strings.Contains(got, "HOST-CHILD") {
		t.Fatalf("nested card missing host child: %q", got)
	}
}

func TestRemoteIssueRefreshChangedNotModifiedHiddenAndFailure(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	run(t, m, m.openPreviewIssue("td-a4dd72"))
	view := m.preview.issue.view()
	view.SetSize(80, 16)
	if src.lastIfRev != "" {
		t.Fatalf("first load IfRevision = %q, want empty", src.lastIfRev)
	}

	src.mu.Lock()
	src.issue = &issueview.Data{ID: "td-a4dd72", Title: "CHANGED-HOST-ISSUE", Status: "open"}
	src.issueRev = "v1:issue-2"
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewIssues()...))
	got := ansi.Strip(view.View())
	if !strings.Contains(got, "CHANGED-HOST-ISSUE") {
		t.Fatalf("changed payload did not refresh: %q", got)
	}
	if src.lastIfRev != "v1:issue-1" {
		t.Fatalf("refresh IfRevision = %q, want v1:issue-1", src.lastIfRev)
	}

	src.mu.Lock()
	src.notModified = true
	loads := src.issueLoads
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewIssues()...))
	if !strings.Contains(ansi.Strip(view.View()), "CHANGED-HOST-ISSUE") {
		t.Fatal("notModified dropped the body")
	}
	if m.preview.issue.hostNotice != "" {
		t.Fatalf("notModified set host notice %q", m.preview.issue.hostNotice)
	}

	src.mu.Lock()
	src.notModified = false
	src.loadErr = context.DeadlineExceeded
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewIssues()...))
	if !strings.Contains(ansi.Strip(view.View()), "CHANGED-HOST-ISSUE") {
		t.Fatal("failed refresh dropped the last body")
	}
	if m.preview.issue.hostNotice != remoteDocumentStaleNotice {
		t.Fatalf("host notice = %q, want %q", m.preview.issue.hostNotice, remoteDocumentStaleNotice)
	}

	src.mu.Lock()
	src.loadErr = nil
	loadsAfterFail := src.issueLoads
	src.mu.Unlock()
	m.WorkspacesView(40, 10)
	run(t, m, tea.Batch(m.refreshPreviewIssues()...))
	src.mu.Lock()
	hiddenLoads := src.issueLoads
	src.mu.Unlock()
	if hiddenLoads != loadsAfterFail {
		t.Fatalf("hidden pane issued a remote check: before=%d after=%d (first changed loads=%d)", loadsAfterFail, hiddenLoads, loads)
	}
}

func TestRemoteNoteRefreshSendsIfRevision(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	run(t, m, m.openPreviewNote("nt-host01"))
	view := m.preview.note.view()
	view.SetSize(80, 12)
	src.mu.Lock()
	src.note = &noteview.Data{ID: "nt-host01", Title: "CHANGED-NOTE", Content: "new"}
	src.noteRev = "v1:note-2"
	src.mu.Unlock()
	run(t, m, tea.Batch(m.refreshPreviewNotes()...))
	if src.lastIfRev != "v1:note-1" {
		t.Fatalf("note refresh IfRevision = %q, want v1:note-1", src.lastIfRev)
	}
	if view.Data() == nil || view.Data().Title != "CHANGED-NOTE" {
		t.Fatalf("changed note did not refresh: %#v", view.Data())
	}
	got := ansi.Strip(view.View())
	if !strings.Contains(got, "CHANGED-NOTE") {
		t.Fatalf("changed note view = %q", got)
	}
}

func TestRemoteRestoreAdmitsIssueAndNoteWithoutLocalTd(t *testing.T) {
	m, src := showingRemoteIssueNoteModel(t)
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	layout := &state.PaneLayoutJSON{
		Root: ws.Path, Surface: ws.ID, Open: true, HostID: "mac-mini", FocusKind: "issue",
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
				Axis: "rows", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: "issue", IssueTabs: []state.PaneIssueTabJSON{{Issue: "td-a4dd72"}}},
				B: &state.PaneLayoutJSON{Kind: "note", NoteTabs: []state.PaneNoteTabJSON{{Note: "nt-host01"}}},
			}},
		},
	}
	run(t, m, m.restoreSpecPreviewLayout(layout))
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		t.Fatal("restore dropped the remote Issue tab")
	}
	if m.preview.note == nil || m.preview.note.view() == nil {
		t.Fatal("restore dropped the remote Note tab")
	}
	m.preview.issue.view().SetSize(80, 16)
	m.preview.note.view().SetSize(80, 12)
	if m.preview.issue.view().Data() == nil || m.preview.issue.view().Data().Title != remoteIssueTitle {
		t.Fatalf("restored issue data = %#v", m.preview.issue.view().Data())
	}
	if m.preview.note.view().Data() == nil || m.preview.note.view().Data().Title != remoteNoteTitle {
		t.Fatalf("restored note data = %#v", m.preview.note.view().Data())
	}
	if !strings.Contains(ansi.Strip(m.preview.issue.view().View()), remoteIssueTitle) {
		t.Fatalf("restored issue = %q", ansi.Strip(m.preview.issue.view().View()))
	}
	if !strings.Contains(ansi.Strip(m.preview.note.view().View()), remoteNoteTitle) {
		t.Fatalf("restored note = %q", ansi.Strip(m.preview.note.view().View()))
	}
	if src.issueLoads == 0 || src.noteLoads == 0 {
		t.Fatalf("restore did not load through the remote source: issue=%d note=%d", src.issueLoads, src.noteLoads)
	}
}

func TestRemoteRestoreStillDropsResource(t *testing.T) {
	m, _, root := showingRemoteTwinModel(t, nil)
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	layout := &state.PaneLayoutJSON{
		Root: root, Surface: ws.ID, Open: true, HostID: "mac-mini", FocusKind: "doc",
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Kind: "resource"},
		},
	}
	run(t, m, m.restoreSpecPreviewLayout(layout))
	if m.preview.resource != nil {
		t.Fatalf("restore opened a remote resource: %#v", m.preview.resource)
	}
}

func TestRemoteIssueDoesNotLivewatchLocalPath(t *testing.T) {
	m, _ := showingRemoteIssueNoteModel(t)
	run(t, m, m.openPreviewIssue("td-a4dd72"))
	if targets := m.previewIssueTargets(); len(targets) != 0 {
		t.Fatalf("remote issue livewatch targets = %v", targets)
	}
	if cmd := m.resolvePreviewTDStore(); cmd != nil {
		t.Fatal("remote issue queued a local td store resolve")
	}
}
