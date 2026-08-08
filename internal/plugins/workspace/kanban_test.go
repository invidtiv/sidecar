package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestKanbanSemanticParityMatrix(t *testing.T) {
	supported := func(state agentactivity.State, seen bool) *Agent {
		return &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: state, Seen: seen}}
	}
	tests := []struct {
		name string
		wt   *Worktree
		want kanbanLane
	}{
		{"supported working", &Worktree{Status: StatusWaiting, Agent: supported(agentactivity.StateWorking, false)}, kanbanLaneWorking},
		{"supported blocked", &Worktree{Status: StatusActive, Agent: supported(agentactivity.StateBlocked, false)}, kanbanLaneBlocked},
		{"supported unseen idle is done", &Worktree{Status: StatusWaiting, Agent: supported(agentactivity.StateIdle, false)}, kanbanLaneDone},
		{"supported seen idle", &Worktree{Status: StatusWaiting, Agent: supported(agentactivity.StateIdle, true)}, kanbanLaneIdle},
		{"supported unknown", &Worktree{Status: StatusActive, Agent: supported(agentactivity.StateUnknown, false)}, kanbanLanePaused},
		{"orphan health wins", &Worktree{Status: StatusActive, IsOrphaned: true, Agent: supported(agentactivity.StateWorking, false)}, kanbanLanePaused},
		{"missing health wins", &Worktree{Status: StatusActive, IsMissing: true, Agent: supported(agentactivity.StateWorking, false)}, kanbanLanePaused},
		{"error health wins", &Worktree{Status: StatusError, Agent: supported(agentactivity.StateWorking, false)}, kanbanLanePaused},
		{"unsupported active fallback", &Worktree{Status: StatusActive, Agent: &Agent{Type: AgentCustom}}, kanbanLaneWorking},
		{"unsupported waiting fallback", &Worktree{Status: StatusWaiting, Agent: &Agent{Type: AgentCustom}}, kanbanLaneBlocked},
		{"unsupported done fallback", &Worktree{Status: StatusDone, Agent: &Agent{Type: AgentCustom}}, kanbanLaneDone},
		{"no agent paused fallback", &Worktree{Status: StatusPaused}, kanbanLanePaused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kanbanLaneForWorktree(tt.wt); got != tt.want {
				t.Fatalf("lane = %v, want %v", got, tt.want)
			}
		})
	}
	for _, provider := range []AgentType{AgentCodex, AgentClaude, AgentGrok, AgentAntigravity, AgentPi, AgentCopilot, AgentCursor, AgentOpenCode, AgentAmp} {
		t.Run("supported provider "+string(provider), func(t *testing.T) {
			wt := &Worktree{Status: StatusActive, Agent: &Agent{
				Type: provider, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked},
			}}
			if got := kanbanLaneForWorktree(wt); got != kanbanLaneBlocked {
				t.Fatalf("lane = %v, want blocked", got)
			}
		})
	}
}

