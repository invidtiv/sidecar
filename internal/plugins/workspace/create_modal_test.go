package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

func renderCreateForm(t *testing.T, p *Plugin) (*modal.Modal, string) {
	t.Helper()
	if p.width == 0 {
		p.width = 80
	}
	if p.height == 0 {
		p.height = 40
	}
	if p.mouseHandler == nil {
		p.mouseHandler = mouse.NewHandler()
	}
	p.ensureCreateModal()
	m := p.createFormModal()
	if m == nil {
		t.Fatal("create form modal is nil")
	}
	view := m.Render(p.width, p.height, p.mouseHandler)
	return m, view
}

func TestValidateAndCreateWorktreeAcceptsSpacedName(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("Auth Refresh")

	if SlugifyWorktreeName(p.createForm.Name()) != "auth-refresh" {
		t.Fatalf("slug = %q, want auth-refresh", SlugifyWorktreeName(p.createForm.Name()))
	}
	if cmd := p.validateAndCreateWorktree(); cmd == nil {
		t.Fatalf("expected create command, error=%q", p.createError)
	}
	if p.createError != "" {
		t.Fatalf("createError = %q, want empty", p.createError)
	}
	if p.createBusyStep == "" {
		t.Fatal("expected busy step after valid submit")
	}
}

func TestValidateAndCreateWorktreeRejectsEmptySlug(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.initCreateModalNamed("???")
	if cmd := p.validateAndCreateWorktree(); cmd != nil {
		t.Fatal("expected no command for empty slug")
	}
	if p.createError == "" {
		t.Fatal("expected error for empty slug")
	}
}

func TestValidateAndCreateWorktreeRequiresName(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.initCreateModalNamed("   ")
	if cmd := p.validateAndCreateWorktree(); cmd != nil {
		t.Fatal("expected no command for blank name")
	}
	if p.createError != "Name is required" {
		t.Fatalf("createError = %q", p.createError)
	}
}

func TestCreateModalOmitsTaskLink(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalBase()
	if p.taskSearchLoading {
		t.Fatal("opening create should not start a task load")
	}
	_, view := renderCreateForm(t, p)
	if strings.Contains(view, "Link Task") {
		t.Fatalf("create modal still shows Link Task:\n%s", view)
	}
	if strings.Contains(view, "Search tasks") {
		t.Fatalf("create modal still shows a task picker:\n%s", view)
	}
	if !strings.Contains(view, "Create Workspace") {
		t.Fatalf("create modal missing title Create Workspace:\n%s", view)
	}
}

func TestOpenCreateModalWithTaskPrefillsNameOnly(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.openCreateModalWithTask("td-abc123", "Add user auth")
	if p.taskSearchLoading {
		t.Fatal("create-from-task should not start a task load")
	}
	want := p.deriveBranchName("td-abc123", "Add user auth")
	if got := p.createForm.Name(); got != want {
		t.Fatalf("prefilled name = %q, want %q", got, want)
	}
	if p.createForm.Kind() != workspacecreate.KindWorktree {
		t.Fatalf("kind = %v, want worktree", p.createForm.Kind())
	}
	if p.createForm.InitialFocusID() != workspacecreate.FieldName {
		t.Fatalf("focus = %q, want %q", p.createForm.InitialFocusID(), workspacecreate.FieldName)
	}
}

func TestCreateModalEnterFromNameSubmits(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("Auth Refresh")
	m, _ := renderCreateForm(t, p)
	m.SetFocus(createNameFieldID)

	cmd := p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter from name should submit, error=%q", p.createError)
	}
	if p.createError != "" {
		t.Fatalf("createError = %q, want empty", p.createError)
	}
}

func TestInitCreateModalLoadsLastAgentAndAutoApprove(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = state.SetLastCreateAgent("")
		_ = state.SetAgentAutoApprove(string(AgentCodex), false)
	})
	if err := state.SetLastCreateAgent(string(AgentCodex)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentCodex), true); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir()}
	p.initCreateModalBase()
	if p.createForm.Agent() != string(AgentCodex) {
		t.Fatalf("agent = %q, want %q", p.createForm.Agent(), AgentCodex)
	}
	if !p.createForm.SkipPerms() {
		t.Fatal("expected persisted auto-approve for last-create agent")
	}
}

