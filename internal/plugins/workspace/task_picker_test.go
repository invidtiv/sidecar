package workspace

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestTaskLinkModalStaysBoundedAndRegistersRenderedRows(t *testing.T) {
	for _, size := range []struct{ width, height int }{{60, 24}, {80, 42}, {120, 40}, {200, 50}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			p := &Plugin{width: size.width, height: size.height, linkingWorktree: &Worktree{Name: "feature/界面"}, mouseHandler: mouse.NewHandler()}
			p.taskSearchInput = textinput.New()
			p.taskSearchInput.Prompt = ""
			p.taskSearchFiltered = []Task{
				{ID: "td-one", Title: "A long title with 界面 and emoji 🚀 that must never split or wrap the border"},
				{ID: "td-two", Title: "Second task"},
			}
			p.ensureTaskLinkModal()
			out := p.taskLinkModal.Render(size.width, size.height, p.mouseHandler)
			for lineNo, line := range strings.Split(out, "\n") {
				if got := ansi.StringWidth(line); got > size.width {
					t.Fatalf("line %d width = %d, screen = %d", lineNo, got, size.width)
				}
			}
			plainLines := strings.Split(ansi.Strip(out), "\n")
			borderOnOneLine := false
			for _, line := range plainLines {
				if strings.Contains(line, "┌") && strings.Contains(line, "┐") {
					borderOnOneLine = true
				}
			}
			if !borderOnOneLine {
				t.Fatalf("task search border did not remain on one line:\n%s", ansi.Strip(out))
			}
			for i := range p.taskSearchFiltered {
				id := createIndexedID(taskLinkItemPrefix, i)
				found := false
				for _, region := range p.mouseHandler.HitMap.Regions() {
					if region.ID == id {
						found = region.Rect.H == 1 && region.Rect.W <= size.width
					}
				}
				if !found {
					t.Fatalf("missing bounded hit region %q", id)
				}
			}
		})
	}
}

func TestTaskPickerInputAndListNavigationParity(t *testing.T) {
	tasks := []Task{{ID: "td-jump", Title: "Jump"}, {ID: "td-keep", Title: "Keep"}, {ID: "td-last", Title: "Last"}}

	link := New()
	link.width, link.height = 80, 42
	link.viewMode = ViewModeTaskLink
	link.linkingWorktree = &Worktree{Name: "feature"}
	link.taskSearchInput = textinput.New()
	link.taskSearchInput.Prompt = ""
	link.taskSearchInput.Focus()
	link.taskSearchAll = tasks
	link.taskSearchFiltered = tasks
	link.ensureTaskLinkModal()
	link.taskLinkModal.Render(80, 42, link.mouseHandler)

	for _, r := range []rune{'j', 'k'} {
		link.handleTaskLinkKeys(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := link.taskSearchInput.Value(); got != "jk" {
		t.Fatalf("link input swallowed printable j/k: %q", got)
	}

	link.taskSearchInput.SetValue("")
	link.taskSearchFiltered = tasks
	link.taskSearchIdx = 0
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: tea.KeyDown})
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if link.taskSearchIdx != 1 {
		t.Fatalf("link arrow/ctrl navigation index = %d, want 1", link.taskSearchIdx)
	}
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	link.taskLinkModal.Render(80, 42, link.mouseHandler)
	if link.taskSearchInput.Focused() {
		t.Fatal("link input stayed focused after moving focus to the task list")
	}
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if link.taskSearchIdx != 2 || link.taskSearchInput.Value() != "" {
		t.Fatalf("list-focused j did not navigate: idx=%d query=%q", link.taskSearchIdx, link.taskSearchInput.Value())
	}
	link.handleTaskLinkKeys(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if link.taskSearchIdx != 1 {
		t.Fatalf("list-focused k index = %d, want 1", link.taskSearchIdx)
	}
}

func TestTaskSearchAsyncArrivalPreservesSelectionByID(t *testing.T) {
	p := &Plugin{height: 24, taskSearchIdx: 1, taskSearchFiltered: []Task{{ID: "td-a"}, {ID: "td-b"}}}
	p.taskSearchInput = textinput.New()
	p.update(TaskSearchResultsMsg{Tasks: []Task{{ID: "td-new"}, {ID: "td-b"}, {ID: "td-a"}}})
	if got := p.taskSearchFiltered[p.taskSearchIdx].ID; got != "td-b" {
		t.Fatalf("selected task after arrival = %q, want td-b", got)
	}
}

func TestEnsureListSelectionVisible(t *testing.T) {
	if got := ensureListSelectionVisible(7, 0, 3, 10); got != 5 {
		t.Fatalf("scroll = %d, want 5", got)
	}
	if got := ensureListSelectionVisible(1, 5, 3, 10); got != 1 {
		t.Fatalf("reverse scroll = %d, want 1", got)
	}
}

func TestWorktreeStateLabelsExposeLifecycleAndAvailability(t *testing.T) {
	p := &Plugin{activeLifecycleOperationID: "busy", createPlan: &CreateOperationPlan{Path: "/repo/wt"}}
	wt := &Worktree{
		Path: "/repo/wt", Branch: "feature", IsLocked: true, IsPrunable: true,
		SetupWarning: "hook failed", PRState: "closed",
		Changes: &WorktreeChanges{State: LoadStateTruncated},
	}
	got := strings.Join(p.worktreeStateLabels(wt), " | ")
	for _, want := range []string{"branch feature", "locked · actions unavailable", "prunable · actions unavailable", "operation in progress", "setup warning", "PR closed", "diff truncated"} {
		if !strings.Contains(got, want) {
			t.Errorf("labels %q missing %q", got, want)
		}
	}
	if reason := WorktreeActionRefusal(wt, WorktreeActionMerge); !strings.Contains(reason, "locked") {
		t.Fatalf("merge refusal = %q, want locked", reason)
	}
}
