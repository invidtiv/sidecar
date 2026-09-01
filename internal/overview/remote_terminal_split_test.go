package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/panelayout"
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