func TestCreateAgentChangeReloadsAutoApprove(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetLastCreateAgent(string(AgentClaude)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentClaude), false); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentCodex), true); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{}
	p.initCreateModalBase()
	m, _ := renderCreateForm(t, p)
	m.SetFocus(createAgentFieldID)
	m.Render(p.width, p.height, p.mouseHandler)

	p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyDown})
	if p.createForm.Agent() != string(AgentCodex) {
		t.Fatalf("after Down, agent = %q, want %q", p.createForm.Agent(), AgentCodex)
	}
	if !p.createForm.SkipPerms() {
		t.Fatal("expected codex auto-approve after agent change")
	}
}

func TestCreateSlugHintHiddenWhenEqual(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{}
	p.initCreateModalNamed("auth-refresh")
	_, view := renderCreateForm(t, p)
	if strings.Contains(view, "git:") {
		t.Fatalf("slug hint shown when slug equals name:\n%s", view)
	}

	p.initCreateModalNamed("Auth Refresh")
	_, view = renderCreateForm(t, p)
	if !strings.Contains(view, "git: auth-refresh") {
		t.Fatalf("expected slug hint when display differs from slug:\n%s", view)
	}
}

func TestCreateModalAgentComboChangesAgent(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.SetLastCreateAgent("") })
	if err := state.SetLastCreateAgent(string(AgentClaude)); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("Auth Refresh")
	if p.createForm.Agent() != string(AgentClaude) {
		t.Fatalf("starting agent = %q, want claude", p.createForm.Agent())
	}
	m, _ := renderCreateForm(t, p)
	m.SetFocus(createAgentFieldID)
	m.Render(p.width, p.height, p.mouseHandler)

	p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyDown})
	if p.createForm.Agent() != string(AgentCodex) {
		t.Fatalf("after Down, agent = %q, want %q", p.createForm.Agent(), AgentCodex)
	}

	if cmd := p.validateAndCreateWorktree(); cmd == nil {
		t.Fatalf("expected submit cmd, error=%q", p.createError)
	}
	if got := state.GetLastCreateAgent(); got != string(AgentClaude) {
		t.Fatalf("validate persisted last agent = %q, want claude until create succeeds", got)
	}

	p.finishCreatedWorktree(&CreateOperationPlan{AgentType: AgentType(p.createForm.Agent())}, &Worktree{Name: "auth-refresh", Path: "/tmp/auth-refresh"})
	if got := state.GetLastCreateAgent(); got != string(AgentCodex) {
		t.Fatalf("successful create last agent = %q, want codex", got)
	}
}

func TestCreateModalAgentComboKeepsIncrementalQuery(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetLastCreateAgent(string(AgentClaude)); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{}
	p.initCreateModalBase()
	m, _ := renderCreateForm(t, p)
	m.SetFocus(createAgentFieldID)
	m.Render(p.width, p.height, p.mouseHandler)

	p.handleCreateKeys(tea.KeyPressMsg{Code: 'z', Text: "z"})
	view := p.createFormModal().Render(p.width, p.height, p.mouseHandler)
	if !strings.Contains(view, "z") {
		t.Fatalf("agent combo query missing typed z (prefill must not overwrite incremental search):\n%s", view)
	}
	if p.createForm.Agent() != string(AgentClaude) {
		t.Fatalf("typing a filter should not commit a different agent, got %q", p.createForm.Agent())
	}
}

func TestCreateModalCheckboxEnterSubmitsWithoutToggle(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetLastCreateAgent(string(AgentClaude)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentClaude), false); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("Auth Refresh")
	m, _ := renderCreateForm(t, p)
	m.SetFocus(createSkipPermissionsID)

	cmd := p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil && p.createBusyStep == "" {
		t.Fatal("Enter on auto-approve should submit without flipping")
	}
	if p.createForm.SkipPerms() {
		t.Fatal("Enter on auto-approve must not toggle")
	}

	p.createBusyStep = ""
	p.createPlan = nil
	m.SetFocus(createSkipPermissionsID)
	if cmd := p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}); cmd != nil || p.createBusyStep != "" {
		t.Fatal("Space on auto-approve should not submit")
	}
	if !p.createForm.SkipPerms() {
		t.Fatal("Space on auto-approve should toggle")
	}
}