func TestKanbanCardsReuseActivityPresentationWithHealthPriority(t *testing.T) {
	p := &Plugin{}
	agent := &Agent{Type: AgentClaude, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	wt := &Worktree{Name: "feature", Status: StatusActive, Agent: agent}
	shell := &ShellSession{Name: "review", ChosenAgent: AgentClaude, Agent: agent}

	if got := p.renderKanbanCardLine(wt, 0, 24, false) + p.renderKanbanCardLine(wt, 1, 24, false); !strings.Contains(got, "◆") || !strings.Contains(got, "blocked") {
		t.Fatalf("worktree card lacks activity parity: %q", got)
	}
	if got := p.renderKanbanShellCardLine(shell, 0, 24, false) + p.renderKanbanShellCardLine(shell, 1, 24, false); !strings.Contains(got, "◆") || !strings.Contains(got, "blocked") {
		t.Fatalf("shell card lacks activity parity: %q", got)
	}

	shell.IsOrphaned = true
	if got := p.renderKanbanShellCardLine(shell, 0, 24, false) + p.renderKanbanShellCardLine(shell, 1, 24, false); !strings.Contains(got, "◌") || !strings.Contains(got, "offline") || strings.Contains(got, "blocked") {
		t.Fatalf("shell health did not override activity: %q", got)
	}
}

func TestKanbanHealthPresentationOverridesStaleActivity(t *testing.T) {
	for _, state := range []agentactivity.State{agentactivity.StateWorking, agentactivity.StateBlocked, agentactivity.StateIdle} {
		t.Run(string(state), func(t *testing.T) {
			wt := &Worktree{Name: "broken", Status: StatusError, Agent: &Agent{
				Type: AgentCodex, Activity: agentactivity.Tracker{State: state},
			}}
			presentation := kanbanPresentationForWorktree(wt)
			if presentation.lane != kanbanLanePaused || !presentation.health {
				t.Fatalf("presentation = %#v, want paused health", presentation)
			}
			p := &Plugin{}
			got := p.renderKanbanCardLine(wt, 0, 24, false) + p.renderKanbanCardLine(wt, 1, 24, false)
			if !strings.Contains(got, "✗") || !strings.Contains(got, "error") {
				t.Fatalf("error health absent from card: %q", got)
			}
			for _, stale := range []string{"working", "blocked", "done", "idle"} {
				if strings.Contains(got, stale) {
					t.Fatalf("card leaked stale %q activity: %q", stale, got)
				}
			}
		})
	}
	for _, tt := range []struct {
		name, want string
		wt         *Worktree
	}{
		{"missing", "folder missing", &Worktree{Name: "missing", Status: StatusActive, IsMissing: true}},
		{"orphaned", "session ended", &Worktree{Name: "orphaned", Status: StatusActive, IsOrphaned: true}},
		{"paused", "paused", &Worktree{Name: "paused", Status: StatusPaused, Agent: &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			presentation := kanbanPresentationForWorktree(tt.wt)
			got := (&Plugin{}).renderKanbanCardLine(tt.wt, 1, 24, false)
			if presentation.lane != kanbanLanePaused || !presentation.health || !strings.Contains(got, tt.want) || strings.Contains(got, "working") {
				t.Fatalf("presentation=%#v card=%q", presentation, got)
			}
		})
	}
}

func TestKanbanDeterministicSemanticFixture(t *testing.T) {
	agent := func(provider AgentType, state agentactivity.State, seen bool) *Agent {
		return &Agent{Type: provider, Activity: agentactivity.Tracker{State: state, Seen: seen}}
	}
	p := &Plugin{
		mouseHandler: mouse.NewHandler(),
		worktrees: []*Worktree{
			{Name: "working-wt", Status: StatusActive, Agent: agent(AgentCodex, agentactivity.StateWorking, false)},
			{Name: "blocked-wt", Status: StatusWaiting, Agent: agent(AgentClaude, agentactivity.StateBlocked, false)},
			{Name: "done-wt", Status: StatusWaiting, Agent: agent(AgentGrok, agentactivity.StateIdle, false)},
			{Name: "idle-wt", Status: StatusWaiting, Agent: agent(AgentAntigravity, agentactivity.StateIdle, true)},
			{Name: "error-wt", Status: StatusError, Agent: agent(AgentCodex, agentactivity.StateWorking, false)},
		},
		shells: []*ShellSession{
			{Name: "working-shell", ChosenAgent: AgentCodex, Agent: agent(AgentCodex, agentactivity.StateWorking, false)},
			{Name: "blocked-shell", ChosenAgent: AgentClaude, Agent: agent(AgentClaude, agentactivity.StateBlocked, false)},
			{Name: "done-shell", ChosenAgent: AgentGrok, Agent: agent(AgentGrok, agentactivity.StateIdle, false)},
			{Name: "idle-shell", ChosenAgent: AgentAntigravity, Agent: agent(AgentAntigravity, agentactivity.StateIdle, true)},
		},
	}
	got := p.renderKanbanView(200, 50)
	for _, want := range []string{
		"Working (1)", "Blocked (1)", "Done (1)", "Idle (1)", "Paused (1)",
		"working-wt", "blocked-wt", "done-wt", "idle-wt", "error-wt",
		"working-shell", "blocked-shell", "done-shell", "idle-shell",
		"codex · working", "Claude · blocked", "Grok · done", "Antigravity · idle", "error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fixture lacks %q", want)
		}
	}
	if proofDir := os.Getenv("SIDECAR_KANBAN_PROOF_DIR"); proofDir != "" {
		if err := os.MkdirAll(proofDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proofDir, "kanban-semantic-fixture.txt"), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetKanbanColumns(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusWaiting},
			{Name: "wt3", Status: StatusDone},
			{Name: "wt4", Status: StatusPaused},
			{Name: "wt5", Status: StatusActive},
			{Name: "wt6", Status: StatusError}, // Should be grouped with Paused
		},
	}

	columns := p.getKanbanColumns()

	if len(columns[kanbanLaneWorking]) != 2 {
		t.Errorf("expected 2 working worktrees, got %d", len(columns[kanbanLaneWorking]))
	}
	if len(columns[kanbanLaneBlocked]) != 1 {
		t.Errorf("expected 1 blocked worktree, got %d", len(columns[kanbanLaneBlocked]))
	}
	if len(columns[kanbanLaneDone]) != 1 {
		t.Errorf("expected 1 done worktree, got %d", len(columns[kanbanLaneDone]))
	}
	// Paused should include both StatusPaused and StatusError worktrees
	if len(columns[kanbanLanePaused]) != 2 {
		t.Errorf("expected 2 paused worktrees (1 paused + 1 error), got %d", len(columns[kanbanLanePaused]))
	}
}

