package overview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverview_UIRequestProviderResourceUsesLiveMatcher(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	m := resourcePreviewModel(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	m.SetResourceMatchers([]terminallink.ResourceMatcher{{
		Provider: "jira-work", ID: "issue-key", Re: regexp.MustCompile(`CASH-[0-9]+`),
	}})
	m.SetResourceResolver((&fakeResolver{}).resolve)
	req := uirequest.Request{
		ID: "overview-provider-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: selected.TmuxName},
		Target: uirequest.Target{Kind: uirequest.TargetKindResource, Provider: "jira-work", Value: "CASH-1245"},
	}
	if cmd := m.handleUIRequest(req); cmd == nil {
		t.Fatal("resource request did not open")
	}
	if got := resourceTabLocators(m.preview.resource); len(got) != 1 || got[0] != "CASH-1245" {
		t.Fatalf("resource tabs = %v", got)
	}
}

func TestOverview_UIRequestPendingView(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{
			"ws-1": {
				ID:       "ws-1",
				TmuxName: "sidecar-sh-sidecar-1",
			},
		},
	}

	req := uirequest.Request{
		ID:        "req-ov-1",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin: uirequest.Origin{
			TmuxSession: "sidecar-sh-sidecar-1",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "README.md",
		},
	}

	// When not selected, queues and acks StatusQueued
	cmd := m.handleUIRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd for unselected workspace, got %v", cmd)
	}

	badge, hasBadge := m.pendingViewBadge("sidecar-sh-sidecar-1")
	if !hasBadge || badge == "" {
		t.Errorf("expected pending view badge on ws-1, got %q, %v", badge, hasBadge)
	}

	acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 || acks[0].Status != uirequest.StatusQueued {
		t.Fatalf("expected 1 queued ack, got %+v", acks)
	}
}

func TestOverview_WorktreeRenameRepaintsSharedCatalog(t *testing.T) {
	path := workspaceinventory.CanonicalPath(t.TempDir())
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "sidecar", Path: path}}
	key := projectKey(m.projects[0])
	workspace := workspaceinventory.Workspace{
		ID: key + ":worktree:" + path, ProjectKey: key, ProjectName: "sidecar",
		Kind: workspaceinventory.KindWorktree, Path: path, Key: path, Name: "panes",
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{workspace}}
	m.syncBoard()

	m.handleUIRequest(uirequest.Request{
		Action: uirequest.ActionRenameWorktree,
		Origin: uirequest.Origin{WorkDir: path},
		Target: uirequest.Target{Kind: uirequest.TargetKindWorktree, Value: "pane handle polish"},
	})
	if got := m.results[key].Workspaces[0].Name; got != "pane handle polish" {
		t.Fatalf("result name = %q", got)
	}
	if got := m.catalog[workspace.ID].Name; got != "pane handle polish" {
		t.Fatalf("catalog name = %q", got)
	}
}

func TestOverview_ShellRenameRepaintsSharedCatalog(t *testing.T) {
	path := workspaceinventory.CanonicalPath(t.TempDir())
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "sidecar", Path: path}}
	key := projectKey(m.projects[0])
	workspace := workspaceinventory.Workspace{
		ID: key + ":shell:sidecar-sh-sidecar-1", ProjectKey: key, ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-sidecar-1", Name: "stale shell",
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{workspace}}
	m.syncBoard()

	m.handleUIRequest(uirequest.Request{
		Action: uirequest.ActionRenameShell,
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target: uirequest.Target{Kind: uirequest.TargetKindShell, Value: "active task context"},
	})
	if got := m.results[key].Workspaces[0].Name; got != "active task context" {
		t.Fatalf("result name = %q", got)
	}
	if got := m.catalog[workspace.ID].Name; got != "active task context" {
		t.Fatalf("catalog name = %q", got)
	}
}