func TestConfirmCreateEnterOnEnvCheckboxCreatesWithoutToggle(t *testing.T) {
	p := New()
	p.width, p.height = 100, 40
	p.mouseHandler = mouse.NewHandler()
	p.viewMode = ViewModeCreate
	p.createCopyEnv = true
	p.createPlan = &CreateOperationPlan{
		SourceRef: "refs/heads/main", SourceOID: strings.Repeat("a", 40),
		SourceWorktree: "/repo", MainWorktree: "/repo", Path: "/feature/auth",
		Branch: "feature/auth", RemotePolicy: "local", EnvFiles: []string{".env.local"},
		CopyEnv: true,
	}
	p.ensureCreateOperationModal()
	p.createOperationModal.Render(p.width, p.height, p.mouseHandler)
	if got := p.createOperationModal.FocusedID(); got != createCopyEnvID {
		t.Fatalf("initial confirm focus = %q, want env checkbox", got)
	}

	action, _ := p.createOperationModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if action != createConfirmID {
		t.Fatalf("Enter on env checkbox action = %q, want %q", action, createConfirmID)
	}
	if !p.createCopyEnv {
		t.Fatal("Enter on env checkbox must not toggle Copy env")
	}
}

func TestComboExactOrAllFilterShowsAllOnExactValue(t *testing.T) {
	items := []modal.DropdownItem{
		{Label: "main", Value: "main"},
		{Label: "feat", Value: "feat"},
		{Label: "worktree-modal", Value: "worktree-modal"},
	}
	filter := comboExactOrAllFilter(items)
	if !filter("worktree-modal", items[0]) || !filter("worktree-modal", items[1]) || !filter("worktree-modal", items[2]) {
		t.Fatal("exact committed value should show all items")
	}
	if filter("fea", items[0]) {
		t.Fatal("substring fea should not match main")
	}
	if !filter("fea", items[1]) {
		t.Fatal("substring fea should match feat")
	}
}

func TestNOpensSharedCreateFormWorktreeNameFocused(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.viewMode = ViewModeList

	_ = p.handleListKeys(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if p.viewMode != ViewModeCreate {
		t.Fatalf("viewMode = %d, want ViewModeCreate", p.viewMode)
	}
	if p.createForm == nil {
		t.Fatal("n should open workspacecreate.Form")
	}
	if p.createForm.Kind() != workspacecreate.KindWorktree {
		t.Fatalf("kind = %v, want worktree", p.createForm.Kind())
	}
	if p.createForm.InitialFocusID() != workspacecreate.FieldName {
		t.Fatalf("initial focus = %q, want %q", p.createForm.InitialFocusID(), workspacecreate.FieldName)
	}
	m, view := renderCreateForm(t, p)
	if !strings.Contains(view, "Create Workspace") {
		t.Fatalf("missing title Create Workspace:\n%s", view)
	}
	if got := m.FocusedID(); got != workspacecreate.FieldName {
		t.Fatalf("focused = %q, want %q", got, workspacecreate.FieldName)
	}
}

func TestHeaderPlusOpensCreateFormKindFocused(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.viewMode = ViewModeList

	cmd := p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionCreateWorktreeButton},
	})
	if cmd == nil {
		t.Fatal("header [+] should return loadBranches cmd")
	}
	if p.createForm == nil {
		t.Fatal("header [+] should open workspacecreate.Form")
	}
	if p.createForm.Kind() != workspacecreate.KindWorktree {
		t.Fatalf("kind = %v, want worktree", p.createForm.Kind())
	}
	if p.createForm.InitialFocusID() != workspacecreate.FieldKind {
		t.Fatalf("initial focus = %q, want %q", p.createForm.InitialFocusID(), workspacecreate.FieldKind)
	}
	m, _ := renderCreateForm(t, p)
	if got := m.FocusedID(); got != workspacecreate.FieldKind {
		t.Fatalf("focused = %q, want kind", got)
	}
}