func TestGetKanbanColumnsEmpty(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{},
	}

	columns := p.getKanbanColumns()

	for _, lane := range kanbanLaneOrder {
		if len(columns[lane]) != 0 {
			t.Errorf("expected empty column for %v, got %d items", lane, len(columns[lane]))
		}
	}
}

func TestSyncListToKanban(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusWaiting},
			{Name: "wt3", Status: StatusDone},
			{Name: "wt4", Status: StatusPaused},
		},
		selectedIdx: 2, // wt3 (Done)
	}

	p.syncListToKanban()

	if p.kanbanCol != 3 { // Done column (Shells=0, Working=1, Blocked=2, Done=3, Idle=4, Paused=5)
		t.Errorf("expected kanbanCol=3 (Done), got %d", p.kanbanCol)
	}
	if p.kanbanRow != 0 { // First item in Done column
		t.Errorf("expected kanbanRow=0, got %d", p.kanbanRow)
	}
}

func TestSyncListToKanbanWithErrorStatus(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusError}, // Should be in Paused column
		},
		selectedIdx: 1, // wt2 (Error -> Paused)
	}

	p.syncListToKanban()

	if p.kanbanCol != 5 { // Paused column (Shells=0, Active=1, Thinking=2, Waiting=3, Done=4, Paused=5)
		t.Errorf("expected kanbanCol=5 (Paused), got %d", p.kanbanCol)
	}
	if p.kanbanRow != 0 {
		t.Errorf("expected kanbanRow=0, got %d", p.kanbanRow)
	}
}

func TestSyncListToKanbanNoWorktrees(t *testing.T) {
	p := &Plugin{
		worktrees:   []*Worktree{},
		selectedIdx: 0,
		kanbanCol:   2,
		kanbanRow:   5,
	}

	p.syncListToKanban()

	if p.kanbanCol != 0 {
		t.Errorf("expected kanbanCol=0, got %d", p.kanbanCol)
	}
	if p.kanbanRow != 0 {
		t.Errorf("expected kanbanRow=0, got %d", p.kanbanRow)
	}
}

func TestSyncKanbanToList(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusWaiting},
			{Name: "wt3", Status: StatusDone},
			{Name: "wt4", Status: StatusPaused},
		},
		kanbanCol:   2, // Blocked column
		kanbanRow:   0, // First item (wt2)
		selectedIdx: 0,
	}

	p.syncKanbanToList()

	if p.selectedIdx != 1 { // wt2 is at index 1
		t.Errorf("expected selectedIdx=1, got %d", p.selectedIdx)
	}
}

func TestSyncKanbanToListEmptyColumn(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
		},
		kanbanCol:   2, // Blocked column (empty)
		kanbanRow:   0,
		selectedIdx: 0,
	}

	p.syncKanbanToList()

	// selectedIdx should remain unchanged since column is empty
	if p.selectedIdx != 0 {
		t.Errorf("expected selectedIdx=0 (unchanged), got %d", p.selectedIdx)
	}
}

func TestMoveKanbanColumn(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusWaiting},
		},
		kanbanCol: 0, // Start at Shells
		kanbanRow: 0,
	}

	// Move right
	p.moveKanbanColumn(1)
	if p.kanbanCol != 1 {
		t.Errorf("expected kanbanCol=1 after move right, got %d", p.kanbanCol)
	}

	// Move left
	p.moveKanbanColumn(-1)
	if p.kanbanCol != 0 {
		t.Errorf("expected kanbanCol=0 after move left, got %d", p.kanbanCol)
	}

	// Move left at boundary (should stay at 0)
	p.moveKanbanColumn(-1)
	if p.kanbanCol != 0 {
		t.Errorf("expected kanbanCol=0 at left boundary, got %d", p.kanbanCol)
	}

	// Move to far right
	p.kanbanCol = kanbanColumnCount() - 1
	p.moveKanbanColumn(1)
	if p.kanbanCol != kanbanColumnCount()-1 {
		t.Errorf("expected kanbanCol=%d at right boundary, got %d", kanbanColumnCount()-1, p.kanbanCol)
	}
}

