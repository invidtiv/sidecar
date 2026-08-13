package workspacediff

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

// TaskMsg is a completed `td show --json` for one worktree.
type TaskMsg struct {
	WorkspaceID string
	TaskID      string
	Task        *Task
	Err         error
}

// LoadTaskCmd fetches task details from td.
func LoadTaskCmd(workDir, taskID, workspaceID string) tea.Cmd {
	if taskID == "" {
		return nil
	}
	return func() tea.Msg {
		task, err := LoadTask(context.Background(), workDir, taskID)
		return TaskMsg{WorkspaceID: workspaceID, TaskID: taskID, Task: task, Err: err}
	}
}

// LoadTask runs `td show --json` in workDir.
func LoadTask(ctx context.Context, workDir, taskID string) (*Task, error) {
	cmd := exec.CommandContext(ctx, "td", "show", taskID, "--json")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("td show: %w", err)
	}
	var details struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Priority    string `json:"priority"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Acceptance  string `json:"acceptance"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	if err := json.Unmarshal(output, &details); err != nil {
		return nil, fmt.Errorf("parse task json: %w", err)
	}
	return &Task{
		ID: details.ID, Title: details.Title, Status: details.Status,
		Priority: details.Priority, Type: details.Type,
		Description: details.Description, Acceptance: details.Acceptance,
		CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
	}, nil
}

// TaskRenderOpts controls the empty-state copy. Global must not offer "t".
type TaskRenderOpts struct {
	// EmptyHint is shown under "No linked task" when TaskID is empty.
	// Leave empty on global (that key is not offered).
	EmptyHint string
	Width     int
	Height    int
	Offset    int
}

// RenderTask draws the linked task, or an empty/loading state.
// Returns the view and the unclipped line count (for scroll clamping).
func RenderTask(tv TaskView, opts TaskRenderOpts) (string, int) {
	if tv.TaskID == "" {
		body := "No linked task"
		if opts.EmptyHint != "" {
			body += "\n" + opts.EmptyHint
		}
		return dimText(body), 2
	}
	if tv.Loading || tv.Task == nil {
		return dimText(fmt.Sprintf("Loading task %s...", tv.TaskID)), 1
	}
	if tv.Error != "" {
		return styles.StatusDeleted.Render("Error loading task") + "\n" + dimText(tv.Error), 2
	}

	task := tv.Task
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Task: %s", task.ID)))
	statusLine := fmt.Sprintf("Status: %s", task.Status)
	if task.Priority != "" {
		statusLine += fmt.Sprintf("  Priority: %s", task.Priority)
	}
	if task.Type != "" {
		statusLine += fmt.Sprintf("  Type: %s", task.Type)
	}
	lines = append(lines, statusLine)
	lines = append(lines, strings.Repeat("─", min(opts.Width-4, 60)))
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(task.Title))
	lines = append(lines, "")

	if task.Description != "" {
		lines = append(lines, wrapText(task.Description, opts.Width-4))
		lines = append(lines, "")
	}
	if task.Acceptance != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Acceptance Criteria:"))
		lines = append(lines, wrapText(task.Acceptance, opts.Width-4))
		lines = append(lines, "")
	}
	lines = append(lines, "")
	if task.CreatedAt != "" {
		lines = append(lines, dimText(fmt.Sprintf("Created: %s", task.CreatedAt)))
	}
	if task.UpdatedAt != "" {
		lines = append(lines, dimText(fmt.Sprintf("Updated: %s", task.UpdatedAt)))
	}

	lineCount := len(lines)
	offset := opts.Offset
	if offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	} else if offset >= len(lines) {
		if len(lines) > opts.Height && opts.Height > 0 {
			lines = lines[len(lines)-opts.Height:]
		}
	}
	if opts.Height > 0 && len(lines) > opts.Height {
		lines = lines[:opts.Height]
	}
	return strings.Join(lines, "\n"), lineCount
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if len(para) <= width {
			lines = append(lines, para)
			continue
		}
		words := strings.Fields(para)
		var currentLine string
		for _, word := range words {
			if currentLine == "" {
				currentLine = word
			} else if len(currentLine)+1+len(word) <= width {
				currentLine += " " + word
			} else {
				lines = append(lines, currentLine)
				currentLine = word
			}
		}
		if currentLine != "" {
			lines = append(lines, currentLine)
		}
	}
	return strings.Join(lines, "\n")
}