func TestWorktreesPlusOpensCreateFormNameFocused(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.viewMode = ViewModeList

	_ = p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionWorkspacesPlusButton},
	})
	if p.createForm == nil || p.createForm.InitialFocusID() != workspacecreate.FieldName {
		t.Fatalf("Worktrees [+] focus = %q, want name", p.createForm.InitialFocusID())
	}
}

func TestCreateModalTabCyclesAcrossFieldsWithRenders(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalBase()

	m, _ := renderCreateForm(t, p)
	if got := m.FocusedID(); got != workspacecreate.FieldName {
		t.Fatalf("initial focus = %q, want %s", got, workspacecreate.FieldName)
	}

	wantOrder := []string{
		workspacecreate.FieldBase,
		workspacecreate.FieldAgent,
		workspacecreate.FieldSkip,
		workspacecreate.ActionCreate,
		workspacecreate.ActionCancel,
		workspacecreate.FieldKind,
		workspacecreate.FieldName,
	}

	for i, want := range wantOrder {
		p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyTab})
		m, _ = renderCreateForm(t, p)
		if got := m.FocusedID(); got != want {
			t.Fatalf("after tab %d, focus = %q, want %s", i+1, got, want)
		}
	}
}

func TestShellSubmitFromFormUsesNameAgentSkip(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.SetLastCreateAgent("") })
	if err := state.SetLastCreateAgent(string(AgentClaude)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentClaude), true); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("my-shell")
	p.createForm.SetKind(workspacecreate.KindShell)
	if p.createForm.Agent() != string(AgentClaude) {
		t.Fatalf("agent after kind switch = %q, want claude", p.createForm.Agent())
	}
	if !p.createForm.SkipPerms() {
		t.Fatal("expected skip perms to survive kind switch")
	}

	cmd := p.submitCreateForm()
	if cmd == nil {
		t.Fatal("shell submit returned no command")
	}
	if p.viewMode != ViewModeList {
		t.Fatalf("viewMode = %d, want list after shell submit", p.viewMode)
	}
	if p.createForm != nil {
		t.Fatal("form should be cleared after shell submit")
	}
	if got := state.GetLastCreateAgent(); got != string(AgentClaude) {
		t.Fatalf("shell submit last agent = %q, want claude", got)
	}

	msg := cmd()
	created, ok := msg.(ShellCreatedMsg)
	if !ok {
		t.Fatalf("submit produced %T, want ShellCreatedMsg", msg)
	}
	if !isTmuxInstalled() {
		return
	}
	if created.SessionName != "" {
		t.Cleanup(func() {
			_ = exec.Command("tmux", "kill-session", "-t", created.SessionName).Run()
		})
	}
	if created.Err != nil {
		t.Fatalf("shell creation failed: %v", created.Err)
	}
	if created.DisplayName != "my-shell" {
		t.Fatalf("shell name = %q, want my-shell", created.DisplayName)
	}
	if created.AgentType != AgentClaude {
		t.Fatalf("shell agent = %q, want claude", created.AgentType)
	}
	if !created.SkipPerms {
		t.Fatal("shell skip perms not passed through")
	}
}

func TestWorktreeSubmitPlansThenConfirm(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalNamed("Auth Refresh")
	cmd := p.submitCreateForm()
	if cmd == nil {
		t.Fatalf("worktree submit returned no command, error=%q", p.createError)
	}
	if p.createBusyStep == "" {
		t.Fatal("worktree submit should enter plan/confirm, not create immediately")
	}
	if p.viewMode != ViewModeCreate {
		t.Fatalf("viewMode = %d, want ViewModeCreate hosting confirm", p.viewMode)
	}
}

func TestRemovedChooserIdentifiersHaveZeroReferences(t *testing.T) {
	pat := strings.Join([]string{
		"type" + "Selector",
		"ViewMode" + "TypeSelector",
		"ensure" + "TypeSelector",
		"createShell" + "WithAgent",
		"workspace-" + "type-selector",
		"Create New " + "Worktree",
	}, "|")
	re := regexp.MustCompile(pat)
	root := "."
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.FindString(line) == "" {
				continue
			}
			hits = append(hits, filepath.ToSlash(path)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("removed chooser identifiers still referenced:\n%s", strings.Join(hits, "\n"))
	}
}