func TestMoveKanbanRow(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusActive},
			{Name: "wt3", Status: StatusActive},
		},
		kanbanCol: 1, // Active column (has 3 items)
		kanbanRow: 0,
	}

	// Move down
	p.moveKanbanRow(1)
	if p.kanbanRow != 1 {
		t.Errorf("expected kanbanRow=1 after move down, got %d", p.kanbanRow)
	}

	// Move down again
	p.moveKanbanRow(1)
	if p.kanbanRow != 2 {
		t.Errorf("expected kanbanRow=2 after move down, got %d", p.kanbanRow)
	}

	// Move down at boundary (should stay at 2)
	p.moveKanbanRow(1)
	if p.kanbanRow != 2 {
		t.Errorf("expected kanbanRow=2 at bottom boundary, got %d", p.kanbanRow)
	}

	// Move up
	p.moveKanbanRow(-1)
	if p.kanbanRow != 1 {
		t.Errorf("expected kanbanRow=1 after move up, got %d", p.kanbanRow)
	}

	// Move up to top
	p.kanbanRow = 0
	p.moveKanbanRow(-1)
	if p.kanbanRow != 0 {
		t.Errorf("expected kanbanRow=0 at top boundary, got %d", p.kanbanRow)
	}
}

func TestMoveKanbanRowEmptyColumn(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
		},
		kanbanCol: 2, // Blocked column (empty)
		kanbanRow: 0,
	}

	// Move should have no effect on empty column
	p.moveKanbanRow(1)
	if p.kanbanRow != 0 {
		t.Errorf("expected kanbanRow=0 after move in empty column, got %d", p.kanbanRow)
	}
}

func TestSelectedKanbanWorktree(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusWaiting},
		},
		kanbanCol: 2, // Blocked column
		kanbanRow: 0,
	}

	wt := p.selectedKanbanWorktree()
	if wt == nil {
		t.Fatal("expected non-nil worktree")
	}
	if wt.Name != "wt2" {
		t.Errorf("expected wt2, got %s", wt.Name)
	}
}

func TestSelectedKanbanWorktreeEmptyColumn(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
		},
		kanbanCol: 2, // Blocked column (empty)
		kanbanRow: 0,
	}

	wt := p.selectedKanbanWorktree()
	if wt != nil {
		t.Errorf("expected nil worktree for empty column, got %s", wt.Name)
	}
}

func TestSelectedKanbanWorktreeInvalidColumn(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
		},
		kanbanCol: -1, // Invalid
		kanbanRow: 0,
	}

	wt := p.selectedKanbanWorktree()
	if wt != nil {
		t.Error("expected nil worktree for invalid column")
	}

	p.kanbanCol = 100 // Too large
	wt = p.selectedKanbanWorktree()
	if wt != nil {
		t.Error("expected nil worktree for out-of-range column")
	}
}

func TestMoveKanbanColumnClampsRow(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{
			{Name: "wt1", Status: StatusActive},
			{Name: "wt2", Status: StatusActive},
			{Name: "wt3", Status: StatusActive},
			{Name: "wt4", Status: StatusWaiting}, // Only 1 item in Blocked
		},
		kanbanCol: 1, // Active column (3 items)
		kanbanRow: 2, // Last item
	}

	// Move to Blocked column (only 1 item)
	p.moveKanbanColumn(1)

	if p.kanbanCol != 2 {
		t.Errorf("expected kanbanCol=2, got %d", p.kanbanCol)
	}
	// Row should be clamped to 0 since Thinking only has 1 item
	if p.kanbanRow != 0 {
		t.Errorf("expected kanbanRow=0 (clamped), got %d", p.kanbanRow)
	}
}

func TestSyncListToKanbanShellSelected(t *testing.T) {
	p := &Plugin{
		shells: []*ShellSession{
			{Name: "Shell 1"},
			{Name: "Shell 2"},
		},
		shellSelected:    true,
		selectedShellIdx: 1,
	}

	p.syncListToKanban()

	if p.kanbanCol != 0 {
		t.Errorf("expected kanbanCol=0 (Shells), got %d", p.kanbanCol)
	}
	if p.kanbanRow != 1 {
		t.Errorf("expected kanbanRow=1, got %d", p.kanbanRow)
	}
}

func TestSyncKanbanToListShells(t *testing.T) {
	p := &Plugin{
		shells: []*ShellSession{
			{Name: "Shell 1"},
		},
		kanbanCol: 0,
		kanbanRow: 0,
	}

	p.syncKanbanToList()

	if !p.shellSelected {
		t.Errorf("expected shellSelected=true, got false")
	}
	if p.selectedShellIdx != 0 {
		t.Errorf("expected selectedShellIdx=0, got %d", p.selectedShellIdx)
	}
}
