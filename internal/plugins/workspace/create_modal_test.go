package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

func TestValidateAndCreateWorktreeAcceptsSpacedName(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalBase()
	p.createNameInput.SetValue("Auth Refresh")

	if SlugifyWorktreeName(p.createNameInput.Value()) != "auth-refresh" {
		t.Fatalf("slug = %q, want auth-refresh", SlugifyWorktreeName(p.createNameInput.Value()))
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
	p.initCreateModalBase()
	p.createNameInput.SetValue("???")
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
	p.initCreateModalBase()
	p.createNameInput.SetValue("   ")
	if cmd := p.validateAndCreateWorktree(); cmd != nil {
		t.Fatal("expected no command for blank name")
	}
	if p.createError != "Name is required" {
		t.Fatalf("createError = %q", p.createError)
	}
}

func TestCreateModalEnterFromNameSubmits(t *testing.T) {
	p := New()
	p.width, p.height = 80, 40
	p.mouseHandler = mouse.NewHandler()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}
	p.initCreateModalBase()
	p.createNameInput.SetValue("Auth Refresh")
	p.ensureCreateModal()
	p.createModal.Render(80, 40, p.mouseHandler)
	p.createModal.SetFocus(createNameFieldID)

	cmd := p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter from name should submit, error=%q", p.createError)
	}
	if p.createError != "" {
		t.Fatalf("createError = %q", p.createError)
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
	if p.createAgentType != AgentCodex {
		t.Fatalf("createAgentType = %q, want %q", p.createAgentType, AgentCodex)
	}
	if !p.createSkipPermissions {
		t.Fatal("expected persisted auto-approve for last-create agent")
	}
}

func TestCreateAgentChangeReloadsAutoApprove(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentClaude), false); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentCodex), true); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{}
	p.createAgentType = AgentClaude
	p.createAgentIdx = p.agentTypeIndex(AgentClaude)
	p.createSkipPermissions = false

	p.createAgentType = AgentCodex
	p.loadCreateAutoApprove()
	if !p.createSkipPermissions {
		t.Fatal("expected codex auto-approve after agent change")
	}
}

func TestCreateSlugHintHiddenWhenEqual(t *testing.T) {
	p := New()
	p.createNameInput.SetValue("auth-refresh")
	sec := p.createSlugHintSection()
	got := sec.Render(40, "", "")
	if got.Content != "" {
		t.Fatalf("slug hint = %q, want empty when slug equals name", got.Content)
	}

	p.createNameInput.SetValue("Auth Refresh")
	got = sec.Render(40, "", "")
	if got.Content == "" {
		t.Fatal("expected slug hint when display differs from slug")
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
