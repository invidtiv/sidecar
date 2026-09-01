package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

func TestNTerminalSplitOnRemoteRowCreatesHostSession(t *testing.T) {
	m, _, root := showingRemoteTwinModel(t, nil)
	refuseLocalTerminalSplit(t)
	remote := installRemoteTerminalSplitStub(t)
	selected, ok := m.SelectedWorkspace()
	if !ok || !selected.Remote() {
		t.Fatal("fixture did not select a remote row")
	}

	if cmd := m.OpenPaneSwitcher(); cmd != nil {
		run(t, m, cmd)
	}
	if m.createForm == nil {
		t.Fatal("pane switcher did not open")
	}
	m.createForm.SetKind(workspacecreate.KindTerminalSplit)
	if reason := m.createForm.KindDisabledReason(); reason != "" {
		t.Fatalf("terminal split disabled on a remote row: %s", reason)
	}
	cmd := m.createPreviewTerminalSplit()
	if cmd == nil {
		t.Fatalf("create command is nil: %s", m.createError)
	}
	run(t, m, cmd)
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell) == nil {
		t.Fatal("terminal split was not placed in the viewer tree")
	}
	remote.assertCalled(t, "mac-mini", termpanes.SessionName(selected.TmuxName), root)
}

func TestRestoringRemoteTerminalSplitUsesHostEnsure(t *testing.T) {
	m, _, root := showingRemoteTwinModel(t, nil)
	refuseLocalTerminalSplit(t)
	remote := installRemoteTerminalSplitStub(t)
	selected, ok := m.SelectedWorkspace()
	if !ok || !selected.Remote() {
		t.Fatal("fixture did not select a remote row")
	}
	session := termpanes.SessionName(selected.TmuxName)
	result := m.results[selected.ProjectKey]
	for i, ws := range result.Workspaces {
		if ws.ID != selected.ID {
			continue
		}
		ws.Live = true
		ws.PaneID = "%1"
		result.Workspaces[i] = ws
	}
	m.results[selected.ProjectKey] = result
	m.syncBoard()
	if !m.workspaces.SelectID(selected.ID) {
		t.Fatal("could not reselect the remote row")
	}
	loadSessionsPaneLayout = func(id string) *state.PaneLayoutJSON {
		if id != selected.ID {
			return nil
		}
		return &state.PaneLayoutJSON{
			Root: selected.Path, Surface: selected.ID, Open: true, HostID: selected.HostID,
			Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: "terminal"},
				B: &state.PaneLayoutJSON{Kind: "shell", Session: session, Name: "ghost"},
			},
		}
	}
	t.Cleanup(func() { loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil } })

	m.resetActivePreviewPanes()
	m.preview.workspaceID = ""
	m.preview.paneCache = nil
	run(t, m, m.bindPreview(false))

	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		t.Fatal("restored tree missing shell")
	}
	leaf := m.preview.terminalPanes.Leaf(shell.ID)
	if leaf == nil || leaf.Target.Host != "mac-mini" {
		t.Fatalf("restored leaf host = %+v, want mac-mini", leaf)
	}
	remote.assertCalled(t, "mac-mini", session, root)
}