func TestOverview_CreateShellSelectsAndAcks(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	path := workspaceinventory.CanonicalPath(t.TempDir())
	writeRegisteredSlug(t, stateHome, "demo", path)
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "Demo", Path: path}}
	key := projectKey(m.projects[0])
	existing := workspaceinventory.Workspace{
		ID: key + ":shell:sidecar-sh-sidecar-1", ProjectKey: key, ProjectName: "Demo",
		Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-sidecar-1", Name: "Shell 1", Path: path,
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{existing}}
	m.syncBoard()

	focus := true
	payload, err := json.Marshal(uirequest.CreatePayload{
		Kind: uirequest.CreateKindShell, Session: "sidecar-sh-sidecar-2", DisplayName: "dev server", Focus: &focus,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := uirequest.Request{
		ID: "req-create-shell", Action: uirequest.ActionCreate, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{ProjectKey: "demo", WorkDir: path},
		Payload: payload,
	}
	_ = m.handleUIRequest(req)

	selected, ok := m.SelectedWorkspace()
	if !ok || selected.TmuxName != "sidecar-sh-sidecar-2" || selected.Name != "dev server" {
		t.Fatalf("selected = %+v ok=%v", selected, ok)
	}
	acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusOpened {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
}

func TestOverview_CreateOriginSlugMatchesProjectCount(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		t.Run(string(rune('0'+n))+"projects", func(t *testing.T) {
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("SIDECAR_ISOLATED_STATE", "1")
			a := workspaceinventory.CanonicalPath(t.TempDir())
			b := workspaceinventory.CanonicalPath(t.TempDir())
			writeRegisteredSlug(t, stateHome, "demo", a)
			m := New(workspaceinventory.Collector{})
			switch n {
			case 1:
				m.projects = []Project{{Name: "Demo", Path: a}}
			case 2:
				m.projects = []Project{{Name: "Demo", Path: a}, {Name: "Other", Path: b}}
			}
			if n > 0 {
				key := projectKey(m.projects[0])
				m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{{
					ID: key + ":shell:sidecar-sh-a-1", ProjectKey: key, ProjectName: "Demo",
					Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-a-1", Name: "Shell 1", Path: a, Live: true,
				}}}
				m.syncBoard()
			}
			focus := true
			payload, err := json.Marshal(uirequest.CreatePayload{
				Kind: uirequest.CreateKindShell, Session: "sidecar-sh-a-2", DisplayName: "peer", Focus: &focus,
			})
			if err != nil {
				t.Fatal(err)
			}
			req := uirequest.Request{
				ID: "req-slug-" + string(rune('0'+n)), Action: uirequest.ActionCreate,
				CreatedAt: time.Now().UTC(), TTLMs: 5000,
				Origin:  uirequest.Origin{ProjectKey: "demo", WorkDir: a},
				Payload: payload,
			}
			_ = m.handleUIRequest(req)
			acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				if len(acks) != 0 {
					t.Fatalf("0 projects should not ack, got %+v", acks)
				}
				return
			}
			selected, ok := m.SelectedWorkspace()
			if !ok || selected.TmuxName != "sidecar-sh-a-2" {
				t.Fatalf("n=%d selected = %+v ok=%v", n, selected, ok)
			}
			if len(acks) != 1 || acks[0].Status != uirequest.StatusOpened {
				t.Fatalf("n=%d acks = %+v", n, acks)
			}
		})
	}
}

func writeRegisteredSlug(t *testing.T, stateHome, slug, path string) {
	t.Helper()
	dir := filepath.Join(stateHome, "sidecar", "projects", slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestOverview_CreateWorktreeSelectsAndAcks(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	path := workspaceinventory.CanonicalPath(t.TempDir())
	wtPath := workspaceinventory.CanonicalPath(t.TempDir())
	other := workspaceinventory.CanonicalPath(t.TempDir())
	writeRegisteredSlug(t, stateHome, "demo", path)
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "Demo", Path: path}, {Name: "Other", Path: other}}
	key := projectKey(m.projects[0])
	existing := workspaceinventory.Workspace{
		ID: key + ":worktree:" + path, ProjectKey: key, ProjectName: "Demo",
		Kind: workspaceinventory.KindWorktree, Path: path, Name: "main", IsMain: true, Live: true,
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{existing}}
	m.syncBoard()

	focus := true
	payload, err := json.Marshal(uirequest.CreatePayload{
		Kind: uirequest.CreateKindWorktree, Session: "sidecar-ws-cli-wt", DisplayName: "cli-wt",
		Focus: &focus, Path: wtPath, Branch: "cli-wt",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := uirequest.Request{
		ID: "req-create-wt", Action: uirequest.ActionCreate, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{ProjectKey: "demo", WorkDir: path},
		Payload: payload,
	}
	_ = m.handleUIRequest(req)
	selected, ok := m.SelectedWorkspace()
	if !ok || selected.Kind != workspaceinventory.KindWorktree || selected.Path != wtPath || selected.Name != "cli-wt" {
		t.Fatalf("selected = %+v ok=%v", selected, ok)
	}
	acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusOpened {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
}

func TestOverview_PendingDiffLastWriteWins(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{
			"ws-1": {ID: "ws-1", TmuxName: "sidecar-sh-sidecar-1"},
		},
	}
	first := uirequest.Request{
		ID: "req-ov-diff-1", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target: uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"},
	}
	second := first
	second.ID = "req-ov-diff-2"
	second.Target.Value = "c:def5678"

	m.handleUIRequest(first)
	m.handleUIRequest(second)
	pv := m.pendingViews["sidecar-sh-sidecar-1"]
	if pv == nil || pv.Target.Value != "c:def5678" {
		t.Fatalf("pending = %+v, want last-write-wins c:def5678", pv)
	}
	if len(m.pendingViews) != 1 {
		t.Fatalf("pending slots = %d, want one", len(m.pendingViews))
	}
}