func TestCreatePickersUseHostCatalogNotLocalTwin(t *testing.T) {
	m, _, root := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{
		results: []any{contentservice.CatalogResult{
			Kind:      contentservice.KindCatalog,
			Workspace: "api:worktree:remote",
			Files:     []string{"HOST-ONLY.md"},
			Diffs:     []contentservice.CatalogDiff{{Identity: "aabbcc1", Label: "aabbcc1  host commit"}},
			Issues:    []contentservice.CatalogIssue{{ID: "td-host01", Title: "host issue", Status: "open"}},
			Notes:     []contentservice.CatalogNote{{ID: "nt-host01", Title: "host note"}},
		}},
	}
	stub.install(t)

	if cmd := m.OpenPaneSwitcher(); cmd != nil {
		run(t, m, cmd)
	}
	if m.createForm == nil {
		t.Fatal("no create form")
	}
	m.createForm.SetKind(workspacecreate.KindFile)
	m.createForm.AdvanceToTarget()
	cmd := m.loadCreatePickerData()
	if cmd == nil {
		t.Fatal("remote picker catalog was not requested")
	}
	if m.loadCreateFileCandidates() != nil {
		t.Fatal("file picker still scanned this machine for a remote row")
	}
	run(t, m, cmd)
	if len(stub.calls) != 1 {
		t.Fatalf("catalog invocations = %v", stub.calls)
	}
	joined := strings.Join(stub.argv(t, 0), " ")
	if !strings.Contains(joined, "content catalog") || !strings.Contains(joined, "--json") {
		t.Fatalf("argv = %s, want content catalog --json", joined)
	}
	if strings.Contains(joined, root) {
		t.Fatalf("catalog argv carried this machine's twin path: %s", joined)
	}

	m.createForm.SetKind(workspacecreate.KindFile)
	m.createForm.AdvanceToTarget()
	for _, item := range m.createForm.PickerSuggestions() {
		if strings.Contains(item.Value, "twin.txt") || strings.Contains(item.Value, localTwinMarker) {
			t.Fatalf("file picker filled from the local twin: %+v", item)
		}
	}
	m.createForm.PickerInput().SetValue("HOST-ONLY.md")
	m.createForm.SyncAfterInput()
	target, err := m.createForm.TargetForRemote()
	if err != nil {
		t.Fatal(err)
	}
	if target.Kind != uirequest.TargetKindFile || target.Value != "HOST-ONLY.md" {
		t.Fatalf("file target = %+v", target)
	}

	m.createForm.SetKind(workspacecreate.KindDiff)
	m.createForm.AdvanceToTarget()
	foundHost := false
	for _, item := range m.createForm.PickerSuggestions() {
		if item.Value == "aabbcc1" {
			foundHost = true
		}
		if strings.Contains(item.Label, localTwinMarker) || strings.Contains(item.Value, localTwinMarker) {
			t.Fatalf("diff picker filled from the local twin: %+v", item)
		}
	}
	if !foundHost {
		t.Fatalf("diff picker missing host catalog: %+v", m.createForm.PickerSuggestions())
	}
}

func TestCreatePickersStayEmptyWhenHostCatalogFails(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{errs: []error{errors.New("host catalog boom")}}
	stub.install(t)
	if cmd := m.OpenPaneSwitcher(); cmd != nil {
		run(t, m, cmd)
	}
	m.createForm.SetKind(workspacecreate.KindFile)
	m.createForm.AdvanceToTarget()
	run(t, m, m.loadCreatePickerData())
	for _, item := range m.createForm.PickerSuggestions() {
		if strings.Contains(item.Value, "twin.txt") || strings.Contains(item.Value, localTwinMarker) {
			t.Fatalf("failed catalog filled from the local twin: %+v", item)
		}
	}
}

func TestRemoteCreateResourceRowsUseHostDescribeNotViewerSnapshot(t *testing.T) {
	viewerCfg := &config.Config{TerminalResources: config.TerminalResourcesConfig{
		Providers: []config.TerminalResourceProviderConfig{
			{ID: "jira-work", Enabled: true},
			{ID: "viewer-only", Enabled: true},
		},
	}}
	t.Run("before describe", func(t *testing.T) {
		m, _ := showingRemoteResourceModel(t)
		m.config = viewerCfg
		if cmd := m.OpenPaneSwitcher(); cmd != nil {
			run(t, m, cmd)
		}
		view := ansi.Strip(renderCreateModal(t, m))
		if strings.Contains(view, "viewer-only") {
			t.Fatalf("remote switcher offered a viewer-only provider:\n%s", view)
		}
		if strings.Contains(view, "jira-work") {
			t.Fatalf("remote switcher offered host providers before describe:\n%s", view)
		}
	})
	t.Run("after describe", func(t *testing.T) {
		m, _ := showingRemoteResourceModel(t)
		m.config = viewerCfg
		runRemoteDescribe(t, m)
		if cmd := m.OpenPaneSwitcher(); cmd != nil {
			run(t, m, cmd)
		}
		view := ansi.Strip(renderCreateModal(t, m))
		if !strings.Contains(view, "jira-work") {
			t.Fatalf("remote switcher missing host describe provider:\n%s", view)
		}
		if strings.Contains(view, "viewer-only") {
			t.Fatalf("remote switcher offered a viewer-only provider after describe:\n%s", view)
		}
	})
}
